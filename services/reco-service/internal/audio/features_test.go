package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os/exec"
	"strings"
	"testing"
)

func sine(freq float64, sec float64, amp float64) []float32 {
	n := int(sec * SampleRate)
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/SampleRate))
	}
	return out
}

// clicks at bpm: short decaying noise bursts.
func clicks(bpm float64, sec float64) []float32 {
	n := int(sec * SampleRate)
	out := make([]float32, n)
	period := int(60 / bpm * SampleRate)
	rnd := rand.New(rand.NewPCG(1, 2))
	for start := 0; start < n; start += period {
		for i := 0; i < 600 && start+i < n; i++ {
			out[start+i] = float32((rnd.Float64()*2 - 1) * math.Exp(-float64(i)/120))
		}
	}
	return out
}

func TestAnalyzeSine(t *testing.T) {
	f, err := Analyze(sine(440, 10, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	if f.Centroid < 300 || f.Centroid > 800 {
		t.Fatalf("centroid %.0f, want ~440", f.Centroid)
	}
	if f.Key != 9 { // A
		t.Fatalf("key %d, want 9 (A)", f.Key)
	}
	if f.RMS < 0.3 || f.RMS > 0.4 {
		t.Fatalf("rms %.3f, want ~0.354", f.RMS)
	}
	if f.LoudnessDB > -8 || f.LoudnessDB < -10 {
		t.Fatalf("loudness %.2f dB, want ~-9", f.LoudnessDB)
	}
	if len(f.Raw()) != RawDims {
		t.Fatalf("raw dims %d", len(f.Raw()))
	}
}

func TestAnalyzeTempo(t *testing.T) {
	for _, bpm := range []float64{90, 120, 140} {
		f, err := Analyze(clicks(bpm, 30))
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(f.Tempo-bpm) > bpm*0.05 {
			t.Fatalf("tempo %.1f, want %.0f", f.Tempo, bpm)
		}
	}
}

func TestNoiseIsFlatterThanSine(t *testing.T) {
	rnd := rand.New(rand.NewPCG(3, 4))
	noise := make([]float32, SampleRate*5)
	for i := range noise {
		noise[i] = float32(rnd.Float64()*2-1) * 0.3
	}
	fn, err := Analyze(noise)
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := Analyze(sine(440, 5, 0.3))
	if fn.Flatness <= fs.Flatness || fn.ZCR <= fs.ZCR || fn.Centroid <= fs.Centroid {
		t.Fatalf("noise should be flatter/brighter: noise %+v sine %+v", fn, fs)
	}
}

func TestMajorVsMinor(t *testing.T) {
	chord := func(freqs ...float64) []float32 {
		out := make([]float32, SampleRate*6)
		for _, f := range freqs {
			s := sine(f, 6, 0.2)
			for i := range out {
				out[i] += s[i]
			}
		}
		return out
	}
	maj, _ := Analyze(chord(261.63, 329.63, 392.0)) // C E G
	mnr, _ := Analyze(chord(220.0, 261.63, 329.63)) // A C E
	if maj.Valence <= mnr.Valence {
		t.Fatalf("major valence %.2f should exceed minor %.2f", maj.Valence, mnr.Valence)
	}
}

func TestTooShortAndLimits(t *testing.T) {
	if _, err := Analyze(make([]float32, 100)); !errors.Is(err, ErrTooShort) {
		t.Fatalf("want ErrTooShort, got %v", err)
	}
	if Tempo(make([]float64, 3), 43) != 0 {
		t.Fatal("tempo of tiny envelope should be 0")
	}
	long := sine(220, MaxSeconds+5, 0.1)
	if _, err := Analyze(long); err != nil {
		t.Fatal(err)
	}
	if Percentile(nil, 0.5) != 0 || Percentile([]float64{3, 1, 2}, 0.5) != 2 || Percentile([]float64{1, 2}, 2) != 2 {
		t.Fatal("percentile")
	}
	if pearson([]float64{1, 1}, []float64{1, 2}) != 0 {
		t.Fatal("pearson of constant must be 0")
	}
	if std([]float64{1}) != 0 {
		t.Fatal("std of one value")
	}
}

func TestPCMFromF32LE(t *testing.T) {
	var b bytes.Buffer
	for _, v := range []float32{0.5, -0.25, float32(math.NaN())} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	pcm, err := PCMFromF32LE(b.Bytes())
	if err != nil || len(pcm) != 3 || pcm[0] != 0.5 || pcm[1] != -0.25 || pcm[2] != 0 {
		t.Fatalf("pcm %v %v", pcm, err)
	}
	if _, err := PCMFromF32LE([]byte{1, 2, 3}); err == nil {
		t.Fatal("truncated input must fail")
	}
}

func TestFFmpegDecode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// 3 s 440 Hz sine encoded as WAV by ffmpeg itself
	wav, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-f", "wav", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := FFmpeg{}.Decode(context.Background(), bytes.NewReader(wav))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) < SampleRate*2 || len(pcm) > SampleRate*4 {
		t.Fatalf("decoded %d samples", len(pcm))
	}
	f, err := Analyze(pcm)
	if err != nil || f.Key != 9 {
		t.Fatalf("analysis of decoded sine: key %d err %v", f.Key, err)
	}
	_, err = FFmpeg{}.Decode(context.Background(), strings.NewReader("not audio at all"))
	if err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestSpectralFeaturesIgnoreRecordingLevel(t *testing.T) {
	mix := func(amp float64) []float32 {
		a, b := clicks(120, 8), sine(440, 8, amp)
		out := make([]float32, len(b))
		for i := range out {
			out[i] = float32(amp)*a[i] + b[i]
		}
		return out
	}
	loud, err := Analyze(mix(0.5))
	if err != nil {
		t.Fatal(err)
	}
	quiet, _ := Analyze(mix(0.05))
	if loud.LoudnessDB-quiet.LoudnessDB < 15 {
		t.Fatalf("measured level must differ: %.1f vs %.1f dB", loud.LoudnessDB, quiet.LoudnessDB)
	}
	if math.Abs(loud.Flux-quiet.Flux) > 0.02*math.Max(loud.Flux, 1e-9)+1e-6 {
		t.Fatalf("flux depends on level: %v vs %v", loud.Flux, quiet.Flux)
	}
	if math.Abs(loud.Mel[5]-quiet.Mel[5]) > 0.05 {
		t.Fatalf("mel depends on level: %v vs %v", loud.Mel[5], quiet.Mel[5])
	}
}

func TestTempoStaysInRange(t *testing.T) {
	flat := make([]float64, 800)
	for i := range flat {
		flat[i] = float64(i%3) * 1e-9
	}
	rising := make([]float64, 800)
	for i := range rising {
		rising[i] = float64(i)
	}
	for _, env := range [][]float64{flat, rising} {
		if bpm := Tempo(env, SampleRate/HopSize); bpm != 0 && (bpm < 60 || bpm > 200) {
			t.Fatalf("tempo out of range: %v", bpm)
		}
	}
}

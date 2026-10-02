// Package audio extracts content features from decoded PCM in pure Go (ADR 0010):
// ffmpeg decodes any format to mono float32 at 22.05 kHz, the DSP below (gonum
// FFT) turns it into a small, fixed-size descriptor used for content similarity,
// mood tags and wave modes. No Python / native ML runtime is required.
package audio

import (
	"errors"
	"math"
	"math/cmplx"
	"sort"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Analysis parameters.
const (
	SampleRate = 22050
	FrameSize  = 2048
	HopSize    = 512
	MelBands   = 20
	// Version is bumped whenever the descriptor changes; stale rows are re-analysed.
	Version = 2 // v2: spectral features on loudness-normalised audio, tempo clamped
	// MaxSeconds of audio analysed per track (from the start).
	MaxSeconds = 120
)

// Features is the per-track descriptor. Raw() flattens it for storage.
type Features struct {
	Tempo      float64 // BPM (60..200)
	LoudnessDB float64 // mean frame energy in dBFS
	RMS        float64 // mean frame RMS (0..1), the "energy" proxy
	Dynamics   float64 // std of frame loudness in dB
	ZCR        float64 // zero crossings per sample
	Centroid   float64 // spectral centroid, Hz
	Rolloff    float64 // 85% spectral roll-off, Hz
	Flatness   float64 // spectral flatness 0..1 (noise-like → 1)
	Flux       float64 // mean positive spectral flux (onset density)
	Valence    float64 // major-vs-minor key clarity −1..1 (mood proxy)
	KeyClarity float64 // max key-profile correlation 0..1
	Key        int     // 0..11 pitch class of the best key
	Mel        [MelBands]float64
}

// RawDims is len(Features.Raw()).
const RawDims = 11 + MelBands

// Raw returns the flat vector stored in reco.track_features.raw.
func (f Features) Raw() []float32 {
	out := []float32{
		float32(f.Tempo), float32(f.LoudnessDB), float32(f.RMS), float32(f.Dynamics), float32(f.ZCR),
		float32(f.Centroid), float32(f.Rolloff), float32(f.Flatness), float32(f.Flux), float32(f.Valence),
		float32(f.KeyClarity),
	}
	for _, m := range f.Mel {
		out = append(out, float32(m))
	}
	return out
}

// ErrTooShort is returned for clips shorter than ~2 seconds.
var ErrTooShort = errors.New("audio too short to analyse")

// Analyze computes the descriptor of mono PCM samples at SampleRate.
func Analyze(pcm []float32) (Features, error) {
	var f Features
	if len(pcm) < SampleRate*2 {
		return f, ErrTooShort
	}
	if len(pcm) > SampleRate*MaxSeconds {
		pcm = pcm[:SampleRate*MaxSeconds]
	}
	fft := fourier.NewFFT(FrameSize)
	win := hann(FrameSize)
	mel := melFilters(MelBands, FrameSize, SampleRate, 30, 8000)
	nBins := FrameSize/2 + 1
	binHz := float64(SampleRate) / FrameSize

	// Spectral descriptors (flux/onsets, mel, chroma, flatness) are computed on
	// loudness-normalised audio so a hot master and a quiet 78 rpm transfer of
	// similar music look alike; RMS/loudness/dynamics keep the measured level.
	gain := 1.0
	if r := rmsOf(pcm); r > 1e-6 {
		gain = targetRMS / r
	}
	frame := make([]float64, FrameSize)
	mag := make([]float64, nBins)
	prev := make([]float64, nBins)
	coeff := make([]complex128, nBins)
	var chroma [12]float64
	var dbs, flux []float64
	var sumRMS, sumZCR, sumCent, sumRoll, sumFlat, sumEnergy float64
	frames := 0
	for start := 0; start+FrameSize <= len(pcm); start += HopSize {
		var energy float64
		zc := 0
		for i := 0; i < FrameSize; i++ {
			x := float64(pcm[start+i])
			energy += x * x
			frame[i] = x * gain * win[i]
			if i > 0 && (pcm[start+i] >= 0) != (pcm[start+i-1] >= 0) {
				zc++
			}
		}
		energy /= FrameSize
		rms := math.Sqrt(energy)
		sumRMS += rms
		sumEnergy += energy
		sumZCR += float64(zc) / FrameSize
		dbs = append(dbs, 10*math.Log10(energy+1e-10))

		fft.Coefficients(coeff, frame)
		var total, weighted, logSum float64
		for k := range coeff {
			m := cmplx.Abs(coeff[k])
			mag[k] = m
			total += m
			weighted += m * float64(k) * binHz
			logSum += math.Log(m + 1e-10)
		}
		// onset strength: positive log-magnitude difference (spectral flux)
		var fl float64
		for k := range mag {
			if d := math.Log1p(mag[k]) - math.Log1p(prev[k]); d > 0 {
				fl += d
			}
		}
		copy(prev, mag)
		flux = append(flux, fl)
		if total > 1e-6 {
			sumCent += weighted / total
			acc, thr := 0.0, 0.85*total
			for k := range mag {
				acc += mag[k]
				if acc >= thr {
					sumRoll += float64(k) * binHz
					break
				}
			}
			sumFlat += math.Exp(logSum/float64(nBins)) / (total / float64(nBins))
		}
		for b, filt := range mel {
			var e float64
			for _, w := range filt {
				e += w.w * mag[w.k] * mag[w.k]
			}
			f.Mel[b] += math.Log10(e + 1e-8)
		}
		for k := 1; k < nBins; k++ {
			hz := float64(k) * binHz
			if hz < 55 || hz > 4200 {
				continue
			}
			pc := int(math.Round(12*math.Log2(hz/440.0)))%12 + 9 // A=9 relative to C
			chroma[((pc%12)+12)%12] += mag[k] * mag[k]
		}
		frames++
	}
	if frames == 0 {
		return f, ErrTooShort
	}
	n := float64(frames)
	f.RMS = sumRMS / n
	f.ZCR = sumZCR / n
	f.Centroid = sumCent / n
	f.Rolloff = sumRoll / n
	f.Flatness = sumFlat / n
	f.LoudnessDB = 10 * math.Log10(sumEnergy/n+1e-10)
	f.Dynamics = std(dbs)
	for b := range f.Mel {
		f.Mel[b] /= n
	}
	var fsum float64
	for _, v := range flux {
		fsum += v
	}
	f.Flux = fsum / n / float64(nBins)
	f.Tempo = Tempo(flux, float64(SampleRate)/HopSize)
	f.Key, f.Valence, f.KeyClarity = keyMode(chroma)
	return f, nil
}

// Tempo estimates BPM from an onset-strength envelope sampled at rate Hz:
// autocorrelation over 60..200 BPM lags with a log-Gaussian prior around 110 BPM
// (the usual trick to resolve octave errors).
func Tempo(onset []float64, rate float64) float64 {
	if len(onset) < 16 {
		return 0
	}
	x := make([]float64, len(onset))
	mean := 0.0
	for _, v := range onset {
		mean += v
	}
	mean /= float64(len(onset))
	for i, v := range onset {
		x[i] = v - mean
	}
	// smooth (two passes of a [¼ ½ ¼] kernel) so beat periods that fall between
	// two integer lags are not split across them
	for pass := 0; pass < 2; pass++ {
		y := make([]float64, len(x))
		for i := range x {
			l, r := x[max(i-1, 0)], x[min(i+1, len(x)-1)]
			y[i] = 0.25*l + 0.5*x[i] + 0.25*r
		}
		x = y
	}
	best, bestLag := math.Inf(-1), 0
	minLag := int(math.Floor(rate * 60 / 200))
	maxLag := int(math.Ceil(rate * 60 / 60))
	for lag := max(minLag, 1); lag <= maxLag && lag < len(x); lag++ {
		// harmonic enhancement: a true beat period also correlates at 2·lag,
		// which suppresses the "half tempo" octave error
		ac := acAt(x, lag)
		if 2*lag < len(x) {
			ac += 0.5 * acAt(x, 2*lag)
		}
		bpm := 60 * rate / float64(lag)
		prior := math.Exp(-0.5 * math.Pow(math.Log2(bpm/110)/0.9, 2))
		if s := ac * prior; s > best {
			best, bestLag = s, lag
		}
	}
	if bestLag == 0 {
		return 0
	}
	// parabolic interpolation for sub-lag precision, only around a true peak
	// (d < 0) and bounded to half a lag: a flat or rising autocorrelation once
	// produced a near-zero lag and a 1300 BPM "tempo"
	lag := float64(bestLag)
	if bestLag > minLag && bestLag < maxLag && bestLag+1 < len(x) {
		a, b, c := acAt(x, bestLag-1), acAt(x, bestLag), acAt(x, bestLag+1)
		if d := a - 2*b + c; d < 0 {
			lag += math.Max(-0.5, math.Min(0.5, 0.5*(a-c)/d))
		}
	}
	return math.Max(60, math.Min(200, 60*rate/lag))
}

func acAt(x []float64, lag int) float64 {
	var ac float64
	for i := lag; i < len(x); i++ {
		ac += x[i] * x[i-lag]
	}
	return ac / float64(len(x)-lag)
}

// Krumhansl–Kessler key profiles (C major / C minor).
var (
	majorProfile = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	minorProfile = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
)

// keyMode returns the best key, valence (major − minor correlation, −1..1) and clarity.
func keyMode(chroma [12]float64) (key int, valence, clarity float64) {
	bestMaj, bestMin := -2.0, -2.0
	kMaj, kMin := 0, 0
	for k := 0; k < 12; k++ {
		var rot [12]float64
		for i := 0; i < 12; i++ {
			rot[i] = chroma[(i+k)%12]
		}
		if c := pearson(rot[:], majorProfile[:]); c > bestMaj {
			bestMaj, kMaj = c, k
		}
		if c := pearson(rot[:], minorProfile[:]); c > bestMin {
			bestMin, kMin = c, k
		}
	}
	if bestMaj >= bestMin {
		key, clarity = kMaj, bestMaj
	} else {
		key, clarity = kMin, bestMin
	}
	valence = math.Max(-1, math.Min(1, (bestMaj-bestMin)*4))
	return key, valence, math.Max(0, clarity)
}

func pearson(a, b []float64) float64 {
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(len(a))
	mb /= float64(len(b))
	var num, da, db float64
	for i := range a {
		num += (a[i] - ma) * (b[i] - mb)
		da += (a[i] - ma) * (a[i] - ma)
		db += (b[i] - mb) * (b[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

type binWeight struct {
	k int
	w float64
}

func hzToMel(hz float64) float64 { return 2595 * math.Log10(1+hz/700) }
func melToHz(m float64) float64  { return 700 * (math.Pow(10, m/2595) - 1) }

// melFilters builds triangular mel filters as sparse (bin, weight) lists.
func melFilters(bands, n, rate int, lo, hi float64) [][]binWeight {
	ml, mh := hzToMel(lo), hzToMel(hi)
	pts := make([]float64, bands+2)
	for i := range pts {
		pts[i] = melToHz(ml + (mh-ml)*float64(i)/float64(bands+1))
	}
	binHz := float64(rate) / float64(n)
	out := make([][]binWeight, bands)
	for b := 0; b < bands; b++ {
		l, c, r := pts[b], pts[b+1], pts[b+2]
		for k := int(l / binHz); k <= int(r/binHz)+1 && k <= n/2; k++ {
			hz := float64(k) * binHz
			var w float64
			switch {
			case hz >= l && hz <= c:
				w = (hz - l) / (c - l)
			case hz > c && hz <= r:
				w = (r - hz) / (r - c)
			}
			if w > 0 {
				out[b] = append(out[b], binWeight{k, w})
			}
		}
	}
	return out
}

func std(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	var m float64
	for _, x := range v {
		m += x
	}
	m /= float64(len(v))
	var s float64
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

// Percentile returns the p-quantile (0..1) of v (copy-sorted).
func Percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Round(p * float64(len(s)-1)))
	return s[min(max(i, 0), len(s)-1)]
}

// targetRMS is the level (≈ −20 dBFS) spectral features are normalised to.
const targetRMS = 0.1

func rmsOf(pcm []float32) float64 {
	var e float64
	for _, x := range pcm {
		e += float64(x) * float64(x)
	}
	return math.Sqrt(e / float64(len(pcm)))
}

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
)

// Decoder turns an encoded stream (mp3/ogg/flac/...) into mono float32 PCM.
type Decoder interface {
	Decode(ctx context.Context, r io.Reader) ([]float32, error)
}

// FFmpeg decodes with the ffmpeg binary (stdin → f32le stdout). Only the first
// MaxSeconds are decoded, so memory stays bounded (~10 MB per track).
type FFmpeg struct{ Bin string }

// Decode implements Decoder.
func (f FFmpeg) Decode(ctx context.Context, r io.Reader) ([]float32, error) {
	bin := f.Bin
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin, "-nostdin", "-hide_banner", "-loglevel", "error", //nolint:gosec // fixed args
		"-i", "pipe:0", "-t", fmt.Sprint(MaxSeconds), "-vn", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-f", "f32le", "pipe:1")
	cmd.Stdin = r
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return PCMFromF32LE(out.Bytes())
}

// PCMFromF32LE converts little-endian float32 bytes to samples.
func PCMFromF32LE(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, errors.New("pcm: truncated sample")
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			v = 0
		}
		out[i] = v
	}
	return out, nil
}

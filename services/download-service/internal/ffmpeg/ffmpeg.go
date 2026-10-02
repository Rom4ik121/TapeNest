// Package ffmpeg shells out to the ffmpeg binary already required by yt-dlp.
// It trims a local file and grabs one frame. No extra media libraries.
package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FFmpeg is the local binary.
type FFmpeg struct {
	Bin string
}

// New resolves the binary. loc may be empty (PATH), a directory, or the executable.
func New(loc string) (*FFmpeg, error) {
	bin := strings.TrimSpace(loc)
	switch {
	case bin == "":
		p, err := exec.LookPath("ffmpeg")
		if err != nil {
			return nil, fmt.Errorf("ffmpeg: %w", err)
		}
		bin = p
	default:
		if st, err := os.Stat(bin); err == nil && st.IsDir() {
			bin = filepath.Join(bin, "ffmpeg")
		}
		if _, err := os.Stat(bin); err != nil {
			p, err2 := exec.LookPath(bin)
			if err2 != nil {
				return nil, fmt.Errorf("ffmpeg: %w", err)
			}
			bin = p
		}
	}
	return &FFmpeg{Bin: bin}, nil
}

func (f *FFmpeg) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, f.Bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return nil
}

// Poster writes one jpeg frame. A 1s seek is tried first so a black opener is skipped.
func (f *FFmpeg) Poster(ctx context.Context, src, dst string) error {
	err := f.run(ctx, "-y", "-ss", "1", "-i", src, "-frames:v", "1", "-vf", "scale=640:-2", "-q:v", "3", dst)
	if err == nil {
		return nil
	}
	return f.run(ctx, "-y", "-ss", "0", "-i", src, "-frames:v", "1", "-vf", "scale=640:-2", "-q:v", "3", dst)
}

// Trim writes [start, end) to dst. Stream copy is tried first; a non-keyframe
// cut falls back to a libx264/aac reencode, both with the ffmpeg already installed.
func (f *FFmpeg) Trim(ctx context.Context, src, dst string, start, end time.Duration) error {
	ss := fmt.Sprintf("%.3f", start.Seconds())
	to := fmt.Sprintf("%.3f", end.Seconds())
	err := f.run(ctx, "-y", "-i", src, "-ss", ss, "-to", to, "-c", "copy", "-avoid_negative_ts", "make_zero", dst)
	if err == nil {
		if st, statErr := os.Stat(dst); statErr == nil && st.Size() > 0 {
			return nil
		}
	}
	_ = os.Remove(dst)
	return f.run(ctx, "-y", "-i", src, "-ss", ss, "-to", to,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-c:a", "aac", "-movflags", "+faststart", dst)
}

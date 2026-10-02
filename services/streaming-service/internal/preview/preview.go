// Package preview ensures a locally generated HLS clip exists.
// The clip is a test pattern (color bars + tone), not a film.
package preview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Library is a directory with index.m3u8 and seg*.ts.
type Library struct {
	Dir string

	once sync.Once
	err  error
}

// New points at dir.
func New(dir string) *Library { return &Library{Dir: dir} }

// Ensure creates the clip with ffmpeg when index.m3u8 is missing.
func (l *Library) Ensure(ctx context.Context) error {
	l.once.Do(func() { l.err = l.ensure(ctx) })
	return l.err
}

func (l *Library) ensure(ctx context.Context) error {
	if l.Dir == "" {
		return errors.New("preview dir is empty")
	}
	idx := filepath.Join(l.Dir, "index.m3u8")
	if st, err := os.Stat(idx); err == nil && st.Size() > 0 {
		return nil
	}
	if err := os.MkdirAll(l.Dir, 0o750); err != nil {
		return fmt.Errorf("preview dir: %w", err)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("ffmpeg not found and no preview playlist is present")
	}
	// Fixed arguments only: a short generated pattern, never a remote URL.
	cmd := exec.CommandContext(ctx, ffmpeg, //nolint:gosec // argv is a constant test pattern
		"-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=44100",
		"-t", "12",
		"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-profile:v", "main",
		"-c:a", "aac", "-b:a", "64k",
		"-hls_time", "4", "-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(l.Dir, "seg%02d.ts"),
		idx,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg preview: %w (%s)", err, trim(out))
	}
	return nil
}

// Playlist reads index.m3u8.
func (l *Library) Playlist() (string, error) {
	b, err := os.ReadFile(filepath.Join(l.Dir, "index.m3u8"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// OpenSegment opens a segment by base name. Names with a path are rejected.
func (l *Library) OpenSegment(name string) (*os.File, error) {
	if name == "" || name != filepath.Base(name) || filepath.Ext(name) != ".ts" {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(l.Dir, name)) //nolint:gosec // G304: name is a basename with a .ts suffix
	if err != nil {
		return nil, err
	}
	return f, nil
}

func trim(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

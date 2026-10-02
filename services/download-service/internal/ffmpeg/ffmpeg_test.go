package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTrimAndPoster(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ff, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	// 3-second synthetic clip, no network and no extra libraries.
	cmd := exec.CommandContext(context.Background(), ff.Bin,
		"-y", "-f", "lavfi", "-i", "color=c=0x4FB3B3:s=320x180:d=3",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo",
		"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synth: %v %s", err, out)
	}
	dst := filepath.Join(dir, "cut.mp4")
	if err := ff.Trim(context.Background(), src, dst, time.Second, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() == 0 {
		t.Fatalf("trim output: %v", err)
	}
	frame := filepath.Join(dir, "frame.jpg")
	if err := ff.Poster(context.Background(), src, frame); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(frame); err != nil || st.Size() < 100 {
		t.Fatalf("poster: %v size %d", err, st.Size())
	}
}

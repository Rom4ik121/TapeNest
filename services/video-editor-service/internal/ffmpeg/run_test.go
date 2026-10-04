package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

func TestProbeAndExport(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	if _, err := os.Stat(font); err != nil {
		t.Skip("dejavu font missing")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	gen := exec.CommandContext(ctx, "ffmpeg", "-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("gen: %v %s", err, out)
	}
	info, err := Probe(ctx, "ffprobe", src)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasAudio || info.Width != 160 || info.Duration < 1.5 {
		t.Fatalf("probe: %+v", info)
	}
	r := domain.DefaultRecipe(info.Duration)
	r.Clips[0].StartSec = 0.2
	r.Clips[0].EndSec = 1.2
	r.Clips[0].Speed = 1
	r.Texts = []domain.Text{{Text: "Tape", StartSec: 0, EndSec: 0.8, X: 0.5, Y: 0.4, Size: 24, Color: "#FFFFFF"}}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	steps, err := Build(Input{
		Source: src, HasAudio: info.HasAudio, Out: out, WorkDir: work, Font: font,
		Width: 160, Height: 120, Recipe: r,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, "ffmpeg", steps); err != nil {
		t.Fatal(err)
	}
	got, err := Probe(ctx, "ffprobe", out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Duration < 0.7 || got.Duration > 1.4 {
		t.Fatalf("trimmed duration %v", got.Duration)
	}
}

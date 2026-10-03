package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFilterGraphMentionsTimelineTools(t *testing.T) {
	spec := ComposeSpec{
		Clips: []ComposeClip{
			{Path: "a.mp4", InSec: 0, OutSec: 3, Speed: 1, Volume: 1, CropW: 1, CropH: 1},
			{Path: "b.mp4", InSec: 0, OutSec: 3, Speed: 1.5, Volume: 0.4, CropX: 0.1, CropY: 0.1, CropW: 0.8, CropH: 0.8, Rotate: 90, Transition: "wipe", TransitionSec: 0.5},
		},
		Texts: []ComposeText{{Text: "Привет", TextFile: "/tmp/overlay.txt", StartSec: 0.4, EndSec: 2, X: 0.5, Y: 0.8}},
		Music: &ComposeMusic{Path: "a.mp4", InSec: 0, Volume: 0.5, OffsetSec: 0.3},
	}
	graph, err := filterGraph(spec, "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", []bool{true, true}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"xfade=transition=wipeleft", "atempo=1.500", "volume=0.400", "transpose=1", "drawtext=", "amix=", "crop="} {
		if !strings.Contains(graph, want) {
			t.Fatalf("graph missing %q\n%s", want, graph)
		}
	}
	plain := spec
	plain.Clips[1].Transition = "none"
	plain.Texts = nil
	plain.Music = nil
	graph, err = filterGraph(plain, "", []bool{true, false}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(graph, "concat=n=2") || strings.Contains(graph, "xfade=") {
		t.Fatalf("hard cut should concat: %s", graph)
	}
}

func TestComposeRendersTimeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ff, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mk := func(name, color string, seconds int) string {
		t.Helper()
		path := filepath.Join(dir, name)
		cmd := exec.CommandContext(context.Background(), ff.Bin,
			"-y", "-f", "lavfi", "-i", "color=c="+color+":s=320x180:d="+strconv.Itoa(seconds),
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:d="+strconv.Itoa(seconds),
			"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("synth %s: %v %s", name, err, out)
		}
		return path
	}
	a := mk("a.mp4", "0x4FB3B3", 3)
	b := mk("b.mp4", "0xBB3381", 3)
	dst := filepath.Join(dir, "out.mp4")
	err = ff.Compose(context.Background(), ComposeSpec{
		Clips: []ComposeClip{
			{Path: a, InSec: 0, OutSec: 3, Speed: 1, Volume: 1, CropW: 1, CropH: 1},
			{Path: b, InSec: 0, OutSec: 3, Speed: 1, Volume: 0.4, CropX: 0.25, CropY: 0.25, CropW: 0.5, CropH: 0.5, Rotate: 90, Transition: "fade", TransitionSec: 0.5},
		},
		Texts: []ComposeText{{Text: "Привет", StartSec: 0.4, EndSec: 2, X: 0.5, Y: 0.8}},
		Music: &ComposeMusic{Path: a, InSec: 0, Volume: 0.5, OffsetSec: 0.3},
	}, dst)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() < 1000 {
		t.Fatalf("output: %v", err)
	}
	probe := probeBin(ff.Bin)
	out, err := exec.CommandContext(context.Background(), probe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatal(err)
	}
	// 3s + 3s - 0.5s fade.
	if dur < 5.2 || dur > 5.9 {
		t.Fatalf("duration %v", dur)
	}
}

package ffmpeg

import (
	"strings"
	"testing"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

func recipe() domain.Recipe {
	r := domain.DefaultRecipe(8)
	r.Clips[0].EndSec = 4
	r.Clips = append(r.Clips, domain.Clip{
		StartSec: 4, EndSec: 8, Speed: 2, Volume: 0.5, Crop: domain.Crop{X: 0.1, Y: 0.1, W: 0.8, H: 0.8},
		Rotate: 90, Transition: "wipeleft", TransitionSec: 0.5,
	})
	r.Texts = []domain.Text{{Text: "Hi: there", StartSec: 0, EndSec: 2, X: 0.5, Y: 0.2, Size: 36, Color: "#FFFFFF"}}
	r.MusicVolume = 0.3
	return r
}

func TestBuildFilters(t *testing.T) {
	steps, err := Build(Input{
		Source: "/tmp/in.mp4", HasAudio: true, Music: "/tmp/bed.m4a", Out: "/tmp/out.mp4",
		WorkDir: "/tmp/work", Font: "/usr/share/fonts/DejaVuSans.ttf", Width: 320, Height: 240, Recipe: recipe(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 5 {
		t.Fatalf("steps %d", len(steps))
	}
	clip := strings.Join(steps[1].Args, " ")
	if !strings.Contains(clip, "crop=") || !strings.Contains(clip, "transpose=1") || !strings.Contains(clip, "setpts=") {
		t.Fatalf("video chain missing: %s", clip)
	}
	if !strings.Contains(clip, "atempo=") || !strings.Contains(clip, "volume=") {
		t.Fatalf("audio chain missing: %s", clip)
	}
	join := strings.Join(steps[2].Args, " ")
	if !strings.Contains(join, "xfade=transition=wipeleft") || !strings.Contains(join, "offset=") {
		t.Fatalf("xfade missing: %s", join)
	}
	// first clip 4s, transition 0.5 → offset 3.5
	if !strings.Contains(join, "offset=3.5000") {
		t.Fatalf("offset: %s", join)
	}
	text := strings.Join(steps[3].Args, " ")
	if !strings.Contains(text, `text='Hi\: there'`) || !strings.Contains(text, "drawtext=") {
		t.Fatalf("text: %s", text)
	}
	music := strings.Join(steps[4].Args, " ")
	if !strings.Contains(music, "amix=") || !strings.Contains(music, "volume=0.3000") || !strings.Contains(music, "/tmp/out.mp4") {
		t.Fatalf("music: %s", music)
	}
}

func TestHardCutConcatAndSilence(t *testing.T) {
	r := domain.DefaultRecipe(3)
	r.Clips = append(r.Clips, domain.Clip{
		StartSec: 1, EndSec: 2, Speed: 0.25, Volume: 1, Crop: domain.FullCrop(),
		Transition: "none", TransitionSec: 0.4,
	})
	r.MuteSource = true
	steps, err := Build(Input{
		Source: "in.mp4", HasAudio: false, Out: "out.mp4", WorkDir: "/w", Font: "f.ttf", Recipe: r,
	})
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Name != "clip" || !strings.Contains(strings.Join(steps[0].Args, " "), "anullsrc=") {
		t.Fatalf("silence: %v", steps[0].Args)
	}
	if !strings.Contains(strings.Join(steps[0].Args, " "), "volume=0.0000") {
		t.Fatal("mute")
	}
	var concat Step
	for _, s := range steps {
		if s.Name == "concat" {
			concat = s
		}
	}
	joined := strings.Join(concat.Args, " ")
	if !strings.Contains(joined, "-f concat") || !strings.Contains(joined, "out.mp4") {
		t.Fatalf("concat should be the final file: %s", joined)
	}
	body := concat.Files["/w/list.txt"]
	if !strings.Contains(body, "seg-00.mp4") || !strings.Contains(body, "seg-01.mp4") {
		t.Fatalf("list: %s", body)
	}
	if strings.Contains(joined, "atempo=") {
		t.Fatal("atempo belongs on the clip, not the concat")
	}
	slow := strings.Join(steps[0].Args, " ")
	// first clip speed is 1, second is 0.25 — check the second clip step
	slow = strings.Join(steps[0].Args, " ")
	second := ""
	for _, s := range steps {
		if s.Name == "clip" && strings.Contains(strings.Join(s.Args, " "), "atempo=0.5") {
			second = strings.Join(s.Args, " ")
		}
	}
	if strings.Count(second, "atempo=0.5") < 2 {
		t.Fatalf("slow atempo: %s / first %s", second, slow)
	}
}

func TestAtempoBounds(t *testing.T) {
	if got := strings.Join(atempo(4), ","); got != "atempo=2,atempo=2.0000" {
		t.Fatal(got)
	}
	if got := strings.Join(atempo(1), ","); got != "atempo=1.0000" {
		t.Fatal(got)
	}
}

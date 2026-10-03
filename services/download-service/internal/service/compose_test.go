package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/ffmpeg"
)

func TestNormalizeTimeline(t *testing.T) {
	id := uuid.New()
	other := uuid.New()
	clip := func(job uuid.UUID, speed float64, transition string) ProjectClip {
		return ProjectClip{
			JobID: job.String(), InSec: 0, OutSec: 4, Speed: speed, Volume: 1,
			Crop: ProjectCrop{X: 0, Y: 0, W: 1, H: 1}, Transition: transition, TransitionSec: 1,
		}
	}
	plan, err := normalizeProject(Project{
		Title: "  Мой ролик ",
		Clips: []ProjectClip{clip(id, 1, "fade"), clip(other, 1, "fade")},
		Texts: []ProjectText{{Text: " Привет ", StartSec: 0.5, EndSec: 2, X: 0.5, Y: 0.8}},
		Music: &ProjectMusic{JobID: other.String(), InSec: 0, Volume: 0.4, OffsetSec: 0.2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.title != "Мой ролик" || plan.texts[0].Text != "Привет" {
		t.Fatalf("text: %+v", plan)
	}
	if math.Abs(plan.duration-7) > 0.01 || math.Abs(plan.clips[1].join-1) > 0.01 {
		t.Fatalf("duration %v join %v", plan.duration, plan.clips[1].join)
	}
	if _, err := normalizeProject(Project{Title: "x", Clips: []ProjectClip{clip(id, 3, "none")}}); err != ErrBadProject {
		t.Fatalf("speed: %v", err)
	}
	if _, err := normalizeProject(Project{Title: " ", Clips: []ProjectClip{clip(id, 1, "none")}}); err != ErrBadTitle {
		t.Fatalf("title: %v", err)
	}
	wide := Project{Title: "x", Clips: make([]ProjectClip, 0, 9)}
	for i := 0; i < 9; i++ {
		wide.Clips = append(wide.Clips, clip(id, 1, "none"))
	}
	if _, err := normalizeProject(wide); err != ErrBadProject {
		t.Fatalf("too many: %v", err)
	}
}

type recordingEditor struct {
	copyEditor
	spec ffmpeg.ComposeSpec
}

func (e *recordingEditor) Compose(ctx context.Context, spec ffmpeg.ComposeSpec, dst string) error {
	e.spec = spec
	return e.copyEditor.Compose(ctx, spec, dst)
}

func TestComposeLibrary(t *testing.T) {
	e := newEnv(t)
	rec := &recordingEditor{}
	e.api.editor = rec
	ctx := context.Background()
	user := uuid.New()
	other := uuid.New()
	mediaID := uuid.New()
	key := "youtube/2026/10/" + mediaID.String() + ".mp4"
	e.files.Objects[key] = []byte("VIDEODATA")
	e.store.Media[mediaID] = domain.Media{
		ID: mediaID, URLHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		URL: "https://youtu.be/jNQXAC9IVRw", Source: domain.SourceYouTube, Title: "Клип",
		DurationSec: 20, ObjectKey: key, SizeBytes: 9, MimeType: "video/mp4",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	jobID := uuid.New()
	now := time.Now()
	e.store.Jobs[jobID] = domain.Job{
		ID: jobID, UserID: user, URL: "https://youtu.be/jNQXAC9IVRw", Source: domain.SourceYouTube,
		Status: domain.StatusDone, Title: "Клип", MediaID: &mediaID, CreatedAt: now, FinishedAt: &now,
	}
	clip := ProjectClip{
		JobID: jobID.String(), InSec: 0, OutSec: 4, Speed: 1, Volume: 1,
		Crop: ProjectCrop{W: 1, H: 1}, Transition: "none",
	}
	second := clip
	second.OutSec = 4
	second.Speed = 2
	second.Volume = 0.5
	second.Rotate = 90
	second.Crop = ProjectCrop{X: 0.1, Y: 0.1, W: 0.8, H: 0.8}
	second.Transition = "wipe"
	second.TransitionSec = 0.4
	view, err := e.api.Compose(ctx, user, Project{
		Title: "Монтаж",
		Clips: []ProjectClip{clip, second},
		Texts: []ProjectText{{Text: "Привет", StartSec: 0.2, EndSec: 1.5, X: 0.5, Y: 0.2}},
		Music: &ProjectMusic{JobID: jobID.String(), InSec: 0, Volume: 0.4, OffsetSec: 0.2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Job.VisibleTitle() != "Монтаж" || view.File == nil || !view.File.OwnedEdit() || view.File.FormatID != "compose" {
		t.Fatalf("view: %+v file %+v", view.Job, view.File)
	}
	// 4s + 2s - 0.4s wipe.
	if view.File.DurationSec != 6 || view.File.Width != 1280 {
		t.Fatalf("file meta: %+v", view.File)
	}
	if len(rec.spec.Clips) != 2 || rec.spec.Clips[1].Speed != 2 || rec.spec.Clips[1].Rotate != 90 || rec.spec.Music == nil {
		t.Fatalf("spec: %+v", rec.spec)
	}
	if _, ok := e.files.Objects[key]; !ok {
		t.Fatal("source must stay")
	}
	if _, err := e.api.Compose(ctx, other, Project{Title: "чужое", Clips: []ProjectClip{clip}}); err != ErrNotFound {
		t.Fatalf("other: %v", err)
	}
	bad := second
	bad.Speed = 9
	if _, err := e.api.Compose(ctx, user, Project{Title: "нет", Clips: []ProjectClip{bad}}); err != ErrBadProject {
		t.Fatalf("bad speed: %v", err)
	}
	rec.fail = errBoom
	if _, err := e.api.Compose(ctx, user, Project{Title: "сбой", Clips: []ProjectClip{clip}}); err != ErrEditFailed {
		t.Fatalf("fail: %v", err)
	}
	e.api.editor = nil
	if _, err := e.api.Compose(ctx, user, Project{Title: "нет", Clips: []ProjectClip{clip}}); err != ErrEditUnavailable {
		t.Fatalf("off: %v", err)
	}
}

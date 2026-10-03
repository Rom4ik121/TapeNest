package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/ffmpeg"
)

func TestTrimWindow(t *testing.T) {
	if _, _, err := TrimWindow(0, 0.5, 10); err != ErrBadRange {
		t.Fatalf("short: %v", err)
	}
	if _, _, err := TrimWindow(8, 12, 10); err != ErrBadRange {
		t.Fatalf("past end: %v", err)
	}
	if _, _, err := TrimWindow(-1, 4, 10); err != ErrBadRange {
		t.Fatalf("negative: %v", err)
	}
	start, end, err := TrimWindow(1.5, 4, 10)
	if err != nil || start != 1500*time.Millisecond || end != 4*time.Second {
		t.Fatalf("%v %v %v", start, end, err)
	}
	if _, titleErr := cleanTitle("  "); titleErr != ErrBadTitle {
		t.Fatal(titleErr)
	}
	title, err := cleanTitle("  Мой\nролик  ")
	if err != nil || title != "Мой ролик" {
		t.Fatalf("%q %v", title, err)
	}
}

type copyEditor struct{ fail error }

func (e copyEditor) Poster(_ context.Context, _, dst string) error {
	return os.WriteFile(dst, []byte("jpeg"), 0o600)
}

func (e copyEditor) Trim(_ context.Context, src, dst string, _, _ time.Duration) error {
	if e.fail != nil {
		return e.fail
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, append([]byte("cut:"), b...), 0o600)
}

func (e copyEditor) Compose(_ context.Context, spec ffmpeg.ComposeSpec, dst string) error {
	if e.fail != nil {
		return e.fail
	}
	var b []byte
	for _, clip := range spec.Clips {
		raw, err := os.ReadFile(clip.Path)
		if err != nil {
			return err
		}
		b = append(b, raw...)
	}
	return os.WriteFile(dst, append([]byte("edit:"), b...), 0o600)
}

func TestLibraryRenameDeleteTrim(t *testing.T) {
	e := newEnv(t)
	e.api.editor = copyEditor{}
	ctx := context.Background()
	user := uuid.New()
	other := uuid.New()
	mediaID := uuid.New()
	key := "youtube/2026/10/" + mediaID.String() + ".mp4"
	e.files.Objects[key] = []byte("VIDEODATA")
	e.store.Media[mediaID] = domain.Media{
		ID: mediaID, URLHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		URL: "https://youtu.be/jNQXAC9IVRw", Source: domain.SourceYouTube, Title: "Клип",
		DurationSec: 20, ObjectKey: key, SizeBytes: 9, MimeType: "video/mp4",
		Thumbnail: "https://i.ytimg.com/vi/x/hqdefault.jpg", ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	jobID := uuid.New()
	now := time.Now()
	e.store.Jobs[jobID] = domain.Job{
		ID: jobID, UserID: user, URL: "https://youtu.be/jNQXAC9IVRw", Normalized: "https://www.youtube.com/watch?v=jNQXAC9IVRw",
		URLHash: e.store.Media[mediaID].URLHash, Source: domain.SourceYouTube, Status: domain.StatusDone,
		Title: "Клип", MediaID: &mediaID, CreatedAt: now, FinishedAt: &now,
	}

	view, err := e.api.Get(ctx, user, jobID)
	if err != nil || view.PosterURL != "https://i.ytimg.com/vi/x/hqdefault.jpg" {
		t.Fatalf("poster from thumbnail: %+v %v", view.PosterURL, err)
	}
	renamed, err := e.api.Rename(ctx, user, jobID, "  Домашнее видео ")
	if err != nil || renamed.Job.VisibleTitle() != "Домашнее видео" {
		t.Fatalf("rename: %+v %v", renamed.Job, err)
	}
	if _, err := e.api.Rename(ctx, other, jobID, "чужое"); err != ErrNotFound {
		t.Fatalf("other user: %v", err)
	}
	if _, err := e.api.Trim(ctx, user, jobID, 1, 1.2); err != ErrBadRange {
		t.Fatalf("bad range: %v", err)
	}
	cut, err := e.api.Trim(ctx, user, jobID, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	if cut.Job.UserID != user || cut.Job.Status != domain.StatusDone || cut.File == nil || !cut.File.OwnedEdit() {
		t.Fatalf("cut: %+v file %+v", cut.Job, cut.File)
	}
	if cut.File.DurationSec != 6 || cut.File.PosterKey == "" || len(e.files.Objects[cut.File.ObjectKey]) == 0 {
		t.Fatalf("cut file: %+v", cut.File)
	}
	if _, ok := e.files.Objects[key]; !ok {
		t.Fatal("original object must stay")
	}
	if err := e.api.Delete(ctx, user, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.api.Get(ctx, user, jobID); err != ErrNotFound {
		t.Fatalf("hidden: %v", err)
	}
	if _, ok := e.files.Objects[key]; !ok {
		t.Fatal("shared source must survive delete")
	}
	list, err := e.api.List(ctx, user, nil, 20)
	if err != nil || len(list) != 1 || list[0].Job.ID != cut.Job.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := e.api.Delete(ctx, user, cut.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.files.Objects[cut.File.ObjectKey]; ok {
		t.Fatal("edit object should be removed when nobody references it")
	}
	if _, ok := e.files.Objects[cut.File.PosterKey]; ok {
		t.Fatal("poster should be removed with the edit")
	}

	e.api.editor = nil
	e.store.Jobs[jobID] = domain.Job{
		ID: jobID, UserID: user, URL: "https://youtu.be/jNQXAC9IVRw", Status: domain.StatusDone,
		Title: "Клип", MediaID: &mediaID, CreatedAt: now,
	}
	if _, err := e.api.Trim(ctx, user, jobID, 0, 2); err != ErrEditUnavailable {
		t.Fatalf("no ffmpeg: %v", err)
	}
}

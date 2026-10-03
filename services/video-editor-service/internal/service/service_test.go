package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/ffmpeg"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/repo"
)

type srcs struct{ f domain.SourceFile }

func (s srcs) Resolve(context.Context, uuid.UUID, uuid.UUID) (domain.SourceFile, error) {
	if s.f.ObjectKey == "" {
		return domain.SourceFile{}, domain.ErrNotReady
	}
	return s.f, nil
}

type blobs struct {
	objects map[string][]byte
}

func newBlobs() *blobs { return &blobs{objects: map[string][]byte{}} }

func (b *blobs) Download(_ context.Context, bucket, key, path string, max int64) error {
	body, ok := b.objects[bucket+"/"+key]
	if !ok {
		return errors.New("missing object")
	}
	if max > 0 && int64(len(body)) > max {
		return domain.ErrTooLarge
	}
	return os.WriteFile(path, body, 0o600)
}

func (b *blobs) Put(_ context.Context, bucket, key string, r io.Reader, _ int64, _, _ string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.objects[bucket+"/"+key] = body
	return nil
}

func (b *blobs) Presign(context.Context, string, string, string, time.Duration) (string, error) {
	return "https://files.example/edit.mp4", nil
}

type editor struct {
	fail bool
}

func (e editor) Probe(context.Context, string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeResult{Duration: 8, Width: 320, Height: 240, HasAudio: true}, nil
}

func (e editor) Run(_ context.Context, steps []ffmpeg.Step) error {
	if e.fail {
		return errors.New("ffmpeg failed")
	}
	out := steps[len(steps)-1].Args[len(steps[len(steps)-1].Args)-1]
	return os.WriteFile(out, []byte("mp4"), 0o600)
}

func newSvc(t *testing.T, ed editor) (*Service, *repo.Mem, *blobs) {
	t.Helper()
	dir := t.TempDir()
	b := newBlobs()
	b.objects["media/src.mp4"] = []byte("source")
	s := &Service{
		Store: repo.NewMem(), Sources: srcs{f: domain.SourceFile{ObjectKey: "src.mp4", Title: "Demo", DurationSec: 8, Width: 320, Height: 240, MimeType: "video/mp4", SizeBytes: 6}},
		Blobs: b, Editor: ed, MediaBucket: "media", EditBucket: "edits", WorkDir: dir, Font: "font.ttf",
		MaxBytes: 1 << 30, PresignTTL: time.Hour, Now: func() time.Time { return time.Unix(100, 0).UTC() },
	}
	return s, s.Store.(*repo.Mem), b
}

func TestOpenUpdateExport(t *testing.T) {
	svc, _, blobs := newSvc(t, editor{})
	ctx := context.Background()
	user, job := uuid.New(), uuid.New()
	p, err := svc.Open(ctx, user, job)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Open(ctx, user, job)
	if err != nil || again.ID != p.ID {
		t.Fatalf("idempotent: %v %+v", err, again)
	}
	r := p.Recipe
	r.Clips[0].EndSec = 4
	r.Clips = append(r.Clips, domain.Clip{
		StartSec: 4, EndSec: 8, Speed: 1.5, Volume: 0.8, Crop: domain.FullCrop(),
		Rotate: 90, Transition: "fade", TransitionSec: 0.4,
	})
	r.Texts = []domain.Text{{Text: "Hi", StartSec: 0, EndSec: 1, X: 0.5, Y: 0.2, Size: 32, Color: "#FFFFFF"}}
	p, err = svc.UpdateRecipe(ctx, user, p.ID, r)
	if err != nil || len(p.Recipe.Clips) != 2 {
		t.Fatal(err)
	}
	p, err = svc.AttachMusic(ctx, user, p.ID, bytes.NewReader([]byte("audio")), 5)
	if err != nil || p.MusicKey == "" {
		t.Fatal(err)
	}
	exp, err := svc.Export(ctx, user, p.ID)
	if err != nil || exp.Status != domain.StatusQueued {
		t.Fatal(err)
	}
	ok, err := svc.ProcessOne(ctx)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	got, err := svc.ExportOf(ctx, user, exp.ID)
	if err != nil || got.Status != domain.StatusDone || got.OutputKey == "" {
		t.Fatalf("%+v %v", got, err)
	}
	u, err := svc.FileURL(ctx, user, exp.ID)
	if err != nil || u == "" {
		t.Fatal(err, u)
	}
	if _, ok := blobs.objects["edits/"+got.OutputKey]; !ok {
		t.Fatalf("missing upload %s", got.OutputKey)
	}
	if _, err := os.Stat(filepath.Join(svc.WorkDir, "export-gone")); err == nil {
		t.Fatal("temp dir leaked")
	}
	empty, err := svc.ProcessOne(ctx)
	if err != nil || empty {
		t.Fatal(err, empty)
	}
}

func TestExportFailureAndValidation(t *testing.T) {
	svc, _, _ := newSvc(t, editor{fail: true})
	ctx := context.Background()
	p, err := svc.Open(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	bad := p.Recipe
	bad.Clips = append([]domain.Clip(nil), p.Recipe.Clips...)
	bad.Clips[0].Speed = 9
	if _, err := svc.UpdateRecipe(ctx, p.UserID, p.ID, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	exp, err := svc.Export(ctx, p.UserID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Export(ctx, p.UserID, p.ID); !errors.Is(err, domain.ErrBusy) {
		t.Fatal(err)
	}
	ok, err := svc.ProcessOne(ctx)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	got, err := svc.ExportOf(ctx, p.UserID, exp.ID)
	if err != nil || got.Status != domain.StatusFailed || got.Error == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := svc.FileURL(ctx, p.UserID, exp.ID); !errors.Is(err, domain.ErrNotReady) {
		t.Fatal(err)
	}
}

func TestOpenNotReady(t *testing.T) {
	svc, _, _ := newSvc(t, editor{})
	svc.Sources = srcs{}
	if _, err := svc.Open(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, domain.ErrNotReady) {
		t.Fatal(err)
	}
}

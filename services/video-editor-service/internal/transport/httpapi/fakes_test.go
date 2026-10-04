package httpapi

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/ffmpeg"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/repo"
)

func newTestStore() *repo.Mem { return repo.NewMem() }

type srcStub struct{ f domain.SourceFile }

func (s srcStub) Resolve(context.Context, uuid.UUID, uuid.UUID) (domain.SourceFile, error) {
	return s.f, nil
}

type memBlobs struct{ objects map[string][]byte }

func (b *memBlobs) Download(_ context.Context, bucket, key, path string, _ int64) error {
	return os.WriteFile(path, b.objects[bucket+"/"+key], 0o600)
}
func (b *memBlobs) Put(_ context.Context, bucket, key string, r io.Reader, _ int64, _, _ string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.objects[bucket+"/"+key] = body
	return nil
}
func (b *memBlobs) Presign(context.Context, string, string, string, time.Duration) (string, error) {
	return "https://files.example/edit.mp4", nil
}

type okEditor struct{}

func (okEditor) Probe(context.Context, string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeResult{Duration: 5, Width: 100, Height: 80, HasAudio: false}, nil
}
func (okEditor) Run(_ context.Context, steps []ffmpeg.Step) error {
	out := steps[len(steps)-1].Args[len(steps[len(steps)-1].Args)-1]
	return os.WriteFile(out, []byte("x"), 0o600)
}

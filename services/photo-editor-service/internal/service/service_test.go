package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/repo"
)

type blobs struct{ objects map[string][]byte }

func (b *blobs) Put(_ context.Context, bucket, key string, r io.Reader, _ int64, _ string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.objects[bucket+"/"+key] = body
	return nil
}
func (b *blobs) Get(_ context.Context, bucket, key string, max int64) ([]byte, error) {
	body := b.objects[bucket+"/"+key]
	if int64(len(body)) > max {
		return nil, domain.ErrTooLarge
	}
	return body, nil
}
func (b *blobs) Presign(context.Context, string, string, time.Duration) (string, error) {
	return "https://files.example/p.jpg", nil
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 20, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 20; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 30, G: 60, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadListExportDelete(t *testing.T) {
	store := repo.NewMem()
	b := &blobs{objects: map[string][]byte{}}
	svc := &Service{Store: store, Blobs: b, Bucket: "photos", MaxBytes: 1 << 20, PresignTTL: time.Minute}
	ctx := context.Background()
	user := uuid.New()
	p, err := svc.Upload(ctx, user, "  Кухня  ", pngBytes(t))
	if err != nil || p.Width != 20 || p.Title != "Кухня" || p.MimeType != "image/png" {
		t.Fatalf("%+v %v", p, err)
	}
	page, err := svc.List(ctx, user, time.Time{}, uuid.Nil, 10)
	if err != nil || len(page) != 1 {
		t.Fatal(err, len(page))
	}
	u, err := svc.FileURL(ctx, user, p.ID)
	if err != nil || u == "" {
		t.Fatal(err)
	}
	recipe := domain.Identity()
	recipe.Rotate = 90
	recipe.Filter = "vivid"
	recipe.Text.Text = "Hi"
	exp, link, err := svc.Export(ctx, user, p.ID, recipe)
	if err != nil || link == "" || exp.Width != 16 || exp.Height != 20 {
		t.Fatalf("%+v %s %v", exp, link, err)
	}
	if _, err := svc.ExportURL(ctx, user, exp.ID); err != nil {
		t.Fatal(err)
	}
	bad := recipe
	bad.Format = "gif"
	if _, _, err := svc.Export(ctx, user, p.ID, bad); err == nil {
		t.Fatal("gif")
	}
	if err := svc.Delete(ctx, user, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, user, p.ID); err != domain.ErrNotFound {
		t.Fatal(err)
	}
}

func TestRejectsJunk(t *testing.T) {
	svc := &Service{Store: repo.NewMem(), Blobs: &blobs{objects: map[string][]byte{}}, Bucket: "photos", MaxBytes: 32}
	if _, err := svc.Upload(context.Background(), uuid.New(), "", []byte("not an image")); err == nil {
		t.Fatal("expected invalid")
	}
	if _, err := svc.Upload(context.Background(), uuid.New(), "", bytes.Repeat([]byte{1}, 40)); err != domain.ErrTooLarge {
		t.Fatal(err)
	}
}

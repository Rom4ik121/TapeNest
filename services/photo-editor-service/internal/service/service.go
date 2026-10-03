// Package service stores the user's own photos and renders edits.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/render"
)

const maxEdge = 4096

// Store persists photos and exports.
type Store interface {
	InsertPhoto(ctx context.Context, p domain.Photo) error
	GetPhoto(ctx context.Context, user, id uuid.UUID) (domain.Photo, error)
	ListPhotos(ctx context.Context, user uuid.UUID, after time.Time, afterID uuid.UUID, limit int) ([]domain.Photo, error)
	DeletePhoto(ctx context.Context, user, id uuid.UUID) error
	InsertExport(ctx context.Context, e domain.Export) error
	GetExport(ctx context.Context, user, id uuid.UUID) (domain.Export, error)
}

// Blobs is object storage.
type Blobs interface {
	Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, bucket, key string, max int64) ([]byte, error)
	Presign(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// Service implements the photo library and editor.
type Service struct {
	Store      Store
	Blobs      Blobs
	Bucket     string
	MaxBytes   int64
	PresignTTL time.Duration
	Now        func() time.Time
}

// Upload stores a JPEG or PNG the user picked.
func (s *Service) Upload(ctx context.Context, user uuid.UUID, title string, data []byte) (domain.Photo, error) {
	if int64(len(data)) > s.MaxBytes {
		return domain.Photo{}, domain.ErrTooLarge
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return domain.Photo{}, domain.WrapInvalid(errors.New("file must be a jpeg or png"))
	}
	b := img.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 || b.Dx() > maxEdge || b.Dy() > maxEdge {
		return domain.Photo{}, domain.WrapInvalid(errors.New("photo edge must be 1..4096 px"))
	}
	now := s.now()
	id := uuid.New()
	mime := "image/jpeg"
	if format == "png" {
		mime = "image/png"
	}
	p := domain.Photo{
		UserID: user, ID: id, ObjectKey: fmt.Sprintf("photos/%s/%s/original", user, id),
		Title: cleanTitle(title), Width: b.Dx(), Height: b.Dy(), MimeType: mime,
		SizeBytes: int64(len(data)), CreatedAt: now,
	}
	if err := s.Blobs.Put(ctx, s.Bucket, p.ObjectKey, bytes.NewReader(data), p.SizeBytes, mime); err != nil {
		return domain.Photo{}, err
	}
	if err := s.Store.InsertPhoto(ctx, p); err != nil {
		return domain.Photo{}, err
	}
	return p, nil
}

// List returns a page of the user's photos.
func (s *Service) List(ctx context.Context, user uuid.UUID, after time.Time, afterID uuid.UUID, limit int) ([]domain.Photo, error) {
	if limit < 1 || limit > 50 {
		limit = 24
	}
	return s.Store.ListPhotos(ctx, user, after, afterID, limit)
}

// Get returns one photo.
func (s *Service) Get(ctx context.Context, user, id uuid.UUID) (domain.Photo, error) {
	return s.Store.GetPhoto(ctx, user, id)
}

// Delete removes a photo record. The object expires with the bucket lifecycle.
func (s *Service) Delete(ctx context.Context, user, id uuid.UUID) error {
	return s.Store.DeletePhoto(ctx, user, id)
}

// FileURL is a presigned link to the original.
func (s *Service) FileURL(ctx context.Context, user, id uuid.UUID) (string, error) {
	p, err := s.Store.GetPhoto(ctx, user, id)
	if err != nil {
		return "", err
	}
	return s.link(ctx, p.ObjectKey)
}

// Export renders the recipe and returns a presigned link to the result.
func (s *Service) Export(ctx context.Context, user, id uuid.UUID, r domain.Recipe) (domain.Export, string, error) {
	if err := r.Validate(); err != nil {
		return domain.Export{}, "", domain.WrapInvalid(err)
	}
	p, err := s.Store.GetPhoto(ctx, user, id)
	if err != nil {
		return domain.Export{}, "", err
	}
	raw, err := s.Blobs.Get(ctx, s.Bucket, p.ObjectKey, s.MaxBytes)
	if err != nil {
		return domain.Export{}, "", err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return domain.Export{}, "", err
	}
	out, err := render.Apply(img, r)
	if err != nil {
		return domain.Export{}, "", err
	}
	var buf bytes.Buffer
	mime := "image/jpeg"
	if r.Format == "png" {
		mime = "image/png"
		if err := png.Encode(&buf, out); err != nil {
			return domain.Export{}, "", err
		}
	} else if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 90}); err != nil {
		return domain.Export{}, "", err
	}
	now := s.now()
	exportID := uuid.New()
	e := domain.Export{
		UserID: user, ID: exportID, PhotoID: id,
		ObjectKey: fmt.Sprintf("photos/%s/%s.%s", user, exportID, ext(r.Format)),
		MimeType:  mime, Width: out.Bounds().Dx(), Height: out.Bounds().Dy(), CreatedAt: now,
	}
	if err := s.Blobs.Put(ctx, s.Bucket, e.ObjectKey, bytes.NewReader(buf.Bytes()), int64(buf.Len()), mime); err != nil {
		return domain.Export{}, "", err
	}
	if err := s.Store.InsertExport(ctx, e); err != nil {
		return domain.Export{}, "", err
	}
	u, err := s.link(ctx, e.ObjectKey)
	return e, u, err
}

// ExportURL presigns a previous export.
func (s *Service) ExportURL(ctx context.Context, user, id uuid.UUID) (string, error) {
	e, err := s.Store.GetExport(ctx, user, id)
	if err != nil {
		return "", err
	}
	return s.link(ctx, e.ObjectKey)
}

func (s *Service) link(ctx context.Context, key string) (string, error) {
	u, err := s.Blobs.Presign(ctx, s.Bucket, key, s.ttl())
	if err != nil {
		return "", err
	}
	if u == "" {
		return "", errors.New("public file links are not configured")
	}
	return u, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) ttl() time.Duration {
	if s.PresignTTL <= 0 {
		return time.Hour
	}
	return s.PresignTTL
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Фото"
	}
	if utf8.RuneCountInString(s) > 80 {
		r := []rune(s)
		s = string(r[:80])
	}
	return s
}

func ext(format string) string {
	if format == "png" {
		return "png"
	}
	return "jpg"
}

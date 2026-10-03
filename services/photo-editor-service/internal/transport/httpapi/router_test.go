package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/repo"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/service"
)

const token = "internal-token-0123456789"

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
	if body == nil {
		return nil, domain.ErrNotFound
	}
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
	img.SetNRGBA(1, 1, color.NRGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newSrv(t *testing.T, ready map[string]Pinger) (*httptest.Server, *service.Service) {
	t.Helper()
	svc := &service.Service{
		Store: repo.NewMem(), Blobs: &blobs{objects: map[string][]byte{}},
		Bucket: "photos", MaxBytes: 1 << 20, PresignTTL: time.Minute,
	}
	h := NewRouter(Deps{
		API: svc, InternalToken: token, MaxBytes: 1 << 20,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ready: ready,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, svc
}

func do(t *testing.T, srv *httptest.Server, method, path string, user uuid.UUID, body []byte, hdr map[string]string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", token)
	if user != uuid.Nil {
		req.Header.Set("X-User-Id", user.String())
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPhotoFlow(t *testing.T) {
	srv, _ := newSrv(t, map[string]Pinger{"ok": func(context.Context) error { return nil }})
	user := uuid.New()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("title", "Кухня")
	part, err := w.CreateFormFile("file", "a.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pngBytes(t))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/photos", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", user.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload %d %s", resp.StatusCode, b)
	}
	var photo map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&photo); err != nil {
		t.Fatal(err)
	}
	id := photo["id"].(string)
	res := do(t, srv, http.MethodGet, "/api/v1/photos", user, nil, nil)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodGet, "/api/v1/photos/"+id+"/file?redirect=false", user, nil, nil)
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.Contains(raw, []byte("https://")) {
		t.Fatalf("file %d %s", res.StatusCode, raw)
	}
	recipe, _ := json.Marshal(map[string]any{"recipe": domain.Identity()})
	res = do(t, srv, http.MethodPost, "/api/v1/photos/"+id+"/exports", user, recipe, nil)
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.Contains(raw, []byte("url")) {
		t.Fatalf("export %d %s", res.StatusCode, raw)
	}
	var exp map[string]any
	_ = json.Unmarshal(raw, &exp)
	res = do(t, srv, http.MethodGet, "/api/v1/photos/exports/"+exp["id"].(string)+"/file?redirect=false", user, nil, nil)
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("export file %d %s", res.StatusCode, b)
	}
	res = do(t, srv, http.MethodGet, "/readyz", uuid.Nil, nil, map[string]string{"X-Internal-Token": ""})
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodDelete, "/api/v1/photos/"+id, user, nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodGet, "/api/v1/photos/"+id, user, nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatal(res.StatusCode)
	}
}

func TestUnauthorizedAndInvalid(t *testing.T) {
	srv, _ := newSrv(t, map[string]Pinger{
		"minio": func(context.Context) error { return errors.New("down") },
	})
	res := do(t, srv, http.MethodGet, "/api/v1/photos", uuid.New(), nil, map[string]string{"X-Internal-Token": "nope"})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodGet, "/api/v1/photos", uuid.Nil, nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodGet, "/api/v1/photos?cursor=!!!", uuid.New(), nil, nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodPost, "/api/v1/photos/not-a-uuid/exports", uuid.New(), []byte(`{}`), nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatal(res.StatusCode)
	}
	user := uuid.New()
	res = do(t, srv, http.MethodPost, "/api/v1/photos/"+uuid.NewString()+"/exports", user, []byte(`{`), nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodGet, "/readyz", uuid.Nil, nil, nil)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatal(res.StatusCode)
	}
	res = do(t, srv, http.MethodGet, "/healthz", uuid.Nil, nil, nil)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/photos", bytes.NewReader([]byte("nope")))
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", user.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatal(resp.StatusCode)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := encodeCursor(now, id)
	gotT, gotID, err := decodeCursor(c)
	if err != nil || !gotT.Equal(now) || gotID != id {
		t.Fatal(err, gotT, gotID)
	}
	if _, _, err := decodeCursor("@@@"); err == nil {
		t.Fatal("bad cursor")
	}
}

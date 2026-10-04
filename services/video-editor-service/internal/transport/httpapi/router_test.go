package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/service"
)

func TestAudioMagic(t *testing.T) {
	if !audioMagic([]byte("ID3" + string(make([]byte, 20)))) {
		t.Fatal("id3")
	}
	if !audioMagic(append([]byte{0xff, 0xfb}, make([]byte, 20)...)) {
		t.Fatal("mp3")
	}
	wav := append([]byte("RIFF"), make([]byte, 4)...)
	wav = append(wav, []byte("WAVE")...)
	if !audioMagic(wav) {
		t.Fatal("wav")
	}
	if audioMagic([]byte("not audio!!!!")) {
		t.Fatal("reject")
	}
}

func TestProjectFlow(t *testing.T) {
	dir := t.TempDir()
	blobs := &memBlobs{objects: map[string][]byte{"media/src.mp4": []byte("s")}}
	svc := &service.Service{
		Store:   newTestStore(),
		Sources: srcStub{f: domain.SourceFile{ObjectKey: "src.mp4", Title: "Demo", DurationSec: 5, Width: 100, Height: 80, SizeBytes: 1}},
		Blobs:   blobs, Editor: okEditor{}, MediaBucket: "media", EditBucket: "edits",
		WorkDir: dir, Font: "f.ttf", MaxBytes: 1 << 20, PresignTTL: time.Minute,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRouter(Deps{API: svc, InternalToken: "internal-token-0123456789", Log: log, Ready: map[string]Pinger{
		"ok": func(context.Context) error { return nil },
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	user := uuid.New()
	job := uuid.New()
	res := do(t, srv, http.MethodPost, "/api/v1/video/projects", user, []byte(`{"sourceJobId":"`+job.String()+`"}`))
	if res.Code != 200 {
		t.Fatalf("open %d %s", res.Code, res.Body.String())
	}
	var project map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	id := project["id"].(string)
	recipe := project["recipe"].(map[string]any)
	clips := recipe["clips"].([]any)
	clip := clips[0].(map[string]any)
	clip["endSec"] = 3
	raw, _ := json.Marshal(map[string]any{"recipe": recipe})
	res = do(t, srv, http.MethodPut, "/api/v1/video/projects/"+id, user, raw)
	if res.Code != 200 {
		t.Fatalf("save %d %s", res.Code, res.Body.String())
	}
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", "bed.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(append([]byte("ID3"), make([]byte, 16)...))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/video/projects/"+id+"/music", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Internal-Token", "internal-token-0123456789")
	req.Header.Set("X-User-Id", user.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("music %d %s", resp.StatusCode, b)
	}
	res = do(t, srv, http.MethodPost, "/api/v1/video/projects/"+id+"/exports", user, nil)
	if res.Code != http.StatusAccepted {
		t.Fatalf("export %d %s", res.Code, res.Body.String())
	}
	var exp map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &exp)
	eid := exp["id"].(string)
	if _, err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	res = do(t, srv, http.MethodGet, "/api/v1/video/exports/"+eid+"/file?redirect=false", user, nil)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte("https://")) {
		t.Fatalf("file %d %s", res.Code, res.Body.String())
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/readyz", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp)
	}
	_ = resp.Body.Close()
	res = do(t, srv, http.MethodGet, "/api/v1/video/projects/"+id, user, nil)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
}

func TestUnauthorized(t *testing.T) {
	h := NewRouter(Deps{API: &service.Service{}, InternalToken: "internal-token-0123456789", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/video/projects", bytes.NewReader([]byte(`{}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal(resp.StatusCode)
	}
}

type recorded struct {
	Code int
	Body *bytes.Buffer
}

func do(t *testing.T, srv *httptest.Server, method, path string, user uuid.UUID, body []byte) recorded {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "internal-token-0123456789")
	req.Header.Set("X-User-Id", user.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return recorded{Code: resp.StatusCode, Body: bytes.NewBuffer(b)}
}

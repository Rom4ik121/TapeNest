package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

func TestResolve(t *testing.T) {
	user, job := uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" || r.Header.Get("X-User-Id") != user.String() {
			t.Errorf("headers")
		}
		if r.URL.Path != "/internal/v1/downloads/"+job.String()+"/source" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"objectKey":"youtube/a.mp4","title":"Clip","durationSec":12,"width":1280,"height":720,"mimeType":"video/mp4","sizeBytes":99}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	got, err := c.Resolve(context.Background(), user, job)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObjectKey != "youtube/a.mp4" || got.DurationSec != 12 || got.SizeBytes != 99 {
		t.Fatalf("%+v", got)
	}
}

func TestResolveMapsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	_, err := c.Resolve(context.Background(), uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Fatal(err)
	}
}

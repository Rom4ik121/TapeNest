package musicclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
)

func TestRefresh(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" || r.URL.Path != "/internal/v1/catalog/refresh" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	c := New(srv.URL+"/", "tok")
	err := c.Refresh(context.Background(), []core.CatalogFile{{Path: "a/b.mp3", RecordingMBID: uuid.New(), SizeBytes: 3}})
	if err != nil || got == "" {
		t.Fatal(err, got)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	if err := New(bad.URL, "t").Refresh(context.Background(), nil); err == nil {
		t.Fatal("status")
	}
}

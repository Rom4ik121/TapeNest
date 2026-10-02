package torr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddAndStat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"hash":"abc","preloaded_bytes":50,"torrent_size":100,"download_speed":10,"connected_seeders":2}]`))
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if err := c.Add(context.Background(), "magnet:?xt=urn:btih:abc"); err != nil {
		t.Fatal(err)
	}
	st, err := c.StatOf(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if st.Peers != 2 || st.PreloadPct != 50 {
		t.Fatalf("%+v", st)
	}
}

func TestEmptyURL(t *testing.T) {
	c, err := New("  ")
	if err != nil || c != nil {
		t.Fatalf("c=%v err=%v", c, err)
	}
}

func TestDown(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Add(context.Background(), "magnet:?xt=urn:btih:abc"); !errorsIs(err) {
		t.Fatal(err)
	}
}

func errorsIs(err error) bool { return err != nil }

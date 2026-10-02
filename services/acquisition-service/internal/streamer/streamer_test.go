package streamer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
)

type src struct {
	files []qbt.File
	ps    int64
	st    []int
	tor   qbt.Torrent
}

func (s src) Torrent(context.Context, string) (qbt.Torrent, error) { return s.tor, nil }
func (s src) Files(context.Context, string) ([]qbt.File, error)    { return s.files, nil }
func (s src) PieceSize(context.Context, string) (int64, error)     { return s.ps, nil }
func (s src) PieceStates(context.Context, string) ([]int, error)   { return s.st, nil }

func TestStreamer(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "01 - Aria.mp3")
	body := []byte("0123456789abcdefghij")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	s := src{
		files: []qbt.File{{Index: 0, Name: "01 - Aria.mp3", Size: int64(len(body)), Progress: 1}},
		ps:    8,
		st:    []int{2, 2, 2},
		tor:   qbt.Torrent{SavePath: dir},
	}
	l, err := Locate(context.Background(), s, "h", 0)
	if err != nil || l.Size != int64(len(body)) {
		t.Fatal(err, l)
	}
	if Available(s.st, l, 0) != l.Size || Available(s.st, l, l.Size) != 0 {
		t.Fatal(Available(s.st, l, 0))
	}
	if _, err := Locate(context.Background(), s, "h", 9); err == nil {
		t.Fatal("missing file")
	}
	start, end, partial, err := ParseRange("bytes=0-4", 20)
	if err != nil || start != 0 || end != 4 || !partial {
		t.Fatal(start, end, err)
	}
	if _, _, _, err := ParseRange("bytes=100-101", 20); err == nil {
		t.Fatal("bad range")
	}
	if _, _, _, err := ParseRange("bytes=-5", 20); err != nil {
		t.Fatal(err)
	}
	st := New(s)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=0-9")
	rr := httptest.NewRecorder()
	if err := st.Serve(rr, req, "h", 0); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 206 || rr.Body.String() != string(body[:10]) {
		t.Fatalf("%d %q", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodHead, "/", nil)
	rr = httptest.NewRecorder()
	if err := st.Serve(rr, req, "h", 0); err != nil || rr.Code != 200 {
		t.Fatal(err, rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=999-1000")
	rr = httptest.NewRecorder()
	if err := st.Serve(rr, req, "h", 0); !errors.Is(err, ErrRange) {
		t.Fatal(err)
	}
}

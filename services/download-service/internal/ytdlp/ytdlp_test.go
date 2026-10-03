package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

func fake(t *testing.T) *Runner {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-yt-dlp")
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{Bin: bin, JSRuntimes: "deno", FFmpeg: "/usr/bin/ffmpeg"}
}

func TestProbeWritesInfoAndArgs(t *testing.T) {
	r := fake(t)
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	t.Setenv("FAKE_YTDLP_ARGS", argsFile)
	info, err := r.Probe(context.Background(), "https://www.youtube.com/watch?v=abc",
		Opts{Proxy: "http://p:1", CookiesFile: "/c.txt", UserAgent: "UA", MaxFilesize: 42}, filepath.Join(dir, "info.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "abc" || info.Title != "Test clip" || len(info.Formats) != 1 {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, "info.json")); err != nil {
		t.Fatal("info json not written")
	}
	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{
		"--ignore-config", "--no-playlist", "--js-runtimes\ndeno", "--ffmpeg-location\n/usr/bin/ffmpeg",
		"--proxy\nhttp://p:1", "--cookies\n/c.txt", "--user-agent\nUA", "--max-filesize\n42",
		"--socket-timeout\n8", "--retries\n0", "--fragment-retries\n0",
		"--dump-single-json\n--\nhttps://www.youtube.com/watch?v=abc",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "--no-check-certificates") {
		t.Error("must never disable TLS verification")
	}
	if strings.Contains(args, "--socket-timeout\n30") || strings.Contains(args, "--retries\n3") {
		t.Errorf("metadata probe must not use the long download timeouts:\n%s", args)
	}
}

func TestProbeErrors(t *testing.T) {
	r := fake(t)
	dir := t.TempDir()
	t.Setenv("FAKE_YTDLP_MODE", "fail:[youtube] abc: Private video. Sign in if you've been granted access")
	_, err := r.Probe(context.Background(), "u", Opts{}, filepath.Join(dir, "i.json"))
	var ye *Error
	if !errors.As(err, &ye) || ye.Kind != domain.KindPrivate || !strings.HasPrefix(ye.Msg, "ERROR:") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "private") {
		t.Fatal(err.Error())
	}
	t.Setenv("FAKE_YTDLP_MODE", "badjson")
	if _, err := r.Probe(context.Background(), "u", Opts{}, filepath.Join(dir, "i.json")); !errors.As(err, &ye) || ye.Kind != domain.KindInternal {
		t.Fatalf("badjson err = %v", err)
	}
	t.Setenv("FAKE_YTDLP_MODE", "ok")
	if _, err := r.Probe(context.Background(), "u", Opts{}, filepath.Join(dir, "missing", "i.json")); err == nil {
		t.Fatal("expected write error")
	}
	bad := &Runner{Bin: filepath.Join(dir, "nope")}
	if _, err := bad.Probe(context.Background(), "u", Opts{}, filepath.Join(dir, "i.json")); err == nil || errors.As(err, &ye) {
		t.Fatalf("missing binary err = %v", err)
	}
}

func TestProbeCancelled(t *testing.T) {
	r := fake(t)
	t.Setenv("FAKE_YTDLP_MODE", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := r.Probe(ctx, "u", Opts{}, filepath.Join(t.TempDir(), "i.json"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadFileAccumulatesProgress(t *testing.T) {
	r := fake(t)
	dir := t.TempDir()
	var got []Progress
	path, err := r.DownloadFile(context.Background(), "info.json", "137+140", dir, Opts{}, func(p Progress) { got = append(got, p) })
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "abc.mp4") {
		t.Fatalf("path = %s", path)
	}
	if len(got) != 4 {
		t.Fatalf("progress = %+v", got)
	}
	last := got[3]
	if last.DownloadedBytes != 120 || last.TotalBytes != 120 {
		t.Fatalf("merge progress not accumulated: %+v", last)
	}
	if got[0].SpeedBps != 1000 || got[0].EtaSec != 2 {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestDownloadFileErrors(t *testing.T) {
	r := fake(t)
	t.Setenv("FAKE_YTDLP_MODE", "nofile")
	_, err := r.DownloadFile(context.Background(), "i", "18", t.TempDir(), Opts{}, nil)
	var ye *Error
	if !errors.As(err, &ye) || ye.Kind != domain.KindInternal {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("FAKE_YTDLP_MODE", "fail:HTTP Error 429: Too Many Requests")
	if _, err := r.DownloadFile(context.Background(), "i", "18", t.TempDir(), Opts{}, nil); !errors.As(err, &ye) || ye.Kind != domain.KindRateLimited {
		t.Fatalf("err = %v", err)
	}
	bad := &Runner{Bin: "/nonexistent/yt-dlp"}
	if _, err := bad.DownloadFile(context.Background(), "i", "18", t.TempDir(), Opts{}, nil); err == nil {
		t.Fatal("expected start error")
	}
}

func TestStream(t *testing.T) {
	r := fake(t)
	var mu sync.Mutex
	var got []Progress
	rc, wait, err := r.Stream(context.Background(), "i", "18", Opts{}, func(p Progress) { mu.Lock(); got = append(got, p); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if string(body) != "VIDEODATA" {
		t.Fatalf("body = %q", body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[1].DownloadedBytes != 10 {
		t.Fatalf("progress = %+v", got)
	}
}

func TestStreamFailure(t *testing.T) {
	r := fake(t)
	t.Setenv("FAKE_YTDLP_MODE", "fail:Video unavailable")
	rc, wait, err := r.Stream(context.Background(), "i", "18", Opts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(rc)
	var ye *Error
	if err := wait(); !errors.As(err, &ye) || ye.Kind != domain.KindUnavailable {
		t.Fatalf("err = %v", err)
	}
	bad := &Runner{Bin: "/nonexistent/yt-dlp"}
	if _, _, err := bad.Stream(context.Background(), "i", "18", Opts{}, nil); err == nil {
		t.Fatal("expected start error")
	}
}

func TestHelpers(t *testing.T) {
	if got := lastError("a\nb"); got != "b" {
		t.Fatal(got)
	}
	if got := lastError(""); got != "" {
		t.Fatal(got)
	}
	if got := truncate(strings.Repeat("x", 600), 500); len([]rune(got)) != 501 {
		t.Fatal(len(got))
	}
	var b bytes.Buffer
	l := &limited{max: 4, buf: &b}
	_, _ = l.Write([]byte("abcdef"))
	if b.String() != "cdef" {
		t.Fatal(b.String())
	}
	tr := newTracker(nil)
	if !tr.line("TNPROG 1 2") || tr.line("other") {
		t.Fatal("tracker line detection")
	}
	if !tr.line("TNPROG 5 NA 50 NA NA") || tr.lastTotal != 50 {
		t.Fatal("estimate fallback")
	}
}

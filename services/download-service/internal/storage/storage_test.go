package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewAndPresignOffline(t *testing.T) {
	if _, err := New(Config{Endpoint: "127.0.0.1:9000", PublicURL: "::bad"}); err == nil {
		t.Fatal("bad public url must fail")
	}
	s, err := New(Config{Endpoint: "minio:9000", AccessKey: "a", SecretKey: "b", Region: "us-east-1", Bucket: "media", PublicURL: "https://app.example"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Bucket() != "media" {
		t.Fatal(s.Bucket())
	}
	ctx := context.Background()
	pub, err := s.PresignPublic(ctx, "youtube/2026/09/x.mp4", "Видео: тест.mp4", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(pub)
	if u.Host != "app.example" || u.Path != "/media/youtube/2026/09/x.mp4" || u.Query().Get("X-Amz-Expires") != "3600" {
		t.Fatalf("public = %s", pub)
	}
	if cd := u.Query().Get("response-content-disposition"); !strings.Contains(cd, "filename*=UTF-8''") || !strings.Contains(cd, `filename="`) {
		t.Fatalf("disposition = %q", cd)
	}
	in, err := s.PresignInternal(ctx, "k.mp4", "a.mp4", time.Minute)
	if err != nil || !strings.HasPrefix(in, "http://minio:9000/media/k.mp4?") {
		t.Fatalf("internal = %s %v", in, err)
	}
	noPub, _ := New(Config{Endpoint: "minio:9000", Bucket: "media"})
	if p, err := noPub.PresignPublic(ctx, "k", "a.mp4", time.Minute); p != "" || err != nil {
		t.Fatal("no public url configured → empty link")
	}
	if got := asciiName("Привет 🎬 video.mp4"); strings.ContainsAny(got, "Пр🎬\"") || !strings.HasSuffix(got, "video.mp4") {
		t.Fatal(got)
	}
}

// Integration against a real MinIO: TEST_S3_ENDPOINT (+ TEST_S3_ACCESS_KEY/SECRET_KEY).
func TestS3Integration(t *testing.T) {
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	s, err := New(Config{
		Endpoint: ep, AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("TEST_S3_SECRET_KEY"),
		Region: "us-east-1", Bucket: "tapenest-test", Retention: 48 * time.Hour, MultipartThreshold: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.EnsureBucket(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	small, big := "t/"+uuid.NewString()+".mp4", "t/"+uuid.NewString()+".mp4"
	t.Cleanup(func() { _ = s.Remove(ctx, small); _ = s.Remove(ctx, big) })
	if n, err := s.Put(ctx, small, strings.NewReader("hello"), 5, "video/mp4", "a.mp4"); err != nil || n != 5 {
		t.Fatal(n, err)
	}
	payload := bytes.Repeat([]byte("x"), 2<<20) // > threshold: multipart; unknown size: streamed
	if n, err := s.Put(ctx, big, bytes.NewReader(payload), -1, "video/mp4", "b.mp4"); err != nil || n != int64(len(payload)) {
		t.Fatal(n, err)
	}
	if ok, err := s.Exists(ctx, small); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := s.Exists(ctx, "t/missing"); ok || err != nil {
		t.Fatal(ok, err)
	}
	link, err := s.PresignInternal(ctx, small, "a.mp4", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(link) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello" || !strings.Contains(resp.Header.Get("Content-Disposition"), "a.mp4") {
		t.Fatal(resp.StatusCode, string(body), resp.Header)
	}
	if err := s.Remove(ctx, small); err != nil {
		t.Fatal(err)
	}
	bad, _ := New(Config{Endpoint: "127.0.0.1:1", Bucket: "x", Region: "us-east-1"})
	if bad.Ping(ctx) == nil || bad.EnsureBucket(ctx) == nil {
		t.Fatal("unreachable endpoint must fail")
	}
	if _, err := bad.Exists(ctx, "k"); err == nil {
		t.Fatal("exists must fail")
	}
}

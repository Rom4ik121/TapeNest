package cookies

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

func TestPutAndWriteFile(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := New(rdb, 12*time.Hour)
	ctx := context.Background()
	dir := t.TempDir()
	if _, ok, err := s.WriteFile(ctx, domain.SourceYouTube, dir); ok || err != nil {
		t.Fatalf("no cookies = %v %v", ok, err)
	}
	if err := s.Put(ctx, domain.SourceYouTube, "", time.Now()); err == nil {
		t.Fatal("empty must fail")
	}
	if err := s.Put(ctx, domain.SourceYouTube, "# Netscape HTTP Cookie File\n", time.Now().Add(-13*time.Hour)); err != nil {
		t.Fatal(err)
	}
	j, ok, err := s.WriteFile(ctx, domain.SourceYouTube, dir)
	if err != nil || !ok || !j.Stale || j.Age < 13*time.Hour {
		t.Fatalf("jar = %+v %v %v", j, ok, err)
	}
	st, err := os.Stat(j.Path)
	if err != nil || st.Mode().Perm() != 0o600 || filepath.Dir(j.Path) != dir {
		t.Fatalf("file = %v %v", st, err)
	}
	_ = s.Put(ctx, domain.SourceVK, "x", time.Now())
	if j, _, _ := s.WriteFile(ctx, domain.SourceVK, dir); j.Stale {
		t.Fatal("fresh cookies are not stale")
	}
	mr.Set(Key(domain.SourceRuTube), "x")
	if j, ok, _ := s.WriteFile(ctx, domain.SourceRuTube, dir); !ok || j.Age != 0 {
		t.Fatal("unknown age")
	}
	if _, _, err := s.WriteFile(ctx, domain.SourceVK, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("write error expected")
	}
	mr.Close()
	if _, _, err := s.WriteFile(ctx, domain.SourceVK, dir); err == nil {
		t.Fatal("redis error expected")
	}
	if err := s.Put(ctx, domain.SourceVK, "x", time.Now()); err == nil {
		t.Fatal("redis error expected")
	}
}

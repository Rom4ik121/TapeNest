package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
)

func TestScore(t *testing.T) {
	now := time.Now()
	a := uuid.New()
	base := repo.WaveCandidate{ArtistID: a, Popularity: 0}
	if service.Score(base, now, nil, nil, 0) != 0 {
		t.Fatal("zero")
	}
	liked := base
	liked.Liked, liked.LikedArtist = true, true
	if s := service.Score(liked, now, nil, nil, 0); s != 2.2 {
		t.Fatalf("liked: %v", s)
	}
	if s := service.Score(base, now, map[string]int{a.String(): 2}, map[string]int{a.String(): 1}, 0.1); s < 0.59 || s > 0.61 {
		t.Fatalf("session feedback: %v", s)
	}
	hour, day := now.Add(-time.Hour), now.Add(-5*time.Hour)
	old := now.Add(-48 * time.Hour)
	for want, at := range map[float64]*time.Time{-3: &hour, -1: &day, 0: &old} {
		c := base
		c.LastPlayed = at
		if s := service.Score(c, now, nil, nil, 0); s != want {
			t.Fatalf("recency %v: %v", want, s)
		}
	}
}

func TestWaveSmallCatalogAndSkips(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mem := testutil.NewMem(4) // smaller than a batch
	w := service.NewWave(mem, rdb)
	ctx := context.Background()
	u := uuid.New()
	id, b, err := w.Start(ctx, u, "")
	if err != nil || len(b.Tracks) != 4 || b.Strategy != domain.StrategyFallback {
		t.Fatalf("start: %+v %v", b, err)
	}
	b2, err := w.Next(ctx, u, id)
	if err != nil || len(b2.Tracks) != 4 {
		t.Fatalf("tiny catalog keeps playing: %d %v", len(b2.Tracks), err)
	}
	empty := service.NewWave(testutil.NewMem(0), rdb)
	if _, b, err := empty.Start(ctx, u, ""); err != nil || len(b.Tracks) != 0 {
		t.Fatalf("empty catalog: %v", err)
	}
}

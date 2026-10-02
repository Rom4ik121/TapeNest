package mb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestClient(t *testing.T) {
	id := uuid.New().String()
	rg := uuid.New().String()
	rec := uuid.New().String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ws/2/artist/") && !strings.Contains(r.URL.RawQuery, "query") {
			_, _ = w.Write([]byte(`{"id":"` + id + `","name":"Ada","disambiguation":"test"}`))
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/ws/2/artist"):
			_, _ = w.Write([]byte(`{"artists":[{"id":"` + id + `","name":"Ada","score":90}]}`))
		case strings.Contains(r.URL.Path, "/ws/2/release-group") && r.URL.Query().Get("artist") != "":
			_, _ = w.Write([]byte(`{"release-groups":[{"id":"` + rg + `","title":"Songs","primary-type":"Album","first-release-date":"2020","score":80,"artist-credit":[{"name":"Ada","artist":{"id":"` + id + `","name":"Ada"}}]}]}`))
		case strings.Contains(r.URL.Path, "/ws/2/release-group"):
			_, _ = w.Write([]byte(`{"release-groups":[{"id":"` + rg + `","title":"Songs Live","primary-type":"Album","secondary-types":["Live"],"score":10,"artist-credit":[{"name":"Ada","joinphrase":" & ","artist":{"id":"` + id + `"}},{"name":"Bea","artist":{"id":"` + uuid.New().String() + `"}}]}]}`))
		case strings.Contains(r.URL.Path, "/ws/2/recording"):
			_, _ = w.Write([]byte(`{"recordings":[{"id":"` + rec + `","title":"Aria","length":1000,"score":90,"artist-credit":[{"name":"Ada","artist":{"id":"` + id + `"}}],"releases":[{"title":"Songs","status":"Official","date":"2020","release-group":{"id":"` + rg + `","title":"Songs","primary-type":"Album"}}]}]}`))
		case strings.Contains(r.URL.Path, "/ws/2/release"):
			_, _ = w.Write([]byte(`{"releases":[{"id":"` + uuid.New().String() + `","title":"Songs","status":"Official","date":"2020","artist-credit":[{"name":"Ada","artist":{"id":"` + id + `"}}],"release-group":{"id":"` + rg + `","title":"Songs","primary-type":"Album"},"media":[{"position":1,"tracks":[{"title":"Aria","position":1,"length":1000,"recording":{"id":"` + rec + `","title":"Aria"}}]}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	c := New(srv.URL, "TapeNest test", rdb)
	c.Limiter = nil
	ctx := context.Background()
	as, err := c.SearchArtists(ctx, `ada "x"`, 5)
	if err != nil || len(as) != 1 {
		t.Fatal(err, as)
	}
	// cached
	if _, err := c.SearchArtists(ctx, `ada "x"`, 5); err != nil {
		t.Fatal(err)
	}
	rgs, err := c.SearchReleaseGroups(ctx, "songs", 5)
	if err != nil || len(rgs) != 1 || rgs[0].Score > 10 {
		t.Fatal(err, rgs)
	}
	recs, err := c.SearchRecordings(ctx, "aria", 5)
	if err != nil || len(recs) != 1 || recs[0].Album == "" {
		t.Fatal(err, recs)
	}
	al, err := c.Album(ctx, uuid.MustParse(rg))
	if err != nil || len(al.Tracks) != 1 {
		t.Fatal(err, al)
	}
	art, discs, err := c.Discography(ctx, uuid.MustParse(id), 10)
	if err != nil || art.Name != "Ada" || len(discs) != 1 {
		t.Fatal(err, art, discs)
	}
	if !strings.Contains(CoverURL("https://coverartarchive.org", uuid.MustParse(rg), 250), "front-250") {
		t.Fatal("cover")
	}
	if fieldQuery("a b", "recording") == "" || luceneEscape(`a"b`) == "" {
		t.Fatal("query")
	}
}

func TestErrorsAndLimiter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "missing") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c := New(srv.URL, "ua", nil)
	c.Limiter = nil
	c.HTTP.Timeout = time.Second
	if _, err := c.SearchArtists(context.Background(), "missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := c.SearchArtists(ctx, "slow", 1); err == nil {
		t.Fatal("want throttle/ctx error")
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	l := &RedisLimiter{Redis: rdb, Key: "k", Interval: time.Millisecond, Burst: 2}
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (&RedisLimiter{}).Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

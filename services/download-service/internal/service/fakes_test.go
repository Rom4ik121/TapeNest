package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/cookies"
	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
	"github.com/tapenest/tapenest/services/download-service/internal/proxy"
	"github.com/tapenest/tapenest/services/download-service/internal/testutil"
	"github.com/tapenest/tapenest/services/download-service/internal/ytdlp"
)

var errBoom = errors.New("boom")

type fakeGuard struct{ err error }

func (g fakeGuard) Check(context.Context, string) error { return g.err }

// env is a full service wiring: in-memory PG/S3, miniredis for everything Redis.
type env struct {
	mr    *miniredis.Miniredis
	rdb   *redis.Client
	store *testutil.MemStore
	files *testutil.MemFiles
	queue *mq.Queue
	bus   *mq.Bus
	api   *API
	proc  *Processor
	log   *slog.Logger
	guard *fakeGuard
}

func newEnv(t *testing.T) *env {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	q, err := mq.NewQueue(context.Background(), rdb, "test")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		mr: mr, rdb: rdb, store: testutil.NewMemStore(), files: testutil.NewMemFiles(), queue: q, bus: mq.NewBus(rdb),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), guard: &fakeGuard{},
	}
	e.api = NewAPI(APIDeps{
		Store: e.store, Queue: q, Bus: e.bus, Files: e.files, Guard: e.guard,
		Quotas: Quotas{Active: 3, Daily: 30}, PresignTTL: time.Hour, Log: e.log,
	})
	bin, _ := filepath.Abs("../ytdlp/testdata/fake-yt-dlp")
	e.proc = NewProcessor(ProcessorDeps{
		Config: ProcessorConfig{
			Limits:      domain.Limits{MaxHeight: 720, MinTGHeight: 360, TGLimit: 50_000_000},
			MaxFilesize: 1 << 30, MaxDuration: 3 * time.Hour, JobTimeout: time.Minute, Retention: 7 * 24 * time.Hour,
			PresignTTL: time.Hour, TmpDir: t.TempDir(),
		},
		Store: e.store, Queue: q, Bus: e.bus, Files: e.files, Fetch: &ytdlp.Runner{Bin: bin},
		Limiter: mq.NewSemaphores(rdb, map[domain.Source]int{domain.SourceYouTube: 1}, time.Minute),
		Locker:  mq.NewLocks(rdb), Plans: mq.NewPlans(rdb), Guard: e.guard,
		Cookies: cookies.New(rdb, 12*time.Hour), Proxies: proxy.New(nil, nil, nil), Log: e.log,
	})
	return e
}

// events returns the bot events stream entries.
func (e *env) events(t *testing.T) []mq.Event {
	t.Helper()
	xs, err := e.rdb.XRange(context.Background(), mq.EventsStream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]mq.Event, 0, len(xs))
	for _, x := range xs {
		var ev mq.Event
		if err := jsonUnmarshal(x.Values["data"].(string), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

var _ = netguard.ErrForbidden

func ytdlpProgress(down, total int64) ytdlp.Progress {
	return ytdlp.Progress{DownloadedBytes: down, TotalBytes: total}
}

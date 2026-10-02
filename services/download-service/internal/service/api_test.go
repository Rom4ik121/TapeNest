package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
)

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

const zoo = "https://youtu.be/jNQXAC9IVRw"

func storedMedia(e *env, url string, expires time.Time) domain.Media {
	n, _ := domain.NormalizeURL(url)
	m := domain.Media{
		ID: uuid.New(), URLHash: n.Hash(), URL: n.URL, Source: n.Source, ExternalID: "jNQXAC9IVRw", Title: "Me at the zoo",
		ObjectKey: "youtube/2026/09/x.mp4", SizeBytes: 633710, MimeType: "video/mp4", DurationSec: 19, Width: 320, Height: 240, ExpiresAt: expires,
	}
	e.store.Media[m.ID] = m
	e.files.Objects[m.ObjectKey] = []byte("x")
	return m
}

func TestCreateQueuesJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	user := uuid.New()
	j, created, err := e.api.Create(ctx, CreateInput{UserID: user, URL: zoo})
	if err != nil || !created || j.Status != domain.StatusQueued || j.Normalized != "https://www.youtube.com/watch?v=jNQXAC9IVRw" || j.Priority != domain.PriorityNormal {
		t.Fatalf("job = %+v %v %v", j, created, err)
	}
	d, _ := e.queue.Depth(ctx)
	if d["normal"] != 1 {
		t.Fatalf("depth = %v", d)
	}
	// same URL again (other spelling) → the active job, not a new one
	j2, created, err := e.api.Create(ctx, CreateInput{UserID: user, URL: "https://www.youtube.com/shorts/jNQXAC9IVRw"})
	if err != nil || created || j2.ID != j.ID {
		t.Fatalf("existing = %+v %v %v", j2, created, err)
	}
}

func TestCreateValidationAndQuotas(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	user := uuid.New()
	for _, u := range []string{"not a url", "https://example.com/video", "https://www.youtube.com/playlist?list=PL1"} {
		if _, _, err := e.api.Create(ctx, CreateInput{UserID: user, URL: u}); err == nil {
			t.Fatalf("%s accepted", u)
		}
	}
	e.guard.err = netguard.ErrForbidden
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: user, URL: zoo}); !errors.Is(err, ErrForbiddenHost) {
		t.Fatalf("forbidden = %v", err)
	}
	e.guard.err = errors.New("dns down") // deferred to the worker
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: user, URL: zoo}); err != nil {
		t.Fatal(err)
	}
	e.guard.err = nil
	for _, id := range []string{"aaaaaaaaaa1", "aaaaaaaaaa2"} {
		if _, _, err := e.api.Create(ctx, CreateInput{UserID: user, URL: "https://youtu.be/" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: user, URL: "https://youtu.be/aaaaaaaaaa3"}); !errors.Is(err, ErrQuotaActive) {
		t.Fatalf("active quota = %v", err)
	}
	// daily: 30 finished jobs within 24 h
	other := uuid.New()
	for i := 0; i < 30; i++ {
		id := uuid.New()
		e.store.Jobs[id] = domain.Job{ID: id, UserID: other, Status: domain.StatusDone, CreatedAt: time.Now()}
	}
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: other, URL: zoo}); !errors.Is(err, ErrQuotaDaily) {
		t.Fatalf("daily quota = %v", err)
	}
	e.store.Fail["FindActiveUserJob"] = errBoom
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: uuid.New(), URL: zoo}); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	delete(e.store.Fail, "FindActiveUserJob")
	e.store.Fail["CountUserJobs"] = errBoom
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: uuid.New(), URL: zoo}); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	delete(e.store.Fail, "CountUserJobs")
	e.store.Fail["InsertJob"] = errBoom
	if _, _, err := e.api.Create(ctx, CreateInput{UserID: uuid.New(), URL: zoo}); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
}

func TestCreateDedupFromStorage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := storedMedia(e, zoo, time.Now().Add(time.Hour))
	chat := &domain.Chat{ChatID: 42, StatusMessageID: 7, ReplyToMessageID: 6, Lang: "ru"}
	j, created, err := e.api.Create(ctx, CreateInput{UserID: uuid.New(), URL: zoo, Chat: chat})
	if err != nil || !created || j.Status != domain.StatusDone || *j.MediaID != m.ID {
		t.Fatalf("dedup = %+v %v", j, err)
	}
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Type != mq.EventDownloaded || !evs[0].Cached || evs[0].ChatID != 42 || evs[0].StatusMessageID != 7 ||
		evs[0].File == nil || evs[0].File.SizeBytes != 633710 || evs[0].File.FileName != "Me at the zoo.mp4" || evs[0].File.PublicURL == "" {
		t.Fatalf("events = %+v", evs)
	}
	// layer 1 (Redis) is now warm
	if id, _ := e.bus.CachedMedia(ctx, m.URLHash); id != m.ID {
		t.Fatal("dedup cache not written")
	}
	j2, _, _ := e.api.Create(ctx, CreateInput{UserID: uuid.New(), URL: zoo})
	if j2.Status != domain.StatusDone {
		t.Fatal("redis layer hit expected")
	}
	v, err := e.api.View(ctx, j2)
	if err != nil || v.File == nil || v.File.ID != m.ID {
		t.Fatalf("view = %+v %v", v, err)
	}
}

func TestLookupSkipsExpiredAndMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := storedMedia(e, zoo, time.Now().Add(-time.Minute)) // expired
	_ = e.bus.CacheMedia(ctx, m.URLHash, m.ID)
	if _, hit, err := e.api.lookup(ctx, m.URLHash); hit || err != nil {
		t.Fatal("expired media must miss")
	}
	if id, _ := e.bus.CachedMedia(ctx, m.URLHash); id != uuid.Nil {
		t.Fatal("stale cache entry must be dropped")
	}
	m2 := storedMedia(e, "https://youtu.be/bbbbbbbbbbb", time.Now().Add(time.Hour))
	delete(e.files.Objects, m2.ObjectKey)
	if _, hit, _ := e.api.lookup(ctx, m2.URLHash); hit {
		t.Fatal("missing object must miss")
	}
	e.files.ExistsErr = errBoom
	if _, _, err := e.api.lookup(ctx, m2.URLHash); err == nil {
		t.Fatal("exists error expected")
	}
	e.store.Fail["GetMediaByHash"] = errBoom
	if _, _, err := e.api.lookup(ctx, m2.URLHash); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	e.mr.Close() // Redis down: lookup falls back to PG
	delete(e.store.Fail, "GetMediaByHash")
	e.files.ExistsErr = nil
	e.files.Objects[m2.ObjectKey] = []byte("x")
	if _, hit, err := e.api.lookup(ctx, m2.URLHash); !hit || err != nil {
		t.Fatalf("pg fallback = %v %v", hit, err)
	}
}

func TestCreateEnqueueFailure(t *testing.T) {
	e := newEnv(t)
	e.rdb.Del(context.Background(), mq.StreamName(domain.PriorityNormal))
	e.mr.Close()
	_, _, err := e.api.Create(context.Background(), CreateInput{UserID: uuid.New(), URL: zoo})
	if err == nil {
		t.Fatal("expected enqueue error")
	}
	for _, j := range e.store.Jobs {
		if j.Status != domain.StatusFailed || j.ErrorKind != domain.KindInternal {
			t.Fatalf("job = %+v", j)
		}
	}
}

func TestGetListFileURL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	user := uuid.New()
	j, _, _ := e.api.Create(ctx, CreateInput{UserID: user, URL: zoo})
	if _, err := e.api.Get(ctx, uuid.New(), j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign job must be hidden")
	}
	if _, err := e.api.FileURL(ctx, user, j.ID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not ready = %v", err)
	}
	// running with progress
	_, _ = e.store.MarkRunning(ctx, j.ID, domain.StageDownloading)
	_ = e.bus.SetProgress(ctx, j.ID, domain.Progress{Stage: domain.StageDownloading, Pct: 50})
	v, err := e.api.Get(ctx, user, j.ID)
	if err != nil || v.Progress == nil || v.Progress.Pct != 50 {
		t.Fatalf("view = %+v %v", v, err)
	}
	m := storedMedia(e, zoo, time.Now().Add(time.Hour))
	_, _ = e.store.FinishDone(ctx, j.ID, m.ID, m.Title)
	u, err := e.api.FileURL(ctx, user, j.ID)
	if err != nil || u == "" {
		t.Fatalf("file url = %q %v", u, err)
	}
	e.files.NoPublic = true
	if _, err := e.api.FileURL(ctx, user, j.ID); !errors.Is(err, ErrNoPublicURL) {
		t.Fatal(err)
	}
	list, err := e.api.List(ctx, user, nil, 10)
	if err != nil || len(list) != 1 || list[0].File == nil {
		t.Fatalf("list = %+v %v", list, err)
	}
	e.store.Fail["GetMedia"] = errBoom
	if _, err := e.api.List(ctx, user, nil, 10); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	e.store.Fail["ListUserJobs"] = errBoom
	if _, err := e.api.List(ctx, user, nil, 10); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	if _, err := e.api.FileURL(ctx, user, j.ID); !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
}

func TestFileName(t *testing.T) {
	for _, c := range []struct{ title, fb, mime, want string }{
		{"Me at the zoo", "x", "video/mp4", "Me at the zoo.mp4"},
		{`a/b\c:d*e?f"g<h>i|j`, "x", "video/mp4", "a_b_c_d_e_f_g_h_i_j.mp4"},
		{"  ...  ", "abc", "video/webm", "abc.webm"},
		{"", "", "video/mp4", "video.mp4"},
		{"line\nbreak", "", "video/mp4", "linebreak.mp4"},
	} {
		if got := FileName(c.title, c.fb, c.mime); got != c.want {
			t.Errorf("FileName(%q) = %q, want %q", c.title, got, c.want)
		}
	}
	long := FileName(string(make([]rune, 0))+stringsRepeat("я", 200), "", "video/mp4")
	if len(long) > 130 {
		t.Fatalf("long name %d bytes", len(long))
	}
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

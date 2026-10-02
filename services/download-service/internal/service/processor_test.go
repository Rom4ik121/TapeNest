package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
)

func queued(t *testing.T, e *env, url string, chat *domain.Chat) domain.Job {
	t.Helper()
	j, _, err := e.api.Create(context.Background(), CreateInput{UserID: uuid.New(), URL: url, Chat: chat})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func writeInfo(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "info.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_YTDLP_INFO", p)
}

const mergeInfo = `{"id":"abc","title":"Merged clip","duration":10,"thumbnail":"https://i.ytimg.com/x.jpg","formats":[
 {"format_id":"136","ext":"mp4","vcodec":"avc1.4d401f","acodec":"none","height":720,"width":1280,"filesize":3000},
 {"format_id":"140","ext":"m4a","vcodec":"none","acodec":"mp4a.40.2","filesize":500}]}`

func TestProcessStreamSuccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := queued(t, e, zoo, &domain.Chat{ChatID: 5, StatusMessageID: 9, Lang: "en"})
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got := e.store.Job(j.ID)
	if got.Status != domain.StatusDone || got.Attempts != 1 || got.Title != "Test clip" || got.MediaID == nil {
		t.Fatalf("job = %+v", got)
	}
	m := e.store.Media[*got.MediaID]
	if m.SizeBytes != int64(len("VIDEODATA")) || m.Height != 360 || m.Width != 640 || m.FormatID != "18" || m.MimeType != "video/mp4" {
		t.Fatalf("media = %+v", m)
	}
	if string(e.files.Objects[m.ObjectKey]) != "VIDEODATA" {
		t.Fatal("object not stored")
	}
	evs := e.events(t)
	last := evs[len(evs)-1]
	if last.Type != mq.EventDownloaded || last.File == nil || last.File.InternalURL == "" || last.Cached || last.Lang != "en" {
		t.Fatalf("last event = %+v", last)
	}
	if evs[0].Type != mq.EventProgress {
		t.Fatalf("first event = %+v", evs[0])
	}
	if id, _ := e.bus.CachedMedia(ctx, j.URLHash); id != m.ID {
		t.Fatal("dedup cache")
	}
	// terminal job is a no-op; unknown job too
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.proc.Process(ctx, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

func TestProcessMergeSuccess(t *testing.T) {
	e := newEnv(t)
	writeInfo(t, mergeInfo)
	j := queued(t, e, zoo, nil)
	if err := e.proc.Process(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	got := e.store.Job(j.ID)
	m := e.store.Media[*got.MediaID]
	if got.Status != domain.StatusDone || m.FormatID != "136+140" || m.Height != 720 || m.Width != 1280 || m.Thumbnail == "" ||
		string(e.files.Objects[m.ObjectKey]) != "MERGEDVIDEO" {
		t.Fatalf("job = %+v media = %+v", got, m)
	}
	if len(e.events(t)) != 0 {
		t.Fatal("web jobs do not go to the bot stream")
	}
}

func TestProcessTerminalFailures(t *testing.T) {
	cases := map[string]struct {
		mode, info string
		kind       domain.ErrorKind
	}{
		"private":     {mode: "fail:Private video. Sign in", kind: domain.KindPrivate},
		"live":        {info: `{"id":"a","title":"t","is_live":true,"formats":[]}`, kind: domain.KindLive},
		"drm":         {info: `{"id":"a","title":"t","formats":[{"format_id":"1","vcodec":"avc1","acodec":"aac","height":360,"has_drm":true}]}`, kind: domain.KindDRM},
		"too long":    {info: `{"id":"a","title":"t","duration":99999,"formats":[]}`, kind: domain.KindTooLarge},
		"no formats":  {info: `{"id":"a","title":"t","duration":5,"formats":[{"format_id":"sb","vcodec":"none","acodec":"none"}]}`, kind: domain.KindUnavailable},
		"est too big": {info: `{"id":"a","title":"t","duration":5,"formats":[{"format_id":"18","ext":"mp4","vcodec":"avc1","acodec":"aac","height":360,"filesize":5000000000}]}`, kind: domain.KindTooLarge},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if c.mode != "" {
				t.Setenv("FAKE_YTDLP_MODE", c.mode)
			}
			if c.info != "" {
				writeInfo(t, c.info)
			}
			j := queued(t, e, zoo, &domain.Chat{ChatID: 1})
			if err := e.proc.Process(context.Background(), j.ID); err != nil {
				t.Fatal(err)
			}
			got := e.store.Job(j.ID)
			if got.Status != domain.StatusFailed || got.ErrorKind != c.kind {
				t.Fatalf("job = %+v", got)
			}
			evs := e.events(t)
			if last := evs[len(evs)-1]; last.Type != mq.EventFailed || last.ErrorKind != c.kind {
				t.Fatalf("event = %+v", last)
			}
		})
	}
}

func TestProcessRetryThenFail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	t.Setenv("FAKE_YTDLP_MODE", "fail:Unable to download webpage: timed out")
	j := queued(t, e, zoo, &domain.Chat{ChatID: 1})
	for attempt := 1; attempt <= domain.MaxAttempts; attempt++ {
		if err := e.proc.Process(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		got := e.store.Job(j.ID)
		if attempt < domain.MaxAttempts {
			if got.Status != domain.StatusQueued || got.Priority != domain.PriorityLow || got.ErrorKind != domain.KindNetwork {
				t.Fatalf("attempt %d: job = %+v", attempt, got)
			}
			pl, _ := mq.NewPlans(e.rdb).Get(ctx, j.ID)
			if attempt >= 2 && !pl.WithCookies {
				t.Fatalf("attempt %d: plan = %+v", attempt, pl)
			}
		} else if got.Status != domain.StatusFailed {
			t.Fatalf("final: job = %+v", got)
		}
	}
	d, _ := e.queue.Depth(ctx)
	if d["delayed"] != 1 {
		t.Fatalf("depth = %v", d)
	}
}

func TestProcessWithCookiesAndMobileUA(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	args := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_YTDLP_ARGS", args)
	j := queued(t, e, zoo, nil)
	_ = mq.NewPlans(e.rdb).Set(ctx, j.ID, mq.Plan{Attempt: domain.Attempt{Tier: domain.TierMobile, WithCookies: true, MobileUA: true, NewProxy: true}, LastProxy: "http://old"})
	e.rdb.Set(ctx, "cookies:youtube", "# Netscape HTTP Cookie File\n", 0)
	e.rdb.Set(ctx, "cookies:youtube:updated_at", time.Now().Add(-24*time.Hour).Unix(), 0)
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(args)
	if !contains(string(raw), "--cookies") || !contains(string(raw), "iPhone") {
		t.Fatalf("args = %s", raw)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestProcessLockedBusyAndDedup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := queued(t, e, zoo, nil)
	unlock, ok, _ := mq.NewLocks(e.rdb).Lock(ctx, "download:lock:"+j.URLHash, time.Minute)
	if !ok {
		t.Fatal("lock")
	}
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.queue.Depth(ctx); d["delayed"] != 1 || e.store.Job(j.ID).Status != domain.StatusQueued {
		t.Fatalf("locked: depth = %v", d)
	}
	unlock()
	sem := mq.NewSemaphores(e.rdb, map[domain.Source]int{domain.SourceYouTube: 1}, time.Minute)
	_, _ = sem.Acquire(ctx, domain.SourceYouTube, "someone")
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if e.store.Job(j.ID).Status != domain.StatusQueued {
		t.Fatal("busy domain must defer")
	}
	_ = sem.Release(ctx, domain.SourceYouTube, "someone")
	// another job stored the file meanwhile → cached result under the lock
	storedMedia(e, zoo, time.Now().Add(time.Hour))
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.store.Job(j.ID); got.Status != domain.StatusDone || got.Attempts != 0 {
		t.Fatalf("dedup under lock = %+v", got)
	}
}

func TestProcessGuardAndInfraErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := queued(t, e, zoo, nil)
	e.guard.err = netguard.ErrForbidden
	_ = e.proc.Process(ctx, j.ID)
	if got := e.store.Job(j.ID); got.Status != domain.StatusFailed || got.ErrorKind != domain.KindUnsupported {
		t.Fatalf("guard = %+v", got)
	}
	e.guard.err = nil
	j2 := queued(t, e, "https://youtu.be/ccccccccccc", nil)
	e.files.PutErr = errBoom
	_ = e.proc.Process(ctx, j2.ID)
	if got := e.store.Job(j2.ID); got.ErrorKind != domain.KindNetwork || got.Status != domain.StatusQueued {
		t.Fatalf("put error = %+v", got)
	}
	e.files.PutErr = nil
	e.store.Fail["GetJob"] = errBoom
	if err := e.proc.Process(ctx, j2.ID); err == nil {
		t.Fatal("store error must propagate")
	}
	delete(e.store.Fail, "GetJob")
	e.store.Fail["MarkRunning"] = errBoom
	if err := e.proc.Process(ctx, j2.ID); err == nil {
		t.Fatal("mark running error must propagate")
	}
}

func TestProcessShutdownRequeues(t *testing.T) {
	e := newEnv(t)
	t.Setenv("FAKE_YTDLP_MODE", "slow")
	j := queued(t, e, zoo, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	if err := e.proc.Process(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got := e.store.Job(j.ID)
	if got.Status != domain.StatusQueued || got.ErrorKind != "" {
		t.Fatalf("job = %+v", got)
	}
	d, _ := e.queue.Depth(context.Background())
	if d["normal"] != 2 { // original entry (never consumed in this test) + requeue
		t.Fatalf("depth = %v", d)
	}
}

func TestProgressFnThrottles(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	e.proc.now = func() time.Time { return now }
	j := domain.Job{ID: uuid.New(), Chat: &domain.Chat{ChatID: 1}}
	fn := e.proc.progressFn(context.Background(), j, 1000)
	fn(ytdlpProgress(0, 0))
	fn(ytdlpProgress(100, 0)) // same step, same instant → dropped
	fn(ytdlpProgress(300, 0)) // new 25 % step → bot event
	now = now.Add(time.Second)
	fn(ytdlpProgress(400, 900)) // time passed, same step → Redis only
	if n := len(e.events(t)); n != 2 {
		t.Fatalf("bot events = %d", n)
	}
	p, _ := e.bus.Progress(context.Background(), j.ID)
	if p == nil || p.TotalBytes != 1000 || p.Pct != 40 {
		t.Fatalf("progress = %+v", p)
	}
}

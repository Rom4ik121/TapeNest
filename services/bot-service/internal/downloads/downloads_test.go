package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/bot-service/internal/i18n"
	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

type fakeTG struct {
	mu        sync.Mutex
	sent      []telegram.SendMessage
	edits     []telegram.EditMessageText
	uploads   []telegram.Upload
	bodies    []string
	editErr   error
	uploadErr error
}

func (f *fakeTG) SendMessage(_ context.Context, m telegram.SendMessage) (telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return telegram.Message{MessageID: 1}, nil
}

func (f *fakeTG) EditMessageText(_ context.Context, m telegram.EditMessageText) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, m)
	return f.editErr
}

func (f *fakeTG) SendFile(_ context.Context, u telegram.Upload) error {
	b, _ := io.ReadAll(u.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, u)
	f.bodies = append(f.bodies, string(b))
	return f.uploadErr
}

func (f *fakeTG) lastText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.edits) == 0 {
		return ""
	}
	return f.edits[len(f.edits)-1].Text
}

func newNotifier(t *testing.T) (*Notifier, *fakeTG, *httptest.Server) {
	t.Helper()
	texts, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("MP4DATA"))
	}))
	t.Cleanup(files.Close)
	mr := miniredis.RunT(t)
	tg := &fakeTG{}
	return &Notifier{
		TG: tg, Texts: texts, HTTP: files.Client(), UploadLimit: 50_000_000,
		Redis: redis.NewClient(&redis.Options{Addr: mr.Addr()}), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, tg, files
}

func base(typ string) Event {
	return Event{Type: typ, JobID: "j1", ChatID: 42, StatusMessageID: 7, ReplyToMessageID: 6, Lang: "ru", Source: "youtube", Title: "Me at the zoo"}
}

func TestProgressTexts(t *testing.T) {
	n, tg, _ := newNotifier(t)
	ctx := context.Background()
	e := base(TypeProgress)
	e.Title = ""
	_ = n.Handle(ctx, e)
	if !strings.Contains(tg.lastText(), "Проверяю видео (YouTube)") || tg.edits[0].MessageID != 7 || tg.edits[0].ChatID != 42 {
		t.Fatalf("probing: %+v", tg.edits)
	}
	e = base(TypeProgress)
	e.Progress = &Progress{Stage: "downloading", Pct: 50}
	_ = n.Handle(ctx, e)
	if tg.lastText() != "⏬ Скачиваю: Me at the zoo\n▓▓▓▓▓░░░░░ 50%" {
		t.Fatalf("progress: %q", tg.lastText())
	}
	e.Progress, e.Lang = &Progress{Stage: "uploading", Pct: 99}, "en"
	_ = n.Handle(ctx, e)
	if !strings.Contains(tg.lastText(), "Almost done") {
		t.Fatal(tg.lastText())
	}
	e = base(TypeProgress)
	e.Status, e.ErrorKind, e.Attempt = "queued", "network", 1
	_ = n.Handle(ctx, e)
	if !strings.Contains(tg.lastText(), "попытка 2") {
		t.Fatal(tg.lastText())
	}
	// web jobs (no chat) and unknown types are ignored
	before := len(tg.edits)
	_ = n.Handle(ctx, Event{Type: TypeProgress})
	_ = n.Handle(ctx, Event{Type: "other", ChatID: 1})
	if len(tg.edits) != before {
		t.Fatal("must be ignored")
	}
	if bar(-5) != "░░░░░░░░░░" || bar(150) != "▓▓▓▓▓▓▓▓▓▓" {
		t.Fatal("bar clamps")
	}
	long := base(TypeProgress)
	long.Title = strings.Repeat("я", 300)
	if r := []rune(title(long)); len(r) != 201 {
		t.Fatal(len(r))
	}
	if title(Event{}) != "video" {
		t.Fatal(title(Event{}))
	}
}

func TestFailedTexts(t *testing.T) {
	n, tg, _ := newNotifier(t)
	for kind, want := range map[string]string{
		"private": "приватное", "drm_protected": "DRM", "live": "трансляция", "unavailable": "недоступно",
		"weird-new-kind": "Не получилось скачать «Me at the zoo»",
	} {
		e := base(TypeFailed)
		e.ErrorKind = kind
		if err := n.Handle(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tg.lastText(), want) {
			t.Errorf("%s: %q", kind, tg.lastText())
		}
	}
}

func TestDownloadedSmallIsUploaded(t *testing.T) {
	n, tg, files := newNotifier(t)
	e := base(TypeDownloaded)
	e.File = &FileInfo{
		SizeBytes: 7, MimeType: "video/mp4", FileName: "Me at the zoo.mp4", DurationSec: 19, Width: 320, Height: 240,
		InternalURL: files.URL + "/media/k.mp4?X-Amz-Signature=x", PublicURL: "https://app.example/media/k.mp4",
	}
	if err := n.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(tg.uploads) != 1 || tg.bodies[0] != "MP4DATA" {
		t.Fatalf("uploads: %+v", tg.uploads)
	}
	u := tg.uploads[0]
	if u.ChatID != 42 || u.ReplyToMessageID != 6 || u.Size != 7 || u.AsDocument || u.DurationSec != 19 || u.Caption != "🎬 Me at the zoo" {
		t.Fatalf("upload: %+v", u)
	}
	if !strings.HasPrefix(tg.lastText(), "✅ Готово: Me at the zoo") {
		t.Fatal(tg.lastText())
	}
	// duplicate delivery of the same event: no second upload
	if err := n.Handle(context.Background(), e); err != nil || len(tg.uploads) != 1 {
		t.Fatal("duplicate event must not re-upload")
	}
}

func TestDownloadedBigOrFailedUploadGivesLink(t *testing.T) {
	n, tg, files := newNotifier(t)
	e := base(TypeDownloaded)
	e.File = &FileInfo{SizeBytes: 80_000_000, MimeType: "video/mp4", InternalURL: files.URL + "/k", PublicURL: "https://app.example/media/k.mp4?sig"}
	if err := n.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	last := tg.edits[len(tg.edits)-1]
	if len(tg.uploads) != 0 || !strings.Contains(last.Text, "80.0 MB") || !strings.Contains(last.Text, "50.0 MB") ||
		last.ReplyMarkup == nil || last.ReplyMarkup.InlineKeyboard[0][0].URL != e.File.PublicURL {
		t.Fatalf("link: %+v", last)
	}
	// Telegram rejects the upload → link fallback
	e2 := base(TypeDownloaded)
	e2.JobID = "j2"
	e2.File = &FileInfo{SizeBytes: 7, MimeType: "video/webm", InternalURL: files.URL + "/k", PublicURL: "https://app.example/x"}
	tg.uploadErr = &telegram.APIError{Code: 413, Description: "Request Entity Too Large"}
	_ = n.Handle(context.Background(), e2)
	if len(tg.uploads) != 1 || !tg.uploads[0].AsDocument || tg.edits[len(tg.edits)-1].ReplyMarkup == nil {
		t.Fatalf("fallback: %+v", tg.edits[len(tg.edits)-1])
	}
	// storage fetch fails → link; no public link → explanatory text
	e3 := base(TypeDownloaded)
	e3.JobID, e3.Lang = "j3", "en"
	e3.File = &FileInfo{SizeBytes: 7, InternalURL: files.URL + "/missing"}
	_ = n.Handle(context.Background(), e3)
	if !strings.Contains(tg.lastText(), "not configured") {
		t.Fatal(tg.lastText())
	}
	e4 := base(TypeDownloaded)
	e4.JobID = "j4"
	_ = n.Handle(context.Background(), e4) // no file info
	if !strings.Contains(tg.lastText(), "Не получилось") {
		t.Fatal(tg.lastText())
	}
	e5 := base(TypeDownloaded)
	e5.JobID = "j5"
	e5.File = &FileInfo{SizeBytes: 7, InternalURL: "http://127.0.0.1:1/k?X-Amz-Signature=secret"}
	if err := n.upload(context.Background(), e5); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("fetch error must not leak the signature: %v", err)
	}
	if HumanSize(1500) != "2 KB" || HumanSize(2_500_000_000) != "2.5 GB" {
		t.Fatal(HumanSize(1500), HumanSize(2_500_000_000))
	}
}

func TestStatusFallsBackToNewMessage(t *testing.T) {
	n, tg, _ := newNotifier(t)
	tg.editErr = &telegram.APIError{Code: 400, Description: "Bad Request: message to edit not found"}
	e := base(TypeFailed)
	e.ErrorKind = "private"
	if err := n.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(tg.sent) != 1 || tg.sent[0].ReplyParameters.MessageID != 6 {
		t.Fatalf("sent: %+v", tg.sent)
	}
	tg.editErr = errors.New("network")
	if err := n.Handle(context.Background(), e); err == nil {
		t.Fatal("transient edit errors must surface (retry)")
	}
	e.StatusMessageID = 0
	tg.editErr = nil
	_ = n.Handle(context.Background(), e)
	if len(tg.sent) != 2 {
		t.Fatal("no status message → send")
	}
}

func TestUnreachableChatDropsEvent(t *testing.T) {
	n, tg, files := newNotifier(t)
	tg.uploadErr = &telegram.APIError{Code: 400, Description: "Bad Request: chat not found"}
	tg.editErr = &telegram.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}
	e := base(TypeDownloaded)
	e.File = &FileInfo{SizeBytes: 7, MimeType: "video/mp4", InternalURL: files.URL + "/k", PublicURL: "https://app.example/x"}
	if err := n.Handle(context.Background(), e); err != nil {
		t.Fatalf("unreachable chat must not be retried: %v", err)
	}
	if len(tg.uploads) != 1 || len(tg.sent) != 0 {
		t.Fatalf("no link fallback / new message for an unreachable chat: uploads=%d sent=%d", len(tg.uploads), len(tg.sent))
	}
}

func TestConsumer(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []string
	fails := map[string]int{}
	c := &Consumer{
		Redis: rdb, Name: "c1", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Block: 50 * time.Millisecond, Retries: 2,
		Handle: func(_ context.Context, e Event) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, e.JobID)
			if e.JobID == "bad" {
				fails[e.JobID]++
				return errors.New("tg down")
			}
			return nil
		},
	}
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond) // group created with "$"
	add := func(id string) {
		raw, _ := json.Marshal(Event{Type: TypeProgress, JobID: id, ChatID: 1})
		rdb.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, Values: map[string]any{"type": TypeProgress, "data": string(raw)}})
	}
	add("a")
	add("bad")
	rdb.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, Values: map[string]any{"data": "{broken"}})
	add("b")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != "a,bad,bad,b" || fails["bad"] != 2 {
		t.Fatalf("got %v", got)
	}
	pend, err := rdb.XPending(context.Background(), Stream, Group).Result()
	if err != nil || pend.Count != 0 {
		t.Fatalf("everything must be acked: %+v %v", pend, err)
	}
}

func TestConsumerRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c := &Consumer{Redis: rdb, Name: "c", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Handle: func(context.Context, Event) error { return nil }}
	c.Run(ctx) // returns on ctx without panicking
}

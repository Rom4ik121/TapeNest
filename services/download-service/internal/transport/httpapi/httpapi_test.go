package httpapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/service"
	"github.com/tapenest/tapenest/services/download-service/internal/testutil"
)

const token = "internal-token-0123456789abcdef"

type guard struct{ err error }

func (g *guard) Check(context.Context, string) error { return g.err }

type fixture struct {
	srv   *httptest.Server
	store *testutil.MemStore
	files *testutil.MemFiles
	bus   *mq.Bus
	mr    *miniredis.Miniredis
	user  uuid.UUID
	ready map[string]Pinger
}

func setup(t *testing.T) *fixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	q, err := mq.NewQueue(context.Background(), rdb, "t")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		store: testutil.NewMemStore(), files: testutil.NewMemFiles(), bus: mq.NewBus(rdb), mr: mr, user: uuid.New(),
		ready: map[string]Pinger{"redis": func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := service.NewAPI(service.APIDeps{
		Store: f.store, Queue: q, Bus: f.bus, Files: f.files, Guard: &guard{},
		Quotas: service.Quotas{Active: 3, Daily: 30}, PresignTTL: time.Hour, Log: log,
	})
	f.srv = httptest.NewServer(NewRouter(Deps{
		API: api, Bus: f.bus, InternalToken: token, Log: log, Ready: f.ready,
		SSEHeartbeat: 50 * time.Millisecond, SSEMax: 5 * time.Second,
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) do(t *testing.T, method, path, body string, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", f.user.String())
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func TestHealthAndAuth(t *testing.T) {
	f := setup(t)
	if r, _ := f.do(t, "GET", "/healthz", "", nil); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r, b := f.do(t, "GET", "/readyz", "", nil); r.StatusCode != 200 || b["redis"] != "ok" {
		t.Fatal(r.StatusCode, b)
	}
	f.ready["pg"] = func(context.Context) error { return errors.New("down") }
	if r, b := f.do(t, "GET", "/readyz", "", nil); r.StatusCode != 503 || b["pg"] != "down" {
		t.Fatal(r.StatusCode, b)
	}
	if r, b := f.do(t, "GET", "/api/v1/downloads", "", map[string]string{"X-Internal-Token": "wrong"}); r.StatusCode != 401 || b["code"] != CodeUnauthorized {
		t.Fatal(r.StatusCode, b)
	}
	if r, _ := f.do(t, "GET", "/api/v1/downloads", "", map[string]string{"X-Internal-Token": ""}); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := f.do(t, "GET", "/api/v1/downloads", "", map[string]string{"X-User-Id": "nope"}); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	r, _ := f.do(t, "GET", "/api/v1/downloads", "", map[string]string{"X-Request-Id": "req-12345678"})
	if r.Header.Get("X-Request-Id") != "req-12345678" {
		t.Fatal("request id not echoed")
	}
}

func TestCreateGetListFile(t *testing.T) {
	f := setup(t)
	r, b := f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/jNQXAC9IVRw"}`, nil)
	if r.StatusCode != 202 || b["status"] != "queued" || b["source"] != "youtube" {
		t.Fatal(r.StatusCode, b)
	}
	id := b["id"].(string)
	if r, b := f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/jNQXAC9IVRw"}`, nil); r.StatusCode != 200 || b["id"] != id {
		t.Fatal("existing job expected", r.StatusCode, b)
	}
	if r, b := f.do(t, "GET", "/api/v1/downloads/"+id, "", nil); r.StatusCode != 200 || b["id"] != id {
		t.Fatal(r.StatusCode, b)
	}
	if r, b := f.do(t, "GET", "/api/v1/downloads/"+id+"/file", "", nil); r.StatusCode != 409 || b["code"] != CodeNotReady {
		t.Fatal(r.StatusCode, b)
	}
	if r, _ := f.do(t, "GET", "/api/v1/downloads/not-a-uuid", "", nil); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if r, b := f.do(t, "GET", "/api/v1/downloads/"+uuid.NewString(), "", nil); r.StatusCode != 404 || b["code"] != CodeNotFound {
		t.Fatal(r.StatusCode, b)
	}
	// finish it with a stored file
	jid := uuid.MustParse(id)
	m := domain.Media{ID: uuid.New(), URLHash: "h", ObjectKey: "youtube/x.mp4", Title: "Zoo", MimeType: "video/mp4", SizeBytes: 10, ExpiresAt: time.Now().Add(time.Hour)}
	f.store.Media[m.ID] = m
	_, _ = f.store.FinishDone(context.Background(), jid, m.ID, "Zoo")
	r, _ = f.do(t, "GET", "/api/v1/downloads/"+id+"/file", "", nil)
	if r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), "https://app.example/media/youtube/x.mp4") || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(r.StatusCode, r.Header)
	}
	if r, b := f.do(t, "GET", "/api/v1/downloads/"+id+"/file?redirect=false", "", nil); r.StatusCode != 200 || b["url"] == "" {
		t.Fatal(r.StatusCode, b)
	}
	f.files.NoPublic = true
	if r, b := f.do(t, "GET", "/api/v1/downloads/"+id+"/file", "", nil); r.StatusCode != 503 || b["code"] != CodeNoPublicURL {
		t.Fatal(r.StatusCode, b)
	}
	if r, b := f.do(t, "GET", "/api/v1/downloads/"+id, "", nil); b["file"] == nil {
		t.Fatal(r.StatusCode, b)
	}

	// list + cursor
	for i, u := range []string{"https://youtu.be/aaaaaaaaaa1", "https://youtu.be/aaaaaaaaaa2"} {
		time.Sleep(2 * time.Millisecond)
		if r, _ := f.do(t, "POST", "/api/v1/downloads", `{"url":"`+u+`"}`, nil); r.StatusCode != 202 {
			t.Fatal(i, r.StatusCode)
		}
	}
	r, b = f.do(t, "GET", "/api/v1/downloads?limit=2", "", nil)
	items := b["items"].([]any)
	if r.StatusCode != 200 || len(items) != 2 || b["nextCursor"] == nil {
		t.Fatal(r.StatusCode, b)
	}
	_, b = f.do(t, "GET", "/api/v1/downloads?limit=2&cursor="+b["nextCursor"].(string), "", nil)
	if items := b["items"].([]any); len(items) != 1 || b["nextCursor"] != nil {
		t.Fatal(b)
	}
	for _, q := range []string{"limit=0", "limit=x", "cursor=!!", "cursor=" + encodeB64("nocolon"), "cursor=" + encodeB64("x:y"), "cursor=" + encodeB64("1:y")} {
		if r, _ := f.do(t, "GET", "/api/v1/downloads?"+q, "", nil); r.StatusCode != 400 {
			t.Fatal(q, r.StatusCode)
		}
	}
	if _, err := decodeCursor(encodeCursor(repo.Cursor{CreatedAt: time.UnixMicro(5), ID: jid})); err != nil {
		t.Fatal(err)
	}
}

func encodeB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func TestCreateErrors(t *testing.T) {
	f := setup(t)
	cases := []struct {
		body   string
		status int
		code   string
	}{
		{`{`, 400, CodeInvalid},
		{`{"url":""}`, 400, CodeInvalid},
		{`{"url":"hello world"}`, 400, CodeInvalidURL},
		{`{"url":"https://example.com/v"}`, 400, CodeUnsupported},
		{`{"url":"https://www.youtube.com/playlist?list=PL1"}`, 400, CodePlaylist},
	}
	for _, c := range cases {
		if r, b := f.do(t, "POST", "/api/v1/downloads", c.body, nil); r.StatusCode != c.status || b["code"] != c.code {
			t.Errorf("%s → %d %v", c.body, r.StatusCode, b)
		}
	}
	for _, id := range []string{"aaaaaaaaaa1", "aaaaaaaaaa2", "aaaaaaaaaa3"} {
		f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/`+id+`"}`, nil)
	}
	if r, b := f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/aaaaaaaaaa4"}`, nil); r.StatusCode != 429 || b["code"] != CodeQuotaActive {
		t.Fatal(r.StatusCode, b)
	}
	other := uuid.New()
	for i := 0; i < 30; i++ {
		id := uuid.New()
		f.store.Jobs[id] = domain.Job{ID: id, UserID: other, Status: domain.StatusFailed, CreatedAt: time.Now()}
	}
	if r, b := f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/aaaaaaaaaa4"}`, map[string]string{"X-User-Id": other.String()}); r.StatusCode != 429 || b["code"] != CodeQuotaDaily {
		t.Fatal(r.StatusCode, b)
	}
	f.store.Fail["CountUserJobs"] = errors.New("db down")
	if r, b := f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/aaaaaaaaaa5"}`, map[string]string{"X-User-Id": uuid.NewString()}); r.StatusCode != 500 || b["code"] != CodeInternal {
		t.Fatal(r.StatusCode, b)
	}
}

func TestCreateInternal(t *testing.T) {
	f := setup(t)
	body := `{"userId":"` + f.user.String() + `","telegramId":1,"chatId":99,"url":"https://rutube.ru/video/0123456789abcdef0123456789abcdef/","statusMessageId":5,"replyToMessageId":4,"lang":"ru"}`
	r, b := f.do(t, "POST", "/internal/v1/downloads", body, map[string]string{"X-User-Id": ""})
	if r.StatusCode != 202 || b["source"] != "rutube" {
		t.Fatal(r.StatusCode, b)
	}
	for _, j := range f.store.Jobs {
		if j.Chat == nil || j.Chat.ChatID != 99 || j.Chat.StatusMessageID != 5 || j.Chat.ReplyToMessageID != 4 || j.Chat.Lang != "ru" {
			t.Fatalf("chat = %+v", j.Chat)
		}
	}
	for _, bad := range []string{`{"userId":"x","chatId":1,"url":"https://youtu.be/aaaaaaaaaa1"}`, `{"userId":"` + f.user.String() + `","url":"https://youtu.be/aaaaaaaaaa1"}`, `nope`} {
		if r, _ := f.do(t, "POST", "/internal/v1/downloads", bad, nil); r.StatusCode != 400 {
			t.Fatal(bad, r.StatusCode)
		}
	}
	if r, b := f.do(t, "POST", "/internal/v1/downloads", `{"userId":"`+f.user.String()+`","chatId":1,"url":"https://vk.com/wall1"}`, nil); r.StatusCode != 400 {
		t.Fatal(r.StatusCode, b)
	}
}

func TestSSE(t *testing.T) {
	f := setup(t)
	_, b := f.do(t, "POST", "/api/v1/downloads", `{"url":"https://youtu.be/jNQXAC9IVRw"}`, nil)
	id := uuid.MustParse(b["id"].(string))
	req, _ := http.NewRequest("GET", f.srv.URL+"/api/v1/downloads/"+id.String()+"/events", nil)
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", f.user.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header)
	}
	sc := bufio.NewScanner(resp.Body)
	var events []map[string]any
	pings := 0
	go func() {
		time.Sleep(150 * time.Millisecond)
		ctx := context.Background()
		_, _ = f.store.MarkRunning(ctx, id, domain.StageDownloading)
		_ = f.bus.SetProgress(ctx, id, domain.Progress{Stage: domain.StageDownloading, Pct: 50})
		time.Sleep(100 * time.Millisecond)
		m := domain.Media{ID: uuid.New(), ObjectKey: "k", MimeType: "video/mp4", ExpiresAt: time.Now().Add(time.Hour)}
		f.store.Media[m.ID] = m
		j, _ := f.store.FinishDone(ctx, id, m.ID, "t")
		_ = f.bus.Publish(ctx, mq.Event{Type: mq.EventDownloaded, JobID: j.ID})
	}()
	for sc.Scan() {
		line := sc.Text()
		if line == ": ping" {
			pings++
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var ev map[string]any
			_ = json.Unmarshal([]byte(data), &ev)
			events = append(events, ev)
		}
	}
	if len(events) < 3 || events[0]["status"] != "queued" || events[len(events)-1]["status"] != "done" {
		t.Fatalf("events = %v", events)
	}
	sawProgress := false
	for _, ev := range events {
		if p, ok := ev["progress"].(map[string]any); ok && p["pct"] == 50.0 {
			sawProgress = true
		}
	}
	if !sawProgress || pings == 0 {
		t.Fatalf("progress=%v pings=%d events=%v", sawProgress, pings, events)
	}
	// terminal job: one event, then the stream closes
	r, _ := f.do(t, "GET", "/api/v1/downloads/"+id.String()+"/events", "", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := f.do(t, "GET", "/api/v1/downloads/"+uuid.NewString()+"/events", "", nil); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := f.do(t, "GET", "/api/v1/downloads/x/events", "", nil); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
}

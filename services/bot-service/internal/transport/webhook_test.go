package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

type fakeBot struct {
	mu      sync.Mutex
	handled []int64
	err     error
}

func (f *fakeBot) Handle(_ context.Context, u telegram.Update, reqID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if reqID == "" {
		panic("request id required")
	}
	f.handled = append(f.handled, u.UpdateID)
	return f.err
}

const secret = "webhook-secret-123456"

func setup(t *testing.T) (*httptest.Server, *fakeBot, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	fb := &fakeBot{}
	srv := httptest.NewServer(NewRouter(Deps{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Bot: fb, Redis: rdb, Secret: secret, Path: "/tg/webhook",
	}))
	t.Cleanup(srv.Close)
	return srv, fb, mr
}

func post(t *testing.T, srv *httptest.Server, token, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/tg/webhook", strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

const update = `{"update_id":1001,"message":{"message_id":1,"chat":{"id":5,"type":"private"},"from":{"id":5,"first_name":"R"},"text":"/start"}}`

func TestWebhookSecretAndDedupe(t *testing.T) {
	srv, fb, _ := setup(t)
	if c := post(t, srv, "", update); c != 401 {
		t.Fatalf("no secret: %d", c)
	}
	if c := post(t, srv, "wrong-secret-wrong", update); c != 401 {
		t.Fatalf("wrong secret: %d", c)
	}
	if c := post(t, srv, secret, "{"); c != 400 {
		t.Fatalf("bad json: %d", c)
	}
	if c := post(t, srv, secret, `{"message":{}}`); c != 400 {
		t.Fatalf("no update_id: %d", c)
	}
	if len(fb.handled) != 0 {
		t.Fatal("nothing must be handled yet")
	}
	for i := 0; i < 3; i++ {
		if c := post(t, srv, secret, update); c != 200 {
			t.Fatalf("delivery %d: %d", i, c)
		}
	}
	if len(fb.handled) != 1 {
		t.Fatalf("redeliveries must be deduplicated: %v", fb.handled)
	}
	fb.err = errors.New("tg down")
	if c := post(t, srv, secret, strings.Replace(update, "1001", "1002", 1)); c != 200 {
		t.Fatalf("handler errors are acknowledged: %d", c)
	}
}

func TestWebhookRedisDownStillProcesses(t *testing.T) {
	srv, fb, mr := setup(t)
	mr.Close()
	if c := post(t, srv, secret, update); c != 200 || len(fb.handled) != 1 {
		t.Fatalf("got %d %v", c, fb.handled)
	}
	res, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("readyz with redis down: %d", res.StatusCode)
	}
}

func TestHealth(t *testing.T) {
	srv, _, _ := setup(t)
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/nope": 404} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
}

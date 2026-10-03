package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/api-gateway/internal/auth"
	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
	"github.com/tapenest/tapenest/services/api-gateway/internal/ratelimit"
	"github.com/tapenest/tapenest/services/api-gateway/internal/upstream"
)

const (
	botToken      = "123456:router-test-token"
	internalToken = "internal-token-internal-token"
)

type memRepo struct {
	mu   sync.Mutex
	byTG map[int64]domain.User
}

func (m *memRepo) UpsertTelegramUser(_ context.Context, p domain.TelegramProfile, role domain.Role, _ *time.Time) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byTG[p.ID]
	if !ok {
		u = domain.User{ID: uuid.New(), TelegramID: p.ID}
	}
	u.FirstName, u.Role = p.FirstName, role
	if p.LanguageCode != "" {
		lc := p.LanguageCode
		u.LanguageCode = &lc
	}
	m.byTG[p.ID] = u
	return u, nil
}

func (m *memRepo) GetByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.byTG {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

type testEnv struct {
	srv       *httptest.Server
	validator *auth.InitDataValidator
	repo      *memRepo
	mr        *miniredis.Miniredis
	now       time.Time
}

type opts struct {
	music, download string
	authBurst       int
	apiBurst        int
}

func newTestEnv(t *testing.T, o opts) *testEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	e := &testEnv{mr: mr, repo: &memRepo{byTG: map[int64]domain.User{}}, now: time.Now()}
	e.validator = auth.NewInitDataValidator(botToken, 24*time.Hour, nil)
	svc := auth.NewService(e.validator, auth.NewJWTIssuer(strings.Repeat("k", 40), 15*time.Minute, nil),
		auth.NewRefreshStore(rdb, 720*time.Hour, 15*time.Minute), e.repo, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mk := func(name, u string) *upstream.Service {
		s, err := upstream.New(name, u, upstream.Options{Timeout: 2 * time.Second, FailureThreshold: 2, OpenFor: time.Minute, InternalToken: internalToken}, upstreamError(log))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if o.authBurst == 0 {
		o.authBurst = 100
	}
	if o.apiBurst == 0 {
		o.apiBurst = 100
	}
	h := NewRouter(Deps{
		Log: log, Auth: svc,
		AuthLimiter:   ratelimit.New(rdb, "t:auth", 0.001, o.authBurst, nil),
		APILimiter:    ratelimit.New(rdb, "t:api", 0.001, o.apiBurst, nil),
		CORSOrigins:   []string{"https://app.example", "https://*.ngrok-free.app"},
		TrustProxy:    true,
		InternalToken: internalToken,
		Ready: map[string]Pinger{
			"redis":    PingFunc(func(ctx context.Context) error { return rdb.Ping(ctx).Err() }),
			"postgres": PingFunc(func(context.Context) error { return nil }),
		},
		Music: mk("music", o.music), Download: mk("download", o.download), Streaming: mk("streaming", ""),
	})
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *testEnv) initData(t *testing.T, tgID int64, authDate time.Time) string {
	t.Helper()
	v := url.Values{}
	v.Set("user", `{"id":`+strconv.FormatInt(tgID, 10)+`,"first_name":"Roma","language_code":"en"}`)
	v.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	v.Set("signature", "sig")
	v.Set("hash", e.validator.Sign(v))
	return v.Encode()
}

type resp struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (e *testEnv) do(t *testing.T, method, path string, body any, hdr map[string]string) resp {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = strings.NewReader(string(j))
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (e *testEnv) login(t *testing.T, tgID int64) AuthTokensDTO {
	t.Helper()
	r := e.do(t, "POST", "/api/v1/auth/telegram", map[string]string{"initData": e.initData(t, tgID, time.Now())}, nil)
	if r.status != 200 {
		t.Fatalf("login: %d %s", r.status, r.raw)
	}
	var tok AuthTokensDTO
	if err := json.Unmarshal([]byte(r.raw), &tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func TestAuthFlow(t *testing.T) {
	e := newTestEnv(t, opts{})
	tok := e.login(t, 42)
	if _, err := uuid.Parse(tok.User.ID); err != nil || tok.User.TelegramID != 42 || tok.ExpiresIn < 890 || tok.ExpiresIn > 900 {
		t.Fatalf("contract: %+v", tok)
	}
	if tok.User.LanguageCode == nil || *tok.User.LanguageCode != "en" {
		t.Fatal("languageCode must be returned")
	}
	me := e.do(t, "GET", "/api/v1/me", nil, bearer(tok.AccessToken))
	if me.status != 200 || me.body["id"] != tok.User.ID {
		t.Fatalf("me: %d %s", me.status, me.raw)
	}

	ref := e.do(t, "POST", "/api/v1/auth/refresh", map[string]string{"refreshToken": tok.RefreshToken}, nil)
	if ref.status != 200 || ref.body["refreshToken"] == tok.RefreshToken {
		t.Fatalf("refresh: %d %s", ref.status, ref.raw)
	}
	newRefresh, _ := ref.body["refreshToken"].(string)
	reuse := e.do(t, "POST", "/api/v1/auth/refresh", map[string]string{"refreshToken": tok.RefreshToken}, nil)
	if reuse.status != 401 || reuse.body["code"] != CodeRefreshReused {
		t.Fatalf("reuse: %d %s", reuse.status, reuse.raw)
	}
	after := e.do(t, "POST", "/api/v1/auth/refresh", map[string]string{"refreshToken": newRefresh}, nil)
	if after.status != 401 || after.body["code"] != CodeRefreshInvalid {
		t.Fatalf("revoked family: %d %s", after.status, after.raw)
	}

	tok2 := e.login(t, 42)
	if tok2.User.ID != tok.User.ID {
		t.Fatal("stable UUID per telegram user")
	}
	if r := e.do(t, "POST", "/api/v1/auth/logout", map[string]string{"refreshToken": tok2.RefreshToken}, nil); r.status != 204 {
		t.Fatalf("logout: %d", r.status)
	}
	if r := e.do(t, "GET", "/api/v1/me", nil, bearer(tok2.AccessToken)); r.status != 401 || r.body["code"] != CodeUnauthorized {
		t.Fatalf("after logout: %d %s", r.status, r.raw)
	}
}

func TestAuthErrors(t *testing.T) {
	e := newTestEnv(t, opts{})
	cases := []struct {
		name, path string
		body       any
		status     int
		code       string
	}{
		{"expired initData", "/api/v1/auth/telegram", map[string]string{"initData": e.initData(t, 1, time.Now().Add(-25*time.Hour))}, 401, CodeInitDataExpired},
		{"future initData", "/api/v1/auth/telegram", map[string]string{"initData": e.initData(t, 1, time.Now().Add(time.Hour))}, 401, CodeInitDataExpired},
		{"forged initData", "/api/v1/auth/telegram", map[string]string{"initData": strings.Replace(e.initData(t, 1, time.Now()), "Roma", "Eve", 1)}, 401, CodeInvalidInitData},
		{"garbage initData", "/api/v1/auth/telegram", map[string]string{"initData": "hello"}, 401, CodeInvalidInitData},
		{"empty initData", "/api/v1/auth/telegram", map[string]string{"initData": ""}, 400, CodeInvalid},
		{"bad json", "/api/v1/auth/telegram", "{", 400, CodeInvalid},
		{"unknown field", "/api/v1/auth/telegram", map[string]string{"initData": "x", "evil": "1"}, 400, CodeInvalid},
		{"trailing data", "/api/v1/auth/telegram", `{"initData":"x"}{}`, 400, CodeInvalid},
		{"short refresh", "/api/v1/auth/refresh", map[string]string{"refreshToken": "x"}, 400, CodeInvalid},
		{"unknown refresh", "/api/v1/auth/refresh", map[string]string{"refreshToken": strings.Repeat("a", 43)}, 401, CodeRefreshInvalid},
		{"logout without token", "/api/v1/auth/logout", map[string]string{}, 400, CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := e.do(t, "POST", tc.path, tc.body, nil)
			if r.status != tc.status || r.body["code"] != tc.code {
				t.Fatalf("got %d %s", r.status, r.raw)
			}
		})
	}
	for name, hdr := range map[string]map[string]string{
		"no token": nil, "basic": {"Authorization": "Basic x"}, "garbage": bearer("x.y.z"),
	} {
		if r := e.do(t, "GET", "/api/v1/me", nil, hdr); r.status != 401 || r.body["code"] != CodeUnauthorized {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
	// Deleted user with a still-valid access token.
	tok := e.login(t, 5)
	e.repo.mu.Lock()
	delete(e.repo.byTG, 5)
	e.repo.mu.Unlock()
	if r := e.do(t, "GET", "/api/v1/me", nil, bearer(tok.AccessToken)); r.status != 401 {
		t.Fatalf("deleted user: %d", r.status)
	}
}

func TestExpiredAccessTokenCode(t *testing.T) {
	e := newTestEnv(t, opts{})
	old := auth.NewJWTIssuer(strings.Repeat("k", 40), time.Minute, func() time.Time { return time.Now().Add(-time.Hour) })
	tok, _, _ := old.Issue(auth.Principal{UserID: uuid.New(), TelegramID: 1, Role: domain.RoleUser, SessionID: "s"})
	if r := e.do(t, "GET", "/api/v1/me", nil, bearer(tok)); r.status != 401 || r.body["code"] != CodeTokenExpired {
		t.Fatalf("got %d %s", r.status, r.raw)
	}
}

func TestRateLimits(t *testing.T) {
	e := newTestEnv(t, opts{authBurst: 2, apiBurst: 3})
	body := map[string]string{"initData": "x"}
	hdr := map[string]string{"X-Forwarded-For": "203.0.113.7"}
	for i := 0; i < 2; i++ {
		if r := e.do(t, "POST", "/api/v1/auth/telegram", body, hdr); r.status != 401 {
			t.Fatalf("attempt %d: %d", i, r.status)
		}
	}
	r := e.do(t, "POST", "/api/v1/auth/telegram", body, hdr)
	if r.status != 429 || r.body["code"] != CodeRateLimited || r.header.Get("Retry-After") == "" {
		t.Fatalf("auth limit: %d %v", r.status, r.header)
	}
	other := e.do(t, "POST", "/api/v1/auth/telegram", body, map[string]string{"X-Forwarded-For": "198.51.100.1"})
	if other.status != 401 {
		t.Fatal("limit is per IP")
	}

	e2 := newTestEnv(t, opts{apiBurst: 2})
	tok := e2.login(t, 9)
	for i := 0; i < 2; i++ {
		if r := e2.do(t, "GET", "/api/v1/me", nil, bearer(tok.AccessToken)); r.status != 200 {
			t.Fatalf("api %d: %d", i, r.status)
		}
	}
	if r := e2.do(t, "GET", "/api/v1/me", nil, bearer(tok.AccessToken)); r.status != 429 {
		t.Fatalf("api limit per user: %d", r.status)
	}
	e2.mr.Close() // Redis down → fail open for the limiter, but auth needs Redis → 503
	if r := e2.do(t, "GET", "/api/v1/me", nil, bearer(tok.AccessToken)); r.status != 503 {
		t.Fatalf("redis down: %d %s", r.status, r.raw)
	}
}

func TestCORS(t *testing.T) {
	e := newTestEnv(t, opts{})
	pre := func(origin string) resp {
		return e.do(t, "OPTIONS", "/api/v1/auth/telegram", nil, map[string]string{
			"Origin": origin, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type",
		})
	}
	for _, o := range []string{"https://app.example", "https://abcd-1-2-3-4.ngrok-free.app"} {
		r := pre(o)
		if r.status != 204 || r.header.Get("Access-Control-Allow-Origin") != o || !strings.Contains(r.header.Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Fatalf("%s: %d %v", o, r.status, r.header)
		}
	}
	for _, o := range []string{"https://evil.example", "https://ngrok-free.app.evil.com", "http://x.ngrok-free.app"} {
		if r := pre(o); r.status != 403 || r.header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s must be rejected: %d", o, r.status)
		}
	}
	r := e.do(t, "POST", "/api/v1/auth/telegram", map[string]string{"initData": "x"}, map[string]string{"Origin": "https://app.example"})
	if r.header.Get("Access-Control-Allow-Origin") != "https://app.example" || !strings.Contains(r.header.Get("Access-Control-Expose-Headers"), "X-Request-Id") {
		t.Fatalf("simple request CORS headers: %v", r.header)
	}
	r = e.do(t, "POST", "/api/v1/auth/telegram", map[string]string{"initData": "x"}, map[string]string{"Origin": "https://evil.example"})
	if r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no ACAO for foreign origins")
	}
}

func TestHealthAndRequestID(t *testing.T) {
	e := newTestEnv(t, opts{})
	if r := e.do(t, "GET", "/healthz", nil, nil); r.status != 200 || r.header.Get("X-Request-Id") == "" {
		t.Fatalf("healthz: %d", r.status)
	}
	if r := e.do(t, "GET", "/readyz", nil, map[string]string{"X-Request-Id": "trace-12345678"}); r.status != 200 || r.header.Get("X-Request-Id") != "trace-12345678" {
		t.Fatalf("readyz: %d %v", r.status, r.header)
	}
	if r := e.do(t, "GET", "/healthz", nil, map[string]string{"X-Request-Id": "bad id;<script>"}); r.header.Get("X-Request-Id") == "bad id;<script>" {
		t.Fatal("unsafe request ids are replaced")
	}
	e.mr.Close()
	r := e.do(t, "GET", "/readyz", nil, nil)
	checks, _ := r.body["checks"].(map[string]any)
	if r.status != 503 || checks["redis"] != "down" || checks["postgres"] != "ok" {
		t.Fatalf("degraded: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "GET", "/nope", nil, nil); r.status != 404 || r.body["code"] != CodeNotFound {
		t.Fatalf("404: %d", r.status)
	}
	if r := e.do(t, "GET", "/api/v1/auth/telegram", nil, nil); r.status != 405 {
		t.Fatalf("405: %d", r.status)
	}
}

func TestUpstreamRouting(t *testing.T) {
	var got http.Header
	var gotPath string
	music := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotPath = r.Header.Clone(), r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"nextCursor":null}`))
	}))
	defer music.Close()
	e := newTestEnv(t, opts{music: music.URL})
	tok := e.login(t, 77)

	r := e.do(t, "GET", "/api/v1/tracks/popular?limit=5", nil, bearer(tok.AccessToken))
	if r.status != 200 || gotPath != "/api/v1/tracks/popular?limit=5" {
		t.Fatalf("proxy: %d %s %s", r.status, gotPath, r.raw)
	}
	if got.Get("Authorization") != "" || got.Get("X-User-Id") != tok.User.ID || got.Get("X-Telegram-Id") != "77" || got.Get("X-Request-Id") == "" {
		t.Fatalf("identity headers: %v", got)
	}
	if r := e.do(t, "GET", "/api/v1/tracks/popular", nil, nil); r.status != 401 {
		t.Fatal("proxied routes require auth")
	}
	// Not deployed → honest 501 with service name.
	r = e.do(t, "POST", "/api/v1/downloads", map[string]string{"url": "https://youtu.be/x"}, bearer(tok.AccessToken))
	if r.status != 501 || r.body["code"] != CodeNotImplemented || r.body["service"] != "download" {
		t.Fatalf("501: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "GET", "/api/v1/cinema/titles", nil, bearer(tok.AccessToken)); r.status != 501 || r.body["service"] != "streaming" {
		t.Fatalf("streaming 501: %d", r.status)
	}
	if r := e.do(t, "GET", "/api/v1/video/projects", nil, bearer(tok.AccessToken)); r.status != 501 || r.body["service"] != "video" {
		t.Fatalf("video 501: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "GET", "/api/v1/photos", nil, bearer(tok.AccessToken)); r.status != 501 || r.body["service"] != "photo" {
		t.Fatalf("photo 501: %d %s", r.status, r.raw)
	}
	// Configured but down → 503 SERVICE_UNAVAILABLE, then the breaker opens.
	music.Close()
	for i := 0; i < 3; i++ {
		r = e.do(t, "GET", "/api/v1/playlists", nil, bearer(tok.AccessToken))
		if r.status != 503 || r.body["code"] != CodeServiceUnavailable || r.body["service"] != "music" {
			t.Fatalf("503 #%d: %d %s", i, r.status, r.raw)
		}
	}
}

func TestUpstream5xxPassThrough(t *testing.T) {
	calls := 0
	music := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"SERVICE_UNAVAILABLE","service":"navidrome"}`))
	}))
	defer music.Close()
	e := newTestEnv(t, opts{music: music.URL})
	tok := e.login(t, 1)
	for i := 0; i < 2; i++ {
		if r := e.do(t, "GET", "/api/v1/tracks/recent", nil, bearer(tok.AccessToken)); r.status != 503 || r.body["service"] != "navidrome" {
			t.Fatalf("pass-through: %d %s", r.status, r.raw)
		}
	}
	r := e.do(t, "GET", "/api/v1/tracks/recent", nil, bearer(tok.AccessToken))
	if r.status != 503 || r.body["service"] != "music" || calls != 2 {
		t.Fatalf("breaker must open after 2 failures: %d calls=%d %s", r.status, calls, r.raw)
	}
}

func TestInternalBotDownload(t *testing.T) {
	e := newTestEnv(t, opts{})
	body := map[string]any{
		"telegramId": 42, "chatId": 42, "url": "https://youtu.be/dQw4w9WgXcQ", "firstName": "Roma", "languageCode": "ru",
		"statusMessageId": 7, "replyToMessageId": 6,
	}
	if r := e.do(t, "POST", "/internal/v1/bot/downloads", body, nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := e.do(t, "POST", "/internal/v1/bot/downloads", body, bearer("wrong-token-wrong-token-wrong")); r.status != 401 {
		t.Fatalf("wrong token: %d", r.status)
	}
	for name, b := range map[string]map[string]any{
		"ftp url":  {"telegramId": 1, "chatId": 1, "url": "ftp://x.example/a"},
		"not url":  {"telegramId": 1, "chatId": 1, "url": "hello world"},
		"no tg id": {"chatId": 1, "url": "https://youtu.be/x"},
		"no chat":  {"telegramId": 1, "url": "https://youtu.be/x"},
	} {
		if r := e.do(t, "POST", "/internal/v1/bot/downloads", b, bearer(internalToken)); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
	r := e.do(t, "POST", "/internal/v1/bot/downloads", body, bearer(internalToken))
	if r.status != 501 || r.body["code"] != CodeNotImplemented || !strings.Contains(r.raw, "DOWNLOAD_SERVICE_URL") {
		t.Fatalf("501: %d %s", r.status, r.raw)
	}
	if _, err := e.repo.GetByID(context.Background(), e.repo.byTG[42].ID); err != nil {
		t.Fatal("bot path must upsert the user")
	}

	var fwd map[string]any
	var fwdUser, fwdToken string
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/downloads" {
			w.WriteHeader(404)
			return
		}
		fwdUser, fwdToken = r.Header.Get("X-User-Id"), r.Header.Get("X-Internal-Token")
		_ = json.NewDecoder(r.Body).Decode(&fwd)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"jobId":"j1","status":"queued"}`))
	}))
	defer dl.Close()
	e2 := newTestEnv(t, opts{download: dl.URL})
	r = e2.do(t, "POST", "/internal/v1/bot/downloads", body, bearer(internalToken))
	if r.status != 202 || r.body["jobId"] != "j1" || fwd["url"] != body["url"] || fwdUser == "" || fwd["userId"] != fwdUser {
		t.Fatalf("forward: %d %s %v", r.status, r.raw, fwd)
	}
	if fwdToken != internalToken || fwd["statusMessageId"] != 7.0 || fwd["replyToMessageId"] != 6.0 || fwd["lang"] != "ru" {
		t.Fatalf("forwarded token/fields: %v", fwd)
	}
	dl.Close()
	if r := e2.do(t, "POST", "/internal/v1/bot/downloads", body, bearer(internalToken)); r.status != 503 {
		t.Fatalf("download down: %d", r.status)
	}
}

func TestRecoverMiddleware(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := recoverMiddleware(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(errors.New("boom")) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), CodeInternal) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")
	if got := clientIP(true)(r); got != "203.0.113.9" {
		t.Fatal(got)
	}
	if got := clientIP(false)(r); got != "10.0.0.1" {
		t.Fatal(got)
	}
	r.Header.Set("X-Forwarded-For", "garbage")
	if got := clientIP(true)(r); got != "10.0.0.1" {
		t.Fatal(got)
	}
}

func TestDegradedDependencyDoesNotTripBreaker(t *testing.T) {
	calls := 0
	music := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("X-Degraded-Dependency", "navidrome")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"SERVICE_UNAVAILABLE","service":"streaming"}`))
	}))
	defer music.Close()
	e := newTestEnv(t, opts{music: music.URL})
	tok := e.login(t, 1)
	for i := 0; i < 5; i++ {
		r := e.do(t, "GET", "/api/v1/tracks/x/stream-url", nil, bearer(tok.AccessToken))
		if r.status != 503 || r.body["service"] != "streaming" {
			t.Fatalf("#%d: %d %s", i, r.status, r.raw)
		}
	}
	if calls != 5 {
		t.Fatalf("breaker must stay closed: calls=%d", calls)
	}
}

func TestPublicSignedStreamRoute(t *testing.T) {
	var gotPath, gotUser, gotToken, gotMethod string
	music := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUser, gotToken, gotMethod = r.URL.RequestURI(), r.Header.Get("X-User-Id"), r.Header.Get("X-Internal-Token"), r.Method
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Content-Range", "bytes 0-1/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("ab"))
	}))
	defer music.Close()
	e := newTestEnv(t, opts{music: music.URL})
	// No bearer token: the signature in the query is the credential.
	r := e.do(t, "GET", "/api/v1/stream/tracks/abc?exp=1&u=x&sig=s", nil, map[string]string{"Range": "bytes=0-1", "X-User-Id": "spoofed"})
	if r.status != 206 || gotPath != "/api/v1/stream/tracks/abc?exp=1&u=x&sig=s" || r.header.Get("Content-Range") != "bytes 0-1/10" {
		t.Fatalf("stream: %d %s", r.status, gotPath)
	}
	if gotToken == "" || gotUser != "" {
		t.Fatalf("internal token injected, client identity stripped: token=%v user=%q", gotToken != "", gotUser)
	}
	if r := e.do(t, "HEAD", "/api/v1/stream/covers/al-1?size=300&sig=s", nil, nil); r.status != 206 || gotMethod != "HEAD" {
		t.Fatalf("head: %d %s", r.status, gotMethod)
	}
	if r := e.do(t, "POST", "/api/v1/stream/tracks/abc", nil, nil); r.status != 405 {
		t.Fatalf("only GET/HEAD: %d", r.status)
	}
}

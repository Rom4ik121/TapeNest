// Package ytdlp resolves a YouTube video to a temporary googlevideo URL and
// proxies it. Audio is not written to disk. The Android player client is
// required: the default web client is rejected by YouTube as a bot.
package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

const androidUA = "com.google.android.youtube/19.44.38 (Linux; U; Android 14)"

// Resolver shells out to yt-dlp and proxies the resulting URL.
type Resolver struct {
	Bin  string
	HTTP *http.Client

	mu     sync.Mutex
	cache  map[string]cached
	flying map[string]*resolveCall
}

type cached struct {
	url string
	exp time.Time
}

// resolveCall is one in-flight yt-dlp. Prefetch and playback share it, and a
// cancelled prefetch does not cancel the lookup the player is about to need.
type resolveCall struct {
	done chan struct{}
	url  string
	err  error
}

// New builds a resolver. bin defaults to "yt-dlp" on PATH.
func New(bin string) *Resolver {
	if strings.TrimSpace(bin) == "" {
		bin = "yt-dlp"
	}
	return &Resolver{
		Bin:    bin,
		HTTP:   &http.Client{Timeout: 0, CheckRedirect: checkRedirect},
		cache:  map[string]cached{},
		flying: map[string]*resolveCall{},
	}
}

func checkRedirect(req *http.Request, _ []*http.Request) error {
	if !allowedStreamHost(req.URL.Hostname()) {
		return errors.New("refusing redirect off googlevideo")
	}
	return nil
}

func allowedStreamHost(h string) bool {
	h = strings.ToLower(h)
	return h == "googlevideo.com" || strings.HasSuffix(h, ".googlevideo.com")
}

// Resolve returns a googlevideo URL for videoID. The URL is cached until
// shortly before its expire parameter. Concurrent calls share one yt-dlp run.
func (r *Resolver) Resolve(ctx context.Context, videoID string) (string, error) {
	if !ytm.ValidVideoID(videoID) {
		return "", errors.New("invalid video id")
	}
	r.mu.Lock()
	if c, ok := r.cache[videoID]; ok && time.Now().Before(c.exp) {
		u := c.url
		r.mu.Unlock()
		return u, nil
	}
	if f, ok := r.flying[videoID]; ok {
		r.mu.Unlock()
		return waitResolve(ctx, f)
	}
	f := &resolveCall{done: make(chan struct{})}
	r.flying[videoID] = f
	r.mu.Unlock()
	go r.fulfill(f, videoID)
	return waitResolve(ctx, f)
}

func waitResolve(ctx context.Context, f *resolveCall) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-f.done:
		return f.url, f.err
	}
}

func (r *Resolver) fulfill(f *resolveCall, videoID string) {
	defer func() {
		close(f.done)
		r.mu.Lock()
		if r.flying[videoID] == f {
			delete(r.flying, videoID)
		}
		r.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	u, err := r.resolveOnce(ctx, videoID)
	f.url, f.err = u, err
}

func (r *Resolver) resolveOnce(ctx context.Context, videoID string) (string, error) {
	// #nosec G204 -- videoID is 11 url-safe chars; arguments are fixed.
	cmd := exec.CommandContext(ctx, r.Bin, //nolint:gosec // validated id, fixed args
		"--no-warnings", "--no-playlist", "--no-progress",
		"--extractor-args", "youtube:player_client=android",
		"-f", "ba[ext=m4a]/ba/b",
		"-g", "--",
		"https://www.youtube.com/watch?v="+videoID,
	)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("yt-dlp failed")
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	u, err := url.Parse(line)
	if err != nil || u.Scheme != "https" || !allowedStreamHost(u.Hostname()) {
		return "", errors.New("yt-dlp returned an unexpected url")
	}
	r.store(videoID, u)
	return u.String(), nil
}

func (r *Resolver) store(id string, u *url.URL) {
	exp := time.Now().Add(30 * time.Minute)
	if s := u.Query().Get("expire"); s != "" {
		if unix, err := strconv.ParseInt(s, 10, 64); err == nil {
			t := time.Unix(unix, 0).Add(-time.Minute)
			if t.After(time.Now().Add(time.Minute)) {
				exp = t
			}
		}
	}
	r.mu.Lock()
	r.cache[id] = cached{url: u.String(), exp: exp}
	r.mu.Unlock()
}

// Proxy streams videoID to w. Range is forwarded. Nothing is saved to disk.
func (r *Resolver) Proxy(w http.ResponseWriter, req *http.Request, videoID string) error {
	raw, err := r.Resolve(req.Context(), videoID)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil || !allowedStreamHost(parsed.Hostname()) {
		return errors.New("refusing stream host")
	}
	up, err := http.NewRequestWithContext(req.Context(), req.Method, raw, nil)
	if err != nil {
		return err
	}
	if rng := req.Header.Get("Range"); rng != "" {
		up.Header.Set("Range", rng)
	}
	up.Header.Set("User-Agent", androidUA)
	resp, err := r.http().Do(up) //nolint:gosec // G704: host is restricted to googlevideo.com above
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("stream upstream status %d", resp.StatusCode)
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	if req.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(w, resp.Body)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (r *Resolver) http() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return http.DefaultClient
}

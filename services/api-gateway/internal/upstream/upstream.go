// Package upstream proxies requests to internal services with timeouts and a
// circuit breaker (spec §5.2, §8). An unavailable service yields a structured
// error so the mini app degrades per section instead of failing entirely.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/sony/gobreaker"
)

// ErrNotConfigured means the service is not deployed in this environment
// (e.g. download-service before stage 2).
var ErrNotConfigured = errors.New("upstream not configured")

// ErrUnavailable means the service is configured but failing or its breaker is open.
var ErrUnavailable = errors.New("upstream unavailable")

// ErrorWriter renders an upstream failure (not configured / unavailable).
type ErrorWriter func(w http.ResponseWriter, r *http.Request, service string, err error)

// Service is one proxied backend.
type Service struct {
	Name    string
	base    *url.URL
	proxy   *httputil.ReverseProxy
	breaker *gobreaker.CircuitBreaker
	client  *http.Client
	token   string
}

// Options tune timeouts and breaker behaviour.
type Options struct {
	Timeout          time.Duration // response-header timeout (bodies may stream, e.g. audio)
	FailureThreshold uint32        // consecutive failures that open the breaker
	OpenFor          time.Duration // how long the breaker stays open
	// InternalToken is sent as X-Internal-Token on every upstream request
	// (services accept only gateway traffic); a client-supplied value is dropped.
	InternalToken string
}

// New builds a service. An empty rawURL yields a service that always reports ErrNotConfigured.
func New(name, rawURL string, opt Options, onError ErrorWriter) (*Service, error) {
	s := &Service{Name: name}
	if rawURL == "" {
		return s, nil
	}
	base, err := url.Parse(rawURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("upstream %s: bad url", name)
	}
	if opt.FailureThreshold == 0 {
		opt.FailureThreshold = 5
	}
	if opt.OpenFor == 0 {
		opt.OpenFor = 15 * time.Second
	}
	s.base, s.token = base, opt.InternalToken
	s.breaker = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		Timeout:     opt.OpenFor,
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= opt.FailureThreshold },
	})
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: opt.Timeout,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
	}
	rt := &breakerTransport{next: transport, cb: s.breaker}
	s.client = &http.Client{Transport: rt, Timeout: opt.Timeout}
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			pr.SetXForwarded()
			pr.Out.Header.Del("Authorization") // upstreams trust gateway identity headers
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("X-Internal-Token")
			if opt.InternalToken != "" {
				pr.Out.Header.Set("X-Internal-Token", opt.InternalToken)
			}
		},
		Transport: rt,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			onError(w, r, name, fmt.Errorf("%w: %w", ErrUnavailable, err))
		},
	}
	return s, nil
}

// Configured reports whether the service has a URL.
func (s *Service) Configured() bool { return s.base != nil }

// Handler proxies the request as-is (path unchanged).
func (s *Service) Handler(onError ErrorWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Configured() {
			onError(w, r, s.Name, ErrNotConfigured)
			return
		}
		s.proxy.ServeHTTP(w, r)
	})
}

// Do sends a request to the service (for gateway-owned handlers that call upstreams).
func (s *Service) Do(ctx context.Context, method, path string, header http.Header, body []byte) (*http.Response, error) {
	if !s.Configured() {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base.JoinPath(path).String(), bytesReader(body))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if s.token != "" {
		req.Header.Set("X-Internal-Token", s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return resp, nil
}

// DegradedHeader marks a 503 caused by a dependency *behind* the upstream
// (e.g. music-service answering "streaming unavailable" while Navidrome is down).
// The upstream itself is healthy, so such responses do not trip the breaker.
const DegradedHeader = "X-Degraded-Dependency"

// breakerTransport counts network errors and 502/503/504 as failures
// (except a 503 carrying DegradedHeader).
type breakerTransport struct {
	next http.RoundTripper
	cb   *gobreaker.CircuitBreaker
}

var errBadGateway = errors.New("upstream returned 5xx")

func (t *breakerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var resp *http.Response
	_, err := t.cb.Execute(func() (any, error) {
		var err error
		resp, err = t.next.RoundTrip(r) //nolint:bodyclose // returned to the caller
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get(DegradedHeader) != "" {
			return nil, nil
		}
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return nil, errBadGateway // response is still passed through below
		}
		return nil, nil
	})
	if resp != nil && errors.Is(err, errBadGateway) {
		return resp, nil
	}
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	return resp, nil
}

// Package proxy manages the download proxy pools (spec §5.3): datacenter,
// residential and mobile, round-robin rotation over healthy entries and a
// periodic health check. Empty pools mean direct connections.
package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

type entry struct {
	url     string
	healthy atomic.Bool
}

// Pools holds the three tiers.
type Pools struct {
	tiers map[domain.ProxyTier][]*entry
	next  map[domain.ProxyTier]*atomic.Uint64
	mu    sync.Mutex // guards nothing hot; used by Check to serialize runs
}

// New builds pools; every proxy starts healthy.
func New(datacenter, residential, mobile []string) *Pools {
	p := &Pools{tiers: map[domain.ProxyTier][]*entry{}, next: map[domain.ProxyTier]*atomic.Uint64{}}
	for tier, list := range map[domain.ProxyTier][]string{domain.TierDatacenter: datacenter, domain.TierResidential: residential, domain.TierMobile: mobile} {
		p.next[tier] = &atomic.Uint64{}
		for _, u := range list {
			e := &entry{url: u}
			e.healthy.Store(true)
			p.tiers[tier] = append(p.tiers[tier], e)
		}
	}
	return p
}

// escalation order when a tier has no healthy proxy: go up, then down, then direct.
var fallback = map[domain.ProxyTier][]domain.ProxyTier{
	domain.TierDirect:      {domain.TierDirect},
	domain.TierDatacenter:  {domain.TierDatacenter, domain.TierResidential, domain.TierMobile},
	domain.TierResidential: {domain.TierResidential, domain.TierMobile, domain.TierDatacenter},
	domain.TierMobile:      {domain.TierMobile, domain.TierResidential, domain.TierDatacenter},
}

// Pick returns a proxy URL for the tier ("" = direct). avoid is the proxy used by
// the previous attempt (NewProxy in the retry plan); it is skipped when possible.
func (p *Pools) Pick(tier domain.ProxyTier, avoid string) (string, domain.ProxyTier) {
	for _, t := range fallback[tier] {
		list := p.tiers[t]
		if len(list) == 0 {
			continue
		}
		start := p.next[t].Add(1)
		var fallbackURL string
		for i := range list {
			e := list[(start+uint64(i))%uint64(len(list))]
			if !e.healthy.Load() {
				continue
			}
			if e.url == avoid {
				fallbackURL = e.url
				continue
			}
			return e.url, t
		}
		if fallbackURL != "" {
			return fallbackURL, t
		}
	}
	return "", domain.TierDirect
}

// Healthy returns healthy/total per tier (metrics, logs).
func (p *Pools) Healthy() map[domain.ProxyTier][2]int {
	out := map[domain.ProxyTier][2]int{}
	for t, list := range p.tiers {
		h := 0
		for _, e := range list {
			if e.healthy.Load() {
				h++
			}
		}
		out[t] = [2]int{h, len(list)}
	}
	return out
}

// Check probes every proxy with a GET of target (expects 2xx) and updates health.
func (p *Pools) Check(ctx context.Context, target string, timeout time.Duration, log *slog.Logger) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var wg sync.WaitGroup
	for tier, list := range p.tiers {
		for _, e := range list {
			wg.Add(1)
			go func(tier domain.ProxyTier, e *entry) {
				defer wg.Done()
				ok := probe(ctx, e.url, target, timeout)
				if was := e.healthy.Swap(ok); was != ok {
					log.WarnContext(ctx, "proxy health changed", "tier", tier, "proxy", redact(e.url), "healthy", ok)
				}
			}(tier, e)
		}
	}
	wg.Wait()
}

// Run checks health every interval until ctx is done (spec: 5 min).
func (p *Pools) Run(ctx context.Context, target string, every time.Duration, log *slog.Logger) {
	if len(p.tiers) == 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p.Check(ctx, target, 10*time.Second, log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func probe(ctx context.Context, proxyURL, target string, timeout time.Duration) bool {
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return false
	}
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// redact hides proxy credentials in logs.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid"
	}
	u.User = nil
	return u.String()
}

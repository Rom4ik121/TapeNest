package domain

import (
	"regexp"
	"strings"
	"time"
)

// ErrorKind classifies a failed download (spec §5.3) and drives the retry strategy.
type ErrorKind string

// Error kinds. Kinds without retry end the job as failed immediately.
const (
	KindUnavailable ErrorKind = "unavailable"   // removed / not found
	KindPrivate     ErrorKind = "private"       // private, members-only, login required
	KindDRM         ErrorKind = "drm_protected" // never bypassed (spec §14)
	KindGeoBlocked  ErrorKind = "geo_blocked"
	KindBotCheck    ErrorKind = "bot_check" // "confirm you're not a bot", captcha
	KindRateLimited ErrorKind = "rate_limited"
	KindNetwork     ErrorKind = "network"
	KindTooLarge    ErrorKind = "too_large" // over DOWNLOAD_MAX_FILESIZE / duration
	KindLive        ErrorKind = "live"      // live streams / premieres are not downloadable
	KindUnsupported ErrorKind = "unsupported"
	KindInternal    ErrorKind = "internal"
)

// ordered: the first match wins (DRM before "unavailable", etc.)
var classifiers = []struct {
	kind ErrorKind
	re   *regexp.Regexp
}{
	{KindDRM, regexp.MustCompile(`(?i)\bdrm\b`)},
	{KindLive, regexp.MustCompile(`(?i)(is (currently )?live|live event will begin|premieres in|this live event|is_live|live stream recording is not available)`)},
	{KindTooLarge, regexp.MustCompile(`(?i)(larger than max-filesize|file is larger than|exceeds the maximum)`)},
	{KindBotCheck, regexp.MustCompile(`(?i)(not a bot|captcha|confirm you.?re not|unusual traffic|po token)`)},
	{KindGeoBlocked, regexp.MustCompile(`(?i)((made this video|not) available in your country|geo.?restrict|blocked it in your country|not available from your location|in your region)`)},
	{KindPrivate, regexp.MustCompile(`(?i)(private video|video is private|members.only|join this channel|login required|sign in to view|requires authentication|age.restricted|confirm your age|access restricted|closed video)`)},
	{KindRateLimited, regexp.MustCompile(`(?i)(http error 429|too many requests|rate.?limit)`)},
	{KindUnsupported, regexp.MustCompile(`(?i)unsupported url`)},
	{KindUnavailable, regexp.MustCompile(`(?i)(video unavailable|has been removed|no longer available|does not exist|http error 404|not found|video is unavailable|was deleted|account .* terminated|requested format is not available|no video formats found)`)},
	{KindNetwork, regexp.MustCompile(`(?i)(timed out|timeout|connection (reset|refused|aborted)|temporary failure in name resolution|name or service not known|unable to download (webpage|video data)|http error 5\d\d|incompleteread|network is unreachable|ssl: |remote end closed|errno 104|got error: )`)},
}

// Classify maps yt-dlp stderr (ERROR lines first) to an ErrorKind.
func Classify(stderr string) ErrorKind {
	msg := stderr
	var errs []string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "ERROR:") {
			errs = append(errs, l)
		}
	}
	if len(errs) > 0 {
		msg = strings.Join(errs, "\n")
	}
	for _, c := range classifiers {
		if c.re.MatchString(msg) {
			return c.kind
		}
	}
	return KindInternal
}

// ProxyTier is a proxy pool class (spec §5.3), cheapest first.
type ProxyTier string

// Pools. TierDirect means no proxy.
const (
	TierDirect      ProxyTier = "direct"
	TierDatacenter  ProxyTier = "datacenter"
	TierResidential ProxyTier = "residential"
	TierMobile      ProxyTier = "mobile"
)

// Attempt describes how to run the next try.
type Attempt struct {
	Tier        ProxyTier
	NewProxy    bool // pick a different proxy than the previous attempt
	WithCookies bool
	MobileUA    bool
	Delay       time.Duration
}

// Strategy says whether and how to retry after an error.
type Strategy struct {
	Retry bool
	Next  Attempt
}

// MaxAttempts is the hard cap of tries per job (spec §8: retries ≤ 3).
const MaxAttempts = 4

// Plan returns the strategy after `attempt` (1-based) failed with `kind`
// (spec §5.3). Escalation ladder: same proxy → other proxy + cookies →
// mobile + other UA; geo → residential; 429 → exponential backoff + new proxy.
func Plan(kind ErrorKind, attempt int) Strategy {
	if attempt >= MaxAttempts {
		return Strategy{}
	}
	backoff := func(base time.Duration) time.Duration { return base << (attempt - 1) }
	ladder := func() Attempt {
		switch attempt {
		case 1:
			return Attempt{Tier: TierDatacenter, Delay: backoff(2 * time.Second)}
		case 2:
			return Attempt{Tier: TierDatacenter, NewProxy: true, WithCookies: true, Delay: backoff(2 * time.Second)}
		default:
			return Attempt{Tier: TierMobile, NewProxy: true, WithCookies: true, MobileUA: true, Delay: backoff(2 * time.Second)}
		}
	}
	switch kind {
	case KindUnavailable, KindPrivate, KindDRM, KindTooLarge, KindLive, KindUnsupported:
		return Strategy{}
	case KindGeoBlocked:
		if attempt > 2 {
			return Strategy{}
		}
		return Strategy{Retry: true, Next: Attempt{Tier: TierResidential, NewProxy: true, Delay: time.Second}}
	case KindBotCheck:
		// The user is waiting on the ack. Climbing the proxy ladder here keeps
		// the chat on «проверяю» for minutes. Cookies are still passed on the
		// attempt itself when a jar is stored; a bot wall is reported at once.
		return Strategy{}
	case KindRateLimited:
		a := ladder()
		a.NewProxy = true
		a.Delay = backoff(15 * time.Second)
		return Strategy{Retry: true, Next: a}
	case KindNetwork:
		return Strategy{Retry: true, Next: ladder()}
	default: // internal: one more try
		if attempt > 1 {
			return Strategy{}
		}
		return Strategy{Retry: true, Next: Attempt{Tier: TierDatacenter, Delay: 3 * time.Second}}
	}
}

package domain

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := map[string]ErrorKind{
		"ERROR: [youtube] abc: Video unavailable. This video has been removed by the uploader": KindUnavailable,
		"ERROR: [youtube] abc: Private video. Sign in if you've been granted access":           KindPrivate,
		"ERROR: [youtube] abc: Join this channel to get access to members-only content":        KindPrivate,
		"ERROR: [vk] 1_2: This video is protected by DRM":                                      KindDRM,
		"ERROR: [youtube] abc: The uploader has not made this video available in your country": KindGeoBlocked,
		"ERROR: [youtube] abc: Sign in to confirm you’re not a bot. Use --cookies":             KindBotCheck,
		"ERROR: unable to download video data: HTTP Error 429: Too Many Requests":              KindRateLimited,
		"ERROR: [rutube] x: Unable to download webpage: <urlopen error timed out>":             KindNetwork,
		"ERROR: unable to download video data: HTTP Error 503: Service Unavailable":            KindNetwork,
		"ERROR: [youtube] abc: This live event will begin in 3 hours":                          KindLive,
		"ERROR: Unsupported URL: https://example.com":                                          KindUnsupported,
		"ERROR: File is larger than max-filesize (2147483648 bytes > 1073741824 bytes)":        KindTooLarge,
		"ERROR: [vk] 1_2: HTTP Error 404: Not Found":                                           KindUnavailable,
		"ERROR: something odd happened":                                                        KindInternal,
		"":                                                                                     KindInternal,
		// ERROR lines win over earlier warnings
		"WARNING: rate limit hint\nERROR: [youtube] abc: Video unavailable": KindUnavailable,
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPlan(t *testing.T) {
	for _, k := range []ErrorKind{KindUnavailable, KindPrivate, KindDRM, KindTooLarge, KindLive, KindUnsupported} {
		if Plan(k, 1).Retry {
			t.Errorf("%s must not retry", k)
		}
	}
	// network: ladder same → other+cookies → mobile+UA, exponential delay, max 3 retries
	s1, s2, s3 := Plan(KindNetwork, 1), Plan(KindNetwork, 2), Plan(KindNetwork, 3)
	if !s1.Retry || s1.Next.NewProxy || s1.Next.Tier != TierDatacenter || s1.Next.Delay != 2*time.Second {
		t.Fatalf("attempt 1: %+v", s1)
	}
	if !s2.Next.NewProxy || !s2.Next.WithCookies || s2.Next.Delay != 4*time.Second {
		t.Fatalf("attempt 2: %+v", s2)
	}
	if s3.Next.Tier != TierMobile || !s3.Next.MobileUA {
		t.Fatalf("attempt 3: %+v", s3)
	}
	if Plan(KindNetwork, MaxAttempts).Retry {
		t.Fatal("cap reached")
	}
	if g := Plan(KindGeoBlocked, 1); !g.Retry || g.Next.Tier != TierResidential {
		t.Fatalf("geo: %+v", g)
	}
	if Plan(KindGeoBlocked, 3).Retry {
		t.Fatal("geo retries twice at most")
	}
	if b := Plan(KindBotCheck, 2); !b.Next.WithCookies || !b.Next.NewProxy || b.Next.Tier != TierResidential {
		t.Fatalf("bot check: %+v", b)
	}
	if r := Plan(KindRateLimited, 2); !r.Next.NewProxy || r.Next.Delay != 30*time.Second {
		t.Fatalf("429: %+v", r)
	}
	if !Plan(KindInternal, 1).Retry || Plan(KindInternal, 2).Retry {
		t.Fatal("internal retries once")
	}
}

func TestStatus(t *testing.T) {
	if StatusQueued.Terminal() || StatusRunning.Terminal() || !StatusDone.Terminal() || !StatusFailed.Terminal() {
		t.Fatal("terminal states")
	}
}

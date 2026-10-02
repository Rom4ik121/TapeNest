package service

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/streaming-service/internal/domain"
	"github.com/tapenest/tapenest/services/streaming-service/internal/sign"
)

func TestViewWarmThenReady(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := &Cinema{Sign: sign.New("0123456789abcdef0123456789abcdef", time.Hour), Warmup: 2 * time.Second, Now: func() time.Time { return now }}
	s := domain.Session{ID: uuid.MustParse("2677eb7b-513c-4ac8-8021-29add2525a7a"), CreatedAt: now, Mode: "preview"}
	tok := c.Sign.Sign(s.ID.String(), now)
	warm := c.ViewSession(s, tok)
	if warm.Status != "warming" || warm.HLSPath != "" {
		t.Fatalf("%+v", warm)
	}
	c.Now = func() time.Time { return now.Add(2 * time.Second) }
	ready := c.ViewSession(s, tok)
	if ready.Status != "ready" || ready.HLSPath == "" || ready.BufferedPct != 100 {
		t.Fatalf("%+v", ready)
	}
	s.Err = "no peers"
	if failed := c.ViewSession(s, tok); failed.Status != "failed" || failed.Error != "no peers" {
		t.Fatalf("%+v", failed)
	}
}

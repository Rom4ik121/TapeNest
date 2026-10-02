package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Plan is the network setup of a job's next attempt plus the proxy used last
// (so "NewProxy" can avoid it on another worker).
type Plan struct {
	domain.Attempt
	LastProxy string `json:"lastProxy,omitempty"`
}

// Plans stores retry plans between attempts (TTL 2 h).
type Plans struct {
	rdb redis.UniversalClient
}

// NewPlans creates the store.
func NewPlans(rdb redis.UniversalClient) *Plans { return &Plans{rdb: rdb} }

func planKey(id uuid.UUID) string { return "download:plan:" + id.String() }

// Get returns the plan (zero value = first attempt).
func (p *Plans) Get(ctx context.Context, id uuid.UUID) (Plan, error) {
	raw, err := p.rdb.Get(ctx, planKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Plan{}, nil
	}
	if err != nil {
		return Plan{}, fmt.Errorf("get plan: %w", err)
	}
	var pl Plan
	_ = json.Unmarshal(raw, &pl)
	return pl, nil
}

// Set stores the plan for the next attempt.
func (p *Plans) Set(ctx context.Context, id uuid.UUID, pl Plan) error {
	raw, _ := json.Marshal(pl)
	if err := p.rdb.Set(ctx, planKey(id), raw, 2*time.Hour).Err(); err != nil {
		return fmt.Errorf("set plan: %w", err)
	}
	return nil
}

// Delete drops the plan (job finished).
func (p *Plans) Delete(ctx context.Context, id uuid.UUID) error {
	return p.rdb.Del(ctx, planKey(id)).Err()
}

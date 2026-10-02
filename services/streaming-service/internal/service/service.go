// Package service is the cinema use-cases behind docs/api/cinema.openapi.yaml.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/streaming-service/internal/domain"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
	"github.com/tapenest/tapenest/services/streaming-service/internal/sign"
	"github.com/tapenest/tapenest/services/streaming-service/internal/torr"
)

// ErrNotFound is a missing row.
var ErrNotFound = repo.ErrNotFound

// ErrUnavailable is TorrServer down. The catalog itself is still served.
var ErrUnavailable = torr.ErrUnavailable

// View is a stream session as the player polls it.
type View struct {
	ID          uuid.UUID
	TitleID     uuid.UUID
	FileID      uuid.UUID
	Status      string
	BufferedPct float64
	Peers       int
	SpeedBps    float64
	HLSPath     string
	Error       string
}

// Cinema is the application service.
type Cinema struct {
	Store  *repo.Store
	Sign   *sign.Signer
	Warmup time.Duration
	P2P    bool
	Torr   *torr.Client
	Now    func() time.Time
}

func (c *Cinema) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ViewSession maps a stored session to warming/ready/failed.
// Licensed previews warm locally for Warmup and then point at /hls/{id}/index.m3u8.
func (c *Cinema) ViewSession(s domain.Session, tok sign.Token) View {
	v := View{ID: s.ID, TitleID: s.TitleID, FileID: s.FileID, Status: "warming"}
	if s.Mode == "failed" || s.Err != "" {
		v.Status = "failed"
		v.Error = s.Err
		if v.Error == "" {
			v.Error = "stream failed"
		}
		return v
	}
	warm := c.Warmup
	if warm < 0 {
		warm = 2 * time.Second
	}
	elapsed := c.now().Sub(s.CreatedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	pct := 100.0
	if warm > 0 {
		pct = float64(elapsed) / float64(warm) * 100
	}
	if pct > 100 {
		pct = 100
	}
	v.BufferedPct = pct
	if pct < 100 {
		v.SpeedBps = 1_500_000
		return v
	}
	v.Status = "ready"
	v.BufferedPct = 100
	v.SpeedBps = 2_000_000
	v.HLSPath = fmt.Sprintf("/hls/%s/index.m3u8?exp=%d&sig=%s", s.ID, tok.Exp, tok.Sig)
	return v
}

// Start begins or reuses a session for a file.
// Files without a magnet play the local generated preview. A magnet is sent to
// TorrServer only when p2p is enabled; otherwise the call fails without fetching anything.
func (c *Cinema) Start(ctx context.Context, user, titleID, fileID uuid.UUID) (domain.Session, error) {
	title, err := c.Store.Get(ctx, user, titleID)
	if err != nil {
		return domain.Session{}, err
	}
	var file *domain.File
	for i := range title.Files {
		if title.Files[i].ID == fileID {
			file = &title.Files[i]
			break
		}
	}
	if file == nil {
		return domain.Session{}, ErrNotFound
	}
	if existing, err := c.Store.ActiveSession(ctx, user, fileID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return domain.Session{}, err
	}
	sess := domain.Session{
		ID: uuid.New(), UserID: user, TitleID: titleID, FileID: fileID,
		Mode: "preview", CreatedAt: c.now(), UpdatedAt: c.now(),
	}
	if file.Magnet != "" {
		if !c.P2P || c.Torr == nil {
			return domain.Session{}, ErrUnavailable
		}
		if err := c.Torr.Add(ctx, file.Magnet); err != nil {
			return domain.Session{}, ErrUnavailable
		}
		// The bytes stay inside TorrServer. We do not copy the file into our storage.
		// Until a legal magnet is configured, the seeded catalog never takes this branch.
		sess.Mode = "preview"
	}
	if err := c.Store.InsertSession(ctx, sess); err != nil {
		return domain.Session{}, err
	}
	return sess, nil
}

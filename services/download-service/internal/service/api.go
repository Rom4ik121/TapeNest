package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
)

// API errors (mapped to HTTP codes by the transport layer).
var (
	ErrQuotaActive   = errors.New("too many active downloads")
	ErrQuotaDaily    = errors.New("daily download limit reached")
	ErrForbiddenHost = errors.New("host is not allowed")
	ErrNotReady      = errors.New("download is not finished")
	ErrNoPublicURL   = errors.New("public file links are not configured")
	ErrNotFailed     = errors.New("only a failed download can be retried")
	ErrNotDone       = errors.New("only a finished download can be removed")
	ErrNotFound      = repo.ErrNotFound
)

// Quotas are per-user limits (ADR 0008).
type Quotas struct {
	Active int // queued + running
	Daily  int // jobs per rolling 24 h
}

// API serves the HTTP use cases.
type API struct {
	mediaIndex
	queue      Queue
	guard      HostGuard
	quotas     Quotas
	presignTTL time.Duration
}

// APIDeps groups API dependencies.
type APIDeps struct {
	Store      Store
	Queue      Queue
	Bus        Bus
	Files      Files
	Guard      HostGuard
	Quotas     Quotas
	PresignTTL time.Duration
	Log        *slog.Logger
	Now        func() time.Time
}

// NewAPI builds the API service.
func NewAPI(d APIDeps) *API {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &API{
		mediaIndex: mediaIndex{store: d.Store, bus: d.Bus, files: d.Files, log: d.Log, now: d.Now},
		queue:      d.Queue, guard: d.Guard, quotas: d.Quotas, presignTTL: d.PresignTTL,
	}
}

// CreateInput is a download request.
type CreateInput struct {
	UserID   uuid.UUID
	URL      string
	Chat     *domain.Chat // bot jobs
	Priority domain.Priority
}

// Create validates and normalizes the URL, applies quotas and dedup, and either
// answers from storage (done at once) or queues the job. created=false means an
// identical active job of this user was returned instead.
func (a *API) Create(ctx context.Context, in CreateInput) (domain.Job, bool, error) {
	n, err := domain.NormalizeURL(in.URL)
	if err != nil {
		return domain.Job{}, false, err
	}
	u, _ := url.Parse(n.URL)
	if err := a.guard.Check(ctx, u.Hostname()); errors.Is(err, netguard.ErrForbidden) {
		return domain.Job{}, false, ErrForbiddenHost
	} else if err != nil {
		a.log.WarnContext(ctx, "host check failed, deferring to worker", "host", u.Hostname(), "err", err)
	}
	hash := n.Hash()
	if j, err := a.store.FindActiveUserJob(ctx, in.UserID, hash); err == nil {
		return j, false, nil
	} else if !errors.Is(err, repo.ErrNotFound) {
		return domain.Job{}, false, err
	}
	active, recent, err := a.store.CountUserJobs(ctx, in.UserID, a.now().Add(-24*time.Hour))
	if err != nil {
		return domain.Job{}, false, err
	}
	if active >= a.quotas.Active {
		return domain.Job{}, false, ErrQuotaActive
	}
	if recent >= a.quotas.Daily {
		return domain.Job{}, false, ErrQuotaDaily
	}
	prio := in.Priority
	if prio == "" {
		prio = domain.PriorityNormal
	}
	job := domain.Job{
		ID: uuid.New(), UserID: in.UserID, URL: in.URL, Normalized: n.URL, URLHash: hash, Source: n.Source,
		Status: domain.StatusQueued, Priority: prio, Chat: in.Chat,
	}
	m, hit, err := a.lookup(ctx, hash)
	if err != nil {
		return domain.Job{}, false, err
	}
	if hit { // dedup: the file is already stored — done immediately
		now := a.now()
		job.Status, job.MediaID, job.Title, job.FinishedAt = domain.StatusDone, &m.ID, m.Title, &now
		job, err = a.store.InsertJob(ctx, job)
		if err != nil {
			return domain.Job{}, false, err
		}
		e := eventFor(mq.EventDownloaded, job)
		e.File, e.Cached = a.fileInfo(ctx, m, a.presignTTL), true
		if err := a.bus.Publish(ctx, e); err != nil {
			a.log.WarnContext(ctx, "publish cached result failed", "job", job.ID, "err", err)
		}
		return job, true, nil
	}
	job, err = a.store.InsertJob(ctx, job)
	if err != nil {
		return domain.Job{}, false, err
	}
	if err := a.queue.Enqueue(ctx, job.ID, prio); err != nil {
		// the row stays queued; report failure so the caller can retry later
		_, _ = a.store.FinishFailed(ctx, job.ID, domain.KindInternal, "queue unavailable")
		return domain.Job{}, false, fmt.Errorf("enqueue: %w", err)
	}
	return job, true, nil
}

// View is a job with live progress and (when done) its file.
type View struct {
	Job      domain.Job
	Progress *domain.Progress
	File     *domain.Media
}

// Get returns one job of the user.
func (a *API) Get(ctx context.Context, userID, id uuid.UUID) (View, error) {
	j, err := a.store.GetUserJob(ctx, userID, id)
	if err != nil {
		return View{}, err
	}
	return a.view(ctx, j)
}

// View enriches a job with live progress / file metadata.
func (a *API) View(ctx context.Context, j domain.Job) (View, error) { return a.view(ctx, j) }

func (a *API) view(ctx context.Context, j domain.Job) (View, error) {
	v := View{Job: j}
	switch {
	case j.Status == domain.StatusRunning:
		p, err := a.bus.Progress(ctx, j.ID)
		if err != nil {
			a.log.WarnContext(ctx, "progress unavailable", "err", err)
		}
		v.Progress = p
	case j.Status == domain.StatusDone && j.MediaID != nil:
		m, err := a.store.GetMedia(ctx, *j.MediaID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return View{}, err
		}
		if err == nil {
			v.File = &m
		}
	}
	return v, nil
}

// List returns the user's jobs, newest first (keyset pagination).
func (a *API) List(ctx context.Context, userID uuid.UUID, after *repo.Cursor, limit int) ([]View, error) {
	jobs, err := a.store.ListUserJobs(ctx, userID, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(jobs))
	for _, j := range jobs {
		v, err := a.view(ctx, j)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// FileURL returns a presigned public link (TTL ≤ 1 h) to the job's file.
func (a *API) FileURL(ctx context.Context, userID, id uuid.UUID) (string, error) {
	v, err := a.Get(ctx, userID, id)
	if err != nil {
		return "", err
	}
	if v.Job.Status != domain.StatusDone || v.File == nil || v.File.ExpiresAt.Before(a.now()) {
		return "", ErrNotReady
	}
	u, err := a.files.PresignPublic(ctx, v.File.ObjectKey, FileName(v.File.Title, v.File.ExternalID, v.File.MimeType), a.presignTTL)
	if err != nil {
		return "", err
	}
	if u == "" {
		return "", ErrNoPublicURL
	}
	return u, nil
}

// AdminList returns jobs of every user. status empty means all statuses.
func (a *API) AdminList(ctx context.Context, status string, after *repo.Cursor, limit int) ([]View, error) {
	jobs, err := a.store.ListJobs(ctx, status, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(jobs))
	for _, j := range jobs {
		v, err := a.view(ctx, j)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Retry puts a failed job back on the queue. A job that is not failed is rejected.
func (a *API) Retry(ctx context.Context, id uuid.UUID) (domain.Job, error) {
	j, err := a.store.RetryFailed(ctx, id)
	if errors.Is(err, repo.ErrNotFound) {
		existing, gerr := a.store.GetJob(ctx, id)
		if gerr != nil {
			return domain.Job{}, gerr
		}
		if existing.Status != domain.StatusFailed {
			return domain.Job{}, ErrNotFailed
		}
		return domain.Job{}, err
	}
	if err != nil {
		return domain.Job{}, err
	}
	if err := a.queue.Enqueue(ctx, j.ID, domain.PriorityLow); err != nil {
		_, _ = a.store.FinishFailed(ctx, j.ID, domain.KindInternal, "queue unavailable")
		return domain.Job{}, fmt.Errorf("enqueue: %w", err)
	}
	return j, nil
}

// RemoveFromLibrary deletes a finished job from that user's library.
// The stored file is removed only when no other job still references it.
func (a *API) RemoveFromLibrary(ctx context.Context, id uuid.UUID) error {
	j, err := a.store.DeleteDoneJob(ctx, id)
	if errors.Is(err, repo.ErrNotFound) {
		existing, gerr := a.store.GetJob(ctx, id)
		if gerr != nil {
			return gerr
		}
		if existing.Status != domain.StatusDone {
			return ErrNotDone
		}
		return err
	}
	if err != nil {
		return err
	}
	if j.MediaID == nil {
		return nil
	}
	n, err := a.store.CountMediaJobs(ctx, *j.MediaID)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	m, err := a.store.GetMedia(ctx, *j.MediaID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := a.store.DeleteMedia(ctx, m.ID); err != nil {
		return err
	}
	if err := a.files.Remove(ctx, m.ObjectKey); err != nil {
		a.log.WarnContext(ctx, "remove object failed", "key", m.ObjectKey, "err", err)
	}
	if err := a.bus.ForgetMedia(ctx, m.URLHash); err != nil {
		a.log.WarnContext(ctx, "forget media cache failed", "err", err)
	}
	return nil
}

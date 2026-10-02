// Package service implements download-service use cases: job creation with
// quotas and 3-layer dedup (API), and job processing (worker).
package service

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/cookies"
	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/ytdlp"
)

// Store is the persistence port (implemented by repo.Store).
type Store interface {
	InsertJob(ctx context.Context, j domain.Job) (domain.Job, error)
	GetJob(ctx context.Context, id uuid.UUID) (domain.Job, error)
	GetUserJob(ctx context.Context, userID, id uuid.UUID) (domain.Job, error)
	ListUserJobs(ctx context.Context, userID uuid.UUID, after *repo.Cursor, limit int) ([]domain.Job, error)
	FindActiveUserJob(ctx context.Context, userID uuid.UUID, hash string) (domain.Job, error)
	CountUserJobs(ctx context.Context, userID uuid.UUID, since time.Time) (int, int, error)
	MarkRunning(ctx context.Context, id uuid.UUID, stage domain.Stage) (domain.Job, error)
	SetStage(ctx context.Context, id uuid.UUID, stage domain.Stage, title string) error
	Requeue(ctx context.Context, id uuid.UUID, prio domain.Priority, kind domain.ErrorKind, msg string) (domain.Job, error)
	FinishDone(ctx context.Context, id, mediaID uuid.UUID, title string) (domain.Job, error)
	FinishFailed(ctx context.Context, id uuid.UUID, kind domain.ErrorKind, msg string) (domain.Job, error)
	GetMedia(ctx context.Context, id uuid.UUID) (domain.Media, error)
	GetMediaByHash(ctx context.Context, hash string) (domain.Media, error)
	UpsertMedia(ctx context.Context, m domain.Media) (domain.Media, error)
	RenameJob(ctx context.Context, userID, id uuid.UUID, title string) (domain.Job, error)
	SoftDeleteJob(ctx context.Context, userID, id uuid.UUID) (domain.Job, error)
	CountLiveMediaRefs(ctx context.Context, mediaID uuid.UUID) (int, error)
	SetPosterKey(ctx context.Context, mediaID uuid.UUID, key string) error
}

// Editor trims and grabs a frame with the ffmpeg already used by yt-dlp.
type Editor interface {
	Poster(ctx context.Context, src, dst string) error
	Trim(ctx context.Context, src, dst string, start, end time.Duration) error
}

// Queue schedules jobs (mq.Queue).
type Queue interface {
	Enqueue(ctx context.Context, id uuid.UUID, p domain.Priority) error
	EnqueueAt(ctx context.Context, id uuid.UUID, p domain.Priority, at time.Time) error
}

// Bus is progress/events/dedup cache (mq.Bus).
type Bus interface {
	SetProgress(ctx context.Context, id uuid.UUID, p domain.Progress) error
	Progress(ctx context.Context, id uuid.UUID) (*domain.Progress, error)
	Publish(ctx context.Context, e mq.Event) error
	CacheMedia(ctx context.Context, hash string, mediaID uuid.UUID) error
	CachedMedia(ctx context.Context, hash string) (uuid.UUID, error)
	ForgetMedia(ctx context.Context, hash string) error
}

// Files is object storage (storage.S3).
type Files interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType, fileName string) (int64, error)
	Exists(ctx context.Context, key string) (bool, error)
	PresignInternal(ctx context.Context, key, fileName string, ttl time.Duration) (string, error)
	PresignPublic(ctx context.Context, key, fileName string, ttl time.Duration) (string, error)
	// PresignInline is a browser-playable GET without Content-Disposition: attachment.
	PresignInline(ctx context.Context, key string, ttl time.Duration) (string, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Remove(ctx context.Context, key string) error
}

// Fetcher runs yt-dlp (ytdlp.Runner).
type Fetcher interface {
	Probe(ctx context.Context, url string, o ytdlp.Opts, infoPath string) (domain.Info, error)
	DownloadFile(ctx context.Context, infoPath, spec, dir string, o ytdlp.Opts, onProgress func(ytdlp.Progress)) (string, error)
	Stream(ctx context.Context, infoPath, spec string, o ytdlp.Opts, onProgress func(ytdlp.Progress)) (io.ReadCloser, func() error, error)
}

// Limiter is the per-domain semaphore (mq.Semaphores).
type Limiter interface {
	Acquire(ctx context.Context, src domain.Source, holder string) (bool, error)
	Release(ctx context.Context, src domain.Source, holder string) error
}

// Locker is the per-URL lock (mq.Locks).
type Locker interface {
	Lock(ctx context.Context, key string, ttl time.Duration) (func(), bool, error)
}

// Planner stores retry plans (mq.Plans).
type Planner interface {
	Get(ctx context.Context, id uuid.UUID) (mq.Plan, error)
	Set(ctx context.Context, id uuid.UUID, p mq.Plan) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// HostGuard is the SSRF check (netguard.Guard).
type HostGuard interface {
	Check(ctx context.Context, host string) error
}

// CookieJar provides cookies files (cookies.Store).
type CookieJar interface {
	WriteFile(ctx context.Context, src domain.Source, dir string) (cookies.Jar, bool, error)
}

// ProxyPicker selects proxies (proxy.Pools).
type ProxyPicker interface {
	Pick(tier domain.ProxyTier, avoid string) (string, domain.ProxyTier)
}

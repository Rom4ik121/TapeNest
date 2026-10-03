package repo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Integration test against a real PostgreSQL 16 (TEST_DATABASE_URL). CI provides
// it as a service container; locally: tools/dev/infra-native.sh or make infra-up.
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for i := 0; i < 2; i++ { // idempotent
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return NewStore(pool), pool
}

func TestJobLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	user := uuid.New()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(uuid.NewString()))) // unique per run
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM download.jobs WHERE user_id = $1", user)
		_, _ = pool.Exec(ctx, "DELETE FROM download.media WHERE url_hash = $1", hash)
	})
	job := domain.Job{
		ID: uuid.New(), UserID: user, URL: "https://youtu.be/x", Normalized: "https://www.youtube.com/watch?v=x",
		URLHash: hash, Source: domain.SourceYouTube, Status: domain.StatusQueued, Priority: domain.PriorityNormal,
		Chat: &domain.Chat{ChatID: 10, StatusMessageID: 11, Lang: "ru"},
	}
	j, err := s.InsertJob(ctx, job)
	if err != nil || j.Chat == nil || j.Chat.ChatID != 10 || j.Chat.StatusMessageID != 11 || j.Chat.ReplyToMessageID != 0 || j.CreatedAt.IsZero() {
		t.Fatalf("insert = %+v %v", j, err)
	}
	if _, err := s.GetJob(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.GetUserJob(ctx, uuid.New(), j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("owner check")
	}
	if got, err := s.FindActiveUserJob(ctx, user, hash); err != nil || got.ID != j.ID {
		t.Fatal(got, err)
	}
	active, recent, err := s.CountUserJobs(ctx, user, time.Now().Add(-time.Hour))
	if err != nil || active != 1 || recent != 1 {
		t.Fatal(active, recent, err)
	}
	r, err := s.MarkRunning(ctx, j.ID, domain.StageProbing)
	if err != nil || r.Status != domain.StatusRunning || r.Attempts != 1 {
		t.Fatal(r, err)
	}
	// a reclaimed job (worker crashed while running) can be taken again
	if r2, err := s.MarkRunning(ctx, j.ID, domain.StageProbing); err != nil || r2.Attempts != 2 {
		t.Fatal(r2, err)
	}
	if err := s.SetStage(ctx, j.ID, domain.StageDownloading, "Title"); err != nil {
		t.Fatal(err)
	}
	q, err := s.Requeue(ctx, j.ID, domain.PriorityLow, domain.KindNetwork, "timeout")
	if err != nil || q.Status != domain.StatusQueued || q.Priority != domain.PriorityLow || q.ErrorKind != domain.KindNetwork || q.Title != "Title" {
		t.Fatal(q, err)
	}
	_, _ = s.MarkRunning(ctx, j.ID, domain.StageProbing)

	m, err := s.UpsertMedia(ctx, domain.Media{
		ID: uuid.New(), URLHash: hash, URL: job.Normalized, Source: domain.SourceYouTube,
		ExternalID: "x", Title: "Title", DurationSec: 19, Width: 320, Height: 240, FormatID: "18", ObjectKey: "k1",
		SizeBytes: 100, MimeType: "video/mp4", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.UpsertMedia(ctx, domain.Media{
		ID: uuid.New(), URLHash: hash, URL: job.Normalized, Source: domain.SourceYouTube,
		ObjectKey: "k2", SizeBytes: 200, MimeType: "video/mp4", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil || m2.ID != m.ID || m2.ObjectKey != "k2" {
		t.Fatalf("upsert keeps id: %+v %v", m2, err)
	}
	if got, err := s.GetMediaByHash(ctx, hash); err != nil || got.ID != m.ID {
		t.Fatal(got, err)
	}
	if got, err := s.GetMedia(ctx, m.ID); err != nil || got.SizeBytes != 200 {
		t.Fatal(got, err)
	}
	d, err := s.FinishDone(ctx, j.ID, m.ID, "Title")
	if err != nil || d.Status != domain.StatusDone || d.MediaID == nil || *d.MediaID != m.ID || d.FinishedAt == nil {
		t.Fatal(d, err)
	}
	if _, err := s.FinishFailed(ctx, j.ID, domain.KindInternal, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("terminal job must not change")
	}
	if _, err := s.FindActiveUserJob(ctx, user, hash); !errors.Is(err, ErrNotFound) {
		t.Fatal("no active job")
	}

	// a failed download with no file is not a library row; keyset still pages the rest
	j2, _ := s.InsertJob(ctx, domain.Job{
		ID: uuid.New(), UserID: user, URL: "u", Normalized: "u", URLHash: hash, Source: domain.SourceVK,
		Status: domain.StatusQueued, Priority: domain.PriorityNormal,
	})
	f, err := s.FinishFailed(ctx, j2.ID, domain.KindPrivate, "private")
	if err != nil || f.Status != domain.StatusFailed || f.Chat != nil || f.MediaID != nil {
		t.Fatal(f, err)
	}
	queued, _ := s.InsertJob(ctx, domain.Job{
		ID: uuid.New(), UserID: user, URL: "u2", Normalized: "u2", URLHash: hash + "b", Source: domain.SourceVK,
		Status: domain.StatusQueued, Priority: domain.PriorityNormal,
	})
	page, err := s.ListUserJobs(ctx, user, nil, 1)
	if err != nil || len(page) != 1 || page[0].ID != queued.ID {
		t.Fatal(page, err)
	}
	page2, err := s.ListUserJobs(ctx, user, &Cursor{CreatedAt: page[0].CreatedAt, ID: page[0].ID}, 10)
	if err != nil || len(page2) != 1 || page2[0].ID != j.ID {
		t.Fatal(page2, err)
	}
	for _, row := range append(page, page2...) {
		if row.ID == j2.ID {
			t.Fatal("failed job without a file must not be listed")
		}
	}
	_, _ = pool.Exec(ctx, "UPDATE download.media SET expires_at = now() - interval '1 minute' WHERE id = $1", m.ID)
	if _, err := s.GetMediaByHash(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired media must not be a dedup hit")
	}
}

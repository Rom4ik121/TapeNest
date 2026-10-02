package repo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

// Integration test against a real PostgreSQL 16 (TEST_DATABASE_URL). CI provides
// it as a service container; locally: tools/dev/infra-native.sh or make infra-up.
func testPool(t *testing.T) *pgxpool.Pool {
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
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
	return pool
}

func TestUsersRepo(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	r := NewUsers(pool)
	tgID := time.Now().UnixNano() % 1_000_000_000_000
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM gateway.users WHERE telegram_id = $1", tgID) })

	login := time.Now().UTC().Truncate(time.Microsecond)
	u, err := r.UpsertTelegramUser(ctx, domain.TelegramProfile{
		ID: tgID, FirstName: "Рома", Username: "roma", LanguageCode: "ru", PhotoURL: "https://t.me/i/1.jpg", IsPremium: true,
	}, domain.RoleUser, &login)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID.Version() != 4 || u.TelegramID != tgID || u.FirstName != "Рома" || *u.Username != "roma" || u.Role != domain.RoleUser || !u.LastLoginAt.Equal(login) {
		t.Fatalf("insert: %+v", u)
	}

	// Update keeps the UUID; empty optional fields don't wipe photo/language.
	u2, err := r.UpsertTelegramUser(ctx, domain.TelegramProfile{ID: tgID, FirstName: "Roma"}, domain.RoleAdmin, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u2.ID != u.ID || u2.FirstName != "Roma" || u2.Username != nil || u2.Role != domain.RoleAdmin {
		t.Fatalf("update: %+v", u2)
	}
	if u2.PhotoURL == nil || u2.LanguageCode == nil || *u2.LanguageCode != "ru" || u2.LastLoginAt == nil {
		t.Fatalf("coalesced fields lost: %+v", u2)
	}
	byID, err := r.GetByID(ctx, u.ID)
	if err != nil || byID.TelegramID != tgID {
		t.Fatalf("get by id: %v", err)
	}
	byTG, err := r.GetByTelegramID(ctx, tgID)
	if err != nil || byTG.ID != u.ID {
		t.Fatalf("get by tg: %v", err)
	}
	if _, err := r.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if _, err := r.GetByTelegramID(ctx, -1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("not found tg: %v", err)
	}
	if _, err := r.UpsertTelegramUser(ctx, domain.TelegramProfile{ID: tgID}, domain.Role("root"), nil); err == nil {
		t.Fatal("role CHECK constraint must reject unknown roles")
	}
}

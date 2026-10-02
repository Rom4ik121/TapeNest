package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/streaming-service/internal/domain"
)

// Store is the cinema database.
type Store struct{ pool *pgxpool.Pool }

// New returns a store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Seed upserts the fictional catalog. User rows (watchlist, positions) are left alone.
func (s *Store) Seed(ctx context.Context, titles []domain.Title) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, t := range titles {
		if _, err := tx.Exec(ctx, `
			INSERT INTO streaming.titles (id, kind, title, original_title, year, rating, genres, description, runtime_min, sort_index)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO UPDATE SET
				kind=EXCLUDED.kind, title=EXCLUDED.title, original_title=EXCLUDED.original_title,
				year=EXCLUDED.year, rating=EXCLUDED.rating, genres=EXCLUDED.genres,
				description=EXCLUDED.description, runtime_min=EXCLUDED.runtime_min, sort_index=EXCLUDED.sort_index`,
			t.ID, string(t.Kind), t.Title, t.OriginalTitle, t.Year, t.Rating, t.Genres, t.Description, t.RuntimeMin, t.Sort,
		); err != nil {
			return fmt.Errorf("seed title: %w", err)
		}
		for _, f := range t.Files {
			if _, err := tx.Exec(ctx, `
				INSERT INTO streaming.media_files (id, title_id, name, season, episode, quality, size_bytes, duration_sec, magnet, sort_index)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (id) DO UPDATE SET
					name=EXCLUDED.name, season=EXCLUDED.season, episode=EXCLUDED.episode, quality=EXCLUDED.quality,
					size_bytes=EXCLUDED.size_bytes, duration_sec=EXCLUDED.duration_sec, sort_index=EXCLUDED.sort_index`,
				f.ID, t.ID, f.Name, f.Season, f.Episode, f.Quality, f.SizeBytes, f.DurationSec, f.Magnet, f.Sort,
			); err != nil {
				return fmt.Errorf("seed file: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}

// Search lists titles after a sort cursor.
func (s *Store) Search(ctx context.Context, user uuid.UUID, q, kind string, after, limit int) ([]domain.Summary, error) {
	like := likePattern(q)
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.kind, t.title, t.original_title, t.year, t.rating, t.genres, t.sort_index,
		       EXISTS (SELECT 1 FROM streaming.watchlist w WHERE w.user_id=$1 AND w.title_id=t.id)
		FROM streaming.titles t
		WHERE ($2 = '' OR t.kind = $2)
		  AND ($3 = '' OR t.search_text ILIKE $4 ESCAPE '\' OR word_similarity($3, t.search_text) > 0.25)
		  AND t.sort_index > $5
		ORDER BY t.sort_index
		LIMIT $6`, user, kind, strings.ToLower(q), like, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSummaries(rows)
}

// Watchlist lists saved titles, newest first. after is exclusive when ok is true.
func (s *Store) Watchlist(ctx context.Context, user uuid.UUID, after time.Time, afterID uuid.UUID, hasCursor bool, limit int) ([]domain.Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.kind, t.title, t.original_title, t.year, t.rating, t.genres, t.sort_index, true, w.created_at
		FROM streaming.watchlist w
		JOIN streaming.titles t ON t.id = w.title_id
		WHERE w.user_id = $1
		  AND ($2 = false OR (w.created_at, t.id) < ($3, $4))
		ORDER BY w.created_at DESC, t.id DESC
		LIMIT $5`, user, hasCursor, after, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Summary
	for rows.Next() {
		var s domain.Summary
		var kind string
		if err := rows.Scan(&s.ID, &kind, &s.Title, &s.OriginalTitle, &s.Year, &s.Rating, &s.Genres, &s.Sort, &s.InWatchlist, &s.WatchedAt); err != nil {
			return nil, err
		}
		s.Kind = domain.TitleKind(kind)
		if s.Genres == nil {
			s.Genres = []string{}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns a title and its files.
func (s *Store) Get(ctx context.Context, user, id uuid.UUID) (domain.Title, error) {
	var t domain.Title
	var kind string
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.kind, t.title, t.original_title, t.year, t.rating, t.genres, t.sort_index,
		       t.description, t.runtime_min,
		       EXISTS (SELECT 1 FROM streaming.watchlist w WHERE w.user_id=$1 AND w.title_id=t.id)
		FROM streaming.titles t WHERE t.id=$2`, user, id).Scan(
		&t.ID, &kind, &t.Title, &t.OriginalTitle, &t.Year, &t.Rating, &t.Genres, &t.Sort,
		&t.Description, &t.RuntimeMin, &t.InWatchlist,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Title{}, ErrNotFound
	}
	if err != nil {
		return domain.Title{}, err
	}
	t.Kind = domain.TitleKind(kind)
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, season, episode, quality, size_bytes, duration_sec, magnet, sort_index
		FROM streaming.media_files WHERE title_id=$1 ORDER BY sort_index`, id)
	if err != nil {
		return domain.Title{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var f domain.File
		if err := rows.Scan(&f.ID, &f.Name, &f.Season, &f.Episode, &f.Quality, &f.SizeBytes, &f.DurationSec, &f.Magnet, &f.Sort); err != nil {
			return domain.Title{}, err
		}
		t.Files = append(t.Files, f)
	}
	return t, rows.Err()
}

// SetWatch adds or removes a watch-later row.
func (s *Store) SetWatch(ctx context.Context, user, title uuid.UUID, on bool) error {
	if on {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO streaming.watchlist (user_id, title_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING`, user, title)
		if err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23503" {
				return ErrNotFound
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM streaming.titles WHERE id=$1)`, title).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM streaming.watchlist WHERE user_id=$1 AND title_id=$2`, user, title)
	return err
}

// GetPosition returns a saved position.
func (s *Store) GetPosition(ctx context.Context, user, title, file uuid.UUID) (domain.Position, error) {
	var p domain.Position
	err := s.pool.QueryRow(ctx, `
		SELECT title_id, file_id, position_sec, duration_sec, updated_at
		FROM streaming.watch_positions WHERE user_id=$1 AND title_id=$2 AND file_id=$3`,
		user, title, file).Scan(&p.TitleID, &p.FileID, &p.PositionSec, &p.DurationSec, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Position{}, ErrNotFound
	}
	return p, err
}

// PutPosition upserts a position. The file must belong to the title.
func (s *Store) PutPosition(ctx context.Context, user uuid.UUID, p domain.Position) error {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM streaming.media_files WHERE id=$1 AND title_id=$2)`, p.FileID, p.TitleID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO streaming.watch_positions (user_id, title_id, file_id, position_sec, duration_sec, updated_at)
		VALUES ($1,$2,$3,$4,$5, now())
		ON CONFLICT (user_id, title_id, file_id) DO UPDATE SET
			position_sec=EXCLUDED.position_sec, duration_sec=EXCLUDED.duration_sec, updated_at=now()`,
		user, p.TitleID, p.FileID, p.PositionSec, p.DurationSec)
	return err
}

// Continue lists unfinished positions, newest first.
func (s *Store) Continue(ctx context.Context, user uuid.UUID, after time.Time, afterID uuid.UUID, hasCursor bool, limit int) ([]domain.ContinueItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.kind, t.title, t.original_title, t.year, t.rating, t.genres, t.sort_index,
		       EXISTS (SELECT 1 FROM streaming.watchlist w WHERE w.user_id=p.user_id AND w.title_id=t.id),
		       f.id, f.name, f.season, f.episode, f.quality, f.size_bytes, f.duration_sec, f.magnet, f.sort_index,
		       p.position_sec, p.duration_sec, p.updated_at
		FROM streaming.watch_positions p
		JOIN streaming.titles t ON t.id = p.title_id
		JOIN streaming.media_files f ON f.id = p.file_id
		WHERE p.user_id=$1
		  AND p.duration_sec > 0
		  AND p.position_sec >= 10
		  AND p.position_sec < p.duration_sec * 0.95
		  AND ($2 = false OR (p.updated_at, p.file_id) < ($3, $4))
		ORDER BY p.updated_at DESC, p.file_id DESC
		LIMIT $5`, user, hasCursor, after, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ContinueItem
	for rows.Next() {
		var item domain.ContinueItem
		var kind string
		if err := rows.Scan(
			&item.Title.ID, &kind, &item.Title.Title, &item.Title.OriginalTitle, &item.Title.Year, &item.Title.Rating, &item.Title.Genres, &item.Title.Sort, &item.Title.InWatchlist,
			&item.File.ID, &item.File.Name, &item.File.Season, &item.File.Episode, &item.File.Quality, &item.File.SizeBytes, &item.File.DurationSec, &item.File.Magnet, &item.File.Sort,
			&item.Position.PositionSec, &item.Position.DurationSec, &item.Position.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Title.Kind = domain.TitleKind(kind)
		item.Position.TitleID = item.Title.ID
		item.Position.FileID = item.File.ID
		out = append(out, item)
	}
	return out, rows.Err()
}

// ActiveSession returns the running session for a file.
func (s *Store) ActiveSession(ctx context.Context, user, file uuid.UUID) (domain.Session, error) {
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+` WHERE user_id=$1 AND file_id=$2 AND NOT stopped`, user, file))
}

// GetSession returns a session owned by the user.
func (s *Store) GetSession(ctx context.Context, user, id uuid.UUID) (domain.Session, error) {
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+` WHERE user_id=$1 AND id=$2 AND NOT stopped`, user, id))
}

// GetSessionAny loads a session by id for signed HLS URLs (no user header).
func (s *Store) GetSessionAny(ctx context.Context, id uuid.UUID) (domain.Session, error) {
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+` WHERE id=$1 AND NOT stopped`, id))
}

const sessionSelect = `SELECT id, user_id, title_id, file_id, mode, err, created_at, updated_at FROM streaming.sessions`

func (s *Store) scanSession(row pgx.Row) (domain.Session, error) {
	var sess domain.Session
	err := row.Scan(&sess.ID, &sess.UserID, &sess.TitleID, &sess.FileID, &sess.Mode, &sess.Err, &sess.CreatedAt, &sess.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	return sess, err
}

// InsertSession creates a session.
func (s *Store) InsertSession(ctx context.Context, sess domain.Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO streaming.sessions (id, user_id, title_id, file_id, mode, err)
		VALUES ($1,$2,$3,$4,$5,$6)`, sess.ID, sess.UserID, sess.TitleID, sess.FileID, sess.Mode, sess.Err)
	return err
}

// TouchSession refreshes updated_at.
func (s *Store) TouchSession(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE streaming.sessions SET updated_at=now() WHERE id=$1 AND NOT stopped`, id)
	return err
}

// StopSession marks a session stopped.
func (s *Store) StopSession(ctx context.Context, user, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE streaming.sessions SET stopped=true, updated_at=now() WHERE user_id=$1 AND id=$2`, user, id)
	return err
}

// ExpireSessions stops sessions idle since before.
func (s *Store) ExpireSessions(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE streaming.sessions SET stopped=true WHERE NOT stopped AND updated_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// AddTitle inserts an operator title with a single 1080p file and no magnet.
func (s *Store) AddTitle(ctx context.Context, t domain.Title, actor uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sort int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(sort_index),0)+1 FROM streaming.titles`).Scan(&sort); err != nil {
		return err
	}
	t.Sort = sort
	if _, err := tx.Exec(ctx, `
		INSERT INTO streaming.titles (id, kind, title, original_title, year, rating, genres, description, runtime_min, sort_index)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		t.ID, string(t.Kind), t.Title, t.OriginalTitle, t.Year, t.Rating, t.Genres, t.Description, t.RuntimeMin, t.Sort,
	); err != nil {
		return err
	}
	if len(t.Files) > 0 {
		f := t.Files[0]
		if _, err := tx.Exec(ctx, `
			INSERT INTO streaming.media_files (id, title_id, name, season, episode, quality, size_bytes, duration_sec, magnet, sort_index)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'',$9)`,
			f.ID, t.ID, f.Name, f.Season, f.Episode, f.Quality, f.SizeBytes, f.DurationSec, f.Sort,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO streaming.audit_log (actor, action, detail) VALUES ($1,'add_title',$2)`, actor, t.ID.String()+" "+t.Title); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListAudit returns recent admin actions.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.Audit, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, actor, action, detail, created_at FROM streaming.audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Audit
	for rows.Next() {
		var a domain.Audit
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanSummaries(rows pgx.Rows) ([]domain.Summary, error) {
	var out []domain.Summary
	for rows.Next() {
		var s domain.Summary
		var kind string
		if err := rows.Scan(&s.ID, &kind, &s.Title, &s.OriginalTitle, &s.Year, &s.Rating, &s.Genres, &s.Sort, &s.InWatchlist); err != nil {
			return nil, err
		}
		s.Kind = domain.TitleKind(kind)
		if s.Genres == nil {
			s.Genres = []string{}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func likePattern(q string) string {
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(q)) + "%"
}

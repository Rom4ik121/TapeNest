package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// cleanTitle normalizes an owner-supplied name (1..120 runes, no control chars).
func cleanTitle(raw string) (string, error) {
	s := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	s = strings.Join(strings.Fields(s), " ")
	if n := len([]rune(s)); n < 1 || n > 120 {
		return "", ErrBadTitle
	}
	return s, nil
}

// TrimWindow checks a [start, end) cut in seconds against the known duration
// (0 duration skips the upper bound). The fragment must be at least 1 second.
func TrimWindow(startSec, endSec float64, durationSec int) (start, end time.Duration, err error) {
	if math.IsNaN(startSec) || math.IsNaN(endSec) || math.IsInf(startSec, 0) || math.IsInf(endSec, 0) {
		return 0, 0, ErrBadRange
	}
	if startSec < 0 || endSec <= startSec || endSec-startSec < 1 {
		return 0, 0, ErrBadRange
	}
	if durationSec > 0 && endSec > float64(durationSec)+0.05 {
		return 0, 0, ErrBadRange
	}
	return time.Duration(startSec * float64(time.Second)), time.Duration(endSec * float64(time.Second)), nil
}

func editHash(id uuid.UUID) string {
	sum := sha256.Sum256([]byte("edit:" + id.String()))
	return hex.EncodeToString(sum[:])
}

// Rename sets the name shown in this user's library. The shared file title stays.
func (a *API) Rename(ctx context.Context, userID, id uuid.UUID, title string) (View, error) {
	clean, err := cleanTitle(title)
	if err != nil {
		return View{}, err
	}
	j, err := a.store.GetUserJob(ctx, userID, id)
	if err != nil {
		return View{}, err
	}
	if j.Status != domain.StatusDone {
		return View{}, ErrNotReady
	}
	j, err = a.store.RenameJob(ctx, userID, id, clean)
	if err != nil {
		return View{}, err
	}
	return a.view(ctx, j)
}

// Delete hides the job. A trim that nobody else references is removed from storage.
// A shared source file is kept so the next paste of the same link can still hit dedup.
func (a *API) Delete(ctx context.Context, userID, id uuid.UUID) error {
	j, err := a.store.SoftDeleteJob(ctx, userID, id)
	if err != nil {
		return err
	}
	if j.MediaID == nil {
		return nil
	}
	n, err := a.store.CountLiveMediaRefs(ctx, *j.MediaID)
	if err != nil {
		a.log.WarnContext(ctx, "count media refs failed", "err", err)
		return nil
	}
	if n > 0 {
		return nil
	}
	m, err := a.store.GetMedia(ctx, *j.MediaID)
	if err != nil || !m.OwnedEdit() {
		return nil
	}
	if err := a.files.Remove(ctx, m.ObjectKey); err != nil {
		a.log.WarnContext(ctx, "remove edit object failed", "err", err)
	}
	if m.PosterKey != "" {
		if err := a.files.Remove(ctx, m.PosterKey); err != nil {
			a.log.WarnContext(ctx, "remove poster failed", "err", err)
		}
	}
	if err := a.bus.ForgetMedia(ctx, m.URLHash); err != nil {
		a.log.WarnContext(ctx, "forget edit cache failed", "err", err)
	}
	return nil
}

// Trim cuts [startSec, endSec) out of a finished download into a new library item
// owned by the same user. The original stays.
func (a *API) Trim(ctx context.Context, userID, id uuid.UUID, startSec, endSec float64) (View, error) {
	if a.editor == nil {
		return View{}, ErrEditUnavailable
	}
	j, err := a.store.GetUserJob(ctx, userID, id)
	if err != nil {
		return View{}, err
	}
	if j.Status != domain.StatusDone || j.MediaID == nil {
		return View{}, ErrNotReady
	}
	m, err := a.store.GetMedia(ctx, *j.MediaID)
	if err != nil {
		return View{}, err
	}
	start, end, err := TrimWindow(startSec, endSec, m.DurationSec)
	if err != nil {
		return View{}, err
	}
	dir, err := os.MkdirTemp("", "tn-trim-")
	if err != nil {
		return View{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	src := filepath.Join(dir, "src"+extOf(m.ObjectKey))
	if err := a.copyObject(ctx, m.ObjectKey, src); err != nil {
		return View{}, fmt.Errorf("read source: %w", err)
	}
	out := filepath.Join(dir, "out.mp4")
	if err := a.editor.Trim(ctx, src, out, start, end); err != nil {
		a.log.WarnContext(ctx, "trim failed", "err", err)
		return View{}, ErrEditFailed
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		return View{}, ErrEditFailed
	}
	mediaID := uuid.New()
	key := fmt.Sprintf("edits/%s/%s.mp4", a.now().UTC().Format("2006/01"), mediaID)
	f, err := os.Open(out) //nolint:gosec // path is inside our temp dir
	if err != nil {
		return View{}, err
	}
	size, err := a.files.Put(ctx, key, f, st.Size(), "video/mp4", FileName(j.VisibleTitle(), m.ExternalID, "video/mp4"))
	_ = f.Close()
	if err != nil {
		return View{}, err
	}
	posterKey := a.storePoster(ctx, dir, out, mediaID)
	title := j.VisibleTitle()
	if title == "" {
		title = m.Title
	}
	expires := m.ExpiresAt
	if !expires.After(a.now()) {
		expires = a.now().Add(24 * time.Hour)
	}
	dur := int(math.Round(end.Seconds() - start.Seconds()))
	if dur < 1 {
		dur = 1
	}
	stored, err := a.store.UpsertMedia(ctx, domain.Media{
		ID: mediaID, URLHash: editHash(mediaID), URL: "edit:" + mediaID.String(), Source: m.Source,
		ExternalID: m.ExternalID, Title: title, DurationSec: dur, Width: m.Width, Height: m.Height,
		FormatID: "trim", ObjectKey: key, SizeBytes: size, MimeType: "video/mp4", PosterKey: posterKey,
		ExpiresAt: expires,
	})
	if err != nil {
		a.removeEdit(ctx, key, posterKey)
		return View{}, err
	}
	now := a.now()
	created := domain.Job{
		ID: uuid.New(), UserID: userID, URL: j.URL, Normalized: stored.URL, URLHash: stored.URLHash,
		Source: j.Source, Status: domain.StatusDone, Title: title, MediaID: &stored.ID, FinishedAt: &now,
	}
	created, err = a.store.InsertJob(ctx, created)
	if err != nil {
		a.removeEdit(ctx, key, posterKey)
		return View{}, err
	}
	return a.view(ctx, created)
}

func (a *API) copyObject(ctx context.Context, key, dst string) error {
	rc, err := a.files.Open(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.Create(dst) //nolint:gosec // dst is inside our temp dir
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, rc)
	return err
}

// storePoster uploads a jpeg frame next to a local video. Failure leaves the key empty.
func (a *API) storePoster(ctx context.Context, dir, video string, mediaID uuid.UUID) string {
	if a.editor == nil {
		return ""
	}
	dst := filepath.Join(dir, "frame.jpg")
	if err := a.editor.Poster(ctx, video, dst); err != nil {
		a.log.WarnContext(ctx, "poster extract failed", "err", err)
		return ""
	}
	f, err := os.Open(dst) //nolint:gosec // path is inside our temp dir
	if err != nil {
		return ""
	}
	defer f.Close()
	key := "posters/" + mediaID.String() + ".jpg"
	if _, err := a.files.Put(ctx, key, f, -1, "image/jpeg", "poster.jpg"); err != nil {
		a.log.WarnContext(ctx, "poster upload failed", "err", err)
		return ""
	}
	return key
}

func (a *API) removeEdit(ctx context.Context, key, posterKey string) {
	if err := a.files.Remove(ctx, key); err != nil {
		a.log.WarnContext(ctx, "remove unfinished edit failed", "err", err)
	}
	if posterKey != "" {
		if err := a.files.Remove(ctx, posterKey); err != nil {
			a.log.WarnContext(ctx, "remove unfinished poster failed", "err", err)
		}
	}
}

func extOf(key string) string {
	ext := filepath.Ext(key)
	if ext == "" || len(ext) > 5 {
		return ".mp4"
	}
	return ext
}

// Package jobs holds the reco worker's periodic jobs: catalog copy from
// music-service, audio analysis of new tracks and model training (item-item
// co-occurrence, implicit ALS, audio-content neighbours).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/algo"
	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/musicclient"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
)

// Catalog is the music-service port.
type Catalog interface {
	CatalogPage(ctx context.Context, after *uuid.UUID, limit int) ([]musicclient.Track, *uuid.UUID, error)
	Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, error)
}

// Store is the persistence port.
type Store interface {
	UpsertTracks(ctx context.Context, ts []repo.CatalogTrack, syncedAt time.Time) error
	MarkMissingDeleted(ctx context.Context, before time.Time) (int64, error)
	TracksNeedingAnalysis(ctx context.Context, version, maxAttempts, limit int) ([]uuid.UUID, error)
	SaveFeatures(ctx context.Context, id uuid.UUID, version int, f repo.Features) error
	SaveAnalysisError(ctx context.Context, id uuid.UUID, version int, msg string) error
	LoadModel(ctx context.Context, featureVersion int) (*model.Model, error)
	PositiveAffinities(ctx context.Context, now time.Time) ([]repo.Affinity, error)
	SaveModel(ctx context.Context, t repo.Trained, at time.Time) (int64, error)
}

// Jobs bundles the worker jobs.
type Jobs struct {
	Music    Catalog
	Store    Store
	Decoder  audio.Decoder
	Workers  int
	Log      *slog.Logger
	Now      func() time.Time
	PageSize int

	mu       sync.Mutex // one training at a time
	snapshot atomic.Pointer[model.Model]
}

func (j *Jobs) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// Snapshot returns the last trained model (used by ingest for audio tags).
func (j *Jobs) Snapshot() *model.Model { return j.snapshot.Load() }

// SyncCatalog copies music-service's catalog; returns tracks seen and deleted.
func (j *Jobs) SyncCatalog(ctx context.Context) (seen int, deleted int64, err error) {
	started := j.now().UTC()
	size := j.PageSize
	if size <= 0 {
		size = 500
	}
	var after *uuid.UUID
	for {
		items, next, err := j.Music.CatalogPage(ctx, after, size)
		if err != nil {
			return seen, 0, err
		}
		batch := make([]repo.CatalogTrack, 0, len(items))
		for _, t := range items {
			batch = append(batch, repo.CatalogTrack{
				ID: t.ID, Title: t.Title, ArtistID: t.ArtistID, Artist: t.Artist, AlbumID: t.AlbumID,
				Album: t.Album, Genre: t.Genre, Year: t.Year, DurationSec: int32(t.DurationSec), //nolint:gosec // seconds
				Popularity: float32(t.Popularity), CreatedAt: t.CreatedAt,
			})
		}
		if err := j.Store.UpsertTracks(ctx, batch, started); err != nil {
			return seen, 0, err
		}
		seen += len(items)
		if next == nil {
			break
		}
		after = next
	}
	if seen == 0 {
		return 0, 0, nil // never mass-delete on an empty listing
	}
	deleted, err = j.Store.MarkMissingDeleted(ctx, started)
	return seen, deleted, err
}

// AnalyzePending analyses tracks without current features; returns successes.
func (j *Jobs) AnalyzePending(ctx context.Context, limit int) (int, error) {
	ids, err := j.Store.TracksNeedingAnalysis(ctx, audio.Version, 3, limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	workers := max(j.Workers, 1)
	ch := make(chan uuid.UUID)
	var ok atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				if err := j.analyzeOne(ctx, id); err != nil {
					if ctx.Err() != nil {
						return
					}
					j.Log.Warn("audio analysis failed", "track", id.String(), "err", err)
					_ = j.Store.SaveAnalysisError(ctx, id, audio.Version, err.Error())
					continue
				}
				ok.Add(1)
			}
		}()
	}
	for _, id := range ids {
		select {
		case ch <- id:
		case <-ctx.Done():
		}
	}
	close(ch)
	wg.Wait()
	return int(ok.Load()), ctx.Err()
}

func (j *Jobs) analyzeOne(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	body, err := j.Music.Audio(ctx, id)
	if err != nil {
		return err
	}
	defer body.Close()
	pcm, err := j.Decoder.Decode(ctx, body)
	if err != nil {
		return sanitize(err)
	}
	f, err := audio.Analyze(pcm)
	if err != nil {
		return err
	}
	return j.Store.SaveFeatures(ctx, id, audio.Version, repo.Features{
		Tempo: f.Tempo, Energy: f.RMS, LoudnessDB: f.LoudnessDB,
		Centroid: f.Centroid, Flatness: f.Flatness, Valence: f.Valence, Raw: f.Raw(),
	})
}

// sanitize keeps only the first line of decoder errors and drops anything URL-like.
func sanitize(err error) error {
	msg := strings.SplitN(err.Error(), "\n", 2)[0]
	if i := strings.Index(msg, "http"); i >= 0 {
		msg = msg[:i] + "[redacted]"
	}
	return errors.New(msg)
}

// TrainResult summarizes a training run.
type TrainResult struct {
	Version      int64
	Tracks       int
	Users        int
	Interactions int
	WithFeatures int
	CFItems      int
	FactorItems  int
	Took         time.Duration
}

// Train rebuilds CF, ALS and content neighbours and bumps the model version.
func (j *Jobs) Train(ctx context.Context) (TrainResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	start := time.Now()
	m, err := j.Store.LoadModel(ctx, audio.Version)
	if err != nil {
		return TrainResult{}, err
	}
	now := j.now()
	affs, err := j.Store.PositiveAffinities(ctx, now)
	if err != nil {
		return TrainResult{}, err
	}
	users := map[uuid.UUID]int{}
	var userIDs []uuid.UUID
	var inter []algo.Interaction
	for _, a := range affs {
		i, ok := m.Index[a.Track]
		if !ok {
			continue
		}
		u, ok := users[a.User]
		if !ok {
			u = len(userIDs)
			users[a.User] = u
			userIDs = append(userIDs, a.User)
		}
		inter = append(inter, algo.Interaction{User: u, Item: i, Value: a.Value})
	}
	res := TrainResult{Tracks: len(m.Tracks), Users: len(userIDs), Interactions: len(inter)}
	cf := algo.CoOccurrence(len(m.Tracks), inter, 20, 2)
	uf, itf := algo.ALS(len(userIDs), len(m.Tracks), inter, algo.DefaultALS)
	content := algo.ContentNeighbors(m.Tracks, 20)
	t := repo.Trained{
		CF: map[uuid.UUID][]repo.NeighborRow{}, Content: map[uuid.UUID][]repo.NeighborRow{},
		ItemFactors: map[uuid.UUID][]float32{}, UserFactors: map[uuid.UUID][]float32{}, TrainContent: true,
	}
	conv := func(ns []model.Neighbor) []repo.NeighborRow {
		out := make([]repo.NeighborRow, 0, len(ns))
		for _, n := range ns {
			if n.Sim > 0 {
				out = append(out, repo.NeighborRow{ID: m.Tracks[n.Idx].ID, Sim: n.Sim})
			}
		}
		return out
	}
	for i, tr := range m.Tracks {
		if tr.Emb != nil {
			res.WithFeatures++
		}
		if len(cf[i]) > 0 {
			t.CF[tr.ID] = conv(cf[i])
			res.CFItems++
		}
		if len(content[i]) > 0 {
			t.Content[tr.ID] = conv(content[i])
		}
		if itf[i] != nil {
			t.ItemFactors[tr.ID] = itf[i]
			res.FactorItems++
		}
		m.CF[i], m.Content[i], m.ItemFactors[i] = cf[i], content[i], itf[i]
	}
	for u, f := range uf {
		if f != nil {
			t.UserFactors[userIDs[u]] = f
		}
	}
	if res.Version, err = j.Store.SaveModel(ctx, t, now); err != nil {
		return res, fmt.Errorf("save model: %w", err)
	}
	m.Version = res.Version
	j.snapshot.Store(m)
	res.Took = time.Since(start)
	return res, nil
}

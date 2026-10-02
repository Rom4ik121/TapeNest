package jobs_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/jobs"
	"github.com/tapenest/tapenest/services/reco-service/internal/musicclient"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
	"github.com/tapenest/tapenest/services/reco-service/internal/testutil"
)

// fakeDecoder returns a tone whose pitch depends on the payload.
type fakeDecoder struct{}

func (fakeDecoder) Decode(_ context.Context, r io.Reader) ([]float32, error) {
	b, _ := io.ReadAll(r)
	if strings.HasPrefix(string(b), "bad") {
		return nil, errors.New("ffmpeg: invalid data at http://secret.example/x?t=abc\nmore")
	}
	freq := 200 + float64(len(b))*50
	out := make([]float32, audio.SampleRate*4)
	for i := range out {
		out[i] = float32(0.3 * math.Sin(2*math.Pi*freq*float64(i)/audio.SampleRate))
	}
	return out, nil
}

type music struct {
	tracks []musicclient.Track
	audio  map[uuid.UUID]string
}

func (m *music) CatalogPage(_ context.Context, after *uuid.UUID, limit int) ([]musicclient.Track, *uuid.UUID, error) {
	start := 0
	if after != nil {
		for i, t := range m.tracks {
			if t.ID == *after {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(m.tracks))
	var next *uuid.UUID
	if end < len(m.tracks) {
		id := m.tracks[end-1].ID
		next = &id
	}
	return m.tracks[start:end], next, nil
}

func (m *music) Audio(_ context.Context, id uuid.UUID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(m.audio[id])), nil
}

func TestSyncAnalyzeTrain(t *testing.T) {
	store := testutil.NewMem()
	mu := &music{audio: map[uuid.UUID]string{}}
	artist := uuid.New()
	for i := 0; i < 5; i++ {
		id := uuid.New()
		mu.tracks = append(mu.tracks, musicclient.Track{ID: id, Title: "t", ArtistID: artist, Artist: "a", Genre: "Jazz", CreatedAt: time.Now()})
		mu.audio[id] = strings.Repeat("x", i+1)
	}
	mu.audio[mu.tracks[4].ID] = "bad"
	j := &jobs.Jobs{
		Music: mu, Store: store, Decoder: fakeDecoder{}, Workers: 2, PageSize: 2,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	seen, _, err := j.SyncCatalog(context.Background())
	if err != nil || seen != 5 || len(store.Tracks) != 5 {
		t.Fatalf("sync seen=%d err=%v", seen, err)
	}
	n, err := j.AnalyzePending(context.Background(), 10)
	if err != nil || n != 4 {
		t.Fatalf("analysed %d err %v", n, err)
	}
	msg := store.Errors[mu.tracks[4].ID]
	if msg == "" || strings.Contains(msg, "secret.example") || strings.Contains(msg, "more") {
		t.Fatalf("analysis error must be recorded and scrubbed: %q", msg)
	}
	ids := []uuid.UUID{mu.tracks[0].ID, mu.tracks[1].ID, mu.tracks[2].ID}
	u1, u2 := uuid.New(), uuid.New()
	store.Affs = []repo.Affinity{
		{User: u1, Track: ids[0], Value: 2},
		{User: u1, Track: ids[1], Value: 1},
		{User: u2, Track: ids[0], Value: 1},
		{User: u2, Track: ids[1], Value: 3},
		{User: u2, Track: ids[2], Value: 1},
		{User: u2, Track: uuid.New(), Value: 1},
	}
	res, err := j.Train(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 1 || res.Users != 2 || res.Interactions != 5 || res.WithFeatures != 4 || res.CFItems == 0 || res.FactorItems != 3 {
		t.Fatalf("train %+v", res)
	}
	saved := store.Saved[0]
	if len(saved.UserFactors) != 2 || len(saved.Content) != 4 || !saved.TrainContent {
		t.Fatalf("saved model %+v", saved)
	}
	if s := j.Snapshot(); s == nil || s.Version != 1 {
		t.Fatal("snapshot not published")
	}
	if n, _ := j.AnalyzePending(context.Background(), 10); n != 0 {
		t.Fatal("nothing left to analyse")
	}
}

func TestEmptyCatalogDoesNotDelete(t *testing.T) {
	j := &jobs.Jobs{Music: &music{}, Store: testutil.NewMem(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if seen, del, err := j.SyncCatalog(context.Background()); seen != 0 || del != 0 || err != nil {
		t.Fatal("empty sync")
	}
}

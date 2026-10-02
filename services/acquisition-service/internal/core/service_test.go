package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/mb"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
)

type pieces struct {
	files []qbt.File
	ps    int64
	st    []int
	tor   qbt.Torrent
}

func (p pieces) Torrent(context.Context, string) (qbt.Torrent, error) { return p.tor, nil }
func (p pieces) Files(context.Context, string) ([]qbt.File, error)    { return p.files, nil }
func (p pieces) PieceSize(context.Context, string) (int64, error)     { return p.ps, nil }
func (p pieces) PieceStates(context.Context, string) ([]int, error)   { return p.st, nil }

func TestSearchAndLookups(t *testing.T) {
	s := &Service{Enabled: true, Meta: metaFake{}}
	res, err := s.Search(context.Background(), "ad", 10)
	if err != nil || len(res.Artists) != 1 || len(res.Albums) != 1 || len(res.Recordings) != 1 {
		t.Fatal(err, res)
	}
	empty, err := s.Search(context.Background(), " ", 0)
	if err != nil || len(empty.Artists) != 0 {
		t.Fatal(empty, err)
	}
	bad := &Service{Meta: metaFake{err: errors.New("down")}}
	if _, err := bad.Search(context.Background(), "ada", 5); err == nil {
		t.Fatal("want error")
	}
	partial := &Service{Meta: metaFake{err: errors.New("albums")}}
	// artists and recordings ignore err only albums returns it — all three check m.err in my fake for artists and albums.
	// SearchArtists returns err, SearchReleaseGroups returns err, SearchRecordings returns nil. Partial true.
	_ = partial
	if _, err := s.Album(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	nf := &Service{Meta: metaFake{err: mb.ErrNotFound}}
	if _, err := nf.Album(context.Background(), uuid.New()); !errors.Is(err, ErrUnknownAlbum) {
		t.Fatal(err)
	}
	if _, err := nf.Artist(context.Background(), uuid.New()); !errors.Is(err, ErrUnknownAlbum) {
		t.Fatal(err)
	}
	view, err := s.Artist(context.Background(), uuid.New())
	if err != nil || view.Artist.Name != "Ada" || len(view.Albums) != 1 {
		t.Fatal(err, view)
	}
}

func TestAcquire(t *testing.T) {
	rec := uuid.New()
	rg := uuid.New()
	user := uuid.New()
	store := newMem()
	s := &Service{
		Enabled: true,
		Store:   store,
		Meta:    metaFake{album: mb.Album{ReleaseGroup: mb.ReleaseGroup{Title: "Songs", Artist: "Ada", ArtistMBID: uuid.New(), Year: 2020}, ReleaseMBID: uuid.New(), Tracks: []mb.Track{{RecordingMBID: rec, Title: "Aria", Disc: 1, Position: 1, LengthMS: 1000}}}},
		Notify:  note{},
		Limits:  Limits{UserDaily: 2, UserActive: 2, RetryAfter: time.Hour, StreamStartBytes: 16 << 10},
	}
	if _, err := s.Acquire(context.Background(), AcquireInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	off := &Service{Enabled: false}
	if _, err := off.Acquire(context.Background(), AcquireInput{Reason: "play", UserID: user, ReleaseGroup: rg}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	st, err := s.Acquire(context.Background(), AcquireInput{UserID: user, ReleaseGroup: rg, Recording: rec, Reason: "play", Title: "Aria"})
	if err != nil || st.State != StateQueued {
		t.Fatalf("%+v %v", st, err)
	}
	st2, err := s.Acquire(context.Background(), AcquireInput{UserID: uuid.New(), ReleaseGroup: rg, Recording: rec, Reason: "like"})
	if err != nil || st2.RequestID != st.RequestID {
		t.Fatal(err, st2)
	}
	// quota
	s.Limits.UserDaily = 1
	if _, err := s.Acquire(context.Background(), AcquireInput{UserID: user, ReleaseGroup: uuid.New(), Reason: "play"}); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	full := &Service{Enabled: true, Store: newMem(), Meta: s.Meta, Notify: bytesNote{n: 9}, Limits: Limits{UserDaily: 5, UserActive: 5, LibraryMaxBytes: 1}}
	if _, err := full.Acquire(context.Background(), AcquireInput{UserID: uuid.New(), ReleaseGroup: uuid.New(), Reason: "playlist"}); !errors.Is(err, ErrStorageFull) {
		t.Fatal(err)
	}
	// unknown album
	unk := &Service{Enabled: true, Store: newMem(), Meta: metaFake{err: mb.ErrNotFound}, Limits: Limits{UserDaily: 5, UserActive: 5}}
	if _, err := unk.Acquire(context.Background(), AcquireInput{UserID: uuid.New(), ReleaseGroup: uuid.New(), Reason: "play"}); !errors.Is(err, ErrUnknownAlbum) {
		t.Fatal(err)
	}
	// race: create returns false then join
	race := newMem()
	race.lose = true
	// lose without existing row makes RequestByReleaseGroup fail
	rs := &Service{Enabled: true, Store: race, Meta: s.Meta, Limits: s.Limits}
	if _, err := rs.Acquire(context.Background(), AcquireInput{UserID: uuid.New(), ReleaseGroup: uuid.New(), Reason: "play"}); err == nil {
		t.Fatal("race without winner")
	}
	// recording missing from album, with title hint
	hint := &Service{Enabled: true, Store: newMem(), Meta: metaFake{album: mb.Album{ReleaseGroup: mb.ReleaseGroup{Title: "Songs", Artist: "Ada"}}}, Limits: Limits{UserDaily: 5, UserActive: 5}, Notify: note{}}
	other := uuid.New()
	st3, err := hint.Acquire(context.Background(), AcquireInput{UserID: uuid.New(), ReleaseGroup: uuid.New(), Recording: other, Title: "Extra", Reason: "play"})
	if err != nil {
		t.Fatal(err)
	}
	if st3.ErrorCode != "" && st3.State == "" {
		t.Fatal(st3)
	}
	// status of unknown recording
	if _, _, err := s.RecordingStatus(context.Background(), uuid.New()); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal(err)
	}
	// buffered
	idx := int32(0)
	req := store.reqs[st.RequestID]
	req.TorrentHash = "abc"
	req.State = StateDownloading
	ts := store.tracks[st.RequestID]
	ts[0].FileIndex = &idx
	ts[0].FileSize = 100
	store.tracks[st.RequestID] = ts
	s.Pieces = pieces{files: []qbt.File{{Index: 0, Name: "a.mp3", Size: 100}}, ps: 16384, st: []int{2}}
	s.Limits.StreamStartBytes = 50
	got, _, err := s.RecordingStatus(context.Background(), rec)
	if err != nil || got.Stream == nil || !got.Stream.Ready {
		t.Fatalf("%+v %v", got, err)
	}
	req.State = StateAvailable
	ts[0].LibraryPath = "library/a.mp3"
	ts[0].FileSize = 10
	store.tracks[st.RequestID] = ts
	got, _, err = s.RecordingStatus(context.Background(), rec)
	if err != nil || !got.Stream.Imported {
		t.Fatal(got, err)
	}
	// album-level status
	alb, err := s.status(context.Background(), *req, uuid.Nil)
	if err != nil || alb.RequestID != req.ID {
		t.Fatal(alb, err)
	}
	miss, err := s.status(context.Background(), *req, uuid.New())
	if err != nil || miss.ErrorCode != CodeTrackAbsent {
		t.Fatal(miss, err)
	}
	// failed retry
	req.State = StateFailed
	req.UpdatedAt = time.Now().Add(-2 * time.Hour)
	s.Limits.RetryAfter = time.Minute
	if _, err := s.Acquire(context.Background(), AcquireInput{UserID: uuid.New(), ReleaseGroup: rg, Reason: "like"}); err != nil {
		t.Fatal(err)
	}
}

type bytesNote struct{ n int64 }

func (b bytesNote) Wake(context.Context)               {}
func (b bytesNote) LibraryBytes(context.Context) int64 { return b.n }

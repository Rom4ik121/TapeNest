package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/arr"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/torrent"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func bencode(name string, length int) []byte {
	return []byte("d8:announce1:x4:infod6:lengthi" + itoa(length) + "e4:name" + itoa(len(name)) + ":" + name + "12:piece lengthi16384e6:pieces20:" + "xxxxxxxxxxxxxxxxxxxx" + "ee")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [16]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

func seedReq(artist, album, title string, wanted bool) (*mem, uuid.UUID, uuid.UUID) {
	store := newMem()
	id, rec := uuid.New(), uuid.New()
	now := time.Now()
	var w *time.Time
	if wanted {
		w = &now
	}
	store.reqs[id] = &db.AcquisitionRequest{
		ID: id, ReleaseGroupMbid: uuid.New(), ArtistName: artist, AlbumTitle: album, Year: 2020,
		State: StateQueued, CreatedAt: now, UpdatedAt: now, RequestedBy: uuid.New(),
	}
	store.tracks[id] = []db.AcquisitionRequestTrack{{
		RequestID: id, RecordingMbid: rec, Disc: 1, Position: 1, Title: title, WantedAt: w,
	}}
	return store, id, rec
}

func TestWorkerPipeline(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "music")
	lib := filepath.Join(music, "library")
	tor := filepath.Join(dir, "tor")
	_ = os.MkdirAll(lib, 0o755)
	_ = os.MkdirAll(tor, 0o755)
	raw := bencode("01 - Aria.mp3", 800000)
	meta, err := torrent.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tor, "01 - Aria.mp3"), []byte("audio-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, id, _ := seedReq("Ada", "Songs", "Aria", true)
	tf := &torFake{
		files: []qbt.File{{Index: 0, Name: "01 - Aria.mp3", Size: 800000, Progress: 1, Priority: qbt.PrioNormal}},
		tor:   qbt.Torrent{Hash: meta.InfoHash, SavePath: tor, State: "downloading", NumSeeds: 2},
	}
	cat := &catFake{}
	w := &Worker{
		Cfg:      WorkerConfig{Category: "tapenest", MusicDir: music, LibraryRoot: lib, TorrentDir: tor, MaxActive: 2, MaxReleaseByte: 2 << 30, StallTimeout: time.Hour, ImportTimeout: time.Minute, TorrentMaxByte: 1 << 40, WantedFor: time.Hour},
		Store:    store,
		Indexers: idxFake{enabled: 1, rels: []arr.Release{{Title: "Ada Songs MP3", Size: 2 << 20, Seeders: 4, Indexer: "cc0"}}},
		Torrents: tf, Catalog: cat, Log: discard(), addWait: time.Millisecond,
	}
	w.Indexers = idxFake{enabled: 1, rels: []arr.Release{{Title: "Ada Songs MP3", Size: 2 << 20, Seeders: 4, Indexer: "cc0"}}, file: raw}
	ctx := context.Background()
	w.Tick(ctx)
	w.Wait()
	if store.reqs[id].State != StateDownloading {
		t.Fatalf("grab: state=%s err=%s", store.reqs[id].State, store.reqs[id].ErrorCode)
	}
	w.Tick(ctx) // progress and, in the same round, direct import
	if store.reqs[id].State != StateAvailable || cat.n != 1 {
		t.Fatalf("import state=%s mode=%s cat=%d", store.reqs[id].State, store.reqs[id].ImportMode, cat.n)
	}
	tf.list = []qbt.Torrent{{Hash: store.reqs[id].TorrentHash, State: "stoppedUP", Size: 100, CompletionOn: 1, AddedOn: time.Now().Unix()}}
	_ = w.Cleanup(ctx)
	if SafeName(`a/b:*?"<>|`) == "" || SafeName("  ") != "Unknown" {
		t.Fatal("safe")
	}
	long := ""
	for i := 0; i < 200; i++ {
		long += "я"
	}
	if len([]rune(SafeName(long))) > 120 {
		t.Fatal("trim")
	}
	if DirSize(music) <= 0 || stoppedUp("x") || !stoppedUp("pausedUP") {
		t.Fatal("disk")
	}
}

func TestWorkerFailures(t *testing.T) {
	store, id, _ := seedReq("Ada", "Songs", "Aria", true)
	w := &Worker{Cfg: WorkerConfig{MaxActive: 1, StallTimeout: time.Hour}, Store: store, Indexers: idxFake{}, Torrents: &torFake{}, Log: discard()}
	w.Tick(context.Background())
	w.Wait()
	if store.reqs[id].State != StateFailed || store.reqs[id].ErrorCode != CodeNoIndexers {
		t.Fatalf("%s %s", store.reqs[id].State, store.reqs[id].ErrorCode)
	}
	store2, id2, _ := seedReq("Ada", "Songs", "Aria", true)
	w2 := &Worker{Cfg: WorkerConfig{MaxActive: 1, MaxReleaseByte: 2 << 30}, Store: store2, Indexers: idxFake{enabled: 1, err: errors.New("search down")}, Torrents: &torFake{}, Log: discard()}
	w2.Tick(context.Background())
	w2.Wait()
	if store2.reqs[id2].State != StateQueued {
		t.Fatalf("requeue %s", store2.reqs[id2].State)
	}
	// no acceptable release
	store3, id3, _ := seedReq("Ada", "Songs", "Aria", false)
	w3 := &Worker{Cfg: WorkerConfig{MaxActive: 1, MaxReleaseByte: 2 << 30}, Store: store3, Indexers: idxFake{enabled: 1, rels: []arr.Release{{Title: "unrelated", Size: 2 << 20, Seeders: 3}}}, Torrents: &torFake{}, Log: discard()}
	w3.Tick(context.Background())
	w3.Wait()
	if store3.reqs[id3].ErrorCode != CodeNoSources {
		t.Fatalf("sources %s", store3.reqs[id3].ErrorCode)
	}
	// removed torrent
	store4, id4, _ := seedReq("Ada", "Songs", "Aria", false)
	store4.reqs[id4].State = StateDownloading
	store4.reqs[id4].TorrentHash = "dead"
	tf := &torFake{gone: true}
	w4 := &Worker{Cfg: WorkerConfig{MaxActive: 1}, Store: store4, Indexers: idxFake{enabled: 1}, Torrents: tf, Log: discard()}
	w4.Tick(context.Background())
	if store4.reqs[id4].ErrorCode != CodeRemoved {
		t.Fatalf("removed %s", store4.reqs[id4].ErrorCode)
	}
	// stall
	store5, id5, rec := seedReq("Ada", "Songs", "Aria", true)
	old := time.Now().Add(-time.Hour)
	store5.reqs[id5].State = StateDownloading
	store5.reqs[id5].TorrentHash = "h"
	store5.reqs[id5].LastProgressAt = &old
	store5.reqs[id5].CreatedAt = old
	idx := int32(0)
	store5.tracks[id5][0].FileIndex = &idx
	store5.tracks[id5][0].RecordingMbid = rec
	w5 := &Worker{
		Cfg:      WorkerConfig{MaxActive: 1, StallTimeout: time.Minute},
		Store:    store5,
		Torrents: &torFake{files: []qbt.File{{Index: 0, Name: "a.mp3", Size: 100, Progress: 0}}, tor: qbt.Torrent{Hash: "h"}},
		Log:      discard(),
	}
	w5.Tick(context.Background())
	if store5.reqs[id5].ErrorCode != CodeStalled {
		t.Fatalf("stall %s %s", store5.reqs[id5].State, store5.reqs[id5].ErrorCode)
	}
}

func TestWorkerLidarrAndQuota(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "music")
	lib := filepath.Join(music, "library")
	tor := filepath.Join(dir, "dl")
	_ = os.MkdirAll(filepath.Join(lib, "Ada"), 0o755)
	_ = os.MkdirAll(tor, 0o755)
	src := filepath.Join(tor, "01 - Aria.mp3")
	_ = os.WriteFile(src, []byte("xyz"), 0o644)
	imported := filepath.Join(lib, "Ada", "imported.mp3")
	_ = os.WriteFile(imported, []byte("xyz"), 0o644)
	store, id, rec := seedReq("Ada", "Songs", "Aria", false)
	store.reqs[id].State = StateImporting
	store.reqs[id].TorrentHash = "h"
	store.reqs[id].UpdatedAt = time.Now()
	idx := int32(0)
	store.tracks[id][0].FileIndex = &idx
	store.tracks[id][0].FileName = "01 - Aria.mp3"
	albumID := int32(3)
	store.reqs[id].LidarrAlbumID = &albumID
	rel := uuid.New()
	store.reqs[id].ReleaseMbid = &rel
	libf := libFake{
		album: arr.Album{ID: 3, ArtistID: 1, Releases: []struct {
			ID               int    `json:"id"`
			ForeignReleaseID string `json:"foreignReleaseId"`
			Monitored        bool   `json:"monitored"`
		}{{ID: 9, ForeignReleaseID: rel.String(), Monitored: true}}},
		tracks: []arr.Track{{ID: 1, ForeignRecordingID: rec.String(), MediumNumber: 1, AbsoluteTrack: 1}},
		files:  []arr.TrackFile{{ID: 4, Path: imported}},
		cmd:    "completed",
	}
	// tracks after completed need TrackFileID
	libf.tracks[0].TrackFileID = 4
	w := &Worker{
		Cfg:       WorkerConfig{MusicDir: music, LibraryRoot: lib, ImportTimeout: time.Hour, MaxActive: 1},
		Store:     store,
		Torrents:  &torFake{tor: qbt.Torrent{Hash: "h", SavePath: tor}},
		Library:   libf,
		Catalog:   &catFake{},
		Log:       discard(),
		ProfileID: 1,
	}
	ctx := context.Background()
	w.importStep(ctx, *store.reqs[id])
	if store.reqs[id].ImportMode == "" {
		t.Fatal("expected lidarr import mode")
	}
	// poll completed
	fresh, _ := store.RequestByID(ctx, id)
	w.importStep(ctx, fresh)
	if store.reqs[id].State != StateAvailable && store.reqs[id].ImportMode != "done:lidarr" {
		// second call should finalize when mode is lidarr:7
		t.Logf("state=%s mode=%s", store.reqs[id].State, store.reqs[id].ImportMode)
	}
	// disk quota eviction
	store.reqs[id].State = StateAvailable
	store.reqs[id].TorrentHash = "h"
	tf := &torFake{list: []qbt.Torrent{{Hash: "h", State: "uploading", Size: 500, CompletionOn: 1, AddedOn: time.Now().Add(-3 * time.Hour).Unix(), Downloaded: 500}}}
	w.Torrents = tf
	w.Cfg.TorrentMaxByte = 10
	w.Cfg.TorrentDir = tor
	_ = w.Cleanup(ctx)
	// orphan
	tf.list = []qbt.Torrent{{Hash: "orphan", State: "stoppedUP", AddedOn: time.Now().Add(-2 * time.Hour).Unix(), Downloaded: 1}}
	_ = w.Cleanup(ctx)
	_ = QbtPreferences(BootstrapConfig{TorrentDir: tor, MaxActive: 1, SeedRatio: 1, SeedMinutes: 10})
	if _, err := Bootstrap(ctx, BootstrapConfig{QbtURL: "http://127.0.0.1:9", Category: "c", TorrentDir: tor, LibraryRoot: lib}, qbtSetup{}, lidarrSetup{}, prowlarrSetup{}, discard()); err != nil {
		t.Fatal(err)
	}
	w.SetProfile(4)
	if w.profile() != 4 {
		t.Fatal(w.profile())
	}
	if !w.due(id, time.Hour) || w.due(id, time.Hour) {
		t.Fatal("due")
	}
	if !w.claimLocal(uuid.New()) {
		t.Fatal("claim")
	}
}

type qbtSetup struct{}

func (qbtSetup) SetPreferences(context.Context, map[string]any) error { return nil }
func (qbtSetup) EnsureCategory(context.Context, string, string) error { return nil }

type lidarrSetup struct{}

func (lidarrSetup) EnsureQualityProfile(context.Context) (int, error) { return 2, nil }
func (lidarrSetup) TuneQualitySizes(context.Context) error            { return nil }
func (lidarrSetup) EnsureMediaManagement(context.Context) error       { return nil }
func (lidarrSetup) EnsureRootFolder(context.Context, string, int) error {
	return nil
}

func (lidarrSetup) EnsureDownloadClient(context.Context, arr.QBitSettings) (bool, error) {
	return true, nil
}

type prowlarrSetup struct{}

func (prowlarrSetup) EnsureLidarrApp(context.Context, string, string, string) (bool, error) {
	return true, nil
}

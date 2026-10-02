package core

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/arr"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/mb"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
)

type mem struct {
	mu     sync.Mutex
	reqs   map[uuid.UUID]*db.AcquisitionRequest
	byRG   map[uuid.UUID]uuid.UUID
	tracks map[uuid.UUID][]db.AcquisitionRequestTrack
	users  map[string]struct{}
	events int
	lose   bool
}

func newMem() *mem {
	return &mem{reqs: map[uuid.UUID]*db.AcquisitionRequest{}, byRG: map[uuid.UUID]uuid.UUID{}, tracks: map[uuid.UUID][]db.AcquisitionRequestTrack{}, users: map[string]struct{}{}}
}

func (m *mem) copyReq(r *db.AcquisitionRequest) db.AcquisitionRequest {
	if r == nil {
		return db.AcquisitionRequest{}
	}
	return *r
}

func (m *mem) RequestByReleaseGroup(_ context.Context, rg uuid.UUID) (db.AcquisitionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byRG[rg]
	if !ok {
		return db.AcquisitionRequest{}, pgx.ErrNoRows
	}
	return m.copyReq(m.reqs[id]), nil
}

func (m *mem) RequestByID(_ context.Context, id uuid.UUID) (db.AcquisitionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reqs[id]
	if !ok {
		return db.AcquisitionRequest{}, pgx.ErrNoRows
	}
	return m.copyReq(r), nil
}

func (m *mem) CreateRequest(_ context.Context, n repo.NewRequest) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byRG[n.Request.ReleaseGroupMbid]; ok || m.lose {
		m.lose = false
		return false, nil
	}
	now := time.Now()
	r := db.AcquisitionRequest{
		ID: n.Request.ID, ReleaseGroupMbid: n.Request.ReleaseGroupMbid, ReleaseMbid: n.Request.ReleaseMbid,
		ArtistMbid: n.Request.ArtistMbid, ArtistName: n.Request.ArtistName, AlbumTitle: n.Request.AlbumTitle,
		Year: n.Request.Year, State: StateQueued, Priority: n.Request.Priority, RequestedBy: n.Request.RequestedBy,
		CreatedAt: now, UpdatedAt: now,
	}
	m.reqs[r.ID] = &r
	m.byRG[r.ReleaseGroupMbid] = r.ID
	for _, tr := range n.Tracks {
		m.tracks[r.ID] = append(m.tracks[r.ID], db.AcquisitionRequestTrack{
			RequestID: r.ID, RecordingMbid: tr.RecordingMbid, Disc: tr.Disc, Position: tr.Position,
			Title: tr.Title, LengthMs: tr.LengthMs, WantedAt: tr.WantedAt,
		})
	}
	m.users[r.ID.String()+n.UserID.String()] = struct{}{}
	return true, nil
}

func (m *mem) AddRequestUser(_ context.Context, arg db.AddRequestUserParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := arg.RequestID.String() + arg.UserID.String()
	if _, ok := m.users[k]; ok {
		return 0, nil
	}
	m.users[k] = struct{}{}
	return 1, nil
}

func (m *mem) BumpPriority(_ context.Context, arg db.BumpPriorityParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.reqs[arg.ID]; r != nil && arg.Priority > r.Priority {
		r.Priority = arg.Priority
	}
	return nil
}

func (m *mem) MarkWanted(_ context.Context, arg db.MarkWantedParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	ts := m.tracks[arg.RequestID]
	for i := range ts {
		if ts[i].RecordingMbid == arg.RecordingMbid {
			ts[i].WantedAt = &now
		}
	}
	m.tracks[arg.RequestID] = ts
	return nil
}

func (m *mem) RequeueFailed(_ context.Context, id uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.reqs[id]
	if r == nil || r.State != StateFailed {
		return 0, nil
	}
	r.State = StateQueued
	r.ErrorCode = ""
	r.UpdatedAt = time.Now()
	return 1, nil
}

func (m *mem) CountUserRequestsSince(_ context.Context, arg db.CountUserRequestsSinceParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.reqs {
		if r.RequestedBy == arg.UserID && r.CreatedAt.After(arg.Since) {
			n++
		}
	}
	return n, nil
}

func (m *mem) CountUserActive(_ context.Context, userID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.reqs {
		if r.RequestedBy == userID && r.State != StateAvailable && r.State != StateFailed {
			n++
		}
	}
	return n, nil
}

func (m *mem) TrackByRecording(_ context.Context, rec uuid.UUID) (db.TrackByRecordingRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, ts := range m.tracks {
		for _, t := range ts {
			if t.RecordingMbid == rec {
				r := m.reqs[id]
				return db.TrackByRecordingRow{
					RequestID: r.ID, RecordingMbid: t.RecordingMbid, Disc: t.Disc, Position: t.Position, Title: t.Title,
					FileIndex: t.FileIndex, FileName: t.FileName, FileSize: t.FileSize, LibraryPath: t.LibraryPath, WantedAt: t.WantedAt,
					State: r.State, TorrentHash: r.TorrentHash, ErrorCode: r.ErrorCode, Progress: r.Progress,
				}, nil
			}
		}
	}
	return db.TrackByRecordingRow{}, pgx.ErrNoRows
}

func (m *mem) InsertRequestTrack(_ context.Context, arg db.InsertRequestTrackParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracks[arg.RequestID] = append(m.tracks[arg.RequestID], db.AcquisitionRequestTrack{
		RequestID: arg.RequestID, RecordingMbid: arg.RecordingMbid, Disc: arg.Disc, Position: arg.Position, Title: arg.Title, WantedAt: arg.WantedAt,
	})
	return nil
}

func (m *mem) Event(context.Context, uuid.UUID, string, *uuid.UUID, map[string]any) error {
	m.mu.Lock()
	m.events++
	m.mu.Unlock()
	return nil
}

func (m *mem) ClaimQueued(_ context.Context, lim int32) ([]db.AcquisitionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.AcquisitionRequest
	for _, r := range m.reqs {
		if r.State == StateQueued && int32(len(out)) < lim {
			r.State = StateSearching
			now := time.Now()
			r.UpdatedAt = now
			r.StartedAt = &now
			out = append(out, *r)
		}
	}
	return out, nil
}

func (m *mem) CountActive(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.reqs {
		if r.State == StateSearching || r.State == StateDownloading || r.State == StateImporting {
			n++
		}
	}
	return n, nil
}

func (m *mem) ListByState(_ context.Context, arg db.ListByStateParams) ([]db.AcquisitionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.AcquisitionRequest
	for _, r := range m.reqs {
		if r.State == arg.State {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (m *mem) ResetStaleSearching(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.reqs {
		if r.State == StateSearching && r.UpdatedAt.Before(before) {
			r.State = StateQueued
			n++
		}
	}
	return n, nil
}

func (m *mem) SetDownloading(_ context.Context, arg db.SetDownloadingParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.reqs[arg.ID]
	r.State = StateDownloading
	r.TorrentHash = arg.TorrentHash
	r.ReleaseTitle = arg.ReleaseTitle
	r.Indexer = arg.Indexer
	r.Quality = arg.Quality
	r.SizeBytes = arg.SizeBytes
	r.Seeders = arg.Seeders
	r.FileExt = arg.FileExt
	now := time.Now()
	r.LastProgressAt = &now
	r.UpdatedAt = now
	return nil
}

func (m *mem) SetProgress(_ context.Context, arg db.SetProgressParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.reqs[arg.ID]
	r.Progress = arg.Progress
	r.Seeders = arg.Seeders
	if arg.Advanced {
		now := time.Now()
		r.LastProgressAt = &now
	}
	return nil
}

func (m *mem) SetState(_ context.Context, arg db.SetStateParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.reqs[arg.ID]
	r.State = arg.State
	r.ErrorCode = arg.ErrorCode
	r.UpdatedAt = time.Now()
	return nil
}

func (m *mem) SetLidarrAlbum(_ context.Context, arg db.SetLidarrAlbumParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs[arg.ID].LidarrAlbumID = arg.LidarrAlbumID
	return nil
}

func (m *mem) SetImportMode(_ context.Context, arg db.SetImportModeParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs[arg.ID].ImportMode = arg.ImportMode
	m.reqs[arg.ID].UpdatedAt = time.Now()
	return nil
}

func (m *mem) RequestTracks(_ context.Context, id uuid.UUID) ([]db.AcquisitionRequestTrack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]db.AcquisitionRequestTrack(nil), m.tracks[id]...)
	return out, nil
}

func (m *mem) SetTrackFile(_ context.Context, arg db.SetTrackFileParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.tracks[arg.RequestID]
	for i := range ts {
		if ts[i].RecordingMbid == arg.RecordingMbid {
			ts[i].FileIndex = arg.FileIndex
			ts[i].FileName = arg.FileName
			ts[i].FileSize = arg.FileSize
		}
	}
	m.tracks[arg.RequestID] = ts
	return nil
}

func (m *mem) SetTrackLibraryPath(_ context.Context, arg db.SetTrackLibraryPathParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.tracks[arg.RequestID]
	for i := range ts {
		if ts[i].RecordingMbid == arg.RecordingMbid {
			ts[i].LibraryPath = arg.LibraryPath
		}
	}
	m.tracks[arg.RequestID] = ts
	return nil
}

func (m *mem) RequestByHash(_ context.Context, hash string) (db.AcquisitionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reqs {
		if r.TorrentHash == hash && hash != "" {
			return *r, nil
		}
	}
	return db.AcquisitionRequest{}, pgx.ErrNoRows
}

type metaFake struct {
	album mb.Album
	err   error
}

func (m metaFake) SearchArtists(context.Context, string, int) ([]mb.Artist, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []mb.Artist{{MBID: uuid.New(), Name: "Ada", Score: 100}}, nil
}

func (m metaFake) SearchReleaseGroups(context.Context, string, int) ([]mb.ReleaseGroup, error) {
	return []mb.ReleaseGroup{{MBID: uuid.New(), Title: "Songs", Artist: "Ada"}}, m.err
}

func (m metaFake) SearchRecordings(context.Context, string, int) ([]mb.Recording, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []mb.Recording{{MBID: uuid.New(), Title: "Aria", Artist: "Ada", Album: "Songs"}}, nil
}

func (m metaFake) Album(context.Context, uuid.UUID) (mb.Album, error) {
	if m.err != nil {
		return mb.Album{}, m.err
	}
	return m.album, nil
}

func (m metaFake) Discography(context.Context, uuid.UUID, int) (mb.Artist, []mb.ReleaseGroup, error) {
	if m.err != nil {
		return mb.Artist{}, nil, mb.ErrNotFound
	}
	return mb.Artist{Name: "Ada"}, []mb.ReleaseGroup{{Title: "Songs"}}, nil
}

type note struct{}

func (note) Wake(context.Context)               {}
func (note) LibraryBytes(context.Context) int64 { return 0 }

type idxFake struct {
	enabled int
	rels    []arr.Release
	file    []byte
	magnet  string
	err     error
}

func (i idxFake) Indexers(context.Context) ([]arr.Indexer, error) {
	if i.err != nil && i.enabled == 0 && i.rels == nil {
		return nil, i.err
	}
	out := make([]arr.Indexer, i.enabled)
	for n := range out {
		out[n] = arr.Indexer{ID: n + 1, Name: "cc0", Enable: true, Protocol: "torrent"}
	}
	return out, nil
}

func (i idxFake) Search(context.Context, string, int) ([]arr.Release, error) {
	if i.err != nil {
		return nil, i.err
	}
	return i.rels, nil
}

func (i idxFake) Download(context.Context, arr.Release) ([]byte, string, error) {
	return i.file, i.magnet, nil
}

type torFake struct {
	mu    sync.Mutex
	files []qbt.File
	tor   qbt.Torrent
	gone  bool
	list  []qbt.Torrent
}

func (t *torFake) AddTorrent(context.Context, []byte, string, qbt.AddOptions) error { return nil }
func (t *torFake) Torrent(context.Context, string) (qbt.Torrent, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gone {
		return qbt.Torrent{}, qbt.ErrNotFound
	}
	return t.tor, nil
}

func (t *torFake) Torrents(context.Context, string, ...string) ([]qbt.Torrent, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.list != nil {
		return t.list, nil
	}
	return []qbt.Torrent{t.tor}, nil
}

func (t *torFake) Files(context.Context, string) ([]qbt.File, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]qbt.File(nil), t.files...), nil
}
func (t *torFake) SetFilePriority(context.Context, string, []int, int) error { return nil }
func (t *torFake) SetSequential(context.Context, string, bool) error         { return nil }
func (t *torFake) Start(context.Context, string) error                       { return nil }
func (t *torFake) Delete(context.Context, string, bool) error                { return nil }

type libFake struct {
	album  arr.Album
	tracks []arr.Track
	files  []arr.TrackFile
	cmd    string
	addErr error
}

func (l libFake) AddAlbum(context.Context, string, string, int) (arr.Album, error) {
	return l.album, l.addErr
}
func (l libFake) Tracks(context.Context, int) ([]arr.Track, error) { return l.tracks, nil }
func (l libFake) TrackFiles(context.Context, int) ([]arr.TrackFile, error) {
	return l.files, nil
}

func (l libFake) ManualImport(context.Context, string, arr.Album, int, []arr.ImportFile) (int, error) {
	return 7, nil
}
func (l libFake) CommandStatus(context.Context, int) (string, error) { return l.cmd, nil }

type catFake struct{ n int }

func (c *catFake) Refresh(context.Context, []CatalogFile) error { c.n++; return nil }

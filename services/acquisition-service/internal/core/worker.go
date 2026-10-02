package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/arr"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/match"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/torrent"
)

// WorkerStore is the repository subset used by the worker.
type WorkerStore interface {
	ClaimQueued(ctx context.Context, lim int32) ([]db.AcquisitionRequest, error)
	CountActive(ctx context.Context) (int64, error)
	ListByState(ctx context.Context, arg db.ListByStateParams) ([]db.AcquisitionRequest, error)
	ResetStaleSearching(ctx context.Context, before time.Time) (int64, error)
	SetDownloading(ctx context.Context, arg db.SetDownloadingParams) error
	SetProgress(ctx context.Context, arg db.SetProgressParams) error
	SetState(ctx context.Context, arg db.SetStateParams) error
	SetLidarrAlbum(ctx context.Context, arg db.SetLidarrAlbumParams) error
	SetImportMode(ctx context.Context, arg db.SetImportModeParams) error
	RequestTracks(ctx context.Context, requestID uuid.UUID) ([]db.AcquisitionRequestTrack, error)
	SetTrackFile(ctx context.Context, arg db.SetTrackFileParams) error
	SetTrackLibraryPath(ctx context.Context, arg db.SetTrackLibraryPathParams) error
	RequestByHash(ctx context.Context, hash string) (db.AcquisitionRequest, error)
	RequestByID(ctx context.Context, id uuid.UUID) (db.AcquisitionRequest, error)
	Event(ctx context.Context, id uuid.UUID, kind string, user *uuid.UUID, detail map[string]any) error
}

// Indexers is the Prowlarr subset.
type Indexers interface {
	Indexers(ctx context.Context) ([]arr.Indexer, error)
	Search(ctx context.Context, query string, limit int) ([]arr.Release, error)
	Download(ctx context.Context, r arr.Release) ([]byte, string, error)
}

// Torrents is the qBittorrent subset.
type Torrents interface {
	AddTorrent(ctx context.Context, file []byte, magnet string, o qbt.AddOptions) error
	Torrent(ctx context.Context, hash string) (qbt.Torrent, error)
	Torrents(ctx context.Context, category string, hashes ...string) ([]qbt.Torrent, error)
	Files(ctx context.Context, hash string) ([]qbt.File, error)
	SetFilePriority(ctx context.Context, hash string, ids []int, prio int) error
	SetSequential(ctx context.Context, hash string, on bool) error
	Start(ctx context.Context, hash string) error
	Delete(ctx context.Context, hash string, withFiles bool) error
}

// Library is the Lidarr subset (optional: nil → direct import only).
type Library interface {
	AddAlbum(ctx context.Context, rg, rootFolder string, profileID int) (arr.Album, error)
	Tracks(ctx context.Context, albumID int) ([]arr.Track, error)
	TrackFiles(ctx context.Context, albumID int) ([]arr.TrackFile, error)
	ManualImport(ctx context.Context, folder string, a arr.Album, releaseID int, files []arr.ImportFile) (int, error)
	CommandStatus(ctx context.Context, id int) (string, error)
}

// CatalogFile is one imported file for music-service (path relative to MUSIC_DIR).
type CatalogFile struct {
	Path          string    `json:"path"`
	RecordingMBID uuid.UUID `json:"recordingMbid"`
	ReleaseGroup  uuid.UUID `json:"releaseGroupMbid"`
	SizeBytes     int64     `json:"sizeBytes"`
}

// Catalog notifies music-service about new files.
type Catalog interface {
	Refresh(ctx context.Context, files []CatalogFile) error
}

// WorkerConfig holds worker tuning.
type WorkerConfig struct {
	Category       string
	MusicDir       string
	LibraryRoot    string
	TorrentDir     string
	MaxActive      int
	MaxReleaseByte int64
	StallTimeout   time.Duration
	ImportTimeout  time.Duration
	TorrentMaxByte int64
	WantedFor      time.Duration // how long a "play" keeps a file at top priority
}

// Worker drives requests through their states.
type Worker struct {
	Cfg       WorkerConfig
	Store     WorkerStore
	Indexers  Indexers
	Torrents  Torrents
	Library   Library
	Catalog   Catalog
	ProfileID int
	Log       *slog.Logger
	Now       func() time.Time

	mu       sync.Mutex
	wg       sync.WaitGroup
	backoff  map[uuid.UUID]time.Time
	inflight map[uuid.UUID]bool
	addWait  time.Duration
}

// SetProfile sets the Lidarr quality profile once bootstrap found it.
func (w *Worker) SetProfile(id int) {
	w.mu.Lock()
	w.ProfileID = id
	w.mu.Unlock()
}

func (w *Worker) profile() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ProfileID
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Worker) event(ctx context.Context, id uuid.UUID, kind string, d map[string]any) {
	if err := w.Store.Event(ctx, id, kind, nil, d); err != nil {
		w.Log.Warn("event", "kind", kind, "err", redact.Error(err))
	}
}

func (w *Worker) fail(ctx context.Context, req db.AcquisitionRequest, code string, d map[string]any) {
	if err := w.Store.SetState(ctx, db.SetStateParams{State: StateFailed, ErrorCode: code, ID: req.ID}); err != nil {
		w.Log.Error("set failed", "request", req.ID, "err", redact.Error(err))
	}
	if d == nil {
		d = map[string]any{}
	}
	d["code"] = code
	w.event(ctx, req.ID, "failed", d)
	w.Log.Info("request failed", "request", req.ID, "code", code)
}

// Tick runs one scheduling round: claims queued work and advances active requests.
func (w *Worker) Tick(ctx context.Context) {
	if _, err := w.Store.ResetStaleSearching(ctx, w.now().Add(-5*time.Minute)); err != nil {
		w.Log.Warn("reset stale", "err", redact.Error(err))
	}
	for _, st := range []string{StateDownloading, StateImporting} {
		reqs, err := w.Store.ListByState(ctx, db.ListByStateParams{State: st, Lim: 100})
		if err != nil {
			w.Log.Warn("list", "state", st, "err", redact.Error(err))
			continue
		}
		for _, r := range reqs {
			if st == StateDownloading {
				w.progress(ctx, r)
			} else {
				w.importStep(ctx, r)
			}
		}
	}
	active, err := w.Store.CountActive(ctx)
	if err != nil {
		return
	}
	free := w.Cfg.MaxActive - int(active)
	if free <= 0 {
		return
	}
	claimed, err := w.Store.ClaimQueued(ctx, int32(free)) //nolint:gosec // free is bounded by MaxActive (1..20)
	if err != nil {
		w.Log.Warn("claim", "err", redact.Error(err))
		return
	}
	for _, r := range claimed {
		if !w.claimLocal(r.ID) {
			continue
		}
		// searches run in the background so a slow indexer never delays the
		// priority/progress maintenance of downloads someone is listening to
		w.wg.Add(1)
		go func(r db.AcquisitionRequest) {
			defer w.wg.Done()
			defer w.releaseLocal(r.ID)
			w.search(ctx, r)
		}(r)
	}
}

// Wait blocks until background searches finish (shutdown, tests).
func (w *Worker) Wait() { w.wg.Wait() }

func (w *Worker) claimLocal(id uuid.UUID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inflight == nil {
		w.inflight = map[uuid.UUID]bool{}
		w.backoff = map[uuid.UUID]time.Time{}
	}
	if w.inflight[id] || w.now().Before(w.backoff[id]) {
		return false
	}
	w.inflight[id] = true
	return true
}

func (w *Worker) releaseLocal(id uuid.UUID) {
	w.mu.Lock()
	delete(w.inflight, id)
	w.mu.Unlock()
}

// due rate-limits a retried step per request (in-memory; a restart just retries sooner).
func (w *Worker) due(id uuid.UUID, every time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.backoff == nil {
		w.backoff = map[uuid.UUID]time.Time{}
	}
	if w.now().Before(w.backoff[id]) {
		return false
	}
	w.backoff[id] = w.now().Add(every)
	return true
}

func (w *Worker) requeue(ctx context.Context, req db.AcquisitionRequest, why string, err error) {
	w.mu.Lock()
	if w.backoff == nil {
		w.backoff = map[uuid.UUID]time.Time{}
	}
	w.backoff[req.ID] = w.now().Add(30 * time.Second)
	w.mu.Unlock()
	w.Log.Warn("transient error, requeued", "request", req.ID, "step", why, "err", redact.Error(err))
	_ = w.Store.SetState(ctx, db.SetStateParams{State: StateQueued, ID: req.ID})
}

// ── search & grab ────────────────────────────────────────────────────────────

func trackKey(t db.AcquisitionRequestTrack) string { return t.RecordingMbid.String() }

func (w *Worker) wanted(t db.AcquisitionRequestTrack) bool {
	win := w.Cfg.WantedFor
	if win <= 0 {
		win = 30 * time.Minute
	}
	return t.WantedAt != nil && w.now().Sub(*t.WantedAt) < win
}

func (w *Worker) search(ctx context.Context, req db.AcquisitionRequest) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	tracks, err := w.Store.RequestTracks(ctx, req.ID)
	if err != nil {
		w.requeue(ctx, req, "tracks", err)
		return
	}
	idx, err := w.Indexers.Indexers(ctx)
	if err != nil {
		w.requeue(ctx, req, "indexers", err)
		return
	}
	enabled := 0
	for _, i := range idx {
		if i.Enable && (i.Protocol == "" || i.Protocol == "torrent") {
			enabled++
		}
	}
	if enabled == 0 {
		w.fail(ctx, req, CodeNoIndexers, nil)
		return
	}
	want := match.Want{Artist: req.ArtistName, Album: req.AlbumTitle, TrackCount: len(tracks), MaxBytes: w.Cfg.MaxReleaseByte}
	queries := []string{strings.TrimSpace(req.ArtistName + " " + req.AlbumTitle), req.AlbumTitle}
	var (
		rels   []arr.Release
		ranked []match.Scored
	)
	for i, q := range queries {
		if i > 0 && strings.EqualFold(q, queries[0]) {
			break
		}
		rels, err = w.Indexers.Search(ctx, q, 100)
		if err != nil {
			w.requeue(ctx, req, "search", err)
			return
		}
		cand := make([]match.Release, len(rels))
		for j, r := range rels {
			cand[j] = match.Release{Title: r.Title, Size: r.Size, Seeders: r.Seeders}
		}
		if ranked = match.Rank(want, cand); len(ranked) > 0 {
			break
		}
	}
	w.event(ctx, req.ID, "searched", map[string]any{"results": len(rels), "acceptable": len(ranked), "indexers": enabled})
	for i, sc := range ranked {
		if i >= 3 {
			break
		}
		if w.grab(ctx, req, tracks, rels[sc.Index], sc) {
			return
		}
	}
	w.fail(ctx, req, CodeNoSources, map[string]any{"results": len(rels), "acceptable": len(ranked)})
}

func mapTracks(tracks []db.AcquisitionRequestTrack) []match.Track {
	out := make([]match.Track, len(tracks))
	for i, t := range tracks {
		out[i] = match.Track{Key: trackKey(t), Disc: int(t.Disc), Position: int(t.Position), Title: t.Title}
	}
	return out
}

// enough: at least half the tracks mapped, and every wanted track.
func (w *Worker) enough(tracks []db.AcquisitionRequestTrack, m match.Mapping) bool {
	for _, t := range tracks {
		if w.wanted(t) {
			if _, ok := m.ByTrack[trackKey(t)]; !ok {
				return false
			}
		}
	}
	return len(m.ByTrack)*2 >= len(tracks) && len(m.ByTrack) > 0
}

func (w *Worker) grab(ctx context.Context, req db.AcquisitionRequest, tracks []db.AcquisitionRequestTrack, rel arr.Release, sc match.Scored) bool {
	log := w.Log.With("request", req.ID, "release", rel.Title, "indexer", rel.Indexer)
	file, magnet, err := w.Indexers.Download(ctx, rel)
	if err != nil {
		log.Warn("torrent fetch failed", "err", redact.Error(err))
		return false
	}
	var hash string
	if file != nil {
		meta, err := torrent.Parse(file)
		if err != nil {
			log.Warn("bad torrent", "err", err)
			return false
		}
		hash = meta.InfoHash
		files := make([]match.File, len(meta.Files))
		for i, f := range meta.Files {
			files[i] = match.File{Index: i, Name: f.Path, Size: f.Size}
		}
		if m := match.MapFiles(mapTracks(tracks), files, ""); !w.enough(tracks, m) {
			log.Info("release does not match tracklist", "mapped", len(m.ByTrack), "tracks", len(tracks))
			return false
		}
	} else if hash, err = torrent.MagnetHash(magnet); err != nil {
		log.Warn("bad magnet", "err", err)
		return false
	}
	if existing, err := w.Store.RequestByHash(ctx, hash); err == nil && existing.ID != req.ID {
		log.Info("torrent already used by another request", "other", existing.ID)
		return false
	}
	tag := "acq-" + req.ID.String()[:8]
	if err := w.Torrents.AddTorrent(ctx, file, magnet, qbt.AddOptions{Category: w.Cfg.Category, Tags: []string{"tapenest", tag}, Stopped: true}); err != nil {
		log.Warn("add torrent failed", "err", redact.Error(err))
		return false
	}
	qfiles, err := w.waitFiles(ctx, hash)
	if err != nil {
		log.Warn("no torrent metadata", "err", redact.Error(err))
		_ = w.Torrents.Delete(ctx, hash, true)
		return false
	}
	files := make([]match.File, len(qfiles))
	for i, f := range qfiles {
		files[i] = match.File{Index: f.Index, Name: f.Name, Size: f.Size}
	}
	m := match.MapFiles(mapTracks(tracks), files, "")
	if !w.enough(tracks, m) {
		log.Info("release does not match tracklist", "mapped", len(m.ByTrack), "tracks", len(tracks))
		_ = w.Torrents.Delete(ctx, hash, true)
		return false
	}
	if len(m.Skip) > 0 {
		if err := w.Torrents.SetFilePriority(ctx, hash, m.Skip, qbt.PrioSkip); err != nil {
			log.Warn("skip files", "err", redact.Error(err))
		}
	}
	var wantedIdx []int
	byIndex := map[int]qbt.File{}
	for _, f := range qfiles {
		byIndex[f.Index] = f
	}
	var total int64
	for _, t := range tracks {
		fi, ok := m.ByTrack[trackKey(t)]
		if !ok {
			continue
		}
		f := byIndex[fi]
		total += f.Size
		idx := int32(fi) //nolint:gosec // file index fits int32
		if err := w.Store.SetTrackFile(ctx, db.SetTrackFileParams{FileIndex: &idx, FileName: f.Name, FileSize: f.Size, RequestID: req.ID, RecordingMbid: t.RecordingMbid}); err != nil {
			log.Error("set track file", "err", redact.Error(err))
			_ = w.Torrents.Delete(ctx, hash, true)
			return false
		}
		if w.wanted(t) {
			wantedIdx = append(wantedIdx, fi)
		}
	}
	if len(wantedIdx) > 0 {
		_ = w.Torrents.SetFilePriority(ctx, hash, wantedIdx, qbt.PrioMaximal)
		_ = w.Torrents.SetSequential(ctx, hash, true)
	}
	if err := w.Torrents.Start(ctx, hash); err != nil {
		log.Warn("start torrent", "err", redact.Error(err))
	}
	if err := w.Store.SetDownloading(ctx, db.SetDownloadingParams{
		TorrentHash: hash, ReleaseTitle: rel.Title, Indexer: rel.Indexer, Quality: sc.Quality,
		SizeBytes: total, Seeders: int32(rel.Seeders), FileExt: m.Ext, ID: req.ID, //nolint:gosec // seeder count fits int32
	}); err != nil {
		log.Error("set downloading", "err", redact.Error(err))
		return false
	}
	w.event(ctx, req.ID, "grabbed", map[string]any{
		"release": rel.Title, "indexer": rel.Indexer, "seeders": rel.Seeders, "quality": sc.Quality,
		"mapped": len(m.ByTrack), "tracks": len(tracks), "wantedFirst": len(wantedIdx), "bytes": total,
	})
	log.Info("grabbed", "hash", hash, "mapped", len(m.ByTrack), "quality", sc.Quality)
	return true
}

func (w *Worker) waitFiles(ctx context.Context, hash string) ([]qbt.File, error) {
	wait := w.addWait
	if wait == 0 {
		wait = 90 * time.Second
	}
	deadline := w.now().Add(wait)
	for {
		files, err := w.Torrents.Files(ctx, hash)
		if err == nil && len(files) > 0 {
			return files, nil
		}
		if w.now().After(deadline) {
			if err == nil {
				err = errors.New("metadata timeout")
			}
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// ── progress ─────────────────────────────────────────────────────────────────

func (w *Worker) progress(ctx context.Context, req db.AcquisitionRequest) {
	t, err := w.Torrents.Torrent(ctx, req.TorrentHash)
	if errors.Is(err, qbt.ErrNotFound) {
		w.fail(ctx, req, CodeRemoved, nil)
		return
	} else if err != nil {
		return
	}
	files, err := w.Torrents.Files(ctx, req.TorrentHash)
	if err != nil {
		return
	}
	tracks, err := w.Store.RequestTracks(ctx, req.ID)
	if err != nil {
		return
	}
	byIndex := map[int]qbt.File{}
	for _, f := range files {
		byIndex[f.Index] = f
	}
	var total, done float64
	var pending, promote []int
	for _, tr := range tracks {
		if tr.FileIndex == nil {
			continue
		}
		f, ok := byIndex[int(*tr.FileIndex)]
		if !ok {
			continue
		}
		total += float64(f.Size)
		done += f.Progress * float64(f.Size)
		if f.Progress >= 1 {
			continue
		}
		if f.Priority == qbt.PrioSkip { // never leave a mapped file unwanted
			promote = append(promote, f.Index)
		}
		if w.wanted(tr) {
			pending = append(pending, f.Index)
		}
	}
	if len(promote) > 0 {
		_ = w.Torrents.SetFilePriority(ctx, req.TorrentHash, promote, qbt.PrioNormal)
	}
	var raise []int
	for _, i := range pending {
		if byIndex[i].Priority != qbt.PrioMaximal {
			raise = append(raise, i)
		}
	}
	if len(raise) > 0 {
		_ = w.Torrents.SetFilePriority(ctx, req.TorrentHash, raise, qbt.PrioMaximal)
	}
	// sequential only while someone is waiting to listen; rarest-first otherwise
	if (len(pending) > 0) != t.Sequential {
		_ = w.Torrents.SetSequential(ctx, req.TorrentHash, len(pending) > 0)
	}
	p := float32(0)
	if total > 0 {
		p = float32(done / total)
	}
	advanced := p > req.Progress+0.0005
	if err := w.Store.SetProgress(ctx, db.SetProgressParams{Progress: p, Seeders: int32(t.NumSeeds), Advanced: advanced, ID: req.ID}); err != nil { //nolint:gosec // seeder count fits int32
		return
	}
	if total > 0 && done >= total {
		_ = w.Store.SetState(ctx, db.SetStateParams{State: StateImporting, ID: req.ID})
		w.event(ctx, req.ID, "downloaded", map[string]any{"bytes": int64(total)})
		return
	}
	last := req.CreatedAt
	if req.LastProgressAt != nil {
		last = *req.LastProgressAt
	}
	if !advanced && w.Cfg.StallTimeout > 0 && w.now().Sub(last) > w.Cfg.StallTimeout {
		_ = w.Torrents.Delete(ctx, req.TorrentHash, true)
		w.fail(ctx, req, CodeStalled, map[string]any{"progress": p})
	}
}

// ── import ───────────────────────────────────────────────────────────────────

// localFiles maps recording → absolute path of the downloaded file.
func (w *Worker) localFiles(ctx context.Context, req db.AcquisitionRequest, tracks []db.AcquisitionRequestTrack) (map[uuid.UUID]string, string, error) {
	t, err := w.Torrents.Torrent(ctx, req.TorrentHash)
	if err != nil {
		return nil, "", err
	}
	out := map[uuid.UUID]string{}
	for _, tr := range tracks {
		if tr.FileIndex == nil || tr.FileName == "" {
			continue
		}
		for _, base := range []string{t.SavePath, t.DownloadPath} {
			if base == "" {
				continue
			}
			p := filepath.Join(base, filepath.FromSlash(tr.FileName))
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				out[tr.RecordingMbid] = p
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, "", errors.New("downloaded files not found on disk")
	}
	var dirs []string
	for _, p := range out {
		dirs = append(dirs, filepath.Dir(p))
	}
	return out, commonDir(dirs), nil
}

func commonDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	c := dirs[0]
	for _, d := range dirs[1:] {
		for !strings.HasPrefix(d+string(filepath.Separator), c+string(filepath.Separator)) {
			c = filepath.Dir(c)
		}
	}
	return c
}

func (w *Worker) importStep(ctx context.Context, req db.AcquisitionRequest) {
	tracks, err := w.Store.RequestTracks(ctx, req.ID)
	if err != nil {
		return
	}
	mode, arg := splitMode(req.ImportMode)
	switch mode {
	case "":
		if w.Library == nil {
			w.direct(ctx, req, tracks, "lidarr disabled")
			return
		}
		w.startLidarr(ctx, req, tracks)
	case "lidarr-wait": // album added, waiting for Lidarr to fetch its tracklist
		if w.now().Sub(req.UpdatedAt) > w.Cfg.ImportTimeout {
			w.direct(ctx, req, tracks, "lidarr tracklist timeout")
			return
		}
		if w.due(req.ID, 10*time.Second) {
			w.startLidarr(ctx, req, tracks)
		}
	case "lidarr":
		w.pollLidarr(ctx, req, tracks, arg)
	case "done":
		if w.due(req.ID, 20*time.Second) {
			w.finalize(ctx, req, tracks, arg)
		}
	default:
		w.direct(ctx, req, tracks, "unknown import mode")
	}
}

func splitMode(s string) (string, string) {
	mode, arg, _ := strings.Cut(s, ":")
	return mode, arg
}

func (w *Worker) startLidarr(ctx context.Context, req db.AcquisitionRequest, tracks []db.AcquisitionRequestTrack) {
	files, folder, err := w.localFiles(ctx, req, tracks)
	if err != nil {
		w.fail(ctx, req, CodeImport, map[string]any{"error": errText(err)})
		return
	}
	album, err := w.Library.AddAlbum(ctx, req.ReleaseGroupMbid.String(), w.Cfg.LibraryRoot, w.profile())
	if err != nil {
		w.direct(ctx, req, tracks, "lidarr add album: "+errText(err))
		return
	}
	if req.LidarrAlbumID == nil || int(*req.LidarrAlbumID) != album.ID {
		id := int32(album.ID) //nolint:gosec // Lidarr ids fit int32
		_ = w.Store.SetLidarrAlbum(ctx, db.SetLidarrAlbumParams{LidarrAlbumID: &id, ID: req.ID})
	}
	lt, err := w.Library.Tracks(ctx, album.ID)
	if err != nil || len(lt) == 0 {
		if req.ImportMode == "" {
			_ = w.Store.SetImportMode(ctx, db.SetImportModeParams{ImportMode: "lidarr-wait", ID: req.ID})
		}
		return
	}
	releaseID := 0
	for _, r := range album.Releases {
		if r.Monitored || (req.ReleaseMbid != nil && strings.EqualFold(r.ForeignReleaseID, req.ReleaseMbid.String())) {
			releaseID = r.ID
			if !r.Monitored {
				continue
			}
			break
		}
	}
	byRec := map[string]int{}
	byPos := map[[2]int]int{}
	for _, t := range lt {
		byRec[strings.ToLower(t.ForeignRecordingID)] = t.ID
		byPos[[2]int{t.MediumNumber, t.AbsoluteTrack}] = t.ID
	}
	var imp []arr.ImportFile
	for _, tr := range tracks {
		p, ok := files[tr.RecordingMbid]
		if !ok {
			continue
		}
		id, ok := byRec[tr.RecordingMbid.String()]
		if !ok {
			id, ok = byPos[[2]int{int(tr.Disc), int(tr.Position)}]
		}
		if ok {
			imp = append(imp, arr.ImportFile{Path: p, TrackIDs: []int{id}})
		}
	}
	if len(imp) == 0 {
		w.direct(ctx, req, tracks, "no lidarr track matches")
		return
	}
	cmd, err := w.Library.ManualImport(ctx, folder, album, releaseID, imp)
	if err != nil {
		w.direct(ctx, req, tracks, "lidarr manual import: "+errText(err))
		return
	}
	_ = w.Store.SetImportMode(ctx, db.SetImportModeParams{ImportMode: fmt.Sprintf("lidarr:%d", cmd), ID: req.ID})
	w.event(ctx, req.ID, "import_started", map[string]any{"mode": "lidarr", "files": len(imp), "command": cmd})
}

func (w *Worker) pollLidarr(ctx context.Context, req db.AcquisitionRequest, tracks []db.AcquisitionRequestTrack, arg string) {
	cmd, _ := strconv.Atoi(arg)
	st, err := w.Library.CommandStatus(ctx, cmd)
	timedOut := w.now().Sub(req.UpdatedAt) > w.Cfg.ImportTimeout
	if err != nil && !timedOut {
		return
	}
	switch {
	case err == nil && st == "completed":
	case err == nil && (st == "failed" || st == "aborted" || st == "cancelled" || st == "orphaned"):
		w.direct(ctx, req, tracks, "lidarr command "+st)
		return
	case timedOut:
		w.direct(ctx, req, tracks, "lidarr import timeout")
		return
	default:
		return // queued / started
	}
	if req.LidarrAlbumID == nil {
		w.direct(ctx, req, tracks, "lidarr album unknown")
		return
	}
	lt, err1 := w.Library.Tracks(ctx, int(*req.LidarrAlbumID))
	tf, err2 := w.Library.TrackFiles(ctx, int(*req.LidarrAlbumID))
	if err1 != nil || err2 != nil {
		w.direct(ctx, req, tracks, "lidarr track files unavailable")
		return
	}
	pathByFile := map[int]string{}
	for _, f := range tf {
		pathByFile[f.ID] = f.Path
	}
	byPos := map[[2]int]string{}
	byRec := map[string]string{}
	for _, t := range lt {
		if p := pathByFile[t.TrackFileID]; t.TrackFileID > 0 && p != "" {
			byRec[strings.ToLower(t.ForeignRecordingID)] = p
			byPos[[2]int{t.MediumNumber, t.AbsoluteTrack}] = p
		}
	}
	n := 0
	for _, tr := range tracks {
		p, ok := byRec[tr.RecordingMbid.String()]
		if !ok {
			p, ok = byPos[[2]int{int(tr.Disc), int(tr.Position)}]
		}
		if !ok {
			continue
		}
		rel, err := filepath.Rel(w.Cfg.MusicDir, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if err := w.Store.SetTrackLibraryPath(ctx, db.SetTrackLibraryPathParams{LibraryPath: filepath.ToSlash(rel), RequestID: req.ID, RecordingMbid: tr.RecordingMbid}); err == nil {
			n++
		}
	}
	if n == 0 {
		w.direct(ctx, req, tracks, "lidarr imported nothing")
		return
	}
	w.event(ctx, req.ID, "imported", map[string]any{"mode": "lidarr", "files": n})
	_ = w.Store.SetImportMode(ctx, db.SetImportModeParams{ImportMode: "done:lidarr", ID: req.ID})
	w.finalize(ctx, req, tracks, "lidarr")
}

// direct is the fallback import: hardlink (or copy) into the library tree
// "<Artist>/<Album (Year)>/NN - Title.ext" without touching tags.
func (w *Worker) direct(ctx context.Context, req db.AcquisitionRequest, tracks []db.AcquisitionRequestTrack, why string) {
	files, _, err := w.localFiles(ctx, req, tracks)
	if err != nil {
		w.fail(ctx, req, CodeImport, map[string]any{"error": errText(err), "why": why})
		return
	}
	album := SafeName(req.AlbumTitle)
	if req.Year > 0 {
		album = fmt.Sprintf("%s (%d)", album, req.Year)
	}
	dir := filepath.Join(w.Cfg.LibraryRoot, SafeName(req.ArtistName), album)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		w.fail(ctx, req, CodeImport, map[string]any{"error": "mkdir failed", "why": why})
		return
	}
	n := 0
	for _, tr := range tracks {
		src, ok := files[tr.RecordingMbid]
		if !ok {
			continue
		}
		name := fmt.Sprintf("%02d - %s%s", tr.Position, SafeName(tr.Title), strings.ToLower(filepath.Ext(src)))
		if tr.Disc > 1 {
			name = fmt.Sprintf("%d-%s", tr.Disc, name)
		}
		dst := filepath.Join(dir, name)
		if err := linkOrCopy(src, dst); err != nil {
			w.Log.Warn("direct import file", "request", req.ID, "err", redact.Error(err))
			continue
		}
		rel, _ := filepath.Rel(w.Cfg.MusicDir, dst)
		if err := w.Store.SetTrackLibraryPath(ctx, db.SetTrackLibraryPathParams{LibraryPath: filepath.ToSlash(rel), RequestID: req.ID, RecordingMbid: tr.RecordingMbid}); err == nil {
			n++
		}
	}
	if n == 0 {
		w.fail(ctx, req, CodeImport, map[string]any{"why": why})
		return
	}
	w.event(ctx, req.ID, "imported", map[string]any{"mode": "direct", "files": n, "why": why})
	_ = w.Store.SetImportMode(ctx, db.SetImportModeParams{ImportMode: "done:direct", ID: req.ID})
	w.finalize(ctx, req, tracks, "direct")
}

func linkOrCopy(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src) //nolint:gosec // src is a qBittorrent save path joined by us, not a client path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // dst is under the library root
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// SafeName makes a path component safe on every common filesystem.
func SafeName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ". ")
	for len(out) > 120 {
		rs := []rune(out)
		out = string(rs[:len(rs)-1])
	}
	if out == "" {
		return "Unknown"
	}
	return out
}

// finalize tells music-service about the files (retried every tick until it succeeds).
func (w *Worker) finalize(ctx context.Context, req db.AcquisitionRequest, tracks []db.AcquisitionRequestTrack, mode string) {
	if len(tracks) == 0 || tracks[0].LibraryPath == "" {
		fresh, err := w.Store.RequestTracks(ctx, req.ID)
		if err != nil {
			return
		}
		tracks = fresh
	}
	var files []CatalogFile
	for _, t := range tracks {
		if t.LibraryPath != "" {
			files = append(files, CatalogFile{Path: t.LibraryPath, RecordingMBID: t.RecordingMbid, ReleaseGroup: req.ReleaseGroupMbid, SizeBytes: w.librarySize(t)})
		}
	}
	if w.Catalog != nil {
		if err := w.Catalog.Refresh(ctx, files); err != nil {
			w.Log.Warn("catalog refresh failed (will retry)", "request", req.ID, "err", redact.Error(err))
			return
		}
	}
	_ = w.Store.SetState(ctx, db.SetStateParams{State: StateAvailable, ID: req.ID})
	w.event(ctx, req.ID, "available", map[string]any{"mode": mode, "files": len(files)})
	w.Log.Info("request available", "request", req.ID, "mode", mode, "files", len(files))
}

// librarySize is the imported file's size on disk (falls back to the torrent's).
func (w *Worker) librarySize(t db.AcquisitionRequestTrack) int64 {
	if st, err := os.Stat(filepath.Join(w.Cfg.MusicDir, filepath.FromSlash(t.LibraryPath))); err == nil {
		return st.Size()
	}
	return t.FileSize
}

// ── cleanup ──────────────────────────────────────────────────────────────────

// DiskStats is reported by the admin endpoint.
type DiskStats struct {
	TorrentBytes int64 `json:"torrentBytes"`
	LibraryBytes int64 `json:"libraryBytes"`
}

// DirSize sums regular file sizes (hardlinks counted once per inode are not
// deduplicated; torrent + library hardlinks therefore over-report, safely).
func DirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func stoppedUp(state string) bool {
	switch state {
	case "stoppedUP", "pausedUP":
		return true
	}
	return false
}

// Cleanup removes finished/failed/orphaned torrents and enforces the torrent disk quota.
func (w *Worker) Cleanup(ctx context.Context) DiskStats {
	ts, err := w.Torrents.Torrents(ctx, w.Cfg.Category)
	if err != nil {
		w.Log.Warn("cleanup list", "err", redact.Error(err))
		return DiskStats{TorrentBytes: DirSize(w.Cfg.TorrentDir), LibraryBytes: DirSize(w.Cfg.LibraryRoot)}
	}
	var evictable []qbt.Torrent
	used := DirSize(w.Cfg.TorrentDir)
	for _, t := range ts {
		req, err := w.Store.RequestByHash(ctx, t.Hash)
		switch {
		case errors.Is(repo.NotFound(err), repo.ErrNotFound):
			if w.now().Sub(time.Unix(t.AddedOn, 0)) > time.Hour {
				w.remove(ctx, t, nil, "orphan")
				used -= t.Downloaded
			}
		case err != nil:
		case req.State == StateFailed:
			w.remove(ctx, t, &req, "failed")
			used -= t.Downloaded
		case req.State == StateAvailable && stoppedUp(t.State):
			w.remove(ctx, t, &req, "seeding_goal_reached")
			used -= t.Size
		case req.State == StateAvailable:
			evictable = append(evictable, t)
		}
	}
	if w.Cfg.TorrentMaxByte > 0 && used > w.Cfg.TorrentMaxByte {
		sort.Slice(evictable, func(i, j int) bool { return evictable[i].CompletionOn < evictable[j].CompletionOn })
		for _, t := range evictable {
			if used <= w.Cfg.TorrentMaxByte*9/10 {
				break
			}
			req, _ := w.Store.RequestByHash(ctx, t.Hash)
			w.remove(ctx, t, &req, "disk_quota")
			used -= t.Size
		}
	}
	return DiskStats{TorrentBytes: max(used, 0), LibraryBytes: DirSize(w.Cfg.LibraryRoot)}
}

func (w *Worker) remove(ctx context.Context, t qbt.Torrent, req *db.AcquisitionRequest, why string) {
	if err := w.Torrents.Delete(ctx, t.Hash, true); err != nil {
		w.Log.Warn("cleanup delete", "err", redact.Error(err))
		return
	}
	w.Log.Info("torrent removed", "hash", t.Hash, "why", why)
	if req != nil && req.ID != uuid.Nil {
		w.event(ctx, req.ID, "torrent_removed", map[string]any{"why": why, "ratio": t.Ratio, "seedingSeconds": t.SeedingTime})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return redact.Error(err).Error()
}

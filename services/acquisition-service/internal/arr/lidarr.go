package arr

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lidarr client.
type Lidarr struct{ b base }

// NewLidarr builds a client.
func NewLidarr(rawURL, key string) (*Lidarr, error) {
	b, err := newBase(rawURL, key, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return &Lidarr{b: b}, nil
}

// Ping checks the API (and the key).
func (l *Lidarr) Ping(ctx context.Context) error {
	return l.b.do(ctx, http.MethodGet, "/api/v1/system/status", nil, nil, nil)
}

// ── bootstrap (idempotent wiring) ────────────────────────────────────────────

// QBitSettings is how Lidarr reaches qBittorrent.
type QBitSettings struct {
	Host, Username, Password, Category string
	Port                               int
}

// EnsureDownloadClient registers qBittorrent (removeCompletedDownloads: Lidarr
// deletes finished torrents once the seeding goals are met → disk is reclaimed).
func (l *Lidarr) EnsureDownloadClient(ctx context.Context, q QBitSettings) (bool, error) {
	var all []Provider
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/downloadclient", nil, nil, &all); err != nil {
		return false, err
	}
	for _, c := range all {
		if strings.EqualFold(c.Implementation, "QBittorrent") {
			return false, nil
		}
	}
	p, err := schemaFor(ctx, l.b, "/api/v1/downloadclient/schema", "QBittorrent")
	if err != nil {
		return false, err
	}
	p.Name = "qBittorrent"
	p.SetField("host", q.Host)
	p.SetField("port", q.Port)
	p.SetField("useSsl", false)
	p.SetField("username", q.Username)
	p.SetField("password", q.Password)
	p.SetField("musicCategory", q.Category)
	p.SetField("sequentialOrder", false)
	p.SetField("firstAndLast", true)
	p.Extra = map[string]any{"enable": true, "protocol": "torrent", "priority": 1, "removeCompletedDownloads": true, "removeFailedDownloads": true}
	return true, l.b.do(ctx, http.MethodPost, "/api/v1/downloadclient", nil, p.body(), nil)
}

// QualityProfileName is the profile TapeNest creates and uses.
const QualityProfileName = "TapeNest (MP3 320 / FLAC)"

// EnsureQualityProfile creates "MP3 320 / V0 / FLAC only" with cutoff at
// high-quality lossy (no automatic FLAC upgrades: saves disk and bandwidth).
func (l *Lidarr) EnsureQualityProfile(ctx context.Context) (int, error) {
	var all []map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/qualityprofile", nil, nil, &all); err != nil {
		return 0, err
	}
	for _, p := range all {
		if p["name"] == QualityProfileName {
			return intOf(p["id"]), nil
		}
	}
	var tmpl map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/qualityprofile/schema", nil, nil, &tmpl); err != nil {
		return 0, err
	}
	items, _ := tmpl["items"].([]any)
	cutoff := 0
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		allowed := name == "High Quality Lossy" || name == "Lossless"
		m["allowed"] = allowed
		for _, sub := range asSlice(m["items"]) {
			if sm, ok := sub.(map[string]any); ok {
				sm["allowed"] = allowed
			}
		}
		if name == "High Quality Lossy" {
			cutoff = intOf(m["id"])
		}
	}
	tmpl["name"] = QualityProfileName
	tmpl["upgradeAllowed"] = false
	tmpl["cutoff"] = cutoff
	delete(tmpl, "id")
	var created map[string]any
	if err := l.b.do(ctx, http.MethodPost, "/api/v1/qualityprofile", nil, tmpl, &created); err != nil {
		return 0, err
	}
	return intOf(created["id"]), nil
}

// TuneQualitySizes applies sane size limits (Lidarr units: kbit/s of audio):
// rejects fake "320" releases that are too small and bloated/mislabelled ones.
func (l *Lidarr) TuneQualitySizes(ctx context.Context) error {
	var defs []map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/qualitydefinition", nil, nil, &defs); err != nil {
		return err
	}
	limits := map[string][2]float64{"MP3-320": {250, 350}, "MP3-VBR-V0": {180, 350}, "FLAC": {400, 1500}, "FLAC 24bit": {700, 3000}}
	var changed []map[string]any
	for _, d := range defs {
		q, _ := d["quality"].(map[string]any)
		name, _ := q["name"].(string)
		if lim, ok := limits[name]; ok && (floatOf(d["minSize"]) != lim[0] || floatOf(d["maxSize"]) != lim[1]) {
			d["minSize"], d["maxSize"] = lim[0], lim[1]
			d["preferredSize"] = lim[1] - 5
			changed = append(changed, d)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return l.b.do(ctx, http.MethodPut, "/api/v1/qualitydefinition/update", nil, changed, nil)
}

// EnsureMediaManagement prefers hard links instead of copies (the torrent keeps seeding
// from the same inode, zero extra disk) and disables tag writing (writing tags into a
// hard-linked file would corrupt the torrent's pieces).
func (l *Lidarr) EnsureMediaManagement(ctx context.Context) error {
	var mm map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/config/mediamanagement", nil, nil, &mm); err != nil {
		return err
	}
	if mm["copyUsingHardlinks"] != true {
		mm["copyUsingHardlinks"] = true
		if err := l.b.do(ctx, http.MethodPut, "/api/v1/config/mediamanagement", nil, mm, nil); err != nil {
			return err
		}
	}
	var md map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/config/metadataprovider", nil, nil, &md); err != nil {
		return err
	}
	if md["writeAudioTags"] != "no" {
		md["writeAudioTags"] = "no"
		return l.b.do(ctx, http.MethodPut, "/api/v1/config/metadataprovider", nil, md, nil)
	}
	return nil
}

// EnsureRootFolder registers the library folder Navidrome scans.
func (l *Lidarr) EnsureRootFolder(ctx context.Context, path string, profileID int) error {
	var roots []map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/rootfolder", nil, nil, &roots); err != nil {
		return err
	}
	for _, r := range roots {
		if strings.TrimRight(stringOf(r["path"]), "/") == strings.TrimRight(path, "/") {
			return nil
		}
	}
	body := map[string]any{
		"name": "TapeNest library", "path": path, "defaultQualityProfileId": profileID,
		"defaultMetadataProfileId": l.metadataProfile(ctx), "defaultMonitorOption": "none",
		"defaultNewItemMonitorOption": "none", "defaultTags": []int{},
	}
	return l.b.do(ctx, http.MethodPost, "/api/v1/rootfolder", nil, body, nil)
}

func (l *Lidarr) metadataProfile(ctx context.Context) int {
	var ps []map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/metadataprofile", nil, nil, &ps); err != nil || len(ps) == 0 {
		return 1
	}
	for _, p := range ps {
		if p["name"] == "Standard" {
			return intOf(p["id"])
		}
	}
	return intOf(ps[0]["id"])
}

// ── albums ───────────────────────────────────────────────────────────────────

// Album is the subset of Lidarr's album resource we use.
type Album struct {
	ID             int    `json:"id"`
	ForeignAlbumID string `json:"foreignAlbumId"`
	ArtistID       int    `json:"artistId"`
	Monitored      bool   `json:"monitored"`
	Releases       []struct {
		ID               int    `json:"id"`
		ForeignReleaseID string `json:"foreignReleaseId"`
		Monitored        bool   `json:"monitored"`
	} `json:"releases"`
}

// FindAlbum returns the album by MusicBrainz release-group id (ErrNotFound if absent).
func (l *Lidarr) FindAlbum(ctx context.Context, rg string) (Album, error) {
	var out []Album
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/album", url.Values{"foreignAlbumId": {rg}, "includeAllArtistAlbums": {"true"}}, nil, &out); err != nil {
		return Album{}, err
	}
	for _, a := range out {
		if strings.EqualFold(a.ForeignAlbumID, rg) {
			return a, nil
		}
	}
	return Album{}, ErrNotFound
}

// AddAlbum adds one album (and its artist) to Lidarr unmonitored and without a
// search: TapeNest already grabbed it, and an unmonitored album can never make
// Lidarr's RSS sync download a duplicate copy.
func (l *Lidarr) AddAlbum(ctx context.Context, rg, rootFolder string, profileID int) (Album, error) {
	if a, err := l.FindAlbum(ctx, rg); err == nil {
		return a, nil
	}
	var found []map[string]any
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/album/lookup", url.Values{"term": {"lidarr:" + rg}}, nil, &found); err != nil {
		return Album{}, err
	}
	if len(found) == 0 {
		return Album{}, ErrNotFound
	}
	a := found[0]
	a["monitored"] = false
	a["addOptions"] = map[string]any{"searchForNewAlbum": false}
	if artist, ok := a["artist"].(map[string]any); ok {
		artist["qualityProfileId"] = profileID
		artist["metadataProfileId"] = l.metadataProfile(ctx)
		artist["rootFolderPath"] = rootFolder
		artist["monitored"] = false
		artist["monitorNewItems"] = "none"
		artist["addOptions"] = map[string]any{"monitor": "none", "searchForMissingAlbums": false}
	}
	var created Album
	if err := l.b.do(ctx, http.MethodPost, "/api/v1/album", nil, a, &created); err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Status == http.StatusBadRequest &&
			(strings.Contains(strings.ToLower(se.Body), "exist") || strings.Contains(strings.ToLower(se.Body), "already been added")) {
			return l.FindAlbum(ctx, rg)
		}
		if strings.Contains(err.Error(), "bad json") { // created, but the reply was cut: look it up
			return l.FindAlbum(ctx, rg)
		}
		return Album{}, err
	}
	return created, nil
}

// Track is a Lidarr track of an album's monitored release.
type Track struct {
	ID                 int    `json:"id"`
	ForeignRecordingID string `json:"foreignRecordingId"`
	TrackFileID        int    `json:"trackFileId"`
	AbsoluteTrack      int    `json:"absoluteTrackNumber"`
	MediumNumber       int    `json:"mediumNumber"`
	Title              string `json:"title"`
}

// Tracks lists the album's tracks.
func (l *Lidarr) Tracks(ctx context.Context, albumID int) ([]Track, error) {
	var out []Track
	err := l.b.do(ctx, http.MethodGet, "/api/v1/track", url.Values{"albumId": {strconv.Itoa(albumID)}}, nil, &out)
	return out, err
}

// TrackFile is an imported file.
type TrackFile struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

// TrackFiles lists the album's imported files.
func (l *Lidarr) TrackFiles(ctx context.Context, albumID int) ([]TrackFile, error) {
	var out []TrackFile
	err := l.b.do(ctx, http.MethodGet, "/api/v1/trackfile", url.Values{"albumId": {strconv.Itoa(albumID)}}, nil, &out)
	return out, err
}

// ImportFile maps one downloaded file to Lidarr tracks.
type ImportFile struct {
	Path     string
	TrackIDs []int
}

// ManualImport imports files (hardlink/copy, the torrent keeps seeding) using
// TapeNest's own file→track mapping, bypassing Lidarr's fuzzy auto-matching.
// Returns the command id.
func (l *Lidarr) ManualImport(ctx context.Context, folder string, a Album, releaseID int, files []ImportFile) (int, error) {
	var items []map[string]any
	q := url.Values{"folder": {folder}, "filterExistingFiles": {"false"}, "replaceExistingFiles": {"false"}}
	if err := l.b.do(ctx, http.MethodGet, "/api/v1/manualimport", q, nil, &items); err != nil {
		return 0, err
	}
	byPath := map[string]map[string]any{}
	for _, it := range items {
		byPath[stringOf(it["path"])] = it
	}
	payload := make([]map[string]any, 0, len(files))
	for _, f := range files {
		it := byPath[f.Path]
		entry := map[string]any{
			"path": f.Path, "artistId": a.ArtistID, "albumId": a.ID, "albumReleaseId": releaseID,
			"trackIds": f.TrackIDs, "disableReleaseSwitching": true, "indexerFlags": 0,
		}
		if it != nil {
			entry["quality"] = it["quality"]
			entry["releaseGroup"] = it["releaseGroup"]
			entry["downloadId"] = it["downloadId"]
		}
		payload = append(payload, entry)
	}
	var cmd struct {
		ID int `json:"id"`
	}
	body := map[string]any{"name": "ManualImport", "importMode": "copy", "replaceExistingFiles": false, "files": payload}
	if err := l.b.do(ctx, http.MethodPost, "/api/v1/command", nil, body, &cmd); err != nil {
		return 0, err
	}
	return cmd.ID, nil
}

// CommandStatus returns queued|started|completed|failed|aborted.
func (l *Lidarr) CommandStatus(ctx context.Context, id int) (string, error) {
	var c struct {
		Status string `json:"status"`
	}
	err := l.b.do(ctx, http.MethodGet, "/api/v1/command/"+strconv.Itoa(id), nil, nil, &c)
	return c.Status, err
}

// ── helpers ──────────────────────────────────────────────────────────────────

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func floatOf(v any) float64 { f, _ := v.(float64); return f }

func stringOf(v any) string { s, _ := v.(string); return s }

func asSlice(v any) []any { s, _ := v.([]any); return s }

package core

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strconv"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/arr"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// LidarrSetup is the Lidarr bootstrap subset.
type LidarrSetup interface {
	EnsureQualityProfile(ctx context.Context) (int, error)
	TuneQualitySizes(ctx context.Context) error
	EnsureMediaManagement(ctx context.Context) error
	EnsureRootFolder(ctx context.Context, path string, profileID int) error
	EnsureDownloadClient(ctx context.Context, q arr.QBitSettings) (bool, error)
}

// ProwlarrSetup is the Prowlarr bootstrap subset.
type ProwlarrSetup interface {
	EnsureLidarrApp(ctx context.Context, prowlarrURL, lidarrURL, lidarrKey string) (bool, error)
}

// QbtSetup is the qBittorrent bootstrap subset.
type QbtSetup interface {
	SetPreferences(ctx context.Context, prefs map[string]any) error
	EnsureCategory(ctx context.Context, name, savePath string) error
}

// BootstrapConfig carries the settings the wiring needs (secrets never logged).
type BootstrapConfig struct {
	Category        string
	TorrentDir      string
	LibraryRoot     string
	MaxActive       int
	SeedRatio       float64
	SeedMinutes     int
	QbtURL          string
	QbtUser         string
	QbtPassword     string
	LidarrQbtHost   string
	LidarrSelfURL   string
	ProwlarrSelfURL string
	LidarrAPIKey    string
}

// QbtPreferences are the efficiency settings applied to qBittorrent.
func QbtPreferences(c BootstrapConfig) map[string]any {
	return map[string]any{
		"save_path": c.TorrentDir + "/complete", "temp_path_enabled": true, "temp_path": c.TorrentDir + "/incomplete",
		"incomplete_files_ext": false, "preallocate_all": false,
		"max_connec": 500, "max_connec_per_torrent": 150, "max_uploads": 40, "max_uploads_per_torrent": 8,
		"queueing_enabled": true, "max_active_downloads": c.MaxActive + 1, "max_active_torrents": 200, "max_active_uploads": 150,
		"dont_count_slow_torrents": true,
		"max_ratio_enabled":        c.SeedRatio > 0, "max_ratio": c.SeedRatio,
		"max_seeding_time_enabled": c.SeedMinutes > 0, "max_seeding_time": c.SeedMinutes,
		"max_ratio_act": 0, // stop the torrent; the worker then deletes it (library keeps its hardlink)
		"dht":           true, "pex": true, "lsd": true, "upnp": false,
		"auto_tmm_enabled": true, "torrent_content_layout": "Original",
		"announce_to_all_trackers": true, "announce_to_all_tiers": true,
	}
}

// Bootstrap wires qBittorrent, Lidarr and Prowlarr together (idempotent).
// Returns the Lidarr quality profile id (0 when Lidarr is unavailable).
func Bootstrap(ctx context.Context, c BootstrapConfig, q QbtSetup, l LidarrSetup, p ProwlarrSetup, log *slog.Logger) (int, error) {
	var errs []error
	step := func(name string, err error) {
		if err != nil {
			log.Warn("bootstrap step failed", "step", name, "err", redact.Error(err))
			errs = append(errs, errors.New(name))
			return
		}
		log.Info("bootstrap step ok", "step", name)
	}
	if q != nil {
		step("qbittorrent preferences", q.SetPreferences(ctx, QbtPreferences(c)))
		step("qbittorrent category", q.EnsureCategory(ctx, c.Category, c.TorrentDir+"/complete/"+c.Category))
	}
	profile := 0
	if l != nil {
		var err error
		profile, err = l.EnsureQualityProfile(ctx)
		step("lidarr quality profile", err)
		step("lidarr quality sizes", l.TuneQualitySizes(ctx))
		step("lidarr media management", l.EnsureMediaManagement(ctx))
		if profile > 0 {
			step("lidarr root folder", l.EnsureRootFolder(ctx, c.LibraryRoot, profile))
		}
		host, port := c.LidarrQbtHost, 8080
		if u, err := url.Parse(c.QbtURL); err == nil {
			if p, err := strconv.Atoi(u.Port()); err == nil {
				port = p
			}
			if host == "" {
				host = u.Hostname()
			}
		}
		_, err = l.EnsureDownloadClient(ctx, arr.QBitSettings{Host: host, Port: port, Username: c.QbtUser, Password: c.QbtPassword, Category: "lidarr"})
		step("lidarr download client", err)
	}
	if p != nil {
		_, err := p.EnsureLidarrApp(ctx, c.ProwlarrSelfURL, c.LidarrSelfURL, c.LidarrAPIKey)
		step("prowlarr lidarr application", err)
	}
	return profile, errors.Join(errs...)
}

// Command acqctl is a small operator tool for acquisition-service.
//
//	acqctl add-test-indexer [-url http://127.0.0.1:8093]  register the local CC0 test indexer in Prowlarr (dev/e2e only)
//	acqctl indexers                                        list Prowlarr indexers (names only)
//	acqctl bootstrap                                       run the idempotent qBittorrent/Lidarr/Prowlarr wiring once
//	acqctl purge -yes                                      dev/e2e reset: delete TapeNest torrents (+data), imported
//	                                                       library files and all acquisition requests
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/app"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/arr"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/config"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// TestIndexerName is the only indexer TapeNest tooling ever registers.
const TestIndexerName = "TapeNest Legal Test (CC0)"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: acqctl add-test-indexer|indexers|bootstrap")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "acqctl:", redact.Error(err))
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := arr.NewProwlarr(cfg.ProwlarrURL, cfg.ProwlarrAPIKey)
	if err != nil {
		return err
	}
	switch cmd {
	case "add-test-indexer":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		u := fs.String("url", "http://127.0.0.1:8093", "legal indexer base URL as reachable from Prowlarr")
		_ = fs.Parse(args)
		added, err := p.AddTorznab(ctx, TestIndexerName, *u, []int{3000, 3010, 3040})
		if err != nil {
			return err
		}
		fmt.Printf("test indexer %q: added=%v\n", TestIndexerName, added)
	case "indexers":
		idx, err := p.Indexers(ctx)
		if err != nil {
			return err
		}
		for _, i := range idx {
			fmt.Printf("%d\t%s\tenabled=%v\t%s\n", i.ID, i.Name, i.Enable, i.Protocol)
		}
	case "bootstrap":
		l, err := arr.NewLidarr(cfg.LidarrURL, cfg.LidarrAPIKey)
		if err != nil {
			return err
		}
		q, err := qbt.New(cfg.QbtURL, cfg.QbtUser, cfg.QbtPassword)
		if err != nil {
			return err
		}
		log := app.NewLogger(cfg.LogLevel, "acqctl")
		profile, err := core.Bootstrap(ctx, core.BootstrapConfig{
			Category: cfg.QbtCategory, TorrentDir: cfg.TorrentDir, LibraryRoot: cfg.LibraryRoot(), MaxActive: cfg.MaxActive,
			SeedRatio: cfg.SeedRatio, SeedMinutes: cfg.SeedMinutes, QbtURL: cfg.QbtURL, QbtUser: cfg.QbtUser, QbtPassword: cfg.QbtPassword,
			LidarrQbtHost: cfg.LidarrQbtHost, LidarrSelfURL: cfg.LidarrSelfURL, ProwlarrSelfURL: cfg.ProwlarrSelfURL, LidarrAPIKey: cfg.LidarrAPIKey,
		}, q, l, p, log)
		fmt.Printf("quality profile id %d\n", profile)
		return err
	case "purge":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		yes := fs.Bool("yes", false, "really delete")
		_ = fs.Parse(args)
		if !*yes {
			return fmt.Errorf("purge deletes torrents, imported files and requests; pass -yes")
		}
		return purge(ctx, cfg)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}

// purge resets acquisition state (dev/e2e). Only files under the library root
// are removed; music-service turns the vanished tracks back into placeholders
// on its next sync.
func purge(ctx context.Context, cfg *config.Config) error {
	q, err := qbt.New(cfg.QbtURL, cfg.QbtUser, cfg.QbtPassword)
	if err != nil {
		return err
	}
	ts, err := q.Torrents(ctx, cfg.QbtCategory)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if err := q.Delete(ctx, t.Hash, true); err != nil {
			return err
		}
	}
	infra, err := app.Open(ctx, cfg, app.NewLogger(cfg.LogLevel, "acqctl"), false)
	if err != nil {
		return err
	}
	defer infra.Close()
	rows, err := infra.Pool.Query(ctx, "SELECT library_path FROM acquisition.request_tracks WHERE library_path <> ''")
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		paths = append(paths, p)
	}
	rows.Close()
	root := filepath.Clean(cfg.LibraryRoot()) + string(filepath.Separator)
	removed := 0
	for _, p := range paths {
		full := filepath.Clean(filepath.Join(cfg.MusicDir, filepath.FromSlash(p)))
		if !strings.HasPrefix(full, root) {
			continue
		}
		if err := os.Remove(full); err == nil {
			removed++
		}
		for dir := filepath.Dir(full); strings.HasPrefix(dir+string(filepath.Separator), root) && dir+string(filepath.Separator) != root; dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil { // not empty
				break
			}
		}
	}
	if _, err := infra.Pool.Exec(ctx, "TRUNCATE acquisition.requests CASCADE"); err != nil {
		return err
	}
	fmt.Printf("purged: %d torrents, %d library files, all requests\n", len(ts), removed)
	return nil
}

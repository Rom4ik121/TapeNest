// Command admin adds a fictional or operator-owned title. It never downloads a film.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tapenest/tapenest/services/streaming-service/internal/config"
	"github.com/tapenest/tapenest/services/streaming-service/internal/domain"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
)

func main() {
	title := flag.String("title", "", "display title")
	original := flag.String("original", "", "original title")
	kind := flag.String("kind", "movie", "movie or series")
	year := flag.Int("year", 0, "year")
	flag.Parse()
	if *title == "" || (*kind != "movie" && *kind != "series") {
		fmt.Fprintln(os.Stderr, "usage: admin -title NAME [-kind movie|series] [-year N] [-original NAME]")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := repo.Migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var orig *string
	if *original != "" {
		orig = original
	}
	var y *int
	if *year > 0 {
		y = year
	}
	dur := 0.0
	t := domain.Title{
		Summary: domain.Summary{
			ID: uuid.New(), Kind: domain.TitleKind(*kind), Title: *title, OriginalTitle: orig, Year: y, Genres: []string{},
		},
		Description: "Added by admin. Metadata only — no media file is stored.",
		Files:       []domain.File{{ID: uuid.New(), Name: "local.1080p.mkv", Quality: "1080p", DurationSec: &dur}},
	}
	if err := repo.New(pool).AddTitle(ctx, t, uuid.Nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(t.ID.String())
}

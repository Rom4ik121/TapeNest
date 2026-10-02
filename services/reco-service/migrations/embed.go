// Package migrations embeds the reco schema migrations (golang-migrate).
package migrations

import "embed"

// FS holds the *.sql migrations.
//
//go:embed *.sql
var FS embed.FS

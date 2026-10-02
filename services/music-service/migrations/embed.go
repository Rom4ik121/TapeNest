// Package migrations embeds the music schema migrations (golang-migrate).
package migrations

import "embed"

// FS holds *.sql migrations.
//
//go:embed *.sql
var FS embed.FS

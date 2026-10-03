// Package migrations embeds the photo schema (golang-migrate).
package migrations

import "embed"

// FS holds *.sql migrations.
//
//go:embed *.sql
var FS embed.FS

// Package migrations embeds the SQL migrations of acquisition-service.
package migrations

import "embed"

// FS holds *.sql migrations (golang-migrate iofs source).
//
//go:embed *.sql
var FS embed.FS

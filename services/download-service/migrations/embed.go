// Package migrations embeds the golang-migrate SQL files of download-service.
package migrations

import "embed"

// FS holds *.up.sql / *.down.sql files.
//
//go:embed *.sql
var FS embed.FS

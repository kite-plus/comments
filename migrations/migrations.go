// Package migrations embeds the SQL migrations that comments migrate applies.
package migrations

import "embed"

// FS holds every migration, applied in file name order.
//
//go:embed *.sql
var FS embed.FS

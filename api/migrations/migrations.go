// Package migrations embeds the goose SQL migrations so the binary carries its own schema.
package migrations

import "embed"

// FS holds every migration file at its root, ready for goose.NewProvider.
//
//go:embed *.sql
var FS embed.FS

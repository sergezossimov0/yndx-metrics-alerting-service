// Package migrations embeds the golang-migrate SQL files, so the server binary
// can apply them on startup without the migrations directory on disk. The same
// files can be applied manually with the migrate CLI.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

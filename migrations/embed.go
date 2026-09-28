// Package migrations embeds the SQL migration files so the binary is
// self-contained; both `deadliner migrate` and tests use this FS.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

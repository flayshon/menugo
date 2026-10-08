// Package migrations embeds the SQL migration files so they ship inside the
// application binary. See internal/migrate for how they are applied.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

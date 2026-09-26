// Package migrations embeds the SQL migration files.
//
// The files are embedded rather than read from disk so that a single binary is self-contained. The
// test packages run from their own directories, and the deployed migrate binary runs from whatever
// directory Render starts it in, so a path-based runner would only work from the repository root.
//
// This also removes a class of deployment failure: the binary and the SQL it applies can no longer
// come from different versions of the repository.
package migrations

import "embed"

// FS holds every migration file, keyed by filename.
//
// Filenames are the migration identity, and they are also the primary key recorded in
// schema_migrations, so they must be unique and must never be reused for different content.
//
//go:embed *.sql
var FS embed.FS

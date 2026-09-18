// Package ent holds the two generated control-plane clients.
//
// Two clients rather than one, because §99 splits the control plane into a tier that
// holds player identifiers and a tier that must not. Generating them separately makes
// that a compile-time boundary: nothing in the global client can reference a regional
// entity, and each tier migrates into its own PostgreSQL schema.
//
// The schema directories are named *tier so they do not collide with the generated
// target package names, which ent references by directory.
package ent

// Both clients are generated with upsert support, and each has its own reason for needing it —
// which is the test a feature has to pass here rather than "for consistency".
//
// The **global** client needs it for the usage meter. A day is written as it goes and rewritten
// when late events land (§40), so the job that produces it is re-run against days it has already
// written; the unique index on (project, day) is what makes that an update rather than a second row,
// and read-then-write would race two workers against each other.
//go:generate go run entgo.io/ent/cmd/ent generate --feature sql/upsert --target ./global ./schema/globaltier
// The **regional** client needs it because the archive manifest needs insert-or-do-nothing:
// delivery is at-least-once (§21), so a redelivered flush produces the same object path and must
// leave one manifest row rather than fail.
//go:generate go run entgo.io/ent/cmd/ent generate --feature sql/upsert --target ./regional ./schema/regionaltier

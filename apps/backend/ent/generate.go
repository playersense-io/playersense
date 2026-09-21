// Package ent holds the generated PostgreSQL client.
//
// One client over one database. It was two — a global tier that could hold no player
// identifier and a per-cell tier that held nothing else — and the split was a package
// boundary so the compiler could enforce it. There is one database now, so §96's
// classification is enforced per column instead: every field carries a class in its
// comment, and `migrate verify` reads them back off the live database.
package ent

// Generated with upsert support, which two callers need.
//
// The usage meter writes a day as it goes and rewrites it when late events land (§40), so the
// job that produces it is re-run against days it has already written; the unique index on
// (project, day) is what makes that an update rather than a second row, and read-then-write
// would race two workers against each other.
//
// The archive manifest needs insert-or-do-nothing: delivery is at-least-once (§21), so a
// redelivered flush produces the same object path and must leave one manifest row rather
// than fail.
//go:generate go run entgo.io/ent/cmd/ent generate --feature sql/upsert ./schema

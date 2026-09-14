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

//go:generate go run entgo.io/ent/cmd/ent generate --target ./global ./schema/globaltier
// The regional client is generated with upsert support because the archive manifest needs
// insert-or-do-nothing: delivery is at-least-once (§21), so a redelivered flush produces the
// same object path and must leave one manifest row rather than fail. The global client has
// no such need and stays minimal — a feature enabled everywhere "for consistency" is a
// feature nobody can later argue against.
//go:generate go run entgo.io/ent/cmd/ent generate --feature sql/upsert --target ./regional ./schema/regionaltier

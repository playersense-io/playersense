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
//go:generate go run entgo.io/ent/cmd/ent generate --target ./regional ./schema/regionaltier

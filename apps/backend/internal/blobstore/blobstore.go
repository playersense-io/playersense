// Package blobstore is the archive's object storage (project.md §14, §3.6).
//
// An interface for the reason §3.6 gives and eventbus already demonstrates: the archive
// must not couple the writer to one deployment product. §14 is written against Google Cloud
// Storage and that is the default, but a local implementation makes the archive path
// runnable and testable without a cloud account — which matters more than it sounds, since
// an archive nobody can exercise locally is an archive nobody exercises.
//
// The interface is deliberately small. Writing an object, deleting one, and knowing which
// bucket you are in is the whole of what §10.4 and §60 need. Reading is absent because
// nothing reads yet: §34's cold queries are not built, and an unused method is a method
// nobody keeps correct.
package blobstore

import (
	"context"
	"errors"
)

// ErrNotStarted reports use of a store that never connected.
var ErrNotStarted = errors.New("object store is not started")

// Object is what a completed write produced.
type Object struct {
	// Path is the object's key within the bucket, without a leading slash.
	Path string
	// Size is the stored byte count, which §39 bills on.
	Size uint64
}

// Store writes immutable archive objects.
//
// Put must be **idempotent on the path**: writing the same path twice leaves one object.
// That is what makes a redelivered flush safe (§21), and it is why the archive writer
// derives a path from content rather than from a clock.
type Store interface {
	Put(ctx context.Context, path string, body []byte) (Object, error)
	Delete(ctx context.Context, path string) error
	Bucket() string
	Name() string
}

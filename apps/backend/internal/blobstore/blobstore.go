// Package blobstore is the archive's object storage (project.md §14, §3.6).
//
// An interface for the reason §3.6 gives and eventbus already demonstrates: the archive
// must not couple the writer to one deployment product. §14 is written against Google Cloud
// Storage and that is the default, but a local implementation makes the archive path
// runnable and testable without a cloud account — which matters more than it sounds, since
// an archive nobody can exercise locally is an archive nobody exercises.
//
// The interface is deliberately small. Writing an object, reading one back, deleting one,
// and knowing which bucket you are in is the whole of what §10.4, §60 and §61 need. Get
// arrived with the erasure path rather than with §34's cold queries: rewriting an archive
// object without the erased actor's rows means reading the object first, and an archive
// that can only be appended to is an archive no erasure can reach.
package blobstore

import (
	"context"
	"errors"
)

// ErrNotStarted reports use of a store that never connected.
var ErrNotStarted = errors.New("object store is not started")

// ErrNotFound reports an object that is not there.
//
// Named rather than left to each implementation, because the callers treat absence as an
// outcome rather than as a failure: §60's sweeper has already deleted it, and §61's rewrite
// has nothing left to redact. Both need to tell "gone" from "the bucket is unreachable",
// and only a sentinel lets them.
var ErrNotFound = errors.New("object does not exist")

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

	// Get reads an object back whole, returning ErrNotFound when it is not there.
	//
	// Whole rather than streamed, because an archive object is sized for a merge rather
	// than for a scan (ADR 0023) and every caller decodes it as one Parquet file anyway.
	// A reader that streams belongs with §34's cold queries, which will want row groups.
	Get(ctx context.Context, path string) ([]byte, error)

	Delete(ctx context.Context, path string) error

	// Ping reports whether the store is reachable, without writing anything.
	//
	// It exists so a failing write can be attributed. A consumer that cannot store an
	// object has to decide between "these bytes are unstorable" and "the bucket is
	// unreachable", and the two want opposite treatment: one message parked, or the whole
	// backlog kept and retried. Attempt counting cannot tell them apart; asking can.
	Ping(ctx context.Context) error

	Bucket() string
	Name() string
}

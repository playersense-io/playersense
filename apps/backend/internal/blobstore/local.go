package blobstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Local writes archive objects to a directory.
//
// For development and tests. It reproduces the two properties the archive path depends on —
// an object is durable before Put returns, and writing the same path twice leaves one
// object — so the writer's acknowledgement contract means locally what it means in
// production. It reproduces nothing else: no lifecycle rules, no versioning, no
// cross-region replication.
type Local struct {
	root   string
	bucket string
}

// NewLocal returns a store writing under root/bucket.
func NewLocal(root, bucket string) *Local { return &Local{root: root, bucket: bucket} }

// Name identifies the component in start-up and shutdown logs.
func (l *Local) Name() string { return "blobstore-local" }

// Bucket is the bucket objects are written to.
func (l *Local) Bucket() string { return l.bucket }

// Start creates the bucket directory.
func (l *Local) Start(context.Context) error {
	if err := os.MkdirAll(l.dir(), 0o750); err != nil {
		return fmt.Errorf("create archive directory %s: %w", l.dir(), err)
	}
	return nil
}

// Stop releases nothing; a directory has no connection.
func (l *Local) Stop(context.Context) error { return nil }

func (l *Local) dir() string { return filepath.Join(l.root, l.bucket) }

// Put writes the object and fsyncs it.
//
// Written to a temporary file and renamed, so a crash part way through leaves either the
// previous object or none — never a truncated one that a manifest would nonetheless claim
// is durable.
func (l *Local) Put(_ context.Context, path string, body []byte) (Object, error) {
	if err := safePath(path); err != nil {
		return Object{}, err
	}
	full := filepath.Join(l.dir(), filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return Object{}, fmt.Errorf("create archive prefix: %w", err)
	}

	tmp := full + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return Object{}, err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return Object{}, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return Object{}, err
	}
	if err := f.Close(); err != nil {
		return Object{}, err
	}
	if err := os.Rename(tmp, full); err != nil {
		return Object{}, err
	}
	return Object{Path: path, Size: uint64(len(body))}, nil
}

// Delete removes an object, tolerating one that is already gone.
//
// §60 retries a failed deletion, so a delete that has already succeeded must not look like
// a failure on the second attempt.
func (l *Local) Delete(_ context.Context, path string) error {
	if err := safePath(path); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(l.dir(), filepath.FromSlash(path)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// safePath refuses a key that would escape the bucket directory.
//
// Only this implementation can be escaped — an object store treats the key as a name, not a
// path — which is exactly why it is checked here rather than trusted to the caller. The
// archive path is built from tenant identifiers, and "build a path from user data" is the
// sentence that precedes most traversal bugs even when the data is a UUID today.
func safePath(path string) error {
	if path == "" {
		return fmt.Errorf("an object needs a path")
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return fmt.Errorf("refusing an archive path that escapes the bucket: %q", path)
	}
	return nil
}

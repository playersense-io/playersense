package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/storage"
)

// GCS writes archive objects to a Google Cloud Storage bucket (§14).
//
// One bucket per cell, in that cell's region. That is not a convenience: §6 makes the
// archive part of the cell, and an archive in the wrong region is the same residency
// failure as a database in the wrong region, with the additional property that nobody
// notices until an auditor asks.
//
// Not exercised by the local test suite, for the same reason eventbus.PubSub is not: it
// needs a cloud account. The Local implementation carries the properties the writer depends
// on, and this one is the deployment target.
type GCS struct {
	bucket string

	client *storage.Client
}

// NewGCS returns an unconnected store. Construction dials nothing, so the application opens
// it before consuming and closes it after draining.
func NewGCS(bucket string) *GCS { return &GCS{bucket: bucket} }

// Name identifies the component in start-up and shutdown logs.
func (g *GCS) Name() string { return "blobstore-gcs" }

// Bucket is the bucket objects are written to.
func (g *GCS) Bucket() string { return g.bucket }

// Start dials Cloud Storage.
func (g *GCS) Start(ctx context.Context) error {
	if g.bucket == "" {
		return fmt.Errorf("ARCHIVE_BUCKET is required when ARCHIVE_STORE=gcs")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("dial cloud storage: %w", err)
	}
	g.client = client
	return nil
}

// Stop closes the client.
func (g *GCS) Stop(context.Context) error {
	if g.client == nil {
		return nil
	}
	err := g.client.Close()
	g.client = nil
	return err
}

// Ping reads the bucket's own attributes.
//
// A metadata read rather than a probe object: writing one to check reachability leaves
// litter in a bucket whose contents §60 expires and §39 bills for.
func (g *GCS) Ping(ctx context.Context) error {
	if g.client == nil {
		return ErrNotStarted
	}
	if _, err := g.client.Bucket(g.bucket).Attrs(ctx); err != nil {
		return fmt.Errorf("stat bucket %s: %w", g.bucket, err)
	}
	return nil
}

// Put writes the object and waits for it to be durable.
//
// Waiting is the point. The writer acknowledges the queue only after this returns, so a Put
// that returned before the bytes were durable would turn an acknowledgement into a claim
// about data that no longer exists (Appendix C.2).
//
// Writing the same path twice leaves one object, because Cloud Storage object writes replace
// wholesale. That is what makes a redelivered flush safe (§21).
func (g *GCS) Put(ctx context.Context, path string, body []byte) (Object, error) {
	if g.client == nil {
		return Object{}, ErrNotStarted
	}
	if err := safePath(path); err != nil {
		return Object{}, err
	}

	w := g.client.Bucket(g.bucket).Object(path).NewWriter(ctx)
	w.ContentType = contentType(path)
	// Already compressed by the Parquet codec (§10.4), so asking the transport to compress
	// again spends CPU to make the object slightly larger.
	w.ContentEncoding = ""
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return Object{}, fmt.Errorf("write gs://%s/%s: %w", g.bucket, path, err)
	}
	// Close is where the upload is finalised and where an error actually surfaces; a Write
	// that succeeded means only that bytes reached a buffer.
	if err := w.Close(); err != nil {
		return Object{}, fmt.Errorf("finalise gs://%s/%s: %w", g.bucket, path, err)
	}
	return Object{Path: path, Size: uint64(w.Attrs().Size)}, nil
}

// Get reads an object back whole.
func (g *GCS) Get(ctx context.Context, path string) ([]byte, error) {
	if g.client == nil {
		return nil, ErrNotStarted
	}
	if err := safePath(path); err != nil {
		return nil, err
	}
	r, err := g.client.Bucket(g.bucket).Object(path).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, fmt.Errorf("gs://%s/%s: %w", g.bucket, path, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("open gs://%s/%s: %w", g.bucket, path, err)
	}
	defer r.Close()

	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read gs://%s/%s: %w", g.bucket, path, err)
	}
	return body, nil
}

// contentType names what is in the object, from its extension.
//
// The archive writes Parquet and nothing else, so this was a constant until §101's exports
// put a second kind of object in the same bucket. A JSON bundle served as Parquet is a file
// every tool downloads and no tool opens, which is a poor answer to a portability request.
func contentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".parquet"):
		return "application/vnd.apache.parquet"
	case strings.HasSuffix(path, ".ndjson"), strings.HasSuffix(path, ".jsonl"):
		return "application/x-ndjson"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// Delete removes an object, tolerating one that is already gone (§60 retries).
func (g *GCS) Delete(ctx context.Context, path string) error {
	if g.client == nil {
		return ErrNotStarted
	}
	if err := safePath(path); err != nil {
		return err
	}
	err := g.client.Bucket(g.bucket).Object(path).Delete(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	return err
}

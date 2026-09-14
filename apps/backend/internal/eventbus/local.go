package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Local is a durable on-disk bus for development and tests.
//
// It writes one JSON object per line under a dated directory and fsyncs before returning,
// so a 202 means the same thing it means in production: the batch survived the process
// that accepted it. That is the property worth preserving — a local bus that only buffers
// in memory would make every latency number and every crash-safety assumption wrong in
// the one environment where they get tested.
//
// It is not a queue. There is no subscription, no acknowledgement and no redelivery, so
// it stands in for Pub/Sub only as far as the publish side of §19 is concerned.
//
// Segments live under a directory named for the cell, so a writer for one cell tails one
// directory and a developer running two cells against one spool does not get one cell's
// batches delivered to the other's writer.
type Local struct {
	cell string
	dir  string

	mu   sync.Mutex
	file *os.File
	day  string
}

// NewLocal returns a bus writing under dir for one cell.
func NewLocal(cell, dir string) *Local { return &Local{cell: cell, dir: dir} }

// segment is where a day's messages for this cell are spooled.
func (l *Local) segment(day string) string {
	return filepath.Join(l.dir, l.cell, "analytics-events-"+day+".ndjson")
}

// Name identifies the component in start-up and shutdown logs.
func (l *Local) Name() string { return "eventbus-local" }

// Start creates the spool directory.
func (l *Local) Start(context.Context) error {
	dir := filepath.Join(l.dir, l.cell)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create spool %s: %w", dir, err)
	}
	return nil
}

// Stop closes the open segment.
func (l *Local) Stop(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Publish appends the message and fsyncs.
func (l *Local) Publish(_ context.Context, msg *Message) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	day := msg.PublishedAt.UTC().Format("2006-01-02")
	if l.file == nil || l.day != day {
		if l.file != nil {
			_ = l.file.Close()
		}
		f, err := os.OpenFile(l.segment(day), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return fmt.Errorf("open spool segment: %w", err)
		}
		l.file, l.day = f, day
	}

	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append to spool: %w", err)
	}
	// Durable before the gateway answers 202, which is what §19 promises.
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync spool: %w", err)
	}
	return nil
}

// Read returns every message published on a day, for tests and for the writer to replay.
func (l *Local) Read(day time.Time) ([]*Message, error) {
	raw, err := os.ReadFile(l.segment(day.UTC().Format("2006-01-02")))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Message
	dec := json.NewDecoder(newLineReader(raw))
	for dec.More() {
		var m Message
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, nil
}

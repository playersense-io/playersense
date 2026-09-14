package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

	// pollInterval is how often Subscribe looks for new bytes. Zero means the default.
	pollInterval time.Duration

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

// ---- the consume side -------------------------------------------------------

// Subscribe tails the spool and delivers what it finds, committing a byte offset per
// segment.
//
// This is the part of Local that is honestly a development stand-in rather than a queue.
// There is no lease, no redelivery timer and no competing consumers: one process reads the
// files another process is appending to. What it *does* reproduce faithfully is the
// property the writer is built against — the position advances only after the handler
// returns nil, so a crash mid-insert replays rather than skips.
//
// The offset is committed after a successful handler call and fsynced, because an offset
// that outlives a crash by being in the page cache is an offset that can skip a batch.
func (l *Local) Subscribe(ctx context.Context, group string, handle Handler) error {
	if group == "" {
		return fmt.Errorf("a subscription needs a group; two consumers sharing one position " +
			"would each skip what the other took")
	}
	if err := os.MkdirAll(l.offsetDir(group), 0o750); err != nil {
		return fmt.Errorf("create offset directory: %w", err)
	}

	ticker := time.NewTicker(l.poll())
	defer ticker.Stop()

	for {
		// Drain first, so a backlog is worked through without waiting a tick per group.
		for {
			delivered, err := l.deliverOnce(ctx, group, handle)
			if err != nil {
				return err
			}
			if !delivered {
				break
			}
			if ctx.Err() != nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// SetPollInterval overrides how often the spool is checked for new segments. Tests set it
// low; the default is fine for a development stack.
func (l *Local) SetPollInterval(d time.Duration) { l.pollInterval = d }

func (l *Local) poll() time.Duration {
	if l.pollInterval > 0 {
		return l.pollInterval
	}
	return 250 * time.Millisecond
}

// offsetDir is per group, so two consumers of the same spool keep independent positions —
// the property a shared directory silently removed.
func (l *Local) offsetDir(group string) string {
	return filepath.Join(l.dir, l.cell, ".offsets", group)
}

// maxReadPerPoll bounds how much of a segment is read in one pass, so a large backlog is
// worked through in pieces rather than loaded whole.
const maxReadPerPoll = 8 << 20

// deliverOnce finds the oldest segment with unread bytes, delivers what is there, and
// reports whether it delivered anything.
func (l *Local) deliverOnce(ctx context.Context, group string, handle Handler) (bool, error) {
	segments, err := filepath.Glob(filepath.Join(l.dir, l.cell, "analytics-events-*.ndjson"))
	if err != nil {
		return false, err
	}
	// Lexical order is chronological: the names carry an ISO date.
	sort.Strings(segments)

	for _, segment := range segments {
		offset, err := l.readOffset(group, segment)
		if err != nil {
			return false, err
		}
		msgs, next, err := readFrom(segment, offset)
		if err != nil {
			return false, err
		}
		if len(msgs) == 0 {
			continue
		}
		if err := handle(ctx, msgs); err != nil {
			// The position does not move. The same messages are delivered again on the
			// next pass, which is the whole point of committing after rather than before.
			return false, err
		}
		if err := l.writeOffset(group, segment, next); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// readFrom reads whole lines from offset and returns the new offset.
//
// Only up to the last newline is consumed. A publisher appending to this file is not
// atomic at the record level, so a trailing partial line is a message still being written,
// not a corrupt one.
func readFrom(path string, offset int64) ([]*Message, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, offset, nil
		}
		return nil, offset, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if offset >= info.Size() {
		return nil, offset, nil
	}
	length := min(info.Size()-offset, int64(maxReadPerPoll))

	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, offset, err
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil, offset, nil
	}
	buf = buf[:end+1]

	var msgs []*Message
	for line := range bytes.Lines(buf) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, offset, fmt.Errorf("decode %s at %d: %w", path, offset, err)
		}
		msgs = append(msgs, &m)
	}
	return msgs, offset + int64(len(buf)), nil
}

func (l *Local) offsetPath(group, segment string) string {
	return filepath.Join(l.offsetDir(group), filepath.Base(segment)+".offset")
}

func (l *Local) readOffset(group, segment string) (int64, error) {
	raw, err := os.ReadFile(l.offsetPath(group, segment))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("read offset for %s: %w", filepath.Base(segment), err)
	}
	return n, nil
}

// writeOffset commits the position durably.
//
// Written to a temporary file and renamed, so a crash part way through leaves the previous
// offset rather than a truncated one — an unreadable offset would mean replaying a whole
// segment, and a half-written number could mean skipping part of one.
func (l *Local) writeOffset(group, segment string, offset int64) error {
	path := l.offsetPath(group, segment)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strconv.FormatInt(offset, 10)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

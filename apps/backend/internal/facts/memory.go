package facts

import (
	"context"
	"sync"
)

// Memory is a Store for tests, and a Source for the cache's own tests.
//
// It exists so a test about *what a rule does with a whale* needs neither Valkey nor
// ClickHouse: the interesting assertions in this package are about thresholds and absence, and
// neither is about a cache.
type Memory struct {
	mu      sync.Mutex
	records map[string]Record

	// Reads counts Store calls and Lookups counts Source calls, so a test can assert the
	// two properties that actually matter for cost: that a document whose rules name no
	// fact performs zero reads, and that a cache hit performs zero lookups.
	Reads   int
	Lookups int

	// Err makes the source fail, for the tests about failing closed.
	Err error
}

func NewMemory() *Memory { return &Memory{records: map[string]Record{}} }

func (m *Memory) key(projectSeq uint32, environmentSeq uint16, subject, identifier string) string {
	return subject + ":" + identifier + ":" +
		string(rune(projectSeq)) + ":" + string(rune(environmentSeq))
}

func (m *Memory) Get(_ context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string) (Record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Reads++
	record, found := m.records[m.key(projectSeq, environmentSeq, subject, identifier)]
	return record, found
}

func (m *Memory) Put(_ context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string, record Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[m.key(projectSeq, environmentSeq, subject, identifier)] = record
	return nil
}

// Subject returns every record held for one project, environment and subject.
//
// For tests that assert over what a loader wrote without knowing the generated identifiers.
func (m *Memory) Subject(projectSeq uint32, environmentSeq uint16, subject string) []Record {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := subject + ":"
	suffix := ":" + string(rune(projectSeq)) + ":" + string(rune(environmentSeq))

	var found []Record
	for key, record := range m.records {
		if len(key) > len(prefix)+len(suffix) &&
			key[:len(prefix)] == prefix && key[len(key)-len(suffix):] == suffix {
			found = append(found, record)
		}
	}
	return found
}

// Lookup makes Memory usable as a Source, so the cache can be tested without ClickHouse.
//
// Counts its calls, which is how a test asserts the property that matters: a hit must not
// reach the source, and a remembered absence must not reach it either.
func (m *Memory) Lookup(_ context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Lookups++
	if m.Err != nil {
		return Record{}, false, m.Err
	}
	record, found := m.records[m.key(projectSeq, environmentSeq, subject, identifier)]
	return record, found, nil
}

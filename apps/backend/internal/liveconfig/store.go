package liveconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/configversion"
)

// StoreTTL is how long a loaded document is served before it is checked again.
//
// A publish is a deliberate act somebody is watching the result of, so a minute is the longest
// anybody should wait to see it — and it is short enough that a rollback is a rollback rather
// than an incident. Against §42's ~230 requests a second it also means one query a minute
// instead of two hundred and thirty, which is the entire reason the cache exists.
const StoreTTL = time.Minute

// Store serves the current published document for a project's environment.
//
// In process rather than in Valkey, and that is the unusual choice worth the paragraph. A
// document is small, identical for every instance, and changes only on a publish — so the
// thing a shared cache buys, one copy of a large value, is not needed, while the thing it
// costs, a network round trip on the hot path, is paid on every request. Each instance holding
// its own copy for a minute is both faster and one fewer dependency in the path that decides
// what a player sees.
//
// The cost is honest: two instances can serve different versions for up to StoreTTL after a
// publish. Players already see different versions during a rollout — an SDK fetches when it
// launches — so this adds no failure mode the product does not already have.
type Store struct {
	client func() *global.Client
	ttl    time.Duration

	mu      sync.RWMutex
	entries map[int]*entry

	// Loads and Hits are counters a metric or a test reads.
	Loads atomic.Int64
	Hits  atomic.Int64
}

type entry struct {
	document Document
	found    bool
	loadedAt time.Time
}

// NewStore returns a store reading from the global tier.
//
// The client is asked for per load rather than held, because a service builds its dependency
// graph before it dials anything: the pool does not exist until the application starts, and a
// store constructed with the nil one would be a nil dereference on the first player who asked
// for a config — which is exactly how this was found.
func NewStore(client func() *global.Client, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = StoreTTL
	}
	return &Store{client: client, ttl: ttl, entries: map[int]*entry{}}
}

// Name and lifecycle: the store holds no connection of its own, so there is nothing to open.
func (s *Store) Name() string { return "config store" }

// Current returns the newest published document for one environment.
//
// The second value is false when the project has published nothing yet, which is an ordinary
// state — a project exists from the moment it is created and a studio publishes later — and
// not an error. A caller serves an empty document for it rather than a failure, because a game
// that cannot start because nobody has pressed publish is a worse first experience than one
// that starts with its own defaults.
func (s *Store) Current(ctx context.Context, environmentID int) (Document, bool, error) {
	if cached, fresh := s.cached(environmentID); fresh {
		s.Hits.Add(1)
		return cached.document, cached.found, nil
	}

	db := s.client()
	if db == nil {
		return Document{}, false, fmt.Errorf("the control plane is not connected")
	}

	version, err := db.ConfigVersion.Query().
		Where(configversion.EnvironmentID(environmentID)).
		Order(global.Desc(configversion.FieldVersion)).
		First(ctx)

	switch {
	case err == nil:
	case global.IsNotFound(err):
		s.remember(environmentID, entry{found: false, loadedAt: time.Now()})
		s.Loads.Add(1)
		return Document{}, false, nil
	default:
		// A stale document beats no document. The alternative is that a blip in the
		// control plane's database changes what every player in the game sees, which is
		// the one thing this path must not do — §42's whole point is that a config
		// endpoint is in front of a game that is already running.
		if cached, _ := s.cached(environmentID); cached != nil {
			return cached.document, cached.found, nil
		}
		return Document{}, false, fmt.Errorf("read the published config: %w", err)
	}

	var document Document
	if err := json.Unmarshal(mustJSON(version.Document), &document); err != nil {
		return Document{}, false, fmt.Errorf("version %d is not a document: %w", version.Version, err)
	}
	// Trusted over whatever the JSON says: the row is the authority on which version this
	// is, and a document whose embedded number disagreed would make an ETag lie.
	document.Version = version.Version

	s.remember(environmentID, entry{document: document, found: true, loadedAt: time.Now()})
	s.Loads.Add(1)
	return document, true, nil
}

// Invalidate drops one environment's entry, so a publish is visible immediately on the
// instance that handled it rather than after a TTL.
//
// Only that instance: the others catch up within StoreTTL. Broadcasting it would mean a
// message bus in the path that decides what a player sees, to save at most a minute on a
// change a person is already watching a dashboard for.
func (s *Store) Invalidate(environmentID int) {
	s.mu.Lock()
	delete(s.entries, environmentID)
	s.mu.Unlock()
}

func (s *Store) cached(environmentID int) (*entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	found, present := s.entries[environmentID]
	if !present {
		return nil, false
	}
	return found, time.Since(found.loadedAt) < s.ttl
}

func (s *Store) remember(environmentID int, e entry) {
	s.mu.Lock()
	s.entries[environmentID] = &e
	s.mu.Unlock()
}

// mustJSON re-encodes ent's map back to bytes.
//
// ent stores the document as JSON and hands it back as map[string]any, so the typed shape has
// to be recovered. Encoding and decoding once per load — a minute apart, not per request — is
// cheaper than either a second column or a hand-written walk of the map that would have to
// change every time the document does.
func mustJSON(value map[string]any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		// Unreachable: it came out of the database as JSON.
		return []byte("{}")
	}
	return encoded
}

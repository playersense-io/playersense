// Package core holds the configuration, guards, dependencies and managed resources the
// services are composed from. It knows nothing about routers or handlers, which is what
// keeps it testable on its own.
package core

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Key classes (§52).
//
// A key is scoped to exactly one of these. Three enum values rather than a scope set,
// because what a credential may do is the single most important thing about it and an
// enum is harder to get subtly wrong than a list.
const (
	// KindProject ships inside a game binary and is assumed discoverable (§71). It
	// authenticates ingestion and config fetches, and nothing else.
	KindProject = "project"
	// KindReporting reads analytics through the reporting API. It never leaves a
	// studio's own backend or dashboard.
	KindReporting = "reporting"
	// KindServer is the server-to-server credential §47 wants for purchase verification
	// and other operations that must not trust a modified game client.
	KindServer = "server"
)

// Key format (§53).
//
//	ps_<kind>_<env>_<public> : <private>
//	└──────── public ───────┘ └─ secret ─┘
//
// Two parts, and the split is what makes a slow hash affordable. The public half is
// stored in clear and indexed, so verifying a presented key is one indexed lookup
// followed by exactly one Argon2 comparison. Without it, checking a key would mean
// running a deliberately slow hash against every key in the table, and a slow hash you
// have to run N times is a denial-of-service vector rather than a defence.
//
// The public half is also what makes a leaked key recognisable. A secret scanner, a log
// filter or a person reading a pull request can see `ps_project_live_…` for what it is.
const (
	keyPrefix    = "ps"
	keySeparator = ":"

	publicRandomBytes = 6  // ~48 bits: an identifier, not a secret
	secretBytes       = 24 // 192 bits of entropy in the half that matters
)

// Argon2id parameters.
//
// These are deliberately lighter than a password profile. Argon2 exists to make guessing
// a low-entropy secret expensive, and the private half here is 192 random bits — already
// infeasible to guess against any hash. What remains is defence in depth if the database
// leaks, and for that these parameters are ample.
//
// They travel encoded with every digest, so raising them later re-hashes keys on next use
// rather than invalidating them.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024 // 19 MiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var (
	// ErrMalformedKey reports a credential that is not a PlayerSense key at all.
	ErrMalformedKey = errors.New("malformed api key")
	// ErrUnknownKeyKind reports a kind nothing mints.
	ErrUnknownKeyKind = errors.New("unknown api key kind")
)

// ValidKind reports whether a kind is one this build issues.
func ValidKind(kind string) bool {
	switch kind {
	case KindProject, KindReporting, KindServer:
		return true
	}
	return false
}

// GeneratedKey is a freshly minted credential.
//
// Secret is returned to the caller once, at creation, and never stored. What is persisted
// is PublicID and Hash — and the hash is of the private half alone, so knowing the public
// half reveals nothing about it.
type GeneratedKey struct {
	Secret   string // the full ps_..._...:... credential
	PublicID string // the half stored in clear and indexed
	Hash     string // PHC-encoded Argon2id digest of the private half
}

// NewAPIKey mints a key of the given kind for the given environment.
func NewAPIKey(kind, environment string) (*GeneratedKey, error) {
	if !ValidKind(kind) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKeyKind, kind)
	}

	// The environment is part of the public half, so a development key pasted into a
	// production build is visibly wrong before it is ever sent (§76).
	env := "live"
	if environment != "production" {
		env = environment
	}

	publicRandom, err := randomID(publicRandomBytes)
	if err != nil {
		return nil, err
	}
	private, err := randomToken(secretBytes)
	if err != nil {
		return nil, err
	}

	publicID := strings.Join([]string{keyPrefix, kind, env, publicRandom}, "_")
	hash, err := HashSecret(private)
	if err != nil {
		return nil, err
	}

	return &GeneratedKey{
		Secret:   publicID + keySeparator + private,
		PublicID: publicID,
		Hash:     hash,
	}, nil
}

// ParseKey splits a presented credential into its two halves.
//
// Nothing about the input is trusted: a value that is not shaped like a key is refused
// here, before it can reach a database lookup or a hash comparison.
func ParseKey(presented string) (publicID, private string, err error) {
	publicID, private, found := strings.Cut(strings.TrimSpace(presented), keySeparator)
	if !found || publicID == "" || private == "" {
		return "", "", ErrMalformedKey
	}
	parts := strings.Split(publicID, "_")
	if len(parts) != 4 || parts[0] != keyPrefix || !ValidKind(parts[1]) {
		return "", "", ErrMalformedKey
	}
	return publicID, private, nil
}

// KindOf reports the class a public identifier belongs to.
func KindOf(publicID string) string {
	parts := strings.Split(publicID, "_")
	if len(parts) != 4 {
		return ""
	}
	return parts[1]
}

// CacheDigest is the identifier a verified key is cached under.
//
// SHA-256 of the whole credential, so the cache is keyed by something only a holder of
// the key can produce, and the cache itself never holds the key. It is fast on purpose:
// this runs on every request, and the slow hash is what the cache exists to avoid.
//
// The cell prefix is added by the client that stores it, so one cell's verified
// credential is never served from another cell's keyspace.
func CacheDigest(presented string) string {
	sum := sha256.Sum256([]byte(presented))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// HashSecret produces a PHC-encoded Argon2id digest of the private half.
func HashSecret(private string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	digest := argon2.IDKey([]byte(private), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// VerifySecret checks a presented private half against a stored digest.
//
// The parameters come from the stored string rather than from the constants above, so a
// digest written under older settings still verifies. Comparison is constant time: it
// returns only after examining every byte, so the response timing says nothing about how
// much of the secret was right.
func VerifySecret(private, encoded string) bool {
	params, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(private), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NeedsRehash reports whether a stored digest was written under weaker parameters than
// this build uses, so it can be upgraded the next time the key is presented.
func NeedsRehash(encoded string) bool {
	params, _, _, err := decodePHC(encoded)
	if err != nil {
		return true
	}
	return params.memory < argonMemory || params.time < argonTime
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodePHC(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=…,t=…,p=…", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrMalformedKey
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParams{}, nil, nil, ErrMalformedKey
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, ErrMalformedKey
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, ErrMalformedKey
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, ErrMalformedKey
	}
	return p, salt, digest, nil
}

// randomToken produces the private half, where any URL-safe character is fine.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// randomID produces the random part of the public half.
//
// Base32 rather than base64url, and the reason is the separator. The public half is
// `ps_<kind>_<env>_<random>` and is parsed by splitting on underscores — but `_` is in
// the base64url alphabet, so roughly one key in eight would contain an extra one and
// become unparseable. The failure was intermittent and looked like an invalid credential,
// which is the worst way for this to go wrong.
//
// Base32's alphabet is letters and digits only, so no value it produces can contain the
// separator.
func randomID(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random id: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)), nil
}

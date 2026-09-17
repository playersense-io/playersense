//! Everything the core cannot do for itself.
//!
//! The core owns every decision in `docs/sdk/protocol.md` — when a session starts, what may
//! be spooled, which batch goes next, how long to wait after a failure — and performs no I/O
//! at all. Clock, storage, network and randomness arrive through this trait.
//!
//! That split is the whole architecture, and it buys four things:
//!
//! - **Size.** No TLS stack and no async runtime, so the compiled core is a few hundred
//!   kilobytes rather than several megabytes. On a mobile game that is the difference
//!   between a dependency and an argument with the studio's build team.
//! - **Platform correctness for free.** `URLSession`, `OkHttp` and `UnityWebRequest` already
//!   handle proxies, App Transport Security, certificate pinning, captive portals and
//!   background transfer. Reimplementing that here would be worse everywhere.
//! - **Reach.** WebGL has no filesystem and no sockets; consoles have neither an open
//!   toolchain nor a public network API. A core that only computes ports to both.
//! - **Testability.** The conformance suite drives a fake host with a fake clock, an
//!   in-memory store and scripted responses, so "the process is killed between spooling and
//!   the 202" is three lines rather than a device farm.
//!
//! # The contract
//!
//! A host must never panic and never block for long: `send` starts a request and returns,
//! and the completion arrives later through [`crate::Client::on_response`]. Storage may fail
//! — a full disk, a revoked permission — and saying so is correct; the core degrades to
//! memory rather than treating it as fatal.

/// Where a message sits in the host's own log.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Level {
    Debug,
    Info,
    Warn,
    Error,
}

/// Why a store operation could not be completed.
///
/// One variant, deliberately. The core's response to any storage failure is the same —
/// keep the batch in memory, where it is subject to eviction — so distinguishing a full
/// disk from a permission error would be a distinction nothing acts on.
#[derive(Clone, Debug)]
pub struct StoreError(pub String);

/// One HTTP request the host is asked to perform.
///
/// The host adds `Authorization: Bearer {api_key}` and `Content-Type: application/json`,
/// plus `Content-Encoding` when one is set, and POSTs `body` to `url`. It must answer
/// exactly once through [`crate::Client::on_response`], including when the request failed
/// before reaching a server — a request the core never hears about again is a batch that
/// never retries.
#[derive(Clone, Debug)]
pub struct Request {
    /// Correlates the completion with this request.
    pub id: u64,
    pub url: String,
    pub api_key: String,
    /// `Some("gzip")`, or `None` for an uncompressed body.
    pub content_encoding: Option<&'static str>,
    pub body: Vec<u8>,
}

/// What became of a [`Request`].
#[derive(Clone, Debug)]
pub struct Response {
    pub id: u64,
    /// The HTTP status, or **0** for anything that never got one: a timeout, a DNS failure,
    /// a refused connection, a cancelled request. Zero is not an error code, it is the
    /// absence of one, and the core treats it as transient.
    pub status: u16,
    /// `Retry-After`, in milliseconds, when the server sent one.
    pub retry_after_ms: Option<i64>,
    pub body: Vec<u8>,
}

/// The platform, as the core needs it.
pub trait Host {
    /// Milliseconds since the Unix epoch. May jump: a player can change it.
    fn now_ms(&self) -> i64;

    /// Milliseconds from an arbitrary origin that only moves forwards.
    ///
    /// Separate from the wall clock because the two answer different questions. Event
    /// timestamps are anchored to the wall clock once per session and advanced by this, so
    /// a clock change mid-session cannot make events interleave backwards; every interval
    /// the core measures — flush cadence, session expiry, backoff — uses this alone, so a
    /// clock change cannot make an interval elapse instantly or never.
    fn monotonic_ms(&self) -> i64;

    /// Fills `out` with unpredictable bytes, for identifiers.
    ///
    /// From the host rather than from a crate, because the platforms this has to reach do
    /// not agree on where randomness comes from — and on the two that are hardest to reach,
    /// a browser and a console, they agree least.
    fn fill_random(&self, out: &mut [u8]);

    /// Persists a value under a key. Keys are opaque and safe as filenames.
    fn store_put(&self, key: &str, value: &[u8]) -> Result<(), StoreError>;
    fn store_get(&self, key: &str) -> Option<Vec<u8>>;
    fn store_delete(&self, key: &str);

    /// Every key with this prefix. Order does not matter; the core sorts.
    fn store_keys(&self, prefix: &str) -> Vec<String>;

    /// Starts a request. Must not block.
    fn send(&self, request: Request);

    fn log(&self, level: Level, message: &str);
}

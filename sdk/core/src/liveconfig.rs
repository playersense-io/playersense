//! What this player gets: feature flags and LiveOps events (§41, §42, §45).
//!
//! The server decides; this holds the answer and asks again when it goes stale. There is no
//! rule evaluation here and there never will be — ADR 0027: an audience that reaches a
//! modified build is an audience that build can satisfy, so the SDK receives values and
//! windows and never learns who else got them.
//!
//! # Two things that are true at different times
//!
//! **A flag is a value.** It does not depend on the clock, so a document read off disk at
//! launch is as good as one fetched a moment ago, and better than a fallback. §42 asks for
//! exactly that: "local SDK fallback to last-known-good config".
//!
//! **An event is a window**, and a window is only meaningful against a clock somebody
//! trusts. A persisted document therefore restores flags immediately and reports **no active
//! events** until this process has heard from the server. A player who sets their phone
//! forward a week and launches offline gets their flags and no holiday event, which is the
//! safe direction to be wrong in — §45's whole point is that the server's clock decides.
//!
//! # The anchor
//!
//! Every response carries `server_time_ms`, recorded here beside the monotonic reading at
//! that instant. "Now, in the server's terms" is then that pair plus elapsed monotonic time,
//! which is immune to the device clock being wrong, being changed, or jumping when a
//! timezone does. It is the same reasoning the session clock uses.

use std::collections::BTreeMap;

use serde::Deserialize;

use crate::event::Value;
use crate::host::{Host, Level};

/// Where the persisted document lives. One key: there is one document per installation, and
/// a key per environment would keep a copy the SDK can never use again.
pub const STORE_KEY: &str = "liveconfig";

/// One resolved flag, in the four shapes a flag can have (§41).
///
/// `Json` keeps the payload as text rather than parsing it. PlayerSense does not understand
/// what is inside — a studio's shop layout is its own business — and a parsed tree would mean
/// this crate choosing a JSON type for every binding to translate.
#[derive(Clone, Debug, PartialEq)]
pub enum Flag {
    Bool(bool),
    Number(f64),
    Text(String),
    Json(String),
}

/// One LiveOps event, as the player sees it.
#[derive(Clone, Debug, PartialEq)]
pub struct Event {
    pub key: String,
    pub kind: String,
    pub priority: i32,

    /// Milliseconds since the epoch **in the server's clock**, not the device's. Compare them
    /// with [`LiveConfig::server_now_ms`] and nothing else.
    pub starts_at_ms: i64,
    pub ends_at_ms: i64,
    /// When it comes back, or zero when it never does.
    pub next_starts_at_ms: i64,

    /// The studio's own payload, as it was published. Opaque here.
    pub payload: String,
}

impl Event {
    /// Whether an instant falls inside. Start inclusive, end exclusive, so two back-to-back
    /// occurrences never both claim a moment.
    pub fn contains(&self, server_now_ms: i64) -> bool {
        server_now_ms >= self.starts_at_ms && server_now_ms < self.ends_at_ms
    }

    /// How long is left, in milliseconds, or zero once it is over. What a countdown reads.
    pub fn remaining_ms(&self, server_now_ms: i64) -> i64 {
        (self.ends_at_ms - server_now_ms).max(0)
    }
}

/// The wire shape of a config response. Mirrors `protocol.ConfigResponse` on the server.
#[derive(Debug, Default, Deserialize)]
struct Document {
    #[serde(default)]
    version: u32,
    #[serde(default)]
    flags: BTreeMap<String, serde_json::Value>,
    #[serde(default, rename = "liveops_events")]
    events: Vec<WireEvent>,
    #[serde(default)]
    upcoming: Vec<WireEvent>,
    #[serde(default)]
    server_time_ms: i64,
}

#[derive(Debug, Default, Deserialize)]
struct WireEvent {
    key: String,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    priority: i32,
    #[serde(default)]
    starts_at_ms: i64,
    #[serde(default)]
    ends_at_ms: i64,
    #[serde(default)]
    next_starts_at_ms: i64,
    #[serde(default)]
    payload: serde_json::Value,
}

/// The clock the windows are expressed in, pinned to a monotonic reading.
#[derive(Clone, Copy, Debug)]
struct Anchor {
    server_time_ms: i64,
    monotonic_ms: i64,
}

/// What happened to a config request, for the caller to log or count.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Outcome {
    /// A new document.
    Updated,
    /// The server said nothing this player would see has changed.
    Unchanged,
    /// Refused, and asking again will not help until something changes.
    Refused,
    /// Failed, and asking again later might.
    Failed,
}

/// The player's configuration, and the state of asking for it.
#[derive(Debug, Default)]
pub struct LiveConfig {
    version: u32,
    flags: BTreeMap<String, Flag>,
    events: Vec<Event>,
    upcoming: Vec<Event>,

    /// The tag the server gave, sent back so an unchanged answer costs no body.
    etag: Option<String>,

    /// None until a response has arrived **in this process**. A document off disk does not
    /// set it, which is what makes persisted events inert until a fetch.
    anchor: Option<Anchor>,

    in_flight: Option<u64>,
    last_attempt_mono_ms: i64,
    /// Never attempted, so the first call is due immediately rather than one interval late.
    ever_attempted: bool,
    consecutive_failures: u32,
}

impl LiveConfig {
    /// Restores the last document that was fetched, if there is one.
    ///
    /// Flags only, in effect: no anchor is set, so nothing is reported active until the server
    /// is heard from. A corrupt or unreadable value is discarded silently — the cost is one
    /// fetch, and refusing to start over a cache is not a trade worth making.
    pub fn restore<H: Host>(&mut self, host: &H) {
        let Some(raw) = host.store_get(STORE_KEY) else {
            return;
        };
        let Ok(document) = serde_json::from_slice::<Document>(&raw) else {
            host.store_delete(STORE_KEY);
            return;
        };
        self.absorb(document, None);
    }

    /// Whether it is time to ask.
    ///
    /// Backs off after failures for the same reason the batch path does: a game that cannot
    /// reach the server should not spend a player's battery finding that out sixty times a
    /// minute. The interval is the floor, not the ceiling — a foreground brings it forward.
    pub fn due(&self, now_mono_ms: i64, interval_ms: i64) -> bool {
        if self.in_flight.is_some() {
            return false;
        }
        if !self.ever_attempted {
            return true;
        }
        let wait = backoff_ms(self.consecutive_failures, interval_ms);
        now_mono_ms - self.last_attempt_mono_ms >= wait
    }

    /// Records that a request has gone out.
    pub fn begin(&mut self, request_id: u64, now_mono_ms: i64) {
        self.in_flight = Some(request_id);
        self.last_attempt_mono_ms = now_mono_ms;
        self.ever_attempted = true;
    }

    /// Whether a request is outstanding.
    pub fn in_flight(&self) -> bool {
        self.in_flight.is_some()
    }

    /// Whether a response belongs to this, rather than to a batch.
    pub fn owns(&self, request_id: u64) -> bool {
        self.in_flight == Some(request_id)
    }

    /// The tag to send back, so an unchanged answer costs no body.
    pub fn etag(&self) -> Option<&str> {
        self.etag.as_deref()
    }

    /// Applies a response.
    ///
    /// `etag` is whatever the server's `ETag` header said, which the host passes through. A
    /// 304 carries no body and keeps everything except the anchor, which is refreshed — the
    /// point of a 304 is that the answer has not changed, and the clock always has.
    pub fn apply<H: Host>(
        &mut self,
        host: &H,
        status: u16,
        body: &[u8],
        etag: Option<String>,
        now_mono_ms: i64,
    ) -> Outcome {
        self.in_flight = None;

        match status {
            200 => {
                let document: Document = match serde_json::from_slice(body) {
                    Ok(document) => document,
                    Err(error) => {
                        // Kept rather than discarded: whatever is held is still the last
                        // thing the server actually said, and replacing it with nothing
                        // because one response was malformed would turn a server bug into a
                        // game that lost its configuration.
                        host.log(
                            Level::Warn,
                            &format!("the configuration response could not be read: {error}"),
                        );
                        self.consecutive_failures += 1;
                        return Outcome::Failed;
                    }
                };

                // Persisted before it is applied, so a crash between the two leaves the new
                // document on disk rather than a version number that points at nothing.
                if let Ok(encoded) = serde_json::to_vec(&RawDocument(body)) {
                    let _ = host.store_put(STORE_KEY, &encoded);
                }

                self.absorb(document, Some(now_mono_ms));
                if etag.is_some() {
                    self.etag = etag;
                }
                self.consecutive_failures = 0;
                Outcome::Updated
            }

            304 => {
                // Nothing to read, and there is deliberately no body. The anchor still moves:
                // the windows held are still right and the clock they are measured against
                // has advanced, so a 304 is how a long-running game keeps its countdown
                // honest without carrying the document again.
                if let Some(anchor) = self.anchor.as_mut() {
                    anchor.server_time_ms += now_mono_ms - anchor.monotonic_ms;
                    anchor.monotonic_ms = now_mono_ms;
                }
                self.consecutive_failures = 0;
                Outcome::Unchanged
            }

            401 | 403 => {
                host.log(
                    Level::Error,
                    "the configuration endpoint refused this key; flags will use their fallbacks",
                );
                self.consecutive_failures += 1;
                Outcome::Refused
            }

            _ => {
                self.consecutive_failures += 1;
                Outcome::Failed
            }
        }
    }

    fn absorb(&mut self, document: Document, now_mono_ms: Option<i64>) {
        self.version = document.version;

        self.flags = document
            .flags
            .into_iter()
            .filter_map(|(key, value)| flag_from(value).map(|flag| (key, flag)))
            .collect();

        self.events = document.events.into_iter().map(event_from).collect();
        self.upcoming = document.upcoming.into_iter().map(event_from).collect();

        // Only a live response sets the anchor. A document off disk has a server time from
        // whenever it was fetched and no trustworthy way to say how long ago that was.
        if let Some(monotonic_ms) = now_mono_ms {
            self.anchor = Some(Anchor {
                server_time_ms: document.server_time_ms,
                monotonic_ms,
            });
        }
    }

    /// The published version these answers came from. Zero when nothing has been fetched.
    pub fn version(&self) -> u32 {
        self.version
    }

    /// Whether the server has been heard from in this process, which is what makes the
    /// windows usable.
    pub fn anchored(&self) -> bool {
        self.anchor.is_some()
    }

    /// Now, in the server's clock. None until the server has been heard from.
    pub fn server_now_ms(&self, now_mono_ms: i64) -> Option<i64> {
        self.anchor
            .map(|anchor| anchor.server_time_ms + (now_mono_ms - anchor.monotonic_ms))
    }

    /// One flag's value, or None when the project has no such flag.
    ///
    /// None rather than a default, so a binding can tell "the studio has not published this
    /// flag" from "the studio published it as false". The bindings turn it into a fallback at
    /// the call site, where the game's own default lives.
    pub fn flag(&self, key: &str) -> Option<&Flag> {
        self.flags.get(key)
    }

    /// Every flag, for a binding that wants to enumerate them.
    pub fn flags(&self) -> impl Iterator<Item = (&String, &Flag)> {
        self.flags.iter()
    }

    /// The events on right now, highest priority first as the server ordered them.
    ///
    /// Empty until the server has been heard from in this process, whatever is on disk. See
    /// the module comment: a window without a trustworthy clock is a window a player can open
    /// early.
    pub fn active(&self, now_mono_ms: i64) -> Vec<&Event> {
        let Some(server_now) = self.server_now_ms(now_mono_ms) else {
            return Vec::new();
        };
        self.events
            .iter()
            .filter(|event| event.contains(server_now))
            .collect()
    }

    /// Events that have not started yet, soonest first.
    pub fn upcoming(&self) -> &[Event] {
        if self.anchor.is_none() {
            return &[];
        }
        &self.upcoming
    }

    /// One event by key, whether or not it is on. What a game asking "is the sale running"
    /// calls, so that it can also say when it starts.
    pub fn event(&self, key: &str) -> Option<&Event> {
        self.events
            .iter()
            .chain(self.upcoming.iter())
            .find(|event| event.key == key)
    }
}

/// Serialises the exact bytes the server sent, so what is stored is what was received.
///
/// Re-encoding a parsed document would drop anything this version of the SDK does not know
/// about, and an older build writing a newer server's document is exactly when that matters.
struct RawDocument<'a>(&'a [u8]);

impl serde::Serialize for RawDocument<'_> {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        use serde::ser::Error;
        let parsed: serde_json::Value = serde_json::from_slice(self.0).map_err(S::Error::custom)?;
        parsed.serialize(serializer)
    }
}

fn flag_from(value: serde_json::Value) -> Option<Flag> {
    match value {
        serde_json::Value::Bool(v) => Some(Flag::Bool(v)),
        serde_json::Value::Number(v) => v.as_f64().map(Flag::Number),
        serde_json::Value::String(v) => Some(Flag::Text(v)),
        // An object or an array is a `json` flag, kept as text. Null is not a flag value the
        // server can publish — `fallback` is required — so it is dropped rather than invented
        // into one.
        other @ (serde_json::Value::Object(_) | serde_json::Value::Array(_)) => {
            Some(Flag::Json(other.to_string()))
        }
        serde_json::Value::Null => None,
    }
}

fn event_from(wire: WireEvent) -> Event {
    Event {
        key: wire.key,
        kind: wire.kind,
        priority: wire.priority,
        starts_at_ms: wire.starts_at_ms,
        ends_at_ms: wire.ends_at_ms,
        next_starts_at_ms: wire.next_starts_at_ms,
        payload: match wire.payload {
            serde_json::Value::Null => String::new(),
            other => other.to_string(),
        },
    }
}

/// How long to wait after `failures` consecutive failures.
///
/// Doubling from the ordinary interval, capped at an hour. Deliberately gentler than the
/// batch path's: a failed config fetch costs a player nothing — the flags they have keep
/// working — so there is no reason to press.
fn backoff_ms(failures: u32, interval_ms: i64) -> i64 {
    if failures == 0 {
        return interval_ms;
    }
    let shift = failures.min(6);
    let waited = interval_ms.saturating_mul(1i64 << shift);
    waited.min(60 * 60 * 1000)
}

/// Renders a list of events as the document a binding parses.
///
/// Here rather than in the FFI so that the C ABI adds no dependency of its own, and beside
/// the code that parses the wire shape so the two cannot drift.
///
/// `payload` is rendered as a **string** holding the studio's own JSON rather than as a nested
/// object: a binding's model cannot declare a type for something PlayerSense does not
/// understand, and the game parses it with whatever it already uses for its own data.
pub fn render_events<'a>(
    anchored: bool,
    version: u32,
    server_time_ms: i64,
    events: impl Iterator<Item = &'a Event>,
) -> String {
    let rendered: Vec<serde_json::Value> = events
        .map(|event| {
            serde_json::json!({
                "key": event.key,
                "kind": event.kind,
                "priority": event.priority,
                "starts_at_ms": event.starts_at_ms,
                "ends_at_ms": event.ends_at_ms,
                "next_starts_at_ms": event.next_starts_at_ms,
                "payload": event.payload,
            })
        })
        .collect();

    serde_json::json!({
        "anchored": anchored,
        "version": version,
        "server_time_ms": server_time_ms,
        "events": rendered,
    })
    .to_string()
}

/// Renders the studio's own context as the `context` member of a request.
///
/// Flat, and the server prefixes every key with `context.` when it builds the evaluation
/// context — which is why a studio cannot reach a reserved namespace from here even by trying.
pub(crate) fn context_member(context: &BTreeMap<String, Value>) -> serde_json::Value {
    let mut out = serde_json::Map::with_capacity(context.len());
    for (key, value) in context {
        let rendered = match value {
            Value::Str(v) => serde_json::Value::String(v.clone()),
            Value::Int(v) => serde_json::Value::from(*v),
            Value::Float(v) => serde_json::Number::from_f64(*v)
                .map(serde_json::Value::Number)
                .unwrap_or(serde_json::Value::Null),
            Value::Bool(v) => serde_json::Value::Bool(*v),
        };
        out.insert(key.clone(), rendered);
    }
    serde_json::Value::Object(out)
}

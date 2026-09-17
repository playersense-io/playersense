//! One event, from `Track` to the wire.

use std::collections::BTreeMap;

use serde::{Serialize, Serializer};

use crate::config::{Config, limits};
use crate::time::rfc3339_ms;

/// A property value, in the four types the server keeps apart (§22).
///
/// Typed rather than stringly, because a filter on a number should compare numbers. A value
/// of any other type is dropped rather than coerced, which is the server's rule too: nothing
/// silently changes meaning between what the game passed and what a chart reads.
#[derive(Clone, Debug, PartialEq)]
pub enum Value {
    Str(String),
    Int(i64),
    Float(f64),
    Bool(bool),
}

impl Serialize for Value {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        match self {
            Value::Str(v) => s.serialize_str(v),
            Value::Int(v) => s.serialize_i64(*v),
            Value::Float(v) => s.serialize_f64(*v),
            Value::Bool(v) => s.serialize_bool(*v),
        }
    }
}

/// Read back from the persisted player context, where JSON is all the type there is.
///
/// Hand-written rather than `#[derive]`d with `untagged`, because untagged tries each
/// variant in order and would read `1` as a string in a layout where `Str` came first. The
/// mapping here is the one JSON actually has: a number with a fraction is a float, a number
/// without one is an integer.
impl<'de> serde::Deserialize<'de> for Value {
    fn deserialize<D: serde::Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct V;
        impl serde::de::Visitor<'_> for V {
            type Value = Value;

            fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str("a string, number or boolean")
            }
            fn visit_str<E: serde::de::Error>(self, v: &str) -> Result<Value, E> {
                Ok(Value::Str(v.to_owned()))
            }
            fn visit_i64<E: serde::de::Error>(self, v: i64) -> Result<Value, E> {
                Ok(Value::Int(v))
            }
            fn visit_u64<E: serde::de::Error>(self, v: u64) -> Result<Value, E> {
                Ok(Value::Int(v as i64))
            }
            fn visit_f64<E: serde::de::Error>(self, v: f64) -> Result<Value, E> {
                Ok(Value::Float(v))
            }
            fn visit_bool<E: serde::de::Error>(self, v: bool) -> Result<Value, E> {
                Ok(Value::Bool(v))
            }
        }
        d.deserialize_any(V)
    }
}

impl From<&str> for Value {
    fn from(v: &str) -> Self {
        Value::Str(v.to_owned())
    }
}
impl From<String> for Value {
    fn from(v: String) -> Self {
        Value::Str(v)
    }
}
impl From<i64> for Value {
    fn from(v: i64) -> Self {
        Value::Int(v)
    }
}
impl From<f64> for Value {
    fn from(v: f64) -> Self {
        Value::Float(v)
    }
}
impl From<bool> for Value {
    fn from(v: bool) -> Self {
        Value::Bool(v)
    }
}

/// How much it matters that this event survives (§6).
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Priority {
    /// Dropped first when the spool is full, and content to wait for a batching interval.
    Normal,
    /// Never dropped while a normal event remains, and sent without waiting.
    ///
    /// Two kinds qualify and both are about something that cannot be recovered later: a
    /// session start carries the install's cohort assignment, which is fixed by when the
    /// server *received* it (§29.4), and revenue is money.
    Critical,
}

/// An event waiting to be batched.
#[derive(Clone, Debug)]
pub struct Event {
    pub name: String,
    /// Wall-clock milliseconds, anchored once per session and advanced monotonically, so a
    /// clock change mid-session cannot reorder events (§8).
    pub event_time_ms: i64,
    pub session_id: String,
    pub player_id: Option<String>,
    pub sequence: i64,
    pub properties: BTreeMap<String, Value>,
    pub revenue_micros: i64,
    pub currency: Option<String>,
}

impl Event {
    pub fn priority(&self) -> Priority {
        if self.revenue_micros != 0 || self.name == "session_start" {
            Priority::Critical
        } else {
            Priority::Normal
        }
    }
}

/// What became of an event the SDK accepted.
///
/// Deliberately not an error. [`Rejected`] means the *call* was wrong and a developer has
/// something to fix; every value here is the SDK working exactly as it should, and a binding
/// that logged a warning for one would be shouting at a player for declining.
///
/// The three differ in whether the event will ever reach a server, which is the question a
/// diagnostics screen has to answer and could not before this existed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Recorded {
    /// Queued for delivery under a granted consent.
    Queued,
    /// Held in memory, waiting for the player to answer (§5). It is delivered if they grant
    /// and discarded if they decline, and it never touches the disk either way.
    AwaitingConsent,
    /// Discarded. The player has declined, and nothing about this event was stored.
    Declined,
}

/// Why an event was refused before it was ever queued.
///
/// Refused locally rather than sent and rejected, for the reason §12 gives: bytes not sent
/// cost nothing, and an event dropped here can be named in a log while one dropped at the
/// gateway comes back as a line in `reasons`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Rejected {
    /// Outside `[a-zA-Z0-9_.:-]`, empty, or too long. Almost always a space or a capital
    /// letter in a name somebody typed.
    Name,
    /// More than the gateway accepts on one event.
    TooManyProperties,
}

impl std::fmt::Display for Rejected {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Rejected::Name => write!(
                f,
                "event_name is empty, too long, or has a character outside [a-zA-Z0-9_.:-]"
            ),
            Rejected::TooManyProperties => write!(
                f,
                "an event may carry at most {} properties",
                limits::MAX_PROPERTIES_PER_EVENT
            ),
        }
    }
}

/// Checks a name against the gateway's own pattern.
pub fn valid_name(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= limits::MAX_EVENT_NAME_CHARS
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b':' | b'-'))
}

/// Trims a property bag to what the gateway accepts.
///
/// Values are truncated and the event survives; a key that is too long is truncated too.
/// Only an outright excess of properties rejects the event, because dropping some of an
/// event's properties would change what it means rather than shorten it.
pub fn sanitize_properties(
    props: BTreeMap<String, Value>,
) -> Result<BTreeMap<String, Value>, Rejected> {
    if props.len() > limits::MAX_PROPERTIES_PER_EVENT {
        return Err(Rejected::TooManyProperties);
    }
    Ok(props
        .into_iter()
        .map(|(key, value)| {
            let key = truncate_chars(&key, limits::MAX_PROPERTY_KEY_CHARS);
            let value = match value {
                Value::Str(v) => Value::Str(truncate_chars(&v, limits::MAX_STRING_VALUE_CHARS)),
                other => other,
            };
            (key, value)
        })
        .collect())
}

/// Truncates on a character boundary, never inside one.
fn truncate_chars(s: &str, max: usize) -> String {
    if s.chars().count() <= max {
        return s.to_owned();
    }
    s.chars().take(max).collect()
}

// ---- the wire envelope (§17) ------------------------------------------------
//
// These mirror `apps/backend/internal/protocol/envelope.go` field for field. They are
// separate types from `Event` above on purpose: one is what the SDK holds and the other is
// what the contract says, and letting a change to the first quietly become a change to the
// second is how an SDK drifts from the protocol it claims to implement.

#[derive(Serialize)]
pub struct WireBatch<'a> {
    pub schema_version: u32,
    pub batch_id: &'a str,
    pub environment: &'a str,
    pub install_id: &'a str,
    pub sdk: WireSdk<'a>,
    pub app: WireApp<'a>,
    pub device: WireDevice<'a>,
    pub consent: WireConsent<'a>,

    /// The studio's context, as a timeline rather than as a copy on every event.
    ///
    /// Omitted entirely when the game sets no context, so an integration that does not use
    /// it pays nothing for it.
    #[serde(skip_serializing_if = "<[WireContextFrame<'a>]>::is_empty")]
    pub context: Vec<WireContextFrame<'a>>,

    pub events: Vec<WireEvent<'a>>,
}

/// One frame of the context timeline (§context frames).
///
/// `at` is the index into `events` from which the frame applies. Frame 0 always carries a
/// full `values` snapshot; later frames are deltas. The gateway applies `values`, then `set`,
/// then `unset`, and materializes the result onto each event before storage — so ClickHouse
/// receives fully denormalized rows and no query ever has to join to reconstruct them.
#[derive(Serialize)]
pub struct WireContextFrame<'a> {
    pub at: usize,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub values: Option<&'a BTreeMap<String, Value>>,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub set: &'a BTreeMap<String, Value>,
    #[serde(skip_serializing_if = "<[String]>::is_empty")]
    pub unset: &'a [String],
}

#[derive(Serialize)]
pub struct WireSdk<'a> {
    pub name: &'a str,
    pub version: &'a str,
}

#[derive(Serialize)]
pub struct WireApp<'a> {
    pub version: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub build: &'a str,
}

#[derive(Serialize)]
pub struct WireDevice<'a> {
    pub platform: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub os_version: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub locale: &'a str,
}

#[derive(Serialize)]
pub struct WireConsent<'a> {
    pub analytics: bool,
    pub policy_version: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub att_status: &'a str,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub gdpr_applies: bool,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub collected_at: &'a str,
}

/// One event on the wire.
///
/// There is no `event_id`. The gateway derives it from the batch and this event's position,
/// and sending one would cost sixteen incompressible bytes for something already known
/// (ADR 0026).
#[derive(Serialize)]
pub struct WireEvent<'a> {
    pub event_name: &'a str,
    pub event_time: String,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub session_id: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub player_id: &'a str,
    #[serde(skip_serializing_if = "is_zero")]
    pub sequence: i64,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub properties: &'a BTreeMap<String, Value>,
    #[serde(skip_serializing_if = "is_zero")]
    pub revenue_micros: i64,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub currency: &'a str,
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

/// Builds the envelope for a batch of events.
pub fn envelope<'a>(
    config: &'a Config,
    install_id: &'a str,
    batch_id: &'a str,
    consent: &'a crate::consent::Granted,
    context: &'a [crate::context::Frame],
    events: &'a [Event],
) -> WireBatch<'a> {
    WireBatch {
        schema_version: crate::config::SCHEMA_VERSION,
        batch_id,
        environment: &config.environment,
        install_id,
        sdk: WireSdk {
            name: &config.sdk_name,
            version: &config.sdk_version,
        },
        app: WireApp {
            version: &config.app_version,
            build: &config.app_build,
        },
        device: WireDevice {
            platform: &config.platform,
            os_version: &config.os_version,
            locale: &config.locale,
        },
        consent: WireConsent {
            analytics: true,
            policy_version: &consent.policy_version,
            att_status: &consent.att_status,
            gdpr_applies: consent.gdpr_applies,
            collected_at: &consent.collected_at,
        },
        context: context
            .iter()
            .map(|frame| WireContextFrame {
                at: frame.at,
                values: frame.values.as_ref(),
                set: &frame.set,
                unset: &frame.unset,
            })
            .collect(),
        events: events
            .iter()
            .map(|e| WireEvent {
                event_name: &e.name,
                event_time: rfc3339_ms(e.event_time_ms),
                session_id: &e.session_id,
                player_id: e.player_id.as_deref().unwrap_or(""),
                sequence: e.sequence,
                properties: &e.properties,
                revenue_micros: e.revenue_micros,
                currency: e.currency.as_deref().unwrap_or(""),
            })
            .collect(),
    }
}

//! Logs, exceptions and crashes: the second stream (`docs/sdk/protocol.md` §15, ADR 0050).
//!
//! Everything the event stream does, done again beside it rather than through it, because the
//! two must never hold each other up: a game logging an error every frame must not delay a
//! purchase event, and a spool full of analytics must not evict the crash that explains why
//! the numbers dropped. So logs have their own queue, their own spool prefix and bounds, their
//! own request slot and their own backoff, and share only the host.
//!
//! # Cheap, bounded, and quiet
//!
//! A log call happens inside the game's frame, often in a loop, sometimes in a loop that is
//! the bug. So:
//!
//! - Below the capture level (warnings, by default) a line is not a record at all: it goes
//!   into the breadcrumb ring, in memory, and costs nothing unless an error follows.
//! - The same line again within a few seconds folds into the previous record as a count
//!   (`ps.repeat`), so an error thrown every frame is one record a second, not sixty.
//! - A per-minute budget caps what one device can send, whatever the game does. A crash is
//!   never refused by it.
//! - Records are sealed into gzip-compressed batches, written to the spool once per batch.
//!
//! # Consent is its own
//!
//! A player may share error reports without sharing analytics, or the reverse: games ask
//! "send crash reports?" in their settings the way desktop software does. [`Diagnostics`]
//! follows the analytics answer until the game gives one of its own. Before any answer, records
//! wait in memory and are sent if the player agrees, as events do; a crash, which would die
//! with the process, is kept on the device (never sent) until the answer comes, and deleted if
//! it is no. A game can ask how many are waiting and offer to send them.

use std::collections::{BTreeMap, VecDeque};
use std::hash::{Hash, Hasher};

use serde::{Deserialize, Serialize};

use crate::config::Config;
use crate::consent::{Granted, State};
use crate::event::Priority;
use crate::host::{Host, Level};
use crate::ids::uuid_v4;
use crate::scrub::scrub;
use crate::spool::{self, Bounds};
use crate::time::rfc3339_ms;

/// Spooled log batches.
pub const PREFIX: &str = "ps.l.";
/// Crashes kept on the device while the player has not yet answered.
pub const HELD_PREFIX: &str = "ps.lh.";
/// Present while the game is in the foreground; found at launch, it means the last run ended
/// without the game ever going to the background or quitting.
pub const KEY_RUNNING: &str = "ps.run";
const KEY_ORDINAL: &str = "ps.l.ordinal";

/// How long an identical line folds into the one before it.
const FOLD_WINDOW_MS: i64 = 10_000;
/// How often the running marker is refreshed, so an abnormal exit is dated to within this.
const HEARTBEAT_MS: i64 = 60_000;

// Bounds on one record, the gateway's own (internal/ingest/logs.go): past them text is cut,
// here rather than there, so the bytes are never sent.
const MAX_MESSAGE_BYTES: usize = 8 * 1024;
const MAX_VALUE_BYTES: usize = 4 * 1024;
const MAX_FRAMES: usize = 128;
const MAX_ATTRIBUTES: usize = 32;
const MAX_ATTRIBUTE_KEY: usize = 64;
const MAX_ATTRIBUTE_VALUE: usize = 1024;
const MAX_BREADCRUMB_MESSAGE: usize = 256;

/// How serious a record is. Ordered: a threshold keeps everything at or above it.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum LogLevel {
    Trace,
    Debug,
    Info,
    Warn,
    Error,
    Fatal,
}

impl LogLevel {
    pub fn from_i32(n: i32) -> Option<Self> {
        Some(match n {
            0 => Self::Trace,
            1 => Self::Debug,
            2 => Self::Info,
            3 => Self::Warn,
            4 => Self::Error,
            5 => Self::Fatal,
            _ => return None,
        })
    }
}

/// One call on the stack.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct StackFrame {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub function: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub module: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub line: u32,
    #[serde(default, skip_serializing_if = "is_false")]
    pub in_app: bool,
}

/// What was thrown.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct LogException {
    #[serde(rename = "type")]
    pub type_name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub value: String,
    #[serde(default, skip_serializing_if = "is_false")]
    pub handled: bool,
    /// Outermost first, innermost (where it was thrown) last.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub frames: Vec<StackFrame>,
}

/// A record as a binding hands it over.
#[derive(Clone, Debug, Default)]
pub struct LogRecord {
    pub level: Option<LogLevel>,
    pub message: String,
    pub logger: String,
    pub attributes: BTreeMap<String, String>,
    pub exception: Option<LogException>,
    /// The process died of this.
    pub crash: bool,
    pub fingerprint: Vec<String>,
    /// When it happened, for a crash read back from a previous run. `None` is now.
    pub time_ms: Option<i64>,
}

/// One thing that happened before an error.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Breadcrumb {
    #[serde(rename = "at")]
    pub at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub category: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub message: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub level: String,
}

/// What became of a record.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Logged {
    /// Queued to be sent.
    Queued,
    /// Folded into the identical record before it, as a count.
    Folded,
    /// Below the capture level: kept as a breadcrumb for the next error.
    Breadcrumb,
    /// Waiting for the player's answer, in memory (or on the device, for a crash).
    AwaitingConsent,
    /// The player said no.
    Declined,
    /// Over this device's per-minute budget.
    RateLimited,
    /// Log capture is off.
    Disabled,
}

/// The player's answer about error reports.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Diagnostics {
    /// Whatever they said about analytics. The default: most games ask once.
    FollowAnalytics,
    Granted(Granted),
    Denied,
}

/// What the log stream has done.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct LogStats {
    pub recorded: u64,
    pub folded: u64,
    pub breadcrumbs: u64,
    pub rate_limited: u64,
    pub without_consent: u64,
    pub evicted: u64,
    pub batches_delivered: u64,
    pub accepted_by_server: u64,
    pub dropped_by_server: u64,
}

/// A record on its way: the binding's record, stamped.
#[derive(Clone, Debug, Serialize, Deserialize)]
struct Pending {
    time_ms: i64,
    level: LogLevel,
    #[serde(default)]
    message: String,
    #[serde(default)]
    logger: String,
    #[serde(default)]
    session_id: String,
    #[serde(default)]
    player_id: String,
    #[serde(default)]
    attributes: BTreeMap<String, String>,
    #[serde(default)]
    exception: Option<LogException>,
    #[serde(default)]
    crash: bool,
    #[serde(default)]
    breadcrumbs: Vec<Breadcrumb>,
    #[serde(default)]
    fingerprint: Vec<String>,
    #[serde(default)]
    repeat: u32,
    #[serde(skip)]
    fold_key: u64,
    #[serde(skip)]
    mono_ms: i64,
}

impl Pending {
    fn urgent(&self) -> bool {
        self.crash || self.level == LogLevel::Fatal
    }

    fn bytes(&self) -> usize {
        let frames: usize = self.exception.as_ref().map_or(0, |e| {
            e.type_name.len()
                + e.value.len()
                + e.frames
                    .iter()
                    .map(|f| f.function.len() + f.module.len() + f.file.len() + 16)
                    .sum::<usize>()
        });
        let crumbs: usize = self
            .breadcrumbs
            .iter()
            .map(|b| b.message.len() + b.category.len() + 40)
            .sum();
        let attrs: usize = self
            .attributes
            .iter()
            .map(|(k, v)| k.len() + v.len() + 6)
            .sum();
        96 + self.message.len()
            + self.logger.len()
            + self.session_id.len()
            + self.player_id.len()
            + frames
            + crumbs
            + attrs
    }
}

struct InFlight {
    request_id: u64,
    key: String,
}

/// The log stream's state. Owned by [`crate::Client`], which calls into it.
pub struct Logs {
    install_id: String,
    diagnostics: Diagnostics,
    pending: Vec<Pending>,
    pending_bytes: usize,
    preconsent: Vec<Pending>,
    breadcrumbs: VecDeque<Breadcrumb>,

    tokens: f64,
    tokens_mono_ms: i64,
    warned_rate_mono_ms: i64,

    ordinal: u64,
    last_seal_mono_ms: i64,
    heartbeat_mono_ms: i64,
    foreground: bool,

    in_flight: Option<InFlight>,
    failures: u32,
    next_attempt_mono_ms: i64,
    cold: bool,
    stopped: bool,
    /// Set when a batch was sealed that should go now (a crash, consent just granted); the
    /// client sends it before returning, as it does for a critical event.
    wants_pump: bool,

    pub stats: LogStats,
}

/// The stamp a record takes from the client: who and in which session.
pub struct Stamp<'a> {
    pub session_id: Option<&'a str>,
    pub player_id: Option<&'a str>,
}

impl Logs {
    pub fn new<H: Host>(host: &H, config: &Config, install_id: &str) -> Self {
        let now = host.monotonic_ms();
        let ordinal = host
            .store_get(KEY_ORDINAL)
            .and_then(|b| String::from_utf8_lossy(&b).parse().ok())
            .unwrap_or(0);
        Self {
            install_id: install_id.to_owned(),
            diagnostics: Diagnostics::FollowAnalytics,
            pending: Vec::new(),
            pending_bytes: 0,
            preconsent: Vec::new(),
            breadcrumbs: VecDeque::with_capacity(config.log_max_breadcrumbs.min(256)),
            tokens: config.log_max_per_minute as f64,
            tokens_mono_ms: now,
            warned_rate_mono_ms: i64::MIN / 2,
            ordinal,
            last_seal_mono_ms: now,
            heartbeat_mono_ms: now,
            foreground: false,
            in_flight: None,
            failures: 0,
            next_attempt_mono_ms: now,
            cold: false,
            stopped: false,
            wants_pump: false,
            stats: LogStats::default(),
        }
    }

    pub fn diagnostics(&self) -> &Diagnostics {
        &self.diagnostics
    }

    /// The consent the log stream is under: the game's own answer, or analytics'.
    pub fn effective(&self, analytics: &State) -> State {
        match &self.diagnostics {
            Diagnostics::FollowAnalytics => analytics.clone(),
            Diagnostics::Granted(g) => State::Granted(g.clone()),
            Diagnostics::Denied => State::Denied,
        }
    }

    // ---- recording ----------------------------------------------------------

    /// Records a log line, an exception or a crash.
    pub fn record<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        analytics: &State,
        stamp: Stamp<'_>,
        record: LogRecord,
    ) -> Logged {
        if !config.logs_enabled {
            return Logged::Disabled;
        }
        let level = record.level.unwrap_or(if record.crash {
            LogLevel::Fatal
        } else {
            LogLevel::Info
        });
        let consent = self.effective(analytics);
        if matches!(consent, State::Denied) {
            self.stats.without_consent += 1;
            return Logged::Declined;
        }

        let now_ms = host.now_ms();
        let mono = host.monotonic_ms();
        let heavy = record.crash || record.exception.is_some();

        // Below the capture level, and not an exception: a breadcrumb for the next error.
        if level < config.log_min_level && !heavy {
            if level >= config.log_breadcrumb_level {
                self.breadcrumb(config, now_ms, "log", &record.message, level);
            }
            return Logged::Breadcrumb;
        }

        let fold_key = fold_key(level, &record);
        // The same line again: a count on the record already queued, not another record.
        if !record.crash
            && let Some(last) = self.pending.last_mut()
            && last.fold_key == fold_key
            && mono - last.mono_ms <= FOLD_WINDOW_MS
        {
            last.repeat += 1;
            last.mono_ms = mono;
            self.stats.folded += 1;
            return Logged::Folded;
        }

        // The device's budget. A crash always goes: it is the record the budget exists to
        // make room for.
        if !record.crash && !self.take_token(config, mono) {
            self.stats.rate_limited += 1;
            if mono - self.warned_rate_mono_ms > 60_000 {
                self.warned_rate_mono_ms = mono;
                host.log(
                    Level::Warn,
                    &format!(
                        "more than {} log records a minute; the rest this minute are dropped. \
                         Raise the capture level or the budget if this is expected.",
                        config.log_max_per_minute
                    ),
                );
            }
            return Logged::RateLimited;
        }

        let pending = self.stamp(config, now_ms, mono, level, fold_key, stamp, record, heavy);
        self.stats.recorded += 1;

        // A line worth keeping is also something that happened, for whatever breaks next.
        self.breadcrumb(config, now_ms, "log", &pending.message_for_crumb(), level);

        match consent {
            State::Granted(_) => {
                let urgent = pending.urgent();
                self.enqueue(pending);
                if urgent
                    || self.pending.len() >= config.log_max_records_per_batch
                    || self.pending_bytes >= config.log_max_batch_bytes
                {
                    self.seal(host, config, analytics);
                }
                Logged::Queued
            }
            _ => {
                if pending.crash && config.log_hold_crashes {
                    // It would die with the process: kept on the device, never sent, until the
                    // player answers.
                    self.hold(host, &pending);
                } else {
                    if self.preconsent.len() >= config.log_preconsent_max {
                        self.preconsent.remove(0);
                    }
                    self.preconsent.push(pending);
                }
                Logged::AwaitingConsent
            }
        }
    }

    /// Something that happened, kept for the next error. Memory only.
    pub fn breadcrumb(
        &mut self,
        config: &Config,
        now_ms: i64,
        category: &str,
        message: &str,
        level: LogLevel,
    ) {
        if config.log_max_breadcrumbs == 0 {
            return;
        }
        while self.breadcrumbs.len() >= config.log_max_breadcrumbs {
            self.breadcrumbs.pop_front();
        }
        let message = clip(message, MAX_BREADCRUMB_MESSAGE);
        self.stats.breadcrumbs += 1;
        self.breadcrumbs.push_back(Breadcrumb {
            at: rfc3339_ms(now_ms),
            category: clip(category, 32),
            message: if config.log_scrub {
                scrub(&message)
            } else {
                message
            },
            level: level_name(level).to_owned(),
        });
    }

    #[allow(clippy::too_many_arguments)]
    fn stamp(
        &self,
        config: &Config,
        now_ms: i64,
        mono: i64,
        level: LogLevel,
        fold_key: u64,
        stamp: Stamp<'_>,
        record: LogRecord,
        heavy: bool,
    ) -> Pending {
        let clean = |text: &str, max: usize| {
            let cut = clip(text, max);
            if config.log_scrub { scrub(&cut) } else { cut }
        };
        let mut attributes = BTreeMap::new();
        // A child-directed game sends nothing that could identify the child: no player id,
        // and none of the game's free-form attributes (Apple 1.3/5.1.4, COPPA).
        let record_attributes = if config.child_directed {
            BTreeMap::new()
        } else {
            record.attributes
        };
        for (key, value) in record_attributes.into_iter().take(MAX_ATTRIBUTES) {
            if key.is_empty()
                || key.len() > MAX_ATTRIBUTE_KEY
                || !key
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b':' | b'-'))
            {
                continue;
            }
            attributes.insert(key, clean(&value, MAX_ATTRIBUTE_VALUE));
        }
        let exception = record.exception.map(|mut e| {
            if e.frames.len() > MAX_FRAMES {
                e.frames.drain(..e.frames.len() - MAX_FRAMES);
            }
            for frame in &mut e.frames {
                if config.log_scrub {
                    frame.file = scrub(&frame.file);
                }
            }
            LogException {
                type_name: clip(&e.type_name, 256),
                value: clean(&e.value, MAX_VALUE_BYTES),
                handled: e.handled && !record.crash,
                frames: e.frames,
            }
        });
        Pending {
            time_ms: record.time_ms.unwrap_or(now_ms),
            level: if record.crash { LogLevel::Fatal } else { level },
            message: clean(&record.message, MAX_MESSAGE_BYTES),
            logger: clip(&record.logger, 128),
            session_id: stamp.session_id.unwrap_or_default().to_owned(),
            player_id: if config.child_directed {
                String::new()
            } else {
                stamp.player_id.unwrap_or_default().to_owned()
            },
            attributes,
            exception,
            crash: record.crash,
            // Only an error or worse carries what came before it; a warning does not need it,
            // and it is most of a record's bytes.
            breadcrumbs: if heavy || level >= LogLevel::Error {
                self.breadcrumbs.iter().cloned().collect()
            } else {
                Vec::new()
            },
            fingerprint: record
                .fingerprint
                .into_iter()
                .take(8)
                .map(|p| clip(&p, 256))
                .collect(),
            repeat: 0,
            fold_key,
            mono_ms: mono,
        }
    }

    fn take_token(&mut self, config: &Config, mono: i64) -> bool {
        let capacity = config.log_max_per_minute as f64;
        let elapsed = (mono - self.tokens_mono_ms).max(0) as f64;
        self.tokens = (self.tokens + elapsed * capacity / 60_000.0).min(capacity);
        self.tokens_mono_ms = mono;
        if self.tokens >= 1.0 {
            self.tokens -= 1.0;
            true
        } else {
            false
        }
    }

    fn enqueue(&mut self, pending: Pending) {
        self.pending_bytes += pending.bytes();
        self.pending.push(pending);
    }

    // ---- consent ------------------------------------------------------------

    /// The game's own answer about error reports.
    pub fn set_diagnostics<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        analytics: &State,
        next: Diagnostics,
    ) {
        let before = self.effective(analytics);
        self.diagnostics = next;
        let after = self.effective(analytics);
        self.consent_moved(host, config, &before, &after, analytics);
    }

    /// The analytics answer moved; it matters here only while diagnostics follows it.
    pub fn analytics_moved<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        before: &State,
        after: &State,
    ) {
        if self.diagnostics == Diagnostics::FollowAnalytics {
            self.consent_moved(host, config, before, after, after);
        }
    }

    fn consent_moved<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        before: &State,
        after: &State,
        analytics_now: &State,
    ) {
        match (before, after) {
            (State::Unknown, State::Granted(_)) => {
                for pending in std::mem::take(&mut self.preconsent) {
                    self.enqueue(pending);
                }
                let held = self.take_held(host);
                for (_, pending) in &held {
                    self.enqueue(pending.clone());
                }
                self.seal(host, config, analytics_now);
                // Deleted only once they are in a sealed batch on the spool.
                for (key, _) in held {
                    host.store_delete(&key);
                }
                self.wants_pump = true;
            }
            (State::Unknown, State::Denied) => {
                self.preconsent.clear();
                self.discard_held(host);
            }
            (State::Granted(_), State::Denied) => {
                // Not retroactive: what was collected under the answer goes, under it.
                self.seal_under(host, config, before, analytics_now);
                self.pending.clear();
                self.pending_bytes = 0;
                self.preconsent.clear();
            }
            (State::Granted(a), State::Granted(b)) if a != b => {
                self.seal_under(host, config, before, analytics_now);
            }
            _ => {}
        }
    }

    fn hold<H: Host>(&mut self, host: &H, pending: &Pending) {
        let key = format!(
            "{HELD_PREFIX}{:013}.{}",
            pending.time_ms.max(0),
            &uuid_v4(host)[..8]
        );
        if let Ok(bytes) = serde_json::to_vec(pending)
            && host.store_put(&key, &bytes).is_err()
        {
            host.log(
                Level::Warn,
                "could not keep a crash report on the device; it will be lost",
            );
        }
    }

    fn take_held<H: Host>(&mut self, host: &H) -> Vec<(String, Pending)> {
        let mut keys = host.store_keys(HELD_PREFIX);
        keys.sort();
        keys.into_iter()
            .filter_map(|key| {
                let bytes = host.store_get(&key)?;
                match serde_json::from_slice::<Pending>(&bytes) {
                    Ok(pending) => Some((key, pending)),
                    Err(_) => {
                        host.store_delete(&key);
                        None
                    }
                }
            })
            .collect()
    }

    /// How many crash reports are waiting on the device for the player's answer.
    pub fn held_crashes<H: Host>(&self, host: &H) -> usize {
        host.store_keys(HELD_PREFIX).len()
    }

    /// Deletes the crash reports waiting for an answer, without sending them.
    pub fn discard_held<H: Host>(&mut self, host: &H) {
        for key in host.store_keys(HELD_PREFIX) {
            host.store_delete(&key);
        }
    }

    // ---- the abnormal exit --------------------------------------------------

    /// Called at launch: a running marker left behind means the last run ended in the
    /// foreground without backgrounding or quitting. A crash, a watchdog kill, the system
    /// reclaiming memory from the game in front: from here they look the same, and all are
    /// what a player experienced as the game dying.
    pub fn check_last_run<H: Host>(&mut self, host: &H, config: &Config, analytics: &State) {
        let Some(bytes) = host.store_get(KEY_RUNNING) else {
            return;
        };
        host.store_delete(KEY_RUNNING);
        if !config.logs_enabled || !config.log_abnormal_exits {
            return;
        }
        let Ok(last) = serde_json::from_slice::<RunMarker>(&bytes) else {
            return;
        };
        let record = LogRecord {
            level: Some(LogLevel::Fatal),
            message: "The game stopped while in the foreground, without going to the background or quitting: \
                      a crash, a watchdog kill or the system reclaiming memory."
                .to_owned(),
            logger: "PlayerSense".to_owned(),
            exception: Some(LogException {
                type_name: "AbnormalExit".to_owned(),
                value: String::new(),
                handled: false,
                frames: Vec::new(),
            }),
            crash: true,
            fingerprint: vec!["ps.abnormal-exit".to_owned()],
            time_ms: Some(last.alive_ms),
            ..LogRecord::default()
        };
        let stamp = Stamp {
            session_id: Some(&last.session_id)
                .filter(|s| !s.is_empty())
                .map(String::as_str),
            player_id: None,
        };
        self.record(host, config, analytics, stamp, record);
    }

    /// The game is in front: the marker says so, and is refreshed on the heartbeat.
    pub fn foreground<H: Host>(&mut self, host: &H, config: &Config, session_id: Option<&str>) {
        self.foreground = true;
        self.cold = false;
        self.next_attempt_mono_ms = host.monotonic_ms();
        self.write_marker(host, config, session_id);
    }

    /// The game went away, cleanly: nothing to report next launch.
    pub fn background<H: Host>(&mut self, host: &H) {
        self.foreground = false;
        host.store_delete(KEY_RUNNING);
    }

    fn write_marker<H: Host>(&mut self, host: &H, config: &Config, session_id: Option<&str>) {
        if !config.logs_enabled || !config.log_abnormal_exits {
            return;
        }
        self.heartbeat_mono_ms = host.monotonic_ms();
        let marker = RunMarker {
            alive_ms: host.now_ms(),
            session_id: session_id.unwrap_or_default().to_owned(),
        };
        if let Ok(bytes) = serde_json::to_vec(&marker) {
            let _ = host.store_put(KEY_RUNNING, &bytes);
        }
    }

    // ---- sending ------------------------------------------------------------

    /// The client's tick: the heartbeat, the flush interval, and the next send.
    pub fn tick<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        analytics: &State,
        session_id: Option<&str>,
        ids: &mut u64,
    ) {
        let mono = host.monotonic_ms();
        if self.foreground && mono - self.heartbeat_mono_ms >= HEARTBEAT_MS {
            self.write_marker(host, config, session_id);
        }
        if !self.pending.is_empty() && mono - self.last_seal_mono_ms >= config.log_flush_interval_ms
        {
            self.seal(host, config, analytics);
        }
        self.pump(host, config, ids);
    }

    /// Seals and sends what is queued, now.
    pub fn flush<H: Host>(&mut self, host: &H, config: &Config, analytics: &State, ids: &mut u64) {
        self.seal(host, config, analytics);
        self.pump(host, config, ids);
    }

    /// Whether something was sealed that should go without waiting for the next tick.
    pub fn wants_pump(&self) -> bool {
        self.wants_pump
    }

    fn seal<H: Host>(&mut self, host: &H, config: &Config, analytics: &State) {
        let consent = self.effective(analytics);
        self.seal_under(host, config, &consent, analytics);
    }

    /// Turns what is pending into one immutable batch on the spool, under `consent`.
    fn seal_under<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        consent: &State,
        analytics: &State,
    ) {
        if self.pending.is_empty() {
            return;
        }
        let Some(granted) = consent.granted() else {
            return;
        };
        self.last_seal_mono_ms = host.monotonic_ms();
        let records = std::mem::take(&mut self.pending);
        self.pending_bytes = 0;
        let urgent = records.iter().any(Pending::urgent);

        let batch_id = uuid_v4(host);
        let body = match serde_json::to_vec(&envelope(
            config,
            self.install_id.clone(),
            &batch_id,
            granted,
            analytics.is_granted(),
            &records,
        )) {
            Ok(body) => body,
            Err(err) => {
                host.log(
                    Level::Error,
                    &format!("could not encode a log batch: {err}"),
                );
                return;
            }
        };
        self.ordinal += 1;
        let priority = if urgent {
            Priority::Critical
        } else {
            Priority::Normal
        };
        let key = spool::key_in(PREFIX, self.ordinal, host.now_ms(), priority, body.len());
        if host.store_put(&key, &body).is_err() {
            self.ordinal -= 1;
            host.log(
                Level::Warn,
                "could not write a log batch to the spool; it is dropped",
            );
            return;
        }
        let _ = host.store_put(KEY_ORDINAL, self.ordinal.to_string().as_bytes());
        let evicted = spool::enforce_in(host, PREFIX, bounds(config), host.now_ms());
        self.stats.evicted += (evicted.aged + evicted.overflowed) as u64;
        if urgent {
            self.next_attempt_mono_ms = self.next_attempt_mono_ms.min(host.monotonic_ms());
            self.wants_pump = true;
        }
    }

    /// Sends the oldest spooled log batch, if one may go.
    pub fn pump<H: Host>(&mut self, host: &H, config: &Config, ids: &mut u64) {
        self.wants_pump = false;
        if self.stopped
            || self.cold
            || self.in_flight.is_some()
            || host.monotonic_ms() < self.next_attempt_mono_ms
        {
            return;
        }
        let Some(meta) = spool::list_in(host, PREFIX).into_iter().next() else {
            return;
        };
        let Some(body) = host.store_get(&meta.key).filter(|b| !b.is_empty()) else {
            host.store_delete(&meta.key);
            return;
        };
        let (body, encoding) = if config.compression {
            match crate::client::gzip(&body) {
                Some(compressed) => (compressed, Some("gzip")),
                None => (body, None),
            }
        } else {
            (body, None)
        };
        let request_id = *ids;
        *ids += 1;
        self.in_flight = Some(InFlight {
            request_id,
            key: meta.key,
        });
        host.send(crate::host::Request {
            id: request_id,
            url: format!("{}/v1/logs", config.endpoint.trim_end_matches('/')),
            api_key: config.api_key.clone(),
            content_encoding: encoding,
            if_none_match: None,
            body,
        });
    }

    /// Whether a completion belongs to the log stream.
    pub fn owns(&self, request_id: u64) -> bool {
        self.in_flight
            .as_ref()
            .is_some_and(|f| f.request_id == request_id)
    }

    /// The answer to a log batch.
    pub fn on_response<H: Host>(
        &mut self,
        host: &H,
        config: &Config,
        response: crate::host::Response,
        ids: &mut u64,
    ) {
        let Some(in_flight) = self.in_flight.take() else {
            return;
        };
        use crate::transport::{self, Disposition};
        match transport::classify(response.status, response.retry_after_ms, &response.body) {
            Disposition::Delivered {
                accepted,
                dropped,
                reasons,
            } => {
                host.store_delete(&in_flight.key);
                self.failures = 0;
                self.stats.batches_delivered += 1;
                self.stats.accepted_by_server += u64::from(accepted);
                self.stats.dropped_by_server += u64::from(dropped);
                if dropped > 0 {
                    host.log(
                        Level::Warn,
                        &format!(
                            "the gateway refused {dropped} log records: {}",
                            reasons.join("; ")
                        ),
                    );
                }
                self.pump(host, config, ids);
            }
            Disposition::Discard { reason } => {
                host.store_delete(&in_flight.key);
                host.log(
                    Level::Error,
                    &format!("a log batch was refused and cannot be retried: {reason}"),
                );
                self.pump(host, config, ids);
            }
            Disposition::Split | Disposition::DisableCompression => {
                // A log batch is bounded well under the gateway's limit and gzip is always
                // accepted, so neither should happen; dropping the batch is the safe answer
                // to a gateway that disagrees.
                host.store_delete(&in_flight.key);
                self.pump(host, config, ids);
            }
            Disposition::Stop { reason } => {
                self.stopped = true;
                host.log(
                    Level::Error,
                    &format!("no further log batches will be sent: {reason}"),
                );
            }
            Disposition::Retry { after_ms } => {
                self.failures += 1;
                let wait = after_ms.unwrap_or_else(|| {
                    crate::transport::backoff_ms(host, config, self.failures - 1)
                });
                self.next_attempt_mono_ms = host.monotonic_ms() + wait;
                if self.failures >= config.cold_after_failures {
                    self.cold = true;
                }
            }
        }
    }

    /// Records waiting in memory or in a batch not yet sealed, for the tests.
    #[cfg(test)]
    pub fn pending_len(&self) -> usize {
        self.pending.len()
    }
}

#[derive(Serialize, Deserialize)]
struct RunMarker {
    alive_ms: i64,
    #[serde(default)]
    session_id: String,
}

impl Pending {
    fn message_for_crumb(&self) -> String {
        match &self.exception {
            Some(e) if self.message.is_empty() => format!("{}: {}", e.type_name, e.value),
            _ => self.message.clone(),
        }
    }
}

fn bounds(config: &Config) -> Bounds {
    Bounds {
        max_age_ms: config.log_spool_max_age_ms,
        max_bytes: config.log_spool_max_bytes,
        max_batches: config.log_spool_max_batches,
    }
}

/// What makes two records the same line: level, source, text and what was thrown.
fn fold_key(level: LogLevel, record: &LogRecord) -> u64 {
    let mut h = std::collections::hash_map::DefaultHasher::new();
    level.hash(&mut h);
    record.logger.hash(&mut h);
    record.message.hash(&mut h);
    if let Some(e) = &record.exception {
        e.type_name.hash(&mut h);
        e.value.hash(&mut h);
        e.frames.last().hash(&mut h);
    }
    h.finish()
}

fn level_name(level: LogLevel) -> &'static str {
    match level {
        LogLevel::Trace => "trace",
        LogLevel::Debug => "debug",
        LogLevel::Info => "info",
        LogLevel::Warn => "warn",
        LogLevel::Error => "error",
        LogLevel::Fatal => "fatal",
    }
}

/// Cuts text to at most `max` bytes on a character boundary, marking the cut.
fn clip(text: &str, max: usize) -> String {
    if text.len() <= max {
        return text.to_owned();
    }
    let mut cut = max.saturating_sub(3);
    while cut > 0 && !text.is_char_boundary(cut) {
        cut -= 1;
    }
    format!("{}…", &text[..cut])
}

fn is_false(b: &bool) -> bool {
    !*b
}

fn is_zero(n: &u32) -> bool {
    *n == 0
}

// ---- the wire -------------------------------------------------------------------------------

#[derive(Serialize)]
struct WireLogBatch<'a> {
    schema_version: u32,
    batch_id: &'a str,
    environment: &'a str,
    install_id: String,
    sdk: WireNamed<'a>,
    app: WireApp<'a>,
    device: WireDevice<'a>,
    consent: WireConsent<'a>,
    records: Vec<WireRecord<'a>>,
}

#[derive(Serialize)]
struct WireNamed<'a> {
    name: &'a str,
    version: &'a str,
}

#[derive(Serialize)]
struct WireApp<'a> {
    version: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    build: &'a str,
}

#[derive(Serialize)]
struct WireDevice<'a> {
    platform: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    os_version: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    locale: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    model: &'a str,
}

#[derive(Serialize)]
struct WireConsent<'a> {
    /// Whether analytics is granted too; the batch stands on `diagnostics`.
    analytics: bool,
    diagnostics: bool,
    policy_version: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    att_status: &'a str,
    gdpr_applies: bool,
    collected_at: &'a str,
}

#[derive(Serialize)]
struct WireRecord<'a> {
    time: String,
    level: &'static str,
    #[serde(skip_serializing_if = "str::is_empty")]
    message: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    logger: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    session_id: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    player_id: &'a str,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    attributes: BTreeMap<&'a str, String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    exception: Option<&'a LogException>,
    #[serde(skip_serializing_if = "is_false")]
    crash: bool,
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    breadcrumbs: &'a [Breadcrumb],
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    fingerprint: &'a [String],
}

fn envelope<'a>(
    config: &'a Config,
    install_id: String,
    batch_id: &'a str,
    granted: &'a Granted,
    analytics: bool,
    records: &'a [Pending],
) -> WireLogBatch<'a> {
    WireLogBatch {
        schema_version: crate::config::SCHEMA_VERSION,
        batch_id,
        environment: &config.environment,
        install_id,
        sdk: WireNamed {
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
            model: &config.device_model,
        },
        consent: WireConsent {
            analytics,
            diagnostics: true,
            policy_version: &granted.policy_version,
            att_status: &granted.att_status,
            gdpr_applies: granted.gdpr_applies,
            collected_at: &granted.collected_at,
        },
        records: records
            .iter()
            .map(|r| {
                let mut attributes: BTreeMap<&str, String> = r
                    .attributes
                    .iter()
                    .map(|(k, v)| (k.as_str(), v.clone()))
                    .collect();
                if r.repeat > 0 {
                    attributes.insert("ps.repeat", (r.repeat + 1).to_string());
                }
                WireRecord {
                    time: rfc3339_ms(r.time_ms),
                    level: level_name(r.level),
                    message: &r.message,
                    logger: &r.logger,
                    session_id: &r.session_id,
                    player_id: &r.player_id,
                    attributes,
                    exception: r.exception.as_ref(),
                    crash: r.crash,
                    breadcrumbs: &r.breadcrumbs,
                    fingerprint: &r.fingerprint,
                }
            })
            .collect(),
    }
}

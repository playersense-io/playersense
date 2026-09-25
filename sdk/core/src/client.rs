//! The protocol, as one state machine.
//!
//! Everything `docs/sdk/protocol.md` specifies and nothing a platform does. A binding — C#,
//! Swift, Kotlin, C++ — supplies a [`Host`] and calls the methods below; it makes no
//! decisions of its own, which is the point. Eleven implementations of retry and eleven
//! opinions about when a session starts is what §15 exists to prevent.

use std::collections::BTreeMap;

use crate::config::Config;
use crate::consent::{self, Granted, State};
use crate::context::{self, Context, Frame, Scope};
use crate::event::{
    Event, Priority, Recorded, Rejected, Value, envelope, sanitize_properties, valid_name,
};
use crate::exposure::{self, Exposures};
use crate::host::{Host, Level, Request, Response};
use crate::ids::uuid_v4;
use crate::liveconfig::{Event as LiveOpsEvent, Flag, LiveConfig};
use crate::logs::{Diagnostics, LogLevel, LogRecord, LogStats, Logged, Logs, Stamp};
use crate::session::Session;
use crate::spool;
use crate::transport::{self, Disposition};

/// Keys the client owns in the host's store, beside the spool's own.
const KEY_INSTALL: &str = "ps.install";
const KEY_ENDPOINT: &str = "ps.endpoint";
const KEY_ORDINAL: &str = "ps.ordinal";
const KEY_CONTEXT: &str = "ps.ctx.player";

/// What the SDK has done, for a game that wants to show it and a test that wants to assert it.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Stats {
    pub events_tracked: u64,
    /// Refused locally: a bad name, too many properties.
    pub events_rejected: u64,
    /// Not collected because the player said no.
    pub events_without_consent: u64,
    /// Lost to a full or ageing spool.
    pub events_evicted: u64,
    pub batches_spooled: u64,
    pub batches_delivered: u64,
    /// Batches the server will never accept, deleted rather than retried.
    pub batches_discarded: u64,
    pub requests_failed: u64,
    /// Events the gateway accepted, as it reported them.
    pub events_accepted_by_server: u64,
    /// Events the gateway refused, as it reported them. Non-zero is an integration bug.
    pub events_dropped_by_server: u64,
}

struct InFlight {
    request_id: u64,
    key: String,
}

pub struct Client<H: Host> {
    host: H,
    config: Config,

    install_id: String,
    endpoint: String,

    consent: State,
    session: Option<Session>,
    player_id: Option<String>,

    /// What the studio says about this player, in the studio's own words (§context).
    ///
    /// Held here rather than beside the events because it is *state* rather than a record of
    /// something that happened: targeting reads it, and a value that has not changed does
    /// not need restating on every event.
    context: Context,

    /// The merged context as it stood at the **first pending event**, which is what frame 0
    /// of the next batch carries.
    ///
    /// Held apart from `context` itself because the two diverge the moment a game sets
    /// context while events are already queued: those events happened under the old values
    /// and must keep them.
    context_base: BTreeMap<String, Value>,
    /// The merged context as of the most recent frame, so a change can be diffed into a delta
    /// rather than restated in full.
    context_last: BTreeMap<String, Value>,
    /// Deltas recorded since the last seal, each keyed by the event index it applies from.
    context_frames: Vec<Frame>,

    /// Collected and not yet batched.
    pending: Vec<Event>,
    pending_bytes: usize,
    /// Whether anything pending is [`Priority::Critical`], which is what makes a batch go
    /// without waiting for an interval.
    pending_critical: bool,
    /// Collected while consent is unknown. **Memory only** — never the spool (§5).
    preconsent: Vec<Event>,

    ordinal: u64,
    last_batch_mono_ms: i64,

    in_flight: Option<InFlight>,
    next_request_id: u64,

    /// Flags and LiveOps events, and the state of asking for them (§41, §42).
    ///
    /// Its own in-flight slot rather than sharing the batch path's, because the two must not
    /// block each other: a spool that cannot upload for an hour would otherwise mean a game
    /// running on stale flags for an hour, and a config fetch would take the one slot a
    /// session-start event is waiting for.
    liveconfig: LiveConfig,

    /// Which flags this session has already been recorded as having seen (§44).
    exposures: Exposures,
    consecutive_failures: u32,
    next_attempt_mono_ms: i64,
    /// Stopped attempting until the app is next brought to the foreground (§10).
    cold: bool,
    compression: bool,
    /// Set by a 401 or 403. The spool is kept; nothing further is sent.
    stopped: Option<String>,

    /// Logs, exceptions and crashes: a stream of their own beside the events (§15).
    logs: Logs,

    stats: Stats,
}

impl<H: Host> Client<H> {
    /// Opens the SDK. **Performs no I/O beyond the local store** (§11).
    ///
    /// No network call, no handshake, nothing that can fail because a server is unreachable.
    /// An SDK whose initialization can block a game's launch is a dependency of the game,
    /// which §3.2's whole posture rules out.
    pub fn new(host: H, config: Config) -> Self {
        let config = config.sanitized();

        let install_id = match host.store_get(KEY_INSTALL) {
            Some(bytes) if !bytes.is_empty() => String::from_utf8_lossy(&bytes).into_owned(),
            _ => {
                let fresh = uuid_v4(&host);
                // A failure here means a new install on every launch, which overstates
                // installs and understates retention. Worth saying out loud; not worth
                // refusing to run over.
                if host.store_put(KEY_INSTALL, fresh.as_bytes()).is_err() {
                    host.log(
                        Level::Error,
                        "could not persist the install id; \
                        this device will look like a new install on every launch",
                    );
                }
                fresh
            }
        };

        // A stored endpoint still wins over the configured one, and the reason has
        // outlived the 421 that used to write it: a build shipped against an address that
        // later moves has no other way to be told, and a host may seed this key before the
        // first launch.
        let endpoint = host
            .store_get(KEY_ENDPOINT)
            .map(|b| String::from_utf8_lossy(&b).into_owned())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| config.endpoint.clone());

        let ordinal = host
            .store_get(KEY_ORDINAL)
            .and_then(|b| String::from_utf8_lossy(&b).parse().ok())
            .unwrap_or(0);

        let monotonic = host.monotonic_ms();
        let logs = Logs::new(&host, &config, &install_id);
        let mut client = Self {
            host,
            config,
            install_id,
            endpoint,
            consent: State::Unknown,
            session: None,
            player_id: None,
            context: Context::new(),
            context_base: BTreeMap::new(),
            context_last: BTreeMap::new(),
            context_frames: Vec::new(),
            pending: Vec::new(),
            pending_bytes: 0,
            pending_critical: false,
            preconsent: Vec::new(),
            ordinal,
            last_batch_mono_ms: monotonic,
            in_flight: None,
            next_request_id: 1,
            liveconfig: LiveConfig::default(),
            exposures: Exposures::default(),
            consecutive_failures: 0,
            next_attempt_mono_ms: monotonic,
            cold: false,
            compression: true,
            stopped: None,
            logs,
            stats: Stats::default(),
        };
        client.compression = client.config.compression;

        // Whatever the last run left behind, brought inside its bounds before anything is
        // sent — a week-old spool must not be uploaded just because the app reopened (§6).
        client.evict();
        client.load_context();
        // Whether the last run died in the foreground. Consent is not known yet, so what it
        // finds waits on the device for the player's answer.
        client
            .logs
            .check_last_run(&client.host, &client.config, &client.consent);
        client
    }

    /// Restores the player-scoped context the last run saved, and whose it was.
    ///
    /// Nobody is signed in at this point, so the owner comes back with the values. The
    /// sign-in that follows decides their fate: the same player keeps them, anybody else
    /// clears them, and a game that never signs anyone in was anonymous both times.
    fn load_context(&mut self) {
        let Some(raw) = self.host.store_get(KEY_CONTEXT) else {
            return;
        };
        let Ok(saved) = serde_json::from_slice::<SavedContext>(&raw) else {
            self.host.store_delete(KEY_CONTEXT);
            return;
        };
        self.context.restore(saved.owner.as_deref(), saved.values);
        // Restored state is where this run starts, not a change within it.
        self.context_base = self.context.merged();
        self.context_last = self.context_base.clone();
    }

    fn save_context(&self) {
        let (owner, values) = self.context.player_values();
        let saved = SavedContext {
            owner: owner.map(str::to_owned),
            values: values.clone(),
        };
        match serde_json::to_vec(&saved) {
            Ok(bytes) => {
                let _ = self.host.store_put(KEY_CONTEXT, &bytes);
            }
            Err(_) => self.host.store_delete(KEY_CONTEXT),
        }
    }

    pub fn install_id(&self) -> &str {
        &self.install_id
    }

    pub fn stats(&self) -> Stats {
        self.stats
    }

    pub fn host(&self) -> &H {
        &self.host
    }

    // ---- what the game calls ------------------------------------------------

    /// Attaches an actor to every subsequent event, or detaches on `None`.
    ///
    /// **Deliberately does not flush.** `player_id` travels per event, so a batch that
    /// straddles a sign-in is correct: the events before carry no actor and the events after
    /// carry one. That is the exact opposite of a consent change, and the difference is
    /// structural — consent is on the batch, the actor is on the event (§3, §5).
    pub fn set_player(&mut self, player_id: Option<String>) {
        self.player_id = player_id.filter(|id| !id.is_empty());

        // Player-scoped context belongs to the player. A device where somebody else signs in
        // must not inherit the last one's rank, and a sign-out must not leave it behind.
        if self.context.player_changed(self.player_id.as_deref()) {
            self.note_context_change();
            self.save_context();
        }
    }

    /// Describes the player or the moment, in the studio's own vocabulary.
    ///
    /// PlayerSense never learns what these mean. One studio's `progress.chapter` is
    /// another's `combat_level` and a third has neither, so this is a key, a value and a
    /// scope — and targeting reads them back by name under `context.`.
    ///
    /// Namespaces PlayerSense resolves for itself are refused by name rather than quietly
    /// accepted: see [`crate::context`].
    pub fn set_context(
        &mut self,
        scope: Scope,
        key: &str,
        value: Value,
    ) -> Result<(), context::Rejected> {
        let before = self.context.revision();
        let result = self.context.set(scope, key, value);
        match &result {
            Ok(()) => {
                if self.context.revision() != before {
                    self.note_context_change();
                    if scope == Scope::Player {
                        self.save_context();
                    }
                }
            }
            Err(rejected) => {
                let level = if self.config.debug {
                    Level::Warn
                } else {
                    Level::Debug
                };
                self.host
                    .log(level, &format!("context {key:?} refused: {rejected}"));
            }
        }
        result
    }

    /// Removes a context value.
    pub fn remove_context(&mut self, scope: Scope, key: &str) {
        let before = self.context.revision();
        self.context.remove(scope, key);
        if self.context.revision() != before {
            self.note_context_change();
            if scope == Scope::Player {
                self.save_context();
            }
        }
    }

    /// Records a context change against the events already queued.
    ///
    /// This is the rule that makes context frames mean anything: **a change applies to future
    /// events, never to queued ones.** A game that sets `rank = diamond` after tracking
    /// `match_started` must leave that event under the old rank, even though both end up in
    /// one batch — the same principle that already lets a batch straddle a sign-in.
    ///
    /// With nothing queued there is no "future" to distinguish, so the change folds into the
    /// snapshot the next batch will open with. With events queued it becomes a delta at the
    /// index the *next* event will take.
    fn note_context_change(&mut self) {
        let now = self.context.merged();

        if self.pending.is_empty() {
            self.context_base = now.clone();
            self.context_frames.clear();
        } else {
            let (set, unset) = context::diff(&self.context_last, &now);
            if !set.is_empty() || !unset.is_empty() {
                let at = self.pending.len();
                match self.context_frames.last_mut().filter(|f| f.at == at) {
                    // Several changes before the next event are one frame, not several.
                    Some(frame) => {
                        frame.set.extend(set);
                        for key in unset {
                            frame.set.remove(&key);
                            if !frame.unset.contains(&key) {
                                frame.unset.push(key);
                            }
                        }
                        frame.unset.retain(|k| !frame.set.contains_key(k));
                    }
                    None => self.context_frames.push(Frame {
                        at,
                        values: None,
                        set,
                        unset,
                    }),
                }
            }
        }
        self.context_last = now;
    }

    /// The timeline for a batch about to be sealed: a full snapshot, then the deltas.
    ///
    /// Frame 0 is always a snapshot so the batch is self-contained — the gateway never has to
    /// remember what the previous batch left the context at, which is what keeps an immutable
    /// batch a complete transport unit (ADR 0026).
    fn context_timeline(&mut self) -> Vec<Frame> {
        let mut frames = std::mem::take(&mut self.context_frames);
        if !self.context_base.is_empty() || !frames.is_empty() {
            frames.insert(
                0,
                Frame {
                    at: 0,
                    values: Some(self.context_base.clone()),
                    set: BTreeMap::new(),
                    unset: Vec::new(),
                },
            );
        }
        // The next batch opens where this one left off.
        self.context_base = self.context_last.clone();
        frames
    }

    /// Everything targeting would be given, session values winning a clash.
    pub fn context(&self) -> BTreeMap<String, Value> {
        self.context.merged()
    }

    /// Bumped whenever the context changes.
    ///
    /// What a configuration refresh watches: a burst of `set_context` calls raises it once
    /// each, and the refresh that reads it debounces them into a single re-evaluation rather
    /// than one request per key.
    pub fn context_revision(&self) -> u64 {
        self.context.revision()
    }

    /// Records what the player has said about analytics (§5).
    pub fn set_consent(&mut self, next: State) {
        let before = self.consent.clone();
        self.set_analytics_consent(next);
        // Error reports follow this answer unless the game has asked about them separately.
        let after = self.consent.clone();
        self.logs
            .analytics_moved(&self.host, &self.config, &before, &after);
        self.pump_logs_if_due();
    }

    fn set_analytics_consent(&mut self, next: State) {
        match consent::transition(&self.consent, &next) {
            consent::Transition::None => {
                self.consent = next;
                return;
            }
            consent::Transition::PromoteBuffer => {
                // The buffer becomes lawful. Everything held in memory joins the queue, in
                // the order it happened.
                self.consent = next;
                let buffered = std::mem::take(&mut self.preconsent);
                for event in buffered {
                    self.enqueue(event);
                }
                self.maybe_seal();
            }
            consent::Transition::DiscardBuffer => {
                self.preconsent.clear();
                self.consent = next;
            }
            consent::Transition::FlushThenStop | consent::Transition::FlushThenContinue => {
                // Under the *old* block, which is still on `self.consent` at this point.
                // Withdrawal is not retroactive: those events were collected lawfully, and
                // sending them after the state flipped would stamp them with a basis that
                // was not theirs (§5).
                self.seal_batch();
                self.consent = next;
                if !self.consent.is_granted() {
                    self.pending.clear();
                    self.pending_bytes = 0;
                    self.preconsent.clear();
                }
            }
        }
        self.pump();
    }

    /// Records an event. Never blocks, never throws, never allocates without bound.
    ///
    /// The [`Recorded`] tells a binding what became of it — queued, held pending the player's
    /// answer, or discarded because they declined. All three are the SDK working; only
    /// [`Rejected`] is a bug in the call.
    pub fn track(
        &mut self,
        name: &str,
        properties: BTreeMap<String, Value>,
    ) -> Result<Recorded, Rejected> {
        self.track_event(name, properties, 0, None)
    }

    /// Records an event that carries money.
    ///
    /// Analytics about a purchase, not the purchase itself: the money fact comes from the
    /// studio's backend verifying the receipt against the store (§46–§48), because a
    /// modified client can claim anything. This is what makes a funnel, and it is
    /// [`Priority::Critical`] so it does not sit in a buffer waiting for an interval.
    pub fn track_revenue(
        &mut self,
        name: &str,
        revenue_micros: i64,
        currency: &str,
        properties: BTreeMap<String, Value>,
    ) -> Result<Recorded, Rejected> {
        self.track_event(name, properties, revenue_micros, Some(currency.to_owned()))
    }

    /// Seals whatever is queued and tries to send.
    pub fn flush(&mut self) {
        self.seal_batch();
        self.pump();
        self.logs.flush(
            &self.host,
            &self.config,
            &self.consent,
            &mut self.next_request_id,
        );
    }

    // ---- logs, exceptions and crashes (§15) ---------------------------------

    /// Records a log line, an exception or a crash. Never blocks, never throws; the answer
    /// says what became of it.
    pub fn log(&mut self, record: LogRecord) -> Logged {
        let stamp = Stamp {
            session_id: self.session.as_ref().map(|s| s.id.as_str()),
            player_id: self.player_id.as_deref(),
        };
        let outcome = self
            .logs
            .record(&self.host, &self.config, &self.consent, stamp, record);
        self.pump_logs_if_due();
        outcome
    }

    /// Something that happened, kept in memory for the next error to carry.
    pub fn add_breadcrumb(&mut self, category: &str, message: &str, level: LogLevel) {
        if self.config.logs_enabled {
            let now = self.host.now_ms();
            self.logs
                .breadcrumb(&self.config, now, category, message, level);
        }
    }

    /// The player's answer about error reports, apart from analytics.
    pub fn set_diagnostics_consent(&mut self, next: Diagnostics) {
        self.logs
            .set_diagnostics(&self.host, &self.config, &self.consent, next);
        self.pump_logs_if_due();
    }

    /// Crash reports kept on the device while the player has not answered: a game can offer
    /// to send them ("The game closed unexpectedly. Send a report?").
    pub fn held_crash_reports(&self) -> usize {
        self.logs.held_crashes(&self.host)
    }

    /// Deletes the crash reports waiting for an answer, unsent.
    pub fn discard_held_crash_reports(&mut self) {
        self.logs.discard_held(&self.host);
    }

    pub fn log_stats(&self) -> LogStats {
        self.logs.stats
    }

    fn pump_logs_if_due(&mut self) {
        if self.logs.wants_pump() {
            self.logs
                .pump(&self.host, &self.config, &mut self.next_request_id);
        }
    }

    /// The app came back to the front.
    pub fn foreground(&mut self) {
        let monotonic = self.host.monotonic_ms();

        // A session that expired while away is over; the next event starts a new one, which
        // is what emits `session_start` (§4).
        if let Some(session) = &mut self.session {
            if session.expired(monotonic, self.config.session_timeout_ms) {
                self.session = None;
            } else {
                session.foregrounded();
            }
        }

        // One attempt per foreground while an endpoint is down: the cold state lifts but the
        // failure count does not, so the very next failure puts it straight back. That is
        // the behaviour §10 asks for — a dead server costs one request, not a battery.
        self.cold = false;
        self.next_attempt_mono_ms = monotonic;
        self.pump();

        let session = self.session.as_ref().map(|s| s.id.clone());
        self.logs
            .foreground(&self.host, &self.config, session.as_deref());
        self.logs
            .pump(&self.host, &self.config, &mut self.next_request_id);

        // A game resumed after three hours is the case where the answer is most likely to
        // have moved, so the interval is brought forward rather than waited out.
        self.refresh_config();
    }

    /// The app went away. **Spool first, then attempt the network.**
    ///
    /// The order is the single most important line in this file. This callback is the last
    /// moment the SDK is guaranteed to run, and using it to start a request that may never
    /// finish — without writing to disk first — is how an SDK loses a session (§11).
    pub fn background(&mut self) {
        let monotonic = self.host.monotonic_ms();
        self.seal_batch();
        if let Some(session) = &mut self.session {
            session.backgrounded(monotonic);
        }
        self.pump();
        // The same order for logs: spooled, then attempted, and the running marker cleared,
        // because going to the background is a clean exit whatever happens next.
        self.logs.flush(
            &self.host,
            &self.config,
            &self.consent,
            &mut self.next_request_id,
        );
        self.logs.background(&self.host);
    }

    /// Called regularly by the host — a frame loop, a timer. Cheap when there is nothing due.
    pub fn tick(&mut self) {
        let monotonic = self.host.monotonic_ms();

        let idle = monotonic - self.last_batch_mono_ms >= self.config.flush_interval_ms;
        let foreground = self.session.as_ref().is_none_or(|s| !s.is_backgrounded());
        if idle && foreground && !self.pending.is_empty() {
            self.seal_batch();
        }
        self.pump();

        let session = self.session.as_ref().map(|s| s.id.clone());
        self.logs.tick(
            &self.host,
            &self.config,
            &self.consent,
            session.as_deref(),
            &mut self.next_request_id,
        );

        if self
            .liveconfig
            .due(monotonic, self.config.config_refresh_interval_ms)
        {
            self.send_config_request(monotonic);
        }
    }

    // ---- configuration ------------------------------------------------------

    /// Asks for this player's configuration now, if one is not already in flight.
    ///
    /// Called on foreground and on the refresh interval, and safe to call directly: a studio
    /// that has just changed what it segments on — the player finished the tutorial, joined a
    /// guild — wants the answer that follows from it rather than the one from before.
    pub fn refresh_config(&mut self) {
        let monotonic = self.host.monotonic_ms();
        if self.liveconfig.in_flight() {
            return;
        }
        self.send_config_request(monotonic);
    }

    /// Applies a configuration document directly, for tests.
    ///
    /// The network path goes through [`Client::on_response`]; this is the same absorption
    /// without a request, so a test about exposure is a test about exposure rather than three
    /// lines of request plumbing repeated eight times.
    #[cfg(test)]
    pub(crate) fn set_config_for_test(&mut self, document: &str) {
        let monotonic = self.host.monotonic_ms();
        self.liveconfig
            .apply(&self.host, 200, document.as_bytes(), None, monotonic);
    }

    /// Restores the document persisted by a previous run.
    ///
    /// Flags only, in effect: no clock anchor comes off disk, so nothing is reported active
    /// until the server has been heard from. See the module comment in `liveconfig`.
    pub fn restore_config(&mut self) {
        self.liveconfig.restore(&self.host);
    }

    /// The published version behind the current answers. Zero when none has been fetched.
    pub fn config_version(&self) -> u32 {
        self.liveconfig.version()
    }

    /// Whether the server has been heard from in this process, which is what makes a window
    /// meaningful.
    pub fn config_anchored(&self) -> bool {
        self.liveconfig.anchored()
    }

    /// One flag's published value, or None when the project has no such flag.
    ///
    /// None rather than a default, so a caller can tell "not published" from "published as
    /// false". The bindings fold it into the game's own fallback at the call site, which is
    /// where the fallback belongs — a default chosen here would be a default nobody chose.
    pub fn flag(&self, key: &str) -> Option<&Flag> {
        self.liveconfig.flag(key)
    }

    /// Every published flag. Peeks: reading them all is an inspection rather than a hundred
    /// decisions, so it records nothing.
    pub fn flags(&self) -> impl Iterator<Item = (&String, &Flag)> {
        self.liveconfig.flags()
    }

    /// Reads a boolean flag **and records that this player saw it** (§44).
    ///
    /// The three typed readers below are what a binding calls; [`Client::flag`] is the peek
    /// that does not record, for anything inspecting rather than deciding.
    ///
    /// `None` means the project published no boolean under that key, and the binding
    /// substitutes the game's own default — which is still an exposure, recorded with
    /// `published: false`, because a player running on a default is in neither arm and an
    /// analysis that could not see that would count the control group as smaller than it was.
    pub fn read_flag_bool(&mut self, key: &str) -> Option<bool> {
        let value = match self.liveconfig.flag(key) {
            Some(Flag::Bool(value)) => Some(*value),
            _ => None,
        };
        self.record_exposure(key, value.map(Value::Bool));
        value
    }

    /// Reads a numeric flag and records the exposure. See [`Client::read_flag_bool`].
    pub fn read_flag_number(&mut self, key: &str) -> Option<f64> {
        let value = match self.liveconfig.flag(key) {
            Some(Flag::Number(value)) => Some(*value),
            _ => None,
        };
        self.record_exposure(key, value.map(Value::Float));
        value
    }

    /// Reads a string or JSON flag and records the exposure. See [`Client::read_flag_bool`].
    pub fn read_flag_text(&mut self, key: &str) -> Option<String> {
        let value = match self.liveconfig.flag(key) {
            Some(Flag::Text(value)) => Some(value.clone()),
            Some(Flag::Json(value)) => Some(value.clone()),
            _ => None,
        };
        // A JSON flag's whole document would be a property on every exposure, which for a
        // shop layout is kilobytes per player per session. The key and the version identify
        // which variant this was; the document itself is already in the published version.
        let recorded = match self.liveconfig.flag(key) {
            Some(Flag::Text(value)) => Some(Value::Str(value.clone())),
            Some(Flag::Json(_)) => None,
            _ => None,
        };
        self.record_exposure(key, recorded);
        value
    }

    /// Records that this player read a flag, at most once per flag per version per session.
    fn record_exposure(&mut self, key: &str, value: Option<Value>) {
        if !self.config.exposure_events || key.is_empty() {
            return;
        }

        // Before the set is consulted, not after. Starting a session clears the exposures —
        // a new visit is counted separately — so recording first and letting `track` start the
        // session would clear the entry just written and the very first flag read of every
        // session would be recorded twice. It was, until a test counted them.
        //
        // A player who has declined is left alone: `track` would refuse the event anyway, and
        // starting a session for them would be collection nobody consented to.
        if matches!(self.consent, State::Denied) {
            return;
        }
        self.ensure_session();

        let version = self.liveconfig.version();
        let max = self.config.max_exposures_per_session;
        if !self.exposures.first_time(key, version, max) {
            if self.exposures.newly_capped(max) {
                self.host.log(
                    Level::Warn,
                    &format!(
                        "this session has recorded {max} feature exposures and will record no                          more; a game reading that many distinct flags produces an exposure                          stream larger than its analytics"
                    ),
                );
            }
            return;
        }

        let published = value.is_some() || self.liveconfig.flag(key).is_some();
        let mut properties = BTreeMap::new();
        properties.insert(
            exposure::property::FLAG.to_owned(),
            Value::Str(key.to_owned()),
        );
        properties.insert(
            exposure::property::VERSION.to_owned(),
            Value::Int(i64::from(version)),
        );
        properties.insert(
            exposure::property::PUBLISHED.to_owned(),
            Value::Bool(published),
        );
        if let Some(value) = value {
            properties.insert(exposure::property::VALUE.to_owned(), value);
        }

        // Ordinary priority: an exposure is a fact about a decision already made, and holding
        // a batch open for it would flush more often for no one's benefit. The result is
        // dropped rather than returned — a binding reading a flag is asking about the flag,
        // and `Rejected` here would be a bug in this function rather than in their call.
        let _ = self.track(exposure::EVENT_NAME, properties);
    }

    /// The LiveOps events on right now, highest priority first.
    pub fn liveops_active(&self) -> Vec<&LiveOpsEvent> {
        self.liveconfig.active(self.host.monotonic_ms())
    }

    /// The ones starting soon, soonest first.
    pub fn liveops_upcoming(&self) -> &[LiveOpsEvent] {
        self.liveconfig.upcoming()
    }

    /// One event by key, on or not, so a game can say "starts Friday" as easily as "on now".
    pub fn liveops_event(&self, key: &str) -> Option<&LiveOpsEvent> {
        self.liveconfig.event(key)
    }

    /// The events on now, as the JSON document a binding parses. See
    /// [`crate::liveconfig::render_events`].
    pub fn liveops_active_json(&self) -> String {
        crate::liveconfig::render_events(
            self.config_anchored(),
            self.config_version(),
            self.server_now_ms().unwrap_or(0),
            self.liveops_active().into_iter(),
        )
    }

    /// The events starting soon, in the same shape.
    pub fn liveops_upcoming_json(&self) -> String {
        crate::liveconfig::render_events(
            self.config_anchored(),
            self.config_version(),
            self.server_now_ms().unwrap_or(0),
            self.liveops_upcoming().iter(),
        )
    }

    /// Now in the server's clock, which is what every window is measured against.
    pub fn server_now_ms(&self) -> Option<i64> {
        self.liveconfig.server_now_ms(self.host.monotonic_ms())
    }

    fn send_config_request(&mut self, now_mono_ms: i64) {
        // No endpoint means a studio using analytics alone, and asking would be a request
        // per interval for an answer nobody reads.
        if self.config.config_endpoint.is_empty() || self.stopped.is_some() {
            return;
        }

        let body = match serde_json::to_vec(&self.config_request()) {
            Ok(body) => body,
            Err(_) => return,
        };

        let request_id = self.next_request_id;
        self.next_request_id += 1;
        self.liveconfig.begin(request_id, now_mono_ms);

        self.host.send(Request {
            id: request_id,
            url: format!(
                "{}/v1/config",
                self.config.config_endpoint.trim_end_matches('/')
            ),
            api_key: self.config.api_key.clone(),
            // Uncompressed: the body is a few hundred bytes and gzip's header alone is
            // eighteen of them.
            content_encoding: None,
            if_none_match: self.liveconfig.etag().map(str::to_owned),
            body,
        });
    }

    /// The request body, which carries nothing the server would refuse to believe.
    ///
    /// No country, no network, no purchase history: all three are derived server-side from
    /// the request's own connection and PlayerSense's own record, because a value the client
    /// sends is a value a modified build can choose (ADR 0027).
    fn config_request(&self) -> serde_json::Value {
        serde_json::json!({
            "schema_version": crate::config::SCHEMA_VERSION,
            "install_id": self.install_id,
            "player_id": self.player_id.clone().unwrap_or_default(),
            "environment": self.config.environment,
            "app": {
                "version": self.config.app_version,
                "build": self.config.app_build,
            },
            "device": {
                "platform": self.config.platform,
                "os_version": self.config.os_version,
                "locale": self.config.locale,
            },
            "sdk": {
                "name": self.config.sdk_name,
                "version": self.config.sdk_version,
            },
            "context": crate::liveconfig::context_member(&self.context.merged()),
        })
    }

    /// One last bounded attempt. Whatever does not go stays for the next launch (§10).
    pub fn shutdown(&mut self) {
        self.seal_batch();
        self.pump();
        self.logs.flush(
            &self.host,
            &self.config,
            &self.consent,
            &mut self.next_request_id,
        );
        self.logs.background(&self.host);
    }

    // ---- what the host calls back -------------------------------------------

    /// The answer to a [`Request`]. Must be called exactly once per request, including for
    /// requests that never reached a server.
    pub fn on_response(&mut self, response: Response) {
        // Configuration first, and by identifier rather than by guessing from the shape of
        // the body. Two request kinds share one callback, and a batch completion applied to
        // the config path would delete a spooled batch nobody uploaded.
        if self.liveconfig.owns(response.id) {
            let monotonic = self.host.monotonic_ms();
            self.liveconfig.apply(
                &self.host,
                response.status,
                &response.body,
                response.etag,
                monotonic,
            );
            return;
        }

        if self.logs.owns(response.id) {
            self.logs.on_response(
                &self.host,
                &self.config,
                response,
                &mut self.next_request_id,
            );
            return;
        }

        let Some(in_flight) = self.in_flight.take() else {
            // Nothing outstanding: a duplicate completion, or one from before a reset. Late
            // answers are ignored rather than acted on, because acting on one would delete a
            // batch the client has since decided to resend.
            return;
        };
        if in_flight.request_id != response.id {
            self.in_flight = Some(in_flight);
            return;
        }

        let key = in_flight.key;
        match transport::classify(response.status, response.retry_after_ms, &response.body) {
            Disposition::Delivered {
                accepted,
                dropped,
                reasons,
            } => {
                self.host.store_delete(&key);
                self.consecutive_failures = 0;
                self.stats.batches_delivered += 1;
                self.stats.events_accepted_by_server += u64::from(accepted);
                self.stats.events_dropped_by_server += u64::from(dropped);

                if dropped > 0 {
                    // Never silent. §9: a studio shipping a release where a third of their
                    // events have a space in the name finds out now rather than in a month.
                    self.host.log(
                        Level::Warn,
                        &format!(
                            "the gateway refused {dropped} events: {}",
                            reasons.join("; ")
                        ),
                    );
                }
                self.pump();
            }

            Disposition::Discard { reason } => {
                self.host.store_delete(&key);
                self.consecutive_failures = 0;
                self.stats.batches_discarded += 1;
                self.host.log(
                    Level::Error,
                    &format!("a batch was refused and cannot be retried: {reason}"),
                );
                self.pump();
            }

            Disposition::Split => {
                self.split(&key);
                self.consecutive_failures = 0;
                self.pump();
            }

            Disposition::Stop { reason } => {
                // The spool is kept. The key can be fixed from the dashboard, and throwing
                // away the player's events because of a configuration mistake would be the
                // worse of the two failures.
                self.stopped = Some(reason.clone());
                self.host.log(
                    Level::Error,
                    &format!(
                        "no further batches will be sent: {reason}. \
                        Spooled events are kept and will go when the credential is fixed."
                    ),
                );
            }

            Disposition::DisableCompression => {
                self.host.log(
                    Level::Warn,
                    "the gateway refused the encoding; sending uncompressed",
                );
                self.compression = false;
                self.consecutive_failures = 0;
                self.pump();
            }

            Disposition::Retry { after_ms } => {
                self.stats.requests_failed += 1;
                self.consecutive_failures += 1;
                let wait = after_ms.unwrap_or_else(|| {
                    transport::backoff_ms(&self.host, &self.config, self.consecutive_failures - 1)
                });
                self.next_attempt_mono_ms = self.host.monotonic_ms() + wait;

                if self.consecutive_failures >= self.config.cold_after_failures {
                    self.cold = true;
                    self.host.log(
                        Level::Warn,
                        &format!(
                            "{} attempts failed in a row; pausing until the app is next in the foreground",
                            self.consecutive_failures
                        ),
                    );
                }
            }
        }
    }

    // ---- the machinery ------------------------------------------------------

    fn track_event(
        &mut self,
        name: &str,
        properties: BTreeMap<String, Value>,
        revenue_micros: i64,
        currency: Option<String>,
    ) -> Result<Recorded, Rejected> {
        // Validated *before* consent is consulted, so a misspelled event name is reported to
        // the developer whatever the player has said. The other order hides every integration
        // bug behind a declining test device, which is the one place a developer looks last.
        // Validation reads the arguments and stores nothing, so it is lawful in any state.
        if !valid_name(name) {
            self.stats.events_rejected += 1;
            self.log_rejection(name, Rejected::Name);
            return Err(Rejected::Name);
        }
        let properties = match sanitize_properties(properties) {
            Ok(props) => props,
            Err(rejected) => {
                self.stats.events_rejected += 1;
                self.log_rejection(name, rejected.clone());
                return Err(rejected);
            }
        };

        if matches!(self.consent, State::Denied) {
            self.stats.events_without_consent += 1;
            return Ok(Recorded::Declined);
        }

        // Starting a session emits `session_start` ahead of this event, so the first thing
        // the server sees for a session is the session (§4).
        self.ensure_session();

        let monotonic = self.host.monotonic_ms();
        let session = self.session.as_mut().expect("ensure_session just ran");
        let event = Event {
            name: name.to_owned(),
            event_time_ms: session.event_time_ms(monotonic),
            session_id: session.id.clone(),
            player_id: self.player_id.clone(),
            sequence: session.next_sequence(),
            properties,
            revenue_micros,
            currency,
        };

        // Decided before `enqueue`, which is where the distinction is acted on: a granted
        // client queues for delivery, an unanswered one holds in memory only.
        let outcome = if self.consent.is_granted() {
            Recorded::Queued
        } else {
            Recorded::AwaitingConsent
        };

        self.stats.events_tracked += 1;
        self.enqueue(event);
        self.maybe_seal();
        Ok(outcome)
    }

    /// Puts an event where the current consent state says it may go.
    ///
    /// Deliberately does not decide whether to send. A session start and the event that
    /// caused it arrive as a pair, and evaluating the triggers after the first would seal a
    /// batch holding nothing but the session start — every session paying for two requests,
    /// and the event that actually happened left behind in the next one.
    fn enqueue(&mut self, event: Event) {
        if !self.consent.is_granted() {
            // Memory only. A spool is a copy on disk of data collected under no basis (§5).
            if self.preconsent.len() >= self.config.preconsent_max_events {
                self.preconsent.remove(0);
                self.stats.events_evicted += 1;
            }
            self.preconsent.push(event);
            return;
        }

        self.pending_critical |= event.priority() == Priority::Critical;
        self.pending_bytes += estimate_bytes(&event);
        self.pending.push(event);
    }

    /// Seals and sends if any of §7's triggers is met.
    ///
    /// A session start carries the install's cohort assignment and revenue is money; neither
    /// waits for a batching interval.
    fn maybe_seal(&mut self) {
        if self.pending.is_empty() {
            return;
        }
        if self.pending_critical
            || self.pending.len() >= self.config.max_events_per_batch
            || self.pending_bytes >= self.config.max_batch_bytes
        {
            self.seal_batch();
            self.pump();
        }
    }

    /// Starts a session when there is none, or when the last one is over.
    fn ensure_session(&mut self) {
        let monotonic = self.host.monotonic_ms();
        let over = match &self.session {
            None => true,
            Some(s) => s.expired(monotonic, self.config.session_timeout_ms),
        };
        if !over {
            return;
        }

        self.session = Some(Session::start(&self.host));
        // A new session is a new visit, and retention is counted in visits: a player who came
        // back has to be recorded as having seen the flag again, or the second session looks
        // like one nobody was in the experiment for.
        self.exposures.clear();
        let before_context = self.context.revision();
        self.context.session_started();
        if self.context.revision() != before_context {
            self.note_context_change();
        }
        let session = self.session.as_mut().expect("just set");
        let start = Event {
            name: "session_start".to_owned(),
            event_time_ms: session.event_time_ms(monotonic),
            session_id: session.id.clone(),
            player_id: self.player_id.clone(),
            sequence: session.next_sequence(),
            properties: BTreeMap::new(),
            revenue_micros: 0,
            currency: None,
        };
        self.stats.events_tracked += 1;
        self.enqueue(start);
    }

    /// Turns everything pending into one immutable batch on disk.
    ///
    /// Immutable is the load-bearing word (§6, ADR 0026). The batch id is fixed here and the
    /// events are frozen in this order, because the gateway derives each event's identity
    /// from the batch id and the event's position — so a client that re-packed a batch
    /// between attempts would give the same events different identities and defeat
    /// deduplication.
    fn seal_batch(&mut self) {
        if self.pending.is_empty() {
            return;
        }
        let Some(granted) = self.consent.granted().cloned() else {
            return;
        };

        let events = std::mem::take(&mut self.pending);
        self.pending_bytes = 0;
        self.pending_critical = false;
        self.last_batch_mono_ms = self.host.monotonic_ms();

        let batch_id = uuid_v4(&self.host);
        let context = self.context_timeline();
        let wire = envelope(
            &self.config,
            &self.install_id,
            &batch_id,
            &granted,
            &context,
            &events,
        );
        let body = match serde_json::to_vec(&wire) {
            Ok(body) => body,
            Err(err) => {
                // Unreachable in practice — the envelope is plain data — and dropping is
                // still the right answer, because a body that cannot be produced now cannot
                // be produced on a retry either.
                self.host
                    .log(Level::Error, &format!("could not encode a batch: {err}"));
                return;
            }
        };

        let priority = events
            .iter()
            .map(Event::priority)
            .max()
            .unwrap_or(Priority::Normal);

        if !self.write_spool(priority, &body, events.len()) {
            // The disk refused. Put the events back rather than losing them; the next flush
            // trigger tries again, and the bound below keeps memory finite (§6).
            self.pending_critical = events.iter().any(|e| e.priority() == Priority::Critical);
            self.pending = events;
            self.pending_bytes = self.pending.iter().map(estimate_bytes).sum();
            self.bound_pending();
            return;
        }
        self.evict();
    }

    /// Writes one batch to the spool, returning whether it landed.
    fn write_spool(&mut self, priority: Priority, body: &[u8], events: usize) -> bool {
        self.ordinal += 1;
        let key = spool::key(self.ordinal, self.host.now_ms(), priority, body.len());

        if self.host.store_put(&key, body).is_err() {
            self.ordinal -= 1;
            self.host.log(
                Level::Warn,
                "could not write a batch to the spool; holding it in memory, where it may be dropped",
            );
            return false;
        }
        // After the batch, so a crash between the two leaves an ordinal that is reused —
        // which overwrites nothing, because the key also carries the timestamp and size.
        let _ = self
            .host
            .store_put(KEY_ORDINAL, self.ordinal.to_string().as_bytes());
        self.stats.batches_spooled += 1;
        let _ = events;
        true
    }

    /// Keeps the in-memory queue finite when the disk will not take it.
    fn bound_pending(&mut self) {
        let ceiling = self.config.max_events_per_batch.saturating_mul(4);
        while self.pending.len() > ceiling {
            // Ordinary events first, exactly as the spool evicts (§6).
            let victim = self
                .pending
                .iter()
                .position(|e| e.priority() == Priority::Normal)
                .unwrap_or(0);
            let dropped = self.pending.remove(victim);
            self.pending_bytes = self.pending_bytes.saturating_sub(estimate_bytes(&dropped));
            self.stats.events_evicted += 1;
        }
    }

    fn evict(&mut self) {
        let evicted = spool::enforce(&self.host, &self.config, self.host.now_ms());
        if evicted.any() {
            self.stats.events_evicted += (evicted.aged + evicted.overflowed) as u64;
        }
    }

    /// Sends the oldest spooled batch, if anything may be sent at all.
    fn pump(&mut self) {
        if self.stopped.is_some() || self.cold || self.in_flight.is_some() {
            return;
        }
        if self.host.monotonic_ms() < self.next_attempt_mono_ms {
            return;
        }

        let Some(meta) = spool::list(&self.host).into_iter().next() else {
            return;
        };
        let Some(body) = self.host.store_get(&meta.key).filter(|b| !b.is_empty()) else {
            // A key with no value: a truncated write, or a store cleared underneath us.
            // Discarded rather than retried forever (§6).
            self.host.store_delete(&meta.key);
            self.host.log(
                Level::Warn,
                "a spooled batch was unreadable and has been discarded",
            );
            return;
        };

        let (body, content_encoding) = if self.compression {
            match gzip(&body) {
                Some(compressed) => (compressed, Some("gzip")),
                None => (body, None),
            }
        } else {
            (body, None)
        };

        let request_id = self.next_request_id;
        self.next_request_id += 1;
        self.in_flight = Some(InFlight {
            request_id,
            key: meta.key,
        });

        self.host.send(Request {
            id: request_id,
            url: format!("{}/v1/batches", self.endpoint.trim_end_matches('/')),
            api_key: self.config.api_key.clone(),
            content_encoding,
            if_none_match: None,
            body,
        });
    }

    /// Halves a batch the gateway called too large, and keeps both halves (§9).
    ///
    /// The halves are new batches with new identifiers, which is correct precisely because
    /// the original was never accepted: nothing derived from its id ever reached storage.
    fn split(&mut self, key: &str) {
        let Some(body) = self.host.store_get(key) else {
            return;
        };
        let Ok(mut envelope) = serde_json::from_slice::<serde_json::Value>(&body) else {
            self.host.store_delete(key);
            return;
        };
        let events = envelope
            .get_mut("events")
            .and_then(|e| e.as_array_mut())
            .map(std::mem::take)
            .unwrap_or_default();

        if events.len() < 2 {
            // One event too large for the gateway. No split can help, so it goes, loudly.
            self.host.store_delete(key);
            self.stats.batches_discarded += 1;
            self.host.log(
                Level::Error,
                "a single event exceeds the gateway's body limit and has been dropped; \
                 check for an oversized property",
            );
            return;
        }

        let half = events.len() / 2;
        let (first, second) = events.split_at(half);
        for part in [first, second] {
            let mut copy = envelope.clone();
            copy["batch_id"] = serde_json::Value::String(uuid_v4(&self.host));
            copy["events"] = serde_json::Value::Array(part.to_vec());
            if let Ok(encoded) = serde_json::to_vec(&copy) {
                self.write_spool(Priority::Critical, &encoded, part.len());
            }
        }
        // Only once both halves are down, so a crash in the middle leaves the original.
        self.host.store_delete(key);
        self.host.log(
            Level::Warn,
            &format!("a batch was too large and has been split into two of {half}"),
        );
    }

    fn log_rejection(&self, name: &str, rejected: Rejected) {
        let level = if self.config.debug {
            Level::Warn
        } else {
            Level::Debug
        };
        self.host
            .log(level, &format!("dropped event {name:?}: {rejected}"));
    }
}

/// The player-scoped context as it is persisted.
#[derive(serde::Serialize, serde::Deserialize)]
struct SavedContext {
    #[serde(default)]
    owner: Option<String>,
    #[serde(default)]
    values: BTreeMap<String, Value>,
}

/// Roughly what an event will weigh, for the size trigger.
///
/// An estimate on purpose: serializing every event to measure it would double the work on
/// the path §18 says must never be felt by the game, and the number only decides when to
/// start a batch.
fn estimate_bytes(event: &Event) -> usize {
    let mut total = event.name.len() + event.session_id.len() + 64;
    total += event.player_id.as_ref().map_or(0, String::len);
    for (key, value) in &event.properties {
        total += key.len() + 8;
        total += match value {
            Value::Str(v) => v.len(),
            _ => 8,
        };
    }
    total
}

/// gzip, because it is the encoding every engine can produce and every platform already has
/// (§8). `None` if compression failed, in which case the body goes as it is.
pub(crate) fn gzip(body: &[u8]) -> Option<Vec<u8>> {
    use flate2::{Compression, write::GzEncoder};
    use std::io::Write;

    let mut encoder = GzEncoder::new(Vec::new(), Compression::default());
    encoder.write_all(body).ok()?;
    encoder.finish().ok()
}

/// A granted consent block, for a binding that does not want to build one by hand.
pub fn granted(
    policy_version: &str,
    att_status: &str,
    gdpr_applies: bool,
    collected_at_ms: i64,
) -> State {
    State::Granted(Granted {
        policy_version: policy_version.to_owned(),
        att_status: att_status.to_owned(),
        gdpr_applies,
        collected_at: crate::time::rfc3339_ms(collected_at_ms),
    })
}

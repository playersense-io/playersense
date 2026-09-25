//! What a studio configures, and the defaults `docs/sdk/protocol.md` specifies.
//!
//! Every tunable here has a default that the protocol document justifies rather than
//! asserts. Where a number is derived from something the server does, the comment says so
//! and the derivation is the reason it should not be casually changed.

/// Limits the gateway enforces (§19.1), mirrored so the SDK can truncate locally.
///
/// Bytes not sent cost nothing, and an event dropped here can be logged with its name while
/// an event dropped at the gateway comes back as a line in `reasons`. These are constants
/// rather than settings on purpose: they are not this SDK's to choose, and a copy that
/// drifted from the server's would silently start sending what the server refuses.
pub mod limits {
    pub const MAX_EVENTS_PER_BATCH: usize = 1000;
    pub const MAX_BODY_BYTES: usize = 4 * 1024 * 1024;
    pub const MAX_PROPERTIES_PER_EVENT: usize = 64;
    pub const MAX_PROPERTY_KEY_CHARS: usize = 64;
    pub const MAX_STRING_VALUE_CHARS: usize = 1024;
    pub const MAX_EVENT_NAME_CHARS: usize = 96;

    /// How far behind the server will accept a timestamp before clamping it (§19.2).
    ///
    /// This is where the spool's age bound comes from. A batch older than this uploads
    /// events whose timestamps the server silently rewrites, so dropping it is the honest
    /// act — see [`super::Config::spool_max_age_ms`].
    pub const SKEW_PAST_MS: i64 = 7 * 24 * 60 * 60 * 1000;
}

/// The wire version this build speaks (§17).
pub const SCHEMA_VERSION: u32 = 1;

/// Where a studio's settings and the protocol's defaults meet.
#[derive(Clone, Debug)]
pub struct Config {
    /// The publishable project key. Travels in the `Authorization` header, never in a body
    /// (§17.1).
    pub api_key: String,

    /// The ingest endpoint. A value the host has stored wins over it, so this is a starting
    /// point rather than a fixed address (§9).
    pub endpoint: String,

    /// The configuration endpoint, where flags and LiveOps events come from (§42).
    ///
    /// Its own setting rather than a path derived from `endpoint`, because the two were
    /// separate services and may be again: one takes a million writes a minute and the
    /// other answers a question once a session. Empty turns the whole feature off — every
    /// flag falls back and no event is ever active — which is what a studio using only
    /// analytics wants and should not have to pay a request for.
    pub config_endpoint: String,

    /// development, staging or production. Informational: the key is the authority, and a
    /// batch claiming another environment is refused (§76).
    pub environment: String,

    pub sdk_name: String,
    pub sdk_version: String,
    pub app_version: String,
    pub app_build: String,
    pub platform: String,
    pub os_version: String,
    pub locale: String,

    /// How long a batch of ordinary events waits before it is sent. Critical events — a
    /// session start, anything carrying revenue — do not wait for it (§7).
    pub flush_interval_ms: i64,

    /// How often the configuration is asked for again.
    ///
    /// Fifteen minutes because that is the granularity a studio can reason about: a LiveOps
    /// event published now reaches a session already in progress within a quarter of an hour,
    /// and a session shorter than that got its answer when it started. Polling harder would
    /// buy a finer number nobody can act on, per device, forever.
    ///
    /// It is a floor rather than a schedule: bringing the app to the foreground asks
    /// immediately, because a game resumed after three hours is the case where the answer is
    /// most likely to have moved.
    pub config_refresh_interval_ms: i64,

    /// Whether reading a flag records that this player saw it (§44).
    ///
    /// On, because a flag nobody can measure is a flag nobody can learn from: this is what
    /// connects a rollout to retention and revenue. Deduplicated to one event per flag per
    /// config version per session, so the cost is proportional to how many flags a game has
    /// rather than to how often it reads them.
    ///
    /// It is still a cost — those events count against a studio's allowance like any other —
    /// which is why it can be turned off. A studio using flags as plain remote configuration
    /// and running no experiments gets nothing from them.
    pub exposure_events: bool,

    /// The most exposures one session will record.
    ///
    /// A ceiling on the surprise rather than a limit anybody should reach: a hundred distinct
    /// flags read in one session is a game whose exposure stream rivals its analytics, and a
    /// studio should find that out from a log line rather than from an invoice.
    pub max_exposures_per_session: usize,

    /// Events that force a batch. Below the gateway's hard cap of 1000 so a burst arriving
    /// mid-batch still fits.
    pub max_events_per_batch: usize,

    /// Serialized bytes that force a batch, before compression.
    pub max_batch_bytes: usize,

    /// How long in the background before the next event starts a new session (§4).
    pub session_timeout_ms: i64,

    /// How long a spooled batch may wait before it is dropped rather than sent.
    ///
    /// Seven days, and it is derived rather than chosen: the gateway clamps `event_time` to
    /// [`limits::SKEW_PAST_MS`] in the past, so a batch older than that uploads events whose
    /// timestamps are silently rewritten. Dropping old data beats reporting a fiction.
    pub spool_max_age_ms: i64,

    /// How much the spool may hold before eviction starts (§6).
    pub spool_max_bytes: usize,
    /// How many batches the spool may hold, which also bounds the drain after a long outage.
    pub spool_max_batches: usize,

    /// How many events may be held in memory while consent is unknown (§5).
    ///
    /// In memory and never on disk: a spool is a copy on disk of data collected under no
    /// basis.
    pub preconsent_max_events: usize,

    /// Exponential backoff with full jitter (§10).
    pub backoff_base_ms: i64,
    pub backoff_cap_ms: i64,

    /// Consecutive transient failures before the SDK stops attempting until the app next
    /// comes to the foreground. A dead endpoint must not cost a player their battery.
    pub cold_after_failures: u32,

    /// Whether to gzip the body. Disabled for the process on a 415 (§9).
    pub compression: bool,

    /// Logs every dropped event and every refusal at `Info` rather than `Debug`.
    pub debug: bool,

    // ---- logs, exceptions and crashes (§15) ----------------------------------------------
    /// Whether the log stream runs at all. **Off by default**: turning it on changes what a
    /// studio must declare in its store listings (App Privacy "Crash Data", Play Data safety
    /// "Crash logs"), so an SDK update must never turn it on behind a studio's back.
    pub logs_enabled: bool,
    /// The least serious level sent as a record. Quieter lines become breadcrumbs.
    pub log_min_level: crate::logs::LogLevel,
    /// The least serious level kept as a breadcrumb.
    pub log_breadcrumb_level: crate::logs::LogLevel,
    /// How many breadcrumbs an error carries.
    pub log_max_breadcrumbs: usize,
    /// How long ordinary records wait before a batch is sealed. Crashes do not wait.
    pub log_flush_interval_ms: i64,
    pub log_max_records_per_batch: usize,
    pub log_max_batch_bytes: usize,
    /// The most records one device sends a minute, crashes aside.
    pub log_max_per_minute: u32,
    pub log_spool_max_bytes: usize,
    pub log_spool_max_batches: usize,
    pub log_spool_max_age_ms: i64,
    /// Records held in memory while the player has not answered.
    pub log_preconsent_max: usize,
    /// Emails, tokens, card numbers, addresses and account names in paths, taken out on the
    /// device. On by default; turning it off is the studio's decision to make in writing.
    pub log_scrub: bool,
    /// Keep a crash on the device, unsent, while the player has not answered.
    pub log_hold_crashes: bool,
    /// Report a run that ended in the foreground without going to the background or quitting.
    pub log_abnormal_exits: bool,
    /// The hardware model (`iPhone15,2`, `SM-S918B`): shared by millions of devices, so it
    /// describes a product and not a person.
    pub device_model: String,
    /// A game for children: no player id and no free-form attributes on a record.
    pub child_directed: bool,
}

impl Config {
    /// A configuration carrying the protocol's defaults.
    pub fn new(api_key: impl Into<String>, endpoint: impl Into<String>) -> Self {
        Self {
            api_key: api_key.into(),
            endpoint: endpoint.into(),
            config_endpoint: String::new(),
            environment: "production".into(),

            sdk_name: "unknown".into(),
            sdk_version: env!("CARGO_PKG_VERSION").into(),
            app_version: "0.0.0".into(),
            app_build: String::new(),
            platform: "unknown".into(),
            os_version: String::new(),
            locale: String::new(),

            flush_interval_ms: 30_000,
            config_refresh_interval_ms: 15 * 60 * 1_000,
            exposure_events: true,
            max_exposures_per_session: 100,
            max_events_per_batch: 250,
            max_batch_bytes: 128 * 1024,
            session_timeout_ms: 30_000,

            spool_max_age_ms: limits::SKEW_PAST_MS,
            spool_max_bytes: 5 * 1024 * 1024,
            spool_max_batches: 500,
            preconsent_max_events: 200,

            backoff_base_ms: 1_000,
            backoff_cap_ms: 5 * 60 * 1_000,
            cold_after_failures: 8,

            compression: true,
            debug: false,

            logs_enabled: false,
            log_min_level: crate::logs::LogLevel::Warn,
            log_breadcrumb_level: crate::logs::LogLevel::Info,
            log_max_breadcrumbs: 50,
            log_flush_interval_ms: 15_000,
            log_max_records_per_batch: 200,
            log_max_batch_bytes: 64 * 1024,
            log_max_per_minute: 120,
            log_spool_max_bytes: 2 * 1024 * 1024,
            log_spool_max_batches: 100,
            log_spool_max_age_ms: 30 * 24 * 60 * 60 * 1000,
            log_preconsent_max: 50,
            log_scrub: true,
            log_hold_crashes: true,
            log_abnormal_exits: true,
            device_model: String::new(),
            child_directed: false,
        }
    }

    /// The address a batch is posted to.
    pub fn batches_url(&self) -> String {
        format!("{}/v1/batches", self.endpoint.trim_end_matches('/'))
    }

    /// Clamps anything that would make the SDK misbehave rather than refusing to start.
    ///
    /// Refusing would mean a game that does not launch because an analytics setting was
    /// mistyped, which §2 rules out. A value that cannot work is replaced and said out loud.
    pub fn sanitized(mut self) -> Self {
        self.max_events_per_batch = self
            .max_events_per_batch
            .clamp(1, limits::MAX_EVENTS_PER_BATCH);
        self.max_batch_bytes = self.max_batch_bytes.clamp(1024, limits::MAX_BODY_BYTES);
        self.flush_interval_ms = self.flush_interval_ms.clamp(100, 600_000);
        // One second is the floor, because the endpoint answers per player and a game
        // polling faster than that is a game asking the same question of the same server
        // several times a frame.
        self.max_exposures_per_session = self.max_exposures_per_session.clamp(1, 10_000);
        self.config_refresh_interval_ms = self
            .config_refresh_interval_ms
            .clamp(1_000, 24 * 60 * 60 * 1_000);
        self.session_timeout_ms = self.session_timeout_ms.clamp(1_000, 24 * 3_600_000);
        // Never longer than the server's skew window: past it, every event is rewritten.
        self.spool_max_age_ms = self.spool_max_age_ms.clamp(60_000, limits::SKEW_PAST_MS);
        self.spool_max_batches = self.spool_max_batches.max(1);
        self.backoff_base_ms = self.backoff_base_ms.clamp(100, 60_000);
        self.backoff_cap_ms = self.backoff_cap_ms.clamp(self.backoff_base_ms, 3_600_000);
        self.log_max_breadcrumbs = self.log_max_breadcrumbs.min(100);
        self.log_flush_interval_ms = self.log_flush_interval_ms.clamp(1_000, 600_000);
        self.log_max_records_per_batch = self.log_max_records_per_batch.clamp(1, 1000);
        self.log_max_batch_bytes = self.log_max_batch_bytes.clamp(4 * 1024, 1024 * 1024);
        self.log_max_per_minute = self.log_max_per_minute.clamp(1, 10_000);
        self.log_spool_max_batches = self.log_spool_max_batches.max(1);
        self.log_spool_max_age_ms = self
            .log_spool_max_age_ms
            .clamp(60_000, 30 * 24 * 60 * 60 * 1000);
        self.log_preconsent_max = self.log_preconsent_max.min(1000);
        self.device_model = self.device_model.chars().take(64).collect();
        self.cold_after_failures = self.cold_after_failures.max(1);
        self
    }
}

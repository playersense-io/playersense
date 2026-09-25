//! The exported functions.
//!
//! Every one of them is a single call inside [`with_client`], so the surface is uniform and
//! the interesting rules — reentrancy, poisoning, panic capture — are written once and
//! auditable in one place.

use std::cell::RefCell;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::Ordering;

use playersense_core::consent::State;
use playersense_core::context::{self, Scope};
use playersense_core::event;
use playersense_core::{Client, Config, Recorded, granted};

use crate::convert::*;
use crate::handle;
use crate::host::{FfiHost, response};
use crate::types::*;

thread_local! {
    /// The message for the most recent failure on this thread.
    ///
    /// Valid only immediately after a non-`PS_OK` return: the next call clears it. It exists
    /// so a refusal can carry a sentence — "that namespace is reserved: it is resolved by
    /// PlayerSense from the request address" — without every binding reproducing the table
    /// that sentence comes from, which is the "eleven implementations" §15 exists to prevent.
    static LAST_ERROR: RefCell<String> = const { RefCell::new(String::new()) };
}

fn set_last_error(message: impl Into<String>) {
    LAST_ERROR.with(|slot| *slot.borrow_mut() = message.into());
}

fn clear_last_error() {
    LAST_ERROR.with(|slot| slot.borrow_mut().clear());
}

/// Runs `f` against a client, applying every boundary rule.
///
/// In order: resolve the handle, refuse a poisoned client, refuse reentrancy, take the lock,
/// and catch a panic rather than letting it cross into C — where it would abort the game.
fn with_client<F>(handle: u64, f: F) -> i32
where
    F: FnOnce(&mut Client<FfiHost>) -> i32,
{
    clear_last_error();

    let Some(entry) = handle::lookup(handle) else {
        return PS_ERR_BAD_HANDLE;
    };
    if entry.poisoned.load(Ordering::Relaxed) {
        return PS_ERR_POISONED;
    }
    // A host callback that called back in would deadlock on the lock below. Refusing is a
    // documented no-op; freezing the game is not (§18).
    if handle::is_active(handle) {
        set_last_error("a host callback called back into the same client");
        return PS_ERR_REENTRANT;
    }

    let _active = handle::Active::enter(handle);
    let mut client = entry.client.lock().unwrap_or_else(|p| p.into_inner());

    match catch_unwind(AssertUnwindSafe(|| f(&mut client))) {
        Ok(status) => status,
        Err(payload) => {
            // The client's invariants are now unknown — it may have panicked half way
            // through sealing a batch — so it is retired rather than driven further. The
            // game keeps running with analytics off, which is the right reading of §18.
            entry.poisoned.store(true, Ordering::Relaxed);
            let message = payload
                .downcast_ref::<&str>()
                .map(|s| (*s).to_owned())
                .or_else(|| payload.downcast_ref::<String>().cloned())
                .unwrap_or_else(|| "a panic with no message".to_owned());
            set_last_error(format!(
                "the client panicked and has been disabled: {message}"
            ));
            PS_ERR_POISONED
        }
    }
}

fn scope_of(raw: i32) -> Result<Scope, i32> {
    match raw {
        PS_SCOPE_SESSION => Ok(Scope::Session),
        PS_SCOPE_PLAYER => Ok(Scope::Player),
        _ => Err(PS_ERR_INVALID_ARGUMENT),
    }
}

fn event_rejection(rejected: &event::Rejected) -> i32 {
    set_last_error(rejected.to_string());
    match rejected {
        event::Rejected::Name => PS_REJECTED_EVENT_NAME,
        event::Rejected::TooManyProperties => PS_REJECTED_TOO_MANY_PROPERTIES,
    }
}

fn context_rejection(rejected: &context::Rejected) -> i32 {
    set_last_error(rejected.to_string());
    match rejected {
        context::Rejected::Key => PS_REJECTED_CONTEXT_KEY,
        context::Rejected::Reserved(_) => PS_REJECTED_CONTEXT_RESERVED,
        context::Rejected::Full => PS_REJECTED_CONTEXT_FULL,
    }
}

// ---- library level ----------------------------------------------------------

/// Writes the defaults into a configuration.
///
/// Start here rather than from a zeroed struct: twenty-four fields is a lot to populate
/// correctly, and a forgotten `flush_interval_ms` would clamp to the minimum and flush ten
/// times a second.
///
/// # Safety
/// `out` must point at a writable `PsConfig`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_config_default(out: *mut PsConfig) {
    if out.is_null() {
        return;
    }
    let defaults = Config::new("", "").sanitized();
    let empty = PsStr {
        ptr: std::ptr::null(),
        len: 0,
    };

    unsafe {
        *out = PsConfig {
            struct_size: size_of::<PsConfig>() as u32,
            _pad: 0,
            api_key: empty,
            endpoint: empty,
            environment: empty,
            sdk_name: empty,
            sdk_version: empty,
            app_version: empty,
            app_build: empty,
            platform: empty,
            os_version: empty,
            locale: empty,
            config_endpoint: empty,
            flush_interval_ms: defaults.flush_interval_ms,
            config_refresh_interval_ms: defaults.config_refresh_interval_ms,
            max_events_per_batch: defaults.max_events_per_batch as i64,
            max_batch_bytes: defaults.max_batch_bytes as i64,
            session_timeout_ms: defaults.session_timeout_ms,
            spool_max_age_ms: defaults.spool_max_age_ms,
            spool_max_bytes: defaults.spool_max_bytes as i64,
            spool_max_batches: defaults.spool_max_batches as i64,
            preconsent_max_events: defaults.preconsent_max_events as i64,
            backoff_base_ms: defaults.backoff_base_ms,
            backoff_cap_ms: defaults.backoff_cap_ms,
            exposure_events: i32::from(defaults.exposure_events),
            max_exposures_per_session: defaults.max_exposures_per_session as i32,
            cold_after_failures: defaults.cold_after_failures as i32,
            compression: i32::from(defaults.compression),
            debug: i32::from(defaults.debug),
            _pad2: 0,
            device_model: empty,
            logs_enabled: i32::from(defaults.logs_enabled),
            log_min_level: -1,
            log_breadcrumb_level: -1,
            log_max_per_minute: defaults.log_max_per_minute as i32,
            log_flush_interval_ms: defaults.log_flush_interval_ms,
            log_scrub: i32::from(defaults.log_scrub),
            log_abnormal_exits: i32::from(defaults.log_abnormal_exits),
            log_hold_crashes: i32::from(defaults.log_hold_crashes),
            child_directed: i32::from(defaults.child_directed),
        };
    }
}

/// Confirms the binding and the binary agree about every layout.
///
/// **Call this before anything else.** The ABI version catches a deliberate change; this
/// catches the accident — a `bool` marshalled as one byte instead of four, a `Pack` mismatch,
/// a field reordered in a refactor — where both sides still report the same version. It is
/// the guard that actually prevents heap corruption, and the one people skip.
///
/// # Safety
/// `sizes` must point at a readable [`PsAbiSizes`].
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_abi_selfcheck(sizes: *const PsAbiSizes) -> i32 {
    clear_last_error();
    if sizes.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    // The version first, read alone: a binding from another ABI passes a struct of another
    // length, and reading the whole of ours from it would read past the end of theirs.
    let version = unsafe { std::ptr::addr_of!((*sizes).abi_version).read_unaligned() };
    if version != crate::PS_ABI_VERSION {
        set_last_error(format!(
            "abi_version: the binding says {version} and this binary says {}",
            crate::PS_ABI_VERSION
        ));
        return PS_ERR_ABI_MISMATCH;
    }
    let theirs = unsafe { *sizes };

    let expected = [
        ("abi_version", theirs.abi_version, crate::PS_ABI_VERSION),
        ("PsHost", theirs.sizeof_host, size_of::<PsHost>() as u32),
        (
            "PsConfig",
            theirs.sizeof_config,
            size_of::<PsConfig>() as u32,
        ),
        ("PsStats", theirs.sizeof_stats, size_of::<PsStats>() as u32),
        ("PsStr", theirs.sizeof_str, size_of::<PsStr>() as u32),
        ("PsValue", theirs.sizeof_value, size_of::<PsValue>() as u32),
        (
            "PsProperty",
            theirs.sizeof_property,
            size_of::<PsProperty>() as u32,
        ),
        (
            "PsRequest",
            theirs.sizeof_request,
            size_of::<PsRequest>() as u32,
        ),
        (
            "PsLogRecord",
            theirs.sizeof_log_record,
            size_of::<PsLogRecord>() as u32,
        ),
        ("PsFrame", theirs.sizeof_frame, size_of::<PsFrame>() as u32),
        (
            "PsAttribute",
            theirs.sizeof_attribute,
            size_of::<PsAttribute>() as u32,
        ),
        (
            "PsLogStats",
            theirs.sizeof_log_stats,
            size_of::<PsLogStats>() as u32,
        ),
    ];
    for (name, theirs, ours) in expected {
        if theirs != ours {
            set_last_error(format!(
                "{name}: the binding says {theirs} and this binary says {ours}"
            ));
            return PS_ERR_ABI_MISMATCH;
        }
    }
    PS_OK
}

/// Copies the message for the most recent failure on this thread.
///
/// Valid only immediately after a non-`PS_OK` return; the next call clears it. Pass
/// `(NULL, 0)` to ask for the required length.
///
/// # Safety
/// `out` must be null or point at `cap` writable bytes; `out_len` must be null or writable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_last_error_message(out: *mut u8, cap: i64, out_len: *mut i64) -> i32 {
    LAST_ERROR.with(|slot| unsafe { fill(slot.borrow().as_bytes(), out, cap, out_len) })
}

// ---- lifecycle --------------------------------------------------------------

/// Creates a client. Performs no network operation.
///
/// # Safety
/// `host` and `config` must point at readable, fully-initialized structs whose `struct_size`
/// is set, and whose strings satisfy the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_create(
    host: *const PsHost,
    config: *const PsConfig,
    out_handle: *mut u64,
) -> i32 {
    clear_last_error();
    if host.is_null() || config.is_null() || out_handle.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    unsafe { *out_handle = 0 };

    let result = catch_unwind(AssertUnwindSafe(|| {
        let host = match FfiHost::new(unsafe { &*host }) {
            Ok(host) => host,
            Err(code) => {
                set_last_error("the host vtable is the wrong size or is missing a callback");
                return Err(code);
            }
        };
        let config = unsafe { crate::convert::config(&*config) }?;
        Ok(handle::create(Client::new(host, config)))
    }));

    match result {
        Ok(Ok(created)) => {
            unsafe { *out_handle = created };
            PS_OK
        }
        Ok(Err(code)) => code,
        Err(_) => {
            set_last_error("the client panicked while starting");
            PS_ERR_POISONED
        }
    }
}

/// Retires a client. Safe on a poisoned one, and safe to call twice.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_destroy(client: u64) -> i32 {
    clear_last_error();
    if handle::destroy(client) {
        PS_OK
    } else {
        PS_ERR_BAD_HANDLE
    }
}

/// Reports whether a panic was caught inside this client, leaving it inert.
///
/// # Safety
/// `out` must point at a writable `int32_t`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_is_poisoned(client: u64, out: *mut i32) -> i32 {
    clear_last_error();
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    let Some(entry) = handle::lookup(client) else {
        return PS_ERR_BAD_HANDLE;
    };
    unsafe { *out = i32::from(entry.poisoned.load(Ordering::Relaxed)) };
    PS_OK
}

// ---- introspection ----------------------------------------------------------

/// Copies the installation identifier. Pass `(NULL, 0)` to ask for the required length.
///
/// # Safety
/// As [`ps_last_error_message`].
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_install_id(
    client: u64,
    out: *mut u8,
    cap: i64,
    out_len: *mut i64,
) -> i32 {
    with_client(client, |c| unsafe {
        fill(c.install_id().as_bytes(), out, cap, out_len)
    })
}

/// Fills in what the SDK has done.
///
/// # Safety
/// `out` must point at a writable `PsStats` whose `struct_size` is set.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_stats(client: u64, out: *mut PsStats) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        let stats = c.stats();
        unsafe {
            *out = PsStats {
                struct_size: size_of::<PsStats>() as u32,
                _pad: 0,
                events_tracked: stats.events_tracked,
                events_rejected: stats.events_rejected,
                events_without_consent: stats.events_without_consent,
                events_evicted: stats.events_evicted,
                batches_spooled: stats.batches_spooled,
                batches_delivered: stats.batches_delivered,
                batches_discarded: stats.batches_discarded,
                requests_failed: stats.requests_failed,
                events_accepted_by_server: stats.events_accepted_by_server,
                events_dropped_by_server: stats.events_dropped_by_server,
            };
        }
        PS_OK
    })
}

/// The context revision, which rises on every change. A configuration refresh debounces on it.
///
/// # Safety
/// `out` must point at a writable `uint64_t`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_context_revision(client: u64, out: *mut u64) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        unsafe { *out = c.context_revision() };
        PS_OK
    })
}

// ---- what the game calls ----------------------------------------------------

/// Attaches an actor to every subsequent event. `{ NULL, 0 }` signs out.
///
/// Does not flush: `player_id` travels per event, so a batch may straddle a sign-in.
///
/// # Safety
/// `player_id` must satisfy the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_set_player(client: u64, player_id: PsStr) -> i32 {
    with_client(client, |c| {
        let id = if is_absent(player_id) {
            None
        } else {
            match unsafe { str_strict(player_id) } {
                Ok(id) => Some(id.to_owned()),
                Err(code) => return code,
            }
        };
        c.set_player(id);
        PS_OK
    })
}

/// Records that the player agreed to analytics collection.
///
/// Three functions rather than one tagged call, because only one of the three states carries
/// data. A single entry point with four parameters that two states ignore is the shape that
/// eventually gets called with stale values.
///
/// # Safety
/// The strings must satisfy the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_set_consent_granted(
    client: u64,
    policy_version: PsStr,
    att_status: PsStr,
    gdpr_applies: i32,
    collected_at_ms: i64,
) -> i32 {
    with_client(client, |c| {
        let policy = match unsafe { str_strict(policy_version) } {
            Ok(v) => v,
            Err(code) => return code,
        };
        let att = match unsafe { str_strict(att_status) } {
            Ok(v) => v,
            Err(code) => return code,
        };
        c.set_consent(granted(policy, att, gdpr_applies != 0, collected_at_ms));
        PS_OK
    })
}

/// Records that the player refused. Collection stops and the buffer is discarded.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_set_consent_denied(client: u64) -> i32 {
    with_client(client, |c| {
        c.set_consent(State::Denied);
        PS_OK
    })
}

/// Describes the player or the moment, in the studio's own vocabulary.
///
/// Namespaces PlayerSense resolves for itself — `geo.`, `network.`, `account.` and the rest —
/// are refused, and `ps_last_error_message` names the owner.
///
/// # Safety
/// `key` and `value` must satisfy the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_set_context(
    client: u64,
    scope: i32,
    key: PsStr,
    value: *const PsValue,
) -> i32 {
    if value.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        let scope = match scope_of(scope) {
            Ok(s) => s,
            Err(code) => return code,
        };
        let key = match unsafe { str_strict(key) } {
            Ok(k) => k,
            Err(code) => return code,
        };
        let (value, _) = match unsafe { crate::convert::value(&*value) } {
            Ok(v) => v,
            Err(code) => return code,
        };
        match c.set_context(scope, key, value) {
            Ok(()) => PS_OK,
            Err(rejected) => context_rejection(&rejected),
        }
    })
}

/// Removes a context value.
///
/// # Safety
/// `key` must satisfy the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_remove_context(client: u64, scope: i32, key: PsStr) -> i32 {
    with_client(client, |c| {
        let scope = match scope_of(scope) {
            Ok(s) => s,
            Err(code) => return code,
        };
        let key = match unsafe { str_strict(key) } {
            Ok(k) => k,
            Err(code) => return code,
        };
        c.remove_context(scope, key);
        PS_OK
    })
}

/// Records an event. Never blocks and never throws.
///
/// # Safety
/// `name` must satisfy the borrowing rule, and `props` must be null or point at `prop_count`
/// readable [`PsProperty`] values.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_track(
    client: u64,
    name: PsStr,
    props: *const PsProperty,
    prop_count: i64,
) -> i32 {
    unsafe {
        track_inner(
            client,
            name,
            0,
            PsStr {
                ptr: std::ptr::null(),
                len: 0,
            },
            props,
            prop_count,
        )
    }
}

/// Records an event carrying money. Analytics about a purchase, not the purchase itself:
/// the authoritative record comes from the studio's backend verifying the receipt.
///
/// # Safety
/// As [`ps_client_track`].
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_track_revenue(
    client: u64,
    name: PsStr,
    revenue_micros: i64,
    currency: PsStr,
    props: *const PsProperty,
    prop_count: i64,
) -> i32 {
    unsafe { track_inner(client, name, revenue_micros, currency, props, prop_count) }
}

/// # Safety
/// As [`ps_client_track`].
unsafe fn track_inner(
    client: u64,
    name: PsStr,
    revenue_micros: i64,
    currency: PsStr,
    props: *const PsProperty,
    prop_count: i64,
) -> i32 {
    with_client(client, |c| {
        let name = match unsafe { str_strict(name) } {
            Ok(n) => n,
            Err(code) => return code,
        };
        let (properties, repaired) = match unsafe { properties(props, prop_count) } {
            Ok(p) => p,
            Err(code) => return code,
        };
        if !repaired.is_empty() {
            // Repaired rather than refused: losing a whole event to one mangled string is
            // worse under §18, and Mono can hand over a lone surrogate. Said out loud
            // because silently changing data is its own sin.
            set_last_error(format!(
                "invalid UTF-8 was repaired in {}",
                repaired.join(", ")
            ));
        }

        let result = if revenue_micros == 0 && is_absent(currency) {
            c.track(name, properties)
        } else {
            let currency = match unsafe { str_strict(currency) } {
                Ok(c) => c,
                Err(code) => return code,
            };
            c.track_revenue(name, revenue_micros, currency, properties)
        };
        match result {
            Ok(Recorded::Queued) => PS_OK,
            Ok(Recorded::AwaitingConsent) => PS_HELD_FOR_CONSENT,
            Ok(Recorded::Declined) => PS_DECLINED_BY_CONSENT,
            Err(rejected) => event_rejection(&rejected),
        }
    })
}

/// Seals whatever is queued and tries to send.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_flush(client: u64) -> i32 {
    with_client(client, |c| {
        c.flush();
        PS_OK
    })
}

/// The app came back to the front.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_foreground(client: u64) -> i32 {
    with_client(client, |c| {
        c.foreground();
        PS_OK
    })
}

/// The app went away. Spools first, then attempts the network.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_background(client: u64) -> i32 {
    with_client(client, |c| {
        c.background();
        PS_OK
    })
}

/// Called regularly — a frame loop, a timer. Cheap when nothing is due.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_tick(client: u64) -> i32 {
    with_client(client, |c| {
        c.tick();
        PS_OK
    })
}

/// One last bounded attempt. Whatever does not go stays for the next launch.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_shutdown(client: u64) -> i32 {
    with_client(client, |c| {
        c.shutdown();
        PS_OK
    })
}

/// Answers a request the host was asked to perform.
///
/// Must be called exactly once per request, **including** when it never reached a server: pass
/// `status = 0` for a timeout, a refused connection or a cancellation. A request the core
/// never hears about again is a batch that never retries.
///
/// # Safety
/// `body` must be null or point at `body_len` readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_on_response(
    client: u64,
    request_id: u64,
    status: u16,
    retry_after_ms: i64,
    body: *const u8,
    body_len: i64,
) -> i32 {
    with_client(client, |c| {
        c.on_response(unsafe { response(request_id, status, retry_after_ms, body, body_len) });
        PS_OK
    })
}

// ---- live configuration (§41, §42, §45) -------------------------------------

/// Asks the server what this player gets, now.
///
/// A no-op when a request is already outstanding or no configuration endpoint was set. The
/// core also asks on its own — on the refresh interval and when the app comes to the front —
/// so a binding only needs this when the game has just changed something it segments on.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_refresh_config(client: u64) -> i32 {
    with_client(client, |c| {
        c.refresh_config();
        PS_OK
    })
}

/// Restores the document a previous run persisted.
///
/// Flags only, in effect: no clock anchor comes off disk, so nothing is reported active until
/// the server has been heard from this run. Called once at start-up, before the first frame
/// asks about a flag, so that a launch with no network answers from the last good document
/// rather than from every fallback.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_restore_config(client: u64) -> i32 {
    with_client(client, |c| {
        c.restore_config();
        PS_OK
    })
}

/// The published version behind the current answers, or 0 when none has been fetched.
///
/// # Safety
/// `out` must be a writable `uint32_t`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_config_version(client: u64, out: *mut u32) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        unsafe { *out = c.config_version() };
        PS_OK
    })
}

/// Whether the server has been heard from in this process.
///
/// What makes a window meaningful: until this is 1, every event list is empty, because a
/// window measured against a clock nobody has checked is a window a player can open early.
///
/// # Safety
/// `out` must be a writable `int32_t`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_config_anchored(client: u64, out: *mut i32) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        unsafe { *out = i32::from(c.config_anchored()) };
        PS_OK
    })
}

/// A boolean flag. `PS_NOT_FOUND` when the project has no flag with that key.
///
/// The value is untouched on anything but `PS_OK`, so a binding can initialise `out` with the
/// game's own fallback and read it back regardless of the result.
///
/// **Records an exposure** (§44), at most once per flag per config version per session. That
/// is why this takes a client rather than being a pure lookup: reading a flag is the moment a
/// player is in an experiment, and a flag nobody can measure is a flag nobody can learn from.
/// A binding inspecting flags rather than deciding with them — a debug overlay — should not
/// use these.
///
/// # Safety
/// `key` must be valid UTF-8 of its stated length; `out` must be a writable `int32_t`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_flag_bool(client: u64, key: PsStr, out: *mut i32) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    let Ok(key) = (unsafe { str_strict(key) }) else {
        return PS_ERR_INVALID_ARGUMENT;
    };
    with_client(client, |c| match c.read_flag_bool(key) {
        Some(value) => {
            unsafe { *out = i32::from(value) };
            PS_OK
        }
        // A flag of another kind is not this flag. Coercing "0" or 0.0 into false would let a
        // studio's typo change what a game does rather than show up as a fallback.
        None => PS_NOT_FOUND,
    })
}

/// A numeric flag. `PS_NOT_FOUND` when the project has no number with that key.
///
/// # Safety
/// `key` must be valid UTF-8 of its stated length; `out` must be a writable `double`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_flag_number(client: u64, key: PsStr, out: *mut f64) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    let Ok(key) = (unsafe { str_strict(key) }) else {
        return PS_ERR_INVALID_ARGUMENT;
    };
    with_client(client, |c| match c.read_flag_number(key) {
        Some(value) => {
            unsafe { *out = value };
            PS_OK
        }
        None => PS_NOT_FOUND,
    })
}

/// A string flag, or a JSON one as the text it was published as.
///
/// Both kinds answer here because both are text to a caller: a game reading a `json` flag
/// wants the document to parse, and giving it a second function to remember would buy nothing.
///
/// Follows the usual out-buffer contract: `out_len` is always the length, and a buffer too
/// small returns `PS_ERR_BUFFER_TOO_SMALL` having written nothing, so a caller sizes once and
/// calls again.
///
/// # Safety
/// `key` must be valid UTF-8; `out` must be writable for `cap` bytes; `out_len` writable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_flag_text(
    client: u64,
    key: PsStr,
    out: *mut u8,
    cap: i64,
    out_len: *mut i64,
) -> i32 {
    let Ok(key) = (unsafe { str_strict(key) }) else {
        return PS_ERR_INVALID_ARGUMENT;
    };
    with_client(client, |c| match c.read_flag_text(key) {
        Some(value) => unsafe { fill(value.as_bytes(), out, cap, out_len) },
        None => {
            if !out_len.is_null() {
                unsafe { *out_len = 0 };
            }
            PS_NOT_FOUND
        }
    })
}

/// The LiveOps events on right now, as a JSON document.
///
/// One call rather than a count and an accessor per field, because a binding then parses once
/// with whatever its platform already has instead of making four calls per event across the
/// ABI. LiveOps is read when a screen opens, not per frame, so the parse is not on a hot path.
///
/// The shape, which is stable and is what a binding's model declares:
///
/// ```json
/// {"anchored":true,"version":7,"server_time_ms":1789560000000,"events":[
///   {"key":"happy_hour","kind":"offer","priority":10,
///    "starts_at_ms":…,"ends_at_ms":…,"next_starts_at_ms":…,
///    "payload":"{\"multiplier\":2}"}]}
/// ```
///
/// `payload` is a **string** holding the studio's own JSON, not a nested object. A binding's
/// JSON model cannot declare a type for something PlayerSense does not understand, and the
/// game parses it with whatever it already uses for its own data.
///
/// `events` is empty until the server has been heard from this run — see
/// [`ps_client_config_anchored`].
///
/// # Safety
/// `out` must be writable for `cap` bytes; `out_len` writable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_liveops_active(
    client: u64,
    out: *mut u8,
    cap: i64,
    out_len: *mut i64,
) -> i32 {
    with_client(client, |c| {
        let rendered = c.liveops_active_json();
        unsafe { fill(rendered.as_bytes(), out, cap, out_len) }
    })
}

/// The events starting soon, soonest first. Same shape as [`ps_client_liveops_active`].
///
/// # Safety
/// `out` must be writable for `cap` bytes; `out_len` writable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_liveops_upcoming(
    client: u64,
    out: *mut u8,
    cap: i64,
    out_len: *mut i64,
) -> i32 {
    with_client(client, |c| {
        let rendered = c.liveops_upcoming_json();
        unsafe { fill(rendered.as_bytes(), out, cap, out_len) }
    })
}

// ---- logs, exceptions and crashes (§15) ------------------------------------------------------

/// Reads text from a record: never refused, because a log line with a bad byte in it is still
/// the log line somebody needs. Invalid UTF-8 is replaced, not rejected.
///
/// # Safety
/// As [`str_lossy`].
unsafe fn text(s: PsStr) -> Result<String, i32> {
    unsafe { str_lossy(s) }.map(|(text, _)| text)
}

/// Reads a borrowed array, `{ NULL, 0 }` being empty.
///
/// # Safety
/// `ptr` must be null with `len == 0`, or point at `len` readable values.
unsafe fn array<'a, T>(ptr: *const T, len: i64) -> Result<&'a [T], i32> {
    if len < 0 || (ptr.is_null() && len != 0) {
        return Err(PS_ERR_INVALID_ARGUMENT);
    }
    if len == 0 {
        return Ok(&[]);
    }
    Ok(unsafe { std::slice::from_raw_parts(ptr, len as usize) })
}

/// # Safety
/// `raw` must be a readable, fully-initialized [`PsLogRecord`] whose arrays and strings obey
/// the borrowing rule.
unsafe fn log_record(raw: &PsLogRecord) -> Result<playersense_core::LogRecord, i32> {
    use playersense_core::{LogException, LogLevel, LogRecord, StackFrame};

    if (raw.struct_size as usize) < size_of::<PsLogRecord>() {
        return Err(PS_ERR_ABI_MISMATCH);
    }
    let mut record = LogRecord {
        level: LogLevel::from_i32(raw.level),
        message: unsafe { text(raw.message)? },
        logger: unsafe { text(raw.logger)? },
        crash: raw.crash != 0,
        time_ms: (raw.time_ms > 0).then_some(raw.time_ms),
        ..LogRecord::default()
    };
    if !is_absent(raw.exception_type) {
        let frames = unsafe { array(raw.frames, raw.frame_count)? };
        let mut read = Vec::with_capacity(frames.len());
        for f in frames {
            read.push(StackFrame {
                function: unsafe { text(f.function)? },
                module: unsafe { text(f.module)? },
                file: unsafe { text(f.file)? },
                line: f.line.max(0) as u32,
                in_app: f.in_app != 0,
            });
        }
        record.exception = Some(LogException {
            type_name: unsafe { text(raw.exception_type)? },
            value: unsafe { text(raw.exception_value)? },
            handled: raw.handled != 0,
            frames: read,
        });
    }
    for a in unsafe { array(raw.attributes, raw.attribute_count)? } {
        record
            .attributes
            .insert(unsafe { text(a.key)? }, unsafe { text(a.value)? });
    }
    for part in unsafe { array(raw.fingerprint, raw.fingerprint_count)? } {
        record.fingerprint.push(unsafe { text(*part)? });
    }
    Ok(record)
}

/// Records a log line, an exception or a crash. Never blocks and never throws.
///
/// Returns [`PS_OK`] when it was queued, and a positive status for every other thing that can
/// correctly happen to it: held for consent, declined, folded into the line before, kept as a
/// breadcrumb, over the device's budget, or capture being off.
///
/// # Safety
/// `record` must point at a readable [`PsLogRecord`] whose contents obey the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_log(client: u64, record: *const PsLogRecord) -> i32 {
    if record.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        let record = match unsafe { log_record(&*record) } {
            Ok(r) => r,
            Err(code) => return code,
        };
        use playersense_core::Logged;
        match c.log(record) {
            Logged::Queued => PS_OK,
            Logged::Folded => PS_LOG_FOLDED,
            Logged::Breadcrumb => PS_LOG_BREADCRUMB,
            Logged::AwaitingConsent => PS_HELD_FOR_CONSENT,
            Logged::Declined => PS_DECLINED_BY_CONSENT,
            Logged::RateLimited => PS_LOG_RATE_LIMITED,
            Logged::Disabled => PS_LOG_DISABLED,
        }
    })
}

/// Keeps something that happened, in memory, for the next error to carry.
///
/// # Safety
/// The strings must obey the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_add_breadcrumb(
    client: u64,
    category: PsStr,
    message: PsStr,
    level: i32,
) -> i32 {
    with_client(client, |c| {
        let (category, message) = match unsafe { (text(category), text(message)) } {
            (Ok(a), Ok(b)) => (a, b),
            _ => return PS_ERR_INVALID_ARGUMENT,
        };
        let level =
            playersense_core::LogLevel::from_i32(level).unwrap_or(playersense_core::LogLevel::Info);
        c.add_breadcrumb(&category, &message, level);
        PS_OK
    })
}

/// Records that the player agreed to share error reports, whatever they said about analytics.
///
/// # Safety
/// The strings must obey the borrowing rule.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_set_diagnostics_granted(
    client: u64,
    policy_version: PsStr,
    att_status: PsStr,
    gdpr_applies: i32,
    collected_at_ms: i64,
) -> i32 {
    with_client(client, |c| {
        let (policy, att) = match unsafe { (str_strict(policy_version), str_strict(att_status)) } {
            (Ok(p), Ok(a)) => (p, a),
            _ => return PS_ERR_INVALID_ARGUMENT,
        };
        let State::Granted(block) = granted(policy, att, gdpr_applies != 0, collected_at_ms) else {
            return PS_ERR_INVALID_ARGUMENT;
        };
        c.set_diagnostics_consent(playersense_core::Diagnostics::Granted(block));
        PS_OK
    })
}

/// Records that the player refused error reports. What was waiting is deleted.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_set_diagnostics_denied(client: u64) -> i32 {
    with_client(client, |c| {
        c.set_diagnostics_consent(playersense_core::Diagnostics::Denied);
        PS_OK
    })
}

/// Error reports follow the analytics answer again: the default.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_set_diagnostics_follow_analytics(client: u64) -> i32 {
    with_client(client, |c| {
        c.set_diagnostics_consent(playersense_core::Diagnostics::FollowAnalytics);
        PS_OK
    })
}

/// How many crash reports are waiting on the device for the player's answer.
///
/// # Safety
/// `out` must be writable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_held_crash_reports(client: u64, out: *mut i64) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    with_client(client, |c| {
        unsafe { *out = c.held_crash_reports() as i64 };
        PS_OK
    })
}

/// Deletes the crash reports waiting for an answer, without sending them.
#[unsafe(no_mangle)]
pub extern "C" fn ps_client_discard_held_crash_reports(client: u64) -> i32 {
    with_client(client, |c| {
        c.discard_held_crash_reports();
        PS_OK
    })
}

/// What the log stream has done.
///
/// # Safety
/// `out` must point at a writable [`PsLogStats`] whose `struct_size` is set.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn ps_client_log_stats(client: u64, out: *mut PsLogStats) -> i32 {
    if out.is_null() {
        return PS_ERR_INVALID_ARGUMENT;
    }
    if (unsafe { (*out).struct_size } as usize) < size_of::<PsLogStats>() {
        return PS_ERR_ABI_MISMATCH;
    }
    with_client(client, |c| {
        let s = c.log_stats();
        unsafe {
            *out = PsLogStats {
                struct_size: size_of::<PsLogStats>() as u32,
                _pad: 0,
                recorded: s.recorded,
                folded: s.folded,
                breadcrumbs: s.breadcrumbs,
                rate_limited: s.rate_limited,
                without_consent: s.without_consent,
                evicted: s.evicted,
                batches_delivered: s.batches_delivered,
                accepted_by_server: s.accepted_by_server,
                dropped_by_server: s.dropped_by_server,
            };
        }
        PS_OK
    })
}

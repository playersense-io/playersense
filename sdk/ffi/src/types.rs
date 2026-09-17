//! The C-visible types, and the rules that govern them.
//!
//! # Ownership, in four rules
//!
//! **R1 — anything passed *in* is borrowed for the call.** The callee copies what it needs
//! before returning. `ptr == NULL` is legal only with `len == 0`; a negative length is
//! [`PS_ERR_INVALID_ARGUMENT`].
//!
//! **R2 — nothing is ever returned owned.** There is no `ps_string_free`, because no function
//! hands the caller memory. Values flow out through a caller-provided buffer and a
//! required-length out-parameter, so a leak is not representable.
//!
//! **R3 — buffers flowing host → core go through a [`PsSink`].** Neither side ever frees the
//! other's memory, and the host's buffer need only live for the duration of the sink call.
//!
//! **R4 — optional values use a natural sentinel, never a companion flag.** A `has_value`
//! boolean beside a value admits `has=0, value=5`, which somebody will eventually construct.
//! So `PsStr { NULL, 0 }` is `None`, and a negative `retry_after_ms` is `None`.
//!
//! # Two conventions that exist to defeat specific bugs
//!
//! Every length is `int64_t` and never `size_t`: `size_t` is four bytes on 32-bit Android and
//! eight everywhere else, which makes the C# declaration bitness-dependent.
//!
//! Every boolean is `int32_t` and never C `_Bool`: C#'s default `bool` marshals as a 4-byte
//! Win32 `BOOL`, and the mismatch is silent.

use std::ffi::c_void;

// ---- status codes -----------------------------------------------------------
//
// One flat space with disjoint ranges, rather than one enum per source. A caller should not
// have to know which of the core's two `Rejected` types a given function returns — that
// asymmetry is exactly what a binding gets wrong once and then everywhere.
//
// Negative is failure, zero is success, and **positive is reserved** for "succeeded, with
// something to say", so a future addition there cannot break a `status < 0` check.

pub const PS_OK: i32 = 0;

/// The event is held in memory, waiting for the player to answer the consent dialog.
///
/// It is delivered if they grant and discarded if they decline, and it never touches the disk
/// either way. **Not a failure** — a binding that warns here warns once per event on every
/// launch before the dialog is answered.
pub const PS_HELD_FOR_CONSENT: i32 = 1;

/// The event was discarded because the player has declined analytics.
///
/// **Not a failure** either: this is the SDK obeying them. It is distinguished from
/// [`PS_OK`] so that a diagnostics screen can say which of the two happened, which was
/// impossible when both returned zero.
pub const PS_DECLINED_BY_CONSENT: i32 = 2;

/// The handle is unknown, or belonged to a client that has been destroyed.
pub const PS_ERR_BAD_HANDLE: i32 = -1;
/// The host vtable is the wrong size or is missing a required function.
pub const PS_ERR_BAD_HOST: i32 = -2;
/// A null pointer with a non-zero length, a negative length, or invalid UTF-8 where the
/// protocol requires ASCII.
pub const PS_ERR_INVALID_ARGUMENT: i32 = -3;
/// The provided buffer was too small; `*out_len` holds the size required.
pub const PS_ERR_BUFFER_TOO_SMALL: i32 = -4;
/// A host callback called back into this client. See [`PsHost`].
pub const PS_ERR_REENTRANT: i32 = -5;
/// A panic was caught inside this client, which is now inert.
pub const PS_ERR_POISONED: i32 = -6;
/// The binding and the binary disagree about the ABI.
pub const PS_ERR_ABI_MISMATCH: i32 = -7;

/// The event name is empty, too long, or outside `[a-zA-Z0-9_.:-]`.
pub const PS_REJECTED_EVENT_NAME: i32 = -20;
/// More properties than one event may carry.
pub const PS_REJECTED_TOO_MANY_PROPERTIES: i32 = -21;

/// The context key is empty, too long, or outside `[a-zA-Z0-9_.-]`.
pub const PS_REJECTED_CONTEXT_KEY: i32 = -40;
/// The context key is in a namespace PlayerSense resolves for itself. The owner is named in
/// `ps_last_error_message`.
pub const PS_REJECTED_CONTEXT_RESERVED: i32 = -41;
/// That scope already holds as many keys as it may.
pub const PS_REJECTED_CONTEXT_FULL: i32 = -42;

// ---- value types ------------------------------------------------------------

pub const PS_VALUE_STR: i32 = 0;
pub const PS_VALUE_INT: i32 = 1;
pub const PS_VALUE_FLOAT: i32 = 2;
pub const PS_VALUE_BOOL: i32 = 3;

pub const PS_SCOPE_SESSION: i32 = 0;
pub const PS_SCOPE_PLAYER: i32 = 1;

pub const PS_LEVEL_DEBUG: i32 = 0;
pub const PS_LEVEL_INFO: i32 = 1;
pub const PS_LEVEL_WARN: i32 = 2;
pub const PS_LEVEL_ERROR: i32 = 3;

/// A borrowed UTF-8 string. **Not** NUL-terminated: the length is authoritative.
///
/// `{ NULL, 0 }` is the empty string, and where a value is optional it is also `None`.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct PsStr {
    pub ptr: *const u8,
    pub len: i64,
}

/// One typed value, for an event property or a context entry.
///
/// A flat struct rather than a union. Unions marshal badly and inconsistently across C#,
/// Swift and JNI, and sixteen saved bytes on a stack array is not worth a layout bug that
/// only appears on one platform.
///
/// Only the field matching `type` is read.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct PsValue {
    /// One of the `PS_VALUE_*` constants.
    pub kind: i32,
    /// `PS_VALUE_BOOL`: 0 or 1.
    pub b: i32,
    pub i: i64,
    pub f: f64,
    pub s: PsStr,
}

/// One named value in a property bag.
///
/// Passed as a flat array — one crossing per event, and no handle to leak. A builder handle
/// reads better in C# and has a leak mode; a stack array cannot leak, which matters more in
/// something that lives inside a game for hours.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct PsProperty {
    pub key: PsStr,
    pub value: PsValue,
}

/// What the SDK has done. `struct_size` is set by the caller.
#[repr(C)]
#[derive(Clone, Copy, Default)]
pub struct PsStats {
    pub struct_size: u32,
    pub _pad: u32,
    pub events_tracked: u64,
    pub events_rejected: u64,
    pub events_without_consent: u64,
    pub events_evicted: u64,
    pub batches_spooled: u64,
    pub batches_delivered: u64,
    pub batches_discarded: u64,
    pub requests_failed: u64,
    pub events_accepted_by_server: u64,
    pub events_dropped_by_server: u64,
}

/// The configuration, mirroring `playersense_core::Config`.
///
/// **Fields are append-only and never reordered.** Combined with `struct_size`, that is what
/// lets a binary with a twenty-fifth tunable keep working with a binding compiled against
/// twenty-four.
///
/// Zero means "use the default" for every numeric field, which is why
/// [`crate::ps_config_default`] exists: twenty-four fields is a lot to get right, and a
/// forgotten `flush_interval_ms` would otherwise clamp to 100 ms and hammer the endpoint.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct PsConfig {
    pub struct_size: u32,
    pub _pad: u32,

    pub api_key: PsStr,
    pub endpoint: PsStr,
    pub environment: PsStr,
    pub sdk_name: PsStr,
    pub sdk_version: PsStr,
    pub app_version: PsStr,
    pub app_build: PsStr,
    pub platform: PsStr,
    pub os_version: PsStr,
    pub locale: PsStr,

    pub flush_interval_ms: i64,
    pub max_events_per_batch: i64,
    pub max_batch_bytes: i64,
    pub session_timeout_ms: i64,
    pub spool_max_age_ms: i64,
    pub spool_max_bytes: i64,
    pub spool_max_batches: i64,
    pub preconsent_max_events: i64,
    pub backoff_base_ms: i64,
    pub backoff_cap_ms: i64,

    pub cold_after_failures: i32,
    /// 0 or 1.
    pub compression: i32,
    /// 0 or 1.
    pub debug: i32,
    pub _pad2: i32,
}

/// One HTTP request the host is asked to perform.
///
/// Everything in it is **borrowed for the duration of the `send` call**. The host must copy
/// the body before returning — which it was going to do anyway when handing it to
/// `UnityWebRequest` or `URLSession`. Transferring ownership was rejected: a host that forgot
/// to free would leak a batch body per flush, which over a two-hour session is tens of
/// megabytes inside somebody's shipped game.
#[repr(C)]
pub struct PsRequest {
    pub id: u64,
    pub url: PsStr,
    pub api_key: PsStr,
    /// `{ NULL, 0 }` when the body is not compressed.
    pub content_encoding: PsStr,
    pub body: *const u8,
    pub body_len: i64,
}

/// Receives a buffer from the host.
///
/// The host calls this with data it owns; the core copies immediately. Neither side frees the
/// other's memory, and the host's buffer need only be valid for the call — so a stack array,
/// an `NSData`, or a `byte[]` copied into a scratch buffer all work.
pub type PsSink = extern "C" fn(ctx: *mut c_void, data: *const u8, len: i64);

/// The platform, as function pointers.
///
/// # Every callback must be wrapped in a catch-all
///
/// These are `extern "C"`, so a foreign exception crossing one — a C# exception, a Swift
/// `fatalError` — **aborts the process at that frame**. That is deliberate: the alternative,
/// letting a foreign payload unwind through the state machine's frames, risks memory
/// corruption, and a deterministic abort with a usable stack is the better of two bad
/// outcomes.
///
/// So every binding MUST catch everything inside every callback body. In C#, every callback
/// must also be a **static** method (IL2CPP requires it) and, under Mono, any delegate passed
/// as a function pointer must be **rooted in a static field** or the collector will take it
/// and the second callback will crash.
///
/// # A callback must not call back
///
/// The client's lock is held while these run. A host whose `send` synchronously completes the
/// request would deadlock, so re-entering on the same handle returns [`PS_ERR_REENTRANT`] and
/// does nothing. `send` starts a request and returns; the completion arrives later through
/// `ps_client_on_response`.
#[repr(C)]
pub struct PsHost {
    /// Set by the caller to `sizeof(PsHost)`.
    pub struct_size: u32,
    pub _pad: u32,
    /// Passed back to every callback. Typically a handle to the binding's own host object.
    pub user_data: *mut c_void,

    pub now_ms: Option<extern "C" fn(*mut c_void) -> i64>,
    pub monotonic_ms: Option<extern "C" fn(*mut c_void) -> i64>,
    pub fill_random: Option<extern "C" fn(*mut c_void, *mut u8, i64)>,

    /// Returns 0 on success, non-zero on failure. The core's response to any failure is the
    /// same — keep the batch in memory, where it is subject to eviction — so a message would
    /// be a distinction nothing acts on.
    pub store_put: Option<extern "C" fn(*mut c_void, PsStr, *const u8, i64) -> i32>,
    /// Calls `sink` **once** with the value, or **not at all** if the key is absent. Calling
    /// it with a zero length means present and empty, which is a different fact.
    pub store_get: Option<extern "C" fn(*mut c_void, PsStr, PsSink, *mut c_void)>,
    pub store_delete: Option<extern "C" fn(*mut c_void, PsStr)>,
    /// Calls `sink` once per key carrying that prefix, in any order.
    pub store_keys: Option<extern "C" fn(*mut c_void, PsStr, PsSink, *mut c_void)>,

    pub send: Option<extern "C" fn(*mut c_void, *const PsRequest)>,
    /// Optional; may be NULL.
    pub log: Option<extern "C" fn(*mut c_void, i32, PsStr)>,
}

/// What a binding believes the layout to be, for [`crate::ps_abi_selfcheck`].
///
/// This is the guard that actually prevents heap corruption, and it is the one people skip.
/// The ABI version catches a deliberate change; this catches the accident — a `bool`
/// marshalled as one byte instead of four, a `Pack` mismatch, a field reordered in a
/// refactor — where both sides still report the same version.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct PsAbiSizes {
    pub abi_version: u32,
    pub sizeof_host: u32,
    pub sizeof_config: u32,
    pub sizeof_stats: u32,
    pub sizeof_str: u32,
    pub sizeof_value: u32,
    pub sizeof_property: u32,
    pub sizeof_request: u32,
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The layouts a binding has to reproduce. A change here is a breaking ABI change, and
    /// this test is where that gets noticed rather than in somebody's crash report.
    #[test]
    fn the_abi_layout_is_what_the_bindings_declare() {
        assert_eq!(size_of::<PsStr>(), 16);
        assert_eq!(size_of::<PsValue>(), 40);
        assert_eq!(size_of::<PsProperty>(), 56);
        assert_eq!(size_of::<PsAbiSizes>(), 32);

        // Every struct is 8-aligned, so the SysV i386 ABI — where `int64_t` is 4-aligned —
        // lays them out identically to everywhere else.
        assert_eq!(align_of::<PsStr>(), 8);
        assert_eq!(align_of::<PsValue>(), 8);
        assert_eq!(align_of::<PsConfig>(), 8);
        assert_eq!(align_of::<PsHost>(), 8);
        assert_eq!(align_of::<PsStats>(), 8);
    }

    /// A null function pointer must be representable, or the required-callback check cannot
    /// be written.
    #[test]
    fn an_absent_callback_is_a_null_pointer() {
        assert_eq!(
            size_of::<Option<extern "C" fn(*mut c_void) -> i64>>(),
            size_of::<*const c_void>()
        );
    }
}

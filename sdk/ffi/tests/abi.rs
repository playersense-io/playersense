//! The C ABI, driven through the real `extern "C"` symbols.
//!
//! Not through the Rust API underneath them: the point is to exercise the boundary itself —
//! the vtable, the pointer and length conventions, the handle table, the reentrancy guard and
//! the error codes — because that is the part no test in `playersense-core` can reach and the
//! part a binding will get wrong.
//!
//! The mock host is a vtable of `extern "C"` functions over a `MockState` reached through
//! `user_data`, which is exactly the shape a C# or Swift binding has.

#![allow(clippy::missing_safety_doc)]

use std::collections::BTreeMap;
use std::ffi::c_void;
use std::slice;

use playersense::*;

// ---- the mock host ----------------------------------------------------------

#[derive(Default)]
struct MockState {
    wall_ms: i64,
    monotonic_ms: i64,
    store: BTreeMap<String, Vec<u8>>,
    sent: Vec<Sent>,
    logs: Vec<String>,
    /// Makes every write fail, as a full disk does.
    storage_fails: bool,
    /// Calls back into the client from inside `send`, which must be refused rather than
    /// deadlock.
    reenter_on_send: u64,
}

#[derive(Clone)]
struct Sent {
    id: u64,
    body: Vec<u8>,
    compressed: bool,
}

unsafe fn state<'a>(ud: *mut c_void) -> &'a mut MockState {
    unsafe { &mut *(ud as *mut MockState) }
}

unsafe fn borrowed<'a>(s: PsStr) -> &'a str {
    if s.ptr.is_null() {
        return "";
    }
    std::str::from_utf8(unsafe { slice::from_raw_parts(s.ptr, s.len as usize) }).unwrap()
}

extern "C" fn now_ms(ud: *mut c_void) -> i64 {
    unsafe { state(ud) }.wall_ms
}

extern "C" fn monotonic_ms(ud: *mut c_void) -> i64 {
    unsafe { state(ud) }.monotonic_ms
}

extern "C" fn fill_random(_ud: *mut c_void, out: *mut u8, len: i64) {
    // Deliberately does nothing beyond what the core pre-seeded, which is itself the test
    // of the pre-seed: a binding whose RNG is a stub must still produce distinct ids.
    let _ = (out, len);
}

extern "C" fn store_put(ud: *mut c_void, key: PsStr, value: *const u8, len: i64) -> i32 {
    let s = unsafe { state(ud) };
    if s.storage_fails {
        return 1;
    }
    let bytes = if value.is_null() || len <= 0 {
        Vec::new()
    } else {
        unsafe { slice::from_raw_parts(value, len as usize) }.to_vec()
    };
    s.store.insert(unsafe { borrowed(key) }.to_owned(), bytes);
    0
}

extern "C" fn store_get(ud: *mut c_void, key: PsStr, sink: PsSink, ctx: *mut c_void) {
    let s = unsafe { state(ud) };
    // Not calling the sink at all is how absence travels.
    if let Some(value) = s.store.get(unsafe { borrowed(key) }) {
        sink(ctx, value.as_ptr(), value.len() as i64);
    }
}

extern "C" fn store_delete(ud: *mut c_void, key: PsStr) {
    unsafe { state(ud) }.store.remove(unsafe { borrowed(key) });
}

extern "C" fn store_keys(ud: *mut c_void, prefix: PsStr, sink: PsSink, ctx: *mut c_void) {
    let s = unsafe { state(ud) };
    let prefix = unsafe { borrowed(prefix) };
    let keys: Vec<String> = s
        .store
        .keys()
        .filter(|k| k.starts_with(prefix))
        .cloned()
        .collect();
    for key in keys {
        sink(ctx, key.as_ptr(), key.len() as i64);
    }
}

extern "C" fn send(ud: *mut c_void, request: *const PsRequest) {
    let s = unsafe { state(ud) };
    let r = unsafe { &*request };
    let body = if r.body.is_null() || r.body_len <= 0 {
        Vec::new()
    } else {
        unsafe { slice::from_raw_parts(r.body, r.body_len as usize) }.to_vec()
    };
    s.sent.push(Sent {
        id: r.id,
        body,
        compressed: !r.content_encoding.ptr.is_null(),
    });

    if s.reenter_on_send != 0 {
        // A host that completes synchronously. Must be refused, not deadlocked.
        let handle = s.reenter_on_send;
        let code = unsafe { ps_client_on_response(handle, r.id, 202, -1, std::ptr::null(), 0) };
        s.logs.push(format!("reentrant call returned {code}"));
    }
}

extern "C" fn log(ud: *mut c_void, _level: i32, message: PsStr) {
    unsafe { state(ud) }
        .logs
        .push(unsafe { borrowed(message) }.to_owned());
}

fn vtable(ud: *mut MockState) -> PsHost {
    PsHost {
        struct_size: size_of::<PsHost>() as u32,
        _pad: 0,
        user_data: ud as *mut c_void,
        now_ms: Some(now_ms),
        monotonic_ms: Some(monotonic_ms),
        fill_random: Some(fill_random),
        store_put: Some(store_put),
        store_get: Some(store_get),
        store_delete: Some(store_delete),
        store_keys: Some(store_keys),
        send: Some(send),
        log: Some(log),
    }
}

fn str_of(s: &str) -> PsStr {
    PsStr {
        ptr: s.as_ptr(),
        len: s.len() as i64,
    }
}

const ABSENT: PsStr = PsStr {
    ptr: std::ptr::null(),
    len: 0,
};

/// A client and the state its host reads, kept together so the state outlives the client.
///
/// The state is a **raw pointer from `Box::into_raw`**, not a `Box` field, and that is not
/// stylistic. Taking `state.as_mut() as *mut _` and then moving the `Box` into this struct
/// invalidates the pointer's provenance — the allocation does not move, but the borrow it was
/// derived from is gone, and every later callback dereferences a tag that no longer exists.
/// Miri catches it; a release build does not, until one day it does.
///
/// A binding has the same hazard in the same shape: a `GCHandle` or a `void*` taken from an
/// object that is later moved or collected. Holding the pointer and nothing else is the fix
/// in both languages.
struct Fixture {
    handle: u64,
    state: *mut MockState,
}

impl Fixture {
    fn new() -> Self {
        Self::with_store(BTreeMap::new())
    }

    fn with_store(store: BTreeMap<String, Vec<u8>>) -> Self {
        // Into a raw pointer up front, so nothing this fixture does can move it afterwards.
        let state = Box::into_raw(Box::new(MockState {
            wall_ms: 1_789_560_000_000,
            store,
            ..Default::default()
        }));

        let host = vtable(state);
        let mut config = unsafe { std::mem::zeroed::<PsConfig>() };
        unsafe { ps_config_default(&mut config) };
        config.api_key = str_of("ps_project_live_test");
        config.endpoint = str_of("https://ingest.eu-1.example");
        config.sdk_name = str_of("test");
        config.platform = str_of("ios");

        let mut handle = 0u64;
        let code = unsafe { ps_client_create(&host, &config, &mut handle) };
        assert_eq!(code, PS_OK, "create: {}", last_error());
        assert_ne!(handle, 0);

        Self { handle, state }
    }

    /// The host's state.
    ///
    /// `&mut self`, so the compiler enforces what a comment used to promise: a callback takes
    /// its own reference to this same object while a `ps_client_*` call is running, and two
    /// live `&mut` to one object is undefined behaviour rather than a style question. Taking
    /// the fixture mutably makes holding one across a call impossible to write.
    fn state(&mut self) -> &mut MockState {
        unsafe { &mut *self.state }
    }

    fn consent(&self) {
        let code = unsafe {
            ps_client_set_consent_granted(
                self.handle,
                str_of("2026-01-15"),
                str_of("authorized"),
                1,
                1_789_560_000_000,
            )
        };
        assert_eq!(code, PS_OK);
    }

    fn track(&self, name: &str) -> i32 {
        unsafe { ps_client_track(self.handle, str_of(name), std::ptr::null(), 0) }
    }

    fn take_sent(&mut self) -> Vec<Sent> {
        std::mem::take(&mut self.state().sent)
    }

    fn logged(&mut self, needle: &str) -> bool {
        self.state().logs.iter().any(|m| m.contains(needle))
    }
}

impl Drop for Fixture {
    fn drop(&mut self) {
        // The client first: a late callback would otherwise read freed memory.
        ps_client_destroy(self.handle);
        drop(unsafe { Box::from_raw(self.state) });
    }
}

fn last_error() -> String {
    let mut len = 0i64;
    unsafe { ps_last_error_message(std::ptr::null_mut(), 0, &mut len) };
    if len <= 0 {
        return String::new();
    }
    let mut buf = vec![0u8; len as usize];
    unsafe { ps_last_error_message(buf.as_mut_ptr(), len, &mut len) };
    String::from_utf8_lossy(&buf).into_owned()
}

// ---- the ABI itself ---------------------------------------------------------

/// The guard that actually prevents heap corruption, and the one bindings skip.
#[test]
fn the_selfcheck_accepts_a_matching_binding_and_refuses_a_drifted_one() {
    let matching = PsAbiSizes {
        abi_version: PS_ABI_VERSION,
        sizeof_host: size_of::<PsHost>() as u32,
        sizeof_config: size_of::<PsConfig>() as u32,
        sizeof_stats: size_of::<PsStats>() as u32,
        sizeof_str: size_of::<PsStr>() as u32,
        sizeof_value: size_of::<PsValue>() as u32,
        sizeof_property: size_of::<PsProperty>() as u32,
        sizeof_request: size_of::<PsRequest>() as u32,
    };
    assert_eq!(unsafe { ps_abi_selfcheck(&matching) }, PS_OK);

    // A `bool` marshalled as one byte instead of four is exactly this.
    let drifted = PsAbiSizes {
        sizeof_value: 36,
        ..matching
    };
    assert_eq!(unsafe { ps_abi_selfcheck(&drifted) }, PS_ERR_ABI_MISMATCH);
    assert!(last_error().contains("PsValue"), "{}", last_error());
}

/// A host missing a callback is refused at creation, not at the first call that needs it.
#[test]
fn an_incomplete_vtable_is_refused_before_anything_is_built() {
    let mut state = Box::new(MockState::default());
    let mut host = vtable(state.as_mut() as *mut MockState);
    host.store_get = None;

    let mut config = unsafe { std::mem::zeroed::<PsConfig>() };
    unsafe { ps_config_default(&mut config) };
    config.api_key = str_of("k");
    config.endpoint = str_of("https://e");

    let mut handle = 0u64;
    assert_eq!(
        unsafe { ps_client_create(&host, &config, &mut handle) },
        PS_ERR_BAD_HOST
    );
    assert_eq!(handle, 0, "a failed create must not hand back a handle");
}

/// A stale handle is refused rather than dereferenced — the Unity domain-reload case.
#[test]
fn a_destroyed_handle_is_refused_rather_than_reused() {
    let handle = {
        let fixture = Fixture::new();
        fixture.handle
    }; // dropped, which destroys it

    assert_eq!(
        unsafe { ps_client_track(handle, str_of("x"), std::ptr::null(), 0) },
        PS_ERR_BAD_HANDLE
    );
    assert_eq!(ps_client_flush(handle), PS_ERR_BAD_HANDLE);
    assert_eq!(
        ps_client_destroy(handle),
        PS_ERR_BAD_HANDLE,
        "destroying twice is not a crash"
    );
}

/// Nothing is ever returned owned, so a value comes back through a caller's buffer.
#[test]
fn a_value_comes_back_through_the_callers_buffer() {
    let fixture = Fixture::new();

    // The probe: how big?
    let mut needed = 0i64;
    let code =
        unsafe { ps_client_install_id(fixture.handle, std::ptr::null_mut(), 0, &mut needed) };
    assert_eq!(code, PS_ERR_BUFFER_TOO_SMALL);
    assert_eq!(needed, 36, "a hyphenated uuid");

    // Too small, and it still reports what it needs.
    let mut tiny = [0u8; 8];
    let mut len = 0i64;
    assert_eq!(
        unsafe { ps_client_install_id(fixture.handle, tiny.as_mut_ptr(), 8, &mut len) },
        PS_ERR_BUFFER_TOO_SMALL
    );
    assert_eq!(len, 36);

    // And big enough.
    let mut buf = [0u8; 64];
    assert_eq!(
        unsafe { ps_client_install_id(fixture.handle, buf.as_mut_ptr(), 64, &mut len) },
        PS_OK
    );
    let id = std::str::from_utf8(&buf[..len as usize]).unwrap();
    assert_eq!(id.matches('-').count(), 4, "{id}");
}

/// Even with a host RNG that does nothing, two installs differ.
#[test]
fn a_stub_random_still_produces_distinct_installs() {
    let read = |f: &Fixture| {
        let mut buf = [0u8; 64];
        let mut len = 0i64;
        unsafe { ps_client_install_id(f.handle, buf.as_mut_ptr(), 64, &mut len) };
        String::from_utf8_lossy(&buf[..len as usize]).into_owned()
    };
    let a = Fixture::new();
    let b = Fixture::new();
    assert_ne!(
        read(&a),
        read(&b),
        "every device would look like the same install"
    );
}

/// Malformed pointer/length pairs are refused rather than dereferenced.
#[test]
fn a_bad_pointer_and_length_pair_is_refused() {
    let fixture = Fixture::new();

    let negative = PsStr {
        ptr: "x".as_ptr(),
        len: -1,
    };
    assert_eq!(
        unsafe { ps_client_track(fixture.handle, negative, std::ptr::null(), 0) },
        PS_ERR_INVALID_ARGUMENT
    );

    // A null pointer with a length is a caller bug; dereferencing it would be ours.
    let lying = PsStr {
        ptr: std::ptr::null(),
        len: 5,
    };
    assert_eq!(
        unsafe { ps_client_track(fixture.handle, lying, std::ptr::null(), 0) },
        PS_ERR_INVALID_ARGUMENT
    );

    assert_eq!(
        unsafe { ps_client_track(fixture.handle, str_of("ok"), std::ptr::null(), -3) },
        PS_ERR_INVALID_ARGUMENT
    );
}

/// The reserved-namespace refusal carries the sentence, so no binding reproduces the table.
#[test]
fn a_reserved_namespace_is_refused_with_its_owner_named() {
    let fixture = Fixture::new();
    let value = PsValue {
        kind: PS_VALUE_STR,
        b: 0,
        i: 0,
        f: 0.0,
        s: str_of("US"),
    };

    let code = unsafe {
        ps_client_set_context(
            fixture.handle,
            PS_SCOPE_PLAYER,
            str_of("geo.country"),
            &value,
        )
    };
    assert_eq!(code, PS_REJECTED_CONTEXT_RESERVED);
    assert!(
        last_error().contains("request address"),
        "the binding gets a code and no explanation: {}",
        last_error()
    );
}

/// A host that completes synchronously is refused, not deadlocked.
#[test]
fn a_host_that_calls_back_from_send_is_refused() {
    let mut fixture = Fixture::new();
    let handle = fixture.handle;
    fixture.state().reenter_on_send = handle;
    fixture.consent();

    // `session_start` is critical, so this sends inside the call.
    assert_eq!(fixture.track("game_started"), PS_OK);

    assert!(
        fixture.logged(&format!("reentrant call returned {PS_ERR_REENTRANT}")),
        "logs: {:?}",
        fixture.state().logs
    );
}

/// The three outcomes of `track` are distinguishable through the C ABI.
///
/// This is the half a binding sees. A status compared with `!= PS_OK` turns a player's
/// refusal into a warning logged once per event, which is why the header says to check the
/// sign instead — and why these two live in the positive range rather than among the errors.
#[test]
fn track_reports_consent_outcomes_as_positive_statuses() {
    let fixture = Fixture::new();

    assert_eq!(
        fixture.track("before_the_dialog"),
        PS_HELD_FOR_CONSENT,
        "an unanswered dialog holds the event; it is not an error"
    );

    fixture.consent();
    assert_eq!(fixture.track("after_granting"), PS_OK);

    let code = ps_client_set_consent_denied(fixture.handle);
    assert_eq!(code, PS_OK);
    assert_eq!(
        fixture.track("after_declining"),
        PS_DECLINED_BY_CONSENT,
        "a refusal is an outcome, not a failure"
    );

    // The rule the header states, exercised as a binding would write it.
    for status in [PS_OK, PS_HELD_FOR_CONSENT, PS_DECLINED_BY_CONSENT] {
        assert!(status >= 0, "an outcome must never read as a failure");
    }

    // And a malformed name is still a failure, whatever the player has said.
    assert_eq!(
        fixture.track("Not A Valid Name"),
        PS_REJECTED_EVENT_NAME,
        "a declining player must not hide an integration bug"
    );
}

// ---- protocol scenarios, replayed through the boundary -----------------------

/// §13.5 — a batch reaches the spool before the first attempt, through the vtable.
#[test]
fn a_batch_is_spooled_through_the_host_before_it_is_sent() {
    let mut fixture = Fixture::new();
    fixture.consent();
    assert_eq!(fixture.track("level_started"), PS_OK);

    let spooled = fixture
        .state()
        .store
        .keys()
        .filter(|k| k.starts_with("ps.b."))
        .count();
    assert_eq!(
        spooled,
        1,
        "keys: {:?}",
        fixture.state().store.keys().collect::<Vec<_>>()
    );
    assert_eq!(fixture.take_sent().len(), 1);
}

/// §13.6 — the process dies between spooling and the 202; the next launch sends it.
///
/// The half `playersense-core` cannot prove: this goes through `store_keys`' sink protocol
/// and the replay at create time, both of which are boundary code.
#[test]
fn a_spooled_batch_survives_a_new_client_over_the_same_store() {
    let surviving = {
        let mut fixture = Fixture::new();
        fixture.consent();
        fixture.track("level_completed");
        fixture.take_sent(); // never answered — the process is gone
        fixture.state().store.clone()
    };

    let mut relaunched = Fixture::with_store(surviving);
    relaunched.consent();
    assert_eq!(ps_client_tick(relaunched.handle), PS_OK);

    let sent = relaunched.take_sent();
    assert!(
        !sent.is_empty(),
        "the spool was not drained on the next launch"
    );
    assert!(sent[0].compressed, "and it should still be gzip");
}

/// §13.24 — a refused credential stops sending and keeps the spool.
#[test]
fn a_401_stops_sending_and_keeps_the_events() {
    let mut fixture = Fixture::new();
    fixture.consent();
    fixture.track("level_started");

    let sent = fixture.take_sent();
    let code =
        unsafe { ps_client_on_response(fixture.handle, sent[0].id, 401, -1, std::ptr::null(), 0) };
    assert_eq!(code, PS_OK);

    let spooled = fixture
        .state()
        .store
        .keys()
        .filter(|k| k.starts_with("ps.b."))
        .count();
    assert_eq!(spooled, 1, "the player's events were thrown away");

    fixture.track("later");
    assert_eq!(ps_client_flush(fixture.handle), PS_OK);
    assert!(
        fixture.take_sent().is_empty(),
        "a shipped build would retry forever"
    );
}

/// §13.26 — tracking survives every dependency being broken, **through the ABI**.
///
/// Which is where the guarantee matters: a binding only ever sees this surface.
#[test]
fn tracking_survives_a_broken_host_and_never_reports_a_fault() {
    let mut fixture = Fixture::new();
    fixture.state().storage_fails = true;
    fixture.consent();

    for i in 0..1_000 {
        let code = fixture.track(&format!("event_{i}"));
        assert!(
            code == PS_OK || code == PS_REJECTED_EVENT_NAME,
            "track returned {code} with the disk failing"
        );
    }
    assert_eq!(ps_client_background(fixture.handle), PS_OK);
    assert_eq!(ps_client_shutdown(fixture.handle), PS_OK);

    let mut stats = PsStats {
        struct_size: size_of::<PsStats>() as u32,
        ..Default::default()
    };
    assert_eq!(
        unsafe { ps_client_stats(fixture.handle, &mut stats) },
        PS_OK
    );
    assert!(stats.events_tracked >= 1_000);
}

/// §13.28 — consecutive failures end in a cold state rather than a retry loop.
#[test]
fn a_dead_endpoint_reaches_the_cold_state() {
    let mut fixture = Fixture::new();
    fixture.consent();
    fixture.track("level_started");

    for _ in 0..8 {
        let sent = fixture.take_sent();
        let Some(last) = sent.last() else { break };
        // Status 0: never reached a server.
        unsafe { ps_client_on_response(fixture.handle, last.id, 0, -1, std::ptr::null(), 0) };
        fixture.state().monotonic_ms += 600_000;
        ps_client_tick(fixture.handle);
    }

    fixture.take_sent();
    fixture.state().monotonic_ms += 600_000;
    ps_client_tick(fixture.handle);
    assert!(
        fixture.take_sent().is_empty(),
        "still hammering a server that is not there"
    );

    assert_eq!(ps_client_foreground(fixture.handle), PS_OK);
    assert_eq!(
        fixture.take_sent().len(),
        1,
        "foreground buys exactly one attempt"
    );
}

/// A 202 is reported, including what the gateway refused.
#[test]
fn a_202_reaches_the_stats_with_what_the_gateway_dropped() {
    let mut fixture = Fixture::new();
    fixture.consent();
    fixture.track("level_started");

    let sent = fixture.take_sent();
    let body = br#"{"batch_id":"b","accepted":1,"dropped":2,"reasons":["event 1: bad name"]}"#;
    let code = unsafe {
        ps_client_on_response(
            fixture.handle,
            sent[0].id,
            202,
            -1,
            body.as_ptr(),
            body.len() as i64,
        )
    };
    assert_eq!(code, PS_OK);

    let mut stats = PsStats {
        struct_size: size_of::<PsStats>() as u32,
        ..Default::default()
    };
    unsafe { ps_client_stats(fixture.handle, &mut stats) };
    assert_eq!(stats.batches_delivered, 1);
    assert_eq!(stats.events_dropped_by_server, 2);
    assert!(fixture.logged("bad name"), "the developer is never told");
}

/// Context reaches the batch through the boundary, as a timeline.
#[test]
fn context_set_through_the_abi_reaches_the_wire_as_a_timeline() {
    let mut fixture = Fixture::new();
    fixture.consent();
    fixture.track("game_started");
    let sent = fixture.take_sent();
    unsafe { ps_client_on_response(fixture.handle, sent[0].id, 202, -1, std::ptr::null(), 0) };

    let rank = PsValue {
        kind: PS_VALUE_STR,
        b: 0,
        i: 0,
        f: 0.0,
        s: str_of("diamond"),
    };
    let level = PsValue {
        kind: PS_VALUE_INT,
        b: 0,
        i: 42,
        f: 0.0,
        s: ABSENT,
    };
    unsafe {
        assert_eq!(
            ps_client_set_context(fixture.handle, PS_SCOPE_PLAYER, str_of("rank"), &rank),
            PS_OK
        );
        assert_eq!(
            ps_client_set_context(
                fixture.handle,
                PS_SCOPE_PLAYER,
                str_of("progress.level"),
                &level
            ),
            PS_OK
        );
    }

    fixture.track("match_started");
    ps_client_flush(fixture.handle);

    let sent = fixture.take_sent();
    let body = gunzip(&sent.last().expect("sent").body);
    let text = String::from_utf8_lossy(&body);
    assert!(text.contains(r#""context""#), "{text}");
    assert!(text.contains(r#""rank":"diamond""#), "{text}");
    assert!(text.contains(r#""progress.level":42"#), "{text}");
}

fn gunzip(body: &[u8]) -> Vec<u8> {
    use std::io::Read;
    let mut out = Vec::new();
    flate2::read::GzDecoder::new(body)
        .read_to_end(&mut out)
        .unwrap();
    out
}

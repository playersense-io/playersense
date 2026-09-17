//! The [`Host`] implementation that forwards to the C vtable.
//!
//! Every method here is the same shape: borrow what the core gave us, call the function
//! pointer, copy anything that came back. The host's memory is never freed here and this
//! crate's memory never escapes, which is the whole of rule R3.

use std::ffi::c_void;
use std::slice;
use std::sync::atomic::{AtomicU64, Ordering};

use playersense_core::host::{Host, Level, Request, Response, StoreError};

use crate::convert::out_str;
use crate::types::*;

/// A caller-supplied vtable, plus the opaque pointer it wants back.
pub struct FfiHost {
    vtable: PsHost,
}

/// # Safety
///
/// Sound precisely because [`PsHost`] documents that every callback must be callable from any
/// thread. The client lives behind a mutex reachable from any thread, so the host it holds
/// must be `Send`; the header states the requirement that makes that true, and a binding that
/// ignores it has violated the contract rather than found a bug here.
unsafe impl Send for FfiHost {}

/// The largest value the core will accept from `store_get`, and the largest response body.
///
/// A bound on what the outside world can make this process allocate. `handle_alloc_error`
/// aborts unconditionally and no `catch_unwind` reaches it, so the only defence against a
/// hostile or broken host is to refuse to grow in the first place.
const MAX_INBOUND_BYTES: usize = 16 * 1024 * 1024;

/// Makes the randomness fallback differ between two clients in one process.
static SEQUENCE: AtomicU64 = AtomicU64::new(0x2545_F491_4F6C_DD1D);

impl FfiHost {
    /// Validates the vtable and takes a copy of it.
    ///
    /// Every required pointer is checked **before** anything is constructed, because a
    /// missing one is the single most common P/Invoke defect — a struct field ordering
    /// mismatch — and catching it at init beats discovering it at the first `store_get`.
    pub fn new(raw: &PsHost) -> Result<Self, i32> {
        if (raw.struct_size as usize) < size_of::<PsHost>() {
            return Err(PS_ERR_ABI_MISMATCH);
        }
        let required = [
            raw.now_ms.is_some(),
            raw.monotonic_ms.is_some(),
            raw.fill_random.is_some(),
            raw.store_put.is_some(),
            raw.store_get.is_some(),
            raw.store_delete.is_some(),
            raw.store_keys.is_some(),
            raw.send.is_some(),
        ];
        if required.iter().any(|present| !present) {
            return Err(PS_ERR_BAD_HOST);
        }
        Ok(Self {
            vtable: PsHost {
                struct_size: raw.struct_size,
                _pad: 0,
                user_data: raw.user_data,
                now_ms: raw.now_ms,
                monotonic_ms: raw.monotonic_ms,
                fill_random: raw.fill_random,
                store_put: raw.store_put,
                store_get: raw.store_get,
                store_delete: raw.store_delete,
                store_keys: raw.store_keys,
                send: raw.send,
                log: raw.log,
            },
        })
    }

    fn ud(&self) -> *mut c_void {
        self.vtable.user_data
    }
}

/// Where a sink accumulates, with the cap applied as it goes.
struct Accumulator {
    bytes: Vec<u8>,
    called: bool,
    truncated: bool,
}

/// Collects one value.
extern "C" fn collect_one(ctx: *mut c_void, data: *const u8, len: i64) {
    if ctx.is_null() || len < 0 {
        return;
    }
    let acc = unsafe { &mut *(ctx as *mut Accumulator) };
    acc.called = true;
    if data.is_null() || len == 0 {
        return;
    }
    let len = len as usize;
    if acc.bytes.len().saturating_add(len) > MAX_INBOUND_BYTES {
        acc.truncated = true;
        return;
    }
    acc.bytes
        .extend_from_slice(unsafe { slice::from_raw_parts(data, len) });
}

/// Collects a list of keys.
extern "C" fn collect_key(ctx: *mut c_void, data: *const u8, len: i64) {
    if ctx.is_null() || data.is_null() || len <= 0 {
        return;
    }
    let keys = unsafe { &mut *(ctx as *mut Vec<String>) };
    // A store holding tens of thousands of keys is a store something else has gone wrong
    // with; enumerating it unboundedly would turn that into an allocation failure.
    if keys.len() >= 100_000 {
        return;
    }
    let bytes = unsafe { slice::from_raw_parts(data, len as usize) };
    if let Ok(key) = std::str::from_utf8(bytes) {
        keys.push(key.to_owned());
    }
}

impl Host for FfiHost {
    fn now_ms(&self) -> i64 {
        (self.vtable.now_ms.expect("checked in new"))(self.ud())
    }

    fn monotonic_ms(&self) -> i64 {
        (self.vtable.monotonic_ms.expect("checked in new"))(self.ud())
    }

    fn fill_random(&self, out: &mut [u8]) {
        // Pre-seeded before the host is asked, so a binding whose RNG silently does nothing
        // still produces a distinct install id rather than an all-zero one — which would make
        // every device on that platform look like the same installation.
        //
        // The counter is not decoration. The clock and the stack address are identical for
        // two clients created in the same millisecond at the same depth, which is not a
        // contrived case: it is what a test does, and what a game creating a second client
        // after a domain reload does. Without it the fallback repeats itself exactly when it
        // is most likely to be noticed.
        let nonce = SEQUENCE.fetch_add(0x9E37_79B9_7F4A_7C15, Ordering::Relaxed);
        let mut seed = (self.now_ms() as u64)
            ^ ((self.monotonic_ms() as u64) << 21)
            ^ (out.as_ptr() as u64)
            ^ nonce;
        for byte in out.iter_mut() {
            seed ^= seed << 13;
            seed ^= seed >> 7;
            seed ^= seed << 17;
            *byte = (seed >> 24) as u8;
        }
        (self.vtable.fill_random.expect("checked in new"))(
            self.ud(),
            out.as_mut_ptr(),
            out.len() as i64,
        );
    }

    fn store_put(&self, key: &str, value: &[u8]) -> Result<(), StoreError> {
        let code = (self.vtable.store_put.expect("checked in new"))(
            self.ud(),
            out_str(key),
            value.as_ptr(),
            value.len() as i64,
        );
        if code == 0 {
            Ok(())
        } else {
            Err(StoreError("the host reported a storage failure".into()))
        }
    }

    fn store_get(&self, key: &str) -> Option<Vec<u8>> {
        let mut acc = Accumulator {
            bytes: Vec::new(),
            called: false,
            truncated: false,
        };
        (self.vtable.store_get.expect("checked in new"))(
            self.ud(),
            out_str(key),
            collect_one,
            &mut acc as *mut Accumulator as *mut c_void,
        );

        // Not calling the sink means absent; calling it with nothing means present and
        // empty, which is a different fact and the core acts on the difference.
        if !acc.called {
            return None;
        }
        if acc.truncated {
            self.log(
                Level::Error,
                &format!(
                    "the host returned more than {MAX_INBOUND_BYTES} bytes for {key}; discarded"
                ),
            );
            return None;
        }
        Some(acc.bytes)
    }

    fn store_delete(&self, key: &str) {
        (self.vtable.store_delete.expect("checked in new"))(self.ud(), out_str(key));
    }

    fn store_keys(&self, prefix: &str) -> Vec<String> {
        let mut keys: Vec<String> = Vec::new();
        (self.vtable.store_keys.expect("checked in new"))(
            self.ud(),
            out_str(prefix),
            collect_key,
            &mut keys as *mut Vec<String> as *mut c_void,
        );
        keys
    }

    fn send(&self, request: Request) {
        let encoding = request.content_encoding.map(out_str).unwrap_or(PsStr {
            ptr: std::ptr::null(),
            len: 0,
        });
        let raw = PsRequest {
            id: request.id,
            url: out_str(&request.url),
            api_key: out_str(&request.api_key),
            content_encoding: encoding,
            body: request.body.as_ptr(),
            body_len: request.body.len() as i64,
        };
        // Borrowed for the call: `request` outlives it and drops on return, so the host must
        // copy the body before returning. The header says so.
        (self.vtable.send.expect("checked in new"))(self.ud(), &raw as *const PsRequest);
    }

    fn log(&self, level: Level, message: &str) {
        let Some(log) = self.vtable.log else {
            return;
        };
        let level = match level {
            Level::Debug => PS_LEVEL_DEBUG,
            Level::Info => PS_LEVEL_INFO,
            Level::Warn => PS_LEVEL_WARN,
            Level::Error => PS_LEVEL_ERROR,
        };
        log(self.ud(), level, out_str(message));
    }
}

/// Reads a response body, bounded.
///
/// # Safety
/// `body` must be null or point at `len` readable bytes.
pub unsafe fn response(
    id: u64,
    status: u16,
    retry_after_ms: i64,
    body: *const u8,
    len: i64,
) -> Response {
    let bytes = if body.is_null() || len <= 0 {
        Vec::new()
    } else {
        let len = (len as usize).min(MAX_INBOUND_BYTES);
        unsafe { slice::from_raw_parts(body, len) }.to_vec()
    };
    Response {
        id,
        status,
        // R4: negative means absent, rather than a companion flag that can disagree.
        retry_after_ms: (retry_after_ms >= 0).then_some(retry_after_ms),
        body: bytes,
    }
}

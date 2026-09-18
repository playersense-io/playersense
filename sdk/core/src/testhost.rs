//! A host that does no I/O, for the conformance suite.
//!
//! This is the payoff of the architecture. Every scenario in §11 of the protocol document —
//! a week offline, a server that is down, a process killed between spooling and the 202, a
//! player who moves their device clock back a year — is a deterministic unit test here,
//! with a clock the test moves, a store the test can inspect, and responses the test writes.
//! None of it needs a network, a device, or a `sleep`.

use std::cell::{Cell, RefCell};
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};

use crate::host::{Host, Level, Request, Response, StoreError};

/// Distinct per `TestHost`, so two instances are two devices.
static SEEDS: AtomicU64 = AtomicU64::new(1);

pub struct TestHost {
    wall_ms: Cell<i64>,
    monotonic_ms: Cell<i64>,
    seed: Cell<u64>,
    store: RefCell<BTreeMap<String, Vec<u8>>>,
    sent: RefCell<Vec<Request>>,
    logs: RefCell<Vec<(Level, String)>>,
    storage_fails: Cell<bool>,
}

impl Default for TestHost {
    fn default() -> Self {
        Self::new()
    }
}

impl TestHost {
    pub fn new() -> Self {
        Self {
            // A fixed, plausible instant rather than zero, so a test that accidentally
            // depends on the epoch fails visibly instead of passing for the wrong reason.
            wall_ms: Cell::new(1_789_560_000_000),
            monotonic_ms: Cell::new(0),
            // Varied per host, or two clients in one test would generate the same
            // "random" install id and a reinstall would look like an update.
            seed: Cell::new(
                0x2545_F491_4F6C_DD1D ^ SEEDS.fetch_add(0x9E37_79B9_7F4A_7C15, Ordering::Relaxed),
            ),
            store: RefCell::new(BTreeMap::new()),
            sent: RefCell::new(Vec::new()),
            logs: RefCell::new(Vec::new()),
            storage_fails: Cell::new(false),
        }
    }

    /// Moves both clocks forward, which is what the passage of time looks like.
    pub fn advance_ms(&self, delta: i64) {
        self.wall_ms.set(self.wall_ms.get() + delta);
        self.monotonic_ms.set(self.monotonic_ms.get() + delta);
    }

    /// Moves the wall clock alone: a player changing their device clock.
    pub fn set_wall_ms(&self, at: i64) {
        self.wall_ms.set(at);
    }

    /// Makes every write fail, as a full disk or a revoked permission does.
    pub fn fail_storage(&self, failing: bool) {
        self.storage_fails.set(failing);
    }

    /// Everything sent since the last call, clearing the queue.
    pub fn take_sent(&self) -> Vec<Request> {
        std::mem::take(&mut self.sent.borrow_mut())
    }

    pub fn sent_count(&self) -> usize {
        self.sent.borrow().len()
    }

    /// Simulates a process that stopped without warning: the store survives, nothing else
    /// does. What comes back is the disk a new `Client` would find on the next launch.
    pub fn surviving_store(&self) -> BTreeMap<String, Vec<u8>> {
        self.store.borrow().clone()
    }

    /// Restores a store, for the other half of that test.
    pub fn with_store(store: BTreeMap<String, Vec<u8>>) -> Self {
        let host = Self::new();
        *host.store.borrow_mut() = store;
        host
    }

    pub fn logged(&self, needle: &str) -> bool {
        self.logs.borrow().iter().any(|(_, m)| m.contains(needle))
    }
}

impl Host for TestHost {
    fn now_ms(&self) -> i64 {
        self.wall_ms.get()
    }

    fn monotonic_ms(&self) -> i64 {
        self.monotonic_ms.get()
    }

    fn fill_random(&self, out: &mut [u8]) {
        // xorshift64*, so a failing test reproduces exactly rather than sometimes.
        for chunk in out.chunks_mut(8) {
            let mut x = self.seed.get();
            x ^= x >> 12;
            x ^= x << 25;
            x ^= x >> 27;
            self.seed.set(x);
            let bytes = x.wrapping_mul(0x2545_F491_4F6C_DD1D).to_le_bytes();
            chunk.copy_from_slice(&bytes[..chunk.len()]);
        }
    }

    fn store_put(&self, key: &str, value: &[u8]) -> Result<(), StoreError> {
        if self.storage_fails.get() {
            return Err(StoreError("the test host has storage failing".into()));
        }
        self.store
            .borrow_mut()
            .insert(key.to_owned(), value.to_vec());
        Ok(())
    }

    fn store_get(&self, key: &str) -> Option<Vec<u8>> {
        self.store.borrow().get(key).cloned()
    }

    fn store_delete(&self, key: &str) {
        self.store.borrow_mut().remove(key);
    }

    fn store_keys(&self, prefix: &str) -> Vec<String> {
        self.store
            .borrow()
            .keys()
            .filter(|k| k.starts_with(prefix))
            .cloned()
            .collect()
    }

    fn send(&self, request: Request) {
        self.sent.borrow_mut().push(request);
    }

    fn log(&self, level: Level, message: &str) {
        self.logs.borrow_mut().push((level, message.to_owned()));
    }
}

/// A 202 body carrying what the gateway made of the batch.
pub fn accepted(id: u64, count: u32) -> Response {
    Response {
        id,
        status: 202,
        retry_after_ms: None,
        etag: None,
        body: format!(r#"{{"batch_id":"b","accepted":{count}}}"#).into_bytes(),
    }
}

/// A response with no body, for the refusals that do not need one.
pub fn status(id: u64, status: u16) -> Response {
    Response {
        id,
        status,
        retry_after_ms: None,
        etag: None,
        body: Vec::new(),
    }
}

/// What a timeout, a refused connection or a cancelled request looks like.
pub fn unreachable(id: u64) -> Response {
    status(id, 0)
}

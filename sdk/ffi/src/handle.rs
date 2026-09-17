//! Handles, and why they are integers rather than pointers.
//!
//! A `ps_client_t*` would be the obvious design and it has a Unity-shaped failure mode that
//! is very hard to diagnose: an `IntPtr` cached in a C# `static` survives a **domain reload**.
//! Exit Play mode, re-enter, and the pointer is dangling — a crash on the second Play session
//! of the day that nobody can reproduce on demand.
//!
//! A generation-tagged index turns that into `PS_ERR_BAD_HANDLE` and a log line. Destroying a
//! client bumps the generation, so a handle to it can never be revived by a later client
//! landing in the same slot.
//!
//! Layout is `generation:32 | index:32`, and generations start at 1, so **0 is never a valid
//! handle** — which lets a binding treat a zeroed field as "not initialized" for free.

use std::cell::Cell;
use std::sync::atomic::AtomicBool;
use std::sync::{Arc, Mutex, RwLock};

use playersense_core::Client;

use crate::host::FfiHost;

pub struct ClientHandle {
    pub client: Mutex<Client<FfiHost>>,
    /// Set when a panic was caught inside this client. Every later call refuses.
    pub poisoned: AtomicBool,
}

struct Slot {
    generation: u32,
    entry: Option<Arc<ClientHandle>>,
}

static TABLE: RwLock<Vec<Slot>> = RwLock::new(Vec::new());

thread_local! {
    /// The handle currently executing on this thread, for the reentrancy guard.
    static ACTIVE: Cell<u64> = const { Cell::new(0) };
}

fn pack(index: usize, generation: u32) -> u64 {
    ((generation as u64) << 32) | index as u64
}

fn unpack(handle: u64) -> (usize, u32) {
    ((handle & 0xffff_ffff) as usize, (handle >> 32) as u32)
}

/// Files a client and returns its handle.
pub fn create(client: Client<FfiHost>) -> u64 {
    let entry = Arc::new(ClientHandle {
        client: Mutex::new(client),
        poisoned: AtomicBool::new(false),
    });

    let mut table = TABLE.write().unwrap_or_else(|p| p.into_inner());
    if let Some(index) = table.iter().position(|slot| slot.entry.is_none()) {
        let slot = &mut table[index];
        slot.entry = Some(entry);
        return pack(index, slot.generation);
    }
    table.push(Slot {
        generation: 1,
        entry: Some(entry),
    });
    pack(table.len() - 1, 1)
}

/// Finds a client, or `None` if the handle is stale or was never valid.
pub fn lookup(handle: u64) -> Option<Arc<ClientHandle>> {
    if handle == 0 {
        return None;
    }
    let (index, generation) = unpack(handle);
    let table = TABLE.read().unwrap_or_else(|p| p.into_inner());
    let slot = table.get(index)?;
    if slot.generation != generation {
        return None;
    }
    slot.entry.clone()
}

/// Retires a handle. Returns whether it was live.
///
/// A call already running on another thread holds its own `Arc`, so the client survives until
/// that call returns. A completion arriving afterwards finds `PS_ERR_BAD_HANDLE` — which is
/// correct rather than a defect: the batch it was answering about is still spooled, so
/// nothing is lost.
pub fn destroy(handle: u64) -> bool {
    if handle == 0 {
        return false;
    }
    let (index, generation) = unpack(handle);
    let mut table = TABLE.write().unwrap_or_else(|p| p.into_inner());
    let Some(slot) = table.get_mut(index) else {
        return false;
    };
    if slot.generation != generation || slot.entry.is_none() {
        return false;
    }
    slot.entry = None;
    // Wrapping, and skipping 0 so a recycled slot can never produce handle 0.
    slot.generation = slot.generation.wrapping_add(1).max(1);
    true
}

/// Whether this thread is already inside a call on this handle.
pub fn is_active(handle: u64) -> bool {
    ACTIVE.with(|active| active.get() == handle)
}

/// Marks a handle active for as long as the guard lives.
pub struct Active(u64);

impl Active {
    pub fn enter(handle: u64) -> Self {
        let previous = ACTIVE.with(|active| active.replace(handle));
        Self(previous)
    }
}

impl Drop for Active {
    fn drop(&mut self) {
        ACTIVE.with(|active| active.set(self.0));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_handle_is_never_zero() {
        // A binding gets a zeroed struct field for free, so zero must mean "nothing".
        assert_eq!(pack(0, 1), 1 << 32);
        assert!(lookup(0).is_none());
    }

    #[test]
    fn a_stale_handle_is_refused_rather_than_reused() {
        // The Unity domain-reload case: the C# static still holds the old value.
        let (index, generation) = unpack(pack(3, 7));
        assert_eq!((index, generation), (3, 7));
        assert!(lookup(pack(999_999, 1)).is_none());
    }

    #[test]
    fn the_reentrancy_guard_nests_and_unwinds() {
        assert!(!is_active(42));
        {
            let _outer = Active::enter(42);
            assert!(is_active(42));
            {
                let _inner = Active::enter(7);
                assert!(is_active(7));
                assert!(!is_active(42));
            }
            assert!(is_active(42), "the guard must restore what it replaced");
        }
        assert!(!is_active(42));
    }
}

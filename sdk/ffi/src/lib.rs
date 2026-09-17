//! The C ABI over [`playersense_core`].
//!
//! One implementation of the protocol, reachable from C#, Swift, Kotlin and C++. Every
//! binding supplies a host vtable and drives the same state machine; none of them makes a
//! decision of its own, which is §15's whole point.
//!
//! The ownership rules, the threading contract and the panic policy are documented on the
//! items below and reproduced into `include/playersense.h` by cbindgen, because the header is
//! the file a binding author actually reads.

pub mod api;
pub mod convert;
pub mod handle;
pub mod host;
pub mod types;
pub use api::*;
pub use types::*;

/// The ABI this build speaks.
///
/// A single monotonic integer before 1.0: any change to a struct or a signature is breaking,
/// so there is nothing to negotiate. A binding compares this against its own constant and
/// refuses to continue on a mismatch, which turns a silently corrupt struct layout into one
/// log line.
pub const PS_ABI_VERSION: u32 = 1;

/// Returns [`PS_ABI_VERSION`], so a binding can check the binary rather than the header it
/// was compiled against.
#[unsafe(no_mangle)]
pub extern "C" fn ps_abi_version() -> u32 {
    PS_ABI_VERSION
}

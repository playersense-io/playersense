//! The PlayerSense SDK protocol, implemented once.
//!
//! `docs/sdk/protocol.md` is the specification; this is the implementation every binding
//! shares. It performs **no I/O**: clock, storage, network and randomness arrive through
//! [`host::Host`], and every decision the protocol describes is made here.
//!
//! §15's instruction is the reason the crate exists: *do not independently reinvent protocol
//! behaviour in every SDK*. Eleven implementations of retry, eleven opinions about when a
//! session starts and eleven identifier bugs is not a platform.
//!
//! ```no_run
//! # use playersense_core::{Client, Config, consent, host::Host};
//! # fn example<H: Host>(host: H) {
//! let config = Config::new("ps_project_live_…", "https://ingest.eu-1.playersense.dev");
//! let mut client = Client::new(host, config);
//!
//! client.set_consent(playersense_core::granted("2026-01-15", "authorized", true, 0));
//! client.track("level_completed", Default::default()).ok();
//! client.flush();
//! # }
//! ```

pub mod client;
pub mod config;
pub mod consent;
pub mod context;
pub mod event;
pub mod host;
pub mod ids;
pub mod session;
pub mod spool;
pub mod time;
pub mod transport;

#[cfg(test)]
mod conformance;
#[cfg(test)]
mod testhost;

pub use client::{Client, Stats, granted};
pub use config::Config;
pub use context::{Context, Scope};
pub use event::{Priority, Recorded, Rejected, Value};
pub use host::{Host, Level, Request, Response, StoreError};

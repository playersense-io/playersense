//! The disk spool: the durability boundary (§6).
//!
//! An event is durable when it is here, not when it was tracked. Everything upstream of this
//! file lives in memory and is lost if the process is killed without warning — which mobile
//! operating systems do routinely and without a callback.
//!
//! # Metadata lives in the key
//!
//! A spooled batch is stored under a key that carries everything a decision needs:
//!
//! ```text
//! ps.b.00000000000000000042.1789567142123.1.2048
//!      └ ordinal ──────────┘└ created ───┘ │ └ bytes
//!                                          └ priority
//! ```
//!
//! So eviction, ageing and ordering are computed from `store_keys` alone, without reading a
//! single body. That matters more than it sounds: on a browser or a console every read is a
//! host call, and a spool that had to read five hundred bodies to decide which one to drop
//! would do it on the frame a player pressed something.
//!
//! The zero-padded ordinal makes lexicographic order chronological order, which is what
//! "oldest first" means here.
//!
//! # What is deliberately not in the key
//!
//! The attempt count. It lives in memory, and a restart resetting it is correct rather than
//! sloppy: a restart is itself a reason the next attempt might succeed. The backend's writer
//! keeps its own attempt counts in memory for the same reason.

use crate::config::Config;
use crate::event::Priority;
use crate::host::{Host, Level};

pub const PREFIX: &str = "ps.b.";

/// Everything a spool decision needs, read from a key.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Meta {
    pub key: String,
    pub ordinal: u64,
    pub created_ms: i64,
    pub priority: Priority,
    pub bytes: usize,
}

/// Builds the key a batch is stored under.
pub fn key(ordinal: u64, created_ms: i64, priority: Priority, bytes: usize) -> String {
    key_in(PREFIX, ordinal, created_ms, priority, bytes)
}

/// [`key`] under another prefix: the log spool (`crate::logs`) keeps its batches apart, so each
/// stream is bounded and evicted on its own and a flood of one never evicts the other.
pub fn key_in(
    prefix: &str,
    ordinal: u64,
    created_ms: i64,
    priority: Priority,
    bytes: usize,
) -> String {
    let p = match priority {
        Priority::Normal => 0,
        Priority::Critical => 1,
    };
    format!("{prefix}{ordinal:020}.{created_ms:013}.{p}.{bytes}")
}

/// Reads a key back, or `None` if it is not one of ours.
///
/// A key that does not parse is not an error to report, it is a key to ignore: the store may
/// hold anything, including a spool written by a version that formatted keys differently.
pub fn parse(key: &str) -> Option<Meta> {
    parse_in(PREFIX, key)
}

/// [`parse`] under another prefix.
pub fn parse_in(prefix: &str, key: &str) -> Option<Meta> {
    let rest = key.strip_prefix(prefix)?;
    let mut parts = rest.split('.');
    let ordinal = parts.next()?.parse().ok()?;
    let created_ms = parts.next()?.parse().ok()?;
    let priority = match parts.next()? {
        "0" => Priority::Normal,
        "1" => Priority::Critical,
        _ => return None,
    };
    let bytes = parts.next()?.parse().ok()?;
    if parts.next().is_some() {
        return None;
    }
    Some(Meta {
        key: key.to_owned(),
        ordinal,
        created_ms,
        priority,
        bytes,
    })
}

/// The spooled batches, oldest first.
pub fn list<H: Host + ?Sized>(host: &H) -> Vec<Meta> {
    list_in(host, PREFIX)
}

/// [`list`] under another prefix.
pub fn list_in<H: Host + ?Sized>(host: &H, prefix: &str) -> Vec<Meta> {
    let mut out: Vec<Meta> = host
        .store_keys(prefix)
        .iter()
        .filter_map(|k| parse_in(prefix, k))
        .collect();
    out.sort_by_key(|m| m.ordinal);
    out
}

/// What `enforce` had to throw away.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Evicted {
    /// Past the age bound. Not a capacity decision — see [`Config::spool_max_age_ms`].
    pub aged: usize,
    /// Dropped to stay inside the size or count bound.
    pub overflowed: usize,
}

impl Evicted {
    pub fn any(&self) -> bool {
        self.aged + self.overflowed > 0
    }
}

/// Brings the spool back inside its bounds (§6).
///
/// Two passes with different meanings, and conflating them would be wrong.
///
/// **Ageing** removes what the server would rewrite. A batch older than the gateway's skew
/// window uploads events whose timestamps are silently clamped, so dropping it is the honest
/// act rather than a capacity measure — it applies to critical batches exactly as it applies
/// to ordinary ones.
///
/// **Overflow** removes what will not fit, and here priority decides: oldest first within the
/// lowest class present, so a session start or a purchase survives a week of chatty gameplay
/// events. §18's rule 8, made specific.
pub fn enforce<H: Host + ?Sized>(host: &H, config: &Config, now_ms: i64) -> Evicted {
    enforce_in(
        host,
        PREFIX,
        Bounds {
            max_age_ms: config.spool_max_age_ms,
            max_bytes: config.spool_max_bytes,
            max_batches: config.spool_max_batches,
        },
        now_ms,
    )
}

/// How much a spool may hold, and for how long.
#[derive(Clone, Copy, Debug)]
pub struct Bounds {
    pub max_age_ms: i64,
    pub max_bytes: usize,
    pub max_batches: usize,
}

/// [`enforce`] for any spool.
pub fn enforce_in<H: Host + ?Sized>(
    host: &H,
    prefix: &str,
    bounds: Bounds,
    now_ms: i64,
) -> Evicted {
    let mut out = Evicted::default();
    let mut entries = list_in(host, prefix);

    entries.retain(|meta| {
        if now_ms - meta.created_ms < bounds.max_age_ms {
            return true;
        }
        host.store_delete(&meta.key);
        out.aged += 1;
        false
    });

    // Ordinary events first, then critical ones — and within each, oldest first. A spool
    // that has to drop something drops the least recoverable thing last.
    let mut order: Vec<usize> = (0..entries.len()).collect();
    order.sort_by_key(|&i| (entries[i].priority, entries[i].ordinal));

    let mut count = entries.len();
    let mut bytes: usize = entries.iter().map(|m| m.bytes).sum();

    for i in order {
        if count <= bounds.max_batches && bytes <= bounds.max_bytes {
            break;
        }
        host.store_delete(&entries[i].key);
        count -= 1;
        bytes -= entries[i].bytes;
        out.overflowed += 1;
    }

    if out.any() {
        host.log(
            Level::Warn,
            &format!(
                "spool {prefix} evicted {} aged and {} overflowing batches; \
                 what was collected while offline is being dropped",
                out.aged, out.overflowed
            ),
        );
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testhost::TestHost;

    fn spool(host: &TestHost, ordinal: u64, created: i64, priority: Priority, bytes: usize) {
        let k = key(ordinal, created, priority, bytes);
        host.store_put(&k, &vec![b'x'; bytes]).unwrap();
    }

    #[test]
    fn a_key_round_trips() {
        let k = key(42, 1_789_567_142_123, Priority::Critical, 2048);
        let meta = parse(&k).expect("parses");
        assert_eq!(meta.ordinal, 42);
        assert_eq!(meta.created_ms, 1_789_567_142_123);
        assert_eq!(meta.priority, Priority::Critical);
        assert_eq!(meta.bytes, 2048);
    }

    #[test]
    fn a_foreign_key_is_ignored_rather_than_failing() {
        assert!(parse("ps.install").is_none());
        assert!(parse("ps.b.nonsense").is_none());
        assert!(parse("ps.b.1.2.3.4.5").is_none());
    }

    #[test]
    fn listing_is_oldest_first_even_past_a_lexicographic_cliff() {
        let host = TestHost::new();
        // 9 then 10 — the pair that sorts wrongly without zero padding.
        spool(&host, 10, 1_000, Priority::Normal, 10);
        spool(&host, 9, 1_000, Priority::Normal, 10);

        let ordinals: Vec<u64> = list(&host).iter().map(|m| m.ordinal).collect();
        assert_eq!(ordinals, vec![9, 10]);
    }

    #[test]
    fn overflow_drops_ordinary_events_before_critical_ones() {
        let host = TestHost::new();
        let mut config = Config::new("k", "https://e").sanitized();
        config.spool_max_batches = 2;

        spool(&host, 1, 1_000, Priority::Critical, 10); // a session start
        spool(&host, 2, 2_000, Priority::Normal, 10);
        spool(&host, 3, 3_000, Priority::Normal, 10);
        spool(&host, 4, 4_000, Priority::Critical, 10); // a purchase

        let evicted = enforce(&host, &config, 5_000);
        assert_eq!(evicted.overflowed, 2);

        let left: Vec<u64> = list(&host).iter().map(|m| m.ordinal).collect();
        assert_eq!(
            left,
            vec![1, 4],
            "the session start and the purchase survive"
        );
    }

    #[test]
    fn ageing_ignores_priority_because_it_is_not_a_capacity_decision() {
        let host = TestHost::new();
        let config = Config::new("k", "https://e").sanitized();

        let old = config.spool_max_age_ms + 1;
        spool(&host, 1, 0, Priority::Critical, 10);
        spool(&host, 2, old, Priority::Normal, 10);

        let evicted = enforce(&host, &config, old);
        assert_eq!(
            evicted.aged, 1,
            "a purchase the server would rewrite is still dropped"
        );
        assert_eq!(list(&host).len(), 1);
    }

    #[test]
    fn a_spool_inside_its_bounds_is_left_alone() {
        let host = TestHost::new();
        let config = Config::new("k", "https://e").sanitized();
        spool(&host, 1, 1_000, Priority::Normal, 10);

        assert!(!enforce(&host, &config, 2_000).any());
        assert_eq!(list(&host).len(), 1);
    }
}

//! Which players actually saw which variant (§44).
//!
//! A flag that nobody can measure is a flag nobody can learn from. An exposure event is the
//! record that *this* player, in *this* session, read *this* flag and got *that* value —
//! which is what connects a rollout to retention, conversion and revenue through the same
//! analytics pipeline everything else uses.
//!
//! # The problem is volume, and the answer is a set
//!
//! §44: "avoid sending a high-volume exposure event every frame/evaluation". A game reading a
//! flag in `Update` reads it sixty times a second, and an event per read would be an event
//! stream about the SDK rather than about the game. So an exposure is emitted **once per flag
//! per config version per session**, and every read after the first is a lookup in a small
//! set.
//!
//! Those three parts are each load-bearing:
//!
//! - **Per flag**, because two flags are two experiments.
//! - **Per config version**, because a publish can move a player between arms, and an
//!   analysis that could not see that would attribute the second arm's behaviour to the first.
//! - **Per session**, because retention is counted in sessions and a player who came back is
//!   the thing being measured.
//!
//! # A flag nobody published is still an exposure
//!
//! When the project has published nothing under that key, the game falls back to its own
//! default — and that player is in neither arm of the experiment. Recording it with
//! `published: false` is what lets an analysis say so, rather than quietly counting the
//! control group as smaller than it was. The value is deliberately absent: the core does not
//! know what the game's own default was, and inventing one would be worse than saying nothing.

use std::collections::BTreeSet;

/// The event name an exposure is recorded under.
///
/// A plain name, not a reserved prefix, because it travels through the same ingestion path as
/// everything else and the server classifies it by name (§19). A studio filtering their own
/// events sees it and can tell what it is.
pub const EVENT_NAME: &str = "feature_exposure";

/// Property names, fixed here so every binding and every query agrees.
pub mod property {
    /// Which flag was read.
    pub const FLAG: &str = "flag";
    /// What the player got. Absent when the project published nothing under that key.
    pub const VALUE: &str = "value";
    /// The published config version the answer came from. Zero before the first fetch.
    pub const VERSION: &str = "config_version";
    /// False when the game used its own default, which means this player is in no arm.
    pub const PUBLISHED: &str = "published";
}

/// Remembers which exposures have already been recorded this session.
///
/// A set of `(version, flag)` rather than a map keyed by flag, so that a publish landing
/// mid-session records every flag again exactly once — the pairs simply do not match the ones
/// already there, and nothing has to notice the version changed.
#[derive(Debug, Default)]
pub struct Exposures {
    seen: BTreeSet<(u32, String)>,
    /// Counted rather than derived from `seen.len()`, because the cap has to hold even for
    /// the reads that were refused by it.
    recorded: usize,
    capped: bool,
}

impl Exposures {
    /// Whether this read is the first of its kind, and therefore worth an event.
    ///
    /// Records the pair as a side effect: a caller that asked and then did not emit would
    /// lose the exposure, and losing one silently is worse than sending a duplicate.
    pub fn first_time(&mut self, flag: &str, version: u32, max: usize) -> bool {
        if self.recorded >= max {
            // Capped, and it stays capped for the session. A game that reads three hundred
            // flags is a game whose exposure stream would be larger than its analytics, and
            // a studio should discover that in a log line rather than on an invoice.
            return false;
        }
        // Allocates only on a miss, which is once per flag per session — the sixty-times-a-
        // second path is the `contains` above it.
        if self.seen.contains(&(version, flag.to_owned())) {
            return false;
        }
        self.seen.insert((version, flag.to_owned()));
        self.recorded += 1;
        true
    }

    /// Whether the cap has just been reached, so a caller can say so once.
    pub fn newly_capped(&mut self, max: usize) -> bool {
        if self.capped || self.recorded < max {
            return false;
        }
        self.capped = true;
        true
    }

    /// Forgets everything. Called when a session ends: the next one is a different player
    /// visit and is counted separately.
    pub fn clear(&mut self) {
        self.seen.clear();
        self.recorded = 0;
        self.capped = false;
    }

    /// How many have been recorded this session, for a test and for [`crate::Stats`].
    pub fn len(&self) -> usize {
        self.recorded
    }

    pub fn is_empty(&self) -> bool {
        self.recorded == 0
    }
}

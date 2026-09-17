//! The consent state machine (§5).
//!
//! Three states, and the transitions are where an SDK gets this wrong rather than the states
//! themselves.
//!
//! The rule that shapes everything here: **a batch carries exactly one consent block, which
//! applies to every event in it.** Consent sits on the batch and the actor sits on the event
//! (§17), which is precisely why a consent change must flush and a sign-in must not.

/// What the player has said.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum State {
    /// The dialog has not been answered. Events are collected **in memory only** — a spool
    /// is a copy on disk of data gathered under no basis.
    Unknown,
    Granted(Granted),
    /// Nothing is collected, nothing is kept.
    Denied,
}

/// The basis a batch is stamped with.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Granted {
    /// Which version of the terms the player agreed to. Required: it is what makes a later
    /// withdrawal scopeable to "everything collected under version N or earlier" (§97).
    pub policy_version: String,
    /// authorized, denied, not_determined or restricted. Recorded, never acted on.
    pub att_status: String,
    pub gdpr_applies: bool,
    /// RFC 3339, when the player answered.
    pub collected_at: String,
}

impl State {
    pub fn is_granted(&self) -> bool {
        matches!(self, State::Granted(_))
    }

    pub fn granted(&self) -> Option<&Granted> {
        match self {
            State::Granted(g) => Some(g),
            _ => None,
        }
    }
}

/// What the client must do when consent moves.
///
/// Returned rather than performed, so the rule is stated in one place and tested on its own.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Transition {
    /// Nothing changed.
    None,
    /// Unknown → granted. The in-memory buffer becomes lawful: stamp it and spool it.
    PromoteBuffer,
    /// Unknown → denied. The buffer was never lawful: discard it.
    DiscardBuffer,
    /// Granted → denied. **Flush first, under the old block.**
    ///
    /// Withdrawal is not retroactive: those events were collected lawfully and the consent
    /// version they were collected under travels on every row so that stays provable (§97,
    /// ADR 0006). Discarding them would throw away data the studio is entitled to, and
    /// sending them after the state flips would label them with a basis that was not theirs.
    FlushThenStop,
    /// Granted → granted under a different policy version. Flush, so no batch mixes two.
    FlushThenContinue,
}

/// Decides what a move from `from` to `to` requires.
pub fn transition(from: &State, to: &State) -> Transition {
    match (from, to) {
        (State::Unknown, State::Granted(_)) => Transition::PromoteBuffer,
        (State::Unknown, State::Denied) => Transition::DiscardBuffer,
        (State::Granted(_), State::Denied) => Transition::FlushThenStop,
        (State::Granted(a), State::Granted(b)) if a != b => Transition::FlushThenContinue,
        _ => Transition::None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn granted(version: &str) -> State {
        State::Granted(Granted {
            policy_version: version.into(),
            att_status: "authorized".into(),
            gdpr_applies: true,
            collected_at: "2026-09-17T10:00:00.000Z".into(),
        })
    }

    #[test]
    fn granting_promotes_what_was_held_in_memory() {
        assert_eq!(
            transition(&State::Unknown, &granted("v1")),
            Transition::PromoteBuffer
        );
    }

    #[test]
    fn denying_before_an_answer_discards_it() {
        assert_eq!(
            transition(&State::Unknown, &State::Denied),
            Transition::DiscardBuffer
        );
    }

    #[test]
    fn withdrawal_flushes_before_it_stops() {
        // The one that is easy to get backwards. Events collected under a valid grant stay
        // lawful; dropping them loses data the studio is entitled to.
        assert_eq!(
            transition(&granted("v1"), &State::Denied),
            Transition::FlushThenStop
        );
    }

    #[test]
    fn a_new_policy_version_flushes_so_no_batch_mixes_two() {
        assert_eq!(
            transition(&granted("v1"), &granted("v2")),
            Transition::FlushThenContinue
        );
    }

    #[test]
    fn re_granting_the_same_thing_changes_nothing() {
        assert_eq!(transition(&granted("v1"), &granted("v1")), Transition::None);
    }
}

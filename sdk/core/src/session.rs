//! Session lifecycle, owned by the SDK rather than by the game (§4).
//!
//! §30 says to do as much session definition as possible in the SDK, and the reason is
//! ergonomic before it is technical: a boundary a developer has to remember is a boundary
//! that will be wrong in one scene and missing in another.

use crate::host::Host;
use crate::ids::uuid_v4;

/// One continuous period of play.
#[derive(Clone, Debug)]
pub struct Session {
    pub id: String,
    sequence: i64,

    /// The wall clock and the monotonic clock at the moment this session began.
    ///
    /// Event timestamps are `anchor_wall + (monotonic_now - anchor_mono)`, which is the one
    /// design decision in this file worth arguing about. A player who moves their device
    /// clock mid-session — to skip a timer, usually — would otherwise produce events that
    /// interleave backwards, and no amount of server-side clamping can reorder them
    /// afterwards. Anchoring keeps a session internally ordered and still tied to real time
    /// at the moment it started.
    anchor_wall_ms: i64,
    anchor_mono_ms: i64,

    /// Monotonic time the app was last backgrounded, or `None` while it is in front.
    backgrounded_at_mono_ms: Option<i64>,
}

impl Session {
    pub fn start<H: Host + ?Sized>(host: &H) -> Self {
        Self {
            id: uuid_v4(host),
            sequence: 0,
            anchor_wall_ms: host.now_ms(),
            anchor_mono_ms: host.monotonic_ms(),
            backgrounded_at_mono_ms: None,
        }
    }

    /// The timestamp to stamp on an event happening now.
    pub fn event_time_ms(&self, monotonic_now_ms: i64) -> i64 {
        self.anchor_wall_ms + (monotonic_now_ms - self.anchor_mono_ms)
    }

    /// The next ordering number within this session.
    ///
    /// Monotonic within a session and nothing more. It is deliberately **not** what the
    /// gateway derives an event's identity from — that uses the event's position in its
    /// batch, because a sequence is per session and two events in one batch can legitimately
    /// share one (ADR 0026).
    pub fn next_sequence(&mut self) -> i64 {
        self.sequence += 1;
        self.sequence
    }

    pub fn foregrounded(&mut self) {
        self.backgrounded_at_mono_ms = None;
    }

    pub fn backgrounded(&mut self, monotonic_now_ms: i64) {
        self.backgrounded_at_mono_ms = Some(monotonic_now_ms);
    }

    pub fn is_backgrounded(&self) -> bool {
        self.backgrounded_at_mono_ms.is_some()
    }

    /// Whether this session has been in the background long enough to be over.
    ///
    /// Measured against the monotonic clock, so a player changing their device clock cannot
    /// end a session instantly or keep one alive forever.
    pub fn expired(&self, monotonic_now_ms: i64, timeout_ms: i64) -> bool {
        match self.backgrounded_at_mono_ms {
            Some(at) => monotonic_now_ms - at >= timeout_ms,
            None => false,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testhost::TestHost;

    #[test]
    fn event_times_follow_the_monotonic_clock_not_the_wall_clock() {
        let host = TestHost::new();
        host.set_wall_ms(1_700_000_000_000);
        let session = Session::start(&host);

        // The player sets their clock back a year to cheat an energy timer. The wall clock
        // moves; the events must not.
        host.set_wall_ms(1_700_000_000_000 - 365 * 24 * 3_600_000);

        let first = session.event_time_ms(host.monotonic_ms());
        host.advance_ms(5_000);
        let second = session.event_time_ms(host.monotonic_ms());

        assert_eq!(
            second - first,
            5_000,
            "five seconds of play is five seconds of events"
        );
        assert!(
            second > 1_700_000_000_000,
            "the anchor survived the clock change"
        );
    }

    #[test]
    fn a_session_ends_only_after_long_enough_in_the_background() {
        let host = TestHost::new();
        let mut session = Session::start(&host);

        host.advance_ms(1_000);
        session.backgrounded(host.monotonic_ms());

        host.advance_ms(29_000);
        assert!(
            !session.expired(host.monotonic_ms(), 30_000),
            "a phone call is not a new session"
        );

        host.advance_ms(2_000);
        assert!(session.expired(host.monotonic_ms(), 30_000));
    }

    #[test]
    fn a_foreground_session_never_expires_on_its_own() {
        let host = TestHost::new();
        let session = Session::start(&host);
        host.advance_ms(6 * 3_600_000);
        assert!(
            !session.expired(host.monotonic_ms(), 30_000),
            "six hours of play is one session"
        );
    }
}

//! What the studio says about the player, in the studio's own vocabulary.
//!
//! PlayerSense must never learn the shape of anybody's game. One studio has
//! `World → Chapter → Level → Zone`, the next has `Map → Region → Dungeon`, the next has
//! `Season → League → Match`, and the fourth has no progression at all. An SDK with
//! `SetLevel` and `SetChapter` fits the first studio and lies to the other three.
//!
//! So this is a key/value store with a scope, and nothing else. The studio picks the
//! vocabulary; targeting reads it back by name.
//!
//! # Authority, and why some names are refused
//!
//! Context is **client supplied**, which means it is whatever a modified build says it is.
//! That is fine for most targeting — a player who lies their way into a Halloween event has
//! stolen nothing — and disqualifying for the rest: "give the legendary sword if
//! `lifetime_spend > 100`" must never be answerable by a modded APK.
//!
//! The separation is by namespace, and it is a boundary rather than a convention:
//!
//! | namespace | authority | where it comes from |
//! |---|---|---|
//! | `geo.*`, `network.*` | **server** | the request's own IP, resolved against PlayerSense's databases |
//! | `app.*`, `device.*`, `sdk.*`, `install.*` | **SDK** | generated here, not settable |
//! | `account.*`, `commerce.*` | **server** | the studio's backend, over a server credential |
//! | `context.*` | **studio** | this module |
//!
//! Everything in this file lands under `context.*` and can reach no other namespace. A
//! studio that writes `Set("geo.country", "US")` is refused by name rather than quietly
//! filed under `context.geo.country`, because the failure it is heading for — a targeting
//! rule on `geo.country` that never matches — is one nobody debugs quickly.
//!
//! The SDK does no geolocation of its own. No MMDB, no VPN detection, no ASN lookup: the
//! server sees the connection and resolves it far better than a game client could, and a
//! value the client never sends is a value the client cannot forge.

use std::collections::BTreeMap;

use crate::event::Value;

/// How long a context value lives.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Scope {
    /// Until the session ends. Where the player is and what they are doing right now:
    /// `game_mode`, `map`, `difficulty`.
    Session,
    /// Until the player changes. What is true of them: `progress.level`, `rank`,
    /// `tutorial.completed`.
    Player,
}

/// Namespaces a studio may not write into, and who owns each.
///
/// Ordered longest-prefix-irrelevant: these are compared as prefixes of the whole key.
const RESERVED: &[(&str, &str)] = &[
    ("geo.", "resolved by PlayerSense from the request address"),
    (
        "network.",
        "resolved by PlayerSense from the request address",
    ),
    ("app.", "sent automatically from the SDK configuration"),
    ("device.", "sent automatically from the SDK configuration"),
    ("sdk.", "sent automatically from the SDK configuration"),
    ("install.", "derived by PlayerSense from the installation"),
    (
        "account.",
        "supplied by your backend over a server credential",
    ),
    (
        "commerce.",
        "supplied by PlayerSense from verified purchases",
    ),
    ("playersense.", "reserved"),
];

/// Bounds, so a game that sets context in a loop cannot grow without end.
pub const MAX_KEYS_PER_SCOPE: usize = 64;
pub const MAX_KEY_CHARS: usize = 96;
pub const MAX_VALUE_CHARS: usize = 256;

/// Why a context key was refused.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Rejected {
    /// Empty, too long, or outside `[a-zA-Z0-9_.-]`.
    Key,
    /// A namespace the studio does not own, with the owner named.
    Reserved(&'static str),
    /// This scope already holds as many keys as it may.
    Full,
}

impl std::fmt::Display for Rejected {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Rejected::Key => write!(
                f,
                "a context key must be 1 to {MAX_KEY_CHARS} characters of [a-zA-Z0-9_.-]"
            ),
            Rejected::Reserved(owner) => write!(
                f,
                "that namespace is reserved: it is {owner}. Use a name of your own — \
                 targeting sees your keys under `context.`"
            ),
            Rejected::Full => write!(f, "a scope may hold at most {MAX_KEYS_PER_SCOPE} keys"),
        }
    }
}

/// One change to the context, applying from event `at` onward within a batch.
///
/// The batch carries a timeline rather than repeating the context on every event. Frame 0
/// always carries a full `values` snapshot, so a batch is a complete transport unit that the
/// gateway can expand without remembering the batch before it — the property deduplication
/// and retries already rest on (ADR 0026).
///
/// Later frames are deltas. `values` replaces the whole context, then `set` applies, then
/// `unset` removes: a fixed order so a frame carrying more than one of them is unambiguous.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Frame {
    /// The index, within this batch's events, from which this frame applies.
    pub at: usize,
    /// A full snapshot. Always present on frame 0, and allowed on any frame.
    pub values: Option<BTreeMap<String, Value>>,
    pub set: BTreeMap<String, Value>,
    pub unset: Vec<String>,
}

impl Frame {
    pub fn is_empty(&self) -> bool {
        self.values.is_none() && self.set.is_empty() && self.unset.is_empty()
    }
}

/// What changed between two states of the context.
///
/// Returned as a frame body rather than applied, because the caller decides whether a change
/// belongs in a delta or folds into the snapshot the next batch opens with.
pub fn diff(
    before: &BTreeMap<String, Value>,
    after: &BTreeMap<String, Value>,
) -> (BTreeMap<String, Value>, Vec<String>) {
    let mut set = BTreeMap::new();
    for (key, value) in after {
        if before.get(key) != Some(value) {
            set.insert(key.clone(), value.clone());
        }
    }
    let unset = before
        .keys()
        .filter(|key| !after.contains_key(*key))
        .cloned()
        .collect();
    (set, unset)
}

/// The studio's description of the player and the moment.
#[derive(Debug, Default)]
pub struct Context {
    session: BTreeMap<String, Value>,
    player: BTreeMap<String, Value>,

    /// Which player the player-scoped values belong to, so a sign-in that changes the actor
    /// takes its context with it rather than leaking one player's rank onto the next.
    owner: Option<String>,

    /// Bumped on every change. Targeting re-evaluation watches this rather than diffing
    /// maps, and it is what lets a burst of `Set` calls collapse into one refresh.
    revision: u64,
}

impl Context {
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets a value, or says why it was refused.
    pub fn set(&mut self, scope: Scope, key: &str, value: Value) -> Result<(), Rejected> {
        let key = check_key(key)?;
        let value = clamp(value);

        let map = match scope {
            Scope::Session => &mut self.session,
            Scope::Player => &mut self.player,
        };
        if !map.contains_key(&key) && map.len() >= MAX_KEYS_PER_SCOPE {
            return Err(Rejected::Full);
        }
        if map.get(&key) == Some(&value) {
            // Setting a value to what it already is must not look like a change, or a game
            // that sets its context every frame re-evaluates targeting every frame.
            return Ok(());
        }
        map.insert(key, value);
        self.revision += 1;
        Ok(())
    }

    pub fn remove(&mut self, scope: Scope, key: &str) {
        let map = match scope {
            Scope::Session => &mut self.session,
            Scope::Player => &mut self.player,
        };
        if map.remove(key).is_some() {
            self.revision += 1;
        }
    }

    /// Everything the config request carries, session values winning a clash.
    ///
    /// Session over player because session is the narrower statement: a player whose rank is
    /// `diamond` playing a `casual` match is, right now, in a casual match.
    pub fn merged(&self) -> BTreeMap<String, Value> {
        let mut out = self.player.clone();
        out.extend(self.session.iter().map(|(k, v)| (k.clone(), v.clone())));
        out
    }

    pub fn revision(&self) -> u64 {
        self.revision
    }

    pub fn is_empty(&self) -> bool {
        self.session.is_empty() && self.player.is_empty()
    }

    /// A new session clears what was true of the last one.
    pub fn session_started(&mut self) {
        if !self.session.is_empty() {
            self.session.clear();
            self.revision += 1;
        }
    }

    /// The actor changed. Player-scoped values belong to the actor, so they go with them.
    ///
    /// Returns whether anything was actually dropped, so a caller can tell a sign-in that
    /// changed the player from one that merely re-stated it.
    pub fn player_changed(&mut self, player_id: Option<&str>) -> bool {
        if self.owner.as_deref() == player_id {
            return false;
        }
        self.owner = player_id.map(str::to_owned);
        if self.player.is_empty() {
            return false;
        }
        self.player.clear();
        self.revision += 1;
        true
    }

    /// The player-scoped half, for persistence.
    pub fn player_values(&self) -> (Option<&str>, &BTreeMap<String, Value>) {
        (self.owner.as_deref(), &self.player)
    }

    /// Adopts a persisted player scope **and the actor it belonged to**.
    ///
    /// Called once, at construction, before anybody has signed in — so it restores the last
    /// run's state along with whose it was. The guard against one player inheriting
    /// another's context is [`Context::player_changed`], which fires on the sign-in that
    /// follows: re-signing in as the same player keeps it, and anybody else clears it.
    ///
    /// Adopting the owner is what makes that work. Restoring only the values would leave
    /// them attached to nobody, and the next sign-in — even by the player they belong to —
    /// would read as a change and throw them away.
    pub fn restore(&mut self, owner: Option<&str>, values: BTreeMap<String, Value>) {
        self.owner = owner.map(str::to_owned);
        self.player = values;
        self.revision += 1;
    }
}

fn check_key(key: &str) -> Result<String, Rejected> {
    if key.is_empty()
        || key.chars().count() > MAX_KEY_CHARS
        || !key
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'))
    {
        return Err(Rejected::Key);
    }
    let lowered = key.to_ascii_lowercase();
    for (prefix, owner) in RESERVED {
        if lowered.starts_with(prefix) {
            return Err(Rejected::Reserved(owner));
        }
    }
    Ok(lowered)
}

fn clamp(value: Value) -> Value {
    match value {
        Value::Str(v) if v.chars().count() > MAX_VALUE_CHARS => {
            Value::Str(v.chars().take(MAX_VALUE_CHARS).collect())
        }
        other => other,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx() -> Context {
        Context::new()
    }

    fn map(pairs: &[(&str, Value)]) -> BTreeMap<String, Value> {
        pairs
            .iter()
            .map(|(k, v)| ((*k).to_owned(), v.clone()))
            .collect()
    }

    #[test]
    fn a_diff_names_what_changed_and_what_went() {
        let before = map(&[
            ("rank", Value::Str("gold".into())),
            ("map", Value::Str("desert".into())),
        ]);
        let after = map(&[
            ("rank", Value::Str("diamond".into())),
            ("chapter", Value::Int(4)),
        ]);

        let (set, unset) = diff(&before, &after);
        assert_eq!(
            set,
            map(&[
                ("rank", Value::Str("diamond".into())),
                ("chapter", Value::Int(4))
            ])
        );
        assert_eq!(unset, vec!["map".to_owned()]);
    }

    #[test]
    fn an_unchanged_context_diffs_to_nothing() {
        let same = map(&[("rank", Value::Str("gold".into()))]);
        let (set, unset) = diff(&same, &same);
        assert!(set.is_empty() && unset.is_empty());
    }

    #[test]
    fn a_studio_uses_its_own_vocabulary() {
        let mut c = ctx();
        c.set(Scope::Player, "progress.chapter", Value::Int(6))
            .unwrap();
        c.set(Scope::Player, "rank", Value::Str("diamond".into()))
            .unwrap();
        c.set(Scope::Session, "game_mode", Value::Str("ranked".into()))
            .unwrap();

        // Another studio's words work exactly as well, which is the whole point.
        let mut other = ctx();
        other
            .set(Scope::Player, "combat_level", Value::Int(78))
            .unwrap();
        other
            .set(Scope::Session, "activity", Value::Str("boss_raid".into()))
            .unwrap();

        assert_eq!(c.merged().len(), 3);
        assert_eq!(other.merged().len(), 2);
    }

    #[test]
    fn a_studio_cannot_write_into_a_namespace_it_does_not_own() {
        let mut c = ctx();

        // The one that matters: geography is resolved from the connection, and a client that
        // could assert it could target itself into anything.
        assert!(matches!(
            c.set(Scope::Player, "geo.country", Value::Str("US".into())),
            Err(Rejected::Reserved(_))
        ));
        assert!(matches!(
            c.set(Scope::Session, "network.vpn", Value::Bool(false)),
            Err(Rejected::Reserved(_))
        ));
        assert!(matches!(
            c.set(
                Scope::Player,
                "account.verified_lifetime_spend",
                Value::Int(10_000)
            ),
            Err(Rejected::Reserved(_))
        ));
        assert!(c.is_empty());
    }

    #[test]
    fn a_refusal_says_who_owns_the_name() {
        let mut c = ctx();
        let Err(rejected) = c.set(Scope::Player, "GEO.country", Value::Str("US".into())) else {
            panic!("case must not be a way around it");
        };
        assert!(
            rejected.to_string().contains("request address"),
            "{rejected}"
        );
    }

    #[test]
    fn the_narrower_statement_wins() {
        let mut c = ctx();
        c.set(Scope::Player, "mode", Value::Str("ranked".into()))
            .unwrap();
        c.set(Scope::Session, "mode", Value::Str("casual".into()))
            .unwrap();

        assert_eq!(c.merged()["mode"], Value::Str("casual".into()));
    }

    #[test]
    fn setting_a_value_to_what_it_already_is_is_not_a_change() {
        let mut c = ctx();
        c.set(Scope::Session, "map", Value::Str("desert_01".into()))
            .unwrap();
        let after_first = c.revision();

        // A game that sets its context in Update() must not re-evaluate targeting per frame.
        for _ in 0..1_000 {
            c.set(Scope::Session, "map", Value::Str("desert_01".into()))
                .unwrap();
        }
        assert_eq!(c.revision(), after_first);
    }

    #[test]
    fn a_new_session_forgets_what_was_true_of_the_last_one() {
        let mut c = ctx();
        c.set(Scope::Player, "rank", Value::Str("diamond".into()))
            .unwrap();
        c.set(Scope::Session, "map", Value::Str("desert_01".into()))
            .unwrap();

        c.session_started();

        assert!(
            c.merged().contains_key("rank"),
            "the player is still the player"
        );
        assert!(!c.merged().contains_key("map"), "the map is not");
    }

    #[test]
    fn one_players_context_does_not_leak_onto_the_next() {
        let mut c = ctx();
        c.player_changed(Some("player-a"));
        c.set(Scope::Player, "rank", Value::Str("diamond".into()))
            .unwrap();

        assert!(c.player_changed(Some("player-b")));
        assert!(
            !c.merged().contains_key("rank"),
            "player B inherited player A's rank"
        );
    }

    #[test]
    fn restating_the_same_player_keeps_their_context() {
        let mut c = ctx();
        c.player_changed(Some("player-a"));
        c.set(Scope::Player, "rank", Value::Str("diamond".into()))
            .unwrap();

        assert!(!c.player_changed(Some("player-a")));
        assert!(c.merged().contains_key("rank"));
    }

    #[test]
    fn a_restored_scope_survives_the_player_it_belongs_to_signing_in_again() {
        let mut c = ctx();
        let mut saved = BTreeMap::new();
        saved.insert("rank".to_owned(), Value::Str("diamond".into()));
        c.restore(Some("player-a"), saved);

        assert!(
            !c.player_changed(Some("player-a")),
            "the same player is not a change"
        );
        assert!(c.merged().contains_key("rank"));
    }

    #[test]
    fn a_restored_scope_is_dropped_when_somebody_else_signs_in() {
        let mut c = ctx();
        let mut saved = BTreeMap::new();
        saved.insert("rank".to_owned(), Value::Str("diamond".into()));
        c.restore(Some("player-a"), saved);

        assert!(c.player_changed(Some("player-b")));
        assert!(c.is_empty(), "player B would be targeted as player A");
    }

    #[test]
    fn the_scope_is_bounded() {
        let mut c = ctx();
        for i in 0..MAX_KEYS_PER_SCOPE {
            c.set(Scope::Session, &format!("k{i}"), Value::Int(1))
                .unwrap();
        }
        assert_eq!(
            c.set(Scope::Session, "one_more", Value::Int(1)),
            Err(Rejected::Full)
        );
        // And an existing key can still be updated, because that does not grow anything.
        assert!(c.set(Scope::Session, "k0", Value::Int(2)).is_ok());
    }

    #[test]
    fn a_long_value_is_trimmed_rather_than_refused() {
        let mut c = ctx();
        c.set(Scope::Player, "bio", Value::Str("x".repeat(10_000)))
            .unwrap();

        let Value::Str(stored) = &c.merged()["bio"] else {
            panic!()
        };
        assert_eq!(stored.chars().count(), MAX_VALUE_CHARS);
    }
}

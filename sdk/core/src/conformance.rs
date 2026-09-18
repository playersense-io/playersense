//! §13 of `docs/sdk/protocol.md`, executable.
//!
//! Every scenario the protocol document walks through, against a fake host: a clock the test
//! moves, a store it can inspect, and responses it writes. Nothing here sleeps, touches a
//! network or needs a device — which is the whole reason the core does no I/O.
//!
//! Three of these are the ones §15 singles out, because each fails silently and corrupts
//! data rather than losing it: the identifier's lifetime, the consent boundary, and the
//! immutability a redelivery depends on.

use std::collections::BTreeMap;

use serde_json::Value as Json;

use crate::client::{Client, granted};
use crate::config::Config;
use crate::consent::State;
use crate::context::Scope;
use crate::event::{Recorded, Rejected, Value};
use crate::host::Host;
use crate::liveconfig::Flag;
use crate::spool;
use crate::testhost::{TestHost, accepted, status, unreachable};

fn config() -> Config {
    let mut config = Config::new("ps_project_live_test", "https://ingest.eu-1.example");
    config.sdk_name = "unity".into();
    config.sdk_version = "1.4.0".into();
    config.app_version = "2.7.1".into();
    config.platform = "ios".into();
    config
}

fn consenting(host: TestHost) -> Client<TestHost> {
    let mut client = Client::new(host, config());
    client.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));
    client
}

fn props(pairs: &[(&str, Value)]) -> BTreeMap<String, Value> {
    pairs
        .iter()
        .map(|(k, v)| ((*k).to_owned(), v.clone()))
        .collect()
}

/// The envelope of the request the host was last asked to send.
fn last_envelope(client: &Client<TestHost>) -> Json {
    let sent = client.host().take_sent();
    let request = sent.last().expect("a request was sent");
    let body = match request.content_encoding {
        Some("gzip") => gunzip(&request.body),
        _ => request.body.clone(),
    };
    serde_json::from_slice(&body).expect("the body is JSON")
}

fn gunzip(body: &[u8]) -> Vec<u8> {
    use flate2::read::GzDecoder;
    use std::io::Read;
    let mut out = Vec::new();
    GzDecoder::new(body)
        .read_to_end(&mut out)
        .expect("valid gzip");
    out
}

// ---- identity ---------------------------------------------------------------

/// §13.1 — the identifier survives an app update.
#[test]
fn the_install_id_survives_a_relaunch() {
    let host = TestHost::new();
    let first = Client::new(host, config());
    let id = first.install_id().to_owned();
    let disk = first.host().surviving_store();

    let second = Client::new(TestHost::with_store(disk), config());
    assert_eq!(
        second.install_id(),
        id,
        "an update must not look like a new install"
    );
}

/// §13.2 — and does not survive an uninstall.
///
/// An uninstall is the store going away, which is what a fresh `TestHost` is. The platform
/// half of this — that the file is not in `NSUserDefaults` or `SharedPreferences`, both of
/// which are copied by iCloud and Android Auto Backup — is a binding's test, not the core's,
/// and §13.3 is where it lives. Unity's is
/// `sdk/unity/Tests/Runtime/BoundaryTests.cs::TheInstallIdIsKeptOutOfPlatformBackup`.
#[test]
fn a_reinstall_is_a_new_install() {
    let first = Client::new(TestHost::new(), config());
    let second = Client::new(TestHost::new(), config());
    assert_ne!(first.install_id(), second.install_id());
}

/// §13.4 — signing in does not flush, and a batch may straddle it.
#[test]
fn signing_in_does_not_split_a_batch() {
    let mut client = consenting(TestHost::new());
    // The session batch is created by the first event and goes at once, so it has to be
    // answered before anything else can be sent — only one request is ever in flight (§6).
    client.track("game_started", BTreeMap::new()).unwrap();
    settle(&mut client);

    client.track("level_started", BTreeMap::new()).unwrap();
    client.set_player(Some("player-91827364".into()));
    client.track("level_completed", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    let events = envelope["events"].as_array().unwrap();
    assert_eq!(events.len(), 2, "a sign-in must not seal the batch");
    assert!(
        events[0].get("player_id").is_none(),
        "before the sign-in: no actor"
    );
    assert_eq!(
        events[1]["player_id"], "player-91827364",
        "after it: an actor"
    );
}

/// Answers whatever is outstanding, so a test can get past the session batch.
///
/// Only one request is ever in flight (§6), so a test that does not answer the first one
/// silently asserts against it rather than against what it just did.
fn settle(client: &mut Client<TestHost>) {
    while let Some(request) = client.host().take_sent().pop() {
        client.on_response(accepted(request.id, 1));
    }
}

// ---- durability -------------------------------------------------------------

/// §13.5 — a batch reaches the disk before the first attempt.
#[test]
fn a_batch_is_spooled_before_it_is_sent() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();
    client.flush();

    let spooled = spool::list(client.host());
    assert_eq!(
        spooled.len(),
        1,
        "one batch, holding the session start and the level event"
    );
    assert!(
        client.host().sent_count() > 0,
        "and it was on disk before the attempt"
    );

    let body = client
        .host()
        .store_get(&spooled[0].key)
        .expect("the batch is readable");
    let envelope: Json = serde_json::from_slice(&body).unwrap();
    assert_eq!(envelope["events"].as_array().unwrap().len(), 2);
}

/// §13.6 — the process dies between spooling and the 202, and nothing is lost.
///
/// The scenario §11 calls "killed with no callback": a force quit, an out-of-memory kill, a
/// crash. Everything spooled survives and is drained on the next launch.
#[test]
fn a_batch_survives_a_process_that_never_came_back() {
    let mut client = consenting(TestHost::new());
    client
        .track("level_completed", props(&[("level", Value::Int(12))]))
        .unwrap();
    client.flush();
    client.host().take_sent();

    // No response ever arrives. The process is gone.
    let disk = client.host().surviving_store();
    drop(client);

    let mut relaunched = consenting(TestHost::with_store(disk));
    relaunched.tick();

    let envelope = last_envelope(&relaunched);
    let names: Vec<&str> = envelope["events"]
        .as_array()
        .unwrap()
        .iter()
        .map(|e| e["event_name"].as_str().unwrap())
        .collect();
    assert!(
        names.contains(&"level_completed"),
        "the spool is drained on the next launch: {names:?}"
    );
}

/// §13.7 — a retry is byte-identical, which is what deduplication rests on.
///
/// The gateway derives each event's identity from the batch id and the event's position
/// (ADR 0026), so a client that re-packed a batch between attempts would give the same
/// events different identities and defeat it.
#[test]
fn a_retried_batch_is_identical_to_the_first_attempt() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();
    client.flush();

    let first = client.host().take_sent().pop().expect("sent once");
    client.on_response(unreachable(first.id));

    // Backoff has to elapse before the next attempt, which is itself the behaviour §10 wants.
    client.host().advance_ms(60_000);
    client.tick();

    let second = client.host().take_sent().pop().expect("sent again");
    assert_eq!(
        first.body, second.body,
        "the bytes must not change between attempts"
    );
}

/// §13.9 — entries past the age bound are dropped rather than sent.
///
/// Seven days is the gateway's skew window: past it every timestamp is silently rewritten,
/// so uploading is reporting a fiction (§6).
#[test]
fn a_spool_older_than_the_skew_window_is_dropped() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();
    client.flush();
    client.host().take_sent();
    assert!(!spool::list(client.host()).is_empty());

    let disk = client.host().surviving_store();
    let host = TestHost::with_store(disk);
    host.advance_ms(8 * 24 * 3_600_000);

    let relaunched = consenting(host);
    assert!(
        spool::list(relaunched.host()).is_empty(),
        "eight-day-old batches are not uploaded"
    );
}

/// §13.10 — a full spool drops ordinary events before critical ones.
#[test]
fn a_full_spool_keeps_the_purchases() {
    let host = TestHost::new();
    let mut settings = config();
    settings.spool_max_batches = 2;
    let mut client = Client::new(host, settings);
    client.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));

    // A purchase, then chatter. The chatter must go first.
    client
        .track_revenue("iap_completed", 4_990_000, "USD", BTreeMap::new())
        .unwrap();
    for i in 0..6 {
        client
            .track(&format!("chatter_{i}"), BTreeMap::new())
            .unwrap();
        client.flush();
    }

    let names = spooled_event_names(&client);
    assert!(
        names.iter().any(|n| n == "iap_completed"),
        "revenue was evicted before ordinary events: {names:?}"
    );
}

fn spooled_event_names(client: &Client<TestHost>) -> Vec<String> {
    spool::list(client.host())
        .iter()
        .filter_map(|meta| client.host().store_get(&meta.key))
        .filter_map(|body| serde_json::from_slice::<Json>(&body).ok())
        .flat_map(|env| {
            env["events"]
                .as_array()
                .map(|a| {
                    a.iter()
                        .filter_map(|e| e["event_name"].as_str().map(str::to_owned))
                        .collect::<Vec<_>>()
                })
                .unwrap_or_default()
        })
        .collect()
}

/// §13.11 — a corrupt entry is discarded without crashing or blocking the rest.
#[test]
fn an_unreadable_spool_entry_is_discarded_rather_than_retried_forever() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();
    settle(&mut client);

    client.track("level_completed", BTreeMap::new()).unwrap();
    client.flush();

    // The attempt fails, so the batch stays spooled and nothing is in flight.
    let request = client.host().take_sent().pop().expect("sent");
    client.on_response(unreachable(request.id));

    // A truncated write: the key is there and the value is not.
    let first = spool::list(client.host())
        .first()
        .expect("a spooled batch")
        .clone();
    client.host().store_put(&first.key, b"").unwrap();

    client.host().advance_ms(600_000);
    client.tick();

    assert!(
        !spool::list(client.host())
            .iter()
            .any(|m| m.key == first.key),
        "the unreadable entry is still there and would be retried forever"
    );
    assert!(client.host().logged("unreadable"));
}

/// The storage failing is a degraded mode, not a crash (§6).
#[test]
fn a_full_disk_does_not_lose_the_game() {
    let host = TestHost::new();
    host.fail_storage(true);

    let mut client = consenting(host);
    for i in 0..500 {
        client
            .track(&format!("event_{i}"), BTreeMap::new())
            .unwrap();
    }
    client.flush();

    assert!(client.host().logged("could not write a batch"));
    assert!(client.stats().events_tracked > 0, "tracking kept working");
}

// ---- sessions ---------------------------------------------------------------

/// §13.12 — the first event of a session is preceded by `session_start`.
#[test]
fn the_first_event_is_preceded_by_a_session_start() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();
    client.flush();

    let names = spooled_event_names(&client);
    assert_eq!(
        names.first().map(String::as_str),
        Some("session_start"),
        "{names:?}"
    );
    assert!(names.contains(&"game_started".to_owned()), "{names:?}");
}

/// §13.13 — and it does not wait for a batching interval.
#[test]
fn a_session_start_is_sent_at_once() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();

    // No flush, no tick, no elapsed interval.
    assert!(
        client.host().sent_count() > 0,
        "the install would go uncounted for 30 seconds"
    );
}

/// §13.14 and §13.15 — a phone call is not a new session; a lunch break is.
#[test]
fn a_session_survives_a_brief_interruption_and_not_a_long_one() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();
    client.flush();
    let first = session_ids(&client);

    client.background();
    client.host().advance_ms(20_000);
    client.foreground();
    client.track("level_completed", BTreeMap::new()).unwrap();
    client.flush();
    assert_eq!(
        session_ids(&client),
        first,
        "twenty seconds away is the same session"
    );

    client.background();
    client.host().advance_ms(45_000);
    client.foreground();
    client.track("level_started", BTreeMap::new()).unwrap();
    client.flush();
    assert!(
        session_ids(&client).len() > first.len(),
        "forty-five seconds away is a new one"
    );
}

fn session_ids(client: &Client<TestHost>) -> std::collections::BTreeSet<String> {
    spool::list(client.host())
        .iter()
        .filter_map(|meta| client.host().store_get(&meta.key))
        .filter_map(|body| serde_json::from_slice::<Json>(&body).ok())
        .flat_map(|env| {
            env["events"]
                .as_array()
                .map(|a| {
                    a.iter()
                        .filter_map(|e| e["session_id"].as_str().map(str::to_owned))
                        .collect::<Vec<_>>()
                })
                .unwrap_or_default()
        })
        .collect()
}

// ---- consent ----------------------------------------------------------------

/// §13.16 — nothing reaches disk while consent is unknown.
///
/// The one that is easy to get wrong by being helpful: spooling "just in case" is writing a
/// copy on disk of data collected under no basis.
#[test]
fn nothing_touches_the_disk_before_the_player_has_answered() {
    let mut client = Client::new(TestHost::new(), config());

    for i in 0..50 {
        client
            .track(&format!("event_{i}"), BTreeMap::new())
            .unwrap();
    }
    client.flush();

    assert!(
        spool::list(client.host()).is_empty(),
        "events were spooled under no consent"
    );
    assert_eq!(
        client.host().sent_count(),
        0,
        "events were sent under no consent"
    );
}

/// §13.17 — granting promotes what was held, denying discards it.
#[test]
fn granting_promotes_the_buffer_and_denying_discards_it() {
    let mut client = Client::new(TestHost::new(), config());
    client.track("game_started", BTreeMap::new()).unwrap();
    client.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));
    client.flush();

    assert!(
        spooled_event_names(&client).contains(&"game_started".to_owned()),
        "what was collected before the dialog was answered is now lawful"
    );

    let mut refused = Client::new(TestHost::new(), config());
    refused.track("game_started", BTreeMap::new()).unwrap();
    refused.set_consent(State::Denied);
    refused.flush();

    assert!(
        spool::list(refused.host()).is_empty(),
        "a refusal keeps nothing"
    );
}

/// §13.34 — `track` says which of the three things happened to an event.
///
/// Before this returned anything but `Ok(())`, a binding could not tell a queued event from
/// one thrown away because the player declined, so a diagnostics screen could not either —
/// and "my events are not arriving" had no answer on the device.
#[test]
fn track_reports_whether_an_event_was_queued_held_or_declined() {
    let mut unanswered = Client::new(TestHost::new(), config());
    assert_eq!(
        unanswered.track("game_started", BTreeMap::new()),
        Ok(Recorded::AwaitingConsent),
        "before the dialog is answered an event is held, not delivered and not discarded"
    );

    let mut granted_client = consenting(TestHost::new());
    assert_eq!(
        granted_client.track("level_started", BTreeMap::new()),
        Ok(Recorded::Queued)
    );

    let mut refused = Client::new(TestHost::new(), config());
    refused.set_consent(State::Denied);
    assert_eq!(
        refused.track("level_started", BTreeMap::new()),
        Ok(Recorded::Declined),
        "a refusal is the SDK working, so it is an outcome and not an error"
    );
    assert_eq!(refused.stats().events_without_consent, 1);
    assert_eq!(
        refused.stats().events_tracked,
        0,
        "a declined event was not recorded, so it must not be counted as one"
    );
}

/// §13.35 — a malformed event is reported even to a client that is collecting nothing.
///
/// The consent check used to come first, so a developer testing with analytics declined got
/// `Ok` for a misspelled event name and found out in production. Validation reads its
/// arguments and stores nothing, so there is no reason for consent to gate it.
#[test]
fn a_bad_name_is_refused_even_when_the_player_has_declined() {
    let mut refused = Client::new(TestHost::new(), config());
    refused.set_consent(State::Denied);

    assert_eq!(
        refused.track("Level Started", BTreeMap::new()),
        Err(Rejected::Name),
        "a declining player must not hide an integration bug"
    );
    assert_eq!(refused.stats().events_rejected, 1);
    assert_eq!(
        refused.stats().events_without_consent,
        0,
        "a malformed event is a bug, not a consent statistic"
    );
}

/// §13.18 — withdrawal flushes under the previous block before it stops collection.
///
/// Withdrawal is not retroactive. Those events were collected lawfully and the version they
/// were collected under travels with them, which is what keeps that provable (§97).
#[test]
fn withdrawal_sends_what_was_lawfully_collected_and_then_stops() {
    let mut client = consenting(TestHost::new());
    client.track("level_completed", BTreeMap::new()).unwrap();

    client.set_consent(State::Denied);

    let names = spooled_event_names(&client);
    assert!(
        names.contains(&"level_completed".to_owned()),
        "lawful events were thrown away: {names:?}"
    );

    let before = spool::list(client.host()).len();
    client.track("after_withdrawal", BTreeMap::new()).unwrap();
    client.flush();
    assert_eq!(
        spool::list(client.host()).len(),
        before,
        "collection continued after a refusal"
    );
}

/// §13.19 — no batch ever carries two consent states.
#[test]
fn a_new_policy_version_starts_a_new_batch() {
    let mut client = consenting(TestHost::new());
    client.host().take_sent();
    client.track("under_v1", BTreeMap::new()).unwrap();

    client.set_consent(granted("2026-09-01", "authorized", true, 1_789_560_000_000));
    client.track("under_v2", BTreeMap::new()).unwrap();
    client.flush();

    for meta in spool::list(client.host()) {
        let body = client.host().store_get(&meta.key).unwrap();
        let envelope: Json = serde_json::from_slice(&body).unwrap();
        let version = envelope["consent"]["policy_version"]
            .as_str()
            .unwrap()
            .to_owned();
        let names: Vec<&str> = envelope["events"]
            .as_array()
            .unwrap()
            .iter()
            .map(|e| e["event_name"].as_str().unwrap())
            .collect();

        if names.contains(&"under_v1") {
            assert_eq!(
                version, "2026-01-15",
                "an event was relabelled with a later basis"
            );
        }
        if names.contains(&"under_v2") {
            assert_eq!(version, "2026-09-01");
        }
    }
}

// ---- transport --------------------------------------------------------------

/// §13.20 — the body is gzip and decodes to the events.
#[test]
fn the_body_is_compressed_and_decodes_to_what_was_tracked() {
    let mut client = consenting(TestHost::new());
    client
        .track("level_completed", props(&[("level", Value::Int(12))]))
        .unwrap();
    client.flush();

    let sent = client.host().take_sent();
    let request = sent.last().unwrap();
    assert_eq!(request.content_encoding, Some("gzip"));

    let envelope: Json = serde_json::from_slice(&gunzip(&request.body)).unwrap();
    assert_eq!(envelope["schema_version"], 1);
    assert_eq!(envelope["sdk"]["name"], "unity");
}

/// §13.21 — `event_id` is not sent.
///
/// Sixteen bytes of entropy an event, which no compressor reduces and the gateway does not
/// need: it derives the identity from the batch and the event's position (ADR 0026).
#[test]
fn no_event_carries_an_identifier() {
    let mut client = consenting(TestHost::new());
    client.track("level_completed", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    for event in envelope["events"].as_array().unwrap() {
        assert!(
            event.get("event_id").is_none(),
            "the SDK is spending bytes on a derived field"
        );
    }
}

/// §13.22 — a 413 splits rather than drops.
#[test]
fn a_batch_the_gateway_calls_too_large_is_split() {
    let mut client = consenting(TestHost::new());
    client.host().take_sent();
    for i in 0..8 {
        client
            .track(&format!("event_{i}"), BTreeMap::new())
            .unwrap();
    }
    client.flush();

    let before = spool::list(client.host()).len();
    let request = client.host().take_sent().pop().expect("sent");
    client.on_response(status(request.id, 413));

    assert_eq!(
        spool::list(client.host()).len(),
        before + 1,
        "one batch became two"
    );
    assert!(
        spooled_event_names(&client).len() >= 8,
        "no events were lost in the split"
    );
}

/// §13.23 — a 421 redirects, and the address is remembered.
#[test]
fn a_misdirected_batch_is_retried_against_the_cell_that_owns_the_project() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();

    let request = client.host().take_sent().pop().expect("sent");
    assert!(request.url.starts_with("https://ingest.eu-1.example"));

    client.on_response(crate::host::Response {
        id: request.id,
        status: 421,
        retry_after_ms: None,
        etag: None,
        body: br#"{"error":{"code":"wrong_cell","message":"this project belongs to cell us-1",
            "details":[{"field":"ingest_url","issue":"https://ingest.us-1.example"}]}}"#
            .to_vec(),
    });

    let retried = client.host().take_sent().pop().expect("retried at once");
    assert_eq!(retried.url, "https://ingest.us-1.example/v1/batches");

    // And it is a property of the project, not of that one request.
    let disk = client.host().surviving_store();
    let mut relaunched = consenting(TestHost::with_store(disk));
    relaunched.track("later", BTreeMap::new()).unwrap();
    let next = relaunched.host().take_sent().pop().expect("sent");
    assert!(
        next.url.starts_with("https://ingest.us-1.example"),
        "the redirect was forgotten"
    );
}

/// §13.24 — a refused credential stops sending and keeps the spool.
#[test]
fn a_bad_key_stops_the_sdk_without_discarding_the_players_events() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();

    let request = client.host().take_sent().pop().expect("sent");
    client.on_response(status(request.id, 401));

    assert!(
        !spool::list(client.host()).is_empty(),
        "the events were thrown away"
    );

    client.track("later", BTreeMap::new()).unwrap();
    client.host().advance_ms(600_000);
    client.tick();
    assert_eq!(
        client.host().sent_count(),
        0,
        "a shipped build would retry forever"
    );
}

/// §13.25 — what the gateway refused reaches the developer.
#[test]
fn events_the_gateway_dropped_are_reported_rather_than_swallowed() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();

    let request = client.host().take_sent().pop().expect("sent");
    client.on_response(crate::host::Response {
        id: request.id,
        status: 202,
        retry_after_ms: None,
        etag: None,
        body: br#"{"batch_id":"b","accepted":1,"dropped":1,
            "reasons":["event 1: event_name \"Level Completed\" is not an accepted name"]}"#
            .to_vec(),
    });

    assert_eq!(client.stats().events_dropped_by_server, 1);
    assert!(
        client.host().logged("is not an accepted name"),
        "a studio would ship a release with a broken event name and never know"
    );
}

/// A bad name is refused locally, with the name in the message.
#[test]
fn a_name_the_gateway_would_refuse_is_refused_here_first() {
    let mut client = consenting(TestHost::new());

    assert!(client.track("Level Completed!", BTreeMap::new()).is_err());
    assert_eq!(client.stats().events_rejected, 1);
    assert!(client.host().logged("Level Completed!"));
}

// ---- behaviour under stress -------------------------------------------------

/// §13.28 — consecutive failures end in a cold state, not a retry loop.
#[test]
fn a_dead_endpoint_costs_one_request_per_foreground_rather_than_a_battery() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();

    for _ in 0..8 {
        let request = client.host().take_sent().pop().expect("an attempt");
        client.on_response(unreachable(request.id));
        client.host().advance_ms(600_000);
        client.tick();
    }

    client.host().take_sent();
    client.host().advance_ms(600_000);
    client.tick();
    assert_eq!(
        client.host().sent_count(),
        0,
        "still hammering a server that is not there"
    );

    // Foreground lifts it, for exactly one attempt.
    client.foreground();
    assert_eq!(client.host().sent_count(), 1);
}

/// §13.26 — tracking keeps working with every dependency broken.
#[test]
fn tracking_survives_everything_being_broken_at_once() {
    let host = TestHost::new();
    host.fail_storage(true);

    let mut client = consenting(host);
    for i in 0..2_000 {
        let _ = client.track(&format!("event_{i}"), props(&[("i", Value::Int(i))]));
    }
    client.background();
    client.shutdown();

    assert!(client.stats().events_tracked >= 2_000);
}

/// Only one request is ever outstanding, so a slow network cannot fan out.
#[test]
fn only_one_request_is_in_flight_at_a_time() {
    let mut client = consenting(TestHost::new());
    for i in 0..5 {
        client
            .track(&format!("event_{i}"), BTreeMap::new())
            .unwrap();
        client.flush();
    }
    client.host().advance_ms(60_000);
    client.tick();
    client.tick();
    client.tick();

    assert_eq!(
        client.host().sent_count(),
        1,
        "the spool drains one batch at a time"
    );
}

/// The spool drains oldest first once answers start arriving.
#[test]
fn the_spool_drains_oldest_first() {
    let mut client = consenting(TestHost::new());
    client.host().take_sent();

    for i in 0..3 {
        client
            .track(&format!("event_{i}"), BTreeMap::new())
            .unwrap();
        client.flush();
    }

    let mut seen = Vec::new();
    for _ in 0..4 {
        let Some(request) = client.host().take_sent().pop() else {
            break;
        };
        let envelope: Json = serde_json::from_slice(&gunzip(&request.body)).unwrap();
        for event in envelope["events"].as_array().unwrap() {
            seen.push(event["event_name"].as_str().unwrap().to_owned());
        }
        client.on_response(accepted(request.id, 1));
    }

    let order: Vec<&String> = seen.iter().filter(|n| n.starts_with("event_")).collect();
    assert_eq!(order, vec!["event_0", "event_1", "event_2"], "{seen:?}");
}

/// Initialization touches no network at all (§11).
#[test]
fn initialize_performs_no_network_operation() {
    let client = Client::new(TestHost::new(), config());
    assert_eq!(
        client.host().sent_count(),
        0,
        "a game's launch would wait on a server"
    );
}

// ---- context ----------------------------------------------------------------

/// The studio's vocabulary survives a relaunch, because it describes the player rather than
/// a moment.
#[test]
fn player_context_is_remembered_across_launches() {
    let mut client = consenting(TestHost::new());
    client.set_player(Some("player-a".into()));
    client
        .set_context(Scope::Player, "progress.level", Value::Int(42))
        .unwrap();
    client
        .set_context(Scope::Session, "map", Value::Str("desert_01".into()))
        .unwrap();

    let disk = client.host().surviving_store();
    let mut relaunched = consenting(TestHost::with_store(disk));
    relaunched.set_player(Some("player-a".into()));

    let context = relaunched.context();
    assert_eq!(context.get("progress.level"), Some(&Value::Int(42)));
    assert!(
        !context.contains_key("map"),
        "a session value outlived its session"
    );
}

/// And does not survive somebody else signing in on the same device.
#[test]
fn one_players_context_does_not_reach_the_next() {
    let mut client = consenting(TestHost::new());
    client.set_player(Some("player-a".into()));
    client
        .set_context(Scope::Player, "rank", Value::Str("diamond".into()))
        .unwrap();

    let disk = client.host().surviving_store();
    let mut relaunched = consenting(TestHost::with_store(disk));
    relaunched.set_player(Some("player-b".into()));

    assert!(
        !relaunched.context().contains_key("rank"),
        "player B inherited player A's rank, and would be targeted as them"
    );
}

/// A new session forgets what was true of the last one.
#[test]
fn session_context_is_cleared_by_a_new_session() {
    let mut client = consenting(TestHost::new());
    client.track("match_started", BTreeMap::new()).unwrap();
    client
        .set_context(Scope::Session, "game_mode", Value::Str("ranked".into()))
        .unwrap();
    client
        .set_context(Scope::Player, "rank", Value::Str("diamond".into()))
        .unwrap();

    client.background();
    client.host().advance_ms(60_000);
    client.foreground();
    client.track("match_started", BTreeMap::new()).unwrap();

    assert!(
        !client.context().contains_key("game_mode"),
        "last session's mode leaked"
    );
    assert!(
        client.context().contains_key("rank"),
        "the player is still the player"
    );
}

/// Geography and network are resolved from the connection, never asserted by the client.
///
/// The security boundary: a value a modded build can set is a value that cannot decide who
/// gets a discount. Refusing by name also saves the studio from a targeting rule that
/// silently never matches.
#[test]
fn a_client_cannot_assert_the_namespaces_the_server_resolves() {
    let mut client = consenting(TestHost::new());

    for key in [
        "geo.country",
        "network.vpn",
        "account.verified_lifetime_spend",
        "app.version",
    ] {
        assert!(
            client
                .set_context(Scope::Player, key, Value::Str("forged".into()))
                .is_err(),
            "{key} was accepted from the client"
        );
    }
    assert!(client.context().is_empty());
}

/// Setting context in a game loop must not look like a thousand changes.
#[test]
fn restating_context_does_not_look_like_a_change() {
    let mut client = consenting(TestHost::new());
    client
        .set_context(Scope::Session, "map", Value::Str("desert_01".into()))
        .unwrap();
    let revision = client.context_revision();

    for _ in 0..1_000 {
        client
            .set_context(Scope::Session, "map", Value::Str("desert_01".into()))
            .unwrap();
    }

    assert_eq!(
        client.context_revision(),
        revision,
        "a game that sets context every frame would refresh its configuration every frame"
    );
}

// ---- context frames ---------------------------------------------------------

/// The shape of a batch that carries context, worked end to end.
///
/// ```text
/// Context.Set("rank", "gold");  Context.Set("chapter", 4);
/// Track("match_started");  Track("enemy_killed");
/// Context.Set("rank", "diamond");
/// Track("match_completed");  Track("shop_opened");
/// ```
///
/// One snapshot and one delta, rather than four copies of the context. The gateway expands
/// them onto each event before storage, so ClickHouse gets denormalized rows and no query
/// ever joins to reconstruct them.
#[test]
fn a_batch_carries_a_context_snapshot_and_its_deltas() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();
    settle(&mut client);

    client
        .set_context(Scope::Player, "rank", Value::Str("gold".into()))
        .unwrap();
    client
        .set_context(Scope::Player, "chapter", Value::Int(4))
        .unwrap();

    client.track("match_started", BTreeMap::new()).unwrap();
    client.track("enemy_killed", BTreeMap::new()).unwrap();

    client
        .set_context(Scope::Player, "rank", Value::Str("diamond".into()))
        .unwrap();

    client.track("match_completed", BTreeMap::new()).unwrap();
    client.track("shop_opened", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    let frames = envelope["context"].as_array().expect("a context timeline");
    assert_eq!(
        frames.len(),
        2,
        "want one snapshot and one delta: {frames:?}"
    );

    assert_eq!(frames[0]["at"], 0);
    assert_eq!(frames[0]["values"]["rank"], "gold");
    assert_eq!(frames[0]["values"]["chapter"], 4);
    assert!(
        frames[0].get("set").is_none(),
        "frame 0 is a snapshot, not a delta"
    );

    // From the third event onward — the two matches happened as gold.
    assert_eq!(frames[1]["at"], 2);
    assert_eq!(frames[1]["set"]["rank"], "diamond");
    assert!(
        frames[1].get("values").is_none(),
        "a delta must not restate the whole context"
    );
    assert!(
        frames[1]["set"].get("chapter").is_none(),
        "chapter did not change and must not be repeated"
    );

    // And the events themselves carry no context at all.
    for event in envelope["events"].as_array().unwrap() {
        assert!(
            event.get("rank").is_none() && event.get("properties").is_none(),
            "{event}"
        );
    }
}

/// A change applies to future events, never to queued ones.
///
/// The rule the whole design rests on. `match_started` happened while the player was gold and
/// must stay gold, even though it ends up in the same batch as the change — the same
/// principle that already lets one batch straddle a sign-in.
#[test]
fn a_context_change_does_not_reach_back_over_queued_events() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();
    settle(&mut client);

    client
        .set_context(Scope::Player, "rank", Value::Str("gold".into()))
        .unwrap();
    client.track("match_started", BTreeMap::new()).unwrap();
    client
        .set_context(Scope::Player, "rank", Value::Str("diamond".into()))
        .unwrap();
    client.track("match_completed", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    let frames = envelope["context"].as_array().unwrap();

    assert_eq!(
        frames[0]["values"]["rank"], "gold",
        "the batch opens as gold"
    );
    assert_eq!(
        frames[1]["at"], 1,
        "diamond applies from the second event, not the first"
    );
    assert_eq!(frames[1]["set"]["rank"], "diamond");
}

/// Frame 0 is always a full snapshot, so a batch needs nothing from the batch before it.
///
/// This is what keeps an immutable batch a complete transport unit. A delta-only batch would
/// make the gateway's expansion depend on server-side memory of the previous one, and a
/// redelivery or an out-of-order arrival would then reconstruct the wrong context.
#[test]
fn every_batch_opens_with_a_complete_snapshot() {
    let mut client = consenting(TestHost::new());
    client
        .set_context(Scope::Player, "rank", Value::Str("diamond".into()))
        .unwrap();
    client
        .set_context(Scope::Session, "map", Value::Str("desert".into()))
        .unwrap();

    // Three batches in a row, with no context change between them.
    for i in 0..3 {
        client
            .track(&format!("event_{i}"), BTreeMap::new())
            .unwrap();
        client.flush();
        settle(&mut client);
    }

    for meta in spool::list(client.host()) {
        let body = client.host().store_get(&meta.key).unwrap();
        let envelope: Json = serde_json::from_slice(&body).unwrap();
        let frames = envelope["context"]
            .as_array()
            .expect("every batch has a timeline");
        assert_eq!(frames[0]["at"], 0);
        assert_eq!(
            frames[0]["values"]["rank"], "diamond",
            "a later batch lost the snapshot"
        );
        assert_eq!(frames[0]["values"]["map"], "desert");
    }
}

/// A game that never sets context pays nothing for it.
#[test]
fn a_batch_without_context_carries_no_timeline() {
    let mut client = consenting(TestHost::new());
    client.track("level_started", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    assert!(
        envelope.get("context").is_none(),
        "an integration that does not use context should not send the field at all"
    );
}

/// Several changes before the next event are one frame, not several.
#[test]
fn changes_between_two_events_collapse_into_one_frame() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();
    settle(&mut client);

    client.track("match_started", BTreeMap::new()).unwrap();
    client
        .set_context(Scope::Player, "rank", Value::Str("diamond".into()))
        .unwrap();
    client
        .set_context(Scope::Player, "chapter", Value::Int(7))
        .unwrap();
    client
        .set_context(Scope::Session, "map", Value::Str("forest".into()))
        .unwrap();
    client.track("match_completed", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    let frames = envelope["context"].as_array().unwrap();
    assert_eq!(
        frames.len(),
        2,
        "three changes at one index are one frame: {frames:?}"
    );
    assert_eq!(frames[1]["at"], 1);
    assert_eq!(frames[1]["set"]["rank"], "diamond");
    assert_eq!(frames[1]["set"]["chapter"], 7);
    assert_eq!(frames[1]["set"]["map"], "forest");
}

/// A cleared scope becomes an `unset`, so the gateway can tell "gone" from "unchanged".
#[test]
fn clearing_a_session_scope_is_recorded_as_a_removal() {
    let mut client = consenting(TestHost::new());
    client.track("game_started", BTreeMap::new()).unwrap();
    settle(&mut client);

    client
        .set_context(Scope::Session, "map", Value::Str("desert".into()))
        .unwrap();
    client
        .set_context(Scope::Player, "rank", Value::Str("gold".into()))
        .unwrap();
    client.track("match_started", BTreeMap::new()).unwrap();

    client.remove_context(Scope::Session, "map");
    client.track("menu_opened", BTreeMap::new()).unwrap();
    client.flush();

    let envelope = last_envelope(&client);
    let frames = envelope["context"].as_array().unwrap();
    assert_eq!(frames[0]["values"]["map"], "desert");
    assert_eq!(frames[1]["at"], 1);
    assert_eq!(frames[1]["unset"][0], "map");
    assert!(
        frames[1].get("set").is_none(),
        "nothing was set: {frames:?}"
    );
}

// ---- live configuration (§41, §42, §45) -------------------------------------

fn configuring() -> Config {
    let mut config = config();
    config.config_endpoint = "https://config.eu-1.example".into();
    config
}

/// A response from the configuration endpoint.
fn served(id: u64, body: &str, etag: Option<&str>) -> crate::host::Response {
    crate::host::Response {
        id,
        status: 200,
        retry_after_ms: None,
        etag: etag.map(str::to_owned),
        body: body.as_bytes().to_vec(),
    }
}

/// One document with a flag of each kind and one event, on at the server's `now`.
fn document(version: u32, server_now_ms: i64) -> String {
    format!(
        r#"{{
          "version": {version},
          "flags": {{
            "hud_v2": true,
            "max_level": 80,
            "greeting": "merhaba",
            "shop_layout": {{"rows": 3}}
          }},
          "liveops_events": [{{
            "key": "happy_hour", "kind": "offer", "priority": 10,
            "starts_at_ms": {start}, "ends_at_ms": {end},
            "next_starts_at_ms": {next},
            "payload": {{"multiplier": 2}}
          }}],
          "upcoming": [{{
            "key": "weekend", "kind": "seasonal_event", "priority": 5,
            "starts_at_ms": {next}, "ends_at_ms": {far},
            "payload": {{"xp": 2}}
          }}],
          "server_time_ms": {server_now_ms}
        }}"#,
        start = server_now_ms - 60_000,
        end = server_now_ms + 3_600_000,
        next = server_now_ms + 86_400_000,
        far = server_now_ms + 90_000_000,
    )
}

/// Asks once, and the answer is what the game reads. The four flag kinds survive the wire
/// and a JSON flag stays text, because what is inside it is the studio's business.
#[test]
fn a_configuration_is_fetched_and_read() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.refresh_config();

    let sent = client.host().take_sent();
    assert_eq!(sent.len(), 1, "one request, to the configuration endpoint");
    assert_eq!(sent[0].url, "https://config.eu-1.example/v1/config");
    assert!(
        sent[0].if_none_match.is_none(),
        "nothing to be conditional about on the first ask"
    );

    let body: Json = serde_json::from_slice(&sent[0].body).unwrap();
    assert_eq!(body["install_id"], client.install_id());
    assert_eq!(body["sdk"]["name"], "unity");
    for forgeable in ["geo", "network", "commerce", "activity"] {
        assert!(
            body.get(forgeable).is_none(),
            "the request carries {forgeable}, which the server must derive rather than believe"
        );
    }

    let now = client.host().now_ms();
    client.on_response(served(sent[0].id, &document(7, now), Some("\"abc\"")));

    assert_eq!(client.config_version(), 7);
    assert_eq!(client.flag("hud_v2"), Some(&Flag::Bool(true)));
    assert_eq!(client.flag("max_level"), Some(&Flag::Number(80.0)));
    assert_eq!(client.flag("greeting"), Some(&Flag::Text("merhaba".into())));
    assert_eq!(
        client.flag("shop_layout"),
        Some(&Flag::Json(r#"{"rows":3}"#.into()))
    );
    // Not published is not the same as published false: a binding folds this into the
    // game's own default, and cannot do that if it cannot tell them apart.
    assert_eq!(client.flag("never_published"), None);
}

/// The whole point of `server_time_ms`: a player who changes their device clock does not
/// change which events are running.
#[test]
fn a_window_is_measured_against_the_server_clock() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.refresh_config();
    let sent = client.host().take_sent();

    let server_now = 1_700_000_000_000;
    client.on_response(served(sent[0].id, &document(1, server_now), None));

    let active = client.liveops_active();
    assert_eq!(active.len(), 1, "one event is on");
    assert_eq!(active[0].key, "happy_hour");
    assert_eq!(
        active[0].remaining_ms(client.server_now_ms().unwrap()),
        3_600_000,
        "an hour left, from the server's clock and not the device's"
    );

    // The player sets their phone forward a year. The device clock is now wildly ahead of
    // the server's, and nothing about the event changes.
    client
        .host()
        .set_wall_ms(client.host().now_ms() + 365 * 86_400_000);
    assert_eq!(
        client.liveops_active().len(),
        1,
        "moving the device clock forward ended an event that is still running"
    );

    // Time actually passing does end it, because that moves the monotonic clock.
    client.host().advance_ms(3_600_001);
    assert!(
        client.liveops_active().is_empty(),
        "the event outlived its window"
    );
}

/// §42: "local SDK fallback to last-known-good config". A launch with no network keeps the
/// flags — and reports no active events, because a window needs a clock somebody trusts.
#[test]
fn a_persisted_document_restores_flags_but_not_windows() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.refresh_config();
    let sent = client.host().take_sent();
    let server_now = client.host().now_ms();
    client.on_response(served(sent[0].id, &document(4, server_now), None));
    assert_eq!(client.liveops_active().len(), 1);

    // The process ends. A new one starts from the same disk, with no network.
    let disk = client.host().surviving_store();
    let mut relaunched = Client::new(TestHost::with_store(disk), configuring());
    relaunched.restore_config();

    assert_eq!(
        relaunched.config_version(),
        4,
        "the last document did not survive the restart"
    );
    assert_eq!(relaunched.flag("hud_v2"), Some(&Flag::Bool(true)));
    assert!(
        !relaunched.config_anchored(),
        "a document off disk must not claim to know what time the server thinks it is"
    );
    assert!(
        relaunched.liveops_active().is_empty(),
        "an event was reported active with no trustworthy clock; a player who moved their \
         phone forward could open it early"
    );
    assert!(
        relaunched.liveops_upcoming().is_empty(),
        "an unanchored document reported what is coming, which it cannot know"
    );
}

/// An SDK polls, and most polls would receive what it already has. The tag it was given goes
/// back, and a 304 keeps everything while moving the clock forward.
#[test]
fn an_unchanged_configuration_costs_no_body() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.refresh_config();
    let first = client.host().take_sent();
    let server_now = client.host().now_ms();
    client.on_response(served(
        first[0].id,
        &document(2, server_now),
        Some("\"v2\""),
    ));

    client.host().advance_ms(20 * 60 * 1_000);
    client.tick();

    let second = client.host().take_sent();
    assert_eq!(
        second.len(),
        1,
        "the interval elapsed and nothing was asked"
    );
    assert_eq!(
        second[0].if_none_match.as_deref(),
        Some("\"v2\""),
        "the tag was not sent back, so the server has to answer with a body every time"
    );

    client.on_response(status(second[0].id, 304));

    assert_eq!(client.config_version(), 2, "a 304 discarded the document");
    assert_eq!(client.flag("hud_v2"), Some(&Flag::Bool(true)));
    assert_eq!(
        client.liveops_active().len(),
        1,
        "the event stopped being reported after a 304"
    );
    // And the countdown is honest: twenty minutes passed, so twenty fewer minutes remain.
    assert_eq!(
        client.liveops_active()[0].remaining_ms(client.server_now_ms().unwrap()),
        3_600_000 - 20 * 60 * 1_000,
    );
}

/// A configuration request must not be able to block a batch, or an upload a fetch. They
/// share one callback and must not share one slot.
#[test]
fn configuration_and_batches_do_not_block_each_other() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));

    client.track("level_completed", Default::default()).ok();
    client.flush();
    client.refresh_config();

    let sent = client.host().take_sent();
    assert_eq!(
        sent.len(),
        2,
        "a batch and a configuration request should both be in flight, got {}",
        sent.len()
    );

    let batch = sent.iter().find(|r| r.url.contains("/v1/batches")).unwrap();
    let config_request = sent.iter().find(|r| r.url.contains("/v1/config")).unwrap();

    // The configuration answers first. The batch must still be outstanding.
    let server_now = client.host().now_ms();
    client.on_response(served(config_request.id, &document(1, server_now), None));
    assert_eq!(client.config_version(), 1);
    assert_eq!(
        client.stats().batches_delivered,
        0,
        "a configuration response was applied to the batch path"
    );

    client.on_response(accepted(batch.id, 1));
    assert_eq!(client.stats().batches_delivered, 1);
}

/// A studio that only sends analytics must not pay a request for an answer nobody reads.
#[test]
fn no_configuration_endpoint_means_no_requests() {
    let mut client = Client::new(TestHost::new(), config());
    client.refresh_config();
    client.host().advance_ms(60 * 60 * 1_000);
    client.tick();

    assert_eq!(
        client.host().sent_count(),
        0,
        "a client with no configuration endpoint asked for one anyway"
    );
    assert_eq!(client.flag("anything"), None);
}

/// A malformed answer keeps what is held. Replacing a working document with nothing because
/// one response was garbled turns a server bug into a game that lost its configuration.
#[test]
fn a_malformed_response_keeps_the_last_good_document() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.refresh_config();
    let first = client.host().take_sent();
    let server_now = client.host().now_ms();
    client.on_response(served(first[0].id, &document(3, server_now), None));

    client.host().advance_ms(20 * 60 * 1_000);
    client.tick();
    let second = client.host().take_sent();
    client.on_response(served(second[0].id, "<html>a proxy said no</html>", None));

    assert_eq!(
        client.config_version(),
        3,
        "the good document was discarded"
    );
    assert_eq!(client.flag("hud_v2"), Some(&Flag::Bool(true)));
    assert!(client.host().logged("could not be read"));
}

/// A game resumed after hours is where the answer is most likely to have moved, so the
/// interval is brought forward rather than waited out.
#[test]
fn coming_to_the_foreground_asks_again() {
    let mut client = Client::new(TestHost::new(), configuring());
    client.refresh_config();
    let first = client.host().take_sent();
    let server_now = client.host().now_ms();
    client.on_response(served(first[0].id, &document(1, server_now), None));

    // Well inside the refresh interval, so a tick would do nothing.
    client.host().advance_ms(60_000);
    client.tick();
    assert_eq!(client.host().sent_count(), 0, "a tick asked too early");

    client.foreground();
    assert_eq!(
        client.host().take_sent().len(),
        1,
        "coming to the foreground did not ask for a fresh configuration"
    );
}

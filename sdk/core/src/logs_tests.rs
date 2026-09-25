//! The log stream's behaviour, driven through the client as a binding drives it (§15).

use serde_json::Value as Json;

use crate::client::{Client, granted};
use crate::config::Config;
use crate::consent::Granted;
use crate::logs::{Diagnostics, LogException, LogLevel, LogRecord, Logged, StackFrame};
use crate::testhost::{TestHost, accepted};

fn config() -> Config {
    let mut config = Config::new("ps_project_live_test", "https://ingest.eu-1.example");
    config.sdk_name = "unity".into();
    config.app_version = "2.7.1".into();
    config.platform = "android".into();
    config.device_model = "SM-S918B".into();
    config.logs_enabled = true;
    config
}

fn client_with(host: TestHost, config: Config) -> Client<TestHost> {
    Client::new(host, config)
}

fn grant(client: &mut Client<TestHost>) {
    client.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));
    // Whatever consent sent (a session or nothing), answered, so the next request is clean.
    for request in client.host().take_sent() {
        client.on_response(accepted(request.id, 1));
    }
}

fn error(message: &str) -> LogRecord {
    LogRecord {
        level: Some(LogLevel::Error),
        message: message.into(),
        logger: "Store".into(),
        ..LogRecord::default()
    }
}

fn log_bodies(client: &Client<TestHost>) -> Vec<Json> {
    client
        .host()
        .take_sent()
        .into_iter()
        .filter(|r| r.url.ends_with("/v1/logs"))
        .map(|r| {
            let body = match r.content_encoding {
                Some("gzip") => {
                    use std::io::Read;
                    let mut out = Vec::new();
                    flate2::read::GzDecoder::new(&r.body[..])
                        .read_to_end(&mut out)
                        .unwrap();
                    out
                }
                _ => r.body.clone(),
            };
            serde_json::from_slice(&body).unwrap()
        })
        .collect()
}

#[test]
fn nothing_is_captured_until_the_studio_turns_it_on() {
    let mut off = config();
    off.logs_enabled = false;
    let mut client = client_with(TestHost::new(), off);
    grant(&mut client);
    assert_eq!(client.log(error("boom")), Logged::Disabled);
    client.flush();
    assert!(log_bodies(&client).is_empty());
}

#[test]
fn an_error_goes_to_its_own_endpoint_compressed_scrubbed_and_with_what_came_before() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    assert_eq!(
        client.log(LogRecord {
            level: Some(LogLevel::Info),
            message: "Loaded scene Shop".into(),
            ..Default::default()
        }),
        Logged::Breadcrumb
    );
    assert_eq!(
        client.log(error("Receipt refused for emma@example.com")),
        Logged::Queued
    );
    client.flush();

    let bodies = log_bodies(&client);
    assert_eq!(bodies.len(), 1);
    let batch = &bodies[0];
    assert_eq!(batch["consent"]["diagnostics"], true);
    assert_eq!(batch["device"]["model"], "SM-S918B");
    let record = &batch["records"][0];
    assert_eq!(record["level"], "error");
    assert_eq!(record["message"], "Receipt refused for [email]");
    let crumbs = record["breadcrumbs"].as_array().unwrap();
    assert_eq!(crumbs[0]["message"], "Loaded scene Shop");
}

#[test]
fn an_error_every_frame_is_one_record_with_a_count() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    for _ in 0..60 {
        client.log(error("NullReferenceException in Update"));
        client.host().advance_ms(16);
    }
    client.flush();
    let bodies = log_bodies(&client);
    let records = bodies[0]["records"].as_array().unwrap();
    assert_eq!(records.len(), 1);
    assert_eq!(records[0]["attributes"]["ps.repeat"], "60");
    assert_eq!(client.log_stats().folded, 59);
}

#[test]
fn a_flood_is_capped_and_a_crash_still_gets_through() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    let mut limited = 0;
    for n in 0..300 {
        if client.log(error(&format!("distinct failure {n}"))) == Logged::RateLimited {
            limited += 1;
        }
    }
    assert_eq!(limited, 300 - 120);
    let crash = LogRecord {
        crash: true,
        exception: Some(LogException {
            type_name: "SIGSEGV".into(),
            ..Default::default()
        }),
        ..Default::default()
    };
    assert_eq!(client.log(crash), Logged::Queued);
}

#[test]
fn a_crash_is_sent_at_once_without_waiting_for_a_flush() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    client.log(LogRecord {
        crash: true,
        message: "died".into(),
        ..Default::default()
    });
    let bodies = log_bodies(&client);
    assert_eq!(bodies.len(), 1);
    assert_eq!(bodies[0]["records"][0]["crash"], true);
    assert_eq!(bodies[0]["records"][0]["level"], "fatal");
}

#[test]
fn before_an_answer_errors_wait_in_memory_and_a_crash_waits_on_the_device() {
    let mut client = client_with(TestHost::new(), config());
    assert_eq!(client.log(error("early")), Logged::AwaitingConsent);
    assert_eq!(
        client.log(LogRecord {
            crash: true,
            message: "died".into(),
            ..Default::default()
        }),
        Logged::AwaitingConsent
    );
    client.flush();
    assert!(
        log_bodies(&client).is_empty(),
        "nothing leaves the device before the player answers"
    );
    assert_eq!(client.held_crash_reports(), 1);
    assert!(
        client
            .host()
            .surviving_store()
            .keys()
            .all(|k| !k.starts_with("ps.l.0")),
        "only the crash is on disk"
    );

    client.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));
    let bodies = log_bodies(&client);
    let records: Vec<&Json> = bodies
        .iter()
        .flat_map(|b| b["records"].as_array().unwrap())
        .collect();
    assert_eq!(records.len(), 2);
    assert_eq!(client.held_crash_reports(), 0);
}

#[test]
fn a_no_deletes_what_was_waiting() {
    let mut client = client_with(TestHost::new(), config());
    client.log(LogRecord {
        crash: true,
        message: "died".into(),
        ..Default::default()
    });
    assert_eq!(client.held_crash_reports(), 1);
    client.set_consent(crate::consent::State::Denied);
    assert_eq!(client.held_crash_reports(), 0);
    assert_eq!(client.log(error("after no")), Logged::Declined);
}

#[test]
fn error_reports_can_be_shared_without_analytics() {
    let mut client = client_with(TestHost::new(), config());
    client.set_consent(crate::consent::State::Denied);
    client.set_diagnostics_consent(Diagnostics::Granted(Granted {
        policy_version: "2026-01-15".into(),
        att_status: String::new(),
        gdpr_applies: true,
        collected_at: "2026-09-25T10:00:00.000Z".into(),
    }));
    client.log(error("only the error"));
    client.flush();
    let bodies = log_bodies(&client);
    assert_eq!(bodies.len(), 1);
    assert_eq!(bodies[0]["consent"]["analytics"], false);
    assert_eq!(bodies[0]["consent"]["diagnostics"], true);
}

#[test]
fn a_run_that_died_in_the_foreground_is_reported_on_the_next_launch() {
    let host = TestHost::new();
    let mut client = client_with(host, config());
    grant(&mut client);
    client.foreground();
    let store = client.host().surviving_store();
    assert!(store.contains_key("ps.run"));

    // The process dies here. The next launch finds the marker.
    let mut next = client_with(TestHost::with_store(store), config());
    assert_eq!(
        next.held_crash_reports(),
        1,
        "held until the player's answer is known"
    );
    next.set_consent(granted("2026-01-15", "authorized", true, 1_789_560_000_000));
    let bodies = log_bodies(&next);
    let record = &bodies[0]["records"][0];
    assert_eq!(record["exception"]["type"], "AbnormalExit");
    assert_eq!(record["crash"], true);
}

#[test]
fn going_to_the_background_is_a_clean_exit() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    client.foreground();
    client.background();
    let store = client.host().surviving_store();
    assert!(!store.contains_key("ps.run"));
    let next = client_with(TestHost::with_store(store), config());
    assert_eq!(next.held_crash_reports(), 0);
}

#[test]
fn a_child_directed_game_sends_no_player_and_no_attributes() {
    let mut kids = config();
    kids.child_directed = true;
    let mut client = client_with(TestHost::new(), kids);
    grant(&mut client);
    client.set_player(Some("player_42".into()));
    let mut record = error("boom");
    record.attributes.insert("nickname".into(), "Emma".into());
    client.log(record);
    client.flush();
    let bodies = log_bodies(&client);
    let sent = &bodies[0]["records"][0];
    assert!(sent.get("player_id").is_none());
    assert!(sent.get("attributes").is_none());
}

#[test]
fn a_deep_stack_keeps_its_innermost_frames() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    let frames: Vec<StackFrame> = (0..300)
        .map(|n| StackFrame {
            function: format!("f{n}"),
            ..Default::default()
        })
        .collect();
    client.log(LogRecord {
        level: Some(LogLevel::Error),
        exception: Some(LogException {
            type_name: "StackOverflow".into(),
            frames,
            ..Default::default()
        }),
        ..Default::default()
    });
    client.flush();
    let bodies = log_bodies(&client);
    let sent = bodies[0]["records"][0]["exception"]["frames"]
        .as_array()
        .unwrap()
        .clone();
    assert_eq!(sent.len(), 128);
    assert_eq!(sent.last().unwrap()["function"], "f299");
}

#[test]
fn a_log_in_flight_does_not_hold_up_an_event() {
    let mut client = client_with(TestHost::new(), config());
    grant(&mut client);
    client.log(LogRecord {
        crash: true,
        message: "died".into(),
        ..Default::default()
    });
    // The crash is in flight and unanswered; an event still goes.
    client
        .track_revenue("purchase", 990_000, "USD", Default::default())
        .unwrap();
    let urls: Vec<String> = client
        .host()
        .take_sent()
        .into_iter()
        .map(|r| r.url)
        .collect();
    assert!(urls.iter().any(|u| u.ends_with("/v1/logs")));
    assert!(urls.iter().any(|u| u.ends_with("/v1/batches")));
}

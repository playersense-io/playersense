//! What the server answered, and what to do about it (§9, §10).
//!
//! The table in the protocol document, as code. It is worth keeping in one place and testing
//! on its own, because the distinctions it draws are the ones an SDK gets wrong: a 400 and a
//! 503 are both "it did not work", and treating them the same means either retrying
//! something forever that can never succeed, or throwing away a batch because a load
//! balancer hiccuped.

use serde::Deserialize;

use crate::config::Config;
use crate::host::Host;

/// What the client should do with the batch it just sent.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Disposition {
    /// Durably queued. Delete it, and tell the game what the server made of it.
    Delivered {
        accepted: u32,
        dropped: u32,
        reasons: Vec<String>,
    },

    /// The server will never accept this batch. Delete it and say so loudly: a 400 or a 422
    /// here is an SDK bug, not a network condition, and retrying cannot help.
    Discard { reason: String },

    /// Too large. Split in half and retry both halves rather than losing the events.
    Split,

    /// The credential is wrong — unknown, revoked, or scoped elsewhere. Stop sending and
    /// **keep the spool**: a shipped build with a bad key must not spend the rest of its
    /// life retrying, and it must not throw away the player's events either, because the key
    /// can be fixed from the dashboard.
    Stop { reason: String },

    /// This project lives in another cell. Persist the address and retry against it
    /// immediately. Not transient: it is where the project is (§6, §99).
    Redirect { endpoint: String },

    /// The gateway did not understand the encoding. Send uncompressed for the rest of the
    /// process rather than dropping the batch.
    DisableCompression,

    /// Transient. Keep the batch and back off.
    Retry { after_ms: Option<i64> },
}

/// The 202 body (`protocol.Accepted`).
#[derive(Deserialize, Default)]
struct Accepted {
    #[serde(default)]
    accepted: u32,
    #[serde(default)]
    dropped: u32,
    #[serde(default)]
    reasons: Vec<String>,
}

/// The framework's error envelope.
#[derive(Deserialize, Default)]
struct ErrorEnvelope {
    #[serde(default)]
    error: ErrorBody,
}

#[derive(Deserialize, Default)]
struct ErrorBody {
    #[serde(default)]
    code: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    details: Vec<ErrorDetail>,
}

#[derive(Deserialize, Default)]
struct ErrorDetail {
    #[serde(default)]
    field: String,
    #[serde(default)]
    issue: String,
}

/// Decides what a response means.
///
/// `status` is 0 for anything that never reached a server — a timeout, a refused connection,
/// a cancelled request. Zero is the absence of an answer rather than an answer, and the only
/// safe reading of it is "try again".
pub fn classify(status: u16, retry_after_ms: Option<i64>, body: &[u8]) -> Disposition {
    match status {
        202 => {
            let parsed: Accepted = serde_json::from_slice(body).unwrap_or_default();
            Disposition::Delivered {
                accepted: parsed.accepted,
                dropped: parsed.dropped,
                reasons: parsed.reasons,
            }
        }

        // Malformed, or an envelope this build cannot produce correctly. Both are bugs here.
        400 | 422 => Disposition::Discard {
            reason: explain(body, "the gateway refused the batch"),
        },

        401 | 403 => Disposition::Stop {
            reason: explain(body, "the project key was refused"),
        },

        413 => Disposition::Split,

        415 => Disposition::DisableCompression,

        421 => match redirect(body) {
            Some(endpoint) => Disposition::Redirect { endpoint },
            // A cell with no registered ingest endpoint. Nothing to redirect to and nothing
            // to retry against, so stopping is the only honest answer.
            None => Disposition::Stop {
                reason: explain(
                    body,
                    "this project belongs to another cell, which has no ingest address",
                ),
            },
        },

        429 => Disposition::Retry {
            after_ms: retry_after_ms,
        },

        // Everything else: 5xx, a gateway in front of it, 0 for no answer at all.
        _ => Disposition::Retry { after_ms: None },
    }
}

/// The address a 421 names.
fn redirect(body: &[u8]) -> Option<String> {
    let envelope: ErrorEnvelope = serde_json::from_slice(body).ok()?;
    envelope
        .error
        .details
        .into_iter()
        .find(|d| d.field == "ingest_url" && !d.issue.is_empty())
        .map(|d| d.issue)
}

/// The server's own words where there are any, so a log line says something useful.
fn explain(body: &[u8], fallback: &str) -> String {
    match serde_json::from_slice::<ErrorEnvelope>(body) {
        Ok(envelope) if !envelope.error.message.is_empty() => {
            if envelope.error.code.is_empty() {
                envelope.error.message
            } else {
                format!("{} ({})", envelope.error.message, envelope.error.code)
            }
        }
        _ => fallback.to_owned(),
    }
}

/// How long to wait before the next attempt (§10).
///
/// Exponential with **full jitter**: a delay uniformly in `[0, min(cap, base × 2^n))`. The
/// jitter is not decoration. Without it every client that lost the same server reconnects in
/// the same millisecond, and a server that came back goes down again — this is the one place
/// where each client behaving optimally makes the fleet behave worst.
pub fn backoff_ms<H: Host + ?Sized>(host: &H, config: &Config, consecutive_failures: u32) -> i64 {
    let shift = consecutive_failures.min(20);
    let ceiling = config
        .backoff_base_ms
        .saturating_mul(1i64 << shift)
        .min(config.backoff_cap_ms)
        .max(1);

    let mut bytes = [0u8; 8];
    host.fill_random(&mut bytes);
    (u64::from_le_bytes(bytes) % ceiling as u64) as i64
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testhost::TestHost;

    #[test]
    fn a_202_carries_what_the_server_made_of_the_batch() {
        let body =
            br#"{"batch_id":"b","accepted":498,"dropped":2,"reasons":["event 17: bad name"]}"#;
        match classify(202, None, body) {
            Disposition::Delivered {
                accepted,
                dropped,
                reasons,
            } => {
                assert_eq!((accepted, dropped), (498, 2));
                assert_eq!(reasons.len(), 1, "the reason must reach the developer");
            }
            other => panic!("{other:?}"),
        }
    }

    #[test]
    fn a_202_with_an_unreadable_body_is_still_delivered() {
        // The batch is queued whatever the body says. Treating a surprising body as a
        // failure would resend events the server already has.
        assert!(matches!(
            classify(202, None, b"<html>"),
            Disposition::Delivered { .. }
        ));
    }

    #[test]
    fn the_four_classes_of_refusal_are_told_apart() {
        assert!(matches!(
            classify(400, None, b""),
            Disposition::Discard { .. }
        ));
        assert!(matches!(
            classify(422, None, b""),
            Disposition::Discard { .. }
        ));
        assert!(matches!(classify(401, None, b""), Disposition::Stop { .. }));
        assert!(matches!(classify(403, None, b""), Disposition::Stop { .. }));
        assert_eq!(classify(413, None, b""), Disposition::Split);
        assert_eq!(classify(415, None, b""), Disposition::DisableCompression);
    }

    #[test]
    fn a_421_yields_the_address_that_can_serve_the_project() {
        let body = br#"{"error":{"code":"wrong_cell","message":"this project belongs to cell us-1",
            "status":421,"details":[{"field":"ingest_url","location":"header",
            "issue":"https://ingest.us-1.playersense.dev"}]}}"#;
        assert_eq!(
            classify(421, None, body),
            Disposition::Redirect {
                endpoint: "https://ingest.us-1.playersense.dev".into()
            }
        );
    }

    #[test]
    fn a_421_naming_no_address_stops_rather_than_looping() {
        assert!(matches!(
            classify(421, None, b"{}"),
            Disposition::Stop { .. }
        ));
    }

    #[test]
    fn a_429_honours_retry_after() {
        assert_eq!(
            classify(429, Some(5_000), b""),
            Disposition::Retry {
                after_ms: Some(5_000)
            }
        );
    }

    #[test]
    fn no_answer_at_all_is_transient() {
        // Status 0 is a timeout, a DNS failure or a refused connection. An SDK that read it
        // as a permanent refusal would throw away a session because of a tunnel.
        assert_eq!(
            classify(0, None, b""),
            Disposition::Retry { after_ms: None }
        );
        assert_eq!(
            classify(503, None, b""),
            Disposition::Retry { after_ms: None }
        );
    }

    #[test]
    fn a_refusal_quotes_the_server() {
        match classify(400, None, br#"{"error":{"code":"unsupported_schema_version","message":"unsupported schema_version"}}"#) {
            Disposition::Discard { reason } => {
                assert!(reason.contains("unsupported_schema_version"), "{reason}");
            }
            other => panic!("{other:?}"),
        }
    }

    #[test]
    fn backoff_grows_and_then_stops_growing() {
        let host = TestHost::new();
        let config = Config::new("k", "https://e").sanitized();

        // Full jitter means any single draw can be small, so the property to assert is the
        // ceiling rather than the value.
        for failures in 0..12 {
            for _ in 0..50 {
                let delay = backoff_ms(&host, &config, failures);
                assert!(delay >= 0);
                assert!(delay < config.backoff_cap_ms.max(1));
            }
        }
    }

    #[test]
    fn backoff_is_jittered_rather_than_a_thundering_herd() {
        let host = TestHost::new();
        let config = Config::new("k", "https://e").sanitized();
        let draws: std::collections::BTreeSet<i64> =
            (0..40).map(|_| backoff_ms(&host, &config, 6)).collect();
        assert!(
            draws.len() > 1,
            "every client would reconnect in the same millisecond"
        );
    }
}

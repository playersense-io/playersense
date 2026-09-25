//! What each log operation costs, measured rather than argued.
//!
//! Ignored, because a timing is only meaningful in a release build:
//!
//! ```text
//! cargo test --release -p playersense-core logs_bench -- --ignored --nocapture --test-threads=1
//! ```
//!
//! Every figure is per call on the thread that made it, which in a game is the main thread.
//! Medians hide the frame that stutters, so the slowest 1% is printed beside the median.

use std::hint::black_box;
use std::time::Instant;

use crate::client::{Client, granted};
use crate::config::Config;
use crate::logs::{LogException, LogLevel, LogRecord, StackFrame};
use crate::scrub::scrub;
use crate::testhost::{TestHost, accepted};

const MESSAGE: &str = "Receipt refused by the store for product gems_500: timeout after 3 attempts";

fn client(max_per_minute: u32) -> Client<TestHost> {
    client_with(|config| config.log_max_per_minute = max_per_minute)
}

/// With no budget and no batch limit, so every call is the full work of a new record and none
/// of them seals.
fn unbounded() -> Client<TestHost> {
    client_with(|config| {
        config.log_max_per_minute = u32::MAX;
        config.log_max_records_per_batch = usize::MAX;
        config.log_max_batch_bytes = usize::MAX;
    })
}

fn client_with(tweak: impl FnOnce(&mut Config)) -> Client<TestHost> {
    let mut config = Config::new("ps_project_live_bench", "https://ingest.example");
    config.sdk_name = "unity".into();
    config.platform = "android".into();
    config.logs_enabled = true;
    tweak(&mut config);
    let mut client = Client::new(TestHost::new(), config);
    client.set_consent(granted("2026-01", "authorized", true, 1_789_560_000_000));
    answer(&mut client);
    client
}

fn answer(client: &mut Client<TestHost>) {
    for request in client.host().take_sent() {
        client.on_response(accepted(request.id, 1));
    }
}

fn exception(n: usize) -> LogRecord {
    let frames = (0..24)
        .map(|i| StackFrame {
            function: format!("Method{i}"),
            module: if i < 20 {
                "UnityEngine.EventSystems.ExecuteEvents".into()
            } else {
                format!("Game.Shop{n}")
            },
            file: format!("Assets/Scripts/Shop{i}.cs"),
            line: 40 + i as u32,
            in_app: i >= 20,
        })
        .collect();
    LogRecord {
        level: Some(LogLevel::Error),
        logger: "unity".into(),
        exception: Some(LogException {
            type_name: "NullReferenceException".into(),
            value: format!("Object reference not set to an instance of an object ({n})"),
            handled: false,
            frames,
        }),
        ..LogRecord::default()
    }
}

fn gunzipped(body: &[u8]) -> Vec<u8> {
    use std::io::Read;
    let mut out = Vec::new();
    let _ = flate2::read::GzDecoder::new(body).read_to_end(&mut out);
    out
}

fn gzip_fast(body: &[u8]) -> Vec<u8> {
    use std::io::Write;
    let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::fast());
    encoder.write_all(body).unwrap();
    encoder.finish().unwrap()
}

/// With no budget, so every record is queued, and the default 64 KB batch.
fn unbounded_but_batched() -> Client<TestHost> {
    client_with(|config| config.log_max_per_minute = u32::MAX)
}

fn report(name: &str, samples: &mut [u64]) {
    samples.sort_unstable();
    println!(
        "{name:<58} median {:>8.2} µs   p99 {:>8.2} µs",
        samples[samples.len() / 2] as f64 / 1000.0,
        samples[samples.len() * 99 / 100] as f64 / 1000.0,
    );
}

/// Times `op` one call at a time and prints the median and the 99th percentile.
fn measure(name: &str, calls: usize, mut op: impl FnMut(usize)) {
    for n in 0..calls.min(1_000) {
        op(n);
    }
    let mut samples = Vec::with_capacity(calls);
    for n in 0..calls {
        let start = Instant::now();
        op(n);
        samples.push(start.elapsed().as_nanos() as u64);
    }
    samples.sort_unstable();
    let at = |q: f64| samples[((samples.len() as f64 - 1.0) * q) as usize];
    println!(
        "{name:<58} median {:>8.2} µs   p99 {:>8.2} µs   max {:>8.2} µs",
        at(0.5) as f64 / 1000.0,
        at(0.99) as f64 / 1000.0,
        *samples.last().unwrap() as f64 / 1000.0,
    );
}

#[test]
#[ignore]
fn logs_bench() {
    println!();

    measure("scrub: a clean line", 100_000, |_| {
        black_box(scrub(black_box(MESSAGE)));
    });
    measure("scrub: a line with an email and a path", 100_000, |_| {
        black_box(scrub(black_box(
            "Save failed for emma@example.com at /Users/emma/save.dat",
        )));
    });

    let mut c = client(120);
    measure("add_breadcrumb (80 chars)", 100_000, |_| {
        c.add_breadcrumb("log", MESSAGE, LogLevel::Info);
    });
    let long = "x".repeat(2_000);
    measure("add_breadcrumb (2,000 chars)", 100_000, |_| {
        c.add_breadcrumb("log", &long, LogLevel::Info);
    });
    measure("log: an info line kept as a breadcrumb", 100_000, |_| {
        black_box(c.log(LogRecord {
            level: Some(LogLevel::Info),
            message: MESSAGE.into(),
            ..LogRecord::default()
        }));
    });

    // Records are built before the clock starts: what is timed is the SDK, not the benchmark
    // allocating twenty-four frames.
    let prebuilt = |count: usize, distinct: bool| -> Vec<LogRecord> {
        (0..count)
            .map(|n| exception(if distinct { n } else { 0 }))
            .rev()
            .collect()
    };

    let mut c = client(120);
    let mut records = prebuilt(101_000, false);
    measure("log: the same exception again (folded)", 100_000, |_| {
        c.host().advance_ms(1);
        black_box(c.log(records.pop().unwrap()));
    });

    let mut c = client(120);
    for n in 0..120 {
        c.log(exception(n));
    }
    let mut records = prebuilt(101_000, true);
    measure(
        "log: a new exception over the budget (rate-limited)",
        100_000,
        |_| {
            black_box(c.log(records.pop().unwrap()));
        },
    );

    let mut c = unbounded();
    let mut records = prebuilt(21_000, true);
    measure("log: a new exception, 24 frames (recorded)", 20_000, |n| {
        black_box(c.log(records.pop().unwrap()));
        if n % 500 == 0 {
            c.flush();
            answer(&mut c);
        }
    });

    // A tick with a default batch (64 KB) due, in its three parts. The first and last hold
    // the client's lock and are all a game thread could ever wait behind; encoding runs
    // without it, on whatever thread the binding ticks from.
    let mut c = unbounded_but_batched();
    let (mut locked, mut unlocked, mut raw, mut wire) = (Vec::new(), Vec::new(), 0, 0);
    let mut fast = Vec::new();
    for round in 0..200 {
        let mut n = 0;
        let job = loop {
            c.log(exception(round * 1000 + n));
            n += 1;
            let start = Instant::now();
            if let Some(job) = c.tick_begin() {
                locked.push(start.elapsed().as_nanos() as u64);
                break job;
            }
        };
        let start = Instant::now();
        let sealed = job.encode();
        unlocked.push(start.elapsed().as_nanos() as u64);
        let start = Instant::now();
        c.tick_finish(Some(sealed));
        *locked.last_mut().unwrap() += start.elapsed().as_nanos() as u64;
        for request in c.host().take_sent() {
            wire = request.body.len();
            let json = gunzipped(&request.body);
            raw = json.len();
            let start = Instant::now();
            black_box(gzip_fast(&json));
            fast.push((start.elapsed().as_nanos() as u64, gzip_fast(&json).len()));
            c.on_response(accepted(request.id, 200));
        }
    }
    report(
        "tick, batch due: under the lock (take + spool + send)",
        &mut locked,
    );
    report(
        "tick, batch due: encode, outside the lock (json + gzip 6)",
        &mut unlocked,
    );
    let mut fast_times: Vec<u64> = fast.iter().map(|f| f.0).collect();
    report(
        "  of which gzip alone at level 1, for comparison",
        &mut fast_times,
    );
    println!(
        "  a default batch: {} KB of JSON, {} KB at gzip level 6, {} KB at level 1",
        raw / 1024,
        wire / 1024,
        fast.last().map_or(0, |f| f.1) / 1024
    );

    // Analytics, for comparison: a track call, and a tick that seals and sends a full batch of
    // events (sealing, the spool, and gzip at send), all of which holds the client's lock.
    let props = || {
        let mut p = std::collections::BTreeMap::new();
        p.insert("level".to_owned(), crate::event::Value::Int(12));
        p.insert("weapon".to_owned(), crate::event::Value::Str("bow".into()));
        p
    };
    let mut c = client(120);
    measure("analytics: track", 100_000, |n| {
        black_box(c.track("enemy_killed", props()).ok());
        if n % 200 == 0 {
            answer(&mut c);
        }
    });
    let mut c = client(120);
    let mut ticks = Vec::new();
    for _ in 0..100 {
        answer(&mut c);
        for _ in 0..250 {
            let _ = c.track("enemy_killed", props());
        }
        c.host().advance_ms(31_000);
        let start = Instant::now();
        c.tick();
        ticks.push(start.elapsed().as_nanos() as u64);
    }
    report(
        "analytics: tick sealing + sending 250 events (locked)",
        &mut ticks,
    );

    let mut c = client(120);
    measure("tick: nothing to do", 100_000, |_| {
        c.tick();
    });
}

# 0029. A C ABI over an opaque handle, and unwinding panics

**Status:** Accepted
**Date:** 2026-09-17
**Relates to:** project.md §15, §18; docs/sdk/protocol.md; [ADR 0026](0026-compressed-json-and-derived-event-ids.md)

## Context

`sdk/core` implements the protocol once, in Rust, with no I/O of its own. Every binding —
Unity's C#, Swift, Kotlin, a C++ engine — has to reach it, and §15's instruction is the
reason the crate exists: *do not independently reinvent protocol behaviour in every SDK.*

The alternative shape, native SDKs per platform with engine wrappers on top, was rejected
before this record: Swift and Kotlin cover two of the roughly eight targets a game ships to,
so a Unity game on Steam, a console or WebGL would need a third implementation, and three
implementations is the drift §15 warns about.

## Decision

**A C ABI, and everything about it is chosen to make a binding's mistakes loud rather than
silent.**

**Handles are `uint64_t`, generation-tagged, not pointers.** An `IntPtr` cached in a C#
`static` survives a Unity **domain reload**: exit Play mode, re-enter, and the pointer
dangles — a crash on the second Play session that nobody can reproduce on demand. A
generation tag makes that `PS_ERR_BAD_HANDLE` and a log line. Zero is never valid, so a
binding's zeroed field means "not initialized" for free.

**Nothing is ever returned owned.** No function hands the caller memory, so there is no
`ps_string_free` and **no leak is representable**. Values come back through a caller-provided
buffer and a required-length out-parameter, with `(NULL, 0)` as a legal probe.

**Buffers flowing host → core go through a sink callback.** Neither side frees the other's
memory. `store_get` calls the sink once, or **not at all** when the key is absent — so
"absent" and "present but empty" stay different facts, which the core acts on.

**Properties are a flat array, not a builder handle.** A builder reads better in C# and has a
leak mode; a stack array cannot leak. In something designed to sit inside a production game
for hours, a design with no leak mode beats one that reads slightly better.

**Optional values use a natural sentinel**, never a companion flag: `PsStr { NULL, 0 }` is
`None`, a negative `retry_after_ms` is `None`. A `has_value` boolean beside a value admits
`has=0, value=5`, which somebody will eventually construct.

**Three guards against layout drift**, because they catch different things. `ps_abi_version`
catches a deliberate change. `struct_size` as the first field of every caller-populated struct
catches a partial one. `ps_abi_selfcheck` catches the accident — a `bool` marshalled as one
byte instead of four, a `Pack` mismatch, a field reordered in a refactor — where both sides
still report the same version. **That third one is what actually prevents heap corruption**,
and with no CI and no .NET on the development machine it is the only proof a C# `[StructLayout]`
is right, so a binding calls it before anything else.

**`panic = "unwind"`, with `catch_unwind` at every boundary and the handle poisoned on a
catch.** See below.

**An internal mutex, with single-threaded use recommended.** Unity completes a
`UnityWebRequest` on the main thread; `URLSession` and OkHttp do not. The mutex is not a
performance story — it is a failure-mode converter, turning silent heap corruption in a
shipped game into correctness. The latency cost is real and documented: `on_response` holds
the lock across a gzip, so a binding SHOULD marshal completions onto the thread that calls
`track`. A host callback that calls back on the same handle gets `PS_ERR_REENTRANT` rather
than a deadlock.

## The panic decision, with the measurement

`panic` is a **per-profile, workspace-wide** setting; Cargo's per-package overrides do not
accept it, so "abort for the core, unwind for the ABI" was never available.

And the status quo was worse than it looked: `cargo test` builds under `dev`, which unwinds,
so the core's tests were passing under a *different panic strategy than the shipped binary*.
"Panic-free, proven by tests" was proven in the wrong configuration.

Under `abort`, any panic — including the two `expect`s already in `client.rs` — sends SIGABRT
to the game, and the crash report blames the studio rather than PlayerSense. §18's rule is
that analytics must never crash or freeze the game, and it outranks everything else in the
protocol document.

Measured on the linked `aarch64-apple-darwin` dylib:

| | |
|---|---|
| `panic = "abort"` | 486,880 bytes |
| `panic = "unwind"` | 536,784 bytes |
| **cost** | **+49,904 bytes, +10.2%** |

Fifty kilobytes, once, per game, to convert "the player's game dies" into "the SDK returns an
error code and stops". That is a price worth paying, and the alternative was never free — it
was a crash nobody would attribute correctly.

`catch_unwind` compiles identically under both strategies, so the profile stays a one-line
knob: a studio that measures and disagrees can build with `-C panic=abort` and get the old
semantics with no source change.

## Consequences

- **A caught panic poisons the handle.** A client that panicked half way through sealing a
  batch has unknown invariants, so it is retired rather than driven further. The game keeps
  running with analytics off, which is the right reading of §18, and
  `ps_client_is_poisoned` lets a binding surface it.
- **`catch_unwind` does not cover everything.** Out-of-memory aborts unconditionally, which is
  why every allocation the outside world can force is bounded: response bodies and `store_get`
  results are capped at 16 MiB, key enumeration at 100,000, property counts by the core's own
  limits.
- **Host callbacks are `extern "C"`, so a foreign exception crossing one aborts at that
  frame.** This is the design's least comfortable corner and the header says so in capitals.
  The alternative, `extern "C-unwind"`, would let a foreign payload unwind through the state
  machine's frames and arrive at a `catch_unwind` that cannot soundly handle it — deterministic
  abort beats memory corruption. The consequence is a hard rule every binding must honour:
  **every callback body is wrapped in a catch-all.**
- **The randomness fallback needed a counter.** The core pre-seeds `fill_random` so that a
  binding whose RNG is a stub still produces a distinct install id. Seeded from the clock and
  a stack address alone it repeats exactly when two clients are created in the same
  millisecond at the same depth — which is what a test does, and what a game does after a
  domain reload. A process-global nonce fixes it. Found by
  `a_stub_random_still_produces_distinct_installs`.
- **A completion arriving after `ps_client_destroy` returns `PS_ERR_BAD_HANDLE`.** Designed,
  not a defect: the batch it was answering about is still spooled, so nothing is lost. Written
  down so nobody later "fixes" it.

## Alternatives considered

**Raw pointer handles.** Faster by an rwlock read, and it makes the Unity domain-reload case
a dangling dereference instead of an error code.

**A host that returns owned buffers with a `free` callback.** Two coupled functions for a host
to implement, a leak whenever anything goes wrong between them, and it returns a large struct
by value across P/Invoke — a path that is well-defined and historically a source of Mono
marshalling bugs.

**A two-call protocol for `store_get`** (ask the size, then fill). The value can change between
the calls, which is racy for any file-backed host.

**One tagged `set_consent` entry point.** Only one of the three consent states carries data, so
a single function with four parameters that two states ignore is the shape that eventually
gets called with stale values. Three named functions cost nothing.

**A property builder handle.** Covered above: it has a leak mode.

## Enforcement

- `sdk/ffi/src/types.rs` — the layouts, the status codes, and the ownership rules as
  documentation that cbindgen carries into the header a binding author reads.
- `sdk/ffi/src/api.rs` — `with_client`, where the reentrancy guard, the poison check, the lock
  and `catch_unwind` are written once for every exported function.
- `sdk/ffi/src/handle.rs` — the generation-tagged table.
- `the_abi_layout_is_what_the_bindings_declare` pins every `size_of` and `align_of`.
- `the_selfcheck_accepts_a_matching_binding_and_refuses_a_drifted_one` proves the guard fires
  on a one-field mismatch.
- `a_destroyed_handle_is_refused_rather_than_reused` is the domain-reload case.
- `a_host_that_calls_back_from_send_is_refused` proves the reentrancy guard returns rather than
  deadlocks.
- `a_bad_pointer_and_length_pair_is_refused` covers negative lengths and a null pointer with a
  non-zero length.
- `tracking_survives_a_broken_host_and_never_reports_a_fault` is §18's guarantee asserted
  **through the ABI**, which is the only surface a binding ever sees.
- `a_spooled_batch_survives_a_new_client_over_the_same_store` exercises the sink protocol and
  the create-time replay together — the half the core cannot prove.
- `tests/c/layout.c` is compiled by a real C compiler under `-Wall -Wextra -Werror`, with the
  expected sizes and offsets passed in as `-D` defines computed by Rust. A disagreement about
  one field fails the compile. This is the closest available stand-in for the check a C#
  `[StructLayout]` needs and that there is no .NET on this machine to run; verified to have
  teeth by passing a wrong `sizeof(PsValue)`.
- `include/playersense.h` is generated and committed, and `tests/header.rs` regenerates and
  diffs, so the header a binding reads cannot drift from the structs it describes.
- **Miri** runs over the boundary tests — the handle table, the pointer and length
  conventions, the sink protocol — and is clean. It is *not* run over the eight tests that
  compress a batch: `flate2` under an interpreter takes longer than the run is worth, and the
  unsafe code those tests add over the others is one `PsRequest` construction. The command,
  and the skip list, are in the README.
- `build-native.sh` asserts the shared library exports **only** `ps_*` symbols — 23 of them,
  nothing else. Two Rust-based SDKs linked into one iOS binary would otherwise collide on
  `__rust_*`.

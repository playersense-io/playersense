# Platforms

Which platforms the SDK runs on, and what is missing. One Rust core reaches all of them
through the C ABI ([ADR 0029](../adr/0029-a-c-abi-over-an-opaque-handle.md)), so a platform is
"supported" when three things are true: the Rust target builds, the binding wires a host to it,
and something has run the tests there.

## Shipping

| Platform | Rust target | Verified by |
|---|---|---|
| iOS | `aarch64-apple-ios` | Xcode project builds under IL2CPP; `verify.sh --players` |
| iOS simulator | `aarch64-apple-ios-sim` | XCFramework assembles |
| Android arm64 | `aarch64-linux-android` | **Tests run on a Galaxy S23, Android 15, IL2CPP** |
| Android arm32 | `armv7-linux-androideabi` | Packaged and ABI-checked inside the APK |
| Android x86_64 | `x86_64-linux-android` | Builds; emulator only |
| macOS Apple silicon | `aarch64-apple-darwin` | Editor play-mode tests |

## Missing — the SDK cannot ship to these today

A Unity game on Steam is Windows-first, so the first row is the one that matters. A studio
installing the package and building for PC gets a `DllNotFoundException`, not a degraded SDK.

| Platform | Rust target | What it needs |
|---|---|---|
| **Windows x64** | `x86_64-pc-windows-msvc` | A Windows runner. Cross-compiling from macOS needs the MSVC toolchain, which is not redistributable; `x86_64-pc-windows-gnu` via mingw is the fallback if a runner is ever unavailable |
| **Linux x64** | `x86_64-unknown-linux-gnu` | A Linux runner, or `cross`/Docker locally. No linker for it on macOS |
| **macOS Intel** | `x86_64-apple-darwin` | `rustup target add x86_64-apple-darwin`, then `install-plugins.sh` already `lipo`s the two into a universal dylib. **The only one of the three that is a one-liner** — until it is done, the macOS plugin is Apple-silicon only and an Intel editor cannot load it |

All three are what CI exists to produce: `build-native.sh` builds whatever targets the machine
has and names the rest rather than skipping them quietly, so a matrix across three runners
covers the set.

## Not attempted

| Platform | Why |
|---|---|
| WebGL | Needs a static library for `wasm32-unknown-emscripten` built against the exact Emscripten version Unity ships. The core compiling for `wasm32-unknown-unknown` proves it does no I/O of its own; it is **not** a WebGL deliverable |
| Consoles | Each needs an NDA'd toolchain. The C ABI is the right shape for them — a console port is a host implementation, not a second protocol |

## Bindings

Unity is the only one. The others are small by construction, because the protocol is already
implemented: a binding supplies a clock, a file, an HTTP call and a facade in the platform's
own idiom. Worth building when a studio asks rather than speculatively.

| Binding | Status |
|---|---|
| Unity (C#) | Built |
| iOS (Swift), Android (Kotlin) | Not built. Both would use the same static library the Unity package already ships |
| Unreal (C++) | Not built. Consumes the C header directly, with no marshalling layer at all |
| Godot | Not built |
| Web (JS/TS) | Not built, and the only one that cannot reuse the core as-is |

## The check that guards all of it

`build-native.sh` asserts the shared library exports **only** `ps_*` symbols. Two Rust-based
SDKs linked into one iOS binary otherwise collide on their `__rust_*` symbols, and the failure
is a link error in someone else's project, weeks later.

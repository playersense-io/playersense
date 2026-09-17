#!/usr/bin/env bash
#
# Builds the native libraries a binding links against, and checks the two things that go
# wrong silently: what the artifact exports, and how big it is.
#
# Targets are built only when their toolchain is present, and the ones that are missing are
# named rather than skipped quietly — a build that reports success having produced three of
# eight libraries is how a platform ships broken.
#
#   ./build-native.sh              every target this machine can do
#   ./build-native.sh --xcframework  and assemble the Apple one
set -euo pipefail

cd "$(dirname "$0")"
OUT="target/native"
mkdir -p "$OUT"

# Every target a binding wants, and what it is for.
ALL_TARGETS=(
    "aarch64-apple-darwin:macOS, Apple silicon"
    "x86_64-apple-darwin:macOS, Intel"
    "aarch64-apple-ios:iOS device"
    "aarch64-apple-ios-sim:iOS simulator, Apple silicon"
    "aarch64-linux-android:Android arm64"
    "armv7-linux-androideabi:Android arm32"
    "x86_64-linux-android:Android x86_64"
    "x86_64-unknown-linux-gnu:Linux"
    "x86_64-pc-windows-msvc:Windows"
)

# Android cross-compiles need the NDK's linker, one per ABI. Discovered rather than written
# into .cargo/config.toml, because that path is machine-specific and a committed one would be
# wrong on every machine but the author's. Unity's own NDK is preferred when nothing else is
# set: it is the NDK a Unity game is actually built with, so the libraries match the player.
find_ndk() {
    for candidate in "${ANDROID_NDK_HOME:-}" "${ANDROID_NDK_ROOT:-}"; do
        [[ -n "$candidate" && -d "$candidate" ]] && { echo "$candidate"; return; }
    done
    local unity
    unity=$(ls -d /Applications/Unity/Hub/Editor/*/PlaybackEngines/AndroidPlayer/NDK 2>/dev/null \
        | sort -rV | head -1)
    [[ -n "$unity" ]] && { echo "$unity"; return; }
    ls -d "${ANDROID_HOME:-$HOME/Library/Android/sdk}"/ndk/* 2>/dev/null | sort -rV | head -1
}

# Android 6.0, which is Unity 6's minimum. A library built against a newer API than the player
# supports links here and fails to load on a device, with no build-time complaint.
ANDROID_API=23

NDK=$(find_ndk || true)
if [[ -n "$NDK" ]]; then
    HOST=$(ls "$NDK/toolchains/llvm/prebuilt" 2>/dev/null | head -1)
    NDK_BIN="$NDK/toolchains/llvm/prebuilt/$HOST/bin"
    export CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER="$NDK_BIN/aarch64-linux-android$ANDROID_API-clang"
    export CARGO_TARGET_ARMV7_LINUX_ANDROIDEABI_LINKER="$NDK_BIN/armv7a-linux-androideabi$ANDROID_API-clang"
    export CARGO_TARGET_X86_64_LINUX_ANDROID_LINKER="$NDK_BIN/x86_64-linux-android$ANDROID_API-clang"
    echo "NDK: $NDK (API $ANDROID_API)"
else
    echo "No Android NDK found — the three Android targets will fail to link."
fi
echo

built=() missing=()

for entry in "${ALL_TARGETS[@]}"; do
    target="${entry%%:*}"
    description="${entry#*:}"

    if ! rustup target list --installed | grep -qx "$target"; then
        missing+=("$target — $description (rustup target add $target)")
        continue
    fi
    printf '  %-28s ' "$target"
    if cargo build --release --target "$target" -p playersense-ffi >/dev/null 2>&1; then
        built+=("$target")
        # The artifact is named by the platform, not by us: libplayersense.a everywhere
        # except Windows, which produces playersense.lib and playersense.dll. Reported by
        # whichever exists so a Windows runner does not print "0 bytes" and look broken.
        size=0 artifact=""
        for candidate in libplayersense.a playersense.lib; do
            path="target/$target/release/$candidate"
            if [[ -f "$path" ]]; then
                size=$(stat -f%z "$path" 2>/dev/null || stat -c%s "$path")
                artifact="$candidate"
                break
            fi
        done
        printf 'ok  (%s bytes, %s)\n' "$size" "${artifact:-no static library}"
    else
        printf 'FAILED — the target is installed but the linker or SDK is not\n'
        missing+=("$target — $description (toolchain incomplete)")
    fi
done

# What the shared library exports is the number that matters, and the only one a game feels.
#
# Two Rust-based SDKs linked into one iOS binary produce duplicate `__rust_*` symbols, so a
# static library must export nothing but ours. Rust hides everything else by default for a
# cdylib; this asserts it rather than trusting it.
if [[ -f target/release/libplayersense.dylib ]]; then
    echo
    echo "Exported symbols (host dylib):"
    foreign=$(nm -gU target/release/libplayersense.dylib | awk '{print $3}' | sed 's/^_//' \
        | grep -vc '^ps_' || true)
    total=$(nm -gU target/release/libplayersense.dylib | wc -l | tr -d ' ')
    if [[ "$foreign" != "0" ]]; then
        echo "  FAIL: $foreign symbols outside the ps_ prefix are exported"
        nm -gU target/release/libplayersense.dylib | awk '{print $3}' | sed 's/^_//' \
            | grep -v '^ps_' | sed 's/^/    /'
        exit 1
    fi
    echo "  ok  $total symbols, all ps_*"
    printf '  linked size: %s bytes\n' \
        "$(stat -f%z target/release/libplayersense.dylib 2>/dev/null \
            || stat -c%s target/release/libplayersense.dylib)"
fi

if [[ "${1:-}" == "--xcframework" ]]; then
    echo
    echo "Assembling the XCFramework..."
    rm -rf "$OUT/PlayerSense.xcframework"
    xcodebuild -create-xcframework \
        -library target/aarch64-apple-ios/release/libplayersense.a -headers ffi/include \
        -library target/aarch64-apple-ios-sim/release/libplayersense.a -headers ffi/include \
        -output "$OUT/PlayerSense.xcframework" >/dev/null
    echo "  $OUT/PlayerSense.xcframework"
fi

echo
echo "Built: ${#built[@]} target(s)."
if [[ ${#missing[@]} -gt 0 ]]; then
    echo "Not built on this machine — these need CI or another toolchain:"
    printf '  %s\n' "${missing[@]}"
fi

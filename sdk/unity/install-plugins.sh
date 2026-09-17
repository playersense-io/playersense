#!/usr/bin/env bash
#
# Copies the native libraries built by ../build-native.sh into the Unity package, under the
# folder names Unity's plugin importer understands.
#
# The binaries are not committed: the iOS static library alone is 21 MB unstripped, against
# about 550 KB once the linker has taken what it needs, and a repository that carries eight of
# those is a repository nobody can clone. So this is a release step, and a checkout of the
# monorepo builds the SDK from source.
#
#   ../build-native.sh && ./install-plugins.sh
set -euo pipefail

cd "$(dirname "$0")"
SDK=".."
OUT="Runtime/Plugins"

copied=() missing=()

install_one() {          # target, destination, source-name
    local src="$SDK/target/$1/release/$3"
    local dst="$OUT/$2"

    if [[ ! -f "$src" ]]; then
        missing+=("$2 — build $1 first")
        return
    fi
    mkdir -p "$(dirname "$dst")"
    cp "$src" "$dst"
    copied+=("$(printf '%-38s %8s KB' "$2" "$(( $(stat -f%z "$dst" 2>/dev/null || stat -c%s "$dst") / 1024 ))")")
}

# iOS is a static library linked into the app binary, which is why P/Invoke names "__Internal"
# there and "playersense" everywhere else.
install_one aarch64-apple-ios          iOS/libplayersense.a            libplayersense.a

# macOS has to serve both the Editor and a player, on two architectures, from one file.
arm="$SDK/target/aarch64-apple-darwin/release/libplayersense.dylib"
intel="$SDK/target/x86_64-apple-darwin/release/libplayersense.dylib"
if [[ -f "$arm" && -f "$intel" ]]; then
    mkdir -p "$OUT/macOS"
    lipo -create "$arm" "$intel" -output "$OUT/macOS/libplayersense.dylib"
    copied+=("$(printf '%-38s %8s KB (universal)' "macOS/libplayersense.dylib" \
        "$(( $(stat -f%z "$OUT/macOS/libplayersense.dylib") / 1024 ))")")
elif [[ -f "$arm" ]]; then
    install_one aarch64-apple-darwin   macOS/libplayersense.dylib      libplayersense.dylib
    missing+=("macOS x86_64 — the dylib is Apple silicon only (rustup target add x86_64-apple-darwin)")
else
    missing+=("macOS — build aarch64-apple-darwin first")
fi

install_one aarch64-linux-android      Android/arm64-v8a/libplayersense.so    libplayersense.so
install_one armv7-linux-androideabi    Android/armeabi-v7a/libplayersense.so  libplayersense.so
install_one x86_64-linux-android       Android/x86_64/libplayersense.so       libplayersense.so
install_one x86_64-unknown-linux-gnu   Linux/x86_64/libplayersense.so         libplayersense.so
install_one x86_64-pc-windows-msvc     Windows/x86_64/playersense.dll         playersense.dll

echo "Installed into $OUT:"
if [[ ${#copied[@]} -eq 0 ]]; then
    echo "  nothing — run ../build-native.sh first"
else
    printf '  %s\n' "${copied[@]}"
fi

if [[ ${#missing[@]} -gt 0 ]]; then
    echo
    echo "Not installed — the package will not run on these platforms:"
    printf '  %s\n' "${missing[@]}"
fi

echo
echo "Import settings are applied by the package on load — nothing to configure."
echo "Next: ./verify.sh, or ./verify.sh --players to build real Android and iOS players."

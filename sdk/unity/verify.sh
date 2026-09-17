#!/usr/bin/env bash
#
# Compiles the package and runs its play-mode tests, in a throwaway Unity project.
#
# This is the only place the C# is checked at all: there is no CI, and `cargo test` proves the
# protocol but not one line of the binding. It needs the native libraries, so build them first.
#
#   cd .. && ./build-native.sh && cd unity && ./install-plugins.sh && ./verify.sh
#   ./verify.sh --players     also build an Android APK and an iOS Xcode project (slow)
#   ./verify.sh --device      run the tests on an attached Android phone, under IL2CPP
#
# Why --players is not optional-in-spirit: the platform code is guarded
# `#if UNITY_ANDROID && !UNITY_EDITOR`, and UNITY_EDITOR is defined in the editor whatever the
# active build target is. No editor-side compile can reach that code — only a real player
# build. Without --players, `StorageRoot`'s JNI call and `SetNoBackupFlag` are unread by any
# compiler.
set -euo pipefail

cd "$(dirname "$0")"
PACKAGE="$(pwd -P)"
PROJECT="$PACKAGE/../target/unity-verify"
PLAYERS=0
DEVICE=0
case "${1:-}" in
    --players) PLAYERS=1 ;;
    --device)  DEVICE=1 ;;
    "")        ;;
    *) echo "usage: verify.sh [--players | --device]" >&2; exit 1 ;;
esac

UNITY="${UNITY:-}"
if [[ -z "$UNITY" ]]; then
    # Newest first, so a machine with several editors uses the one most likely to be current.
    UNITY=$(ls -d /Applications/Unity/Hub/Editor/*/Unity.app/Contents/MacOS/Unity 2>/dev/null \
        | sort -rV | head -1)
fi
if [[ ! -x "$UNITY" ]]; then
    echo "No Unity editor found. Install one, or set UNITY=/path/to/Unity." >&2
    exit 1
fi
VERSION=$(basename "$(dirname "$(dirname "$(dirname "$(dirname "$UNITY")")")")")
echo "Unity $VERSION"

if [[ ! -e "$PACKAGE/Runtime/Plugins/macOS/libplayersense.dylib" ]]; then
    echo "No macOS library — run ../build-native.sh && ./install-plugins.sh first." >&2
    exit 1
fi

# A fresh project each time. The Library cache is what makes an import fast, and it is also
# what makes a stale one look like a pass, so it is not kept.
rm -rf "$PROJECT"
mkdir -p "$PROJECT/Assets/Editor" "$PROJECT/Packages" "$PROJECT/ProjectSettings"
echo "m_EditorVersion: $VERSION" > "$PROJECT/ProjectSettings/ProjectVersion.txt"
cat > "$PROJECT/Packages/manifest.json" <<JSON
{
  "dependencies": {
    "com.playersense.unity": "file:$PACKAGE",
    "com.unity.test-framework": "1.4.5",
    "com.unity.modules.unitywebrequest": "1.0.0",
    "com.unity.modules.jsonserialize": "1.0.0",
    "com.unity.modules.androidjni": "1.0.0"
  },
  "testables": ["com.playersense.unity"]
}
JSON

# The build harness belongs to the verification, not to the package: a studio installing
# PlayerSense should not find a build script in it.
cat > "$PROJECT/Assets/Editor/PlayerSenseVerify.cs" <<'CSHARP'
using UnityEditor;
using UnityEditor.Build;
using UnityEditor.Build.Reporting;
using UnityEditor.SceneManagement;
using UnityEngine;

public static class PlayerSenseVerify
{
    public static void Android() =>
        Build(BuildTarget.Android, NamedBuildTarget.Android, "Build/verify.apk");

    public static void Ios() =>
        Build(BuildTarget.iOS, NamedBuildTarget.iOS, "Build/xcode");

    /// <summary>
    /// Puts the project into the configuration a game ships in, for the on-device test run.
    /// </summary>
    /// <remarks>
    /// The test runner builds its own player, so these must be set beforehand. IL2CPP is the
    /// point of running on a device at all: it is a different runtime from the editor's Mono,
    /// with different P/Invoke and callback rules, and it is what every shipped Android release
    /// build uses.
    /// </remarks>
    public static void ConfigureAndroid()
    {
        PlayerSettings.SetScriptingBackend(NamedBuildTarget.Android, ScriptingImplementation.IL2CPP);
        PlayerSettings.SetManagedStrippingLevel(NamedBuildTarget.Android, ManagedStrippingLevel.High);
        PlayerSettings.SetApplicationIdentifier(NamedBuildTarget.Android, "dev.playersense.verify");
        PlayerSettings.Android.targetArchitectures = AndroidArchitecture.ARM64;
        PlayerSettings.companyName = "PlayerSense";
        PlayerSettings.productName = "Verify";
        AssetDatabase.SaveAssets();
        EditorApplication.Exit(0);
    }

    private static void Build(BuildTarget target, NamedBuildTarget named, string output)
    {
        const string scenePath = "Assets/Verify.unity";
        var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
        EditorSceneManager.SaveScene(scene, scenePath);

        // IL2CPP and High stripping deliberately: that combination is what a shipped game uses
        // and what deletes a [RuntimeInitializeOnLoadMethod] nothing references.
        PlayerSettings.SetScriptingBackend(named, ScriptingImplementation.IL2CPP);
        PlayerSettings.SetManagedStrippingLevel(named, ManagedStrippingLevel.High);
        PlayerSettings.SetApplicationIdentifier(named, "dev.playersense.verify");
        PlayerSettings.companyName = "PlayerSense";
        PlayerSettings.productName = "Verify";

        if (target == BuildTarget.Android)
        {
            // Both ABIs, because the point is to prove the importer assigned each library to
            // the right one, and a build covering one ABI would prove half of that.
            PlayerSettings.Android.targetArchitectures =
                AndroidArchitecture.ARM64 | AndroidArchitecture.ARMv7;
        }

        var report = BuildPipeline.BuildPlayer(new BuildPlayerOptions
        {
            scenes = new[] { scenePath },
            locationPathName = output,
            target = target,
            targetGroup = BuildPipeline.GetBuildTargetGroup(target),
            options = BuildOptions.None,
        });

        foreach (var step in report.steps)
        {
            foreach (var message in step.messages)
            {
                if (message.type == LogType.Error || message.type == LogType.Exception)
                    Debug.Log("VERIFY-ERROR " + message.content);
            }
        }
        Debug.Log($"VERIFY {target} {report.summary.result} errors={report.summary.totalErrors}");
        EditorApplication.Exit(report.summary.result == BuildResult.Succeeded ? 0 : 1);
    }
}
CSHARP

errors() {              # log
    if grep -q 'error CS' "$1"; then
        grep 'error CS' "$1" | sed 's/^.*error CS/    error CS/' | sort -u
        return 1
    fi
    return 0
}

echo "Compiling and running play-mode tests..."
set +e
"$UNITY" -batchmode -nographics -projectPath "$PROJECT" \
    -runTests -testPlatform PlayMode \
    -testResults "$PROJECT/results.xml" -logFile "$PROJECT/unity.log" >/dev/null 2>&1
set -e

errors "$PROJECT/unity.log" || exit 1

RESULTS="$PROJECT/results.xml" LABEL="in the editor" python3 "$PACKAGE/.results.py"

if [[ $DEVICE -eq 1 ]]; then
    ADB="${ADB:-$(dirname "$UNITY")/../../../PlaybackEngines/AndroidPlayer/SDK/platform-tools/adb}"
    [[ -x "$ADB" ]] || ADB=$(command -v adb || true)
    if [[ ! -x "$ADB" ]]; then
        echo "No adb found. Set ADB=/path/to/adb." >&2
        exit 1
    fi
    # Starting the adb daemon makes the phone re-prompt for USB debugging, and it will do so
    # again on any machine reboot — so this waits rather than throwing away the run that is
    # about to take five minutes.
    "$ADB" start-server >/dev/null 2>&1 || true
    waited=0
    until "$ADB" devices | awk 'NR>1 && $2=="device" {found=1} END {exit !found}'; do
        if [[ $waited -eq 0 ]]; then
            echo "Waiting for an authorised device — tap \"Allow\" on the phone."
            "$ADB" devices | sed 's/^/  /'
        fi
        sleep 2
        waited=$((waited + 2))
        if [[ $waited -ge 60 ]]; then
            echo "No authorised device after 60s. Check the cable and the prompt." >&2
            exit 1
        fi
    done
    model=$("$ADB" shell getprop ro.product.model | tr -d '\r')
    release=$("$ADB" shell getprop ro.build.version.release | tr -d '\r')
    abi=$("$ADB" shell getprop ro.product.cpu.abi | tr -d '\r')
    echo
    echo "On device: $model / Android $release / $abi"

    "$UNITY" -batchmode -quit -nographics -projectPath "$PROJECT" \
        -buildTarget Android -executeMethod PlayerSenseVerify.ConfigureAndroid \
        -logFile "$PROJECT/configure.log" >/dev/null 2>&1

    echo "Building an IL2CPP player and running the tests on it — several minutes..."
    set +e
    "$UNITY" -batchmode -nographics -projectPath "$PROJECT" \
        -buildTarget Android -runTests -testPlatform Android \
        -testResults "$PROJECT/results-device.xml" \
        -logFile "$PROJECT/device.log" >/dev/null 2>&1
    set -e

    errors "$PROJECT/device.log" || exit 1
    RESULTS="$PROJECT/results-device.xml" LABEL="on the device, IL2CPP" python3 "$PACKAGE/.results.py"
    exit $?
fi

if [[ $PLAYERS -eq 0 ]]; then
    echo
    echo "Not checked: the #if UNITY_ANDROID / #if UNITY_IOS branches, which no editor-side"
    echo "compile can reach. Run ./verify.sh --players for those."
    exit 0
fi

build_player() {        # label, method, target
    echo
    echo "Building a $1 player (IL2CPP, stripping High) — this takes a few minutes..."
    set +e
    "$UNITY" -batchmode -nographics -projectPath "$PROJECT" \
        -buildTarget "$3" -executeMethod "PlayerSenseVerify.$2" \
        -logFile "$PROJECT/build-$1.log" >/dev/null 2>&1
    local code=$?
    set -e

    errors "$PROJECT/build-$1.log" || return 1
    grep -h 'VERIFY-ERROR' "$PROJECT/build-$1.log" | sed 's/^/    /' | head -20
    grep -h '^VERIFY ' "$PROJECT/build-$1.log" | sed 's/^/  /'
    return $code
}

build_player Android Android Android || exit 1

# The APK is the only place the importer's ABI assignment becomes a fact rather than a setting.
#
# Listed once into a file rather than piped into each grep: `set -o pipefail` plus a `grep -q`
# that exits on the first match makes unzip die of SIGPIPE, and the pipeline then reports 141
# for a grep that matched. That cost a false failure once already.
LISTING="$PROJECT/apk-contents.txt"
unzip -l "$PROJECT/Build/verify.apk" > "$LISTING"

echo "  Libraries inside the APK:"
awk '/libplayersense/ {print "    " $4 "  " $1 " bytes"}' "$LISTING"

missing=0
for abi in arm64-v8a armeabi-v7a; do
    grep -q "lib/$abi/libplayersense.so" "$LISTING" || {
        echo "    MISSING lib/$abi/libplayersense.so — the importer's CPU setting is wrong"
        missing=1
    }
done
if grep -q 'libplayersense\.a' "$LISTING"; then
    echo "    The iOS static library was packaged into an APK — it is marked Any Platform"
    missing=1
fi
[[ $missing -eq 0 ]] || exit 1

# The managed half of the same question. An assembly nothing references is deleted outright by
# the linker, and the integration this package recommends — a project key in Project Settings
# and no Track call anywhere — is exactly that shape. Measured once with the assembly missing
# from this directory, which is what [assembly: AlwaysLinkAssembly] exists to prevent.
STRIPPED="$PROJECT/Library/Bee/artifacts/Android/ManagedStripped/PlayerSense.Runtime.dll"
if [[ -f "$STRIPPED" ]]; then
    echo "  Survives managed stripping: $(stat -f%z "$STRIPPED" 2>/dev/null \
        || stat -c%s "$STRIPPED") bytes after the linker"
else
    echo "  PlayerSense.Runtime.dll was STRIPPED OUT of the build."
    echo "  A game that only configures a project key would collect nothing, silently."
    exit 1
fi

build_player iOS Ios iOS || exit 1
echo "  Xcode project at $PROJECT/Build/xcode"

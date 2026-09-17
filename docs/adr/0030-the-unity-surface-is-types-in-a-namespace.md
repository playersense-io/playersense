# 0030. The Unity binding: types in a namespace, no_backup, and importer settings in code

**Status:** Accepted
**Date:** 2026-09-17
**Relates to:** project.md §15, §18, §30; docs/sdk/protocol.md §2, §3;
[ADR 0004](0004-installation-identity.md); [ADR 0029](0029-a-c-abi-over-an-opaque-handle.md)

## Context

The specified call sites are exact:

```csharp
PlayerSense.Analytics.Track("enemy_killed", new() { ["enemy"] = "orc" });
PlayerSense.Context.Session.Set("game_mode", "ranked");
PlayerSense.Identity.SetPlayer("user_8842");
```

The obvious shape — a static class `PlayerSense` with properties `Analytics`, `Context` and
`Identity`, inside a namespace `PlayerSense` — **does not compile in a game's project**. C#
resolves a namespace before a type of the same name in the same enclosing namespace
(ECMA-334 §7.8): the walk checks "is `I` a namespace in `N`" *before* "is `I` a type in `N`",
so from a Unity script with no namespace of its own, `PlayerSense` binds to the namespace and
`PlayerSense.Analytics` is an error. It compiles inside the package, where the enclosing
namespace declaration finds the type first. The failure therefore appears only in someone
else's project, on the first line they write.

Separately, §3 requires the installation identifier to be excluded from platform backup: if
iCloud or Android Auto Backup restores it onto a second device, one installation emits from
two phones forever, every retention curve built on it is wrong, and nothing anywhere reports
a problem. iOS has a per-file flag. Android was documented in this package as *not solvable
in code* — Auto Backup being manifest-configured — with the package expected to ship an
`android:fullBackupContent` rule for the game's build to merge.

## Decision

**`Analytics`, `Identity`, `Privacy` and `Context` are static classes in the namespace
`PlayerSense`, not members of a class.** Every specified call site then compiles verbatim
from a file with no `using` directive at all, which is the actual requirement — a game does
not import anything to call this. Each forwards to `PlayerSense.SDK.Client`, the one shared
`PlayerSenseClient`, which is also the object a studio injects if it prefers that.

**Lifecycle verbs live on `PlayerSense.SDK`**: `Initialize`, `Flush`, `Shutdown`, `Client`,
`IsInitialized`, `Stats`. `PlayerSense.Initialize` cannot exist, for the reason above.

**Every static call is a no-op when the SDK is not running**, with one warning the first time
and no exception ever. A missing key, a missing plugin, an ABI mismatch, a call before
`Initialize` — a game must not crash for any of them (§18).

**The Android install id lives under `getNoBackupFilesDir()`**, not under
`persistentDataPath` with a manifest rule. The platform excludes that directory from Auto
Backup *and* from device-to-device transfer, and it needs nothing from the game.

**The native plugins' import settings come from code, not from their `.meta` files.** Unity
guesses a plugin's platform from its folder and guesses wrong: measured on an editor with the
iOS module not installed, `Runtime/Plugins/iOS/libplayersense.a` imported as **Any Platform**
with iOS itself unticked — a static library offered to Windows, Android and Linux builds and
withheld from the one platform it is for. Committing the `.meta` does not fix it, because the
binaries are a release artifact and the `.meta` is written by whichever machine ran
`install-plugins.sh`. `Editor/PlayerSensePluginImporter.cs` applies the rule on load and
rewrites the `.meta` only when it differs.

## Consequences

`PlayerSense.SDK.Initialize(key)` is one token longer than it should be, and a developer who
guesses `PlayerSense.Initialize` gets a compile error rather than a method. This is the whole
cost of the namespace decision and it is paid at the least important call site: the one a
game makes once, or never, because Project Settings starts the SDK before the first scene.

The static classes cannot be mocked. A test that wants a fake takes the `PlayerSenseClient`
instead, which is why the modules are real objects on the client rather than static logic
with a facade over nothing.

`getNoBackupFilesDir()` is a JNI call through `UnityPlayer.currentActivity`, so it can fail
where there is no activity — a headless build, a very early call. It falls back to
`persistentDataPath` **and logs a warning saying the identifier may now be restored onto a
second device**, because the alternative is a wrong number that looks fine.

`Flags`, `LiveOps` and `Log` are absent. `config-api` does not exist, and an API that always
returns the fallback is worse than no API: a studio would ship targeting rules that never
match, with nothing to tell them why. They are namespace slots that stay empty until the
server side is real.

## Alternatives considered

**A global-namespace `static class PlayerSense` with everything nested inside it.** This does
make `PlayerSense.Initialize` work. It also requires that no namespace named `PlayerSense`
exists anywhere in the package, so `PlayerSenseClient`, `UnityHost` and the P/Invoke layer
would have to live under a second root identifier — and it puts a type in the global
namespace of every project that installs the package. Rejected: one awkward token against
polluting every game's global scope.

**`using PlayerSense;` in the game's own file, inside its namespace declaration.** This does
reach a same-named type, because using-directives of the enclosing namespace declaration are
consulted before the outer namespace. It fails for a file-top `using`, and it fails for every
Unity script with no namespace, which is most of them. An API that compiles depending on
where the `using` was typed is not an API.

**Shipping `android:fullBackupContent` in the package manifest.** It would have to merge with
whatever backup configuration the game already has, and an Android manifest merge conflict in
someone else's build is a support ticket in a project we cannot see. The no-backup directory
is the platform's own answer to the same question and conflicts with nothing.

**`PlayerPrefs` for the install id**, on any platform. §3 already forbids it: `PlayerPrefs` is
`NSUserDefaults` and `SharedPreferences`, and both are copied by backup. Recorded here
because it is the default thing a Unity developer reaches for.

## Enforcement

`sdk/unity/verify.sh` compiles the package and runs its tests in a throwaway Unity project,
which is the only place the C# is checked at all — `cargo test` proves the protocol and not
one line of the binding. `sdk/unity/Tests/Runtime/BoundaryTests.cs` is deliberately not a
second implementation of the protocol suite; it covers only the seam a binding can break:

- `TheNativeLibraryAgreesAboutEveryLayout` — `ps_abi_selfcheck` against C#'s own
  `Marshal.SizeOf`. A field added on one side and not the other corrupts the heap silently.
  Confirmed to have teeth: adding an `int64_t` to `Native.PsConfig` fails it with *"PsConfig:
  the binding says 272 and this binary says 264"*, and the SDK refuses to start rather than
  writing past the end of a struct.
- `TheHostSurvivesACollection` — forces a GC, then asserts a batch still reaches the disk.
  Mono collects a delegate whose only reference is a function pointer native code holds.
- `TheInstallIdIsKeptOutOfPlatformBackup` — asserts the Android storage root is under
  `no_backup`. On iOS the flag is not readable from managed code, so that half is asserted
  only as "the directory the flag was applied to exists".
- `TheApiIsSafeBeforeTheSdkStarts` — every verb called with no client, asserting no throw.

**The platform branches need a real player build, and nothing less will do.** They are guarded
`#if UNITY_ANDROID && !UNITY_EDITOR`, and `UNITY_EDITOR` is defined in the editor whatever the
active build target is — so switching build target and recompiling does not reach them. This
was found the way it should be: a `-buildTarget Android` compile check reported *ok* with
`ThisMethodDoesNotExist` deliberately planted in that branch. `./verify.sh --players` builds
an Android APK and an iOS Xcode project under IL2CPP with stripping at High, which is the only
thing that compiles them.

That build also settles two questions a setting cannot. It unzips the APK and asserts
`lib/arm64-v8a/libplayersense.so` and `lib/armeabi-v7a/libplayersense.so` are both present —
the importer's per-ABI `CPU` data as a fact rather than a value in a `.meta` — and that the
iOS static library was *not* packaged into it, which is what "Any Platform" would have done.

**And the tests run on a real phone.** `./verify.sh --device` builds an IL2CPP player, deploys
it to an attached Android device and runs the same four tests there. That is not the same run
as the editor's, and three of the four mean something different on it:

- `ps_abi_selfcheck` is checked against the **Android arm64 ABI** rather than macOS arm64, and
  under IL2CPP's marshalling rather than Mono's.
- `[MonoPInvokeCallback]` is a no-op in the editor and **mandatory** under IL2CPP. All nine
  host callbacks depend on it, and the failure mode is a crash on the second or third one.
- `getNoBackupFilesDir()` finally *executes*. The JNI call is behind `!UNITY_EDITOR`, so the
  editor run asserts a path it could never have produced; on device, the assertion that the
  root contains `no_backup` is the real thing. Had the JNI call thrown, `StorageRoot` would
  have fallen back to `persistentDataPath` and the test would fail — so a pass is proof, not
  the absence of evidence.

Recorded run: Galaxy S23 (SM-S911B), Android 15, arm64-v8a — 4 passed, 0 failed, with
`nativeloader: Load .../lib/arm64/libplayersense.so ... ok` in logcat confirming the Rust
library was loaded by the platform rather than merely packaged.

One thing that has no test: the namespace rule, because testing it would require a second
project that consumes the package. It is enforced by the shape of the code instead — there is
no type named `PlayerSense`, so the failing arrangement cannot be reintroduced by accident.

**Stripping deletes the whole assembly, and only `[assembly: AlwaysLinkAssembly]` stops it.**
Measured on an Android IL2CPP build with stripping at High: `PlayerSense.Runtime.dll` was not
in UnityLinker's output directory at all. That is the linker behaving correctly — nothing in
that project referenced it — and it is catastrophic here, because the integration this package
recommends is the one with no code in it. A studio that pastes a project key into Project
Settings and never writes a `Track` call has nothing referencing the assembly, so analytics
works in the editor and silently does not exist in the shipped build. The zero-code path is
precisely the one that breaks.

With the attribute, the assembly is rooted, `[RuntimeInitializeOnLoadMethod]` roots `Bootstrap`,
and disassembling the stripped result shows everything the runtime and the native side reach
survives with its body: `Bootstrap`, `Load`, `Build`, `Vtable`, `StorageRoot`,
`ExcludeFromBackup` and all nine host callbacks. `Track` and the other game-facing verbs are
stripped, which is correct — a game that calls them keeps them.

Two mechanisms were tried first and rejected **on measurement**, and the ADR records them so
they are not re-added in good faith. `Runtime/link.xml` is not honoured from inside a package:
UnityLinker's command line carries exactly two `--include-link-xml` arguments, both Unity's
own, and a package's own `link.xml` is imported and never passed. `[assembly: Preserve]` costs
512 bytes of metadata and changes no surviving member — the list above is identical with and
without it. Both were deleted rather than left in, because a mechanism that looks like
protection and is not is worse than none.

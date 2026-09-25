# PlayerSense for Unity

Analytics that never blocks, never throws and never loses a session.

```csharp
PlayerSense.Analytics.Track("level_completed", new() { ["level"] = 12, ["score"] = 9500 });
```

That is the whole integration. There is no `await`, no callback, no result to check and
nothing to wrap in `try`/`catch` — a game that had to handle analytics failures would be a
game whose analytics had become a dependency.

## Install

Package Manager ▸ **+** ▸ *Add package from git URL*:

```
https://github.com/playersense/playersense.git?path=/sdk/unity
```

Then **Edit ▸ Project Settings ▸ PlayerSense**, create the settings asset, and paste your
project key (`ps_project_live_…`). The SDK starts itself before the first scene loads.

Prefer to start it yourself — after a consent dialog, say? Turn off *Start automatically* and:

```csharp
PlayerSense.SDK.Initialize("ps_project_live_…");
```

`Initialize` performs no network operation. It opens a file and returns, so it cannot fail
because a server is unreachable and cannot delay your launch.

## The API

Four things a game touches. Everything else the SDK handles.

```csharp
// What happened.
PlayerSense.Analytics.Track("enemy_killed", new() { ["enemy"] = "orc", ["weapon"] = "bow" });
PlayerSense.Analytics.TrackRevenue("iap_purchased", 4.99m, "USD", new() { ["sku"] = "gems_500" });

// Who is playing. Call it when your login completes; the session is not restarted.
PlayerSense.Identity.SetPlayer("user_8842");

// What they agreed to.
PlayerSense.Privacy.Grant("2026.09");   // the version of your terms they accepted
PlayerSense.Privacy.Deny();             // a refusal, or a withdrawal

// Your game's own vocabulary.
PlayerSense.Context.Session.Set("game_mode", "ranked");
PlayerSense.Context.Session.Set("map", "desert_01");
PlayerSense.Context.Player.Set("progress.level", 42);
PlayerSense.Context.Player.Set("rank", "diamond");
```

No `using` directive is needed for any of it.

### Properties or context?

A **property** describes one occurrence: the weapon that killed this orc. **Context** describes
what was true while it happened: the player's rank, the map, the difficulty. Context is set
once and decorates every event after it, so you do not copy `rank` onto forty call sites — and
it costs about a tenth of a byte per event on the wire instead of twelve.

Context set now affects future events only. An event already recorded keeps the context it was
recorded with, which is why a chart of "events by rank" means what it says.

Session context clears when the session does. Player context clears when `SetPlayer` changes
who is playing.

### What PlayerSense will not let you name

`playersense.*`, `geo.*`, `network.*`, `account.*`, `commerce.*`, `app.*`, `device.*`, `sdk.*`
and `install.*` are reserved, and `Context.Set` refuses them with an error naming the owner.

Geography in particular is **not** something the SDK sends. Country, region, ISP, ASN and
whether a connection is a VPN or a datacentre are derived on the server from the connection
itself, because a value a client supplies is a value a modified build can choose. A targeting
rule on `geo.country` is therefore worth something; one on a client-supplied `country` would
not be.

## What you do not have to do

| | |
|---|---|
| Start and end sessions | There is no `StartSession`. Sessions are derived from activity and a background timeout |
| Handle being offline | Events go to disk and are sent on the next launch. A player with no connection for a week loses nothing until the spool's limits are reached |
| Retry | Failures back off and retry. A `Retry-After` is honoured |
| Deduplicate | A batch that was delivered but whose response was lost is recognised and not counted twice |
| Handle a quit | A player who quits after eight seconds still produces a session, because the batch was already on disk |
| Buffer for consent | Events collected before the player answers are held **in memory only** and never touch the disk. Granting makes them lawful and sends them; denying discards them |

The full set of guarantees, including what happens when the process is killed between spooling
and the acknowledgement, is [docs/sdk/protocol.md](../../docs/sdk/protocol.md).

## Errors and crashes

Off until you turn it on, because turning it on changes what you declare to the stores (below).

```csharp
PlayerSense.SDK.Initialize(new PlayerSenseOptions
{
    ProjectKey = "ps_project_live_…",
    Logs = { Enabled = true },
});
```

or tick **Capture logs** in Edit ▸ Project Settings ▸ PlayerSense. From then on
`Debug.LogError`, `Debug.LogException`, exceptions nothing caught (on any thread, and in tasks
nobody awaited) and crashes are recorded with no code. Lines below Warning are kept in memory as
breadcrumbs and travel with the next error. For the rest:

```csharp
PlayerSense.Logs.Error("Receipt refused", new Dictionary<string, string> { ["store"] = "google" });
PlayerSense.Logs.Exception(e);                        // one your code caught
PlayerSense.Logs.Breadcrumb("shop", "Opened the gem pack offer");
```

**Crashes** come from the operating system, read on the next launch: Android's exit reasons on
Android 11+ (Java and native crashes, ANRs, being killed for memory in front of the player) and
MetricKit on iOS 14+. The SDK installs no signal handler, so it cannot fight Unity's own crash
handling or another crash reporter you ship. Elsewhere, a run that ended in the foreground
without going to the background or quitting is reported as `AbnormalExit`.

**Cost.** Unity's log callback copies two references into a fixed ring and returns. Parsing and
sending happen four times a second on the main thread. An error thrown every frame becomes one
record with a count, a device sends at most 120 records a minute (crashes always go), and
batches are gzip, sent every 15 seconds.

### Consent for error reports

By default error reports follow the analytics answer: one consent screen, one answer, nothing
else to do. Before the player answers, errors wait in memory and a crash waits on the device;
"yes" sends them and "no" deletes them.

If your game has its own **Share error reports** switch, the way desktop apps ask:

```csharp
PlayerSense.Privacy.GrantErrorReports("2026-01");   // yes, whatever they said about analytics
PlayerSense.Privacy.DenyErrorReports();             // no; waiting crash reports are deleted
PlayerSense.Privacy.ErrorReportsFollowAnalytics();  // back to the default
```

Or ask after the fact, when there is something to send:

```csharp
if (PlayerSense.Privacy.PendingCrashReports > 0)
    ShowDialog("The game closed unexpectedly last time. Send a report?",
        yes: () => PlayerSense.Privacy.GrantErrorReports("2026-01"),
        no: () => PlayerSense.Privacy.DiscardPendingCrashReports());
```

### What the SDK does about personal data

- **Scrubbed on the device, and again on the server:** emails, bearer tokens and JWTs,
  `password=`-style pairs, card numbers, IP addresses and the account name in file paths
  (`C:\Users\Emma\` becomes `C:\Users\[user]\`).
- **Never read:** the device name (`SystemInfo.deviceName`, which is what the player called
  their phone), `deviceUniqueIdentifier`, any advertising identifier or IDFV. The device is
  described by its model (`SM-S918B`, `iPhone15,2`).
- **Yours to remove:** what only your game knows is personal, such as a display name in a
  message. `Logs.BeforeSend` sees every record on the main thread; change it or return `false`.
- **Games for children:** set `ChildDirected` (or tick it in Project Settings). Error reports
  then carry no player id and none of your attributes.

### What to declare to the stores

The package ships `Runtime/Plugins/iOS/PrivacyInfo.xcprivacy`, which Xcode merges into your
app's privacy report. Your own answers in App Store Connect and Play Console are still yours;
these are the ones that match what the SDK collects.

**App Store Connect, App Privacy.** Tracking: **No**.

| Data type | With | Linked to the user | Used for tracking | Purpose |
|---|---|---|---|---|
| Product Interaction | analytics | Yes | No | Analytics |
| Device ID | analytics | Yes | No | Analytics, App Functionality |
| User ID | `Identity.SetPlayer` | Yes | No | Analytics, App Functionality |
| Crash Data | log capture | Yes | No | App Functionality |
| Other Diagnostic Data | log capture | Yes | No | App Functionality |

**Google Play, Data safety.** Collected, not shared, encrypted in transit, and deletable on
request.

| Category ▸ type | With | Purpose |
|---|---|---|
| App activity ▸ App interactions | analytics | Analytics |
| App info and performance ▸ Crash logs | log capture | App functionality, Analytics |
| App info and performance ▸ Diagnostics | log capture | App functionality, Analytics |
| Device or other IDs | analytics | Analytics, App functionality |

"Not shared" holds because PlayerSense processes the data on your behalf, as a service
provider; Google Play's definition of sharing excludes that.

**Kids.** For the Designed for Families program or the App Store Kids category, set
`ChildDirected`. What is sent is then what COPPA allows without parental consent for the support
of internal operations.

## Did it work?

**Window ▸ Analysis ▸ PlayerSense**, in play mode. A correct SDK is silent by design, so the
counters are the evidence: events tracked, batches spooled, batches delivered, rows the server
accepted. Three of them should stay at zero, and the window colours them when they do not —
events refused locally and events dropped by the server are both integration bugs, and events
evicted means a device was offline long enough to lose the oldest data.

## Platforms

| | |
|---|---|
| iOS | Static library, linked into the app binary |
| Android | arm64-v8a, armeabi-v7a, x86_64 |
| macOS | Universal, Editor and player |
| Windows, Linux | x86_64 |
| WebGL | Not yet — needs an Emscripten build against Unity's own toolchain |

The runtime is one Rust core shared by every binding, so the protocol behaves identically on
all of them rather than being reimplemented five times.

Building the libraries and where they go: [Runtime/Plugins/README.md](Runtime/Plugins/README.md).
You never configure their import settings — the package sets them from the folder on load,
because Unity's own guess is wrong on any machine that lacks the matching platform module.

## Dependency injection

The static API is a convenience over one shared object, not a separate implementation. If you
would rather inject it:

```csharp
container.RegisterInstance(PlayerSense.SDK.Client);   // PlayerSenseClient
...
_client.Analytics.Track("level_completed");
```

## Tests

```bash
cd .. && ./build-native.sh && cd unity && ./install-plugins.sh && ./verify.sh
./verify.sh --players     # also build a real Android APK and iOS Xcode project
./verify.sh --device      # run the tests on an attached Android phone, under IL2CPP
```

`verify.sh` builds a throwaway Unity project around the package, compiles it and runs the
play-mode tests. It is the only place the C# is checked at all — `cargo test` proves the
protocol and not one line of the binding.

`--players` is slow and it is not optional in spirit. The platform code is guarded
`#if UNITY_ANDROID && !UNITY_EDITOR`, and `UNITY_EDITOR` is defined in the editor whatever the
active build target is, so **no editor-side compile reaches it** — switching build target does
not help. Only a real player build compiles those branches, and it also unzips the APK to
prove each native library landed under the right ABI.

`--device` goes one further and runs the tests on an attached phone, which is where
`ps_abi_selfcheck` meets the Android arm64 ABI, `[MonoPInvokeCallback]` stops being a no-op,
and `getNoBackupFilesDir()` actually executes instead of being compiled and skipped.

`Tests/Runtime` deliberately does not re-test the protocol: that is covered once, in Rust, by
28 conformance scenarios against a fake host. What is tested here is the seam only Unity can
break — that the C# `[StructLayout]` declarations match the native library, that the host's
delegates survive a garbage collection, that the installation identifier lives where the
platform will not back it up, and that every call is safe before the SDK has started.

The first of those is the one that earns its keep. Add a field to `Native.PsConfig` and it
fails with *"PsConfig: the binding says 272 and this binary says 264"*, and the SDK refuses to
start — which is the difference between one log line and a heap corruption nobody can
reproduce.

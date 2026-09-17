// Starting, stopping, and the one client everything else talks to.
//
// The game-facing verbs are one file per module — Analytics/Analytics.cs, Context/Context.cs
// and so on, each beside the module it forwards to. This file is the lifecycle underneath
// them: it is what makes PlayerSense.SDK.Client exist for those facades to find.
//
// Why `PlayerSense.SDK.Initialize` and not `PlayerSense.Initialize`: C# resolves a namespace
// before a type of the same name (§7.8), so a class named PlayerSense inside namespace
// PlayerSense is unreachable as `PlayerSense.Something` from a game's own code — it would
// compile here and fail in every project that used it. Making Analytics, Identity, Privacy
// and Context *types in the namespace* is what makes the specified call sites work verbatim
// with no `using` directive at all; the cost is that the lifecycle verbs need a type to live
// on, and that type is SDK.

using System;
using System.Runtime.InteropServices;
using UnityEngine;
using PlayerSense.Core;

namespace PlayerSense
{
    /// <summary>Starting and stopping PlayerSense.</summary>
    /// <remarks>
    /// Most games never call anything here: fill in Edit ▸ Project Settings ▸ PlayerSense and
    /// the SDK starts itself before the first scene loads.
    /// </remarks>
    public static class SDK
    {
        private static PlayerSenseClient s_client;
        private static PlayerSenseRunner s_runner;
        private static bool s_abiChecked;
        private static bool s_warned;

        /// <summary>The client the static API drives, or null before <see cref="Initialize(string)"/>.</summary>
        /// <remarks>
        /// Exposed so a studio that prefers dependency injection can register this one instance
        /// and inject it, rather than reaching through statics. The static API is a convenience
        /// over this object, not a second implementation.
        /// </remarks>
        public static PlayerSenseClient Client => s_client;

        /// <summary>Whether the SDK started successfully.</summary>
        public static bool IsInitialized => s_client != null;

        /// <summary>What the SDK has done, for a diagnostics screen. Zeroes when it never started.</summary>
        public static PlayerSenseStats Stats => s_client?.Stats ?? default;

        /// <summary>Starts the SDK with a project key and the defaults.</summary>
        public static void Initialize(string projectKey) =>
            Initialize(new PlayerSenseOptions { ProjectKey = projectKey });

        /// <summary>Starts the SDK.</summary>
        /// <remarks>
        /// <para>
        /// <b>Performs no network operation.</b> It opens a file and returns, so it cannot fail
        /// because a server is unreachable, cannot delay your launch, and is safe on the first
        /// frame. Call it as early as you like.
        /// </para>
        /// <para>Calling it twice does nothing the second time.</para>
        /// </remarks>
        public static void Initialize(PlayerSenseOptions options)
        {
            if (s_client != null) return;

            if (options == null || string.IsNullOrEmpty(options.ProjectKey))
            {
                Debug.LogError("[PlayerSense] Initialize needs a project key. The SDK is disabled.");
                return;
            }
            if (!CheckAbiOnce()) return;

            try
            {
                s_client = Build(options);
                if (s_client == null) return;

                s_runner = PlayerSenseRunner.Create(s_client, options.TickIntervalSeconds);
                s_client.Host.Runner = s_runner;

                // The core opens a session on the first event, not here, so this only tells it
                // the app is in front. A game that initializes while backgrounded — a push
                // handler, a headless server build — is correct either way.
                s_client.Foreground();
            }
            catch (DllNotFoundException e)
            {
                Debug.LogError(MissingLibrary(e.Message));
                s_client = null;
            }
            catch (Exception e)
            {
                Debug.LogError($"[PlayerSense] could not start: {e}");
                s_client = null;
            }
        }

        /// <summary>Seals whatever is queued and tries to send it. Returns immediately.</summary>
        /// <remarks>
        /// You should not need this. The SDK already flushes on a timer, when a batch fills, on
        /// revenue, and when the app goes to the background. It is here for the moment before a
        /// deliberate sign-out or a level transition you want clean in the data.
        /// </remarks>
        public static void Flush() => s_client?.Flush();

        /// <summary>Stops the SDK, with one last bounded attempt to deliver what is queued.</summary>
        /// <remarks>
        /// Called for you when the app quits. Anything that does not go out in the attempt stays
        /// on disk and goes on the next launch, so nothing is lost by shutting down.
        /// </remarks>
        public static void Shutdown()
        {
            var client = s_client;
            var runner = s_runner;
            s_client = null;
            s_runner = null;

            client?.Dispose();

            // After Dispose, so the coroutine that drives requests is still alive for the
            // shutdown attempt the core makes on its way out.
            if (runner != null) UnityEngine.Object.Destroy(runner.gameObject);
        }

        /// <summary>Starts the SDK from the project settings, before the first scene loads.</summary>
        /// <remarks>
        /// So that most games need no integration code at all. This is only safe because
        /// Initialize is local and synchronous — nothing here can block on a network or fail
        /// because a server is down.
        /// </remarks>
        [RuntimeInitializeOnLoadMethod(RuntimeInitializeLoadType.BeforeSceneLoad)]
        private static void Bootstrap()
        {
            var settings = PlayerSenseSettings.Load();
            if (settings == null || !settings.autoInitialize) return;

            if (string.IsNullOrEmpty(settings.projectKey))
            {
                Debug.LogWarning(
                    "[PlayerSense] no project key in Project Settings, so nothing was started. " +
                    "Edit ▸ Project Settings ▸ PlayerSense.");
                return;
            }
            Initialize(PlayerSenseOptions.From(settings));
        }

        /// <summary>One warning, the first time a game uses the API before it is running.</summary>
        internal static void WarnNotInitialized(string what)
        {
            if (s_warned) return;
            s_warned = true;

            Debug.LogWarning(
                $"[PlayerSense] {what} did nothing: the SDK has not started. Set your project " +
                "key in Edit ▸ Project Settings ▸ PlayerSense, or call " +
                "PlayerSense.SDK.Initialize(key) yourself. (Only reported once.)");
        }

        /// <summary>
        /// Confirms this assembly and the native library agree about every struct layout.
        /// </summary>
        /// <remarks>
        /// First, and fatal. Every struct crossing the boundary is declared twice — once in
        /// Rust and once in C# — and nothing compiles both, so a field added on one side and
        /// not the other writes past the end of a struct and corrupts the heap with no
        /// diagnostic anywhere. This call turns that into one log line and a disabled SDK.
        /// </remarks>
        private static bool CheckAbiOnce()
        {
            if (s_abiChecked) return true;

            try
            {
                if (!Native.CheckAbi(out string problem))
                {
                    Debug.LogError(
                        "[PlayerSense] this package and the native library disagree about their " +
                        $"memory layout, so the SDK is disabled: {problem}");
                    return false;
                }
            }
            catch (DllNotFoundException e)
            {
                Debug.LogError(MissingLibrary(e.Message));
                return false;
            }
            catch (EntryPointNotFoundException)
            {
                // An older native library that predates the self-check. Same conclusion.
                Debug.LogError(
                    "[PlayerSense] the native library is older than this package and is missing " +
                    "ps_abi_selfcheck. Update Runtime/Plugins. The SDK is disabled.");
                return false;
            }

            s_abiChecked = true;
            return true;
        }

        private static string MissingLibrary(string detail) =>
            "[PlayerSense] the native library was not found, so the SDK is disabled. Check that " +
            $"the plugin for this platform is present under Runtime/Plugins. ({detail})";

        private static unsafe PlayerSenseClient Build(PlayerSenseOptions options)
        {
            var host = new UnityHost(UnityHost.StorageRoot(), options.DebugLogging);
            var vtable = host.Vtable();

            Native.ps_config_default(out var config);

            // Every string has to stay put for the duration of the call, so they all go into
            // one buffer that is pinned once, rather than nine separate pinned allocations.
            var scratch = new Utf8Scratch();
            var key = scratch.Write(options.ProjectKey);
            var endpoint = scratch.Write(options.Endpoint);
            var environment = scratch.Write(options.Environment);
            var sdkName = scratch.Write("unity");
            var sdkVersion = scratch.Write(PackageVersion);
            var appVersion = scratch.Write(Application.version);
            var platform = scratch.Write(PlatformName());
            var osVersion = scratch.Write(SystemInfo.operatingSystem);
            var locale = scratch.Write(LocaleTag());

            ulong handle;
            int status;

            fixed (byte* origin = scratch.Buffer)
            {
                config.StructSize = (uint)Marshal.SizeOf<Native.PsConfig>();
                config.ApiKey = key.Resolve(origin);
                config.Endpoint = endpoint.Resolve(origin);
                config.Environment = environment.Resolve(origin);
                config.SdkName = sdkName.Resolve(origin);
                config.SdkVersion = sdkVersion.Resolve(origin);
                config.AppVersion = appVersion.Resolve(origin);
                config.Platform = platform.Resolve(origin);
                config.OsVersion = osVersion.Resolve(origin);
                config.Locale = locale.Resolve(origin);

                config.FlushIntervalMs = options.FlushIntervalMs;
                config.SessionTimeoutMs = options.SessionTimeoutMs;
                config.Compression = options.Compression ? 1 : 0;
                config.Debug = options.DebugLogging ? 1 : 0;

                status = Native.ps_client_create(ref vtable, ref config, out handle);
            }

            if (status != Native.Ok)
            {
                Debug.LogError(
                    $"[PlayerSense] could not start: {Native.LastError() ?? Native.Describe(status)}");
                host.Dispose();
                return null;
            }

            var client = new PlayerSenseClient(handle, host);
            host.Client = client;
            return client;
        }

        /// <summary>Kept in step with package.json by hand; there are two of them and both ship.</summary>
        internal const string PackageVersion = "0.1.0";

        /// <summary>
        /// The platform name, from the closed set the gateway accepts.
        /// </summary>
        /// <remarks>
        /// Deliberately coarse. `platform` is a dimension on every chart, so it is a handful of
        /// values a studio can group by, not the forty-odd RuntimePlatform members — the
        /// difference between an editor and a standalone player belongs in `environment`.
        /// </remarks>
        private static string PlatformName()
        {
            switch (Application.platform)
            {
                case RuntimePlatform.IPhonePlayer:
                    return "ios";
                case RuntimePlatform.Android:
                    return "android";
                case RuntimePlatform.WindowsPlayer:
                case RuntimePlatform.WindowsEditor:
                    return "windows";
                case RuntimePlatform.OSXPlayer:
                case RuntimePlatform.OSXEditor:
                    return "macos";
                case RuntimePlatform.LinuxPlayer:
                case RuntimePlatform.LinuxEditor:
                    return "linux";
                case RuntimePlatform.WebGLPlayer:
                    return "web";
                case RuntimePlatform.PS4:
                case RuntimePlatform.PS5:
                case RuntimePlatform.XboxOne:
                case RuntimePlatform.Switch:
                    return "console";
                default:
                    return "unknown";
            }
        }

        /// <summary>
        /// A BCP-47-ish locale tag.
        /// </summary>
        /// <remarks>
        /// <c>CultureInfo.CurrentCulture</c> rather than <c>Application.systemLanguage</c>,
        /// which is an enum of language names with no region — "Turkish" cannot tell TR from
        /// the diaspora, and region is half of what a locale is for. The enum is the fallback
        /// for the stripped IL2CPP profiles where the culture comes back invariant.
        /// </remarks>
        private static string LocaleTag()
        {
            try
            {
                string tag = System.Globalization.CultureInfo.CurrentCulture.Name;
                if (!string.IsNullOrEmpty(tag)) return tag;
            }
            catch (Exception)
            {
                // A culture that will not load is not a reason to fail initialization.
            }
            return Application.systemLanguage.ToString();
        }
    }
}

// What a game configures. Everything has a default that the protocol document justifies, so a
// typical integration sets one field.

using UnityEngine;

namespace PlayerSense
{
    /// <summary>Settings for <see cref="SDK.Initialize(PlayerSenseOptions)"/>.</summary>
    public sealed class PlayerSenseOptions
    {
        /// <summary>Your publishable project key: <c>ps_project_live_…</c>.</summary>
        public string ProjectKey;

        /// <summary>The cell endpoint. A 421 replaces it and the replacement is remembered.</summary>
        public string Endpoint = "https://ingest.eu-1.playersense.dev";

        /// <summary>
        /// Where feature flags and LiveOps events come from.
        /// </summary>
        /// <remarks>
        /// A second address rather than a path on the first, because they are different
        /// services: one takes every event your game produces and the other answers one
        /// question a session.
        ///
        /// Leave it empty if you only use analytics. The SDK then never asks, every flag
        /// answers with the default you passed, and no LiveOps event is ever running — which
        /// is the right behaviour for a studio that has not set any up.
        /// </remarks>
        public string ConfigEndpoint = "";

        /// <summary>development, staging or production. The key is the authority; this is a hint.</summary>
        public string Environment = "production";

        /// <summary>How long ordinary events wait before a batch is sent. Revenue and session starts do not wait.</summary>
        public int FlushIntervalMs = 30_000;

        /// <summary>How long in the background before the next event starts a new session.</summary>
        public int SessionTimeoutMs = 30_000;

        /// <summary>How often flags and LiveOps events are asked for again.</summary>
        /// <remarks>
        /// Fifteen minutes, and a floor rather than a schedule: returning to the foreground
        /// asks immediately, because a game resumed after three hours is where the answer is
        /// most likely to have moved. Polling harder buys a finer number nobody can act on,
        /// on every device, forever.
        /// </remarks>
        public int ConfigRefreshIntervalMs = 15 * 60 * 1000;

        /// <summary>Logs every dropped event and every refusal, rather than only the serious ones.</summary>
        public bool DebugLogging;

        /// <summary>gzip the body. Leave on: it is roughly a twentyfold reduction.</summary>
        public bool Compression = true;

        /// <summary>How often the SDK is given a chance to do background work.</summary>
        public float TickIntervalSeconds = 0.25f;

        internal static PlayerSenseOptions From(PlayerSenseSettings settings) => new PlayerSenseOptions
        {
            ProjectKey = settings.projectKey,
            Endpoint = string.IsNullOrEmpty(settings.endpoint)
                ? "https://ingest.eu-1.playersense.dev"
                : settings.endpoint,
            ConfigEndpoint = settings.configEndpoint ?? "",
            Environment = settings.environment,
            DebugLogging = settings.debugLogging,
        };
    }

    /// <summary>
    /// The project settings asset, so a game does not need initialization code at all.
    /// </summary>
    /// <remarks>
    /// Loaded from <c>Resources</c> at startup. The editor writes it; nothing at runtime does.
    /// </remarks>
    public sealed class PlayerSenseSettings : ScriptableObject
    {
        internal const string ResourcePath = "PlayerSenseSettings";

        public string projectKey;
        public string endpoint = "https://ingest.eu-1.playersense.dev";

        /// <summary>Where flags and LiveOps events come from. Leave empty for analytics only.</summary>
        public string configEndpoint = "";

        public string environment = "production";

        [Tooltip("Start the SDK before the first scene loads, using the key above.")]
        public bool autoInitialize = true;

        [Tooltip("Log every dropped event and every refusal, not only the serious ones.")]
        public bool debugLogging;

        internal static PlayerSenseSettings Load() => Resources.Load<PlayerSenseSettings>(ResourcePath);
    }
}

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

        /// <summary>development, staging or production. The key is the authority; this is a hint.</summary>
        public string Environment = "production";

        /// <summary>How long ordinary events wait before a batch is sent. Revenue and session starts do not wait.</summary>
        public int FlushIntervalMs = 30_000;

        /// <summary>How long in the background before the next event starts a new session.</summary>
        public int SessionTimeoutMs = 30_000;

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
        public string environment = "production";

        [Tooltip("Start the SDK before the first scene loads, using the key above.")]
        public bool autoInitialize = true;

        [Tooltip("Log every dropped event and every refusal, not only the serious ones.")]
        public bool debugLogging;

        internal static PlayerSenseSettings Load() => Resources.Load<PlayerSenseSettings>(ResourcePath);
    }
}

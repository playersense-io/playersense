// What a game configures about logs, exceptions and crashes.
using System;

namespace PlayerSense
{
    /// <summary>Settings for log, exception and crash capture: <see cref="PlayerSenseOptions.Logs"/>.</summary>
    /// <remarks>
    /// <para>
    /// <b>Off until you turn it on.</b> Sending crash data changes what your game has to declare
    /// on the App Store (App Privacy: Crash Data, Other Diagnostic Data) and on Google Play
    /// (Data safety: Crash logs, Diagnostics). An SDK update must never change those answers
    /// behind your back, so capture starts only when you set <see cref="Enabled"/>.
    /// </para>
    /// <para>
    /// Everything else defaults to what a store reviewer and a privacy regulator would expect:
    /// nothing leaves the device before the player answers the consent question, personal data
    /// is scrubbed on the device, and no device name or hardware identifier is ever read.
    /// </para>
    /// </remarks>
    public sealed class LogsOptions
    {
        /// <summary>Capture logs, exceptions and crashes.</summary>
        public bool Enabled;

        /// <summary>The least serious record that is sent. Below it, records are kept as breadcrumbs.</summary>
        /// <remarks>
        /// Warning by default. An info line is rarely worth a network request of its own, but
        /// the last fifty of them are exactly what explains the error that follows, which is
        /// what breadcrumbs are.
        /// </remarks>
        public LogLevel MinLevel = LogLevel.Warning;

        /// <summary>The least serious record kept as a breadcrumb. Below it, nothing is kept.</summary>
        public LogLevel BreadcrumbLevel = LogLevel.Info;

        /// <summary>The most records one device sends a minute. Crashes are never counted against it.</summary>
        /// <remarks>
        /// An error thrown every frame is folded into one record with a count before this
        /// applies, so this only bites when a device produces many different errors at once,
        /// which is a device worth hearing from a little of, not all of.
        /// </remarks>
        public int MaxPerMinute = 120;

        /// <summary>How long records wait before they are sent. Crashes do not wait.</summary>
        public int FlushIntervalMs = 15_000;

        /// <summary>
        /// Removes emails, access tokens, passwords, card numbers, IP addresses and the account
        /// name in file paths from every message before it leaves the device.
        /// </summary>
        /// <remarks>Leave on. The server scrubs again, but data it never receives is data it cannot leak.</remarks>
        public bool ScrubPersonalData = true;

        /// <summary>Record Unity's own log: <c>Debug.LogError</c>, <c>Debug.LogException</c> and what is below them.</summary>
        public bool CaptureUnityLog = true;

        /// <summary>Record exceptions nothing caught, on any thread, and task exceptions nothing observed.</summary>
        public bool CaptureUnhandledExceptions = true;

        /// <summary>
        /// Record crashes the platform itself reports: Android's exit reasons (Android 11 and
        /// later, including ANRs and native crashes) and iOS MetricKit (iOS 14 and later).
        /// </summary>
        /// <remarks>
        /// Read on the next launch, from the operating system, rather than by installing a
        /// signal handler. A signal handler would fight Unity's own (Mono turns a null pointer
        /// into a NullReferenceException through SIGSEGV) and any other crash reporter the game
        /// ships with, which is how crash reporters crash games.
        /// </remarks>
        public bool ReportPlatformCrashes = true;

        /// <summary>
        /// Record a run that ended in the foreground without the app going to the background or
        /// quitting: the only trace a crash leaves where the platform keeps none.
        /// </summary>
        /// <remarks>
        /// Used where platform reports are not available: desktop, and phones older than the
        /// versions above. Where they are, they are the more precise account of the same event
        /// and this is switched off, so nothing is counted twice.
        /// </remarks>
        public bool ReportAbnormalExits = true;

        /// <summary>
        /// Keeps a crash on the device while the player has not yet answered the consent
        /// question, and sends it if they agree. A "no" deletes it.
        /// </summary>
        /// <remarks>
        /// Games commonly crash before the consent screen has been shown; without this, the
        /// crashes that matter most would be the ones nobody ever sees. Everything else waits in
        /// memory only.
        /// </remarks>
        public bool HoldCrashesUntilConsent = true;

        /// <summary>Capture in the Editor as well. Off, so your console never fills your dashboard.</summary>
        public bool CaptureInEditor;

        /// <summary>
        /// Namespaces that are not your game's own code, besides Unity's, .NET's and common
        /// libraries. Their frames are shown but do not decide how errors group into issues.
        /// </summary>
        public string[] NotInAppPrefixes = Array.Empty<string>();

        /// <summary>
        /// Sees every record just before it is sent, on the SDK's own thread. Change it, or
        /// return false to drop it.
        /// </summary>
        /// <remarks>
        /// For what only your game knows is personal, such as a player's display name in a
        /// message. It runs off the main thread so that it is never in your frame, which means
        /// Unity's own APIs are not available in it; plain string work is. Do not keep a
        /// reference to the entry: it is reused. Not called for crashes read back from a
        /// previous run.
        /// </remarks>
        public Func<LogEntry, bool> BeforeSend;
    }
}

// One client: the handle, the host that feeds it, and the module facades.
//
// Everything here is a thin translation. No decision about sessions, consent, batching,
// retries or spooling is made in C# — those live in the shared core, once, which is the whole
// reason this package is small.

using System;
using System.Collections.Generic;
using PlayerSense.Core;

namespace PlayerSense
{
    /// <summary>A PlayerSense client. Most games use the static API — <see cref="Analytics"/>, <see cref="Identity"/> — instead.</summary>
    public sealed class PlayerSenseClient : IDisposable
    {
        private readonly ulong _handle;
        private readonly UnityHost _host;
        private readonly Utf8Scratch _scratch = new Utf8Scratch();
        private bool _disposed;

        public AnalyticsModule Analytics { get; }
        public IdentityModule Identity { get; }
        public PrivacyModule Privacy { get; }
        public ContextModule Context { get; }
        public FlagsModule Flags { get; }
        public LiveOpsModule LiveOps { get; }
        public LogsModule Logs { get; }

        internal ulong Handle => _handle;
        internal Utf8Scratch Scratch => _scratch;
        internal UnityHost Host => _host;

        internal PlayerSenseClient(ulong handle, UnityHost host, LogsOptions logs = null)
        {
            _handle = handle;
            _host = host;

            Analytics = new AnalyticsModule(this);
            Identity = new IdentityModule(this);
            Privacy = new PrivacyModule(this);
            Context = new ContextModule(this);
            Flags = new FlagsModule(this);
            LiveOps = new LiveOpsModule(this);
            Logs = new LogsModule(this, logs);

            // The last document this installation fetched, so a launch with no network answers
            // from it rather than from every fallback (§42). Flags only in effect: no clock
            // anchor comes off disk, so nothing is reported running until the server answers.
            Native.ps_client_restore_config(_handle);
        }

        /// <summary>The installation identifier this device is known by.</summary>
        public unsafe string InstallId
        {
            get
            {
                byte* probe = null;
                long needed;
                Native.ps_client_install_id(_handle, probe, 0, out needed);
                if (needed <= 0) return null;

                var bytes = new byte[needed];
                fixed (byte* p = bytes)
                {
                    if (Native.ps_client_install_id(_handle, p, needed, out needed) != Native.Ok)
                        return null;
                }
                return System.Text.Encoding.UTF8.GetString(bytes, 0, (int)needed);
            }
        }

        /// <summary>What the SDK has done, for a diagnostics screen.</summary>
        public PlayerSenseStats Stats
        {
            get
            {
                var raw = new Native.PsStats
                {
                    StructSize = (uint)System.Runtime.InteropServices.Marshal.SizeOf<Native.PsStats>(),
                };
                if (Native.ps_client_stats(_handle, ref raw) != Native.Ok) return default;
                return PlayerSenseStats.From(raw);
            }
        }

        /// <summary>Seals whatever is queued and tries to send. Returns immediately.</summary>
        public void Flush() => Check(Native.ps_client_flush(_handle), nameof(Flush));

        internal void Foreground() => Check(Native.ps_client_foreground(_handle), nameof(Foreground));
        internal void Background() => Check(Native.ps_client_background(_handle), nameof(Background));
        internal void Tick()
        {
            _host.StartDeferred();
            // Captured records first, so the tick that follows can already batch them.
            Logs.Drain();
            Native.ps_client_tick(_handle);
        }

        /// <summary>One last bounded attempt, then the client is retired.</summary>
        public void Dispose()
        {
            if (_disposed) return;
            _disposed = true;

            // What was captured since the last tick goes into the shutdown attempt, and nothing
            // captured after this point reaches a client that is gone.
            Logs.Drain();
            Logs.Detach();

            Native.ps_client_shutdown(_handle);
            Native.ps_client_destroy(_handle);
            _host.Dispose();
        }

        /// <summary>
        /// Logs a status that is not OK, and swallows it.
        /// </summary>
        /// <remarks>
        /// Nothing in this package throws. A game that wrapped every Track in a try/catch
        /// because analytics might fail would be a game whose analytics had become a
        /// dependency, which is precisely what the protocol forbids.
        /// </remarks>
        internal static void Check(int status, string what)
        {
            // `>= 0`, not `== Ok`. A player who declines analytics makes every Track return
            // PS_DECLINED_BY_CONSENT, and an equality check here would put a warning in their
            // console for every event of their session — for the SDK obeying them.
            if (status >= Native.Ok) return;

            string detail = Native.LastError() ?? Native.Describe(status);
            UnityEngine.Debug.LogWarning($"[PlayerSense] {what}: {detail}");
        }
    }

    /// <summary>What the SDK has done since it started.</summary>
    public readonly struct PlayerSenseStats
    {
        public readonly ulong EventsTracked;
        /// <summary>Refused locally — a bad name, too many properties. Non-zero is an integration bug.</summary>
        public readonly ulong EventsRejected;
        /// <summary>
        /// Discarded because the player declined. Not a fault: the SDK obeying them.
        /// </summary>
        public readonly ulong EventsWithoutConsent;
        /// <summary>Lost to a full or ageing spool: the device was offline for a long time.</summary>
        public readonly ulong EventsEvicted;
        public readonly ulong BatchesSpooled;
        public readonly ulong BatchesDelivered;
        public readonly ulong BatchesDiscarded;
        public readonly ulong RequestsFailed;
        public readonly ulong EventsAcceptedByServer;
        /// <summary>Events the gateway refused. Non-zero is an integration bug worth chasing.</summary>
        public readonly ulong EventsDroppedByServer;

        private PlayerSenseStats(Native.PsStats s)
        {
            EventsTracked = s.EventsTracked;
            EventsRejected = s.EventsRejected;
            EventsWithoutConsent = s.EventsWithoutConsent;
            EventsEvicted = s.EventsEvicted;
            BatchesSpooled = s.BatchesSpooled;
            BatchesDelivered = s.BatchesDelivered;
            BatchesDiscarded = s.BatchesDiscarded;
            RequestsFailed = s.RequestsFailed;
            EventsAcceptedByServer = s.EventsAcceptedByServer;
            EventsDroppedByServer = s.EventsDroppedByServer;
        }

        internal static PlayerSenseStats From(Native.PsStats s) => new PlayerSenseStats(s);
    }
}

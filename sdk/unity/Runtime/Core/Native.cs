// The P/Invoke surface. Nothing above this file knows the native library exists.
//
// Every declaration here mirrors sdk/ffi/include/playersense.h, and the layouts are checked
// at runtime by PsAbiSizes rather than trusted: a [StructLayout] that drifts from the header
// corrupts the heap silently, and there is no compiler on either side that would notice.

using System;
using System.Runtime.InteropServices;

namespace PlayerSense.Core
{
    internal static class Native
    {
        // On iOS the core is a static library linked into the app binary, so the symbols are
        // already there and P/Invoke must not look for a shared object. Everywhere else it is
        // a real dynamic library. Getting this wrong is a DllNotFoundException on device and
        // nothing in the editor, which is the worst possible place to discover it.
#if UNITY_IOS && !UNITY_EDITOR
        internal const string Library = "__Internal";
#else
        internal const string Library = "playersense";
#endif

        // ---- status codes, from playersense.h -------------------------------

        // Check the SIGN, never equality with Ok. Negative is failure, zero is success, and
        // positive is "it worked, and there is something to say" — a range the ABI reserved
        // for exactly this so that adding an outcome cannot turn into an error in a binding
        // that was written before it existed.
        internal const int Ok = 0;
        internal const int HeldForConsent = 1;
        internal const int DeclinedByConsent = 2;

        internal const int ErrBadHandle = -1;
        internal const int ErrBadHost = -2;
        internal const int ErrInvalidArgument = -3;
        internal const int ErrBufferTooSmall = -4;
        internal const int ErrReentrant = -5;
        internal const int ErrPoisoned = -6;
        internal const int ErrAbiMismatch = -7;

        internal const int RejectedEventName = -20;
        internal const int RejectedTooManyProperties = -21;
        internal const int RejectedContextKey = -40;
        internal const int RejectedContextReserved = -41;
        internal const int RejectedContextFull = -42;

        internal const int ValueStr = 0;
        internal const int ValueInt = 1;
        internal const int ValueFloat = 2;
        internal const int ValueBool = 3;

        internal const int ScopeSession = 0;
        internal const int ScopePlayer = 1;

        internal const int LevelDebug = 0;
        internal const int LevelInfo = 1;
        internal const int LevelWarn = 2;
        internal const int LevelError = 3;

        internal const uint AbiVersion = 1;

        // ---- layouts --------------------------------------------------------
        //
        // Pack = 8 on every struct, matching the 8-byte alignment the Rust side pads to. The
        // default packing would agree on this platform and not on the 32-bit Android ABI,
        // where int64_t is 4-aligned.

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal unsafe struct PsStr
        {
            public byte* Ptr;
            public long Len;

            /// <summary>A borrowed empty string, which also means "absent".</summary>
            public static PsStr Absent => new PsStr { Ptr = null, Len = 0 };
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal struct PsValue
        {
            public int Kind;
            public int Bool;
            public long Int;
            public double Float;
            public PsStr Str;
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal struct PsProperty
        {
            public PsStr Key;
            public PsValue Value;
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal struct PsStats
        {
            public uint StructSize;
            public uint Pad;
            public ulong EventsTracked;
            public ulong EventsRejected;
            public ulong EventsWithoutConsent;
            public ulong EventsEvicted;
            public ulong BatchesSpooled;
            public ulong BatchesDelivered;
            public ulong BatchesDiscarded;
            public ulong RequestsFailed;
            public ulong EventsAcceptedByServer;
            public ulong EventsDroppedByServer;
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal struct PsConfig
        {
            public uint StructSize;
            public uint Pad;

            public PsStr ApiKey;
            public PsStr Endpoint;
            public PsStr Environment;
            public PsStr SdkName;
            public PsStr SdkVersion;
            public PsStr AppVersion;
            public PsStr AppBuild;
            public PsStr Platform;
            public PsStr OsVersion;
            public PsStr Locale;

            public long FlushIntervalMs;
            public long MaxEventsPerBatch;
            public long MaxBatchBytes;
            public long SessionTimeoutMs;
            public long SpoolMaxAgeMs;
            public long SpoolMaxBytes;
            public long SpoolMaxBatches;
            public long PreconsentMaxEvents;
            public long BackoffBaseMs;
            public long BackoffCapMs;

            public int ColdAfterFailures;
            public int Compression;
            public int Debug;
            public int Pad2;
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal unsafe struct PsRequest
        {
            public ulong Id;
            public PsStr Url;
            public PsStr ApiKey;
            public PsStr ContentEncoding;
            public byte* Body;
            public long BodyLen;
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal unsafe struct PsHost
        {
            public uint StructSize;
            public uint Pad;
            public IntPtr UserData;

            public IntPtr NowMs;
            public IntPtr MonotonicMs;
            public IntPtr FillRandom;
            public IntPtr StorePut;
            public IntPtr StoreGet;
            public IntPtr StoreDelete;
            public IntPtr StoreKeys;
            public IntPtr Send;
            public IntPtr Log;
        }

        [StructLayout(LayoutKind.Sequential, Pack = 8)]
        internal struct PsAbiSizes
        {
            public uint AbiVersion;
            public uint SizeofHost;
            public uint SizeofConfig;
            public uint SizeofStats;
            public uint SizeofStr;
            public uint SizeofValue;
            public uint SizeofProperty;
            public uint SizeofRequest;
        }

        // ---- the entry points ------------------------------------------------

        [DllImport(Library)] internal static extern uint ps_abi_version();

        [DllImport(Library)] internal static extern int ps_abi_selfcheck(ref PsAbiSizes sizes);

        [DllImport(Library)]
        internal static extern unsafe int ps_last_error_message(byte* buffer, long cap, out long len);

        [DllImport(Library)] internal static extern void ps_config_default(out PsConfig config);

        [DllImport(Library)]
        internal static extern int ps_client_create(ref PsHost host, ref PsConfig config, out ulong handle);

        [DllImport(Library)] internal static extern int ps_client_destroy(ulong client);
        [DllImport(Library)] internal static extern int ps_client_is_poisoned(ulong client, out int poisoned);

        [DllImport(Library)]
        internal static extern unsafe int ps_client_install_id(ulong client, byte* buffer, long cap, out long len);

        [DllImport(Library)] internal static extern int ps_client_stats(ulong client, ref PsStats stats);
        [DllImport(Library)] internal static extern int ps_client_context_revision(ulong client, out ulong revision);

        [DllImport(Library)] internal static extern int ps_client_set_player(ulong client, PsStr playerId);

        [DllImport(Library)]
        internal static extern int ps_client_set_consent_granted(
            ulong client, PsStr policyVersion, PsStr attStatus, int gdprApplies, long collectedAtMs);

        [DllImport(Library)] internal static extern int ps_client_set_consent_denied(ulong client);

        [DllImport(Library)]
        internal static extern int ps_client_set_context(ulong client, int scope, PsStr key, ref PsValue value);

        [DllImport(Library)]
        internal static extern int ps_client_remove_context(ulong client, int scope, PsStr key);

        [DllImport(Library)]
        internal static extern unsafe int ps_client_track(
            ulong client, PsStr name, PsProperty* props, long propCount);

        [DllImport(Library)]
        internal static extern unsafe int ps_client_track_revenue(
            ulong client, PsStr name, long revenueMicros, PsStr currency,
            PsProperty* props, long propCount);

        [DllImport(Library)] internal static extern int ps_client_flush(ulong client);
        [DllImport(Library)] internal static extern int ps_client_foreground(ulong client);
        [DllImport(Library)] internal static extern int ps_client_background(ulong client);
        [DllImport(Library)] internal static extern int ps_client_tick(ulong client);
        [DllImport(Library)] internal static extern int ps_client_shutdown(ulong client);

        [DllImport(Library)]
        internal static extern unsafe int ps_client_on_response(
            ulong client, ulong requestId, ushort status, long retryAfterMs, byte* body, long bodyLen);

        // ---- helpers ---------------------------------------------------------

        /// <summary>
        /// Confirms this assembly and the native library agree about every layout.
        /// Call before anything else: it is what turns a silently corrupt struct into a log line.
        /// </summary>
        internal static bool CheckAbi(out string problem)
        {
            var sizes = new PsAbiSizes
            {
                AbiVersion = AbiVersion,
                SizeofHost = (uint)Marshal.SizeOf<PsHost>(),
                SizeofConfig = (uint)Marshal.SizeOf<PsConfig>(),
                SizeofStats = (uint)Marshal.SizeOf<PsStats>(),
                SizeofStr = (uint)Marshal.SizeOf<PsStr>(),
                SizeofValue = (uint)Marshal.SizeOf<PsValue>(),
                SizeofProperty = (uint)Marshal.SizeOf<PsProperty>(),
                SizeofRequest = (uint)Marshal.SizeOf<PsRequest>(),
            };

            int code = ps_abi_selfcheck(ref sizes);
            if (code == Ok)
            {
                problem = null;
                return true;
            }
            problem = LastError() ?? $"ps_abi_selfcheck returned {code}";
            return false;
        }

        /// <summary>The message for the most recent failure, or null. Valid only immediately after one.</summary>
        internal static unsafe string LastError()
        {
            long needed;
            ps_last_error_message(null, 0, out needed);
            if (needed <= 0) return null;

            var buffer = new byte[needed];
            fixed (byte* p = buffer)
            {
                if (ps_last_error_message(p, needed, out needed) != Ok) return null;
            }
            return System.Text.Encoding.UTF8.GetString(buffer, 0, (int)needed);
        }

        /// <summary>A human-readable name for a status code, for a log line.</summary>
        internal static string Describe(int status)
        {
            switch (status)
            {
                case Ok: return "ok";
                case HeldForConsent: return "held in memory until the player answers";
                case DeclinedByConsent: return "discarded: the player declined analytics";
                case ErrBadHandle: return "the client handle is stale or was never valid";
                case ErrBadHost: return "the host is missing a required callback";
                case ErrInvalidArgument: return "an argument was malformed";
                case ErrBufferTooSmall: return "the buffer was too small";
                case ErrReentrant: return "a callback called back into the SDK";
                case ErrPoisoned: return "the SDK hit an internal error and has stopped";
                case ErrAbiMismatch: return "the native library and this package disagree about their layouts";
                case RejectedEventName: return "the event name is empty, too long, or has a character outside [a-zA-Z0-9_.:-]";
                case RejectedTooManyProperties: return "too many properties on one event";
                case RejectedContextKey: return "the context key is malformed";
                case RejectedContextReserved: return "that context namespace is reserved";
                case RejectedContextFull: return "that context scope is full";
                default: return $"status {status}";
            }
        }
    }
}

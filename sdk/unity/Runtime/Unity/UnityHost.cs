// The platform, as the core needs it: clock, storage, randomness, network, log.
//
// This is the only file in the package that makes decisions, and even here they are decisions
// about *how* to do something rather than *whether* — the core owns every protocol question.
//
// Three rules from playersense.h are load-bearing and easy to get wrong in C#:
//
//   1. Every callback is a STATIC method with [MonoPInvokeCallback]. IL2CPP requires it: an
//      instance method or a closure cannot be reached from native code. The instance is found
//      through the GCHandle in user_data.
//
//   2. Every delegate is ROOTED IN A STATIC FIELD. Under Mono, a delegate passed as a function
//      pointer is collectable the moment nothing references it, and the crash lands on the
//      second callback rather than the first.
//
//   3. Every callback body is wrapped in a CATCH-ALL. The callbacks are extern "C", so an
//      exception escaping one aborts the process — deliberately, because the alternative risks
//      memory corruption. An exception that reaches the boundary kills the player's game.

using System;
using System.Collections;
using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using UnityEngine;
using UnityEngine.Networking;
using AOT;
using PlayerSense.Core;

namespace PlayerSense
{
    internal sealed unsafe class UnityHost : IDisposable
    {
        // ---- delegate types, matching playersense.h ---------------------------

        private delegate long NowMsFn(IntPtr ud);
        private delegate void FillRandomFn(IntPtr ud, byte* buffer, long len);
        private delegate int StorePutFn(IntPtr ud, Native.PsStr key, byte* value, long len);
        private delegate void StoreGetFn(IntPtr ud, Native.PsStr key, IntPtr sink, IntPtr ctx);
        private delegate void StoreDeleteFn(IntPtr ud, Native.PsStr key);
        private delegate void StoreKeysFn(IntPtr ud, Native.PsStr prefix, IntPtr sink, IntPtr ctx);
        private delegate void SendFn(IntPtr ud, Native.PsRequest* request);
        private delegate void LogFn(IntPtr ud, int level, Native.PsStr message);

        // Rule 2: rooted for the life of the domain. Never make these locals.
        private static readonly NowMsFn s_nowMs = NowMs;
        private static readonly NowMsFn s_monotonicMs = MonotonicMs;
        private static readonly FillRandomFn s_fillRandom = FillRandom;
        private static readonly StorePutFn s_storePut = StorePut;
        private static readonly StoreGetFn s_storeGet = StoreGet;
        private static readonly StoreDeleteFn s_storeDelete = StoreDelete;
        private static readonly StoreKeysFn s_storeKeys = StoreKeys;
        private static readonly SendFn s_send = Send;
        private static readonly LogFn s_log = Log;

        // ---- instance state ---------------------------------------------------

        private readonly string _root;
        private readonly System.Diagnostics.Stopwatch _monotonic = System.Diagnostics.Stopwatch.StartNew();
        private readonly RandomNumberGenerator _random = RandomNumberGenerator.Create();
        private GCHandle _self;
        private bool _debug;

        /// <summary>Set once the client exists, so a callback can answer it.</summary>
        internal PlayerSenseClient Client { get; set; }

        /// <summary>Runs the web requests. Set by the lifecycle driver.</summary>
        internal MonoBehaviour Runner { get; set; }

        internal UnityHost(string storageRoot, bool debug)
        {
            _root = storageRoot;
            _debug = debug;
            Directory.CreateDirectory(_root);
            ExcludeFromBackup(_root);
            _self = GCHandle.Alloc(this);
        }

        /// <summary>Builds the vtable the core is handed.</summary>
        internal Native.PsHost Vtable() => new Native.PsHost
        {
            StructSize = (uint)Marshal.SizeOf<Native.PsHost>(),
            UserData = GCHandle.ToIntPtr(_self),
            NowMs = Marshal.GetFunctionPointerForDelegate(s_nowMs),
            MonotonicMs = Marshal.GetFunctionPointerForDelegate(s_monotonicMs),
            FillRandom = Marshal.GetFunctionPointerForDelegate(s_fillRandom),
            StorePut = Marshal.GetFunctionPointerForDelegate(s_storePut),
            StoreGet = Marshal.GetFunctionPointerForDelegate(s_storeGet),
            StoreDelete = Marshal.GetFunctionPointerForDelegate(s_storeDelete),
            StoreKeys = Marshal.GetFunctionPointerForDelegate(s_storeKeys),
            Send = Marshal.GetFunctionPointerForDelegate(s_send),
            Log = Marshal.GetFunctionPointerForDelegate(s_log),
        };

        public void Dispose()
        {
            if (_self.IsAllocated) _self.Free();
            _random?.Dispose();
        }

        private static UnityHost Resolve(IntPtr ud)
        {
            if (ud == IntPtr.Zero) return null;
            return GCHandle.FromIntPtr(ud).Target as UnityHost;
        }

        private static string Decode(Native.PsStr s)
        {
            if (s.Ptr == null || s.Len <= 0) return string.Empty;
            return System.Text.Encoding.UTF8.GetString(s.Ptr, (int)s.Len);
        }

        // ---- clock ------------------------------------------------------------

        [MonoPInvokeCallback(typeof(NowMsFn))]
        private static long NowMs(IntPtr ud)
        {
            try { return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(); }
            catch { return 0; }
        }

        [MonoPInvokeCallback(typeof(NowMsFn))]
        private static long MonotonicMs(IntPtr ud)
        {
            // A Stopwatch, not Time.realtimeSinceStartup: the core measures session expiry and
            // backoff against this, and Unity's clock does not advance while the app is
            // suspended — which would make a night in the background look like no time at all.
            try { return Resolve(ud)?._monotonic.ElapsedMilliseconds ?? 0; }
            catch { return 0; }
        }

        [MonoPInvokeCallback(typeof(FillRandomFn))]
        private static void FillRandom(IntPtr ud, byte* buffer, long len)
        {
            try
            {
                var host = Resolve(ud);
                if (host == null || buffer == null || len <= 0) return;
                var bytes = new byte[len];
                host._random.GetBytes(bytes);
                for (long i = 0; i < len; i++) buffer[i] = bytes[i];
            }
            catch { /* the core pre-seeded the buffer, so this degrades rather than fails */ }
        }

        // ---- storage ----------------------------------------------------------
        //
        // One file per key, under the directory StorageRoot picks. Keys are opaque and
        // filename-safe by contract, so no escaping is needed — but a key is still checked for
        // separators, because "the contract says so" is not a reason to write outside your own
        // directory.

        /// <summary>Where this device keeps the spool, the install id and the consent record.</summary>
        /// <remarks>
        /// <para>
        /// Android's answer to "do not back this up" is a directory, not a flag:
        /// <c>getNoBackupFilesDir()</c> is excluded from Auto Backup and from device-to-device
        /// transfer by the platform itself.
        /// </para>
        /// <para>
        /// Chosen over shipping an <c>android:fullBackupContent</c> rule in the package's
        /// manifest, which would have to merge with whatever backup configuration the game
        /// already has — and a manifest merge conflict in someone else's build is a support
        /// ticket, while a wrong directory is silent data corruption. This needs nothing from
        /// the game.
        /// </para>
        /// </remarks>
        internal static string StorageRoot()
        {
#if UNITY_ANDROID && !UNITY_EDITOR
            try
            {
                using (var player = new AndroidJavaClass("com.unity3d.player.UnityPlayer"))
                using (var activity = player.GetStatic<AndroidJavaObject>("currentActivity"))
                using (var dir = activity.Call<AndroidJavaObject>("getNoBackupFilesDir"))
                {
                    string path = dir.Call<string>("getAbsolutePath");
                    if (!string.IsNullOrEmpty(path)) return Path.Combine(path, "playersense");
                }
            }
            catch (Exception)
            {
                // No activity yet, or a stripped player. persistentDataPath still works; the
                // identifier is then merely backed up, which is a wrong number rather than a
                // crash — and a wrong number nobody can see is the worse outcome, so it says so.
                Debug.LogWarning(
                    "[PlayerSense] could not reach getNoBackupFilesDir(), so the installation " +
                    "identifier will be included in Android Auto Backup and may be restored " +
                    "onto a second device.");
            }
#endif
            return Path.Combine(Application.persistentDataPath, "playersense");
        }

        private string PathFor(string key)
        {
            if (string.IsNullOrEmpty(key) || key.IndexOfAny(s_separators) >= 0 || key.Contains(".."))
                return null;
            return Path.Combine(_root, key);
        }

        private static readonly char[] s_separators = { '/', '\\', ':' };

        [MonoPInvokeCallback(typeof(StorePutFn))]
        private static int StorePut(IntPtr ud, Native.PsStr key, byte* value, long len)
        {
            try
            {
                var host = Resolve(ud);
                string path = host?.PathFor(Decode(key));
                if (path == null) return 1;

                var bytes = new byte[len < 0 ? 0 : len];
                for (long i = 0; i < bytes.Length; i++) bytes[i] = value[i];

                // Written to a temporary file and moved, so a kill part way through leaves the
                // previous batch rather than a truncated one the core would later fail to read.
                string temp = path + ".tmp";
                File.WriteAllBytes(temp, bytes);
                if (File.Exists(path)) File.Delete(path);
                File.Move(temp, path);
                return 0;
            }
            catch { return 1; }
        }

        [MonoPInvokeCallback(typeof(StoreGetFn))]
        private static void StoreGet(IntPtr ud, Native.PsStr key, IntPtr sink, IntPtr ctx)
        {
            try
            {
                var host = Resolve(ud);
                string path = host?.PathFor(Decode(key));
                if (path == null || !File.Exists(path)) return;  // not calling the sink means absent

                var bytes = File.ReadAllBytes(path);
                var call = Marshal.GetDelegateForFunctionPointer<SinkFn>(sink);
                fixed (byte* p = bytes)
                {
                    call(ctx, bytes.Length == 0 ? null : p, bytes.Length);
                }
            }
            catch { /* unreadable is absent, and the core copes with absent */ }
        }

        [MonoPInvokeCallback(typeof(StoreDeleteFn))]
        private static void StoreDelete(IntPtr ud, Native.PsStr key)
        {
            try
            {
                string path = Resolve(ud)?.PathFor(Decode(key));
                if (path != null && File.Exists(path)) File.Delete(path);
            }
            catch { }
        }

        [MonoPInvokeCallback(typeof(StoreKeysFn))]
        private static void StoreKeys(IntPtr ud, Native.PsStr prefix, IntPtr sink, IntPtr ctx)
        {
            try
            {
                var host = Resolve(ud);
                if (host == null) return;

                string wanted = Decode(prefix);
                var call = Marshal.GetDelegateForFunctionPointer<SinkFn>(sink);

                foreach (string file in Directory.GetFiles(host._root))
                {
                    string name = Path.GetFileName(file);
                    if (name.EndsWith(".tmp", StringComparison.Ordinal)) continue;
                    if (!name.StartsWith(wanted, StringComparison.Ordinal)) continue;

                    var bytes = System.Text.Encoding.UTF8.GetBytes(name);
                    fixed (byte* p = bytes)
                    {
                        call(ctx, p, bytes.Length);
                    }
                }
            }
            catch { }
        }

        private delegate void SinkFn(IntPtr ctx, byte* data, long len);

        // ---- network ----------------------------------------------------------

        [MonoPInvokeCallback(typeof(SendFn))]
        private static void Send(IntPtr ud, Native.PsRequest* request)
        {
            try
            {
                var host = Resolve(ud);
                if (host == null || request == null) return;

                // The body is borrowed for this call only, so it is copied before returning —
                // which UnityWebRequest was going to do anyway when it took ownership.
                long len = request->BodyLen;
                var body = new byte[len < 0 ? 0 : len];
                for (long i = 0; i < body.Length; i++) body[i] = request->Body[i];

                var pending = new PendingRequest
                {
                    Id = request->Id,
                    Url = Decode(request->Url),
                    ApiKey = Decode(request->ApiKey),
                    ContentEncoding = request->ContentEncoding.Ptr == null
                        ? null
                        : Decode(request->ContentEncoding),
                    Body = body,
                };

                if (host.Runner == null)
                {
                    // No driver yet, so nothing can complete the request. Answering with 0 —
                    // "never reached a server" — keeps the batch spooled and retried, which is
                    // better than a request the core waits on forever.
                    host.Complete(pending.Id, 0, -1, null);
                    return;
                }
                host.Runner.StartCoroutine(host.Perform(pending));
            }
            catch { }
        }

        private sealed class PendingRequest
        {
            internal ulong Id;
            internal string Url;
            internal string ApiKey;
            internal string ContentEncoding;
            internal byte[] Body;
        }

        private IEnumerator Perform(PendingRequest pending)
        {
            using var www = new UnityWebRequest(pending.Url, UnityWebRequest.kHttpVerbPOST);
            www.uploadHandler = new UploadHandlerRaw(pending.Body);
            www.downloadHandler = new DownloadHandlerBuffer();
            www.SetRequestHeader("Authorization", "Bearer " + pending.ApiKey);
            www.SetRequestHeader("Content-Type", "application/json");
            if (pending.ContentEncoding != null)
                www.SetRequestHeader("Content-Encoding", pending.ContentEncoding);

            yield return www.SendWebRequest();

            // Status 0 for anything that never got an HTTP answer: a timeout, DNS, a refused
            // connection. The core reads that as transient, which is the only safe reading.
            ushort status = www.result == UnityWebRequest.Result.ConnectionError
                ? (ushort)0
                : (ushort)www.responseCode;

            long retryAfter = -1;
            string header = www.GetResponseHeader("Retry-After");
            if (!string.IsNullOrEmpty(header) && long.TryParse(header, out long seconds))
                retryAfter = seconds * 1000;

            Complete(pending.Id, status, retryAfter, www.downloadHandler?.data);
        }

        /// <summary>
        /// Answers the core. Exactly once per request, including when it never reached a server
        /// — a request the core never hears about again is a batch that never retries.
        /// </summary>
        private void Complete(ulong id, ushort status, long retryAfterMs, byte[] body)
        {
            var client = Client;
            if (client == null) return;

            body ??= Array.Empty<byte>();
            fixed (byte* p = body)
            {
                Native.ps_client_on_response(
                    client.Handle, id, status, retryAfterMs,
                    body.Length == 0 ? null : p, body.Length);
            }
        }

        // ---- log ---------------------------------------------------------------

        [MonoPInvokeCallback(typeof(LogFn))]
        private static void Log(IntPtr ud, int level, Native.PsStr message)
        {
            try
            {
                var host = Resolve(ud);
                string text = "[PlayerSense] " + Decode(message);
                switch (level)
                {
                    case Native.LevelError: Debug.LogError(text); break;
                    case Native.LevelWarn: Debug.LogWarning(text); break;
                    case Native.LevelInfo: Debug.Log(text); break;
                    default:
                        if (host != null && host._debug) Debug.Log(text);
                        break;
                }
            }
            catch { }
        }

        // ---- backup exclusion ---------------------------------------------------

        /// <summary>
        /// Keeps the installation identifier out of platform backup.
        /// </summary>
        /// <remarks>
        /// The most consequential thing in this file. iCloud and Android Auto Backup will
        /// otherwise restore the identifier onto a second device, and one installation emits
        /// from two phones forever with nothing anywhere reporting it — every retention curve
        /// built on it is quietly wrong, and nothing in the data looks broken.
        ///
        /// iOS is a flag on the directory, set here. Android is a choice of directory, made in
        /// <see cref="StorageRoot"/>.
        /// </remarks>
        private static void ExcludeFromBackup(string path)
        {
            try
            {
#if UNITY_IOS && !UNITY_EDITOR
                UnityEngine.iOS.Device.SetNoBackupFlag(path);
#endif
            }
            catch { }
        }
    }
}

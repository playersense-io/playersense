// Crashes as the operating system recorded them, read back on the next launch.
//
// A crash reporter's usual move is a signal handler that writes a report as the process dies.
// This package does not install one. Unity's Mono turns a null dereference into a
// NullReferenceException by catching SIGSEGV, IL2CPP and the engine install their own, and a
// game commonly ships a second crash reporter besides. Handlers chained across all of that are
// how crash reporters crash games, and a store reviewer sees the crash, not whose it was.
//
// Both mobile platforms keep their own account instead, written by the system after the
// process is gone, so it costs the game nothing and cannot interfere with anything:
//
//   Android 11+  ActivityManager.getHistoricalProcessExitReasons: Java and native crashes,
//                ANRs, and low-memory kills while in front.
//   iOS 14+      MetricKit crash diagnostics: signal, exception, termination reason and the
//                crashing thread's stack as binary offsets.
//
// Where neither exists (desktop, older phones), the core's abnormal-exit marker is the account.
// Where one does, the marker is switched off, so one crash is one record.

using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices;
using UnityEngine;

namespace PlayerSense.Core
{
    internal static class PlatformCrashes
    {
        private static bool s_started;
#if UNITY_ANDROID && !UNITY_EDITOR
        private static bool s_androidRead;
#elif UNITY_IOS && !UNITY_EDITOR
        private static int s_pollCountdown;
#endif

        /// <summary>Whether this device keeps its own account of how the last run ended.</summary>
        internal static bool Available()
        {
#if UNITY_ANDROID && !UNITY_EDITOR
            return AndroidSdkLevel() >= 30;
#elif UNITY_IOS && !UNITY_EDITOR
            try { return PlayerSense_MetricKit_Available() != 0; }
            catch (Exception) { return false; }
#else
            return false;
#endif
        }

        /// <summary>
        /// Registers for MetricKit as early in the launch as possible: iOS delivers the last
        /// run's diagnostics shortly after a subscriber appears.
        /// </summary>
        internal static void Start()
        {
            if (s_started) return;
            s_started = true;
#if UNITY_IOS && !UNITY_EDITOR
            try { PlayerSense_MetricKit_Start(); }
            catch (Exception) { }
#endif
        }

        /// <summary>Called every tick; does real work rarely.</summary>
        internal static void Collect(LogsModule logs)
        {
#if UNITY_ANDROID && !UNITY_EDITOR
            if (s_androidRead) return;
            s_androidRead = true;
            try { ReadAndroid(logs); }
            catch (Exception e) { Debug.Log($"[PlayerSense] could not read exit reasons: {e.Message}"); }
#elif UNITY_IOS && !UNITY_EDITOR
            // Every two seconds or so. MetricKit delivers once, a little after launch.
            if (--s_pollCountdown > 0) return;
            s_pollCountdown = 8;
            try { ReadMetricKit(logs); }
            catch (Exception e) { Debug.Log($"[PlayerSense] could not read MetricKit: {e.Message}"); }
#endif
        }

#if UNITY_ANDROID && !UNITY_EDITOR
        // ApplicationExitInfo.REASON_*
        private const int ReasonLowMemory = 3;
        private const int ReasonCrash = 4;
        private const int ReasonCrashNative = 5;
        private const int ReasonAnr = 6;
        private const int ReasonInitializationFailure = 7;
        // RunningAppProcessInfo.IMPORTANCE_VISIBLE: in front of the player, or on screen.
        private const int ImportanceVisible = 200;

        private static int AndroidSdkLevel()
        {
            try
            {
                using (var version = new AndroidJavaClass("android.os.Build$VERSION"))
                    return version.GetStatic<int>("SDK_INT");
            }
            catch (Exception)
            {
                return 0;
            }
        }

        private static void ReadAndroid(LogsModule logs)
        {
            if (AndroidSdkLevel() < 30) return;

            // Which exits were already reported, by the newest timestamp seen. A file beside the
            // spool rather than a core key: this is the binding's own bookkeeping.
            string seenPath = Path.Combine(UnityHost.StorageRoot(), "exit.seen");
            long seen = -1;
            if (File.Exists(seenPath) && long.TryParse(File.ReadAllText(seenPath), out long parsed))
                seen = parsed;

            var found = new List<LogEntry>();
            long newest = seen;

            using (var player = new AndroidJavaClass("com.unity3d.player.UnityPlayer"))
            using (var activity = player.GetStatic<AndroidJavaObject>("currentActivity"))
            using (var manager = activity.Call<AndroidJavaObject>("getSystemService", "activity"))
            {
                string package = activity.Call<string>("getPackageName");
                using (var list = manager.Call<AndroidJavaObject>(
                           "getHistoricalProcessExitReasons", package, 0, 16))
                {
                    int count = list.Call<int>("size");
                    for (int i = 0; i < count; i++)
                    {
                        using (var info = list.Call<AndroidJavaObject>("get", i))
                        {
                            long at = info.Call<long>("getTimestamp");
                            if (at > newest) newest = at;
                            // The first launch with capture on reports nothing old: history from
                            // before the player could have been asked is not ours to send.
                            if (seen < 0 || at <= seen) continue;
                            if (info.Call<string>("getProcessName") != package) continue;

                            var entry = Describe(info, at);
                            if (entry != null) found.Add(entry);
                        }
                    }
                }
            }

            // The system lists newest first; the dashboard reads better oldest first.
            for (int i = found.Count - 1; i >= 0; i--) logs.RecordPlatformCrash(found[i]);
            if (newest != seen || seen < 0)
                File.WriteAllText(seenPath, Math.Max(newest, 0).ToString());
        }

        private static LogEntry Describe(AndroidJavaObject info, long at)
        {
            int reason = info.Call<int>("getReason");
            int importance = info.Call<int>("getImportance");
            int status = info.Call<int>("getStatus");
            string description = info.Call<string>("getDescription");

            string type;
            switch (reason)
            {
                case ReasonCrash:
                    type = "Crash";
                    break;
                case ReasonCrashNative:
                    type = SignalName(status) ?? "NativeCrash";
                    break;
                case ReasonAnr:
                    type = "ANR";
                    break;
                case ReasonInitializationFailure:
                    type = "InitializationFailure";
                    break;
                case ReasonLowMemory when importance <= ImportanceVisible:
                    // Killed for memory while the player was looking at it: from their side,
                    // a crash, and in a game usually the game's own footprint.
                    type = "OutOfMemory";
                    break;
                default:
                    return null;
            }

            var entry = new LogEntry
            {
                Level = LogLevel.Fatal,
                Crash = true,
                Handled = false,
                Logger = "android",
                ExceptionType = type,
                ExceptionValue = string.IsNullOrEmpty(description) ? type : description,
                TimeMs = at,
            };
            entry.Attributes["exit.importance"] = importance.ToString();
            entry.Attributes["exit.pss_kb"] = info.Call<long>("getPss").ToString();
            entry.Attributes["exit.rss_kb"] = info.Call<long>("getRss").ToString();
            return entry;
        }
#endif

        private static string SignalName(int signal)
        {
            switch (signal)
            {
                case 4: return "SIGILL";
                case 5: return "SIGTRAP";
                case 6: return "SIGABRT";
                case 7: return "SIGBUS";
                case 8: return "SIGFPE";
                case 11: return "SIGSEGV";
                default: return null;
            }
        }

        /// <summary>
        /// Reads the plugin's line format: <c>key\tvalue</c> lines, and one
        /// <c>frame\tbinary\toffset\tinApp</c> line per frame, innermost first.
        /// </summary>
        /// <remarks>
        /// Lines rather than JSON so neither side needs a parser for a recursive call tree:
        /// the plugin walks the crashing thread, and this reads a flat list.
        /// </remarks>
        internal static LogEntry ParseMetricKit(string text)
        {
            var entry = new LogEntry
            {
                Level = LogLevel.Fatal,
                Crash = true,
                Handled = false,
                Logger = "ios",
            };
            foreach (var line in text.Split('\n'))
            {
                var parts = line.Split('\t');
                if (parts.Length < 2) continue;
                switch (parts[0])
                {
                    case "type": entry.ExceptionType = parts[1]; break;
                    case "value": entry.ExceptionValue = parts[1]; break;
                    case "time":
                        if (long.TryParse(parts[1], out long ms)) entry.TimeMs = ms;
                        break;
                    case "frame" when parts.Length >= 4:
                        entry.Frames.Add(new LogFrame
                        {
                            Module = parts[1],
                            Function = parts[2],
                            InApp = parts[3] == "1",
                        });
                        break;
                    default:
                        if (parts[0].StartsWith("exit.", StringComparison.Ordinal))
                            entry.Attributes[parts[0]] = parts[1];
                        break;
                }
            }
            if (string.IsNullOrEmpty(entry.ExceptionType)) entry.ExceptionType = "Crash";
            // Innermost first from the plugin; frames are stored outermost first.
            entry.Frames.Reverse();
            return entry;
        }

#if UNITY_IOS && !UNITY_EDITOR
        [DllImport("__Internal")] private static extern int PlayerSense_MetricKit_Available();
        [DllImport("__Internal")] private static extern void PlayerSense_MetricKit_Start();
        [DllImport("__Internal")] private static extern unsafe long PlayerSense_MetricKit_Next(byte* buffer, long cap);

        private static byte[] s_buffer = new byte[16 * 1024];

        private static unsafe void ReadMetricKit(LogsModule logs)
        {
            for (int guard = 0; guard < 16; guard++)
            {
                long len;
                fixed (byte* p = s_buffer) len = PlayerSense_MetricKit_Next(p, s_buffer.Length);
                if (len == 0) return;
                if (len < 0)
                {
                    // Too small: grown to what it said it needs, and asked again.
                    s_buffer = new byte[-len];
                    continue;
                }
                var entry = ParseMetricKit(System.Text.Encoding.UTF8.GetString(s_buffer, 0, (int)len));
                if (entry != null) logs.RecordPlatformCrash(entry);
            }
        }

#endif
    }
}

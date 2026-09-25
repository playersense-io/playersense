// PlayerSense.Logs: errors, exceptions and crashes, with the trail that led to them.
//
// Capture is two halves on purpose. The half that runs inside Unity's log callback, on
// whatever thread logged, does the least it can: one comparison against the level floor and a
// copy of two string references into a fixed ring, under a lock held for nanoseconds. No
// parsing, no allocation, no native call. A game that logs an error every frame pays for a
// struct copy, not for a crash reporter.
//
// The other half runs on the main thread, four times a second, from the runner. It parses the
// stacks that deserve it, asks BeforeSend, and hands records to the core, which folds repeats,
// applies the budget, scrubs, batches, compresses and spools. Only one thing skips the queue:
// an exception that is about to end the process, because there is no next tick.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using UnityEngine;
using UnityEngine.SceneManagement;
using PlayerSense.Core;

namespace PlayerSense
{
    public sealed class LogsModule
    {
        private const int Capacity = 256;
        private const int MaxFrames = 128;
        private const int MaxAttributes = 32;
        private const int MaxFingerprint = 16;

        private enum Kind : byte
        {
            None,
            /// <summary>A Unity log line at or above the send level.</summary>
            UnityRecord,
            /// <summary>A Unity log line kept only as a breadcrumb.</summary>
            UnityCrumb,
            /// <summary>An exception object: from the game, or a task nobody observed.</summary>
            Exception,
            /// <summary>A record the game built.</summary>
            Entry,
            /// <summary>A breadcrumb the game or the SDK left.</summary>
            Crumb,
        }

        /// <summary>One captured thing, as cheap to copy as it can be.</summary>
        private struct Captured
        {
            internal Kind Kind;
            internal LogLevel Level;
            internal bool IsException;
            internal bool Handled;
            internal string Message;
            internal string Stack;
            internal string Category;
            internal Exception Exception;
            internal LogEntry Entry;
            internal long TimeMs;
        }

        private readonly PlayerSenseClient _client;
        private readonly LogsOptions _options;
        private readonly bool _enabled;
        private readonly LogLevel _floor;
        private readonly string[] _notInApp;

        // The ring, filled from any thread and emptied on the main one.
        private readonly object _gate = new object();
        private readonly Captured[] _ring = new Captured[Capacity];
        private readonly Captured[] _draining = new Captured[Capacity];
        private int _head;
        private int _count;
        private long _overflowed;

        // Sending, which the main thread does every tick and a dying thread may do once.
        private readonly object _sendLock = new object();
        private readonly Utf8Scratch _scratch = new Utf8Scratch();
        private readonly Utf8Scratch.Span[] _frameSpans = new Utf8Scratch.Span[MaxFrames * 3];
        private readonly Utf8Scratch.Span[] _attributeSpans = new Utf8Scratch.Span[MaxAttributes * 2];
        private readonly Utf8Scratch.Span[] _fingerprintSpans = new Utf8Scratch.Span[MaxFingerprint];

        // Reused for every record Unity's log produces, so an error every frame allocates nothing
        // here. BeforeSend is told not to keep it.
        private readonly LogEntry _reused = new LogEntry();

        // The last stack parsed and what it parsed to. An error repeated every frame arrives with
        // the same stack each time, and is parsed once.
        private string _lastStack;
        private readonly List<LogFrame> _lastFrames = new List<LogFrame>();

        private bool _attached;
        private bool _warnedBeforeSend;

        internal LogsModule(PlayerSenseClient client, LogsOptions options)
        {
            _client = client;
            _options = options ?? new LogsOptions();
            _enabled = IsCapturing(_options);
            _floor = (LogLevel)Math.Min((int)_options.MinLevel, (int)_options.BreadcrumbLevel);

            var prefixes = new List<string>(StackTraces.DefaultNotInApp);
            if (_options.NotInAppPrefixes != null)
                foreach (var p in _options.NotInAppPrefixes)
                    if (!string.IsNullOrEmpty(p)) prefixes.Add(p);
            _notInApp = prefixes.ToArray();
        }

        /// <summary>Whether this build captures at all: enabled, and not the Editor unless asked.</summary>
        internal static bool IsCapturing(LogsOptions options) =>
            options != null && options.Enabled && (!Application.isEditor || options.CaptureInEditor);

        /// <summary>Records something the game wants on record. Safe from any thread.</summary>
        /// <returns>False when capture is off or the device's queue is full.</returns>
        public bool Record(LogEntry entry)
        {
            if (!_enabled || entry == null) return false;
            return Enqueue(new Captured { Kind = Kind.Entry, Entry = entry, TimeMs = Now() });
        }

        /// <summary>Records a message at a level. Safe from any thread.</summary>
        public bool Record(LogLevel level, string message, string logger = null,
                           IDictionary<string, string> attributes = null)
        {
            if (!_enabled || level < _floor) return false;
            if (level < _options.MinLevel && attributes == null)
                return Breadcrumb(logger ?? "log", message, level);

            var entry = new LogEntry { Level = level, Message = message, Logger = logger };
            if (attributes != null)
                foreach (var pair in attributes) entry.Attributes[pair.Key] = pair.Value;
            return Record(entry);
        }

        /// <summary>Records an exception your code caught. Safe from any thread.</summary>
        public bool Exception(Exception exception, IDictionary<string, string> attributes = null)
        {
            if (!_enabled || exception == null) return false;
            if (attributes == null)
            {
                return Enqueue(new Captured
                {
                    Kind = Kind.Exception, Level = LogLevel.Error, Exception = exception,
                    Handled = true, TimeMs = Now(),
                });
            }
            var entry = FromException(exception, true);
            foreach (var pair in attributes) entry.Attributes[pair.Key] = pair.Value;
            return Record(entry);
        }

        /// <summary>
        /// Leaves a note of something that happened, kept in memory and carried by the next
        /// error. Never sent on its own. Safe from any thread.
        /// </summary>
        public bool Breadcrumb(string category, string message, LogLevel level = LogLevel.Info)
        {
            if (!_enabled || string.IsNullOrEmpty(message)) return false;
            return Enqueue(new Captured
            {
                Kind = Kind.Crumb, Category = category, Message = Clip(message, 256), Level = level,
            });
        }

        /// <summary>What capture has done, for a diagnostics screen.</summary>
        public LogStats Stats
        {
            get
            {
                var raw = new Native.PsLogStats
                {
                    StructSize = (uint)System.Runtime.InteropServices.Marshal.SizeOf<Native.PsLogStats>(),
                };
                Native.ps_client_log_stats(_client.Handle, ref raw);
                long overflowed;
                lock (_gate) overflowed = _overflowed;
                return new LogStats(raw, (ulong)overflowed);
            }
        }

        // ---- wiring -----------------------------------------------------------------

        internal void Attach()
        {
            if (!_enabled || _attached) return;
            _attached = true;

            if (_options.CaptureUnityLog)
                Application.logMessageReceivedThreaded += OnUnityLog;
            if (_options.CaptureUnhandledExceptions)
            {
                AppDomain.CurrentDomain.UnhandledException += OnUnhandled;
                TaskScheduler.UnobservedTaskException += OnUnobservedTask;
            }
            SceneManager.sceneLoaded += OnSceneLoaded;
            Application.lowMemory += OnLowMemory;

            if (_options.ReportPlatformCrashes) PlatformCrashes.Start();
        }

        internal void Detach()
        {
            if (!_attached) return;
            _attached = false;

            Application.logMessageReceivedThreaded -= OnUnityLog;
            AppDomain.CurrentDomain.UnhandledException -= OnUnhandled;
            TaskScheduler.UnobservedTaskException -= OnUnobservedTask;
            SceneManager.sceneLoaded -= OnSceneLoaded;
            Application.lowMemory -= OnLowMemory;
        }

        /// <summary>Called by the runner before every tick, on the main thread.</summary>
        internal void Drain()
        {
            if (!_enabled) return;

            if (_options.ReportPlatformCrashes) PlatformCrashes.Collect(this);

            int n;
            lock (_gate)
            {
                n = _count;
                if (n == 0) return;
                for (int i = 0; i < n; i++)
                {
                    int at = (_head + i) % Capacity;
                    _draining[i] = _ring[at];
                    _ring[at] = default;
                }
                _head = 0;
                _count = 0;
            }

            for (int i = 0; i < n; i++)
            {
                try
                {
                    Process(ref _draining[i]);
                }
                catch (System.Exception e)
                {
                    // Never the game's problem. Prefixed, so capture does not capture itself.
                    Debug.LogWarning($"[PlayerSense] a log record could not be recorded: {e.Message}");
                }
                _draining[i] = default;
            }
        }

        // ---- capture: any thread, and cheap -----------------------------------------

        private void OnUnityLog(string condition, string stack, LogType type)
        {
            // The SDK's own lines. Without this, a warning about a dropped log would be a log.
            if (condition != null && condition.StartsWith("[PlayerSense]", StringComparison.Ordinal))
                return;

            LogLevel level;
            switch (type)
            {
                case LogType.Exception:
                case LogType.Error:
                case LogType.Assert:
                    level = LogLevel.Error;
                    break;
                case LogType.Warning:
                    level = LogLevel.Warning;
                    break;
                default:
                    level = LogLevel.Info;
                    break;
            }
            if (level < _floor) return;

            bool record = level >= _options.MinLevel;
            Enqueue(new Captured
            {
                Kind = record ? Kind.UnityRecord : Kind.UnityCrumb,
                Level = level,
                IsException = type == LogType.Exception,
                Message = condition,
                // A breadcrumb never shows a stack, so it never keeps one alive.
                Stack = record ? stack : null,
                TimeMs = record ? Now() : 0,
            });
        }

        private void OnUnhandled(object sender, UnhandledExceptionEventArgs args)
        {
            // An exception that does not end the process has already reached Unity's log, and
            // was captured there. Only a terminating one is new, and it cannot wait for a tick.
            if (!args.IsTerminating) return;
            try
            {
                if (!(args.ExceptionObject is Exception exception)) return;
                var entry = FromException(exception, false);
                entry.Crash = true;
                entry.Level = LogLevel.Fatal;
                // Synchronous, from the dying thread. The core writes a crash to disk before it
                // tries the network, so whatever does not go now goes on the next launch.
                Send(entry);
            }
            catch
            {
                // Nothing may escape a handler on a thread that is already dying.
            }
        }

        private void OnUnobservedTask(object sender, UnobservedTaskExceptionEventArgs args)
        {
            var exception = (Exception)args.Exception;
            if (args.Exception != null && args.Exception.InnerExceptions.Count == 1)
                exception = args.Exception.InnerExceptions[0];
            Enqueue(new Captured
            {
                Kind = Kind.Exception, Level = LogLevel.Error, Exception = exception,
                Handled = false, Category = "task", TimeMs = Now(),
            });
        }

        private void OnSceneLoaded(Scene scene, LoadSceneMode mode) =>
            Breadcrumb("navigation", mode == LoadSceneMode.Additive
                ? "Loaded scene " + scene.name + " (additive)"
                : "Loaded scene " + scene.name);

        private void OnLowMemory() => Breadcrumb("system", "Low memory warning", LogLevel.Warning);

        private bool Enqueue(in Captured item)
        {
            lock (_gate)
            {
                if (_count == Capacity)
                {
                    // The newest is dropped, not the oldest: what came first is what explains
                    // the flood, and the core would fold or rate-limit the rest anyway.
                    _overflowed++;
                    return false;
                }
                _ring[(_head + _count) % Capacity] = item;
                _count++;
                return true;
            }
        }

        // ---- recording: main thread -------------------------------------------------

        private void Process(ref Captured item)
        {
            switch (item.Kind)
            {
                case Kind.UnityCrumb:
                    AddBreadcrumb("log", Clip(item.Message, 256), item.Level);
                    return;
                case Kind.Crumb:
                    AddBreadcrumb(item.Category, item.Message, item.Level);
                    return;
                case Kind.UnityRecord:
                    SendChecked(FromUnity(ref item));
                    return;
                case Kind.Exception:
                    var entry = FromException(item.Exception, item.Handled);
                    entry.Logger = item.Category;
                    entry.TimeMs = item.TimeMs;
                    SendChecked(entry);
                    return;
                case Kind.Entry:
                    if (item.Entry.TimeMs == 0) item.Entry.TimeMs = item.TimeMs;
                    SendChecked(item.Entry);
                    return;
            }
        }

        private LogEntry FromUnity(ref Captured item)
        {
            var entry = _reused;
            Reset(entry);
            entry.Level = item.Level;
            entry.TimeMs = item.TimeMs;
            entry.Logger = "unity";

            if (item.IsException)
            {
                StackTraces.SplitException(item.Message, out entry.ExceptionType, out entry.ExceptionValue);
                if (entry.ExceptionType == null)
                {
                    entry.ExceptionType = "Exception";
                    entry.ExceptionValue = item.Message;
                }
                // Unity caught it for the game, which is not the game handling it.
                entry.Handled = false;
                Frames(item.Stack, entry.Frames);
            }
            else
            {
                entry.Message = item.Message;
                // A Debug.LogError has no exception to hang a stack on, and grouping reads its
                // message. What it gets is the game's own class that logged it, which is what
                // a reader wants next to the message and what keeps two classes' identical
                // messages apart.
                entry.Logger = CallSite(item.Stack) ?? "unity";
            }
            return entry;
        }

        private void Frames(string stack, List<LogFrame> into)
        {
            if (string.IsNullOrEmpty(stack)) return;
            if (!string.Equals(stack, _lastStack, StringComparison.Ordinal))
            {
                _lastFrames.Clear();
                StackTraces.ParseUnity(stack, _lastFrames, _notInApp);
                _lastStack = stack;
            }
            into.AddRange(_lastFrames);
        }

        /// <summary>The innermost frame that is the game's own, without parsing the rest.</summary>
        private string CallSite(string stack)
        {
            if (string.IsNullOrEmpty(stack)) return null;
            int start = 0;
            // Unity writes the innermost frame first, so the answer is usually the second line.
            for (int line = 0; line < 16 && start < stack.Length; line++)
            {
                int end = stack.IndexOf('\n', start);
                if (end < 0) end = stack.Length;
                int colon = stack.IndexOf(':', start, end - start);
                if (colon > start)
                {
                    string module = stack.Substring(start, colon - start).Trim();
                    if (StackTraces.IsInApp(module, _notInApp)) return module;
                }
                start = end + 1;
            }
            return null;
        }

        private LogEntry FromException(Exception exception, bool handled)
        {
            var entry = new LogEntry
            {
                Level = LogLevel.Error,
                ExceptionType = exception.GetType().Name,
                ExceptionValue = exception.Message,
                Handled = handled,
            };
            StackTraces.FromException(exception, entry.Frames, _notInApp);
            return entry;
        }

        internal void RecordPlatformCrash(LogEntry entry) => Send(entry);

        private void SendChecked(LogEntry entry)
        {
            var filter = _options.BeforeSend;
            if (filter != null)
            {
                bool keep;
                try
                {
                    keep = filter(entry);
                }
                catch (System.Exception e)
                {
                    // A filter that fails is a filter that did not run, so the record is not
                    // sent: it may hold exactly what the filter was written to remove.
                    if (!_warnedBeforeSend)
                    {
                        _warnedBeforeSend = true;
                        Debug.LogWarning(
                            $"[PlayerSense] Logs.BeforeSend threw, so records are dropped: {e.Message} " +
                            "(Only reported once.)");
                    }
                    return;
                }
                if (!keep) return;
            }
            Send(entry);
        }

        private unsafe void AddBreadcrumb(string category, string message, LogLevel level)
        {
            lock (_sendLock)
            {
                var s = _scratch;
                s.Reset();
                var c = s.Write(category);
                var m = s.Write(message);
                fixed (byte* origin = s.Buffer)
                {
                    Native.ps_client_add_breadcrumb(
                        _client.Handle, c.Resolve(origin), m.Resolve(origin), (int)level);
                }
            }
        }

        /// <summary>Hands one record to the core. Every string is encoded into one pinned buffer.</summary>
        private unsafe int Send(LogEntry e)
        {
            lock (_sendLock)
            {
                var s = _scratch;
                s.Reset();

                // Clipped before encoding, at the core's own limits, so a 2 MB message costs a
                // 8 KB encode and not a 2 MB one. Characters, not bytes: never more than the
                // core would keep.
                var message = s.Write(Clip(e.Message, 8 * 1024));
                var logger = s.Write(Clip(e.Logger, 128));
                var type = s.Write(Clip(e.ExceptionType, 256));
                var value = s.Write(Clip(e.ExceptionValue, 4 * 1024));

                // The innermost frames are the ones kept: they are where it happened.
                int frameCount = Math.Min(e.Frames.Count, MaxFrames);
                int firstFrame = e.Frames.Count - frameCount;
                for (int i = 0; i < frameCount; i++)
                {
                    var f = e.Frames[firstFrame + i];
                    _frameSpans[i * 3] = s.Write(Clip(f.Function, 256));
                    _frameSpans[i * 3 + 1] = s.Write(Clip(f.Module, 256));
                    _frameSpans[i * 3 + 2] = s.Write(Clip(f.File, 256));
                }

                int attributeCount = 0;
                foreach (var pair in e.Attributes)
                {
                    if (attributeCount == MaxAttributes) break;
                    if (string.IsNullOrEmpty(pair.Key)) continue;
                    _attributeSpans[attributeCount * 2] = s.Write(Clip(pair.Key, 64));
                    _attributeSpans[attributeCount * 2 + 1] = s.Write(Clip(pair.Value, 1024));
                    attributeCount++;
                }

                int fingerprintCount = Math.Min(e.Fingerprint?.Count ?? 0, MaxFingerprint);
                for (int i = 0; i < fingerprintCount; i++)
                    _fingerprintSpans[i] = s.Write(Clip(e.Fingerprint[i], 256));

                fixed (byte* origin = s.Buffer)
                {
                    Native.PsFrame* frames = stackalloc Native.PsFrame[frameCount == 0 ? 1 : frameCount];
                    for (int i = 0; i < frameCount; i++)
                    {
                        var f = e.Frames[firstFrame + i];
                        frames[i] = new Native.PsFrame
                        {
                            Function = _frameSpans[i * 3].Resolve(origin),
                            Module = _frameSpans[i * 3 + 1].Resolve(origin),
                            File = _frameSpans[i * 3 + 2].Resolve(origin),
                            Line = f.Line,
                            InApp = f.InApp ? 1 : 0,
                        };
                    }

                    Native.PsAttribute* attributes =
                        stackalloc Native.PsAttribute[attributeCount == 0 ? 1 : attributeCount];
                    for (int i = 0; i < attributeCount; i++)
                    {
                        attributes[i] = new Native.PsAttribute
                        {
                            Key = _attributeSpans[i * 2].Resolve(origin),
                            Value = _attributeSpans[i * 2 + 1].Resolve(origin),
                        };
                    }

                    Native.PsStr* fingerprint =
                        stackalloc Native.PsStr[fingerprintCount == 0 ? 1 : fingerprintCount];
                    for (int i = 0; i < fingerprintCount; i++)
                        fingerprint[i] = _fingerprintSpans[i].Resolve(origin);

                    var record = new Native.PsLogRecord
                    {
                        StructSize = (uint)sizeof(Native.PsLogRecord),
                        Level = (int)e.Level,
                        Message = message.Resolve(origin),
                        Logger = logger.Resolve(origin),
                        ExceptionType = type.Resolve(origin),
                        ExceptionValue = value.Resolve(origin),
                        Handled = e.Handled ? 1 : 0,
                        Crash = e.Crash ? 1 : 0,
                        Frames = frames,
                        FrameCount = frameCount,
                        Attributes = attributes,
                        AttributeCount = attributeCount,
                        Fingerprint = fingerprint,
                        FingerprintCount = fingerprintCount,
                        TimeMs = e.TimeMs,
                    };
                    int status = Native.ps_client_log(_client.Handle, ref record);
                    PlayerSenseClient.Check(status, "Logs");
                    return status;
                }
            }
        }

        private static void Reset(LogEntry entry)
        {
            entry.Level = LogLevel.Error;
            entry.Message = null;
            entry.Logger = null;
            entry.ExceptionType = null;
            entry.ExceptionValue = null;
            entry.Handled = false;
            entry.Crash = false;
            entry.Frames.Clear();
            entry.Attributes.Clear();
            entry.Fingerprint = null;
            entry.TimeMs = 0;
        }

        private static string Clip(string text, int max) =>
            text == null || text.Length <= max ? text : text.Substring(0, max);

        private static long Now() => DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
    }

    /// <summary>What log capture has done since the SDK started.</summary>
    public readonly struct LogStats
    {
        /// <summary>Records accepted for sending.</summary>
        public readonly ulong Recorded;
        /// <summary>Repeats counted on the identical record before them rather than sent again.</summary>
        public readonly ulong Folded;
        public readonly ulong Breadcrumbs;
        /// <summary>Over this device's budget for the minute. Crashes never are.</summary>
        public readonly ulong RateLimited;
        /// <summary>Discarded because the player declined. Not a fault: the SDK obeying them.</summary>
        public readonly ulong WithoutConsent;
        /// <summary>Lost to a full or ageing spool: the device was offline for a long time.</summary>
        public readonly ulong Evicted;
        public readonly ulong BatchesDelivered;
        public readonly ulong AcceptedByServer;
        public readonly ulong DroppedByServer;
        /// <summary>Dropped because more arrived between two ticks than the queue holds.</summary>
        public readonly ulong DroppedOnDevice;

        internal LogStats(Native.PsLogStats s, ulong droppedOnDevice)
        {
            Recorded = s.Recorded;
            Folded = s.Folded;
            Breadcrumbs = s.Breadcrumbs;
            RateLimited = s.RateLimited;
            WithoutConsent = s.WithoutConsent;
            Evicted = s.Evicted;
            BatchesDelivered = s.BatchesDelivered;
            AcceptedByServer = s.AcceptedByServer;
            DroppedByServer = s.DroppedByServer;
            DroppedOnDevice = droppedOnDevice;
        }
    }
}

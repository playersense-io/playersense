// The SDK's own thread: where captured logs are recorded and the core is ticked.
//
// The main thread is the game's. Measured on a Galaxy S23 pinned to an efficiency core,
// recording a captured error costs tens of microseconds, 256 queued log lines a few
// milliseconds, and encoding a 64 KB batch several more. None of that belongs in a frame. So
// the main thread keeps only what Unity requires of it (starting web requests, reading
// Android's exit reasons over JNI) and everything else happens here, four times a second.
//
// The core is shared with the main thread behind one lock, and the one slow part of a tick,
// encoding a batch, runs outside it (ps_client_tick), so a Track from the game never waits
// behind this thread for more than microseconds.

using System;
using System.Threading;
using UnityEngine;

namespace PlayerSense.Core
{
    internal sealed class SdkThread : IDisposable
    {
        private readonly Action _work;
        private readonly int _intervalMs;
        private readonly AutoResetEvent _wake = new AutoResetEvent(false);
        private readonly Thread _thread;
        private volatile bool _stopping;
        private bool _warned;

        /// <summary>Whether this platform can run one. WebGL cannot.</summary>
        internal static bool Supported
        {
            get
            {
#if UNITY_WEBGL && !UNITY_EDITOR
                return false;
#else
                return true;
#endif
            }
        }

        internal SdkThread(Action work, float intervalSeconds)
        {
            _work = work;
            _intervalMs = Math.Max(50, (int)(intervalSeconds * 1000));
            _thread = new Thread(Run)
            {
                Name = "PlayerSense",
                IsBackground = true,
                // Below the game's threads: nothing here is urgent, and on a phone the
                // scheduler is then free to put it on an efficiency core.
                Priority = System.Threading.ThreadPriority.BelowNormal,
            };
            _thread.Start();
        }

        /// <summary>Runs the work now rather than at the next interval.</summary>
        internal void Wake() => _wake.Set();

        private void Run()
        {
            while (!_stopping)
            {
                _wake.WaitOne(_intervalMs);
                if (_stopping) break;
                try
                {
                    _work();
                }
                catch (Exception e)
                {
                    // Never fatal, and said once rather than four times a second.
                    if (_warned) continue;
                    _warned = true;
                    Debug.LogWarning($"[PlayerSense] background work failed: {e.Message}");
                }
            }
        }

        /// <summary>Stops the thread and waits for the tick in progress, if any, to finish.</summary>
        public void Dispose()
        {
            _stopping = true;
            _wake.Set();
            // Bounded: a tick is milliseconds, and quitting must never hang on an SDK.
            _thread.Join(2000);
            _wake.Dispose();
        }
    }
}

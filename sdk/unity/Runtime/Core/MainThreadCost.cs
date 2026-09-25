// What the SDK costs the game's frame, measured where it is paid.
//
// The runner's Update is the only place the SDK does work on the main thread on its own
// account (the rest is on SdkThread), so it times itself: two timestamp reads a frame. A studio
// can read this on a real device, in a development build, rather than taking the SDK's word.

using System.Diagnostics;
using System.Threading;

namespace PlayerSense
{
    /// <summary>Time the SDK spent on the main thread, per frame, since it started.</summary>
    /// <remarks>
    /// Covers the SDK's own per-frame work. The log capture callback runs inside your own
    /// <c>Debug.Log</c> calls rather than in a frame of the SDK's, and is a copy into a ring;
    /// the package's performance tests measure it separately.
    /// </remarks>
    public readonly struct MainThreadCost
    {
        /// <summary>Frames measured.</summary>
        public readonly long Frames;
        /// <summary>Average time a frame spent in the SDK, in microseconds.</summary>
        public readonly double MeanMicros;
        /// <summary>The longest one frame spent in the SDK, in microseconds.</summary>
        public readonly double MaxMicros;

        internal MainThreadCost(long frames, double meanMicros, double maxMicros)
        {
            Frames = frames;
            MeanMicros = meanMicros;
            MaxMicros = maxMicros;
        }

        private static long s_frames;
        private static long s_totalTicks;
        private static long s_maxTicks;

        internal static void Record(long ticks)
        {
            s_frames++;
            s_totalTicks += ticks;
            if (ticks > s_maxTicks) s_maxTicks = ticks;
        }

        internal static void Reset()
        {
            s_frames = 0;
            s_totalTicks = 0;
            s_maxTicks = 0;
        }

        internal static MainThreadCost Current
        {
            get
            {
                long frames = Interlocked.Read(ref s_frames);
                double perMicro = Stopwatch.Frequency / 1_000_000.0;
                return new MainThreadCost(
                    frames,
                    frames == 0 ? 0 : s_totalTicks / perMicro / frames,
                    s_maxTicks / perMicro);
            }
        }

        public override string ToString() =>
            $"{MeanMicros:0.0} µs a frame on average, {MaxMicros:0.0} µs at most, over {Frames} frames";
    }
}

// What log capture costs a game, measured inside Unity.
//
// The native side is measured on its own (sdk/core/src/logs_bench.rs, runnable on a phone over
// adb). These are the parts only Unity can measure: the capture callback inside the game's own
// Debug.Log, and the main thread's share of a storm. Run them on a device, where they mean
// something: ./verify.sh --device. Each prints its figures with a [PlayerSense] prefix, so they
// are in the test log and are not themselves captured.

using System;
using System.Collections;
using System.Diagnostics;
using System.IO;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;
using PlayerSense.Core;
using Debug = UnityEngine.Debug;

namespace PlayerSense.Tests
{
    [Category("Performance")]
    public sealed class PerformanceTests
    {
        private const string TestKey = "ps_project_live_unitytests";
        private const string DeadEndpoint = "http://127.0.0.1:9";

        [SetUp]
        public void Setup()
        {
            SDK.Shutdown();
            string root = UnityHost.StorageRoot();
            if (Directory.Exists(root)) Directory.Delete(root, true);
            SDK.Initialize(new PlayerSenseOptions
            {
                ProjectKey = TestKey,
                Endpoint = DeadEndpoint,
                Logs = { Enabled = true, CaptureInEditor = true, ReportAbnormalExits = false },
            });
            Assert.IsTrue(SDK.IsInitialized, "the SDK did not start; see the Console");
            Privacy.Grant("test");
        }

        [TearDown]
        public void Teardown() => SDK.Shutdown();

        /// <summary>
        /// The callback runs inside every Debug.Log the game makes, so it is timed against the
        /// same calls with it unsubscribed, in alternating blocks so that drift in Unity's own
        /// logging cost cancels out.
        /// </summary>
        [UnityTest]
        public IEnumerator CapturingALogLineCostsTheGameMicroseconds()
        {
            const int blocks = 20, perBlock = 200;
            var logs = SDK.Client.Logs;
            double on = 0, off = 0;
            var clock = new Stopwatch();

            for (int block = 0; block < blocks; block++)
            {
                bool capturing = block % 2 == 0;
                if (capturing) logs.Attach(); else logs.Detach();

                clock.Restart();
                for (int i = 0; i < perBlock; i++) Debug.Log("Opened the shop");
                clock.Stop();
                if (capturing) on += clock.Elapsed.TotalMilliseconds;
                else off += clock.Elapsed.TotalMilliseconds;

                // Below the ring's capacity each block, and drained before the next.
                yield return new WaitForSecondsRealtime(0.3f);
            }
            logs.Attach();

            int calls = blocks / 2 * perBlock;
            double overheadMicros = (on - off) * 1000.0 / calls;
            Debug.Log($"[PlayerSense] perf: Debug.Log costs {off * 1000.0 / calls:0.00} µs without " +
                      $"capture and {on * 1000.0 / calls:0.00} µs with it: {overheadMicros:0.00} µs a line");
            Assert.Less(overheadMicros, 10.0,
                "capturing a log line should cost the game a few microseconds at most");
        }

        /// <summary>
        /// Three seconds of a game having a very bad time: every frame, thirty info lines, five
        /// different warnings and a caught exception. What the main thread pays is the SDK's
        /// own per-frame work, which records none of it: that is the SDK's thread's job.
        /// </summary>
        [UnityTest]
        public IEnumerator AStormOfErrorsStaysOffTheMainThread()
        {
            MainThreadCost.Reset();
            var frames = new Stopwatch();
            double worstFrame = 0;
            int n = 0;

            for (int frame = 0; frame < 180; frame++)
            {
                frames.Restart();
                for (int i = 0; i < 30; i++) Debug.Log("Spawned wave member");
                for (int i = 0; i < 5; i++) Debug.LogWarning($"Pathfinding gave up on agent {n++}");
                try
                {
                    throw new InvalidOperationException($"Inventory slot {frame} is locked");
                }
                catch (Exception e)
                {
                    Logs.Exception(e);
                }
                worstFrame = Math.Max(worstFrame, frames.Elapsed.TotalMilliseconds);
                yield return null;
            }
            yield return new WaitForSecondsRealtime(0.5f);

            var cost = SDK.MainThreadCost;
            var stats = Logs.Stats;
            Debug.Log($"[PlayerSense] perf: storm over {cost.Frames} frames: the SDK's main-thread " +
                      $"work took {cost.MeanMicros:0.0} µs a frame on average and " +
                      $"{cost.MaxMicros:0.0} µs at worst; the game's own logging loop took " +
                      $"{worstFrame:0.00} ms at worst. Recorded {stats.Recorded}, folded " +
                      $"{stats.Folded}, over budget {stats.RateLimited}, breadcrumbs " +
                      $"{stats.Breadcrumbs}, dropped between ticks {stats.DroppedOnDevice}.");

            Assert.Greater(stats.Recorded, 0UL, "nothing was recorded, so nothing was measured");
            Assert.Less(cost.MeanMicros, 200.0, "the SDK's per-frame main-thread work is too heavy");
            Assert.Less(cost.MaxMicros, 2000.0, "one frame spent too long in the SDK");
        }

        /// <summary>
        /// Garbage made on any thread becomes a collection that pauses the main one, so what
        /// capture allocates per line is reported, with Unity's own share included: it builds
        /// the strings a log callback receives.
        /// </summary>
        [UnityTest]
        public IEnumerator WhatCaptureAllocatesPerLine()
        {
            long probe = GC.GetAllocatedBytesForCurrentThread();
            var sink = new byte[1024];
            if (GC.GetAllocatedBytesForCurrentThread() - probe < sink.Length)
                Assert.Ignore("this runtime does not count allocations per thread");

            var logs = SDK.Client.Logs;
            const int lines = 200;
            long withCapture = 0, without = 0;
            for (int round = 0; round < 10; round++)
            {
                bool capturing = round % 2 == 0;
                if (capturing) logs.Attach(); else logs.Detach();
                long before = GC.GetAllocatedBytesForCurrentThread();
                for (int i = 0; i < lines; i++) Debug.Log("Opened the shop");
                long used = GC.GetAllocatedBytesForCurrentThread() - before;
                if (capturing) withCapture += used; else without += used;
                yield return new WaitForSecondsRealtime(0.3f);
            }
            logs.Attach();

            double perLine = (withCapture - without) / (5.0 * lines);
            Debug.Log($"[PlayerSense] perf: capture adds {perLine:0} bytes of garbage per " +
                      "Debug.Log on the calling thread, Unity's callback strings included.");
            Assert.Less(perLine, 4096, "capture allocates far more per line than it should");
        }
    }
}

// The four things only Unity can prove.
//
// The protocol itself is tested once, in Rust, against a fake host: 28 conformance scenarios
// covering sessions, consent, the spool, retries and deduplication (sdk/core), and the same
// core driven through the real extern "C" symbols (sdk/ffi). None of that is repeated here —
// a second implementation of the same assertions in C# would only ever disagree by being
// wrong.
//
// What is left is the seam: the C# struct declarations, the delegates the garbage collector
// can see, the directory the platform backs up, and the facade a game touches before the SDK
// exists. Every one of these fails on a device and nowhere else, which is why they are play
// mode tests rather than edit mode ones.

using System;
using System.Collections;
using System.IO;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;
using PlayerSense.Core;

namespace PlayerSense.Tests
{
    public sealed class BoundaryTests
    {
        private const string TestKey = "ps_project_live_unitytests";

        // Port 9 is discard: the connection is refused immediately rather than hanging, so a
        // test that exercises the send path does not wait on a timeout.
        private const string DeadEndpoint = "http://127.0.0.1:9";

        [SetUp]
        public void Setup()
        {
            // A project may have auto-start configured, and a previous test may have left a
            // client running. Every test here starts from nothing.
            SDK.Shutdown();
        }

        [TearDown]
        public void Teardown() => SDK.Shutdown();

        /// <summary>
        /// Every struct that crosses the boundary is declared twice, and nothing compiles both.
        /// </summary>
        /// <remarks>
        /// This is the single most valuable test in the package. A field added to PsConfig in
        /// Rust and not to Native.PsConfig in C# does not fail to compile, does not throw and
        /// does not log: it writes past the end of a struct and corrupts whatever follows. The
        /// self-check compares every size across the boundary and is the only thing standing
        /// between that and a heap bug nobody can reproduce.
        /// </remarks>
        [Test]
        public void TheNativeLibraryAgreesAboutEveryLayout()
        {
            Assert.IsTrue(Native.CheckAbi(out string problem),
                "the C# [StructLayout] declarations and the native library disagree: " + problem);
            Assert.AreEqual(Native.AbiVersion, Native.ps_abi_version());
        }

        /// <summary>
        /// The host's callbacks are still callable after a collection.
        /// </summary>
        /// <remarks>
        /// Mono collects a delegate whose only reference is the function pointer it handed to
        /// native code — a pointer the GC cannot see. The host roots all nine in static fields;
        /// this is what proves it. When it is wrong the symptom is a crash on the second or
        /// third callback, minutes into a session, which is unreproducible by hand.
        /// </remarks>
        [UnityTest]
        public IEnumerator TheHostSurvivesACollection()
        {
            SDK.Initialize(new PlayerSenseOptions
            {
                ProjectKey = TestKey,
                Endpoint = DeadEndpoint,
            });
            Assert.IsTrue(SDK.IsInitialized, "the SDK did not start; see the Console");

            // The install id exists only because the host was called during startup: random
            // bytes, a write, a read back.
            Assert.IsFalse(string.IsNullOrEmpty(Identity.InstallId));

            GC.Collect();
            GC.WaitForPendingFinalizers();
            GC.Collect();
            yield return null;

            Privacy.Grant("test");
            Analytics.Track("gc_probe");
            SDK.Flush();
            yield return null;

            // A spooled batch means store_put ran after the collection: the delegate survived.
            Assert.That(SDK.Stats.EventsTracked, Is.GreaterThan(0UL));
            Assert.That(SDK.Stats.BatchesSpooled, Is.GreaterThan(0UL),
                "nothing reached the disk after a GC, which is what a collected delegate looks " +
                "like when it does not crash outright");
        }

        /// <summary>
        /// The installation identifier is somewhere the platform will not copy to a new device.
        /// </summary>
        /// <remarks>
        /// If it is restored onto a second phone, one installation emits from two devices
        /// forever, every retention curve built on it is wrong, and nothing anywhere reports a
        /// problem. Android's answer is a directory the platform excludes; iOS's is a flag on
        /// the directory, which managed code cannot read back — so on iOS this asserts only
        /// that the flag was applied to something that exists.
        /// </remarks>
        [Test]
        public void TheInstallIdIsKeptOutOfPlatformBackup()
        {
            string root = UnityHost.StorageRoot();
            Assert.IsFalse(string.IsNullOrEmpty(root));

            // Printed, not just asserted. When this fails on somebody's device the path is the
            // whole diagnosis, and on a passing run it is the only record of what the platform
            // actually handed back.
            TestContext.WriteLine("storage root: " + root);

#if UNITY_ANDROID && !UNITY_EDITOR
            StringAssert.Contains("no_backup", root,
                "the spool is under a directory Android Auto Backup will copy to a new device");
#else
            Assert.That(root, Does.StartWith(Application.persistentDataPath));
#endif
            SDK.Initialize(new PlayerSenseOptions { ProjectKey = TestKey, Endpoint = DeadEndpoint });
            Assert.IsTrue(SDK.IsInitialized, "the SDK did not start; see the Console");
            Assert.IsTrue(Directory.Exists(root), "the SDK did not create its own directory");
        }

        /// <summary>
        /// Every call is safe before the SDK starts, and after it fails to.
        /// </summary>
        /// <remarks>
        /// A missing key, a missing plugin, a build where the native library did not ship — all
        /// of them leave the SDK not running, and a game must not crash for any of them. The
        /// static API is null-safe on purpose rather than by accident, so it is asserted.
        /// </remarks>
        [Test]
        public void TheApiIsSafeBeforeTheSdkStarts()
        {
            Assert.IsFalse(SDK.IsInitialized);

            Assert.DoesNotThrow(() =>
            {
                Analytics.Track("before_init");
                Analytics.TrackRevenue("purchase", 4.99m, "USD");
                Identity.SetPlayer("player_1");
                Identity.ClearPlayer();
                Privacy.Grant("test");
                Privacy.Deny();
                Context.Player.Set("rank", "gold");
                Context.Session.Set("mode", "ranked");
                Context.Session.Remove("mode");
                Flags.Refresh();
                LiveOps.Refresh();
                SDK.Flush();
                SDK.Shutdown();
            });

            Assert.IsNull(Identity.InstallId);
            Assert.AreEqual(0UL, Context.Revision);
            Assert.AreEqual(0UL, SDK.Stats.EventsTracked);
        }

        /// <summary>
        /// Before the SDK starts, every flag is the default the caller passed.
        /// </summary>
        /// <remarks>
        /// The whole reason each read takes a default: a game must behave the same whether the
        /// SDK came up, the network is down, or nobody has published that flag yet. A game that
        /// has to check <c>Flags.Ready</c> first is a game that behaves differently on a slow
        /// connection, which is the bug this shape exists to make impossible.
        /// </remarks>
        [Test]
        public void EveryFlagAnswersWithTheCallersDefault()
        {
            Assert.IsFalse(SDK.IsInitialized);

            Assert.IsTrue(Flags.Bool("anything", true));
            Assert.IsFalse(Flags.Bool("anything", false));
            Assert.AreEqual(42.5, Flags.Number("anything", 42.5));
            Assert.AreEqual(7, Flags.Int("anything", 7));
            Assert.AreEqual("fallback", Flags.Text("anything", "fallback"));
            Assert.IsNull(Flags.Text("anything"));

            Assert.IsFalse(Flags.Ready);
            Assert.AreEqual(0u, Flags.Version);
        }

        /// <summary>
        /// A flag the project never published answers with the caller's default, with the SDK
        /// running.
        /// </summary>
        /// <remarks>
        /// The previous test passes before the SDK starts, where the facade short-circuits and
        /// the module is never reached — so it proves the facade and nothing about the module.
        /// This one runs the whole path: the native lookup returns "not published", and the
        /// default the caller passed comes back rather than a zero the SDK invented.
        ///
        /// That distinction is the reason `PS_NOT_FOUND` exists at all. A studio's typo in a
        /// flag key must look like the game's own default, not like a decision somebody made.
        /// </remarks>
        [Test]
        public void AnUnpublishedFlagAnswersWithTheCallersDefault()
        {
            SDK.Initialize(new PlayerSenseOptions
            {
                ProjectKey = TestKey,
                Endpoint = DeadEndpoint,
                // Deliberately none: nothing is fetched, so every flag is unpublished and the
                // fallback path is the only path.
                ConfigEndpoint = "",
            });
            Assert.IsTrue(SDK.IsInitialized, "the SDK did not start; see the Console");

            Assert.IsTrue(Flags.Bool("never_published", true));
            Assert.IsFalse(Flags.Bool("never_published", false));
            Assert.AreEqual(42.5, Flags.Number("never_published", 42.5));
            Assert.AreEqual(7, Flags.Int("never_published", 7));
            Assert.AreEqual("fallback", Flags.Text("never_published", "fallback"));

            // A null key is a caller's bug and must answer like any other miss rather than
            // throwing out of a property read. The guard in FlagsModule short-circuits it; a
            // build without that guard would reach the ABI and get the same answer, which is
            // why this asserts the behaviour and not the path.
            Assert.IsTrue(Flags.Bool("", true));
            Assert.IsTrue(Flags.Bool(null, true));

            // Reading a flag records that this player saw it (§44), deduplicated to one per
            // flag per session — so seven distinct flags read many times each is seven
            // exposures plus the session start, not one per read.
            //
            // Counted through Stats because that is the only thing an editor test can see:
            // nothing is delivered here, the endpoint is a discard port.
            ulong tracked = SDK.Stats.EventsTracked;
            for (int i = 0; i < 50; i++) Flags.Bool("never_published", true);
            Assert.AreEqual(tracked, SDK.Stats.EventsTracked,
                "fifty reads of one flag recorded more than the first exposure");

            // Nothing was fetched, so nothing claims to be ready and no window is open.
            Assert.IsFalse(Flags.Ready);
            Assert.AreEqual(0u, Flags.Version);
            Assert.AreEqual(0, LiveOps.Active.Count);
            Assert.IsFalse(LiveOps.Ready);
        }

        /// <summary>
        /// Before the SDK starts, nothing is running and nothing is coming.
        /// </summary>
        /// <remarks>
        /// Empty lists rather than null, because a game iterating them should not have to
        /// null-check first — and a NullReferenceException in a UI loop is exactly the crash
        /// this package promises never to cause.
        /// </remarks>
        [Test]
        public void LiveOpsIsEmptyRatherThanNullBeforeTheSdkStarts()
        {
            Assert.IsFalse(SDK.IsInitialized);

            Assert.IsNotNull(LiveOps.Active);
            Assert.AreEqual(0, LiveOps.Active.Count);
            Assert.IsNotNull(LiveOps.Upcoming);
            Assert.AreEqual(0, LiveOps.Upcoming.Count);

            Assert.IsFalse(LiveOps.IsActive("winter_sale"));
            Assert.IsFalse(LiveOps.TryGet("winter_sale", out var found));
            Assert.AreEqual(default(LiveOpsEvent), found);

            Assert.AreEqual(0L, LiveOps.ServerTimeMs);
            Assert.IsFalse(LiveOps.Ready);
            Assert.AreEqual(System.TimeSpan.Zero, LiveOps.Remaining(default));
            Assert.AreEqual(System.TimeSpan.Zero, LiveOps.StartsIn(default));

            // Iterating is the thing a game actually does, and it must not throw.
            Assert.DoesNotThrow(() =>
            {
                foreach (var running in LiveOps.Active) { _ = running.Key; }
            });
        }
    }
}

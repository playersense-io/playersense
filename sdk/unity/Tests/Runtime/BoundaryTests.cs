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
                SDK.Flush();
                SDK.Shutdown();
            });

            Assert.IsNull(Identity.InstallId);
            Assert.AreEqual(0UL, Context.Revision);
            Assert.AreEqual(0UL, SDK.Stats.EventsTracked);
        }
    }
}

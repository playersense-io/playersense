// What only Unity can prove about log capture.
//
// Folding, the budget, scrubbing, consent and the wire format are tested in Rust (sdk/core,
// logs_tests.rs). What is left is the seam: Unity's log callback reaching the core, a record
// made on a worker thread arriving on the main one, the SDK not capturing itself, the parsers
// that read Unity's and MetricKit's formats, and the consent verbs a settings screen calls.

using System.Collections;
using System.IO;
using System.Text.RegularExpressions;
using System.Threading.Tasks;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;
using PlayerSense.Core;

namespace PlayerSense.Tests
{
    public sealed class LogsTests
    {
        private const string TestKey = "ps_project_live_unitytests";
        private const string DeadEndpoint = "http://127.0.0.1:9";

        [SetUp]
        public void Setup()
        {
            SDK.Shutdown();
            // A consent answer or a held crash from an earlier test would decide this one.
            string root = UnityHost.StorageRoot();
            if (Directory.Exists(root)) Directory.Delete(root, true);
        }

        [TearDown]
        public void Teardown() => SDK.Shutdown();

        private static void Start(System.Action<LogsOptions> tweak = null)
        {
            var logs = new LogsOptions
            {
                Enabled = true,
                CaptureInEditor = true,
                // A previous run of the Editor is not a crash of this test.
                ReportAbnormalExits = false,
            };
            tweak?.Invoke(logs);
            SDK.Initialize(new PlayerSenseOptions
            {
                ProjectKey = TestKey,
                Endpoint = DeadEndpoint,
                TickIntervalSeconds = 0.05f,
                Logs = logs,
            });
            Assert.IsTrue(SDK.IsInitialized, "the SDK did not start; see the Console");
        }

        private static IEnumerator Ticks() { yield return new WaitForSecondsRealtime(0.3f); }

        [UnityTest]
        public IEnumerator NothingIsCapturedUnlessTheStudioTurnsItOn()
        {
            SDK.Initialize(new PlayerSenseOptions
            {
                ProjectKey = TestKey, Endpoint = DeadEndpoint, TickIntervalSeconds = 0.05f,
            });
            Privacy.Grant("test");
            LogAssert.Expect(LogType.Error, "not captured");
            Debug.LogError("not captured");
            yield return Ticks();
            Assert.AreEqual(0UL, Logs.Stats.Recorded);
        }

        [UnityTest]
        public IEnumerator ADebugLogErrorReachesTheCore()
        {
            Start();
            Privacy.Grant("test");
            Debug.Log("Opened the shop");
            LogAssert.Expect(LogType.Error, "Receipt refused");
            Debug.LogError("Receipt refused");
            yield return Ticks();

            var stats = Logs.Stats;
            Assert.AreEqual(1UL, stats.Recorded, "the error was not recorded");
            Assert.GreaterOrEqual(stats.Breadcrumbs, 1UL, "the info line before it was not kept");
        }

        [UnityTest]
        public IEnumerator TheSdksOwnLinesAreNotCaptured()
        {
            Start();
            Privacy.Grant("test");
            Debug.LogWarning("[PlayerSense] a line of our own");
            yield return Ticks();
            Assert.AreEqual(0UL, Logs.Stats.Recorded);
        }

        [UnityTest]
        public IEnumerator ARecordFromAWorkerThreadArrives()
        {
            Start();
            Privacy.Grant("test");
            var worker = Task.Run(() => Logs.Error("from a worker"));
            while (!worker.IsCompleted) yield return null;
            yield return Ticks();
            Assert.AreEqual(1UL, Logs.Stats.Recorded);
        }

        [UnityTest]
        public IEnumerator BeforeSendCanDropARecord()
        {
            Start(logs => logs.BeforeSend = entry => !entry.Message.Contains("secret"));
            Privacy.Grant("test");
            Logs.Error("the secret level");
            Logs.Error("an ordinary failure");
            yield return Ticks();
            Assert.AreEqual(1UL, Logs.Stats.Recorded);
        }

        [UnityTest]
        public IEnumerator ACrashWaitsForTheAnswerAndANoDeletesIt()
        {
            Start();
            Logs.Record(new LogEntry { Crash = true, Level = LogLevel.Fatal, Message = "died" });
            yield return Ticks();
            Assert.AreEqual(1, Privacy.PendingCrashReports, "the crash was not held on the device");

            Privacy.DenyErrorReports();
            Assert.AreEqual(0, Privacy.PendingCrashReports, "a no did not delete it");
        }

        [UnityTest]
        public IEnumerator ErrorReportsCanBeSharedWithoutAnalytics()
        {
            Start();
            Privacy.Deny();
            Privacy.GrantErrorReports("test");
            Logs.Error("only the error");
            yield return Ticks();
            Assert.AreEqual(1UL, Logs.Stats.Recorded);
        }

        [Test]
        public void UnitysStackFormatIsReadOutermostFirst()
        {
            const string stack =
                "UnityEngine.Debug:LogError (object)\n" +
                "Game.Store:Refuse (string) (at Assets/Scripts/Store.cs:42)\n" +
                "Game.Shop:Buy () (at Assets/Scripts/Shop.cs:17)\n" +
                "UnityEngine.EventSystems.ExecuteEvents:Execute (UnityEngine.EventSystems.IPointerClickHandler,UnityEngine.EventSystems.BaseEventData)\n";
            var frames = new System.Collections.Generic.List<LogFrame>();
            StackTraces.ParseUnity(stack, frames, StackTraces.DefaultNotInApp);

            Assert.AreEqual(3, frames.Count, "the logging frame should be dropped");
            Assert.AreEqual("UnityEngine.EventSystems.ExecuteEvents", frames[0].Module);
            Assert.IsFalse(frames[0].InApp);
            var innermost = frames[2];
            Assert.AreEqual("Game.Store", innermost.Module);
            Assert.AreEqual("Refuse", innermost.Function);
            Assert.AreEqual("Assets/Scripts/Store.cs", innermost.File);
            Assert.AreEqual(42, innermost.Line);
            Assert.IsTrue(innermost.InApp);
        }

        [Test]
        public void AnExceptionLineSplitsIntoTypeAndMessage()
        {
            StackTraces.SplitException(
                "NullReferenceException: Object reference not set to an instance of an object",
                out string type, out string value);
            Assert.AreEqual("NullReferenceException", type);
            Assert.AreEqual("Object reference not set to an instance of an object", value);

            StackTraces.SplitException("Could not load: the file", out type, out value);
            Assert.IsNull(type, "a sentence is not a type name");
        }

        [Test]
        public void AMetricKitCrashIsReadWithItsFrames()
        {
            var entry = PlatformCrashes.ParseMetricKit(
                "type\tEXC_BAD_ACCESS\nvalue\tNamespace SIGNAL, Code 11\ntime\t1789560000000\n" +
                "exit.exception_code\t0\n" +
                "frame\tUnityFramework\t0x1a2b\t1\nframe\tUnityFramework\t0x3c4d\t1\nframe\tdyld\t0x10\t0\n");
            Assert.IsTrue(entry.Crash);
            Assert.AreEqual("EXC_BAD_ACCESS", entry.ExceptionType);
            Assert.AreEqual(1789560000000L, entry.TimeMs);
            Assert.AreEqual(3, entry.Frames.Count);
            Assert.AreEqual("0x1a2b", entry.Frames[2].Function, "innermost last");
            Assert.AreEqual("dyld", entry.Frames[0].Module);
            Assert.AreEqual("0", entry.Attributes["exit.exception_code"]);
        }
    }
}

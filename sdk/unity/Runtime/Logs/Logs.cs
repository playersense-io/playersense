// PlayerSense.Logs: what a game types.
//
// Most games type nothing. With capture on, Debug.LogError, Debug.LogException, exceptions
// nothing caught and crashes are recorded without a line of code. This is for the rest: an
// error the game wants on record without writing it to the console, an exception it caught
// and still wants to hear about, and a note for the trail.
//
// Every call is safe from any thread, returns at once and never throws.

using System;
using System.Collections.Generic;

namespace PlayerSense
{
    /// <summary>Records errors, exceptions and the trail that led to them.</summary>
    public static class Logs
    {
        /// <summary>Records a message. Below the capture level it is kept as a breadcrumb.</summary>
        public static void Info(string message, IDictionary<string, string> attributes = null) =>
            Module("Logs.Info")?.Record(LogLevel.Info, message, null, attributes);

        /// <summary>Records a warning.</summary>
        public static void Warning(string message, IDictionary<string, string> attributes = null) =>
            Module("Logs.Warning")?.Record(LogLevel.Warning, message, null, attributes);

        /// <summary>Records an error.</summary>
        /// <code>
        /// PlayerSense.Logs.Error("Receipt refused", new Dictionary&lt;string, string&gt; { ["store"] = "google" });
        /// </code>
        public static void Error(string message, IDictionary<string, string> attributes = null) =>
            Module("Logs.Error")?.Record(LogLevel.Error, message, null, attributes);

        /// <summary>Records an exception your code caught, with its stack.</summary>
        public static void Exception(Exception exception, IDictionary<string, string> attributes = null) =>
            Module("Logs.Exception")?.Exception(exception, attributes);

        /// <summary>Records a record you built, for full control over its fields.</summary>
        public static void Record(LogEntry entry) => Module("Logs.Record")?.Record(entry);

        /// <summary>
        /// Leaves a note of something that happened: kept in memory, carried by the next error,
        /// never sent on its own.
        /// </summary>
        /// <code>
        /// PlayerSense.Logs.Breadcrumb("shop", "Opened the gem pack offer");
        /// </code>
        public static void Breadcrumb(string category, string message, LogLevel level = LogLevel.Info) =>
            Module("Logs.Breadcrumb")?.Breadcrumb(category, message, level);

        /// <summary>What capture has done, for a diagnostics screen. Zeroes when it never started.</summary>
        public static LogStats Stats => SDK.Client?.Logs.Stats ?? default;

        private static LogsModule Module(string what)
        {
            var module = SDK.Client?.Logs;
            if (module == null) SDK.WarnNotInitialized(what);
            return module;
        }
    }
}

// Stacks as frames: Unity's log format, and .NET exceptions.
//
// Parsed on the main thread when a batch of captured lines is drained, never inside the log
// callback, and only for errors and exceptions: a warning or an info line carries no stack,
// because nobody groups warnings by where they were written and parsing is the only part of
// capture that costs anything.
using System;
using System.Collections.Generic;

namespace PlayerSense.Core
{
    internal static class StackTraces
    {
        /// <summary>
        /// Frames from these are the engine's or a library's rather than the game's. Grouping
        /// reads only the game's own frames, so an engine frame shared by two bugs never merges
        /// them into one issue.
        /// </summary>
        internal static readonly string[] DefaultNotInApp =
        {
            "UnityEngine.", "UnityEditor.", "Unity.", "System.", "Mono.", "Microsoft.",
            "TMPro.", "Newtonsoft.", "Cysharp.", "DG.Tweening.", "PlayerSense.",
        };

        /// <summary>Frames that are the act of logging, not the thing logged.</summary>
        private static readonly string[] s_logging =
        {
            "UnityEngine.Debug", "UnityEngine.Logger", "UnityEngine.DebugLogHandler",
            "PlayerSense.Logs", "PlayerSense.LogsModule",
        };

        /// <summary>
        /// Reads Unity's stack string, innermost first, into frames outermost first.
        /// </summary>
        /// <remarks>
        /// <c>Game.Player:Update () (at Assets/Scripts/Player.cs:42)</c> in a development build,
        /// <c>Game.Player:Update()</c> in an IL2CPP release without line numbers. Anything that
        /// matches neither is skipped rather than guessed at.
        /// </remarks>
        internal static void ParseUnity(string stack, List<LogFrame> into, string[] notInApp)
        {
            if (string.IsNullOrEmpty(stack)) return;
            int start = into.Count;
            int lineStart = 0;
            while (lineStart < stack.Length && into.Count - start < 128)
            {
                int lineEnd = stack.IndexOf('\n', lineStart);
                if (lineEnd < 0) lineEnd = stack.Length;
                var frame = ParseUnityLine(stack, lineStart, lineEnd, notInApp);
                if (frame != null) into.Add(frame);
                lineStart = lineEnd + 1;
            }
            into.Reverse(start, into.Count - start);
        }

        private static LogFrame ParseUnityLine(string stack, int from, int to, string[] notInApp)
        {
            string line = stack.Substring(from, to - from).Trim();
            if (line.Length == 0) return null;

            string file = null;
            int lineNumber = 0;
            int at = line.IndexOf(" (at ", StringComparison.Ordinal);
            if (at >= 0)
            {
                int close = line.LastIndexOf(')');
                if (close > at + 5)
                {
                    string location = line.Substring(at + 5, close - at - 5);
                    int colon = location.LastIndexOf(':');
                    if (colon > 0 && int.TryParse(location.Substring(colon + 1), out int n))
                    {
                        file = location.Substring(0, colon);
                        lineNumber = n;
                    }
                    else file = location;
                }
                line = line.Substring(0, at);
            }

            int paren = line.IndexOf('(');
            string qualified = (paren >= 0 ? line.Substring(0, paren) : line).Trim();
            if (qualified.Length == 0) return null;

            SplitQualified(qualified, ':', out string module, out string function);
            if (IsLogging(module)) return null;
            return new LogFrame
            {
                Module = module,
                Function = function,
                File = file,
                Line = lineNumber,
                InApp = IsInApp(module, notInApp),
            };
        }

        /// <summary>A .NET exception's frames, outermost first.</summary>
        /// <remarks>
        /// From <see cref="System.Diagnostics.StackTrace"/> rather than the exception's string:
        /// the objects carry the declaring type and method separately, where the string would
        /// have to be parsed back out of a locale-dependent format. IL2CPP fills in the file and
        /// line only when "IL2CPP Stacktrace Information" is set to include them.
        /// </remarks>
        internal static void FromException(Exception exception, List<LogFrame> into, string[] notInApp)
        {
            System.Diagnostics.StackTrace trace;
            try
            {
                trace = new System.Diagnostics.StackTrace(exception, true);
            }
            catch
            {
                return;
            }
            int count = Math.Min(trace.FrameCount, 128);
            // Frame 0 is where it was thrown; frames are wanted outermost first.
            for (int i = count - 1; i >= 0; i--)
            {
                var frame = trace.GetFrame(i);
                var method = frame?.GetMethod();
                if (method == null) continue;
                string module = method.DeclaringType?.FullName ?? "";
                into.Add(new LogFrame
                {
                    Module = module,
                    Function = method.Name,
                    File = frame.GetFileName(),
                    Line = Math.Max(0, frame.GetFileLineNumber()),
                    InApp = IsInApp(module, notInApp),
                });
            }
        }

        /// <summary>
        /// Splits Unity's exception line, <c>NullReferenceException: Object reference not set…</c>,
        /// into the type and the message. A line that does not start with a type name is a
        /// message with no type.
        /// </summary>
        internal static void SplitException(string condition, out string type, out string value)
        {
            type = null;
            value = condition;
            if (string.IsNullOrEmpty(condition)) return;
            int colon = condition.IndexOf(": ", StringComparison.Ordinal);
            string head = colon > 0 ? condition.Substring(0, colon) : condition;
            if (head.Length > 200 || head.IndexOf(' ') >= 0) return;
            type = head;
            value = colon > 0 ? condition.Substring(colon + 2) : "";
        }

        private static void SplitQualified(string qualified, char separator, out string module, out string function)
        {
            int split = qualified.LastIndexOf(separator);
            if (split < 0) split = qualified.LastIndexOf('.');
            if (split <= 0)
            {
                module = "";
                function = qualified;
                return;
            }
            module = qualified.Substring(0, split);
            function = qualified.Substring(split + 1);
        }

        private static bool IsLogging(string module)
        {
            foreach (var prefix in s_logging)
                if (module.StartsWith(prefix, StringComparison.Ordinal)) return true;
            return false;
        }

        internal static bool IsInApp(string module, string[] notInApp)
        {
            if (string.IsNullOrEmpty(module)) return false;
            foreach (var prefix in notInApp)
                if (module.StartsWith(prefix, StringComparison.Ordinal)) return false;
            return true;
        }
    }
}

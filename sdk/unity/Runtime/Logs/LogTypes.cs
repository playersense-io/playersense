// The shapes a game sees of a log record: its level, the record itself and its frames.
using System.Collections.Generic;

namespace PlayerSense
{
    /// <summary>How serious a record is. A threshold keeps everything at or above it.</summary>
    public enum LogLevel
    {
        Trace = 0,
        Debug = 1,
        Info = 2,
        Warning = 3,
        Error = 4,
        Fatal = 5,
    }

    /// <summary>One call on a stack.</summary>
    public sealed class LogFrame
    {
        public string Function;
        /// <summary>The class, namespace or library.</summary>
        public string Module;
        public string File;
        public int Line;
        /// <summary>The game's own code, rather than the engine's or a library's.</summary>
        public bool InApp;
    }

    /// <summary>
    /// A log line, an exception or a crash, as it is about to be recorded.
    /// </summary>
    /// <remarks>
    /// This is what <see cref="LogsOptions.BeforeSend"/> receives: change it, or return false to
    /// drop it. The SDK has already taken emails, tokens, card numbers, addresses and account
    /// names in file paths out of it by the time it is sent; what only your game knows (a
    /// player's display name in a message, say) is yours to remove here.
    /// </remarks>
    public sealed class LogEntry
    {
        /// <summary>Error unless set: a record somebody built by hand is usually one.</summary>
        public LogLevel Level = LogLevel.Error;
        public string Message;
        /// <summary>Where it was logged from: a category, a subsystem, a class.</summary>
        public string Logger;
        public string ExceptionType;
        public string ExceptionValue;
        /// <summary>Whether the game caught the exception.</summary>
        public bool Handled;
        /// <summary>The process died of this.</summary>
        public bool Crash;
        /// <summary>Outermost first, innermost (where it was thrown) last.</summary>
        public readonly List<LogFrame> Frames = new List<LogFrame>();
        /// <summary>Anything to search by. At most 32, each at most 1 KB.</summary>
        public readonly Dictionary<string, string> Attributes = new Dictionary<string, string>();
        /// <summary>
        /// Overrides how records group into issues: records with equal fingerprints are one issue.
        /// </summary>
        public List<string> Fingerprint;

        /// <summary>When it happened, for a crash read back from a previous run. 0 is now.</summary>
        internal long TimeMs;
    }
}

// PlayerSense.LiveOps — what a game types.
//
// A static front for the one shared client. Before the SDK starts, every list is empty and
// every time is zero: a game shows no event rather than crashing, which is the same rule the
// rest of this package follows.

using System;
using System.Collections.Generic;

namespace PlayerSense
{
    /// <summary>Events you scheduled in the dashboard, as the player sees them.</summary>
    public static class LiveOps
    {
        private static readonly LiveOpsEvent[] None = Array.Empty<LiveOpsEvent>();

        /// <summary>The events running right now, highest priority first.</summary>
        /// <remarks>
        /// <code>
        /// foreach (var running in PlayerSense.LiveOps.Active)
        ///     if (running.Kind == "offer") ShowBanner(running.Payload);
        /// </code>
        /// The list is reused between calls: copy it if you hold it past this frame. Empty
        /// until the server has answered, including when a document was read from disk — a
        /// window needs a clock somebody has checked.
        /// </remarks>
        public static IReadOnlyList<LiveOpsEvent> Active
            => SDK.Client?.LiveOps.Active ?? None;

        /// <summary>The events starting soon, soonest first.</summary>
        public static IReadOnlyList<LiveOpsEvent> Upcoming
            => SDK.Client?.LiveOps.Upcoming ?? None;

        /// <summary>Whether one particular event is running.</summary>
        public static bool IsActive(string key) => SDK.Client?.LiveOps.IsActive(key) ?? false;

        /// <summary>Finds one running event by key.</summary>
        public static bool TryGet(string key, out LiveOpsEvent found)
        {
            var module = SDK.Client?.LiveOps;
            if (module != null) return module.TryGet(key, out found);
            found = default;
            return false;
        }

        /// <summary>How long is left of an event, measured against the server's clock.</summary>
        public static TimeSpan Remaining(LiveOpsEvent running)
            => SDK.Client?.LiveOps.Remaining(running) ?? TimeSpan.Zero;

        /// <summary>How long until an upcoming event starts.</summary>
        public static TimeSpan StartsIn(LiveOpsEvent upcoming)
            => SDK.Client?.LiveOps.StartsIn(upcoming) ?? TimeSpan.Zero;

        /// <summary>Now, in the server's clock. Zero until the server has answered.</summary>
        /// <remarks>
        /// The only clock these windows mean anything against. Comparing them with
        /// <c>DateTime.UtcNow</c> works right up until a player changes their device clock,
        /// which is exactly the player who would.
        /// </remarks>
        public static long ServerTimeMs => SDK.Client?.LiveOps.ServerTimeMs ?? 0;

        /// <summary>Whether the server has answered in this session.</summary>
        public static bool Ready => SDK.Client?.LiveOps.Ready ?? false;

        /// <summary>Asks the server for a fresh answer now.</summary>
        public static void Refresh() => SDK.Client?.LiveOps.Refresh();
    }
}

// PlayerSense.Analytics — what a game types.
//
// A static front for the one shared client, so the common case is one line with nothing to
// hold, inject or dispose. Everything here forwards to PlayerSense.SDK.Client.Analytics;
// a studio that wants the object instead can take it from PlayerSense.SDK.Client.
//
// Nothing here throws. Before the SDK starts, or after a failed start, every call is a no-op
// and the first one says so once. A game must not crash because analytics did not come up.

namespace PlayerSense
{
    /// <summary>Records what happens in your game.</summary>
    public static class Analytics
    {
        /// <summary>Records that something happened.</summary>
        /// <remarks>
        /// Never blocks, never throws, never awaits. The call copies into a buffer and returns;
        /// batching, compression, retries and the disk spool happen off the gameplay path.
        /// <code>
        /// PlayerSense.Analytics.Track("enemy_killed", new() { ["enemy"] = "orc", ["weapon"] = "bow" });
        /// </code>
        /// </remarks>
        public static void Track(string name, EventProperties properties = null)
        {
            var module = SDK.Client?.Analytics;
            if (module == null)
            {
                SDK.WarnNotInitialized("Analytics.Track");
                return;
            }
            module.Track(name, properties);
        }

        /// <summary>Records an event that carries money.</summary>
        /// <remarks>
        /// Analytics about a purchase, not the purchase itself: the authoritative record comes
        /// from your server verifying the receipt with the store, because a modified build can
        /// claim any amount it likes. This is for funnels and dashboards.
        /// </remarks>
        public static void TrackRevenue(string name, decimal amount, string currency,
                                        EventProperties properties = null)
        {
            var module = SDK.Client?.Analytics;
            if (module == null)
            {
                SDK.WarnNotInitialized("Analytics.TrackRevenue");
                return;
            }
            module.TrackRevenue(name, amount, currency, properties);
        }
    }
}

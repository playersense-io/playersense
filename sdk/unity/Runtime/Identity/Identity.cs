// PlayerSense.Identity — who the player is, in your terms.

namespace PlayerSense
{
    /// <summary>Your own identifier for the player.</summary>
    public static class Identity
    {
        /// <summary>Attaches your identifier to every subsequent event.</summary>
        /// <remarks>
        /// Does not flush and does not restart the session. The identifier travels per event,
        /// so a batch that straddles a sign-in is correct: the events before it are anonymous
        /// and the events after carry the player. Call it when your login completes.
        /// </remarks>
        public static void SetPlayer(string playerId)
        {
            var module = SDK.Client?.Identity;
            if (module == null)
            {
                SDK.WarnNotInitialized("Identity.SetPlayer");
                return;
            }
            module.SetPlayer(playerId);
        }

        /// <summary>Detaches the player. Subsequent events are anonymous again.</summary>
        public static void ClearPlayer() => SDK.Client?.Identity.ClearPlayer();

        /// <summary>
        /// The installation this device is known by, or null before the SDK starts.
        /// </summary>
        /// <remarks>
        /// Useful in a support screen: it is the one identifier that ties a player's report to
        /// their data. A reinstall produces a new one, by design.
        /// </remarks>
        public static string InstallId => SDK.Client?.Identity.InstallId;
    }
}

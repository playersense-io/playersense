// PlayerSense.Flags — what a game types.
//
// A static front for the one shared client, so reading a flag is one line with nothing to
// hold or inject. Every call takes the game's own default and returns it when the SDK is not
// running, has not fetched yet, or the project has not published that flag — which is why
// there is no separate "is it ready" check to forget.

namespace PlayerSense
{
    /// <summary>Values a studio can change without shipping a build.</summary>
    public static class Flags
    {
        /// <summary>Reads a boolean flag, or your default.</summary>
        /// <remarks>
        /// <code>
        /// if (PlayerSense.Flags.Bool("new_hud", false)) ShowNewHud();
        /// </code>
        /// Safe before the SDK starts and safe when the network is down: both answer with the
        /// default you passed. A game must behave correctly in both, and passing the default
        /// here rather than reading it from a dashboard is what makes that possible.
        /// </remarks>
        public static bool Bool(string key, bool fallback = false)
            => SDK.Client?.Flags.Bool(key, fallback) ?? fallback;

        /// <summary>Reads a numeric flag, or your default.</summary>
        public static double Number(string key, double fallback = 0)
            => SDK.Client?.Flags.Number(key, fallback) ?? fallback;

        /// <summary>Reads a numeric flag as an int, or your default.</summary>
        public static int Int(string key, int fallback = 0)
            => SDK.Client?.Flags.Int(key, fallback) ?? fallback;

        /// <summary>Reads a string flag — or a JSON one as text — or your default.</summary>
        public static string Text(string key, string fallback = null)
            => SDK.Client?.Flags.Text(key, fallback) ?? fallback;

        /// <summary>Whether the server has answered in this session.</summary>
        /// <remarks>
        /// For a debug overlay. A game should not branch on it: every read already falls back
        /// to the default you passed, and code that waits for this is code that behaves
        /// differently on a slow network.
        /// </remarks>
        public static bool Ready => SDK.Client?.Flags.Ready ?? false;

        /// <summary>The published version behind the current answers, or 0.</summary>
        public static uint Version => SDK.Client?.Flags.Version ?? 0;

        /// <summary>Asks the server for a fresh answer now.</summary>
        /// <remarks>
        /// Rarely needed: the SDK asks every quarter of an hour and whenever the app returns
        /// to the front. Call it after changing something you segment on, when you want the
        /// answer that follows from it. Returns immediately; new values appear on a later frame.
        /// </remarks>
        public static void Refresh() => SDK.Client?.Flags.Refresh();
    }
}

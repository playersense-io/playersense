// PlayerSense.Context — your game's own vocabulary.
//
//     PlayerSense.Context.Session.Set("game_mode", "ranked");
//     PlayerSense.Context.Player.Set("progress.level", 42);
//
// PlayerSense never learns what any of those mean, which is exactly why it fits a game with
// chapters and zones, a game with maps and dungeons, and a game with no progression at all.

namespace PlayerSense
{
    /// <summary>What is true right now, in your own words.</summary>
    /// <remarks>
    /// Context is state; event properties describe one occurrence. `weapon` on an
    /// `enemy_killed` event is a property; `rank` is context. A value set here decorates every
    /// event <em>after</em> it, never the ones already recorded.
    /// </remarks>
    public static class Context
    {
        /// <summary>Values that end with the session: where they are and what they are doing.</summary>
        public static Scope Session { get; } = new Scope(ContextScope.Session);

        /// <summary>Values that follow the player: rank, progression, subscription tier.</summary>
        /// <remarks>Cleared when <see cref="Identity.SetPlayer"/> changes who is playing.</remarks>
        public static Scope Player { get; } = new Scope(ContextScope.Player);

        /// <summary>Rises whenever anything in either scope changes.</summary>
        /// <remarks>
        /// A configuration or targeting refresh debounces on this rather than on every Set, so
        /// eight changes during a loading screen are one refresh instead of eight.
        /// </remarks>
        public static ulong Revision => SDK.Client?.Context.Revision ?? 0;

        /// <summary>One scope's values. Reached through <see cref="Session"/> or <see cref="Player"/>.</summary>
        public sealed class Scope
        {
            private readonly ContextScope _scope;

            internal Scope(ContextScope scope) => _scope = scope;

            public void Set(string key, string value) => Target("Set")?.Set(key, value);
            public void Set(string key, long value) => Target("Set")?.Set(key, value);
            public void Set(string key, double value) => Target("Set")?.Set(key, value);
            public void Set(string key, bool value) => Target("Set")?.Set(key, value);

            /// <summary>Removes a value. Subsequent events no longer carry it.</summary>
            public void Remove(string key) => Target("Remove")?.Remove(key);

            private ContextModule.Scoped Target(string what)
            {
                var module = SDK.Client?.Context;
                if (module == null)
                {
                    SDK.WarnNotInitialized($"Context.{_scope}.{what}");
                    return null;
                }
                return _scope == ContextScope.Player ? module.Player : module.Session;
            }
        }
    }
}

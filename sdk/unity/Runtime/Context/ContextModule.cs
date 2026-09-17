// PlayerSense.Context — your game's own vocabulary.
//
// PlayerSense never learns what any of these mean. One studio has chapters and zones, the next
// has maps and dungeons, the third has seasons and leagues; a `SetLevel` method would fit the
// first and lie to the other two. So this is a key, a value and a scope.
//
// Context is state. Event properties describe one occurrence; context describes what was true
// while it happened, and it is carried once per batch rather than copied onto every event.

using PlayerSense.Core;

namespace PlayerSense
{
    /// <summary>How long a context value lives.</summary>
    public enum ContextScope
    {
        /// <summary>Until the session ends: where they are and what they are doing.</summary>
        Session,
        /// <summary>Until the player changes: what is true of them.</summary>
        Player,
    }

    public sealed class ContextModule
    {
        private readonly PlayerSenseClient _client;

        internal ContextModule(PlayerSenseClient client)
        {
            _client = client;
            Session = new Scoped(client, ContextScope.Session);
            Player = new Scoped(client, ContextScope.Player);
        }

        /// <summary>Values that end with the session: game mode, map, difficulty.</summary>
        public Scoped Session { get; }

        /// <summary>Values that follow the player: rank, progression, subscription tier.</summary>
        public Scoped Player { get; }

        /// <summary>Rises whenever anything changes. A configuration refresh debounces on it.</summary>
        public ulong Revision
        {
            get
            {
                ulong revision;
                return Native.ps_client_context_revision(_client.Handle, out revision) == Native.Ok
                    ? revision
                    : 0;
            }
        }

        /// <summary>One scope's values.</summary>
        public sealed class Scoped
        {
            private readonly PlayerSenseClient _client;
            private readonly int _scope;

            internal Scoped(PlayerSenseClient client, ContextScope scope)
            {
                _client = client;
                _scope = scope == ContextScope.Player ? Native.ScopePlayer : Native.ScopeSession;
            }

            public void Set(string key, string value) =>
                Write(key, new Native.PsValue { Kind = Native.ValueStr }, value);

            public void Set(string key, long value) =>
                Write(key, new Native.PsValue { Kind = Native.ValueInt, Int = value }, null);

            public void Set(string key, double value) =>
                Write(key, new Native.PsValue { Kind = Native.ValueFloat, Float = value }, null);

            public void Set(string key, bool value) =>
                Write(key, new Native.PsValue { Kind = Native.ValueBool, Bool = value ? 1 : 0 }, null);

            /// <summary>Removes a value.</summary>
            public unsafe void Remove(string key)
            {
                var scratch = _client.Scratch;
                scratch.Reset();
                var span = scratch.Write(key);
                fixed (byte* origin = scratch.Buffer)
                {
                    PlayerSenseClient.Check(
                        Native.ps_client_remove_context(_client.Handle, _scope, span.Resolve(origin)),
                        $"Context.Remove(\"{key}\")");
                }
            }

            private unsafe void Write(string key, Native.PsValue value, string stringValue)
            {
                var scratch = _client.Scratch;
                scratch.Reset();
                var keySpan = scratch.Write(key);
                var valueSpan = value.Kind == Native.ValueStr ? scratch.Write(stringValue) : default;

                fixed (byte* origin = scratch.Buffer)
                {
                    if (value.Kind == Native.ValueStr) value.Str = valueSpan.Resolve(origin);
                    int status = Native.ps_client_set_context(
                        _client.Handle, _scope, keySpan.Resolve(origin), ref value);

                    // A reserved namespace is the one refusal worth shouting about: the
                    // developer is heading for a targeting rule that never matches, and the
                    // core's message names who owns the name they picked.
                    if (status == Native.RejectedContextReserved)
                    {
                        UnityEngine.Debug.LogError(
                            $"[PlayerSense] Context key '{key}' is reserved. " +
                            (Native.LastError() ?? Native.Describe(status)));
                        return;
                    }
                    PlayerSenseClient.Check(status, $"Context.Set(\"{key}\")");
                }
            }
        }
    }
}

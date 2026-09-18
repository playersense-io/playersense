// PlayerSense.Flags — values a studio changes without shipping a build.
//
// Every call takes the game's own default and returns it unless the server published
// something else. That shape is deliberate: the default lives at the call site, next to the
// code that depends on it, rather than in a dashboard somebody has to open to find out what
// happens when the network is down.
//
// The rules are never here. The server decides which players get what and sends values —
// ADR 0027: an audience that reaches a modified build is an audience that build can satisfy.

using System;
using PlayerSense.Core;

namespace PlayerSense
{
    public sealed class FlagsModule
    {
        // 64 KB. A string flag is a label or a small document; the sizing call below is what
        // handles anything larger, so this only has to cover the ordinary case without
        // allocating twice for it.
        private const int TextScratchBytes = 64 * 1024;

        private readonly PlayerSenseClient _client;
        private byte[] _text = new byte[1024];

        internal FlagsModule(PlayerSenseClient client) => _client = client;

        /// <summary>The published version these answers came from, or 0 before the first fetch.</summary>
        public uint Version
        {
            get
            {
                Native.ps_client_config_version(_client.Handle, out uint version);
                return version;
            }
        }

        /// <summary>Whether the server has answered in this session.</summary>
        /// <remarks>
        /// False means flags are still the game's own defaults, or the last ones read from
        /// disk. Worth showing in a debug overlay and worth ignoring everywhere else: a game
        /// should behave correctly either way, which is what passing a default to every call
        /// is for.
        /// </remarks>
        public bool Ready
        {
            get
            {
                Native.ps_client_config_anchored(_client.Handle, out int anchored);
                return anchored != 0;
            }
        }

        /// <summary>Reads a boolean flag, or your default when the project has not published one.</summary>
        public bool Bool(string key, bool fallback = false)
        {
            if (string.IsNullOrEmpty(key)) return fallback;

            var scratch = _client.Scratch;
            scratch.Reset();
            var span = scratch.Write(key);

            unsafe
            {
                fixed (byte* origin = scratch.Buffer)
                {
                    int status = Native.ps_client_flag_bool(
                        _client.Handle, span.Resolve(origin), out int value);
                    // NotFound is not a failure and is deliberately not logged: most games
                    // ask about flags only some environments have, and a warning per frame
                    // for a flag nobody published is a warning nobody reads.
                    return status == Native.Ok ? value != 0 : fallback;
                }
            }
        }

        /// <summary>Reads a numeric flag, or your default.</summary>
        public double Number(string key, double fallback = 0)
        {
            if (string.IsNullOrEmpty(key)) return fallback;

            var scratch = _client.Scratch;
            scratch.Reset();
            var span = scratch.Write(key);

            unsafe
            {
                fixed (byte* origin = scratch.Buffer)
                {
                    int status = Native.ps_client_flag_number(
                        _client.Handle, span.Resolve(origin), out double value);
                    return status == Native.Ok ? value : fallback;
                }
            }
        }

        /// <summary>Reads a numeric flag as an int, or your default.</summary>
        /// <remarks>
        /// Rounded rather than truncated, because a studio typing 10 into a dashboard and a
        /// game reading 9 is the kind of difference nobody thinks to look for. A flag whose
        /// value does not fit an int returns the default rather than wrapping.
        /// </remarks>
        public int Int(string key, int fallback = 0)
        {
            double value = Number(key, fallback);
            if (double.IsNaN(value) || value < int.MinValue || value > int.MaxValue) return fallback;
            return (int)Math.Round(value, MidpointRounding.AwayFromZero);
        }

        /// <summary>Reads a string flag, or your default.</summary>
        /// <remarks>
        /// A <c>json</c> flag answers here too, as the text it was published as: a game
        /// reading one wants the document to parse, and a second method to remember would buy
        /// nothing. Parse it with whatever you already use for your own data — PlayerSense
        /// does not look inside it.
        /// </remarks>
        public string Text(string key, string fallback = null)
        {
            if (string.IsNullOrEmpty(key)) return fallback;

            var scratch = _client.Scratch;
            scratch.Reset();
            var span = scratch.Write(key);

            unsafe
            {
                fixed (byte* origin = scratch.Buffer)
                {
                    var psKey = span.Resolve(origin);

                    fixed (byte* buffer = _text)
                    {
                        int status = Native.ps_client_flag_text(
                            _client.Handle, psKey, buffer, _text.Length, out long len);
                        if (status == Native.Ok)
                            return len == 0 ? string.Empty : System.Text.Encoding.UTF8.GetString(_text, 0, (int)len);
                        if (status != Native.ErrBufferTooSmall)
                            return fallback;

                        // The call reported the length it needed. Grow once and ask again
                        // rather than guessing large every time — a flag read per frame
                        // should not carry a 64 KB buffer for the one studio that publishes
                        // a big document.
                        if (len <= 0 || len > TextScratchBytes) return fallback;
                        _text = new byte[len];
                    }

                    fixed (byte* buffer = _text)
                    {
                        int status = Native.ps_client_flag_text(
                            _client.Handle, psKey, buffer, _text.Length, out long len);
                        return status == Native.Ok
                            ? System.Text.Encoding.UTF8.GetString(_text, 0, (int)len)
                            : fallback;
                    }
                }
            }
        }

        /// <summary>Asks the server for a fresh answer now.</summary>
        /// <remarks>
        /// Rarely needed: the SDK asks on its own every quarter of an hour and whenever the
        /// app comes back to the front. Call it after changing something you segment on —
        /// the player finished the tutorial, joined a guild — when you want the answer that
        /// follows from it rather than the one from before.
        ///
        /// Returns immediately. The new values appear on a later frame; nothing here awaits.
        /// </remarks>
        public void Refresh() => Native.ps_client_refresh_config(_client.Handle);
    }
}

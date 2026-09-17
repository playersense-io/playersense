// PlayerSense.Analytics — the whole analytics API is one verb.
//
// There is no TrackSessionEvent, no TrackPurchaseEvent and no TrackError. The server derives
// an event's type from what it carries: revenue makes it a purchase, a `session_` prefix makes
// it a session, everything else is custom. A developer names the thing that happened and
// carries on.

using System;
using PlayerSense.Core;

namespace PlayerSense
{
    public sealed class AnalyticsModule
    {
        private readonly PlayerSenseClient _client;

        internal AnalyticsModule(PlayerSenseClient client) => _client = client;

        /// <summary>Records that something happened.</summary>
        /// <remarks>
        /// Never blocks, never throws, never awaits. The call copies into a ring buffer and
        /// returns; everything else — batching, spooling, compression, retries — happens off
        /// the gameplay path.
        /// </remarks>
        public void Track(string name, EventProperties properties = null)
        {
            TrackInternal(name, 0, null, properties);
        }

        /// <summary>
        /// Records an event that carries money.
        /// </summary>
        /// <remarks>
        /// This is analytics about a purchase, not the purchase itself. The authoritative
        /// record comes from your backend verifying the receipt against the store, because a
        /// modified build can claim anything. Use this for funnels and dashboards.
        /// </remarks>
        public void TrackRevenue(string name, decimal amount, string currency,
                                 EventProperties properties = null)
        {
            // Micros, so a price is an exact integer rather than a float that has already lost
            // the cent by the time it reaches a sum.
            long micros = (long)Math.Round(amount * 1_000_000m, MidpointRounding.AwayFromZero);
            TrackInternal(name, micros, currency, properties);
        }

        private unsafe void TrackInternal(string name, long revenueMicros, string currency,
                                          EventProperties properties)
        {
            var scratch = _client.Scratch;
            scratch.Reset();

            var nameSpan = scratch.Write(name);
            var currencySpan = scratch.Write(currency);

            int count = properties?.Count ?? 0;
            var keys = count > 0 ? new Utf8Scratch.Span[count] : null;
            var values = count > 0 ? new Native.PsValue[count] : null;
            var strings = count > 0 ? new Utf8Scratch.Span[count] : null;
            int kept = 0;

            if (properties != null)
            {
                foreach (var pair in properties)
                {
                    if (!Convert(pair.Value, scratch, out var value, out var stringSpan))
                    {
                        // A value whose type has no column is dropped rather than turned into
                        // a string. The server does the same: nothing silently changes meaning
                        // between what the game passed and what a chart reads.
                        UnityEngine.Debug.LogWarning(
                            $"[PlayerSense] property '{pair.Key}' has unsupported type " +
                            $"{pair.Value?.GetType().Name ?? "null"} and was dropped");
                        continue;
                    }
                    keys[kept] = scratch.Write(pair.Key);
                    values[kept] = value;
                    strings[kept] = stringSpan;
                    kept++;
                }
            }

            fixed (byte* origin = scratch.Buffer)
            {
                Native.PsProperty* props = stackalloc Native.PsProperty[kept == 0 ? 1 : kept];
                for (int i = 0; i < kept; i++)
                {
                    var value = values[i];
                    if (value.Kind == Native.ValueStr) value.Str = strings[i].Resolve(origin);
                    props[i] = new Native.PsProperty { Key = keys[i].Resolve(origin), Value = value };
                }

                var psName = nameSpan.Resolve(origin);
                int status = revenueMicros == 0 && currency == null
                    ? Native.ps_client_track(_client.Handle, psName, props, kept)
                    : Native.ps_client_track_revenue(_client.Handle, psName, revenueMicros,
                        currencySpan.Resolve(origin), props, kept);

                PlayerSenseClient.Check(status, $"Track(\"{name}\")");
            }
        }

        /// <summary>Maps a boxed C# value onto the four types the server keeps apart.</summary>
        private static bool Convert(object raw, Utf8Scratch scratch,
                                    out Native.PsValue value, out Utf8Scratch.Span stringSpan)
        {
            value = default;
            stringSpan = default;

            switch (raw)
            {
                case string s:
                    value.Kind = Native.ValueStr;
                    stringSpan = scratch.Write(s);
                    return true;
                case bool b:
                    value.Kind = Native.ValueBool;
                    value.Bool = b ? 1 : 0;
                    return true;
                case sbyte or byte or short or ushort or int or uint or long:
                    value.Kind = Native.ValueInt;
                    value.Int = System.Convert.ToInt64(raw);
                    return true;
                case float or double or decimal:
                    value.Kind = Native.ValueFloat;
                    value.Float = System.Convert.ToDouble(raw);
                    return true;
                case Enum e:
                    // An enum is a name in a chart, not a number nobody can read.
                    value.Kind = Native.ValueStr;
                    stringSpan = scratch.Write(e.ToString());
                    return true;
                default:
                    return false;
            }
        }
    }
}

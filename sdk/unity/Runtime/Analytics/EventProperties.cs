// The property bag a game passes to Track.
//
// A Dictionary subclass so that collection-initializer syntax works, which is what the API
// should look like at a call site:
//
//     PlayerSense.Analytics.Track("level_completed", new() { ["level"] = 12, ["score"] = 9500 });
//
// Boxing a few value types per event is a real cost and the right trade: the protocol caps an
// event at 64 properties, a game tracks events a few times a second rather than a few thousand,
// and the alternative reads far worse at every call site.

using System.Collections.Generic;

namespace PlayerSense
{
    /// <summary>Properties describing one occurrence. For state that persists, use <see cref="PlayerSense.Context"/>.</summary>
    public sealed class EventProperties : Dictionary<string, object>
    {
        public EventProperties() { }
        public EventProperties(int capacity) : base(capacity) { }

        /// <summary>Adds a value and returns this, for call sites that prefer chaining.</summary>
        public EventProperties With(string key, object value)
        {
            this[key] = value;
            return this;
        }
    }
}

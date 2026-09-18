// PlayerSense.LiveOps — what is running right now, and what is coming.
//
// A LiveOps event is a thing that happens in your game on a calendar: a double-XP weekend, a
// holiday shop, an offer on the first five days of every month. A studio schedules it once in
// the dashboard; this is how the game finds out.
//
// # Two things the game does not have to do
//
// **No calendar.** The recurrence lives on the server, so "every Friday" becoming "every
// Saturday" is a dashboard change rather than a release. What arrives here is concrete: this
// window, and when the next one starts.
//
// **No clock.** Every instant is the server's, and the SDK tracks the difference. A player who
// moves their device clock forward does not open a holiday event early, and one whose clock is
// behind does not miss the end of an offer.

using System;
using System.Collections.Generic;
using PlayerSense.Core;
using UnityEngine;

namespace PlayerSense
{
    /// <summary>One LiveOps event the player is inside, or about to be.</summary>
    [Serializable]
    public struct LiveOpsEvent
    {
        /// <summary>The key you gave it in the dashboard.</summary>
        public string Key;

        /// <summary>Your own classification: "seasonal_event", "offer", "announcement".</summary>
        public string Kind;

        /// <summary>Higher wins when two events of the same kind are both on.</summary>
        public int Priority;

        /// <summary>When this occurrence began, in server milliseconds since the epoch.</summary>
        public long StartsAtMs;

        /// <summary>When it ends. Compare against <see cref="LiveOpsModule.ServerTimeMs"/>.</summary>
        public long EndsAtMs;

        /// <summary>When it comes back, or 0 when it never does.</summary>
        public long NextStartsAtMs;

        /// <summary>
        /// Your own payload, exactly as you published it, as JSON text.
        /// </summary>
        /// <remarks>
        /// Text rather than a parsed object because PlayerSense does not understand what is
        /// inside it — a shop layout, an economy multiplier, a banner — and a type declared
        /// here would be a type every studio had to bend theirs into. Parse it with
        /// <c>JsonUtility.FromJson</c> or whatever you already use.
        /// </remarks>
        public string Payload;
    }

    public sealed class LiveOpsModule
    {
        // 256 KB. Every event a project has published, with payloads, in one document. A
        // project past this has more live events than a player could look at, and the call
        // below reports the size it needed rather than truncating.
        private const int MaxDocumentBytes = 256 * 1024;

        private readonly PlayerSenseClient _client;
        private byte[] _buffer = new byte[8 * 1024];

        // Reused across calls, because a UI that asks every frame should not allocate a list
        // every frame.
        private readonly List<LiveOpsEvent> _active = new();
        private readonly List<LiveOpsEvent> _upcoming = new();

        internal LiveOpsModule(PlayerSenseClient client) => _client = client;

        /// <summary>Now, in the server's clock. Zero until the server has answered.</summary>
        /// <remarks>
        /// The only clock a window means anything against. Comparing an event's timestamps
        /// with <c>DateTime.UtcNow</c> works right up until a player changes their device
        /// clock, which is exactly the player who would.
        /// </remarks>
        public long ServerTimeMs { get; private set; }

        /// <summary>Whether the server has answered in this session.</summary>
        /// <remarks>
        /// Until it has, <see cref="Active"/> is empty even if a document was read from disk.
        /// A window needs a clock somebody has checked, and a persisted document has one from
        /// whenever it was last fetched with no trustworthy way to say how long ago that was.
        /// </remarks>
        public bool Ready { get; private set; }

        /// <summary>The events running right now, highest priority first.</summary>
        /// <remarks>
        /// The returned list is reused between calls: copy it if you intend to hold it past
        /// the current frame. Reading it is a native call and a small parse, so call it when a
        /// screen opens rather than in Update.
        /// </remarks>
        public IReadOnlyList<LiveOpsEvent> Active => Read(_active, active: true);

        /// <summary>The events starting soon, soonest first.</summary>
        public IReadOnlyList<LiveOpsEvent> Upcoming => Read(_upcoming, active: false);

        /// <summary>Whether one particular event is running.</summary>
        public bool IsActive(string key)
        {
            if (string.IsNullOrEmpty(key)) return false;
            foreach (var running in Active)
                if (running.Key == key)
                    return true;
            return false;
        }

        /// <summary>Finds one running event by key.</summary>
        public bool TryGet(string key, out LiveOpsEvent found)
        {
            found = default;
            if (string.IsNullOrEmpty(key)) return false;
            foreach (var running in Active)
            {
                if (running.Key != key) continue;
                found = running;
                return true;
            }
            return false;
        }

        /// <summary>How long is left of an event, or <see cref="TimeSpan.Zero"/> once it is over.</summary>
        /// <remarks>What a countdown reads, measured against the server's clock rather than the device's.</remarks>
        public TimeSpan Remaining(LiveOpsEvent running)
        {
            long left = running.EndsAtMs - ServerTimeMs;
            return left <= 0 ? TimeSpan.Zero : TimeSpan.FromMilliseconds(left);
        }

        /// <summary>How long until an upcoming event starts.</summary>
        public TimeSpan StartsIn(LiveOpsEvent upcoming)
        {
            long until = upcoming.StartsAtMs - ServerTimeMs;
            return until <= 0 ? TimeSpan.Zero : TimeSpan.FromMilliseconds(until);
        }

        /// <summary>Asks the server for a fresh answer now. See <see cref="FlagsModule.Refresh"/>.</summary>
        public void Refresh() => Native.ps_client_refresh_config(_client.Handle);

        private List<LiveOpsEvent> Read(List<LiveOpsEvent> into, bool active)
        {
            into.Clear();

            string json = Fetch(active);
            if (json == null) return into;

            Document document;
            try
            {
                document = JsonUtility.FromJson<Document>(json);
            }
            catch (Exception error)
            {
                // A parse failure here is a bug in this SDK rather than anything a game can
                // act on, so it is logged once as itself and the list comes back empty. A
                // game showing no events is recoverable; one throwing out of a property
                // getter is not.
                Debug.LogWarning($"[PlayerSense] could not read the LiveOps document: {error.Message}");
                return into;
            }

            ServerTimeMs = document.server_time_ms;
            Ready = document.anchored;

            if (document.events == null) return into;
            foreach (var wire in document.events)
            {
                into.Add(new LiveOpsEvent
                {
                    Key = wire.key,
                    Kind = wire.kind,
                    Priority = wire.priority,
                    StartsAtMs = wire.starts_at_ms,
                    EndsAtMs = wire.ends_at_ms,
                    NextStartsAtMs = wire.next_starts_at_ms,
                    Payload = wire.payload,
                });
            }
            return into;
        }

        private unsafe string Fetch(bool active)
        {
            for (int attempt = 0; attempt < 2; attempt++)
            {
                long len;
                int status;
                fixed (byte* buffer = _buffer)
                {
                    status = active
                        ? Native.ps_client_liveops_active(_client.Handle, buffer, _buffer.Length, out len)
                        : Native.ps_client_liveops_upcoming(_client.Handle, buffer, _buffer.Length, out len);
                }

                if (status == Native.Ok)
                    return len == 0 ? null : System.Text.Encoding.UTF8.GetString(_buffer, 0, (int)len);

                // Grow once to exactly what was asked for, then try again. A second failure
                // is not a sizing problem and is not retried forever.
                if (status != Native.ErrBufferTooSmall || len <= 0 || len > MaxDocumentBytes)
                    return null;
                _buffer = new byte[len];
            }
            return null;
        }

        // The wire shape, named as it arrives so JsonUtility binds it without attributes.
        // Private because it is this file's business: LiveOpsEvent above is the type a game
        // sees, and the two are free to diverge.
        [Serializable]
        private struct Document
        {
            public bool anchored;
            public int version;
            public long server_time_ms;
            public WireEvent[] events;
        }

        [Serializable]
        private struct WireEvent
        {
            public string key;
            public string kind;
            public int priority;
            public long starts_at_ms;
            public long ends_at_ms;
            public long next_starts_at_ms;
            public string payload;
        }
    }
}

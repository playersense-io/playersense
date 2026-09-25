// Encoding strings for the native boundary without allocating per call.
//
// PsStr is a pointer and a length into UTF-8 bytes, and C# strings are UTF-16, so every call
// has to encode. Doing that naively means a byte[] per string per event, which on a path the
// protocol says must never be felt by the game is exactly the wrong cost.
//
// So everything for one call is encoded into a single reusable buffer, and the caller pins it
// once. Offsets are recorded while encoding and turned into pointers inside the `fixed`.

using System;
using System.Text;

namespace PlayerSense.Core
{
    /// <summary>One reusable UTF-8 staging buffer, owned by a client and used one call at a time.</summary>
    internal sealed class Utf8Scratch
    {
        private byte[] _buffer = new byte[4096];
        private int _used;

        internal byte[] Buffer => _buffer;

        /// <summary>Starts a new call. Everything previously written becomes invalid.</summary>
        internal void Reset() => _used = 0;

        /// <summary>
        /// Encodes a string and returns where it landed. A null or empty string is recorded as
        /// absent, which is how the ABI spells <c>None</c>.
        /// </summary>
        internal Span Write(string value) => Write(value, int.MaxValue);

        /// <summary>
        /// Encodes at most <paramref name="maxChars"/> characters of a string: a clip that costs
        /// no Substring, and so no garbage.
        /// </summary>
        internal Span Write(string value, int maxChars)
        {
            if (string.IsNullOrEmpty(value)) return Span.Absent;

            int count = Math.Min(value.Length, maxChars);
            // Never split a surrogate pair: the half left over would not be UTF-8.
            if (count < value.Length && count > 0 && char.IsHighSurrogate(value[count - 1])) count--;
            int worst = Encoding.UTF8.GetMaxByteCount(count);
            EnsureRoom(worst);

            int written = Encoding.UTF8.GetBytes(value, 0, count, _buffer, _used);
            var span = new Span(_used, written);
            _used += written;
            return span;
        }

        private void EnsureRoom(int needed)
        {
            if (_used + needed <= _buffer.Length) return;

            // Grown, not rented: a client's scratch buffer settles at the size of its largest
            // batch within a few events and then never allocates again.
            int size = _buffer.Length;
            while (size < _used + needed) size *= 2;
            Array.Resize(ref _buffer, size);
        }

        /// <summary>Where one string landed, before the buffer is pinned.</summary>
        internal readonly struct Span
        {
            internal readonly int Offset;
            internal readonly int Length;
            internal readonly bool Present;

            internal Span(int offset, int length)
            {
                Offset = offset;
                Length = length;
                Present = true;
            }

            internal static Span Absent => default;

            /// <summary>Turns an offset into a pointer. Only valid inside the caller's <c>fixed</c>.</summary>
            internal unsafe Native.PsStr Resolve(byte* origin)
            {
                if (!Present) return Native.PsStr.Absent;
                return new Native.PsStr { Ptr = origin + Offset, Len = Length };
            }
        }
    }
}

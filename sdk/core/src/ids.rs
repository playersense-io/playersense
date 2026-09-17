//! Identifiers, built from the host's randomness.
//!
//! A v4 UUID, formatted, and nothing else. There is no dependency for this because the one
//! thing a UUID crate would bring — a source of randomness — is the thing that has to come
//! from the host on the platforms this core exists to reach.
//!
//! Note what is *not* here: event identifiers. The gateway derives those from the batch and
//! the event's position in it, so an SDK does not send them (ADR 0026). Sixteen bytes of
//! entropy an event is three quarters of a compressed event, and it buys nothing a derived
//! identifier does not.

use crate::host::Host;

/// A random v4 UUID in the canonical hyphenated form.
pub fn uuid_v4<H: Host + ?Sized>(host: &H) -> String {
    let mut bytes = [0u8; 16];
    host.fill_random(&mut bytes);
    // Version 4, RFC 4122 variant. Without these a value is sixteen random bytes that
    // happen to be printed like a UUID, and the gateway parses it either way — but a
    // library on the other end may not.
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    format(&bytes)
}

fn format(bytes: &[u8; 16]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(36);
    for (i, byte) in bytes.iter().enumerate() {
        if matches!(i, 4 | 6 | 8 | 10) {
            out.push('-');
        }
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testhost::TestHost;

    #[test]
    fn a_generated_id_is_a_v4_uuid() {
        let host = TestHost::new();
        let id = uuid_v4(&host);

        assert_eq!(id.len(), 36, "{id}");
        assert_eq!(id.as_bytes()[14], b'4', "version nibble: {id}");
        assert!(
            matches!(id.as_bytes()[19], b'8' | b'9' | b'a' | b'b'),
            "variant: {id}"
        );
        assert_eq!(id.matches('-').count(), 4, "{id}");
    }

    #[test]
    fn two_generated_ids_differ() {
        let host = TestHost::new();
        assert_ne!(uuid_v4(&host), uuid_v4(&host));
    }
}

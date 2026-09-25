//! Personal data taken out of log text before it leaves the device.
//!
//! A log line is free text a developer wrote months before anybody thought about what it might
//! print, and the commonest ways a crash reporter ends up holding personal data are all here:
//! an email address in an error message, a bearer token in a failed request, a card number in a
//! payment log, and the player's own name in a file path (`C:\Users\Emma\…`,
//! `/Users/emma/…`). Each is replaced with a marker that says what was there, so the line still
//! reads and still groups into the same issue.
//!
//! Hand-written scanners rather than a regular-expression engine: the `regex` crate would be
//! the largest thing in the binary, and this runs on every record on a phone. Each scanner is
//! one pass over the bytes, allocation only when something is replaced.
//!
//! What it does not do is guess. A name in the middle of a sentence cannot be told from a word,
//! and trying would mangle stack traces. The studio's own `before_send` hook, in the binding,
//! is for what only the studio knows.

/// Replaces what looks like personal data or a credential in `text`.
pub fn scrub(text: &str) -> String {
    scrub_owned(text.to_owned())
}

/// Scrubs a string it is given to keep. A clean string, which is nearly every string, comes
/// back as it went in: each scanner runs only when a byte-level check says it could match, so
/// the common case is a few scans of the text and no copy at all.
pub fn scrub_owned(mut text: String) -> String {
    if text.is_empty() {
        return text;
    }
    // Lowered once, so each check below is the standard library's substring search rather than
    // a case-insensitive walk per word.
    let lower = text.to_ascii_lowercase();
    if lower.contains("users") || lower.contains("/home/") {
        text = user_paths(&text);
    }
    if might_hold_secret(&text, &lower) {
        text = secrets(&text);
    }
    if text.contains('@') {
        text = emails(&text);
    }
    if text.bytes().filter(u8::is_ascii_digit).count() >= 13 {
        text = card_numbers(&text);
    }
    if text.bytes().filter(|&b| b == b'.').count() >= 3 {
        text = ip_addresses(&text);
    }
    text
}

/// Whether [`secrets`] could change anything: a JWT's header, a scheme word, or a separator
/// with one of the key words beside it somewhere. `lower` is `text` in lower case.
fn might_hold_secret(text: &str, lower: &str) -> bool {
    if text.contains("eyJ") || lower.contains("bearer") || lower.contains("basic") {
        return true;
    }
    (text.contains('=') || text.contains(':'))
        && ["pass", "secret", "token", "key", "auth", "session"]
            .iter()
            .any(|word| lower.contains(word))
}

/// `/Users/emma/…`, `/home/emma/…` and `C:\Users\Emma\…`: the account name is the player's.
fn user_paths(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let bytes = text.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        let rest = &text[i..];
        let matched = ["/Users/", "/home/", "\\Users\\", "/users/"]
            .iter()
            .find(|prefix| starts_with_ignore_case(rest, prefix));
        if let Some(prefix) = matched {
            out.push_str(&rest[..prefix.len()]);
            let after = &rest[prefix.len()..];
            let end = after
                .find(|c: char| c == '/' || c == '\\' || c.is_whitespace() || c == '"' || c == '\'')
                .unwrap_or(after.len());
            if end > 0 && !after[..end].eq_ignore_ascii_case("shared") {
                out.push_str("[user]");
            } else {
                out.push_str(&after[..end]);
            }
            i += prefix.len() + end;
            continue;
        }
        let ch = rest.chars().next().expect("in bounds");
        out.push(ch);
        i += ch.len_utf8();
    }
    out
}

fn starts_with_ignore_case(text: &str, prefix: &str) -> bool {
    text.len() >= prefix.len()
        && text.as_bytes()[..prefix.len()].eq_ignore_ascii_case(prefix.as_bytes())
}

/// Credentials: `Bearer …`, a JWT, and `password=…`-style pairs.
fn secrets(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while !rest.is_empty() {
        // A word at a time, keeping the separators, so a token is judged whole.
        let start = rest.find(|c: char| !is_separator(c)).unwrap_or(rest.len());
        out.push_str(&rest[..start]);
        rest = &rest[start..];
        if rest.is_empty() {
            break;
        }
        let end = rest.find(is_separator).unwrap_or(rest.len());
        let word = &rest[..end];
        rest = &rest[end..];

        if looks_like_jwt(word) {
            out.push_str("[token]");
        } else if let Some((key, _)) = sensitive_pair(word) {
            out.push_str(key);
            out.push_str("[redacted]");
        } else {
            out.push_str(word);
            if word.eq_ignore_ascii_case("bearer") || word.eq_ignore_ascii_case("basic") {
                // The credential is the next word.
                let gap = rest
                    .find(|c: char| !c.is_whitespace())
                    .unwrap_or(rest.len());
                if gap > 0 && gap < rest.len() {
                    out.push_str(&rest[..gap]);
                    rest = &rest[gap..];
                    let end = rest.find(is_separator).unwrap_or(rest.len());
                    out.push_str("[token]");
                    rest = &rest[end..];
                }
            }
        }
    }
    out
}

fn is_separator(c: char) -> bool {
    c.is_whitespace()
        || matches!(
            c,
            ',' | ';' | '"' | '\'' | '(' | ')' | '[' | ']' | '{' | '}' | '<' | '>' | '&' | '?'
        )
}

/// Three base64url parts separated by dots, the first of them a JSON header (`eyJ`).
fn looks_like_jwt(word: &str) -> bool {
    word.len() > 30
        && word.starts_with("eyJ")
        && word.matches('.').count() == 2
        && word
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'-' | b'_' | b'='))
}

/// `password=hunter2`, `api_key:abc`, `token=…`: the key is kept and the value goes.
fn sensitive_pair(word: &str) -> Option<(&str, &str)> {
    let at = word.find(['=', ':'])?;
    let (key, value) = (&word[..=at], &word[at + 1..]);
    if value.is_empty() {
        return None;
    }
    let lower = key.to_ascii_lowercase();
    const KEYS: [&str; 9] = [
        "password",
        "passwd",
        "secret",
        "token",
        "apikey",
        "api_key",
        "api-key",
        "authorization",
        "session",
    ];
    KEYS.iter()
        .any(|k| lower.contains(k))
        .then_some((key, value))
}

/// `local@domain.tld` → `[email]`.
fn emails(text: &str) -> String {
    if !text.contains('@') {
        return text.to_owned();
    }
    let bytes = text.as_bytes();
    let mut out = String::with_capacity(text.len());
    let mut copied = 0;
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != b'@' {
            i += 1;
            continue;
        }
        let local_start = {
            let mut s = i;
            while s > 0 && is_email_local(bytes[s - 1]) {
                s -= 1;
            }
            s
        };
        let domain_end = {
            let mut e = i + 1;
            while e < bytes.len()
                && (bytes[e].is_ascii_alphanumeric() || bytes[e] == b'-' || bytes[e] == b'.')
            {
                e += 1;
            }
            // A trailing dot ends a sentence, not a domain.
            while e > i + 1 && bytes[e - 1] == b'.' {
                e -= 1;
            }
            e
        };
        let domain = &text[i + 1..domain_end];
        let plausible = local_start < i
            && domain.contains('.')
            && domain
                .rsplit('.')
                .next()
                .is_some_and(|tld| tld.len() >= 2 && tld.bytes().all(|b| b.is_ascii_alphabetic()));
        if plausible && local_start >= copied {
            out.push_str(&text[copied..local_start]);
            out.push_str("[email]");
            copied = domain_end;
            i = domain_end;
        } else {
            i += 1;
        }
    }
    out.push_str(&text[copied..]);
    out
}

fn is_email_local(b: u8) -> bool {
    b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'%' | b'+' | b'-')
}

/// Runs of 13 to 19 digits (spaces and dashes allowed between them) that pass the Luhn check.
fn card_numbers(text: &str) -> String {
    let bytes = text.as_bytes();
    if bytes.iter().filter(|b| b.is_ascii_digit()).count() < 13 {
        return text.to_owned();
    }
    let mut out = String::with_capacity(text.len());
    let mut copied = 0;
    let mut i = 0;
    while i < bytes.len() {
        if !bytes[i].is_ascii_digit() || (i > 0 && bytes[i - 1].is_ascii_alphanumeric()) {
            i += 1;
            continue;
        }
        let mut digits = Vec::with_capacity(19);
        let mut j = i;
        let mut last_digit_end = i;
        while j < bytes.len() && digits.len() < 19 {
            if bytes[j].is_ascii_digit() {
                digits.push(bytes[j] - b'0');
                last_digit_end = j + 1;
            } else if !(bytes[j] == b' ' || bytes[j] == b'-') {
                break;
            }
            j += 1;
        }
        let bounded =
            last_digit_end >= bytes.len() || !bytes[last_digit_end].is_ascii_alphanumeric();
        if (13..=19).contains(&digits.len()) && bounded && luhn(&digits) {
            out.push_str(&text[copied..i]);
            out.push_str("[card]");
            copied = last_digit_end;
            i = last_digit_end;
        } else {
            i += 1;
        }
    }
    out.push_str(&text[copied..]);
    out
}

fn luhn(digits: &[u8]) -> bool {
    let sum: u32 = digits
        .iter()
        .rev()
        .enumerate()
        .map(|(n, &d)| {
            let d = u32::from(d);
            if n % 2 == 1 {
                let doubled = d * 2;
                if doubled > 9 { doubled - 9 } else { doubled }
            } else {
                d
            }
        })
        .sum();
    sum.is_multiple_of(10)
}

/// Dotted IPv4 addresses. The player's address is personal data, and a failed request logs it
/// more often than anything else here. Versions (`1.4.2`, `2.0.1.3` with a component over 255)
/// are left alone by requiring four parts that are all bytes.
fn ip_addresses(text: &str) -> String {
    if text.matches('.').count() < 3 {
        return text.to_owned();
    }
    let bytes = text.as_bytes();
    let mut out = String::with_capacity(text.len());
    let mut copied = 0;
    let mut i = 0;
    while i < bytes.len() {
        if !bytes[i].is_ascii_digit()
            || (i > 0 && (bytes[i - 1].is_ascii_alphanumeric() || bytes[i - 1] == b'.'))
        {
            i += 1;
            continue;
        }
        let mut j = i;
        let mut parts = 0;
        let mut ok = true;
        while parts < 4 {
            let start = j;
            while j < bytes.len() && bytes[j].is_ascii_digit() && j - start < 3 {
                j += 1;
            }
            if j == start || text[start..j].parse::<u16>().map_or(true, |n| n > 255) {
                ok = false;
                break;
            }
            parts += 1;
            if parts < 4 {
                if j < bytes.len() && bytes[j] == b'.' {
                    j += 1;
                } else {
                    ok = false;
                    break;
                }
            }
        }
        let bounded = j >= bytes.len() || !(bytes[j].is_ascii_alphanumeric() || bytes[j] == b'.');
        if ok && parts == 4 && bounded {
            out.push_str(&text[copied..i]);
            out.push_str("[ip]");
            copied = j;
            i = j;
        } else {
            i += 1;
        }
    }
    out.push_str(&text[copied..]);
    out
}

#[cfg(test)]
mod tests {
    use super::scrub;

    #[test]
    fn personal_data_is_replaced_and_the_line_still_reads() {
        assert_eq!(
            scrub("Login failed for emma.stone+test@example.co.uk."),
            "Login failed for [email]."
        );
        assert_eq!(
            scrub("Loading C:\\Users\\Emma\\AppData\\save.dat"),
            "Loading C:\\Users\\[user]\\AppData\\save.dat"
        );
        assert_eq!(
            scrub("at /Users/emma/game/Build/Player.cs:42"),
            "at /Users/[user]/game/Build/Player.cs:42"
        );
        assert_eq!(
            scrub("GET /v1 Authorization: Bearer abc.def-123 failed"),
            "GET /v1 Authorization: Bearer [token] failed"
        );
        assert_eq!(
            scrub("retry password=hunter2 now"),
            "retry password=[redacted] now"
        );
        assert_eq!(
            scrub("paid with 4242 4242 4242 4242 ok"),
            "paid with [card] ok"
        );
        assert_eq!(
            scrub("connect to 192.168.1.20:443 timed out"),
            "connect to [ip]:443 timed out"
        );
        assert_eq!(
            scrub("token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijk expired"),
            "token [token] expired"
        );
    }

    #[test]
    fn what_is_not_personal_is_left_alone() {
        for text in [
            "NullReferenceException: Object reference not set to an instance of an object",
            "Loaded scene Arena_01 in 1.25s",
            "version 1.4.2 build 1234567890123",
            "order GPA.3372-0015-5540-12345 refused",
            "UnityEngine.Debug:LogError (object) (at Assets/Scripts/Shop.cs:88)",
            "a@b is not an address",
            "",
        ] {
            assert_eq!(scrub(text), text, "{text:?} was changed");
        }
    }

    #[test]
    fn a_long_non_ascii_line_does_not_panic() {
        let line = "Ünïcode 🎮 ".repeat(500) + "/Users/ç/x";
        assert!(scrub(&line).ends_with("/Users/[user]/x"));
    }
}

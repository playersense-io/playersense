//! Formatting an instant the way the envelope wants it.
//!
//! RFC 3339 with milliseconds, in UTC, hand-rolled rather than pulled in. A date library
//! would bring a time-zone database this has no use for — every timestamp on the wire is
//! UTC by contract (§17) — onto platforms where every kilobyte is argued over.

/// Formats milliseconds since the Unix epoch as `2026-09-17T10:19:02.000Z`.
pub fn rfc3339_ms(epoch_ms: i64) -> String {
    // Floored division, so an instant before the epoch does not round towards zero and land
    // on the wrong day. A device clock set to 1969 is not hypothetical; it is what an
    // uninitialised real-time clock reads.
    let days = epoch_ms.div_euclid(86_400_000);
    let millis_of_day = epoch_ms.rem_euclid(86_400_000);

    let (year, month, day) = civil_from_days(days);
    let (hour, minute, second, milli) = (
        millis_of_day / 3_600_000,
        (millis_of_day / 60_000) % 60,
        (millis_of_day / 1_000) % 60,
        millis_of_day % 1_000,
    );

    format!("{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}.{milli:03}Z")
}

/// Howard Hinnant's `civil_from_days`, which is correct for the whole proleptic Gregorian
/// calendar and has no branches for leap years.
fn civil_from_days(days: i64) -> (i64, u32, u32) {
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1_460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (if m <= 2 { y + 1 } else { y }, m as u32, d as u32)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats_a_known_instant() {
        assert_eq!(rfc3339_ms(0), "1970-01-01T00:00:00.000Z");
        assert_eq!(rfc3339_ms(1_789_641_542_123), "2026-09-17T10:39:02.123Z");
    }

    #[test]
    fn a_leap_day_is_a_leap_day() {
        // 2024-02-29T12:00:00Z
        assert_eq!(rfc3339_ms(1_709_208_000_000), "2024-02-29T12:00:00.000Z");
    }

    #[test]
    fn an_instant_before_the_epoch_lands_on_the_right_day() {
        // A device whose real-time clock never initialised. The wrong rounding here puts
        // it on 1970-01-01, which looks plausible and is a day out.
        assert_eq!(rfc3339_ms(-1), "1969-12-31T23:59:59.999Z");
    }
}

/// One hour in milliseconds.
pub const HOUR_MS: i64 = 3_600_000;

/// Parses an RFC 3339 UTC timestamp as Claude Code writes it into Unix
/// milliseconds. Any other offset, or a malformed string, is None.
pub fn parse_utc_ms(s: &str) -> Option<i64> {
    let s = s.strip_suffix('Z').or_else(|| s.strip_suffix("+00:00"))?;
    let (date, time) = s.split_once('T')?;
    let (year, month, day) = parse_date(date)?;
    let (clock, fraction) = match time.split_once('.') {
        Some((clock, fraction)) => (clock, fraction_ms(fraction)?),
        None => (time, 0),
    };
    let (hour, minute, second) = parse_clock(clock)?;
    let days = days_from_civil(year, month, day);
    let seconds = days * 86_400 + hour * 3_600 + minute * 60 + second;
    Some(seconds * 1_000 + fraction)
}

/// The start of the UTC hour containing ms.
pub fn hour_floor(ms: i64) -> i64 {
    ms.div_euclid(HOUR_MS) * HOUR_MS
}

fn parse_date(date: &str) -> Option<(i64, i64, i64)> {
    let mut parts = date.split('-');
    let year = number(parts.next()?)?;
    let month = number(parts.next()?)?;
    let day = number(parts.next()?)?;
    if parts.next().is_some() || !(1000..=9999).contains(&year) {
        return None;
    }
    if !(1..=12).contains(&month) || !(1..=days_in_month(year, month)).contains(&day) {
        return None;
    }
    Some((year, month, day))
}

fn parse_clock(clock: &str) -> Option<(i64, i64, i64)> {
    let mut parts = clock.split(':');
    let hour = number(parts.next()?)?;
    let minute = number(parts.next()?)?;
    let second = number(parts.next()?)?;
    if parts.next().is_some() || hour > 23 || minute > 59 || second > 59 {
        return None;
    }
    Some((hour, minute, second))
}

/// The first three digits of a fraction of a second, as milliseconds.
fn fraction_ms(fraction: &str) -> Option<i64> {
    if fraction.is_empty() || !fraction.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let kept = &fraction[..fraction.len().min(3)];
    format!("{kept:0<3}").parse().ok()
}

fn number(digits: &str) -> Option<i64> {
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    digits.parse().ok()
}

fn days_in_month(year: i64, month: i64) -> i64 {
    match month {
        2 if is_leap_year(year) => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

fn is_leap_year(year: i64) -> bool {
    (year % 4 == 0 && year % 100 != 0) || year % 400 == 0
}

/// Days since 1970-01-01 (Howard Hinnant's days_from_civil).
fn days_from_civil(year: i64, month: i64, day: i64) -> i64 {
    let y = if month <= 2 { year - 1 } else { year };
    let era = y.div_euclid(400);
    let year_of_era = y - era * 400;
    let month_from_march = (month + 9) % 12;
    let day_of_year = (153 * month_from_march + 2) / 5 + day - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    era * 146_097 + day_of_era - 719_468
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn whole_second_timestamp_parses() {
        assert_eq!(parse_utc_ms("1970-01-01T00:00:01Z"), Some(1_000));
    }

    #[test]
    fn one_digit_fraction_is_tenths_of_a_second() {
        assert_eq!(parse_utc_ms("1970-01-01T00:00:00.1Z"), Some(100));
    }

    #[test]
    fn three_digit_fraction_is_milliseconds() {
        assert_eq!(
            parse_utc_ms("2026-11-12T20:00:01.123Z"),
            Some(1_794_513_601_123)
        );
    }

    #[test]
    fn longer_fraction_keeps_three_digits() {
        assert_eq!(parse_utc_ms("1970-01-01T00:00:00.123456Z"), Some(123));
    }

    #[test]
    fn explicit_zero_offset_parses_like_z() {
        assert_eq!(
            parse_utc_ms("2026-11-12T20:00:01+00:00"),
            parse_utc_ms("2026-11-12T20:00:01Z")
        );
    }

    #[test]
    fn epoch_is_zero() {
        assert_eq!(parse_utc_ms("1970-01-01T00:00:00Z"), Some(0));
    }

    #[test]
    fn leap_day_is_counted() {
        assert_eq!(
            parse_utc_ms("2024-02-29T12:00:00Z"),
            Some(1_709_208_000_000)
        );
    }

    #[test]
    fn non_utc_offset_is_rejected() {
        assert_eq!(parse_utc_ms("2026-11-12T20:00:01+02:00"), None);
    }

    #[test]
    fn missing_zone_designator_is_rejected() {
        assert_eq!(parse_utc_ms("2026-11-12T20:00:01"), None);
    }

    #[test]
    fn garbage_is_rejected() {
        assert_eq!(parse_utc_ms("yesterday afternoon"), None);
    }

    #[test]
    fn impossible_date_is_rejected() {
        assert_eq!(parse_utc_ms("2026-02-30T00:00:00Z"), None);
    }

    #[test]
    fn hour_floor_drops_minutes() {
        assert_eq!(hour_floor(1_794_513_601_123), 1_794_513_600_000);
    }

    #[test]
    fn hour_floor_rounds_negative_times_down() {
        assert_eq!(hour_floor(-1), -HOUR_MS);
    }
}

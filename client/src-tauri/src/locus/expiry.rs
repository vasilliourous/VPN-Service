//! Subscription expiry: parsing what the hub sends, and describing it to a student.
//!
//! # Why this is its own module
//!
//! The hub stores `expires_at` as a PocketBase date string and hands it to us in
//! three different places (`/api/activate`, `/api/code-lookup`, `/api/heartbeat`)
//! without reformatting it. Every one of those is a string we have to understand,
//! and the previous code simply carried the raw value to the UI and displayed
//! nothing — which is why a subscription's end date was invisible to the person
//! paying for it.
//!
//! Two rules, both learned from the retired client's support load:
//!
//! 1. **Never invent a date.** If the value is absent, empty, or unparseable, the
//!    answer is "we do not know", and the UI shows nothing. A guessed expiry is a
//!    false statement about someone's account that support then has to unpick.
//! 2. **Phase of life matters more than the date.** A student does not want
//!    `2027-03-14 00:00:00.000Z`; they want "expires in 12 days". The raw date is
//!    available for the row's tooltip, but the sentence is the product.

use chrono::{DateTime, NaiveDateTime, Utc};

/// How close to expiry earns a warning, in days.
///
/// Seven days: long enough that a student can act (contact the reseller, get a
/// new code) and short enough that it does not nag for a month. The retired
/// client's complaints were about *surprise* lapses, and a week is the shortest
/// window in which the middleman can realistically respond.
pub const EXPIRY_WARNING_DAYS: i64 = 7;

/// A subscription's end, normalised for display.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Expiry {
    /// The hub did not tell us. Render nothing.
    Unknown,
    /// The date has passed.
    Lapsed,
    /// Still valid, with whole days remaining (0 = today).
    InDays { days: i64, expires: DateTime<Utc> },
}

/// Parses the date formats PocketBase and our hooks can produce.
///
/// Deliberately permissive, and deliberately NOT a general date parser: it
/// accepts the shapes we have actually seen, and returns `None` for everything
/// else rather than guessing. The mirrored Go/JS helper on the hub accepts the
/// same set (see `parsePBDate` in the pb_hooks), so the two do not drift.
#[must_use]
pub fn parse(raw: Option<&str>) -> Expiry {
    let Some(text) = raw.map(str::trim).filter(|s| !s.is_empty()) else {
        return Expiry::Unknown;
    };

    // PocketBase's canonical form is RFC3339-ish with a space instead of `T`
    // (`2027-09-19 00:00:00.000Z`), which `DateTime::parse_from_rfc3339` refuses.
    // Try the strict form first, then the space-separated variants.
    //
    // The fallbacks use `NaiveDateTime`, so the result is interpreted as UTC —
    // which is what the hub means by these values. `.and_utc()` is explicit
    // rather than relying on the local timezone, because a client in NZ reading
    // a date as local would see an expiry a day out from the admin console's.
    let parsed: Option<DateTime<Utc>> = DateTime::parse_from_rfc3339(text)
        .map(|dt| dt.with_timezone(&Utc))
        .ok()
        .or_else(|| {
            const FORMATS: [&str; 4] = [
                "%Y-%m-%d %H:%M:%S%.fZ",
                "%Y-%m-%d %H:%M:%S%.f",
                "%Y-%m-%d %H:%M:%S",
                "%Y-%m-%d",
            ];
            FORMATS
                .iter()
                .find_map(|fmt| NaiveDateTime::parse_from_str(text, fmt).ok())
                .map(|naive| naive.and_utc())
        });

    // A date-only value (`2027-09-19`) parses as midnight UTC via the last fmt
    // above, which is the intended reading: the hub stores a day boundary.
    let Some(expires) = parsed else {
        return Expiry::Unknown;
    };

    let now = Utc::now();
    if expires <= now {
        return Expiry::Lapsed;
    }

    // Whole days remaining, rounded UP.
    //
    // Rounding up rather than truncating so "expires tomorrow" reads as 1 day,
    // not 0 — a 0 that means "less than 24 hours" would be read as "expires
    // today", and the two are different problems for the student.
    let remaining = expires - now;
    let days = (remaining.num_hours() as f64 / 24.0).ceil() as i64;
    Expiry::InDays { days, expires }
}

impl Expiry {
    /// Whether this should be shown as a warning colour.
    #[must_use]
    pub const fn is_urgent(&self) -> bool {
        matches!(self, Self::InDays { days, .. } if *days <= EXPIRY_WARNING_DAYS)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The exact shape the hub stores, which the strict RFC3339 parser rejects.
    ///
    /// This is the regression that matters: PocketBase writes
    /// `2027-09-19 00:00:00.000Z` (space, not `T`), and a naive
    /// `parse_from_rfc3339` returns `Err` for it — so every real expiry would
    /// have been reported as "unknown" while a hand-written test with a `T`
    /// passed. Pinned with the real value.
    #[test]
    fn parses_the_space_separated_form_pocketbase_writes() {
        let parsed = parse(Some("2030-09-19 00:00:00.000Z"));
        assert!(
            matches!(parsed, Expiry::InDays { .. }),
            "the hub's own format must parse, got {parsed:?}"
        );
    }

    /// Both separators work, because the two are the same instant.
    #[test]
    fn accepts_rfc3339_with_a_t_separator_too() {
        let t_form = parse(Some("2030-09-19T00:00:00Z"));
        let space_form = parse(Some("2030-09-19 00:00:00.000Z"));
        assert!(matches!(t_form, Expiry::InDays { .. }));
        assert!(matches!(space_form, Expiry::InDays { .. }));
    }

    /// Absent, empty and whitespace all mean "we do not know".
    ///
    /// The UI renders nothing in this case, so any of these leaking through as
    /// `Lapsed` would show a paying student "expired".
    #[test]
    fn absent_or_blank_is_unknown_not_expired() {
        assert_eq!(parse(None), Expiry::Unknown);
        assert_eq!(parse(Some("")), Expiry::Unknown);
        assert_eq!(parse(Some("   ")), Expiry::Unknown);
    }

    /// Garbage must not be read as a date, and must not be read as expired.
    #[test]
    fn unparseable_text_is_unknown() {
        assert_eq!(parse(Some("not a date")), Expiry::Unknown);
        assert_eq!(parse(Some("0000-00-00")), Expiry::Unknown);
    }

    /// A past date is lapsed, and says so.
    #[test]
    fn a_past_date_is_lapsed() {
        assert_eq!(parse(Some("2020-01-01 00:00:00.000Z")), Expiry::Lapsed);
    }

    /// Days remaining rounds UP, so a partial day is not reported as zero.
    ///
    /// "0 days" reads as "today" when it actually means "in a few hours", and
    /// those are different situations for someone deciding whether to renew now.
    #[test]
    fn a_partial_day_rounds_up_to_one() {
        let soon = Utc::now() + chrono::Duration::hours(2);
        let text = soon.format("%Y-%m-%d %H:%M:%S%.fZ").to_string();
        match parse(Some(&text)) {
            Expiry::InDays { days, .. } => assert_eq!(days, 1, "2 hours away must read as 1 day"),
            other => panic!("expected InDays, got {other:?}"),
        }
    }

    /// A comfortably future date is neither urgent nor unknown.
    #[test]
    fn a_distant_expiry_is_not_urgent() {
        let far = Utc::now() + chrono::Duration::days(60);
        let text = far.format("%Y-%m-%d %H:%M:%S%.fZ").to_string();
        let parsed = parse(Some(&text));
        match parsed {
            Expiry::InDays { days, .. } => {
                assert!(days > EXPIRY_WARNING_DAYS, "60 days must exceed the warning window");
                assert!(!parsed.is_urgent());
            }
            other => panic!("expected InDays, got {other:?}"),
        }
    }

    /// Exactly at the window, and inside it, are urgent.
    ///
    /// Pinned at the boundary because an off-by-one here is the difference
    /// between warning a student and not warning them at all.
    #[test]
    fn the_warning_window_includes_its_own_boundary() {
        assert!(Expiry::InDays {
            days: EXPIRY_WARNING_DAYS,
            expires: Utc::now(),
        }
        .is_urgent());
        assert!(Expiry::InDays {
            days: EXPIRY_WARNING_DAYS + 1,
            expires: Utc::now(),
        }
        .is_urgent()
        == false);
    }

    /// Unknown and lapsed are never "urgent" — they are already resolved states.
    #[test]
    fn only_a_known_future_expiry_can_be_urgent() {
        assert!(!Expiry::Unknown.is_urgent());
        assert!(!Expiry::Lapsed.is_urgent());
    }
}

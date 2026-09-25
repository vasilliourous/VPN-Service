//! Version comparison for the update gate — and why it is hand-written.
//!
//! The retired Wails client learned this the hard way. Its update gate used to
//! decide "is there an update?" with a bare string comparison
//! (`info.Version == a.version`), which is only safe while the hub never
//! advertises anything but the newest build. If `update_config` is ever left
//! pointing at an older release — a rollback, a typo, a stale row after a bad
//! deploy — every client would download it and **downgrade itself**. There is
//! no server-driven downgrade in this system, so a mistaken advertisement is the
//! only way that can happen; refusing anything that is not strictly newer makes
//! the whole class of mistake harmless.
//!
//! A semver crate is the obvious alternative and was deliberately not used. The
//! version strings here come from git tags and are controlled by us: `1.2.3`,
//! `v1.2.3`, optionally `-rc1` / `-beta.2`. A strict semver parser rejects
//! inputs this project legitimately produces, and "the updater refuses a valid
//! version" is a worse failure than "the updater has its own 60 lines".
//!
//! The semantics below are ported from the old client's `updater/version.go`,
//! whose Go tests are the specification. They are re-implemented here rather
//! than re-derived, so a change in behaviour shows up as a test failure.

/// Compares two dotted version strings.
///
/// Returns `-1` if `a < b`, `0` if equal, `1` if `a > b`.
///
/// - A leading `v`/`V` is ignored, so `v1.2.3 == 1.2.3`.
/// - Numeric components compare **numerically**, so `1.10.0 > 1.9.0` — which is
///   precisely what string comparison gets wrong.
/// - Missing components are zero, so `1.2 == 1.2.0`.
/// - A release outranks a pre-release of the same version: `1.2.3 > 1.2.3-rc1`.
/// - Build metadata is **ignored**, so `1.0.0+build5 == 1.0.0`.
///
/// Never errors. Unparseable input degrades to a string comparison, so a
/// malformed version fails *closed* rather than panicking mid-update.
#[must_use]
pub fn compare(a: &str, b: &str) -> i32 {
    let (a_nums, a_pre) = split(a);
    let (b_nums, b_pre) = split(b);

    // Compare numeric segments, padding the shorter with zeros.
    let width = a_nums.len().max(b_nums.len());
    for i in 0..width {
        let av = a_nums.get(i).copied().unwrap_or(0);
        let bv = b_nums.get(i).copied().unwrap_or(0);
        if av != bv {
            return if av < bv { -1 } else { 1 };
        }
    }

    // Numeric parts are equal: a release beats a pre-release of the same version.
    match (a_pre.is_empty(), b_pre.is_empty()) {
        (true, true) => 0,
        (true, false) => 1, // 1.2.3 > 1.2.3-rc1
        (false, true) => -1,
        (false, false) => compare_prerelease(&a_pre, &b_pre),
    }
}

/// Whether `candidate` is **strictly** newer than `current`.
///
/// This is the gate the updater uses. An empty or unparseable candidate is
/// rejected: refusing to act is always safer than acting on a version we do not
/// understand.
#[must_use]
pub fn is_newer(candidate: &str, current: &str) -> bool {
    if candidate.trim().is_empty() || current.trim().is_empty() {
        return false;
    }
    compare(candidate, current) > 0
}

/// Splits `"v1.2.3-rc1+build"` into numeric segments `[1, 2, 3]` and `"rc1"`.
fn split(version: &str) -> (Vec<u64>, String) {
    let mut v = version.trim();
    v = v.strip_prefix(['v', 'V']).unwrap_or(v);

    // Build metadata is stripped FIRST, because semver says it is ignored for
    // ordering. Doing this after the pre-release split would misclassify the
    // metadata as a pre-release and rank `1.0.0+build5` BELOW `1.0.0` — a
    // release sorting below itself.
    let v = v.split('+').next().unwrap_or(v);

    // A pre-release starts at the first '-'.
    let (numeric_part, pre) = match v.split_once('-') {
        Some((head, tail)) => (head, tail.to_owned()),
        None => (v, String::new()),
    };

    let mut nums = Vec::new();
    let mut trailing = String::new();
    for part in numeric_part.split('.') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        // Tolerate a non-numeric segment (e.g. "1.2.x") by stopping the numeric
        // parse and folding the remainder into the pre-release, so the
        // comparison still has something to work with instead of discarding it.
        match part.parse::<u64>() {
            Ok(n) => nums.push(n),
            Err(_) => {
                if pre.is_empty() && trailing.is_empty() {
                    trailing = part.to_owned();
                }
                break;
            }
        }
    }

    let pre = if pre.is_empty() { trailing } else { pre };
    (nums, pre)
}

/// Splits an identifier into an alphabetic prefix and an optional trailing
/// number, so `rc10` and `rc9` compare as **numbers** rather than as strings.
///
/// Without this, a plain lexicographic compare reports `rc10 < rc9` — which
/// would make the tenth release candidate look older than the ninth, and a
/// client on `rc9` would refuse `rc10`.
fn peel(identifier: &str) -> (&str, Option<u64>) {
    let split_at = identifier
        .rfind(|c: char| !c.is_ascii_digit())
        .map_or(0, |index| index + 1);
    let (prefix, digits) = identifier.split_at(split_at);
    (prefix, digits.parse::<u64>().ok())
}

/// Compares two pre-release strings identifier by identifier.
///
/// - A purely numeric identifier ranks **below** an alphanumeric one, so
///   `1.0.0-1 < 1.0.0-alpha` (the semver rule).
/// - Alphanumeric identifiers compare by prefix first, then by trailing number,
///   so `rc10 > rc9`.
/// - A shorter pre-release is smaller when every shared part matches, so
///   `1.0.0-alpha < 1.0.0-alpha.1`.
fn compare_prerelease(a: &str, b: &str) -> i32 {
    let asplit: Vec<&str> = a.split(['.', '-']).filter(|s| !s.is_empty()).collect();
    let bsplit: Vec<&str> = b.split(['.', '-']).filter(|s| !s.is_empty()).collect();

    let width = asplit.len().max(bsplit.len());
    for i in 0..width {
        // A shorter pre-release is smaller when all shared parts match.
        let (Some(ai), Some(bi)) = (asplit.get(i), bsplit.get(i)) else {
            return if asplit.len() < bsplit.len() { -1 } else { 1 };
        };

        match (ai.parse::<u64>(), bi.parse::<u64>()) {
            // Both purely numeric: compare as numbers.
            (Ok(an), Ok(bn)) => {
                if an != bn {
                    return if an < bn { -1 } else { 1 };
                }
            }
            // Numeric ranks below alphanumeric.
            (Ok(_), Err(_)) => return -1,
            (Err(_), Ok(_)) => return 1,
            // Both alphanumeric: compare prefix, then the trailing number.
            (Err(_), Err(_)) => {
                let (a_prefix, a_num) = peel(ai);
                let (b_prefix, b_num) = peel(bi);
                if a_prefix != b_prefix {
                    return if a_prefix < b_prefix { -1 } else { 1 };
                }
                match (a_num, b_num) {
                    (Some(x), Some(y)) if x != y => return if x < y { -1 } else { 1 },
                    // A bare prefix ranks below the same prefix with a number,
                    // so "rc" < "rc1".
                    (Some(_), None) => return 1,
                    (None, Some(_)) => return -1,
                    _ => {}
                }
            }
        }
    }
    0
}

/// Explains why an advertised version is not actionable, for the UI.
///
/// Returning a *reason* rather than a bare `false` is deliberate: the retired
/// client's worst failure mode was a silent no-op, where an update was
/// advertised and ignored with nothing anywhere saying so. The caller is
/// expected to log and surface this.
#[must_use]
pub fn rejection_reason(candidate: &str, current: &str) -> Option<String> {
    if candidate.trim().is_empty() {
        return Some("the hub advertised an empty version".to_owned());
    }
    if candidate.contains(['/', '\\', ' ', '\t', '\n']) {
        return Some(format!("the hub advertised a malformed version {candidate:?}"));
    }
    if compare(candidate, current) <= 0 {
        return Some(format!(
            "the hub advertised {candidate}, which is not newer than the running {current}"
        ));
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The bug that motivated hand-writing this at all: as strings, "1.9.0" >
    /// "1.10.0". A client on 1.10.0 would have accepted 1.9.0 as an upgrade.
    #[test]
    fn numeric_segments_compare_numerically_not_lexically() {
        assert!(is_newer("1.10.0", "1.9.0"));
        assert!(!is_newer("1.9.0", "1.10.0"));
        assert_eq!(compare("1.10.0", "1.9.0"), 1);
    }

    #[test]
    fn a_leading_v_is_ignored() {
        assert_eq!(compare("v1.2.3", "1.2.3"), 0);
        assert_eq!(compare("V1.2.3", "v1.2.3"), 0);
        assert!(!is_newer("v1.2.3", "1.2.3"), "the same version is not newer");
    }

    #[test]
    fn missing_components_are_zero() {
        assert_eq!(compare("1.2", "1.2.0"), 0);
        assert_eq!(compare("1", "1.0.0"), 0);
        assert!(is_newer("1.2.1", "1.2"));
    }

    /// A release must outrank its own pre-release, or every stable release
    /// would look older than the release candidate that preceded it.
    #[test]
    fn a_release_outranks_its_prerelease() {
        assert!(is_newer("1.2.3", "1.2.3-rc1"));
        assert!(!is_newer("1.2.3-rc1", "1.2.3"));
        assert_eq!(compare("1.2.3", "1.2.3-rc1"), 1);
        assert_eq!(compare("1.2.3-rc1", "1.2.3"), -1);
    }

    /// Build metadata is ignored for ordering, so a rebuild of the same version
    /// is not an upgrade. The old client had a special case for promoting a
    /// `+build` to a stable release; that is handled by the caller, not here.
    #[test]
    fn build_metadata_is_ignored() {
        assert_eq!(compare("1.0.0+build5", "1.0.0"), 0);
        assert_eq!(compare("1.0.0", "1.0.0+build5"), 0);
        assert!(!is_newer("1.0.0+build5", "1.0.0"));
    }

    /// Metadata must be stripped before the pre-release split, or a
    /// build-tagged release sorts below the plain one.
    #[test]
    fn build_metadata_does_not_become_a_prerelease() {
        assert_eq!(
            compare("1.0.0+build5", "1.0.0"),
            0,
            "1.0.0+build5 must equal 1.0.0, not rank below it"
        );
        assert_eq!(compare("1.0.0-rc1+build5", "1.0.0-rc1"), 0);
    }

    /// Numeric pre-release identifiers rank below alphanumeric ones, which is
    /// what makes rc numbering behave.
    #[test]
    fn prerelease_identifiers_order_correctly() {
        assert!(is_newer("1.0.0-rc2", "1.0.0-rc1"));
        assert!(is_newer("1.0.0-rc10", "1.0.0-rc9"), "rc10 > rc9 numerically");
        assert!(is_newer("1.0.0-beta", "1.0.0-1"), "alphanumeric ranks above numeric");
        assert!(is_newer("1.0.0-alpha.2", "1.0.0-alpha.1"));
        assert!(!is_newer("1.0.0-alpha.1", "1.0.0-alpha.2"));
    }

    /// A shorter pre-release is smaller when the shared parts match.
    #[test]
    fn a_shorter_prerelease_ranks_lower() {
        assert!(is_newer("1.0.0-alpha.1", "1.0.0-alpha"));
        assert!(!is_newer("1.0.0-alpha", "1.0.0-alpha.1"));
    }

    /// The updater must never act on an input it cannot parse. An empty or
    /// unparseable candidate is rejected rather than treated as newest.
    #[test]
    fn empty_and_unparseable_versions_are_rejected() {
        assert!(!is_newer("", "1.0.0"));
        assert!(!is_newer("   ", "1.0.0"));
        assert!(!is_newer("1.0.0", ""));
        assert!(
            rejection_reason("", "1.0.0").is_some(),
            "an empty advertised version must produce a reason"
        );
    }

    /// A version with a path separator or whitespace could become a filename.
    /// Rejected, with a reason.
    #[test]
    fn malformed_versions_are_rejected_with_a_reason() {
        for bad in ["../etc/passwd", "1.0.0 2.0.0", "1.0\n0"] {
            assert!(
                rejection_reason(bad, "1.0.0").is_some(),
                "{bad:?} must be rejected before it reaches a URL or a filename"
            );
        }
    }

    /// Equal and older versions must both be refused — the no-downgrade rule.
    #[test]
    fn equal_and_older_versions_are_not_newer() {
        assert!(!is_newer("2.0.0", "2.0.0"));
        assert!(!is_newer("1.9.9", "2.0.0"));
        assert!(rejection_reason("2.0.0", "2.0.0").is_some());
        assert!(rejection_reason("1.9.9", "2.0.0").is_some());
        assert!(rejection_reason("2.0.1", "2.0.0").is_none());
    }

    /// The reason must name both versions: a support log that says "ignored an
    /// update" without saying which is unactionable.
    #[test]
    fn the_rejection_reason_names_both_versions() {
        let reason = rejection_reason("1.9.0", "1.10.0").expect("must be rejected");
        assert!(reason.contains("1.9.0"), "must name the advertised version");
        assert!(reason.contains("1.10.0"), "must name the running version");
    }

    /// Real versions from this project's history, compared as a sanity sweep.
    #[test]
    fn real_release_versions_order_as_expected() {
        let ascending = [
            "2.2.0", "2.2.1", "2.2.2", "2.2.3", "2.2.4", "2.2.6", "2.2.7", "2.2.8", "2.3.0",
        ];
        for window in ascending.windows(2) {
            let (older, newer) = (window[0], window[1]);
            assert!(is_newer(newer, older), "{newer} should be newer than {older}");
            assert!(!is_newer(older, newer), "{older} must not be newer than {newer}");
        }
    }
}

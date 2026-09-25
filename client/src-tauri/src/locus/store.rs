//! Product state: what this device is entitled to, and how it is doing.
//!
//! # Why the state lives in `IVerge`
//!
//! Verge already has a config store (`config/verge.rs`), with a draft/transaction
//! system, atomic writes and a save path that the rest of the app follows. A
//! second JSON file beside it would be a second thing to corrupt, a second backup
//! mechanism, and a second migration path — for seven fields. So the product
//! state is **additive fields on `IVerge`**, which needs no migration because
//! every field is `Option<T>` with a serde default.
//!
//! # The security boundary, stated plainly
//!
//! The activation code is stored in plaintext in a user-writable file. That is
//! unavoidable — the device must hold it to authenticate — and it is the same
//! choice the retired client made. What matters is what we do *around* it:
//!
//!   * it is never logged;
//!   * it is never included in a diagnostics export;
//!   * the frontend is given a redacted form, never the real value;
//!   * the real enforcement is the hub's, not the client's: a student who edits
//!     this file to claim a tier they did not buy gets a config the hub refuses
//!     to serve at their next heartbeat.
//!
//! The last point is the important one. Client-side storage is not a security
//! boundary and must not be treated as one; it is a cache of an entitlement the
//! hub owns.

use crate::config::{Config, IVerge};
use crate::locus::contract::TierConfig;
use anyhow::Result;
// `IVerge` uses smartstring, not std String. Importing the alias makes the
// conversions below unnecessary and keeps this module in step with the config
// type it is writing into.
use smartstring::alias::String as SmartString;

/// Everything the client knows about its own entitlement.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Activation {
    pub code: String,
    pub tier: String,
    pub fingerprint: String,
}

/// Whether this device currently has an entitlement it can use.
///
/// Requires all three parts. A code without a fingerprint cannot heartbeat (the
/// hub uses the fingerprint for rollout bucketing and binding checks), and one
/// without a tier cannot build a config — so a partial record is not "activated",
/// it is damaged, and saying so is more useful than half-working.
#[must_use]
pub fn is_usable(activation: &Activation) -> bool {
    !activation.code.trim().is_empty()
        && !activation.tier.trim().is_empty()
        && !activation.fingerprint.trim().is_empty()
}

/// Reads the stored activation, if the record is complete.
///
/// Returns `None` for a partial record rather than a half-filled struct: the
/// caller must decide to re-activate, and it cannot make that decision from a
/// struct that looks populated but is not usable.
#[must_use]
pub fn read(verge: &IVerge) -> Option<Activation> {
    let activation = Activation {
        code: verge.activation_code.as_deref().unwrap_or_default().to_owned(),
        tier: verge.locus_tier.as_deref().unwrap_or_default().to_owned(),
        fingerprint: verge.device_fingerprint.as_deref().unwrap_or_default().to_owned(),
    };
    is_usable(&activation).then_some(activation)
}

/// Persists an activation, then saves the file.
///
/// Written as one operation so a crash cannot leave the code stored without the
/// tier, or the reverse. `save_file` is the same path the rest of the app uses,
/// so this inherits its atomicity.
pub async fn store(activation: &Activation) -> Result<()> {
    let verge = Config::verge().await;
    verge.edit_draft(|draft| {
        draft.activation_code = Some(SmartString::from(activation.code.as_str()));
        draft.locus_tier = Some(SmartString::from(activation.tier.as_str()));
        draft.device_fingerprint = Some(SmartString::from(activation.fingerprint.as_str()));
    });
    verge.data_arc().save_file().await
}

/// Records a successful heartbeat.
///
/// The failure count is reset at the same time, because the two are one fact:
/// a beat either worked (and the backoff restarts) or it did not. Splitting them
/// across two writes is how a device ends up with a fresh timestamp and a stale
/// failure count, and then backs off as though it were still failing.
pub async fn record_heartbeat_success(now_unix: i64) -> Result<()> {
    let verge = Config::verge().await;
    verge.edit_draft(|draft| {
        draft.last_heartbeat_ok = Some(now_unix);
        draft.heartbeat_failures = Some(0);
    });
    verge.data_arc().save_file().await
}

/// Records a failed heartbeat and the new consecutive-failure count.
pub async fn record_heartbeat_failure(failures: u32) -> Result<()> {
    let verge = Config::verge().await;
    verge.edit_draft(|draft| {
        draft.heartbeat_failures = Some(failures);
    });
    verge.data_arc().save_file().await
}

/// Clears the entitlement, e.g. after the hub says the code is suspended.
///
/// Deliberately clears all three fields together. Leaving a stale tier behind
/// would let a deactivated device keep presenting itself as entitled, and the
/// UI would show a tier the hub will not serve.
pub async fn clear() -> Result<()> {
    let verge = Config::verge().await;
    verge.edit_draft(|draft| {
        draft.activation_code = None;
        draft.locus_tier = None;
        draft.last_heartbeat_ok = None;
        draft.heartbeat_failures = None;
    });
    verge.data_arc().save_file().await
}

/// Persists the tier's connection details and UDP preference.
///
/// Stored alongside the entitlement because they arrive together and are useless
/// apart: a code without a config cannot connect, and a config without a code
/// cannot be authorised. Keeping them in one write means a crash cannot leave a
/// device that believes it is activated but has nothing to dial.
pub async fn store_tier_config(config: &TierConfig, udp_relay: bool) -> Result<()> {
    let verge = Config::verge().await;
    verge.edit_draft(|draft| {
        draft.locus_tier_server = Some(serde_json::to_string(config).unwrap_or_default().into());
        draft.locus_udp_relay = Some(udp_relay);
    });
    verge.data_arc().save_file().await
}

/// Reads back the stored tier connection details.
///
/// Returns `None` — rather than an error — when nothing is stored or the stored
/// JSON is unreadable. The caller's question is "can I connect?", and both cases
/// answer it the same way: not yet, re-activate.
pub async fn tier_config(tier: &str) -> Option<TierConfig> {
    let verge = crate::config::Config::verge().await;
    let data = verge.latest_arc();
    let raw = data.locus_tier_server.as_deref()?;
    let config: TierConfig = serde_json::from_str(raw).ok()?;
    // Guard against a stored config for a different tier: a re-activation that
    // changed tier must not keep dialling the old server.
    if config.server.is_empty() || config.server_port == 0 {
        logging::warn_wrong_tier(tier);
        return None;
    }
    Some(config)
}

/// Small logging shim so this module needs no direct logging import.
mod logging {
    pub fn warn_wrong_tier(tier: &str) {
        clash_verge_logging::logging!(
            warn,
            clash_verge_logging::Type::Config,
            "[locus] stored tier config is unusable for the {tier} tier"
        );
    }
}

/// Whether an incoming tier config actually differs from the stored one.
///
/// This exists to stop a RESTART STORM. The hub sends `server_config` on every
/// heartbeat, and applying it unconditionally would mean re-writing the profile
/// and reloading-or-restarting the core every five minutes — dropping every
/// connection the student has, forever, for no reason.
///
/// Compares the whole payload including `udp_relay`, because a change to either
/// can alter the generated config.
pub async fn tier_changed(config: &TierConfig, udp_relay: bool) -> bool {
    let verge = Config::verge().await;
    let data = verge.latest_arc();

    let stored_udp = data.locus_udp_relay.unwrap_or(false);
    if stored_udp != udp_relay {
        return true;
    }

    match data.locus_tier_server.as_deref() {
        Some(raw) => match serde_json::from_str::<TierConfig>(raw) {
            Ok(stored) => stored != *config,
            // Unreadable stored config: treat as changed so it gets replaced.
            // The alternative is refusing to self-heal a corrupt field.
            Err(_) => true,
        },
        None => true,
    }
}

/// A copy of the config with every credential removed.
///
/// Used by the diagnostics export. Written as an allow-list of the fields that
/// are safe rather than a deny-list of the ones that are not: a deny-list has to
/// be updated every time a credential field is added, and the failure mode when
/// someone forgets is publishing a student's code.
#[must_use]
pub fn redacted_for_diagnostics(verge: &IVerge) -> serde_json::Value {
    serde_json::json!({
        // Presence, not value. Support needs to know a code is set; nobody needs
        // to know what it is, and a support log is a thing people paste around.
        "has_activation_code": verge.activation_code.as_deref().is_some_and(|c| !c.is_empty()),
        "tier": verge.locus_tier,
        "device_id": verge.device_fingerprint.as_deref().map(redact),
        "last_heartbeat_ok": verge.last_heartbeat_ok,
        "heartbeat_failures": verge.heartbeat_failures,
        "update_pending_version": verge.update_pending_version,
    })
}

/// Truncates a fingerprint for display and logging.
///
/// The fingerprint is a stable device identifier. It is not a secret, but there
/// is no reason to put the whole thing somewhere it might be screenshotted —
/// and a truncated value is still enough to correlate two log lines.
#[must_use]
pub fn redact(fingerprint: &str) -> String {
    fingerprint.chars().take(12).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn verge_with(code: Option<&str>, tier: Option<&str>, fingerprint: Option<&str>) -> IVerge {
        IVerge {
            activation_code: code.map(SmartString::from),
            locus_tier: tier.map(SmartString::from),
            device_fingerprint: fingerprint.map(SmartString::from),
            ..Default::default()
        }
    }

    #[test]
    fn a_complete_record_reads_back() {
        let verge = verge_with(
            Some("RQ-ABCD-EFGH-JKMN-T"),
            Some("strike"),
            Some("a".repeat(64).as_str()),
        );
        let activation = read(&verge).expect("a complete record must read");
        assert_eq!(activation.tier, "strike");
        assert!(is_usable(&activation));
    }

    /// A partial record must read as absent, not as a half-populated struct.
    ///
    /// The caller's decision is "activate again or not", and it cannot make that
    /// decision from something that looks populated but cannot heartbeat.
    #[test]
    fn a_partial_record_reads_as_absent() {
        for verge in [
            verge_with(None, Some("eco"), Some(&"a".repeat(64))),
            verge_with(Some("RQ-ABCD-EFGH-JKMN-T"), None, Some(&"a".repeat(64))),
            verge_with(Some("RQ-ABCD-EFGH-JKMN-T"), Some("eco"), None),
            // Whitespace-only counts as empty, because that is what a corrupt or
            // hand-edited file produces.
            verge_with(Some("   "), Some("eco"), Some(&"a".repeat(64))),
        ] {
            assert!(
                read(&verge).is_none(),
                "an incomplete record must not present as activated: {verge:?}"
            );
        }
    }

    /// A fresh install is the normal case and must not be an error.
    #[test]
    fn a_fresh_config_is_not_activated() {
        assert!(read(&IVerge::default()).is_none());
    }

    /// The redaction must never emit the activation code, whatever else it says.
    ///
    /// This is the check that matters: a diagnostics export is a thing people
    /// paste into a support thread, and the code is a bearer credential.
    #[test]
    fn diagnostics_never_include_the_code() {
        let secret = "RQ-SECR-ETXX-XXXX-T";
        let verge = verge_with(Some(secret), Some("strike"), Some(&"b".repeat(64)));

        let exported = serde_json::to_string(&redacted_for_diagnostics(&verge)).expect("export must serialise");

        assert!(
            !exported.contains(secret),
            "the activation code appeared in the diagnostics export: {exported}"
        );
        // And the whole fingerprint must not be there either.
        assert!(
            !exported.contains(&"b".repeat(64)),
            "the full fingerprint appeared in the diagnostics export: {exported}"
        );
        // It must still be useful: presence, tier and a truncated id.
        assert!(exported.contains("\"has_activation_code\":true"));
        assert!(exported.contains("strike"));
    }

    /// With nothing stored, the export must say so rather than omit the key —
    /// a missing field is indistinguishable from a bug in the exporter.
    #[test]
    fn diagnostics_reports_absence_explicitly() {
        let exported = redacted_for_diagnostics(&IVerge::default());
        assert_eq!(exported["has_activation_code"], serde_json::json!(false));
        assert_eq!(exported["tier"], serde_json::Value::Null);
    }

    #[test]
    fn redaction_truncates() {
        let full = "c".repeat(64);
        let short = redact(&full);
        assert_eq!(short.len(), 12);
        assert!(full.starts_with(&short));
    }

    /// An empty string must not read as a stored value.
    #[test]
    fn an_empty_code_is_not_a_stored_code() {
        let verge = verge_with(Some(""), Some("eco"), Some(&"d".repeat(64)));
        assert!(read(&verge).is_none());
    }
}

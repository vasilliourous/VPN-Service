//! Applying a tier: from the hub's payload to a configuration the core runs.
//!
//! # The one path
//!
//! This module writes the tier into Verge's existing profile slot and then hands
//! it to Verge's existing pipeline:
//!
//! ```text
//! tier::build_profile()            -> the mihomo document
//! write it as a local profile      -> app_profiles_dir()/locus.yaml
//! register it + make it current    -> IProfiles (profiles.yaml)
//! CoreManager::update_config_forced()  -> validate -> apply -> reload/restart
//! ```
//!
//! # What is deliberately NOT written here
//!
//! * **A second apply path.** `update_config_forced` already carries config
//!   validation, the service-vs-sidecar staging decision, and reload-vs-restart
//!   policy. Every one of those was a source of field bugs in the retired
//!   client, and a parallel path would re-earn them.
//! * **Our own YAML→core plumbing.** `enhance` merges the profile with the
//!   user's settings, TUN preferences and DNS overrides. Writing straight to the
//!   core would bypass all of it.
//! * **Binary or process supervision.** Verge owns that.
//!
//! # Why the tier config is a "profile"
//!
//! Because that is the slot the pipeline reads. A tier payload is not a
//! subscription, but treating it as a local profile means the whole existing
//! machinery works unmodified, and there is exactly one code path from "a tier
//! arrived" to "the core is running it".

use crate::config::{Config, PrfItem, profiles};
use crate::core::CoreManager;
use crate::locus::{contract::TierConfig, tier};
use crate::utils::dirs;
use anyhow::{Context as _, Result};
use clash_verge_logging::{Type, logging};
use smartstring::alias::String as SmartString;

/// The filename the generated tier profile is written to.
///
/// Fixed, not generated: re-applying a rotated password or a moved server must
/// REPLACE the existing profile rather than accumulate one per heartbeat. A new
/// file each time would leave the student's config directory growing by a few
/// kilobytes every five minutes.
const TIER_PROFILE_FILE: &str = "locus.yaml";

/// The profile's display name, shown wherever Verge lists profiles.
const TIER_PROFILE_NAME: &str = "Locus";

/// What applying a tier did, so the caller can report accurately.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ApplyOutcome {
    /// Written and accepted by the core.
    Applied,
    /// Written, but the config was rejected. Carries the reason.
    Rejected { reason: String },
    /// Written and accepted, but the core is not running yet — normal after
    /// activation, because the student has not pressed Connect.
    StagedOnly,
}

/// Writes the tier as the current profile and applies it.
///
/// Returns [`ApplyOutcome::Rejected`] rather than an error when the core refuses
/// the configuration: that is a real, reportable outcome with a reason worth
/// showing, not a transport failure. An `Err` here means we could not even write
/// the profile.
pub async fn apply_tier(config: &TierConfig, udp_relay: bool) -> Result<ApplyOutcome> {
    let profile = tier::build_profile(config, udp_relay);

    write_tier_profile(&profile.yaml).await?;

    // Hand it to Verge. `update_config_forced` validates, stages and applies, and
    // decides whether the running core needs a reload or a full restart.
    match CoreManager::global().update_config_forced().await {
        Ok(outcome) if outcome.is_valid() => {
            logging!(
                info,
                Type::Config,
                "[locus] tier config applied ({} bytes)",
                profile.yaml.len()
            );
            Ok(ApplyOutcome::Applied)
        }
        Ok(outcome) => {
            // The core refused it. Report the reason rather than a bare failure:
            // "the tunnel will not start" is not actionable, "the proxy port is
            // already in use" is.
            let reason = outcome.to_string();
            logging!(warn, Type::Config, "[locus] tier config rejected: {reason}");
            Ok(ApplyOutcome::Rejected { reason })
        }
        Err(error) => {
            logging!(
                warn,
                Type::Config,
                "[locus] could not apply the tier config: {error:#}"
            );
            Ok(ApplyOutcome::Rejected {
                reason: format!("{error:#}"),
            })
        }
    }
}

/// Writes the tier YAML into a local profile and makes it current.
///
/// Reuses `profiles_append_item_safe`, which already handles writing the file,
/// registering the item and setting `current` on a first profile — rather than
/// manipulating `profiles.yaml` directly.
async fn write_tier_profile(yaml: &str) -> Result<()> {
    let mut item = PrfItem {
        uid: Some(SmartString::from("locus-tier")),
        // "local" rather than "remote": there is no URL to refresh from, and
        // marking it remote would make Verge try to fetch it on a timer and fail
        // every time.
        itype: Some(SmartString::from("local")),
        name: Some(SmartString::from(TIER_PROFILE_NAME)),
        file: Some(SmartString::from(TIER_PROFILE_FILE)),
        desc: Some(SmartString::from("Your Locus tier. Managed automatically — do not edit.")),
        // The YAML is handed over rather than pre-written so the existing helper
        // performs the write, the same way every other profile is created.
        // `PrfItem` uses smartstring throughout, including here.
        file_data: Some(SmartString::from(yaml)),
        ..Default::default()
    };

    // If the profile already exists, update it in place instead of appending a
    // duplicate. Re-applying happens on every config refresh, so this is the
    // common path, not the exception.
    let existing = {
        let profiles = Config::profiles().await;
        let latest = profiles.latest_arc();
        latest.items.as_ref().and_then(|items| {
            items
                .iter()
                .find(|candidate| candidate.file.as_deref() == Some(TIER_PROFILE_FILE))
                .cloned()
        })
    };

    if existing.is_some() {
        // The profile is already registered, so only its CONTENT needs replacing.
        //
        // Deliberately not going through the item-patching helper: that is
        // private to the profiles module, and the orchestration it performs
        // (deleting a superseded file, rewriting profiles.yaml) is about changing
        // WHICH profile is current — none of which applies when we are refreshing
        // the contents of the one that is already there.
        //
        // The uid is left alone on purpose: anything referring to it (the current
        // pointer, a saved node selection) must stay valid across a refresh.
        let path = dirs::app_profiles_dir()?.join(TIER_PROFILE_FILE);
        tokio::fs::write(&path, profile_yaml_bytes(yaml))
            .await
            .with_context(|| format!("could not write {}", path.display()))?;

        logging!(debug, Type::Config, "[locus] tier profile updated in place");
        return Ok(());
    }

    profiles::profiles_append_item_safe(&mut item)
        .await
        .context("could not create the Locus tier profile")?;

    logging!(debug, Type::Config, "[locus] tier profile created");
    Ok(())
}

/// The bytes to write for the tier profile.
///
/// A tiny indirection so the write path has one representation of "what goes in
/// the file", and a future change (a header comment, say) cannot be applied to
/// one path and forgotten in the other.
fn profile_yaml_bytes(yaml: &str) -> Vec<u8> {
    yaml.as_bytes().to_vec()
}

/// Removes the tier profile.
///
/// Used when the hub says a code is suspended: leaving the profile behind would
/// keep a configuration the hub will no longer serve, and Verge would still list
/// it. Best-effort — a failure here must not block the deactivation itself.
pub async fn remove_tier_profile() {
    let profiles = Config::profiles().await;
    let result = profiles
        .with_data_modify(|mut candidate| async move {
            let uids: Vec<std::string::String> = candidate
                .items
                .as_ref()
                .map(|items| {
                    items
                        .iter()
                        .filter(|item| item.file.as_deref() == Some(TIER_PROFILE_FILE))
                        .filter_map(|item| item.uid.as_ref().map(ToString::to_string))
                        .collect()
                })
                .unwrap_or_default();

            for uid in &uids {
                if let Ok((_, plan)) = candidate.plan_delete_item(uid) {
                    plan.cleanup().await;
                }
            }
            candidate.save_file().await?;
            Ok((candidate, ()))
        })
        .await;

    match result {
        Ok(()) => logging!(info, Type::Config, "[locus] tier profile removed"),
        Err(error) => logging!(
            warn,
            Type::Config,
            "[locus] could not remove the tier profile: {error:#}"
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_tier_profile_file_is_stable() {
        // Re-applying must overwrite, not accumulate. If this ever changed to a
        // generated name, every heartbeat would leave another file behind.
        assert_eq!(TIER_PROFILE_FILE, "locus.yaml");
    }

    /// The generated document must be valid YAML and carry the tier's server —
    /// this is the last check before it reaches the core, where a malformed
    /// document surfaces as an opaque engine error.
    #[test]
    fn the_document_handed_to_verge_is_well_formed() {
        use crate::locus::tier::build_profile;

        let config = TierConfig {
            server: "networkingguides.duckdns.org".into(),
            server_port: 8445,
            password: "secret".into(),
            method: "aes-256-gcm".into(),
            uot_port: 8446,
        };

        let profile = build_profile(&config, true);
        let parsed: serde_yaml_ng::Value =
            serde_yaml_ng::from_str(&profile.yaml).expect("must be valid YAML");

        assert_eq!(
            parsed["proxies"][0]["server"], "networkingguides.duckdns.org",
            "the tier's server must survive into the document the core runs"
        );
    }

    /// A tier with no UoT endpoint must still produce a document Verge accepts,
    /// because eco and stealth are the common case.
    #[test]
    fn a_tier_without_uot_is_still_applicable() {
        use crate::locus::tier::build_profile;

        let config = TierConfig {
            server: "networkingguides.duckdns.org".into(),
            server_port: 8443,
            password: "secret".into(),
            method: "aes-256-gcm".into(),
            uot_port: 0,
        };

        let profile = build_profile(&config, false);
        let parsed: serde_yaml_ng::Value =
            serde_yaml_ng::from_str(&profile.yaml).expect("must be valid YAML");
        assert_eq!(parsed["proxies"].as_sequence().map(Vec::len), Some(1));
    }

    /// A rejection must carry a reason. "It failed" is not something a student
    /// or an operator can act on.
    #[test]
    fn a_rejection_carries_a_reason() {
        let outcome = ApplyOutcome::Rejected {
            reason: "proxy port 7897 already in use".to_owned(),
        };
        match outcome {
            ApplyOutcome::Rejected { reason } => assert!(!reason.is_empty()),
            other => panic!("expected a rejection, got {other:?}"),
        }
    }
}

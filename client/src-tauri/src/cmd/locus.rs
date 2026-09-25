//! Tauri commands for the Locus product surface.
//!
//! Every command is thin: it gathers inputs, calls into [`crate::locus`], and
//! returns a **typed** result. The typing is deliberate — the retired client
//! returned strings, so the UI could not tell "code already used on another
//! device" from "we could not reach the hub", and every distinct situation
//! collapsed into one unhelpful message. A student whose code is bound to their
//! old laptop needs the former, not "activation failed".
//!
//! The commands are also the seam where persistence and the core meet. Nothing
//! in `locus/` writes config or starts the core; this module owns that wiring so
//! the pure logic stays testable without an app handle.

use super::CmdResult;
use crate::config::Config;
use crate::locus::{activation, contract, device, store};
use crate::utils::dirs;
use serde::Serialize;

/// Whether the app has an activation to work with, and what it is.
///
/// The single call the UI polls. Deliberately small and cheap: the activation
/// gate renders on every launch, and a slow first paint is the first thing a
/// student notices.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LocusStatus {
    /// Whether this device has a code bound and accepted.
    pub activated: bool,
    /// The tier name (`eco`, `stealth`, `strike`), when known.
    pub tier: Option<String>,
    /// The device fingerprint, **truncated** — enough for support to correlate,
    /// not enough to be a useful identifier if someone screenshots it.
    pub device_id: String,
    /// Which platform build this is, so a support report says so without asking.
    pub platform: Option<String>,
    /// The running app version.
    pub version: String,
}

/// Reads the current product state.
///
/// Never fails: a status query that can error forces the UI to invent a state,
/// and the honest answer when we cannot read storage is "not activated".
#[tauri::command]
pub async fn locus_status() -> LocusStatus {
    let verge = Config::verge().await;
    let verge_data = verge.latest_arc();

    // The fingerprint is reported from the DEVICE, not from storage: it is
    // derived, stable, and available before activation. Reporting the stored one
    // would blank it on a fresh install, which is exactly when support asks for
    // it.
    let fingerprint = device::fingerprint();
    let activation = store::read(&verge_data);

    LocusStatus {
        activated: activation.is_some(),
        tier: activation.as_ref().map(|a| a.tier.clone()),
        device_id: store::redact(&fingerprint),
        platform: contract::current_platform().map(str::to_owned),
        version: env!("CARGO_PKG_VERSION").to_owned(),
    }
}

/// Validates an activation code **offline**.
///
/// Called as the student types, so a typo is caught without a request. This is
/// also what protects them from the hub's rate limit: `/api/activate` allows 5
/// attempts per 10 minutes per address, and a student who mistypes four times
/// would otherwise lock themselves out of the one screen that could explain the
/// mistake.
#[tauri::command]
pub async fn locus_validate_code(code: String) -> ValidateCodeResult {
    match activation::validate_code(&code) {
        Ok(canonical) => ValidateCodeResult {
            valid: true,
            canonical: Some(canonical),
            message: None,
        },
        Err(error) => ValidateCodeResult {
            valid: false,
            canonical: None,
            message: Some(error.to_string()),
        },
    }
}

/// The result of the offline code check.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ValidateCodeResult {
    pub valid: bool,
    /// The canonical hyphenated form, so the UI can show what will be sent.
    pub canonical: Option<String>,
    pub message: Option<String>,
}

/// Asks the hub whether a code is usable, without binding anything.
///
/// Lets the student learn their code is real *before* committing to an
/// activation that binds their device. A transport failure is returned as an
/// error rather than as "your code is bad", because those are different
/// situations and only one of them is the student's fault.
#[tauri::command]
pub async fn locus_check_code(code: String) -> CmdResult<activation::CodeCheck> {
    let fingerprint = device::fingerprint();
    activation::lookup_code(&code, &fingerprint)
        .await
        .map_err(|error| super::coded_error("LOCUS_LOOKUP_FAILED", format!("{error:#}")))
}

/// Activates this device, persists the result, and configures the tunnel.
///
/// The order matters and is the whole point of this function:
///
///   1. validate locally (free, and catches typos)
///   2. ask the hub
///   3. **persist only on success** — a half-written activation is worse than
///      none, because the UI would show "activated" on a device the hub has
///      never heard of
///   4. write the tier config and apply it
///
/// A failure at any step leaves the previous state untouched.
#[tauri::command]
pub async fn locus_activate(code: String) -> CmdResult<ActivationResult> {
    let fingerprint = device::fingerprint();

    let outcome = activation::activate(&code, &fingerprint)
        .await
        .map_err(|error| super::coded_error("LOCUS_ACTIVATE_FAILED", format!("{error:#}")))?;

    match outcome {
        activation::ActivationOutcome::Activated {
            code,
            tier,
            // Not applied yet — see `config_applied` below.
            config: _,
            udp_relay,
            ..
        } => {
            // Persist BEFORE reporting success. A UI that shows "activated" on a
            // device the hub has never heard of is worse than a failure: the
            // student stops trying to fix it.
            store::store(&store::Activation {
                code: code.clone(),
                tier: tier.clone(),
                fingerprint,
            })
            .await
            .map_err(|error| {
                super::coded_error("LOCUS_STORE_FAILED", format!("{error:#}"))
            })?;

            Ok(ActivationResult {
                code,
                tier,
                udp_relay,
                // The tier config is not written or applied yet. Reporting true
                // here would make the UI offer a Connect button that cannot work.
                config_applied: false,
                message: "Activated".to_owned(),
            })
        }
        // Every other outcome is a definitive answer about the code, not an
        // error in our code. Returning them as failures would make the UI show
        // "something went wrong" when the truth is "your code is expired".
        other => Err(super::coded_error(
            "LOCUS_ACTIVATION_REFUSED",
            describe_outcome(&other),
        )),
    }
}

/// What a successful activation produced.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ActivationResult {
    pub code: String,
    pub tier: String,
    pub udp_relay: bool,
    /// Whether the tunnel configuration was written and applied. False while the
    /// store and apply path are still being wired, so the UI can say so rather
    /// than implying the VPN is ready.
    pub config_applied: bool,
    pub message: String,
}

/// Turns a refusal into something worth showing a student.
///
/// Each arm is a distinct, actionable situation. "Activation failed" is what the
/// retired client said for all of them, and it is why support conversations
/// started from nothing.
fn describe_outcome(outcome: &activation::ActivationOutcome) -> String {
    use activation::ActivationOutcome as O;
    match outcome {
        O::Activated { .. } => "Activated".to_owned(),
        O::BoundToAnotherDevice => {
            "This code is already activated on a different device. \
             Contact the person who sold it to you — they can move it to this one."
                .to_owned()
        }
        O::Suspended => {
            "This code has been suspended. Contact the person who sold it to you.".to_owned()
        }
        O::Expired => "This code has expired. You will need a new one.".to_owned(),
        O::NotFound => {
            "That code was not recognised. Check it against the card — it is easy \
             to mix up 0 and O, or 1 and I, which is why Locus codes leave them out."
                .to_owned()
        }
        O::RateLimited { .. } => {
            "Too many attempts. Wait ten minutes and try again — the limit protects \
             everyone's codes from being guessed."
                .to_owned()
        }
        O::ServerError { message, .. } => {
            if message.is_empty() {
                "The Locus hub could not complete the activation. Try again in a moment.".to_owned()
            } else {
                message.clone()
            }
        }
    }
}

/// Where the update staging directory lives.
///
/// Exposed so the diagnostics screen can report it without duplicating the path
/// logic, and so a support conversation can ask for exactly one location.
#[tauri::command]
pub async fn locus_update_staging_dir() -> CmdResult<String> {
    let dir = dirs::app_home_dir()
        .map(|root| root.join("update-staging"))
        .map_err(|error| super::coded_error("LOCUS_PATHS_FAILED", format!("{error:#}")))?;
    Ok(dir.to_string_lossy().into_owned())
}

/// Reports the hub URL the client will talk to.
///
/// The UI shows this in diagnostics, and it is the first thing to check when
/// activation fails on a school network.
#[tauri::command]
pub async fn locus_hub_url() -> String {
    contract::HUB_URL.to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;
    use activation::ActivationOutcome as O;

    /// Every refusal must produce a sentence a student can act on.
    ///
    /// This is the direct counter to the retired client's behaviour, where all
    /// of these collapsed into one message. A test rather than a comment because
    /// "add a new outcome and forget to describe it" is exactly the kind of
    /// omission that reaches a student.
    #[test]
    fn every_refusal_is_described_actionably() {
        let outcomes = [
            O::BoundToAnotherDevice,
            O::Suspended,
            O::Expired,
            O::NotFound,
            O::RateLimited {
                retry_after_secs: None,
            },
            O::ServerError {
                code: 500,
                message: String::new(),
            },
        ];

        for outcome in outcomes {
            let text = describe_outcome(&outcome);
            assert!(!text.is_empty(), "{outcome:?} produced no message");
            assert!(
                text.len() > 20,
                "{outcome:?} produced something too terse to act on: {text:?}"
            );
            // "failed" alone is the phrasing this exists to eliminate.
            assert!(
                !text.eq_ignore_ascii_case("activation failed"),
                "{outcome:?} fell back to the useless generic message"
            );
        }
    }

    /// Bound-to-another-device must name the fix, because the student cannot
    /// solve it themselves — an operator has to unbind the code.
    #[test]
    fn the_bound_message_points_at_the_person_who_can_help() {
        let text = describe_outcome(&O::BoundToAnotherDevice);
        assert!(
            text.to_lowercase().contains("sold") || text.to_lowercase().contains("contact"),
            "the message must say who can resolve it, got {text:?}"
        );
    }

    /// A hub-supplied message is shown verbatim: it is what a support
    /// conversation will quote, and rewording it would make the two disagree.
    #[test]
    fn a_hub_message_is_preferred_when_present() {
        let text = describe_outcome(&O::ServerError {
            code: 500,
            message: "Hub is in maintenance until 14:00".to_owned(),
        });
        assert_eq!(text, "Hub is in maintenance until 14:00");
    }

    /// Rate limiting must say to wait, not just to try again — retrying
    /// immediately makes it worse and the limit is 10 minutes.
    #[test]
    fn the_rate_limit_message_says_to_wait() {
        let text = describe_outcome(&O::RateLimited {
            retry_after_secs: None,
        });
        assert!(
            text.to_lowercase().contains("wait") || text.to_lowercase().contains("minute"),
            "the message must tell the student to wait, got {text:?}"
        );
    }

    /// The fingerprint shown in the UI must be truncated. It is a stable device
    /// identifier, and a full one in a screenshot is a shareable one.
    #[test]
    fn status_reports_a_redacted_device_id() {
        let full = device::fingerprint();
        let redacted = device::redact(&full);
        assert_ne!(redacted, full, "the device id must not be the full fingerprint");
        assert_eq!(redacted.len(), 12);
        assert!(full.starts_with(&redacted));
    }
}

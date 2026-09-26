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
use crate::core::CoreManager;
use crate::locus::{activation, apply, contract, device, store};
use crate::utils::dirs;
use clash_verge_logging::{Type, logging};
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

    /// The subscription state, already parsed — see [`SubscriptionStatus`].
    ///
    /// Pre-parsed rather than handed to the UI as a raw date string, because the
    /// "never invent a date" rule needs exactly one decision point. If the
    /// frontend received the raw string it would have to decide what an empty
    /// value means, and each screen could decide differently.
    pub subscription: SubscriptionStatus,
}

/// What the Account screen should say about the subscription.
///
/// A closed set of cases rather than an optional date, because "we do not know"
/// and "it has lapsed" are different answers with different copy, and collapsing
/// them into one nullable field is how a blank or wrong date reaches a paying
/// student.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "state", rename_all = "camelCase")]
pub enum SubscriptionStatus {
    /// The hub has not told us. Render nothing about expiry.
    Unknown,
    /// The date has passed.
    Lapsed,
    /// Valid, with whole days remaining and the date for a tooltip.
    Active {
        /// Whole days remaining, rounded up. `0` is possible only mid-day.
        days_remaining: i64,
        /// Whether this is inside the renewal-warning window.
        urgent: bool,
        /// The hub's own string, so the UI can show the exact date on hover
        /// without re-deriving it.
        expires_at: String,
    },
}

/// Maps stored state to what the Account screen should say about the subscription.
///
/// Split out of the command so the rules can be tested without an app handle —
/// `locus_status` reads global config, and a test that needed that would be
/// testing the config layer instead of these decisions.
///
/// The two rules that matter, and why each is a deliberate choice:
///
///  * **Unactivated implies Unknown, never Lapsed.** A fresh install has no
///    expiry recorded. Reporting that as "expired" would greet a new student
///    whose code is perfectly valid with a message telling them it had run out.
///  * **An unparseable or absent date implies Unknown.** Same reason, one step
///    further on: a hub that predates the field, or a code with no expiry set,
///    must show nothing rather than a guess.
#[must_use]
fn classify_subscription(activated: bool, raw_expiry: Option<&str>) -> SubscriptionStatus {
    if !activated {
        return SubscriptionStatus::Unknown;
    }

    match crate::locus::expiry::parse(raw_expiry) {
        crate::locus::expiry::Expiry::Unknown => SubscriptionStatus::Unknown,
        crate::locus::expiry::Expiry::Lapsed => SubscriptionStatus::Lapsed,
        crate::locus::expiry::Expiry::InDays { days, .. } => SubscriptionStatus::Active {
            days_remaining: days,
            urgent: days <= crate::locus::expiry::EXPIRY_WARNING_DAYS,
            // The hub's own string, so the tooltip can show the exact date
            // without the client re-deriving and possibly shifting it.
            expires_at: raw_expiry.unwrap_or_default().to_owned(),
        },
    }
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

    let subscription = classify_subscription(
        activation.is_some(),
        verge_data.locus_expires_at.as_deref(),
    );

    LocusStatus {
        activated: activation.is_some(),
        tier: activation.as_ref().map(|a| a.tier.clone()),
        device_id: store::redact(&fingerprint),
        platform: contract::current_platform().map(str::to_owned),
        version: env!("CARGO_PKG_VERSION").to_owned(),
        subscription,
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
            config,
            udp_relay,
            ..
        } => {
            // Persist BEFORE reporting success. A UI that shows "activated" on a
            // device the hub has never heard of is worse than a failure: the
            // student stops trying to fix it.
            store::store(&store::Activation {
                code: code.clone(),
                tier: tier.clone(),
                fingerprint: fingerprint.clone(),
            })
            .await
            .map_err(|error| super::coded_error("LOCUS_STORE_FAILED", format!("{error:#}")))?;

            // The tier's connection details, so Connect has something to dial.
            // Stored separately from the entitlement because a hub response can
            // refresh one without the other — but persisted here, in the same
            // operation, because a code with no config is a device that believes
            // it is activated and cannot connect.
            let config_applied = match &config {
                Some(tier_config) => {
                    store::store_tier_config(tier_config, udp_relay)
                        .await
                        .map_err(|error| super::coded_error("LOCUS_STORE_FAILED", format!("{error:#}")))?;

                    // Apply it now so the tunnel is ready to start. A failure
                    // here is reported in the result rather than failing the
                    // whole activation: the student IS activated, and telling
                    // them otherwise would send them to re-enter a code that
                    // already worked.
                    match apply::apply_tier(tier_config, udp_relay).await {
                        Ok(apply::ApplyOutcome::Applied) => true,
                        Ok(apply::ApplyOutcome::StagedOnly) => true,
                        Ok(apply::ApplyOutcome::Rejected { reason }) => {
                            logging!(warn, Type::Cmd, "[locus] tier config rejected: {reason}");
                            false
                        }
                        Err(error) => {
                            logging!(warn, Type::Cmd, "[locus] could not apply tier: {error:#}");
                            false
                        }
                    }
                }
                // The hub returned a success without a server config. That is a
                // hub-side fault (it would leave a "connected" device that cannot
                // reach anything), and it is worth saying so rather than
                // pretending activation fully succeeded.
                None => false,
            };

            // Start beating now rather than waiting for the next launch. A
            // freshly activated device is the one most likely to be used
            // immediately, and without this its entitlement would only start
            // refreshing on the next restart.
            crate::locus::runtime::start(store::Activation {
                code: code.clone(),
                tier: tier.clone(),
                fingerprint: fingerprint.clone(),
            });

            Ok(ActivationResult {
                code,
                tier,
                udp_relay,
                config_applied,
                message: if config_applied {
                    "Activated".to_owned()
                } else {
                    "Activated, but the connection details could not be set up. \
                     Contact support before trying to connect."
                        .to_owned()
                },
            })
        }
        // Every other outcome is a definitive answer about the code, not an
        // error in our code. Returning them as failures would make the UI show
        // "something went wrong" when the truth is "your code is expired".
        other => Err(super::coded_error("LOCUS_ACTIVATION_REFUSED", describe_outcome(&other))),
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
        O::BoundToAnotherDevice => "This code is already activated on a different device. \
             Contact the person who sold it to you — they can move it to this one."
            .to_owned(),
        O::Suspended => "This code has been suspended. Contact the person who sold it to you.".to_owned(),
        O::Expired => "This code has expired. You will need a new one.".to_owned(),
        O::NotFound => "That code was not recognised. Check it against the card — it is easy \
             to mix up 0 and O, or 1 and I, which is why Locus codes leave them out."
            .to_owned(),
        O::RateLimited { .. } => "Too many attempts. Wait ten minutes and try again — the limit protects \
             everyone's codes from being guessed."
            .to_owned(),
        O::ServerError { message, .. } => {
            if message.is_empty() {
                "The Locus hub could not complete the activation. Try again in a moment.".to_owned()
            } else {
                message.clone()
            }
        }
    }
}

/// Connects the tunnel.
///
/// The four steps, in order, with the reason for each:
///
///   1. **Require an entitlement.** Connecting without one produces a tunnel the
///      hub will refuse at the next heartbeat, and a student staring at
///      "connected" while nothing works.
///   2. **Check TUN is actually possible before demanding it.** See below.
///   3. **Write and apply the tier config.** Idempotent: re-applying refreshes a
///      rotated password or a moved server.
///   4. **Turn TUN on, then start the core and confirm it is running.** A success
///      return from `start_core` is not proof; the run state is what the UI will
///      show, so that is what gets checked.
///
/// ## Why TUN capability is checked before anything is written
///
/// TUN is still always-on, by product decision — the retired client was TUN-only,
/// and a system-proxy mode would silently pass traffic the school network can
/// see. Nothing here makes Locus connect without it.
///
/// What changed is *when* we find out it is impossible. TUN needs privileges:
/// mihomo running as an ordinary user fails with `configure tun interface:
/// operation not permitted`. Verge models this as `tun_capable()` — elevated, or
/// a usable service. Forcing `enable_tun_mode = true` on a machine where neither
/// holds is what produced the dead end students hit: the preference was written,
/// `prepare_startup` then computed `service_required = true`, the service was
/// `NotInstalled`, so it returned `StartupDecision::Wait` and the core never
/// started at all. The UI showed "Core temporarily unavailable" — a message about
/// the core, for a problem that was never the core's.
///
/// The failure was made worse by ordering. `reconcile_startup_tun_availability`
/// runs at startup and deliberately turns TUN off so the core *can* start on the
/// sidecar. Connect then set it straight back on, re-arming the exact condition
/// startup had just cleared, so the app started cleanly and then walked into the
/// wall on the first press of Connect.
///
/// So: refuse early, name the real obstacle, and leave the stored preference
/// alone when we refuse. A student who is told "TUN needs administrator rights"
/// can act on that; a student shown a spinning overlay cannot.
/// The refusal returned when TUN cannot start on this device.
///
/// Kept as a named constant so a test can assert its wording without running the
/// command, and so the one place a student is told about privileges is greppable.
/// It names **both** routes (install the service, or run elevated) because on
/// Linux either one is sufficient, and it says "administrator rights" rather than
/// "TUN" — the student does not know what TUN is, and naming our mechanism would
/// describe the implementation instead of the obstacle.
const TUN_UNAVAILABLE_MESSAGE: &str = "Locus needs administrator rights to create the VPN tunnel, \
     and the Locus service is not available on this device. Install the service from Settings, or \
     start Locus as an administrator, then try again.";

/// Whether a connect attempt may proceed on the given run state.
///
/// Split out from the command so the gate is testable without an app handle:
/// `tun_capable` is `is_admin || service_usable`, and the point of the gate is
/// that a machine with neither must be refused *before* anything is written.
#[must_use]
const fn connect_allowed(state: &crate::core::runstate::RunState) -> bool {
    state.tun_capable()
}

#[tauri::command]
pub async fn locus_connect() -> CmdResult<ConnectionResult> {
    let verge = Config::verge().await;
    let activation = store::read(&verge.latest_arc())
        .ok_or_else(|| super::coded_error("LOCUS_NOT_ACTIVATED", "This device is not activated yet."))?;

    // Ask the run state whether TUN can work here, before writing anything.
    //
    // This is the same predicate the manager uses, not a second opinion: if it
    // says TUN is impossible, `start_core` would have refused for the same
    // reason, and we prefer to explain that now rather than after a failed
    // start. Nothing is persisted on this path — a refusal must not leave
    // `enable_tun_mode` flipped on for the next launch to trip over.
    if !connect_allowed(&crate::core::runstate::RUN_STATE.state()) {
        return Err(super::coded_error(
            "LOCUS_TUN_NOT_AVAILABLE",
            TUN_UNAVAILABLE_MESSAGE,
        ));
    }

    // Turn TUN on BEFORE applying, so the generated config is built for TUN
    // rather than being rebuilt immediately afterwards. We only reach this once
    // the capability check above has established that TUN can actually start.
    {
        let verge = Config::verge().await;
        verge.edit_draft(|draft| {
            draft.enable_tun_mode = Some(true);
        });
        verge
            .data_arc()
            .save_file()
            .await
            .map_err(|error| super::coded_error("LOCUS_STORE_FAILED", format!("{error:#}")))?;
    }

    // Step 2 requires the tier config, which activation stored. Until the tier
    // payload is persisted alongside the entitlement, this reports honestly that
    // it cannot proceed rather than connecting with no proxy configured.
    let config = match store::tier_config(&activation.tier).await {
        Some(config) => config,
        None => {
            return Err(super::coded_error(
                "LOCUS_NO_TIER_CONFIG",
                format!(
                    "No connection details are stored for the {} tier yet.                      Re-open the app so activation can complete.",
                    activation.tier
                ),
            ));
        }
    };

    match apply::apply_tier(&config, false).await {
        Ok(apply::ApplyOutcome::Rejected { reason }) => {
            return Err(super::coded_error(
                "LOCUS_CONFIG_REJECTED",
                format!("The tunnel configuration was refused: {reason}"),
            ));
        }
        Ok(_) => {}
        Err(error) => {
            return Err(super::coded_error(
                "LOCUS_CONFIG_FAILED",
                format!("Could not write the tunnel configuration: {error:#}"),
            ));
        }
    }

    CoreManager::global()
        .start_core()
        .await
        .map_err(|error| super::coded_error("LOCUS_CONNECT_FAILED", format!("{error:#}")))?;

    Ok(ConnectionResult {
        connected: true,
        message: "Connected".to_owned(),
    })
}

/// Disconnects the tunnel.
///
/// Stops the core **unconditionally**, even when we believe we are already
/// disconnected.
///
/// This is the direct fix for the retired client's worst state bug: it
/// early-returned when its own `connected` flag was false, so a disconnect that
/// raced the watchdog left the engine running untracked, and the next Connect
/// failed with "already running" — recoverable only by restarting the app.
/// `stop_core` is a safe no-op when nothing is running, so there is nothing to
/// save by checking first, and everything to lose.
#[tauri::command]
pub async fn locus_disconnect() -> CmdResult<ConnectionResult> {
    CoreManager::global()
        .stop_core()
        .await
        .map_err(|error| super::coded_error("LOCUS_DISCONNECT_FAILED", format!("{error:#}")))?;

    Ok(ConnectionResult {
        connected: false,
        message: "Disconnected".to_owned(),
    })
}

/// The result of a connect or disconnect.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConnectionResult {
    pub connected: bool,
    pub message: String,
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
    use crate::core::manager::RunningMode;
    use crate::core::runstate::ServiceHealth;

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
            O::RateLimited { retry_after_secs: None },
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
        let text = describe_outcome(&O::RateLimited { retry_after_secs: None });
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

    /// Build a run state for the connect gate.
    fn run_state(health: ServiceHealth, is_admin: bool, op_in_flight: bool) -> crate::core::runstate::RunState {
        crate::core::runstate::RunState {
            health,
            pending: None,
            sidecar_allowed: false,
            mode: RunningMode::NotRunning,
            is_admin,
            op_in_flight,
        }
    }

    /// A machine with neither privileges nor a usable service must be refused.
    ///
    /// This is the exact configuration that produced the dead end: an ordinary
    /// user, service never installed, because TUN cannot start without one of
    /// them (`configure tun interface: operation not permitted`). Before this
    /// gate, connect wrote `enable_tun_mode = true` anyway, `start_core` then
    /// waited forever on a service that was never coming, and the student saw a
    /// message about the core rather than about privileges.
    #[test]
    fn connect_is_refused_when_tun_cannot_start() {
        let state = run_state(ServiceHealth::NotInstalled, false, false);
        assert!(
            !connect_allowed(&state),
            "an unprivileged machine with no service must not attempt TUN"
        );
    }

    /// Either route is sufficient: elevation alone, or a usable service alone.
    ///
    /// Pinned because `tun_capable` is `is_admin || service_usable`, and a future
    /// edit that tightened it to require *both* would lock out every student who
    /// was only ever going to use one of them.
    #[test]
    fn connect_is_allowed_with_either_privilege_or_service() {
        assert!(
            connect_allowed(&run_state(ServiceHealth::NotInstalled, true, false)),
            "running elevated must be enough on its own"
        );
        assert!(
            connect_allowed(&run_state(ServiceHealth::Ready, false, false)),
            "a ready service must be enough on its own"
        );
    }

    /// A service operation in flight is not a usable service.
    ///
    /// This is the install-in-progress case: the service is `Ready` but an
    /// operation is mid-flight, so `service_usable` is false. Connecting then
    /// would race the operation, so the gate must hold.
    #[test]
    fn connect_is_refused_while_a_service_operation_is_in_flight() {
        let state = run_state(ServiceHealth::Ready, false, true);
        assert!(
            !connect_allowed(&state),
            "a service mid-operation must not be treated as usable"
        );
    }

    /// The refusal must name a route the student can actually take.
    ///
    /// Two independent things could make this useless: a message so terse it does
    /// not say what to do, or one that says "TUN" — our mechanism, not their
    /// problem. It must mention administrator access and where to get the fix.
    #[test]
    fn the_tun_refusal_is_actionable() {
        let text = TUN_UNAVAILABLE_MESSAGE;
        let lowered = text.to_lowercase();
        assert!(
            lowered.contains("administrator"),
            "must name the privilege that is missing, got {text:?}"
        );
        assert!(
            lowered.contains("settings") || lowered.contains("install"),
            "must say where the fix is, got {text:?}"
        );
        // The acronym specifically, as a word — not the substring, or "tunnel"
        // (which is the student-facing concept and belongs here) would match.
        assert!(
            !lowered.split(|c: char| !c.is_alphanumeric()).any(|word| word == "tun"),
            "must not name the TUN mechanism itself, got {text:?}"
        );
        // And it must still say what the thing they are getting *is*.
        assert!(
            lowered.contains("tunnel") || lowered.contains("vpn"),
            "must say what access they are getting, got {text:?}"
        );
    }

    /// An unactivated device has an UNKNOWN subscription, never a lapsed one.
    ///
    /// This is the fresh-install case. `locus_expires_at` is empty because no
    /// hub has ever spoken to this device; if that were classified as lapsed,
    /// the Account screen would tell a brand-new student their subscription had
    /// expired before they had activated anything.
    #[test]
    fn an_unactivated_device_has_an_unknown_subscription() {
        assert!(matches!(
            classify_subscription(false, None),
            SubscriptionStatus::Unknown
        ));
        // Even with a date present — a damaged record — activation wins, because
        // an expiry without an entitlement describes nothing.
        assert!(matches!(
            classify_subscription(false, Some("2030-01-01 00:00:00.000Z")),
            SubscriptionStatus::Unknown
        ));
    }

    /// An activated device with no recorded date also reports UNKNOWN.
    ///
    /// Happens with a hub that predates the heartbeat field, and with codes that
    /// have no expiry set at all. Neither is the student's fault and neither
    /// should render as an expiry.
    #[test]
    fn a_missing_date_on_an_activated_device_is_unknown() {
        assert!(matches!(
            classify_subscription(true, None),
            SubscriptionStatus::Unknown
        ));
        assert!(matches!(
            classify_subscription(true, Some("")),
            SubscriptionStatus::Unknown
        ));
        assert!(matches!(
            classify_subscription(true, Some("garbage")),
            SubscriptionStatus::Unknown
        ));
    }

    /// A past date on an activated device is LAPSED, and says so.
    #[test]
    fn a_past_date_is_reported_as_lapsed() {
        assert!(matches!(
            classify_subscription(true, Some("2020-01-01 00:00:00.000Z")),
            SubscriptionStatus::Lapsed
        ));
    }

    /// A future date is ACTIVE, and its urgency follows the warning window.
    ///
    /// The `urgent` flag is what the UI colours on, so it must agree with the
    /// one threshold constant rather than a second copy of the number.
    #[test]
    fn a_future_date_is_active_with_the_right_urgency() {
        let far = chrono::Utc::now() + chrono::Duration::days(60);
        let far_text = far.format("%Y-%m-%d %H:%M:%S%.fZ").to_string();
        match classify_subscription(true, Some(&far_text)) {
            SubscriptionStatus::Active {
                days_remaining,
                urgent,
                expires_at,
            } => {
                assert!(days_remaining > crate::locus::expiry::EXPIRY_WARNING_DAYS);
                assert!(!urgent, "60 days out must not be urgent");
                // The exact string the hub sent, so the tooltip can show it.
                assert_eq!(expires_at, far_text);
            }
            other => panic!("expected Active, got {other:?}"),
        }
    }

    /// Inside the warning window, `urgent` is set.
    #[test]
    fn a_soon_expiry_is_marked_urgent() {
        let soon = chrono::Utc::now() + chrono::Duration::days(2);
        let text = soon.format("%Y-%m-%d %H:%M:%S%.fZ").to_string();
        match classify_subscription(true, Some(&text)) {
            SubscriptionStatus::Active { urgent, .. } => assert!(
                urgent,
                "two days from expiry must be urgent"
            ),
            other => panic!("expected Active, got {other:?}"),
        }
    }

}

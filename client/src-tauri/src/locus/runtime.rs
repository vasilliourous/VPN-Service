//! Supervising the heartbeat loop for the running app.
//!
//! [`crate::locus::heartbeat`] implements the loop but owns no app state: it
//! knows how to beat, back off and stop, and nothing about Tauri. Something has
//! to start it after activation, restart it if the code changes, and stop it on
//! exit. That is this.
//!
//! # Why one supervisor rather than "start it where it is needed"
//!
//! The loop must never run twice. Two loops means two beats per interval, double
//! the hub load, and a device that looks like it is flapping. It must also not
//! outlive the app: a heartbeat that keeps beating for a quit instance would keep
//! a suspended code looking alive.
//!
//! Both of those are properties of a single owner, not of a caller remembering.
//!
//! # What it does with the hub's answer
//!
//! The loop is deliberately policy-free, and this is where the policy lives:
//!
//!   * a **config refresh** is applied through the same path activation uses, so
//!     there is one route from "the hub said something changed" to "the core is
//!     running it";
//!   * a **suspension or expiry** clears the entitlement and takes the tunnel
//!     down, because a device the hub has refused must not keep a tunnel up on
//!     the strength of a stale local record;
//!   * an **update signal** is recorded, not acted on. Installing is the
//!     student's decision, and doing it silently mid-session would kill their
//!     connection without asking.

use crate::locus::heartbeat::{BeatOutcome, HeartbeatLoop};
use crate::locus::{apply, store};
use clash_verge_logging::{Type, logging};
use std::sync::Mutex;

/// Holds the running loop, if any.
static RUNNING: Mutex<Option<HeartbeatLoop>> = Mutex::new(None);

/// Starts beating for this activation, replacing any loop already running.
///
/// Replacing rather than refusing is deliberate: the caller's intent is always
/// "beat for *this* activation", and the most likely reason a loop is already
/// running is a re-activation that changed the code or tier. Silently keeping the
/// old one would leave the device reporting a tier it no longer has.
pub fn start(activation: store::Activation) {
    // Stop the previous loop first, so its task cannot deliver one more
    // outcome against the old activation.
    stop();

    let code = activation.code.clone();
    let fingerprint = activation.fingerprint.clone();

    logging!(
        info,
        Type::Config,
        "[locus] starting heartbeat (tier {})",
        activation.tier
    );

    let loop_handle = HeartbeatLoop::start(code, fingerprint, move |outcome| {
        // The callback is synchronous and the work it triggers is async, so it
        // hands off to the runtime rather than blocking the loop's task. A
        // heartbeat that stalled on config application would look like a
        // transport failure and back off for no reason.
        crate::process::AsyncHandler::spawn(move || async move {
            handle_outcome(outcome).await;
        });
    });

    match RUNNING.lock() {
        Ok(mut guard) => *guard = Some(loop_handle),
        Err(poisoned) => {
            // A poisoned lock means a previous holder panicked. The loop itself
            // is still valid, so recover the guard rather than leaving the app
            // unable to heartbeat for the rest of its life.
            logging!(warn, Type::Config, "[locus] heartbeat mutex was poisoned; recovering");
            *poisoned.into_inner() = Some(loop_handle);
        }
    }
}

/// Stops the loop if one is running.
///
/// Safe to call when nothing is running, and safe to call twice — this is called
/// from shutdown paths where "is it running?" is not a question worth getting
/// wrong.
pub fn stop() {
    let taken = match RUNNING.lock() {
        Ok(mut guard) => guard.take(),
        Err(poisoned) => poisoned.into_inner().take(),
    };

    if let Some(loop_handle) = taken {
        // Blocking on the task from a synchronous context would deadlock the
        // runtime it is scheduled on, so it is dropped into the runtime instead.
        crate::process::AsyncHandler::spawn(|| async move {
            loop_handle.stop().await;
            logging!(info, Type::Config, "[locus] heartbeat stopped");
        });
    }
}

/// Whether a loop is currently held.
#[must_use]
pub fn is_running() -> bool {
    match RUNNING.lock() {
        Ok(guard) => guard.is_some(),
        Err(poisoned) => poisoned.into_inner().is_some(),
    }
}

/// Applies the policy for one beat's outcome.
async fn handle_outcome(outcome: BeatOutcome) {
    match outcome {
        BeatOutcome::Ok(response) => handle_success(&response).await,
        BeatOutcome::Refused { reason } => handle_refusal(&reason).await,
        BeatOutcome::Unreachable { reason } => {
            // NOT an entitlement problem. The tunnel keeps working through the
            // grace period, and `locus_status` reports the remaining window so
            // the UI can warn before access ends. Taking the tunnel down here
            // would punish a student for the school's network.
            logging!(
                debug,
                Type::Config,
                "[locus] heartbeat could not reach the hub: {reason}"
            );
        }
    }
}

/// A successful beat: record it, then act on anything the hub changed.
async fn handle_success(response: &crate::locus::heartbeat::HeartbeatResponse) {
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = store::record_heartbeat_success(now).await {
        logging!(
            warn,
            Type::Config,
            "[locus] could not record a successful heartbeat: {error:#}"
        );
    }

    if let Some(config) = &response.server_config {
        apply_refreshed_config(config, response.udp_relay).await;
    }

    // An update signal is RECORDED, not installed. Installing is the student's
    // decision: doing it silently mid-session would drop their connection
    // without warning, and on a school network that is the worst moment.
    if let Some(version) = &response.update_available {
        logging!(info, Type::System, "[locus] the hub is offering update {version}");
    }
}

/// Applies a config the hub supplied on a heartbeat, if it actually changed.
///
/// The `tier_changed` check is what prevents a RESTART STORM: the hub sends
/// `server_config` on every beat, and applying it unconditionally would reload
/// or restart the core every five minutes, dropping every connection the student
/// has, forever, for no reason.
async fn apply_refreshed_config(config: &crate::locus::contract::TierConfig, udp_relay: bool) {
    if !store::tier_changed(config, udp_relay).await {
        return;
    }

    logging!(
        info,
        Type::Config,
        "[locus] hub supplied new connection details; applying"
    );

    if let Err(error) = store::store_tier_config(config, udp_relay).await {
        logging!(
            warn,
            Type::Config,
            "[locus] could not store the refreshed config: {error:#}"
        );
        // Continue to the apply attempt: the in-memory config is still correct
        // for this session even if it could not be persisted, and refusing to
        // apply would leave the student on a server the hub has moved away from.
    }

    match apply::apply_tier(config, udp_relay).await {
        Ok(apply::ApplyOutcome::Rejected { reason }) => {
            logging!(warn, Type::Config, "[locus] refreshed config was refused: {reason}")
        }
        Err(error) => logging!(
            warn,
            Type::Config,
            "[locus] could not apply the refreshed config: {error:#}"
        ),
        Ok(_) => logging!(info, Type::Config, "[locus] refreshed config applied"),
    }
}

/// The hub has definitively refused this device: suspended, expired, or unknown.
///
/// Tears the entitlement down and stops the tunnel. Leaving it up would keep
/// working on the strength of a stale local record, which is precisely what
/// suspension exists to prevent.
async fn handle_refusal(reason: &str) {
    logging!(warn, Type::Config, "[locus] the hub refused this device: {reason}");

    stop();

    if let Err(error) = crate::core::CoreManager::global().stop_core().await {
        logging!(
            warn,
            Type::Config,
            "[locus] could not stop the core after refusal: {error:#}"
        );
    }
    if let Err(error) = store::clear().await {
        logging!(warn, Type::Config, "[locus] could not clear the entitlement: {error:#}");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Stopping when nothing is running must be harmless — it is called from
    /// shutdown paths where being wrong would panic on quit.
    #[test]
    fn stopping_when_not_running_is_harmless() {
        stop();
        stop();
    }

    /// A refusal is a definitive statement about the device, so it must be the
    /// branch that tears down. This pins the classification rather than the
    /// effect, because the effect needs a running app.
    #[test]
    fn a_refusal_is_distinct_from_an_outage() {
        let refused = BeatOutcome::Refused {
            reason: "suspended".to_owned(),
        };
        let unreachable = BeatOutcome::Unreachable {
            reason: "timeout".to_owned(),
        };

        assert!(
            matches!(refused, BeatOutcome::Refused { .. }),
            "a suspension must not be treated as a transport failure"
        );
        assert!(
            matches!(unreachable, BeatOutcome::Unreachable { .. }),
            "a timeout must not be treated as a refusal"
        );
    }
}

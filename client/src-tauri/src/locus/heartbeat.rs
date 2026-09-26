//! The heartbeat: the client's periodic check-in with the hub.
//!
//! Ports `legacy/wails-client/internal/heartbeat/heartbeat.go`, including its
//! constants — those were tuned against real school networks, not chosen by
//! feel.
//!
//! Three jobs, in order of importance:
//!
//! 1. **Suspension.** An operator can suspend a code at any time, and the next
//!    heartbeat is how the device finds out. Without this the client would keep
//!    working after a refund.
//! 2. **Config refresh.** The hub can push new connection parameters (a moved
//!    server, a rotated password, a tier change) so existing installs self-heal
//!    with no re-activation.
//! 3. **The update signal.** Updates are advertised through the heartbeat, not
//!    a separate poll, gated server-side by a per-device rollout percentage.

use crate::locus::contract::{CodeRequest, HUB_URL, TierConfig};
use serde::Deserialize;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::Notify;

/// How often to beat once things are healthy.
pub const MIN_INTERVAL: Duration = Duration::from_secs(5 * 60);

/// Longest gap between beats, however many failures have accumulated.
pub const MAX_INTERVAL: Duration = Duration::from_secs(2 * 60 * 60);

/// How long the tunnel keeps working without a successful heartbeat.
///
/// This is what makes a suspension eventually bite even if the device never
/// gets a clean heartbeat, and it is the window an operator has to react before
/// a paying student is cut off by an outage. Bounded by expiry being enforced
/// on every heartbeat.
pub const GRACE_PERIOD: Duration = Duration::from_secs(7 * 24 * 60 * 60);

/// Per-attempt timeout. School networks are slow, not absent.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(15);

/// Proportions of jitter applied to each interval.
///
/// Without it, every client that was activated in the same class period beats
/// in lockstep — and a hub that is briefly behind sees a thundering herd the
/// moment it recovers.
const JITTER: f64 = 0.1;

/// The subset of the heartbeat response the client acts on.
#[derive(Debug, Clone, Deserialize)]
pub struct HeartbeatResponse {
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub tier: Option<String>,
    #[serde(default)]
    pub server_config: Option<TierConfig>,
    #[serde(default)]
    pub udp_relay: bool,

    /// When this code lapses, exactly as the hub stores it.
    ///
    /// **Optional, and absence is meaningful.** A hub that predates the field
    /// sends nothing, and a code with no expiry recorded sends `null`; both
    /// deserialise to `None`. The UI must render nothing in that case rather
    /// than guessing "expired" — a fabricated expiry date is worse than a
    /// missing one, because it is a false claim about the student's account that
    /// support then has to argue with.
    ///
    /// Not reformatted here either: it is parsed with [`crate::locus::expiry::parse`]
    /// at the point of display, so there is one parser and one dialect.
    #[serde(default)]
    pub expires_at: Option<String>,

    /// The advertised version, present only when this device is inside the
    /// rollout bucket. Absence is the normal case, not an error.
    #[serde(default)]
    pub update_available: Option<String>,

    /// Per-platform URL and checksum fields. Named `update_*` on the wire while
    /// the record stores `download_*`; the hub translates, and that rename is
    /// frozen. See `contract`.
    #[serde(default)]
    pub update_linux: Option<String>,
    #[serde(default)]
    pub update_windows: Option<String>,
    #[serde(default)]
    pub update_macos_intel: Option<String>,
    #[serde(default)]
    pub update_macos_arm: Option<String>,
    #[serde(default)]
    pub update_sha256_linux: Option<String>,
    #[serde(default)]
    pub update_sha256_windows: Option<String>,
    #[serde(default)]
    pub update_sha256_macos_intel: Option<String>,
    #[serde(default)]
    pub update_sha256_macos_arm: Option<String>,

    /// The legacy single-platform fields, kept for records that predate
    /// per-platform support. They describe the **Linux** binary only, which is
    /// why they are a fallback and never the first choice.
    #[serde(default)]
    pub update_url: Option<String>,
    #[serde(default)]
    pub update_sha256: Option<String>,
}

impl HeartbeatResponse {
    /// The download URL for a platform, falling back to the legacy field.
    ///
    /// The fallback is the historical bug in miniature: a record that only sets
    /// the legacy fields advertises the **Linux** binary to every platform, so
    /// Windows and macOS would download an ELF and fail the checksum. It is
    /// kept because a hub row may still be in that state, but the fork's own
    /// publishes always write per-platform values.
    #[must_use]
    pub fn download_url(&self, platform: &str) -> Option<&str> {
        let per_platform = match platform {
            crate::locus::contract::PLATFORM_LINUX => self.update_linux.as_deref(),
            crate::locus::contract::PLATFORM_WINDOWS => self.update_windows.as_deref(),
            crate::locus::contract::PLATFORM_MACOS_INTEL => self.update_macos_intel.as_deref(),
            crate::locus::contract::PLATFORM_MACOS_ARM => self.update_macos_arm.as_deref(),
            _ => None,
        };
        per_platform.or(self.update_url.as_deref())
    }

    /// The checksum for the artifact [`Self::download_url`] would fetch.
    #[must_use]
    pub fn download_sha256(&self, platform: &str) -> Option<&str> {
        let per_platform = match platform {
            crate::locus::contract::PLATFORM_LINUX => self.update_sha256_linux.as_deref(),
            crate::locus::contract::PLATFORM_WINDOWS => self.update_sha256_windows.as_deref(),
            crate::locus::contract::PLATFORM_MACOS_INTEL => self.update_sha256_macos_intel.as_deref(),
            crate::locus::contract::PLATFORM_MACOS_ARM => self.update_sha256_macos_arm.as_deref(),
            _ => None,
        };
        per_platform.or(self.update_sha256.as_deref())
    }

    /// Whether the hub reported a healthy account.
    ///
    /// A response with any other status is not a successful beat: treating it as
    /// one would reset the backoff and keep a suspended client looking healthy.
    #[must_use]
    pub fn is_ok(&self) -> bool {
        self.status == "ok"
    }
}

/// What a single beat did, so the caller can react.
#[derive(Debug, Clone)]
pub enum BeatOutcome {
    /// The account is in good standing; the payload may carry a config refresh
    /// and/or an update signal.
    Ok(Box<HeartbeatResponse>),
    /// The hub refused: suspended, expired, or unknown. The device is no longer
    /// entitled, and the tunnel should come down.
    Refused { reason: String },
    /// A transport failure. The account is *not* known to be bad — the tunnel
    /// keeps working through the grace period.
    Unreachable { reason: String },
}

/// How many consecutive failures have happened, and therefore how long to wait.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Backoff {
    consecutive_failures: u32,
}

impl Backoff {
    #[must_use]
    pub const fn new() -> Self {
        Self {
            consecutive_failures: 0,
        }
    }

    pub const fn reset(&mut self) {
        self.consecutive_failures = 0;
    }

    pub const fn record_failure(&mut self) {
        // Saturating: past MAX_INTERVAL the delay is capped anyway, and
        // overflowing on a very long outage would be a buffer-overflow class
        // bug in a place nobody is looking.
        self.consecutive_failures = self.consecutive_failures.saturating_add(1);
    }

    #[must_use]
    pub const fn failures(self) -> u32 {
        self.consecutive_failures
    }

    /// The next delay: `MIN * 2^failures`, capped at `MAX`.
    ///
    /// The exponent is clamped before the shift so a long outage cannot
    /// overflow the multiplication.
    #[must_use]
    pub fn delay(self) -> Duration {
        if self.consecutive_failures == 0 {
            return MIN_INTERVAL;
        }
        let exponent = self.consecutive_failures.min(20);
        let scaled = MIN_INTERVAL.saturating_mul(1_u32 << exponent);
        scaled.min(MAX_INTERVAL)
    }

    /// The delay with ±10% jitter applied.
    #[must_use]
    pub fn jittered_delay(self) -> Duration {
        let base = self.delay().as_secs_f64();
        // A cheap deterministic-ish spread derived from the clock. Jitter does
        // not need to be cryptographic; it needs to differ between devices.
        let seed = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_or(0, |d| d.subsec_nanos());
        let unit = f64::from(seed % 1000) / 1000.0; // 0.0..1.0
        let offset = base * JITTER * (2.0 * unit - 1.0); // -10%..+10%
        Duration::from_secs_f64((base + offset).max(1.0))
    }

    /// How much of the grace period is left, given the last success.
    ///
    /// `last_ok == 0` means the device has never had a successful heartbeat, so
    /// the full window applies from first launch. Returning zero there would cut
    /// off a device that has simply never been online.
    #[must_use]
    pub const fn remaining_grace(last_ok: i64, now: i64) -> Duration {
        if last_ok == 0 {
            return GRACE_PERIOD;
        }
        let elapsed = now.saturating_sub(last_ok);
        if elapsed < 0 {
            return GRACE_PERIOD;
        }
        GRACE_PERIOD.saturating_sub(Duration::from_secs(elapsed as u64))
    }
}

impl Default for Backoff {
    fn default() -> Self {
        Self::new()
    }
}

/// Sends one heartbeat. Makes exactly one attempt.
pub async fn beat(code: &str, fingerprint: &str) -> BeatOutcome {
    let body = CodeRequest { code, fingerprint };

    let client = match reqwest::Client::builder()
        .timeout(REQUEST_TIMEOUT)
        .pool_idle_timeout(Duration::from_secs(30))
        .user_agent(concat!("Locus-Client/", env!("CARGO_PKG_VERSION")))
        .build()
    {
        Ok(client) => client,
        Err(error) => {
            return BeatOutcome::Unreachable {
                reason: format!("could not build the hub client: {error}"),
            };
        }
    };

    let response = match client.post(format!("{HUB_URL}/api/heartbeat")).json(&body).send().await {
        Ok(response) => response,
        Err(error) => {
            return BeatOutcome::Unreachable {
                reason: format!("could not reach the hub: {error}"),
            };
        }
    };

    let status = response.status();
    let text = match response.text().await {
        Ok(text) => text,
        Err(error) => {
            return BeatOutcome::Unreachable {
                reason: format!("hub returned an unreadable body ({status}): {error}"),
            };
        }
    };

    // A refusal is a definite answer about the account, so it must not be
    // treated as a transport failure — retrying will not change it, and the
    // tunnel should come down now rather than at the end of the grace period.
    if matches!(status.as_u16(), 403 | 404) {
        let reason = serde_json::from_str::<serde_json::Value>(&text)
            .ok()
            .and_then(|value| value.get("message").and_then(|m| m.as_str()).map(str::to_owned))
            .unwrap_or_else(|| format!("the hub refused this device ({status})"));
        return BeatOutcome::Refused { reason };
    }

    if !status.is_success() {
        return BeatOutcome::Unreachable {
            reason: format!("the hub returned {status}"),
        };
    }

    match serde_json::from_str::<HeartbeatResponse>(&text) {
        Ok(parsed) if parsed.is_ok() => BeatOutcome::Ok(Box::new(parsed)),
        Ok(parsed) => BeatOutcome::Unreachable {
            reason: format!("the hub reported status {:?}", parsed.status),
        },
        Err(error) => BeatOutcome::Unreachable {
            reason: format!("the hub returned an unexpected shape: {error}"),
        },
    }
}

/// The heartbeat loop's control handle.
///
/// Owns the running task so the app can start it once after activation and stop
/// it cleanly on exit. A loop that outlived the app would keep a suspended code
/// looking alive, and a loop that started before activation would beat with no
/// code.
pub struct HeartbeatLoop {
    stop: Arc<Notify>,
    task: tokio::task::JoinHandle<()>,
}

impl HeartbeatLoop {
    /// Runs `on_outcome` after every beat until stopped.
    ///
    /// The callback is the seam the UI and the tunnel use: it receives the
    /// refresh and the update signal, and decides what to apply. Keeping policy
    /// out of here is what stops this module from needing to know about the core
    /// manager.
    pub fn start<F>(code: String, fingerprint: String, on_outcome: F) -> Self
    where
        F: Fn(BeatOutcome) + Send + 'static,
    {
        let stop = Arc::new(Notify::new());
        let stop_signal = Arc::clone(&stop);

        let task = tokio::spawn(async move {
            let mut backoff = Backoff::new();

            loop {
                let outcome = beat(&code, &fingerprint).await;

                match &outcome {
                    BeatOutcome::Ok(_) => backoff.reset(),
                    // A refusal is terminal for this run: the account is not in
                    // good standing, and hammering the hub will not change it.
                    BeatOutcome::Refused { .. } => {
                        on_outcome(outcome);
                        break;
                    }
                    BeatOutcome::Unreachable { .. } => backoff.record_failure(),
                }

                let refused = matches!(outcome, BeatOutcome::Refused { .. });
                on_outcome(outcome);
                if refused {
                    break;
                }

                let delay = backoff.jittered_delay();
                tokio::select! {
                    () = stop_signal.notified() => break,
                    () = tokio::time::sleep(delay) => {}
                }
            }
        });

        Self { stop, task }
    }

    /// Stops the loop and waits for it to finish.
    pub async fn stop(self) {
        self.stop.notify_one();
        let _ = self.task.await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// These constants are tuned against real school networks. Changing one by
    /// feel changes how quickly a suspended student loses access and how much
    /// load a recovering hub takes.
    #[test]
    fn intervals_match_the_deployed_contract() {
        assert_eq!(MIN_INTERVAL, Duration::from_secs(300), "5 minutes");
        assert_eq!(MAX_INTERVAL, Duration::from_secs(7200), "2 hours");
        assert_eq!(GRACE_PERIOD, Duration::from_secs(604_800), "7 days");
    }

    /// Backoff doubles from the minimum and never exceeds the maximum, however
    /// long the outage lasts.
    #[test]
    fn backoff_doubles_then_caps() {
        let mut backoff = Backoff::new();
        assert_eq!(backoff.delay(), MIN_INTERVAL);

        backoff.record_failure();
        assert_eq!(backoff.delay(), MIN_INTERVAL * 2);

        backoff.record_failure();
        assert_eq!(backoff.delay(), MIN_INTERVAL * 4);

        for _ in 0..50 {
            backoff.record_failure();
        }
        assert_eq!(backoff.delay(), MAX_INTERVAL, "a long outage must cap, not overflow");
    }

    /// A success must return the interval to the minimum, or a device that
    /// recovered would keep beating hours apart.
    #[test]
    fn success_resets_the_backoff() {
        let mut backoff = Backoff::new();
        for _ in 0..5 {
            backoff.record_failure();
        }
        assert!(backoff.delay() > MIN_INTERVAL);

        backoff.reset();
        assert_eq!(backoff.delay(), MIN_INTERVAL);
        assert_eq!(backoff.failures(), 0);
    }

    /// Jitter must stay near the base delay: large enough to spread a fleet,
    /// small enough that the interval is still what it claims to be.
    #[test]
    fn jitter_stays_within_ten_percent() {
        let mut backoff = Backoff::new();
        backoff.record_failure();
        let base = backoff.delay().as_secs_f64();

        for _ in 0..200 {
            let jittered = backoff.jittered_delay().as_secs_f64();
            assert!(
                jittered >= base * 0.89 && jittered <= base * 1.11,
                "jittered {jittered} drifted outside ±10% of {base}"
            );
        }
    }

    /// A device that has never beaten gets the full window, not zero — it has
    /// simply never been online.
    #[test]
    fn a_device_that_never_beats_gets_the_whole_grace_period() {
        assert_eq!(Backoff::remaining_grace(0, 1_000_000), GRACE_PERIOD);
    }

    #[test]
    fn grace_counts_down_from_the_last_success() {
        let now = 1_000_000;
        let day = 86_400;

        assert_eq!(Backoff::remaining_grace(now, now), GRACE_PERIOD);
        assert_eq!(
            Backoff::remaining_grace(now - day, now),
            GRACE_PERIOD - Duration::from_secs(day as u64)
        );
    }

    /// Once the window has elapsed the answer is zero, not an underflow — a
    /// wrapped grace period would grant unlimited access.
    #[test]
    fn an_expired_grace_period_is_zero_not_negative() {
        let now = 1_000_000;
        let long_ago = now - 10_000_000;
        assert_eq!(Backoff::remaining_grace(long_ago, now), Duration::ZERO);
    }

    /// A clock that jumps backwards (NTP correction, timezone change) must not
    /// read as "grace expired".
    #[test]
    fn a_backwards_clock_does_not_expire_the_grace_period() {
        let now = 1_000_000;
        let future = now + 5_000;
        assert_eq!(Backoff::remaining_grace(future, now), GRACE_PERIOD);
    }

    const PLATFORM: &str = "linux";

    /// The per-platform field must win over the legacy one.
    #[test]
    fn per_platform_fields_take_precedence() {
        let response = HeartbeatResponse {
            status: "ok".into(),
            tier: None,
            server_config: None,
            udp_relay: false,
            expires_at: None,
            update_available: Some("2.0.0".into()),
            update_linux: Some("https://hub/updates/2.0.0/locus-linux-amd64".into()),
            update_windows: Some("https://hub/updates/2.0.0/locus-windows-amd64.exe".into()),
            update_macos_intel: None,
            update_macos_arm: None,
            update_sha256_linux: Some("aaa".into()),
            update_sha256_windows: Some("bbb".into()),
            update_sha256_macos_intel: None,
            update_sha256_macos_arm: None,
            update_url: Some("https://hub/updates/2.0.0/locus-linux-amd64".into()),
            update_sha256: Some("legacy".into()),
        };

        assert_eq!(
            response.download_url(PLATFORM),
            Some("https://hub/updates/2.0.0/locus-linux-amd64")
        );
        assert_eq!(
            response.download_sha256(PLATFORM),
            Some("aaa"),
            "the per-platform hash must win over the legacy single hash"
        );
    }

    /// A record carrying only the legacy fields is the documented hazard: it
    /// describes the Linux binary, so a Windows client would fetch an ELF. The
    /// fallback exists for compatibility, so it must be *observable*.
    #[test]
    fn the_legacy_fallback_is_used_only_when_the_platform_field_is_absent() {
        let response = HeartbeatResponse {
            status: "ok".into(),
            tier: None,
            server_config: None,
            udp_relay: false,
            expires_at: None,
            update_available: Some("2.0.0".into()),
            update_linux: None,
            update_windows: None,
            update_macos_intel: None,
            update_macos_arm: None,
            update_sha256_linux: None,
            update_sha256_windows: None,
            update_sha256_macos_intel: None,
            update_sha256_macos_arm: None,
            update_url: Some("https://hub/updates/2.0.0/locus-linux-amd64".into()),
            update_sha256: Some("legacy".into()),
        };

        assert_eq!(
            response.download_url(crate::locus::contract::PLATFORM_WINDOWS),
            Some("https://hub/updates/2.0.0/locus-linux-amd64"),
            "the fallback returns the linux URL even for windows — which is why the \
             publish path must always write per-platform fields"
        );
    }

    /// A response the hub considers unhealthy is not a successful beat.
    #[test]
    fn a_non_ok_status_is_not_a_success() {
        let response = HeartbeatResponse {
            status: "degraded".into(),
            tier: None,
            server_config: None,
            udp_relay: false,
            expires_at: None,
            update_available: None,
            update_linux: None,
            update_windows: None,
            update_macos_intel: None,
            update_macos_arm: None,
            update_sha256_linux: None,
            update_sha256_windows: None,
            update_sha256_macos_intel: None,
            update_sha256_macos_arm: None,
            update_url: None,
            update_sha256: None,
        };
        assert!(!response.is_ok());
    }

    /// The live hub response shape, captured from the deployed server, must
    /// parse — including `uot_port` nested inside `server_config`.
    #[test]
    fn parses_a_live_hub_response() {
        let live = r#"{
          "status": "ok",
          "server_time": "2026-09-19T10:09:42.945Z",
          "tier": "strike",
          "server_config": {
            "server": "networkingguides.duckdns.org",
            "server_port": 8445,
            "password": "3467ce5ae756c185f45be91fed2dbeb6",
            "method": "aes-256-gcm",
            "uot_port": 8446
          },
          "udp_relay": true
        }"#;

        let response: HeartbeatResponse = serde_json::from_str(live).expect("the live response shape must parse");

        assert!(response.is_ok());
        assert_eq!(response.tier.as_deref(), Some("strike"));

        let config = response.server_config.expect("server_config must parse");
        assert_eq!(config.server_port, 8445);
        assert_eq!(
            config.uot_port, 8446,
            "nested uot_port must survive deserialisation or the Strike tier loses UDP"
        );
        assert!(config.uot_enabled(response.udp_relay));
    }
}

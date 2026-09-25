//! The Locus wire contract: one definition of every name the hub and the client
//! must agree on.
//!
//! # Why this module exists
//!
//! The retired Wails client earned every entry here the hard way. Two failures
//! in particular are the reason this is a module rather than a habit:
//!
//! 1. **`uot_port` vs `server_port_uot`.** The hub sends the UoT endpoint as
//!    `uot_port` (the tier config JSON is passed through verbatim). The client
//!    declared only `server_port_uot`. Serde ignores unknown fields silently, so
//!    the value never landed, the port stayed `0`, and UDP-over-TCP — the whole
//!    mechanism behind the Strike gaming tier — was dead fleet-wide while both
//!    sides were individually "correct". Nothing caught it because no test
//!    asserted that the client parses the key the server emits.
//! 2. **`download_<platform>` → `update_<platform>`.** The `update_config`
//!    record and the heartbeat response deliberately use different names. The
//!    rename is historical and is now load-bearing: deployed clients read
//!    `update_linux`, so the hook must keep translating. "Simplifying" it breaks
//!    every installed client.
//!
//! Both are the same class of bug: a field name duplicated in three languages
//! (goja JS, Rust, Go) with nothing enforcing agreement. Everything that crosses
//! the wire is therefore named exactly once, here.

use serde::{Deserialize, Serialize};

/// The Locus hub.
///
/// Kept as a constant rather than a setting: the hub also owns the `server`
/// field in every tier config, so a hub move is a server-side change plus this
/// line, never a per-client setting. A client that cannot reach its hub cannot
/// activate, heartbeat, or update.
pub const HUB_URL: &str = "https://networkingguides.duckdns.org";

/// Request body for `/api/activate` and `/api/heartbeat`.
#[derive(Debug, Clone, Serialize)]
pub struct CodeRequest<'a> {
    pub code: &'a str,
    pub fingerprint: &'a str,
}

/// The tier's connection parameters, as the hub emits them.
///
/// The hub passes the `tier_configs.config` JSON through **verbatim**, so the
/// field names here are the stored ones. `uot_port` in particular must not be
/// renamed — see the module docs.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TierConfig {
    pub server: String,
    pub server_port: u16,
    pub password: String,
    pub method: String,

    /// The optional UDP-over-TCP endpoint for this tier.
    ///
    /// **Frozen wire name.** When `> 0` *and* [`ActivateResponse::udp_relay`] is
    /// true, UDP is carried inside the TCP tunnel instead of being sent raw —
    /// which is the only way game/voice traffic survives a school network that
    /// drops UDP.
    ///
    /// The legacy spelling `server_port_uot` is accepted too, so a future
    /// server-side rename cannot break this build the way the original mismatch
    /// did. Accept both, emit neither: the client only ever reads this.
    #[serde(rename = "uot_port", alias = "server_port_uot", default)]
    pub uot_port: u16,
}

impl TierConfig {
    /// Whether this tier should carry UDP over TCP.
    ///
    /// Both halves are required. `udp_relay` alone is an operator editing a
    /// half-configured tier, and `uot_port` alone would point at a listener
    /// nothing advertises; pinning the condition in one place is what stops the
    /// two from being checked inconsistently.
    #[must_use]
    pub const fn uot_enabled(&self, udp_relay: bool) -> bool {
        udp_relay && self.uot_port > 0
    }
}

/// Response body for `POST /api/activate`.
#[derive(Debug, Clone, Deserialize)]
pub struct ActivateResponse {
    /// The hub's own status code, which is what is switched on — the HTTP status
    /// is only inspected separately for 429. See [`ActivationOutcome`].
    #[serde(default)]
    pub code: i32,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub tier: Option<String>,
    #[serde(default)]
    pub device_fingerprint: Option<String>,
    #[serde(default)]
    pub server_config: Option<TierConfig>,
    #[serde(default)]
    pub udp_relay: bool,
}

/// Response body for `POST /api/code-lookup`.
#[derive(Debug, Clone, Deserialize)]
pub struct LookupResponse {
    pub status: LookupStatus,
    #[serde(default)]
    pub tier: Option<String>,
    #[serde(default)]
    pub expires_at: Option<String>,
    #[serde(default)]
    pub message: Option<String>,
}

/// The classification `/api/code-lookup` returns.
///
/// This endpoint exists so a student learns their code is wrong *before*
/// committing to an activation round-trip. It is read-only and never binds.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum LookupStatus {
    /// The code exists and is ready to activate on this device.
    Ok,
    /// The code exists and is bound to no device yet.
    Unbound,
    /// The code is already bound to *this* device — activation will succeed.
    BoundThisDevice,
    /// The code is bound to a different device.
    BoundOther,
    Suspended,
    Expired,
    NotFound,
    /// An older hub that lacks the route, or a transient failure. The caller
    /// falls back to validating at activation time rather than reporting a
    /// failure, because "we could not ask" is not "your code is bad".
    #[serde(other)]
    Unknown,
}

/// Response body for `GET /api/release`.
///
/// The credential-free update manifest: it answers "what is the current public
/// release?" without an activation code. It exists because every other way of
/// learning about a new build runs through the heartbeat, which needs a bound
/// code — so a client whose build is broken *enough that activation fails* could
/// never learn that a fix had been published. The failure prevented escaping
/// the failure.
#[derive(Debug, Clone, Deserialize)]
pub struct ReleaseManifest {
    pub ok: bool,
    #[serde(default)]
    pub published: bool,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub platforms: std::collections::BTreeMap<String, ReleaseAsset>,
}

/// One platform's artifact in the public manifest.
#[derive(Debug, Clone, Deserialize)]
pub struct ReleaseAsset {
    pub url: String,
    #[serde(default)]
    pub sha256: String,
}

/// The platforms `update_config` and the release manifest use as keys.
///
/// **Frozen.** These strings are the contract: the heartbeat hook interpolates
/// them as `response.update_<key>` and the client matches on them.
pub const PLATFORM_LINUX: &str = "linux";
pub const PLATFORM_WINDOWS: &str = "windows";
pub const PLATFORM_MACOS_INTEL: &str = "macos_intel";
pub const PLATFORM_MACOS_ARM: &str = "macos_arm";

/// The artifact filename CI publishes for a platform.
///
/// **Frozen, and deliberately asymmetric.** The *platform key* says `macos_*`
/// while the *filename* says `darwin-*`. That is not an oversight: the hub's
/// release fetcher resolves these exact names from the GitHub Release, so
/// "tidying" one side to match the other breaks publishing for that platform.
///
/// Returns `None` on an unsupported platform rather than guessing, so a caller
/// cannot accidentally fetch another platform's binary.
#[must_use]
pub const fn artifact_for(platform: &str) -> Option<&'static str> {
    match platform.as_bytes() {
        b"linux" => Some("locus-linux-amd64"),
        b"windows" => Some("locus-windows-amd64.exe"),
        b"macos_intel" => Some("locus-darwin-amd64"),
        b"macos_arm" => Some("locus-darwin-arm64"),
        _ => None,
    }
}

/// The platform key for the running build, or `None` if unsupported.
#[must_use]
pub const fn current_platform() -> Option<&'static str> {
    #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
    {
        Some(PLATFORM_LINUX)
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        Some(PLATFORM_WINDOWS)
    }
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        Some(PLATFORM_MACOS_ARM)
    }
    #[cfg(all(target_os = "macos", target_arch = "x86_64"))]
    {
        Some(PLATFORM_MACOS_INTEL)
    }
    #[cfg(not(any(
        all(target_os = "linux", target_arch = "x86_64"),
        all(target_os = "windows", target_arch = "x86_64"),
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "macos", target_arch = "x86_64"),
    )))]
    {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The regression that motivated this module: the hub emits `uot_port`, and
    /// a client that does not land it silently loses UDP-over-TCP for the whole
    /// Strike tier.
    #[test]
    fn the_hub_uot_key_is_parsed_under_its_frozen_name() {
        let live = r#"{
          "server": "networkingguides.duckdns.org",
          "server_port": 8445,
          "password": "3467ce5ae756c185f45be91fed2dbeb6",
          "method": "aes-256-gcm",
          "uot_port": 8446
        }"#;

        let cfg: TierConfig = serde_json::from_str(live).expect("live tier config must parse");
        assert_eq!(
            cfg.uot_port, 8446,
            "the hub sends `uot_port`; failing to read it is the bug that made UoT dead fleet-wide"
        );
        assert!(cfg.uot_enabled(true), "udp_relay + a live port means UoT is on");
    }

    /// The legacy spelling stays accepted so a future server-side rename cannot
    /// break this build in the opposite direction.
    #[test]
    fn the_legacy_spelling_is_accepted_as_a_fallback() {
        let legacy = r#"{
          "server": "example.org", "server_port": 8443,
          "password": "x", "method": "aes-256-gcm",
          "server_port_uot": 8446
        }"#;
        let cfg: TierConfig = serde_json::from_str(legacy).expect("legacy spelling must parse");
        assert_eq!(cfg.uot_port, 8446);
    }

    /// A tier with no UoT endpoint is the normal case for eco and stealth, and
    /// must not be an error.
    #[test]
    fn a_tier_without_uot_parses_and_reports_disabled() {
        let tcp_only = r#"{
          "server": "example.org", "server_port": 8443,
          "password": "x", "method": "aes-256-gcm"
        }"#;
        let cfg: TierConfig = serde_json::from_str(tcp_only).expect("tcp-only tier must parse");
        assert_eq!(cfg.uot_port, 0);
        assert!(!cfg.uot_enabled(true), "no port means no UoT even if the flag is set");
        assert!(!cfg.uot_enabled(false), "no flag means no UoT even if a port exists");
    }

    /// Both halves of the UoT condition are required; an operator toggling one
    /// without the other is editing a half-configured tier.
    #[test]
    fn uot_needs_both_the_flag_and_a_port() {
        let cfg = TierConfig {
            server: "example.org".into(),
            server_port: 8445,
            password: "x".into(),
            method: "aes-256-gcm".into(),
            uot_port: 8446,
        };
        assert!(cfg.uot_enabled(true));
        assert!(!cfg.uot_enabled(false));
    }

    /// The platform key ↔ filename asymmetry is a contract with the publish
    /// pipeline, not an oversight. Pinning it here means a "tidy-up" fails the
    /// build instead of breaking releases for one platform.
    #[test]
    fn artifact_names_keep_the_macos_darwin_asymmetry() {
        assert_eq!(artifact_for(PLATFORM_LINUX), Some("locus-linux-amd64"));
        assert_eq!(artifact_for(PLATFORM_WINDOWS), Some("locus-windows-amd64.exe"));
        assert_eq!(artifact_for(PLATFORM_MACOS_INTEL), Some("locus-darwin-amd64"));
        assert_eq!(artifact_for(PLATFORM_MACOS_ARM), Some("locus-darwin-arm64"));
    }

    /// An unknown platform must not silently resolve to another platform's
    /// binary — that would hand a Windows user a Linux executable.
    #[test]
    fn an_unknown_platform_has_no_artifact() {
        assert_eq!(artifact_for("freebsd"), None);
        assert_eq!(artifact_for("macos"), None);
    }

    /// The running build must be able to name its own platform, or the updater
    /// cannot choose an artifact.
    #[test]
    fn the_running_platform_is_known() {
        assert!(
            current_platform().is_some(),
            "unsupported build target: the updater would have no artifact to fetch"
        );
    }

    /// Every platform the hub can advertise must have an artifact, or a client
    /// on it is offered an update it can never fetch.
    #[test]
    fn every_platform_key_has_an_artifact() {
        for key in [
            PLATFORM_LINUX,
            PLATFORM_WINDOWS,
            PLATFORM_MACOS_INTEL,
            PLATFORM_MACOS_ARM,
        ] {
            assert!(artifact_for(key).is_some(), "no artifact for platform {key}");
        }
    }
}

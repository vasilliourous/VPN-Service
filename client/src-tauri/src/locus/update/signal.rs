//! Decoding the update signal the hub advertises.
//!
//! # Where the signal comes from
//!
//! Updates are advertised **through the heartbeat**, not by a separate poll:
//!
//! ```text
//! heartbeat -> { update_available, update_linux, update_windows, ...,
//!                update_sha256_linux, update_sha256_windows, ... }
//! ```
//!
//! # The rename that must not be "simplified"
//!
//! The `update_config` **record** stores `download_<platform>`, and the
//! **heartbeat response** renames it to `update_<platform>`. That translation
//! happens in the hook, and it is historical — but it is now load-bearing:
//! clients already in the field read `update_linux`, so the hook must keep
//! translating. Making the hook emit `download_linux` directly would silently
//! stop every deployed client from updating.
//!
//! This module is the client half of that contract, so the names live in
//! exactly one place and cannot drift from the hub.

use crate::locus::contract::{PLATFORM_LINUX, PLATFORM_MACOS_ARM, PLATFORM_MACOS_INTEL, PLATFORM_WINDOWS, TierConfig};

/// A complete, actionable update offer.
///
/// Produced only when the signal names **this** platform with a URL *and* a
/// checksum. Anything less is not an offer — see [`decode`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct UpdateOffer {
    pub version: String,
    pub url: String,
    pub sha256: String,
    /// The minisign signature. Required by the plugin, which verifies it
    /// mandatorily; absent means the offer cannot be installed.
    pub signature: String,
}

/// Which platform's field names to read.
///
/// Tests pass an explicit platform so every branch is exercised on any host,
/// rather than only the one the test happens to run on.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Platform {
    Linux,
    Windows,
    MacOsIntel,
    MacOsArm,
}

impl Platform {
    /// The key used in the wire field names.
    #[must_use]
    pub const fn key(self) -> &'static str {
        match self {
            Self::Linux => PLATFORM_LINUX,
            Self::Windows => PLATFORM_WINDOWS,
            Self::MacOsIntel => PLATFORM_MACOS_INTEL,
            Self::MacOsArm => PLATFORM_MACOS_ARM,
        }
    }

    /// The platform this build runs on, or `None` if unsupported.
    #[must_use]
    pub const fn current() -> Option<Self> {
        #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
        {
            Some(Self::Linux)
        }
        #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
        {
            Some(Self::Windows)
        }
        #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
        {
            Some(Self::MacOsArm)
        }
        #[cfg(all(target_os = "macos", target_arch = "x86_64"))]
        {
            Some(Self::MacOsIntel)
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
}

/// Why a signal did not become an offer.
///
/// Every variant is a distinct, loggable situation. The retired client's worst
/// failure was a silent no-op — an update advertised and ignored with nothing
/// anywhere saying so — so the caller is expected to record which of these
/// happened rather than collapsing them into a boolean.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SignalRejection {
    /// The hub advertised nothing. Normal: no release is published.
    NotAdvertised,
    /// The advertised version failed validation, or was not newer.
    VersionNotActionable(String),
    /// The hub advertised a version but no URL for this platform.
    NoUrlForPlatform {
        platform: &'static str,
        fell_back_to_legacy: bool,
    },
    /// A URL exists but no checksum does. **Never installed** — an unverifiable
    /// artifact is worse than no update.
    NoChecksumForPlatform { platform: &'static str },
    /// A URL and hash exist but no signature does. The plugin verifies
    /// mandatorily, so this offer cannot be installed.
    NoSignatureForPlatform { platform: &'static str },
}

impl SignalRejection {
    /// A one-line explanation for a log or a support report.
    #[must_use]
    pub fn describe(&self) -> String {
        match self {
            Self::NotAdvertised => "the hub is advertising no update".to_owned(),
            Self::VersionNotActionable(reason) => reason.clone(),
            Self::NoUrlForPlatform {
                platform,
                fell_back_to_legacy,
            } => {
                if *fell_back_to_legacy {
                    format!(
                        "no download URL for {platform}, and the legacy fallback is absent \
                         (the hub record is incomplete)"
                    )
                } else {
                    format!("the hub advertised a version but published no build for {platform}")
                }
            }
            Self::NoChecksumForPlatform { platform } => {
                format!("no checksum was published for {platform}, so the download cannot be verified")
            }
            Self::NoSignatureForPlatform { platform } => {
                format!("no signature was published for {platform}, so the update cannot be installed")
            }
        }
    }
}

/// The subset of a heartbeat response that carries an update signal.
///
/// A separate type from the heartbeat response so this decoder can be tested
/// against raw JSON, including payloads the hub does not currently emit.
#[derive(Debug, Clone, Default)]
pub struct UpdateSignal {
    pub version: Option<String>,
    pub urls: [Option<String>; 4],
    pub sha256s: [Option<String>; 4],
    pub signatures: [Option<String>; 4],
    /// The legacy single-platform fields. They describe the **Linux** binary
    /// only, which is exactly why they are a last resort.
    pub legacy_url: Option<String>,
    pub legacy_sha256: Option<String>,
}

impl UpdateSignal {
    const fn index(platform: Platform) -> usize {
        match platform {
            Platform::Linux => 0,
            Platform::Windows => 1,
            Platform::MacOsIntel => 2,
            Platform::MacOsArm => 3,
        }
    }

    /// The URL for a platform, preferring the per-platform field.
    ///
    /// Falling back to the legacy field is the historical hazard in miniature:
    /// a record that sets only the legacy fields advertises the **Linux** binary
    /// to every platform, so Windows and macOS would download an ELF and fail.
    /// The fallback is kept because hub rows in that state may still exist, but
    /// the caller is told when it was used so the situation is visible.
    #[must_use]
    pub fn url_for(&self, platform: Platform) -> (Option<&str>, bool) {
        match self.urls[Self::index(platform)].as_deref() {
            Some(url) => (Some(url), false),
            None => (self.legacy_url.as_deref(), true),
        }
    }

    /// The checksum for a platform, preferring the per-platform field.
    #[must_use]
    pub fn sha256_for(&self, platform: Platform) -> Option<&str> {
        self.sha256s[Self::index(platform)]
            .as_deref()
            .or(self.legacy_sha256.as_deref())
    }

    /// The signature for a platform. No legacy fallback exists — signatures
    /// post-date the single-platform record shape.
    #[must_use]
    pub fn signature_for(&self, platform: Platform) -> Option<&str> {
        self.signatures[Self::index(platform)].as_deref()
    }
}

/// Turns a raw heartbeat JSON object into an actionable offer for `platform`.
///
/// `current_version` is required because "an update is available" is only
/// meaningful relative to what is running, and the hub cannot be trusted to
/// advertise only newer versions — a stale or rolled-back `update_config` row
/// would otherwise downgrade every client, and there is no server-driven
/// downgrade to recover from.
///
/// # Errors
///
/// Returns the specific [`SignalRejection`] so the caller can log *why* nothing
/// happened. An update that is advertised and silently ignored is the failure
/// mode this exists to prevent.
pub fn decode(
    response: &serde_json::Value,
    platform: Platform,
    current_version: &str,
) -> std::result::Result<UpdateOffer, SignalRejection> {
    let version = response
        .get("update_available")
        .and_then(|v| v.as_str())
        .filter(|v| !v.is_empty())
        .ok_or(SignalRejection::NotAdvertised)?;

    // Refuse anything not strictly newer, with a reason, before doing any work.
    if let Some(reason) = super::version::rejection_reason(version, current_version) {
        return Err(SignalRejection::VersionNotActionable(reason));
    }

    let signal = extract(response);

    let (url, fell_back) = signal.url_for(platform);
    let Some(url) = url.filter(|u| !u.is_empty()) else {
        return Err(SignalRejection::NoUrlForPlatform {
            platform: platform.key(),
            fell_back_to_legacy: fell_back,
        });
    };

    // Fail closed. An artifact we cannot verify must never be installed, so a
    // missing checksum is a refusal, not a warning.
    let Some(sha256) = signal.sha256_for(platform).filter(|s| !s.is_empty()) else {
        return Err(SignalRejection::NoChecksumForPlatform {
            platform: platform.key(),
        });
    };

    // The plugin verifies the signature mandatorily and offers no bypass, so an
    // offer without one is not installable. Refusing here means the UI can say
    // so, instead of the failure surfacing as an opaque installer error later.
    let Some(signature) = signal.signature_for(platform).filter(|s| !s.is_empty()) else {
        return Err(SignalRejection::NoSignatureForPlatform {
            platform: platform.key(),
        });
    };

    Ok(UpdateOffer {
        version: version.to_owned(),
        url: url.to_owned(),
        sha256: sha256.to_owned(),
        signature: signature.to_owned(),
    })
}

/// Reads the wire fields out of a response object.
///
/// Kept separate from [`decode`] so the field-name contract is testable on its
/// own, against the exact names the hook emits.
fn extract(response: &serde_json::Value) -> UpdateSignal {
    let field = |name: &str| -> Option<String> {
        response
            .get(name)
            .and_then(|v| v.as_str())
            .filter(|v| !v.is_empty())
            .map(str::to_owned)
    };

    let mut urls = [const { None }; 4];
    let mut sha256s = [const { None }; 4];
    let mut signatures = [const { None }; 4];

    for platform in [
        Platform::Linux,
        Platform::Windows,
        Platform::MacOsIntel,
        Platform::MacOsArm,
    ] {
        let i = UpdateSignal::index(platform);
        let key = platform.key();
        urls[i] = field(&format!("update_{key}"));
        sha256s[i] = field(&format!("update_sha256_{key}"));
        signatures[i] = field(&format!("update_signature_{key}"));
    }

    UpdateSignal {
        version: field("update_available"),
        urls,
        sha256s,
        signatures,
        legacy_url: field("update_url"),
        legacy_sha256: field("update_sha256"),
    }
}

/// Builds a signal from the *record* field names, for the public manifest path.
///
/// The public manifest (`/api/release`) uses the record's own names rather than
/// the heartbeat's renamed ones, so the two need separate extraction. Having
/// both here keeps the rename visible in one file.
#[must_use]
pub fn decode_public_manifest(manifest: &serde_json::Value, platform: Platform) -> Option<UpdateOffer> {
    let version = manifest.get("version")?.as_str()?;
    if version.is_empty() {
        return None;
    }

    let entry = manifest.get("platforms")?.get(platform.key())?;
    let url = entry.get("url")?.as_str().filter(|s| !s.is_empty())?;
    let sha256 = entry.get("sha256")?.as_str().filter(|s| !s.is_empty())?;
    let signature = entry.get("signature")?.as_str().filter(|s| !s.is_empty())?;

    Some(UpdateOffer {
        version: version.to_owned(),
        url: url.to_owned(),
        sha256: sha256.to_owned(),
        signature: signature.to_owned(),
    })
}

/// The tier config's server, exposed so callers do not need a second import.
pub type Tier = TierConfig;

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const RUNNING: &str = "2.2.0";

    /// A realistic payload for a Windows client, with every platform present.
    fn full_signal() -> serde_json::Value {
        json!({
            "status": "ok",
            "tier": "eco",
            "update_available": "2.3.0",
            "update_linux": "https://hub/updates/2.3.0/locus-linux-amd64",
            "update_windows": "https://hub/updates/2.3.0/locus-windows-amd64.exe",
            "update_macos_intel": "https://hub/updates/2.3.0/locus-darwin-amd64",
            "update_macos_arm": "https://hub/updates/2.3.0/locus-darwin-arm64",
            "update_sha256_linux": "aaaa",
            "update_sha256_windows": "bbbb",
            "update_sha256_macos_intel": "cccc",
            "update_sha256_macos_arm": "dddd",
            "update_signature_linux": "sig-linux",
            "update_signature_windows": "sig-windows",
            "update_signature_macos_intel": "sig-macos-intel",
            "update_signature_macos_arm": "sig-macos-arm"
        })
    }

    /// Every platform must resolve to its own artifact. A cross-wired field
    /// would hand a Windows user a Linux binary.
    #[test]
    fn each_platform_resolves_its_own_artifact() {
        let response = full_signal();

        let cases = [
            (Platform::Linux, "locus-linux-amd64", "aaaa", "sig-linux"),
            (Platform::Windows, "locus-windows-amd64.exe", "bbbb", "sig-windows"),
            (Platform::MacOsIntel, "locus-darwin-amd64", "cccc", "sig-macos-intel"),
            (Platform::MacOsArm, "locus-darwin-arm64", "dddd", "sig-macos-arm"),
        ];

        for (platform, filename, hash, signature) in cases {
            let offer = decode(&response, platform, RUNNING)
                .unwrap_or_else(|e| panic!("{platform:?} should decode: {}", e.describe()));
            assert!(
                offer.url.ends_with(filename),
                "{platform:?} resolved to {} instead of {filename}",
                offer.url
            );
            assert_eq!(offer.sha256, hash, "{platform:?} took the wrong hash");
            assert_eq!(offer.signature, signature, "{platform:?} took the wrong signature");
        }
    }

    /// No advertisement is the normal state, not an error.
    #[test]
    fn no_advertised_version_is_not_an_offer() {
        let response = json!({ "status": "ok", "tier": "eco" });
        assert_eq!(
            decode(&response, Platform::Windows, RUNNING),
            Err(SignalRejection::NotAdvertised)
        );
    }

    /// The no-downgrade rule: a stale or rolled-back row must not make the
    /// client replace itself with an older build.
    #[test]
    fn an_older_advertised_version_is_refused() {
        let mut response = full_signal();
        response["update_available"] = json!("2.0.0");

        match decode(&response, Platform::Windows, "2.2.0") {
            Err(SignalRejection::VersionNotActionable(reason)) => {
                assert!(reason.contains("2.0.0"));
                assert!(reason.contains("2.2.0"));
            }
            other => panic!("expected a version rejection, got {other:?}"),
        }
    }

    /// Re-advertising the running version must not trigger a reinstall.
    #[test]
    fn the_running_version_is_refused() {
        let mut response = full_signal();
        response["update_available"] = json!("2.2.0");
        assert!(matches!(
            decode(&response, Platform::Windows, "2.2.0"),
            Err(SignalRejection::VersionNotActionable(_))
        ));
    }

    /// A URL with no checksum must never be installed. Failing closed on a
    /// missing hash is the old client's explicit rule, kept deliberately.
    #[test]
    fn a_missing_checksum_fails_closed() {
        let mut response = full_signal();
        response
            .as_object_mut()
            .expect("object")
            .remove("update_sha256_windows");
        response.as_object_mut().expect("object").remove("update_sha256");

        assert_eq!(
            decode(&response, Platform::Windows, RUNNING),
            Err(SignalRejection::NoChecksumForPlatform { platform: "windows" })
        );
    }

    /// A missing signature is equally fatal: the plugin cannot install without
    /// one, so refusing here turns an opaque installer failure into a clear
    /// state the UI can explain.
    #[test]
    fn a_missing_signature_is_refused() {
        let mut response = full_signal();
        response
            .as_object_mut()
            .expect("object")
            .remove("update_signature_windows");

        assert_eq!(
            decode(&response, Platform::Windows, RUNNING),
            Err(SignalRejection::NoSignatureForPlatform { platform: "windows" })
        );
    }

    /// A platform the hub published nothing for is refused with the platform
    /// named, so a support log says which platform was missing.
    #[test]
    fn a_missing_platform_url_is_refused() {
        let mut response = full_signal();
        response.as_object_mut().expect("object").remove("update_macos_arm");

        match decode(&response, Platform::MacOsArm, RUNNING) {
            Err(SignalRejection::NoUrlForPlatform { platform, .. }) => {
                assert_eq!(platform, "macos_arm");
            }
            other => panic!("expected a missing-URL rejection, got {other:?}"),
        }
    }

    /// The legacy fallback still resolves the URL and hash, so a hub row
    /// predating per-platform support does not strand clients entirely — but the
    /// caller is told it was used, because it describes the **Linux** binary.
    ///
    /// Note the offer is still refused overall: signatures post-date the legacy
    /// record shape, so a legacy row cannot produce an installable offer. That
    /// is correct and deliberate — it is why the fallback resolves URL and hash
    /// only, and this test asserts that rather than pretending otherwise.
    #[test]
    fn the_legacy_fallback_resolves_url_and_hash_and_is_reported() {
        let response = json!({
            "status": "ok",
            "update_available": "2.3.0",
            "update_url": "https://hub/updates/2.3.0/locus-linux-amd64",
            "update_sha256": "legacyhash"
        });

        let signal = extract(&response);

        let (url, fell_back) = signal.url_for(Platform::Linux);
        assert!(fell_back, "the caller must be able to see that the fallback was used");
        assert!(url.expect("legacy url").ends_with("locus-linux-amd64"));
        assert_eq!(signal.sha256_for(Platform::Linux), Some("legacyhash"));

        // For Windows the same record resolves to the LINUX url, which is the
        // hazard: it will fail the checksum rather than install the wrong thing.
        let (url_windows, fell_back_windows) = signal.url_for(Platform::Windows);
        assert!(fell_back_windows);
        assert!(url_windows.expect("fallback url").contains("linux"));

        // And the overall decode still refuses, because there is no signature.
        assert_eq!(
            decode(&response, Platform::Linux, RUNNING),
            Err(SignalRejection::NoSignatureForPlatform { platform: "linux" })
        );
    }

    /// A record with a legacy hash but no legacy URL, and no per-platform URL,
    /// must be reported as such rather than silently doing nothing.
    #[test]
    fn a_record_missing_the_platform_url_reports_that_it_fell_back() {
        let response = json!({
            "status": "ok",
            "update_available": "2.3.0",
            "update_sha256": "legacyhash"
        });

        match decode(&response, Platform::Windows, RUNNING) {
            Err(SignalRejection::NoUrlForPlatform {
                platform,
                fell_back_to_legacy,
            }) => {
                assert_eq!(platform, "windows");
                assert!(
                    fell_back_to_legacy,
                    "the reason must distinguish 'no platform build' from 'nothing at all'"
                );
            }
            other => panic!("expected a missing-URL rejection, got {other:?}"),
        }
    }

    /// An advertised version containing a path separator must never reach a URL
    /// or a filename.
    #[test]
    fn a_malformed_advertised_version_is_refused() {
        let mut response = full_signal();
        response["update_available"] = json!("../../etc/passwd");

        assert!(matches!(
            decode(&response, Platform::Windows, RUNNING),
            Err(SignalRejection::VersionNotActionable(_))
        ));
    }

    /// Empty strings are treated as absent rather than as values, since a hook
    /// writing `""` into a column is the same as it not being there.
    #[test]
    fn empty_strings_count_as_absent() {
        let response = json!({
            "status": "ok",
            "update_available": "2.3.0",
            "update_windows": "",
            "update_sha256_windows": "",
            "update_signature_windows": ""
        });
        assert!(matches!(
            decode(&response, Platform::Windows, RUNNING),
            Err(SignalRejection::NoUrlForPlatform { .. })
        ));
    }

    /// The public manifest uses the record's own field names, so it needs its
    /// own extraction — this pins that both spellings resolve.
    #[test]
    fn the_public_manifest_uses_the_record_field_names() {
        let manifest = json!({
            "ok": true,
            "published": true,
            "version": "2.3.0",
            "platforms": {
                "linux": { "url": "https://hub/updates/2.3.0/locus-linux-amd64", "sha256": "aa", "signature": "s1" },
                "windows": { "url": "https://hub/updates/2.3.0/locus-windows-amd64.exe", "sha256": "bb", "signature": "s2" }
            }
        });

        let offer = decode_public_manifest(&manifest, Platform::Windows).expect("the manifest should produce an offer");
        assert_eq!(offer.version, "2.3.0");
        assert!(offer.url.ends_with("locus-windows-amd64.exe"));
        assert_eq!(offer.sha256, "bb");
        assert_eq!(offer.signature, "s2");

        assert!(
            decode_public_manifest(&manifest, Platform::MacOsArm).is_none(),
            "a platform absent from the manifest must yield nothing, not another platform's build"
        );
    }

    /// An unpublished manifest is a normal state and must yield nothing.
    #[test]
    fn an_unpublished_manifest_yields_nothing() {
        let manifest = json!({ "ok": true, "published": false });
        assert!(decode_public_manifest(&manifest, Platform::Linux).is_none());
    }

    /// The running build must be able to name its own platform, or it could
    /// never select an artifact.
    #[test]
    fn the_running_platform_is_known() {
        assert!(
            Platform::current().is_some(),
            "unsupported build target: the updater would have no artifact to fetch"
        );
    }

    /// Every rejection must explain itself — the message is what a support
    /// report will quote.
    #[test]
    fn every_rejection_describes_itself() {
        let rejections = [
            SignalRejection::NotAdvertised,
            SignalRejection::VersionNotActionable("not newer".into()),
            SignalRejection::NoUrlForPlatform {
                platform: "windows",
                fell_back_to_legacy: false,
            },
            SignalRejection::NoChecksumForPlatform { platform: "windows" },
            SignalRejection::NoSignatureForPlatform { platform: "windows" },
        ];
        for rejection in rejections {
            let text = rejection.describe();
            assert!(!text.is_empty(), "{rejection:?} produced an empty description");
        }
    }
}

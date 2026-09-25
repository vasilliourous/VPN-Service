//! Driving the plugin's installer from Locus update metadata.
//!
//! # The mechanism (corrected against the plugin's source, not its docs)
//!
//! `tauri_plugin_updater::Update` **cannot be constructed by hand**: two of its
//! fields (`extract_path`, `context`) are private, so a struct literal from
//! outside the crate does not compile. The only way to obtain one is
//! `Updater::check()`.
//!
//! That is fine, because `check()` accepts a **dynamic** response shape:
//!
//! ```json
//! { "version": "2.3.0", "url": "https://…", "signature": "…" }
//! ```
//!
//! and `UpdaterBuilder::endpoints(..)` **replaces** the configured endpoint list
//! rather than appending to it. So the plugin can be pointed at a URL we build
//! at runtime, and fed a manifest the hub serves — while still owning the
//! download, the mandatory minisign verification, and the platform install.
//!
//! # What that means for the architecture
//!
//! ```text
//! heartbeat / entitlement  ->  the hub decides who is offered what
//!                          ->  GET /api/update?version=<running>   (our endpoint)
//!                          ->  { version, url, signature }         (dynamic shape)
//!                          ->  UpdaterBuilder::endpoints([ours])   (runtime)
//!                          ->  check() -> Update -> install()      (the plugin's job)
//! ```
//!
//! The client keeps its own [`super::version`] gate and [`super::signal`]
//! decoder for *deciding* whether to offer an update at all, and for the
//! credential-free fallback. The plugin is used strictly for download + verify +
//! install.
//!
//! # Why this is better than the retired client
//!
//! The old client hand-rolled the swap: backup, rename, fork, sentinel-revert.
//! That destroyed an installation (FIXES #22) and staged into the current
//! working directory (FIXES #54). None of it is ported. The plugin's installer
//! is already atomic on all three platforms.

use anyhow::{Context as _, Result};

/// Where the plugin should look for an update manifest.
///
/// Built at runtime rather than baked into `tauri.conf.json`, because the URL
/// carries the running version and the platform — both of which the hub needs
/// to answer correctly. Keeping `endpoints` empty in the config is deliberate:
/// it means a stale static manifest cannot be consulted by accident.
#[must_use]
pub fn manifest_endpoint(hub_url: &str, running_version: &str, platform: &str) -> String {
    format!(
        "{}/api/update?version={}&platform={}",
        hub_url.trim_end_matches('/'),
        encode(running_version),
        encode(platform)
    )
}

/// Percent-encodes a query value.
///
/// Hand-rolled rather than pulling a dependency for two call sites. Only the
/// characters that can appear in a version string or a platform key need
/// handling, and everything else is passed through.
fn encode(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-' | b'_' | b'~') {
            out.push(char::from(byte));
        } else {
            out.push('%');
            out.push_str(&format!("{byte:02X}"));
        }
    }
    out
}

/// An installer handed to the plugin, ready to run.
pub struct PendingInstall {
    update: tauri_plugin_updater::Update,
}

impl PendingInstall {
    /// Asks the hub what this device should install, then validates the answer.
    ///
    /// `current_version` gates the result locally as well as on the hub: the hub
    /// decides, but a stale or misconfigured row must not be able to downgrade a
    /// client, so the version comparison is repeated here. There is no
    /// server-driven downgrade in this system, so refusing is the only correct
    /// behaviour.
    ///
    /// # Errors
    ///
    /// Returns `Ok(None)` when there is nothing to install — which is the normal
    /// state, not a failure. A transport error is returned as `Err`, because the
    /// caller must distinguish "no update" from "could not ask".
    pub async fn check(app: &tauri::AppHandle, hub_url: &str, current_version: &str) -> Result<Option<Self>> {
        use tauri_plugin_updater::UpdaterExt as _;

        let Some(platform) = crate::locus::update::Platform::current() else {
            anyhow::bail!("this build target is not supported by the updater");
        };

        let endpoint = manifest_endpoint(hub_url, current_version, platform.key());
        let url = url::Url::parse(&endpoint)
            .with_context(|| format!("the update endpoint is not a valid URL: {endpoint}"))?;

        let updater = app
            .updater_builder()
            // Replace the configured endpoint list entirely: nothing should ever
            // consult a static manifest.
            .endpoints(vec![url])
            .context("the update endpoint was rejected")?
            .build()
            .context("could not build the updater")?;

        let Some(update) = updater.check().await.context("the update check failed")? else {
            return Ok(None);
        };

        // Second gate. The hub owns rollout and entitlement, but a version that
        // is not strictly newer must never be installed, whoever advertised it.
        if let Some(reason) = crate::locus::update::rejection_reason(&update.version, current_version) {
            logging_rejection(&reason);
            return Ok(None);
        }

        // The plugin will refuse to install without a signature anyway, so
        // catching it here turns an opaque installer error into a clear state.
        if update.signature.trim().is_empty() {
            logging_rejection("the hub offered an update with no signature");
            return Ok(None);
        }

        Ok(Some(Self { update }))
    }

    /// The version that would be installed.
    #[must_use]
    pub fn version(&self) -> &str {
        &self.update.version
    }

    /// Downloads and installs, reporting progress through `on_chunk`.
    ///
    /// On Windows the plugin launches NSIS and then exits the app, so a
    /// successful return may not happen. Callers must treat completion as
    /// "expect a restart" rather than assuming the process survives.
    ///
    /// # Errors
    ///
    /// Returns an error if the download fails, if **either** integrity check
    /// fails (SHA-256 inside the plugin, minisign signature), or if the
    /// installer refuses.
    pub async fn install<C>(self, on_chunk: C) -> Result<()>
    where
        C: FnMut(usize, Option<u64>) + Send + 'static,
    {
        self.update
            .download_and_install(on_chunk, || {})
            .await
            .context("the update could not be installed")
    }
}

/// Records why an offered update was not taken.
///
/// A decision log at every branch is the client-side defence against the
/// retired client's worst failure mode: an update advertised and silently
/// ignored, with nothing anywhere saying so.
fn logging_rejection(reason: &str) {
    clash_verge_logging::logging!(
        warn,
        clash_verge_logging::Type::System,
        "[locus] ignoring the offered update: {reason}"
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The endpoint must carry the version and platform, or the hub cannot
    /// answer for the right client.
    #[test]
    fn the_endpoint_carries_version_and_platform() {
        let url = manifest_endpoint("https://hub.example.org", "2.2.0", "windows");
        assert!(url.starts_with("https://hub.example.org/api/update?"));
        assert!(url.contains("version=2.2.0"));
        assert!(url.contains("platform=windows"));
    }

    /// A trailing slash must not produce a doubled separator.
    #[test]
    fn a_trailing_slash_is_tolerated() {
        let with = manifest_endpoint("https://hub.example.org/", "2.2.0", "linux");
        let without = manifest_endpoint("https://hub.example.org", "2.2.0", "linux");
        assert_eq!(with, without);
        assert!(!with.contains("//api"), "the path must not be doubled: {with}");
    }

    /// A pre-release version contains characters that must survive encoding
    /// intact, or the hub sees a different version than the client is running.
    #[test]
    fn prerelease_versions_survive_encoding() {
        let url = manifest_endpoint("https://hub.example.org", "2.3.0-rc1+build5", "linux");
        assert!(
            url.contains("2.3.0-rc1%2Bbuild5"),
            "the + in build metadata must be encoded, got {url}"
        );
    }

    #[test]
    fn encoding_passes_through_safe_characters() {
        assert_eq!(encode("2.2.0-rc1"), "2.2.0-rc1");
        assert_eq!(encode("windows"), "windows");
        assert_eq!(encode("macos_arm"), "macos_arm");
    }

    #[test]
    fn encoding_escapes_unsafe_characters() {
        assert_eq!(encode("a+b"), "a%2Bb");
        assert_eq!(encode("a b"), "a%20b");
        assert_eq!(encode("a&b"), "a%26b");
        assert_eq!(encode("a/b"), "a%2Fb");
        assert_eq!(encode("a?b"), "a%3Fb");
    }

    /// A version from the hub could contain a query-breaking character; it must
    /// not be able to alter the request.
    #[test]
    fn an_injected_version_cannot_add_query_parameters() {
        let url = manifest_endpoint("https://hub.example.org", "1.0.0&admin=1", "linux");
        assert!(!url.contains("&admin=1"), "an injected & must be encoded, got {url}");
        assert!(url.contains("%261.0.0") || url.contains("%26admin"));
    }
}

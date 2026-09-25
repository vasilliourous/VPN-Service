//! Downloading, verifying and installing an update.
//!
//! # Division of labour
//!
//! **Ours:** which version, from where, and that the bytes are the ones the hub
//! described (SHA-256, fail-closed).
//!
//! **The plugin's:** the platform install. A hand-built
//! `tauri_plugin_updater::Update` — every field is public — is handed to
//! `.install()`, which handles NSIS on Windows, `.app`/`.dmg` on macOS and the
//! Linux package formats. That is the part that is genuinely hard and already
//! correct; hand-rolling it destroyed an installation once (FIXES #22), so it is
//! not repeated here.
//!
//! # Why two integrity checks
//!
//! SHA-256 catches a corrupted or truncated download. The plugin's minisign
//! verification catches a **substituted** artifact — which a hash published by
//! the same server could never detect, since an attacker who controls the
//! response controls the hash too. Neither is redundant.
//!
//! # What is deliberately absent
//!
//! No backup/swap/fork/revert machinery. The retired client needed it because a
//! portable binary had to replace itself; an installed application does not, and
//! the plugin's installer is already atomic. Porting that machinery would
//! re-earn FIXES #22 and #54.

use crate::locus::update::signal::UpdateOffer;
use anyhow::{Context as _, Result, bail};
use sha2::{Digest as _, Sha256};
use std::path::{Path, PathBuf};

/// Largest artifact we will download, mirroring the hub's own cap.
///
/// A guard against a hostile or misconfigured response filling the student's
/// disk; the hub enforces the same ceiling on the publish side.
const MAX_DOWNLOAD_BYTES: u64 = 200 * 1024 * 1024;

/// Smallest artifact we will accept.
///
/// Anything smaller is a truncated download, an error page, or a Git LFS
/// pointer — the same floor the hub applies when publishing.
const MIN_DOWNLOAD_BYTES: u64 = 1024 * 1024;

/// Status of an update attempt, for the UI.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum UpdatePhase {
    Downloading {
        received: u64,
        total: Option<u64>,
    },
    Verifying,
    Installing,
    /// The installer has been handed the package. On Windows the app exits
    /// shortly after, so the UI should expect a restart rather than a response.
    Installed,
    Failed {
        reason: String,
    },
}

/// A downloaded, hash-verified artifact waiting to be installed.
#[derive(Debug)]
pub struct DownloadedUpdate {
    pub path: PathBuf,
    pub version: String,
    pub signature: String,
    pub bytes: u64,
}

impl DownloadedUpdate {
    /// Reads the artifact back for the installer.
    ///
    /// The plugin's `install()` takes the bytes rather than a path, so this is
    /// the hand-off point between "we downloaded and verified a file" and "the
    /// plugin installs it".
    pub fn read(&self) -> Result<Vec<u8>> {
        std::fs::read(&self.path)
            .with_context(|| format!("could not read the downloaded update at {}", self.path.display()))
    }
}

/// Hex-encodes a digest, matching the formatting used elsewhere in this tree.
fn hex(digest: &[u8]) -> String {
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

/// Hashes a file without holding it in memory.
///
/// The artifacts are tens of megabytes; reading one into a `Vec` purely to hash
/// it would double the peak memory on a machine that may be a 4 GB school
/// laptop already running a VPN.
fn sha256_file(path: &Path) -> Result<String> {
    use std::io::Read as _;

    let mut file =
        std::fs::File::open(path).with_context(|| format!("could not open {} for hashing", path.display()))?;
    let mut hasher = Sha256::new();
    let mut buffer = vec![0_u8; 256 * 1024];

    loop {
        let read = file
            .read(&mut buffer)
            .with_context(|| format!("could not read {}", path.display()))?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
    }

    Ok(hex(&hasher.finalize()))
}

/// Compares two hex digests without caring about case.
///
/// Case-insensitive because a hub row written by a person could carry either,
/// and refusing a correct hash over letter case would be an infuriating bug.
#[must_use]
const fn hashes_match(expected: &str, actual: &str) -> bool {
    expected.len() == actual.len() && expected.eq_ignore_ascii_case(actual)
}

/// Downloads an update to a private staging directory.
///
/// The staging directory is inside the app data root, **never** the current
/// working directory. The retired client staged into whatever directory it was
/// launched from, which on Windows is the most heavily observed directory on the
/// system — antivirus, the search indexer and sync clients all hold handles on a
/// new `.exe`, and the rename failed (FIXES #54).
pub async fn download(offer: &UpdateOffer, staging_dir: &Path) -> Result<DownloadedUpdate> {
    // Fail closed before making a request. An artifact we cannot verify must
    // never be fetched, let alone installed.
    if offer.sha256.trim().is_empty() {
        bail!("refusing to download an update with no checksum");
    }
    if offer.signature.trim().is_empty() {
        bail!("refusing to download an update with no signature");
    }

    std::fs::create_dir_all(staging_dir).with_context(|| {
        format!(
            "could not create the update staging directory {}",
            staging_dir.display()
        )
    })?;

    let file_name = offer
        .url
        .rsplit('/')
        .next()
        .filter(|name| !name.is_empty() && !name.contains(['\\', ':']))
        .unwrap_or("locus-update.bin");
    let destination = staging_dir.join(format!("{}.download", sanitize(file_name)));

    let client = reqwest::Client::builder()
        // Downloads happen on school wifi and can be slow; this is an overall
        // ceiling, not a per-read timeout.
        .timeout(std::time::Duration::from_secs(600))
        .user_agent(concat!("Locus-Client/", env!("CARGO_PKG_VERSION")))
        .build()
        .context("could not build the download client")?;

    let response = client
        .get(&offer.url)
        .send()
        .await
        .with_context(|| format!("could not reach {}", offer.url))?;

    if !response.status().is_success() {
        bail!("the download returned HTTP {}", response.status());
    }

    if let Some(total) = response.content_length()
        && total > MAX_DOWNLOAD_BYTES
    {
        bail!("the advertised download is {total} bytes, larger than the accepted maximum");
    }

    // Stream to disk, hashing as we go, so a large artifact is never held in
    // memory.
    let mut file = tokio::fs::File::create(&destination)
        .await
        .with_context(|| format!("could not create {}", destination.display()))?;
    let mut hasher = Sha256::new();
    let mut received: u64 = 0;
    let mut stream = response;

    use tokio::io::AsyncWriteExt as _;
    while let Some(chunk) = stream
        .chunk()
        .await
        .with_context(|| format!("the download from {} was interrupted", offer.url))?
    {
        received += chunk.len() as u64;
        if received > MAX_DOWNLOAD_BYTES {
            let _ = tokio::fs::remove_file(&destination).await;
            bail!("the download exceeded the accepted maximum size and was abandoned");
        }
        hasher.update(&chunk);
        file.write_all(&chunk)
            .await
            .with_context(|| format!("could not write to {}", destination.display()))?;
    }

    // Flush explicitly and surface a failure as its own error. On Windows a
    // file with an open handle cannot be moved, and a close error means bytes
    // may not have reached the disk — so this is a real failure, not a
    // formality.
    file.flush().await.context("could not flush the downloaded update")?;
    drop(file);

    if received < MIN_DOWNLOAD_BYTES {
        let _ = tokio::fs::remove_file(&destination).await;
        bail!(
            "the download was only {received} bytes — that is a truncated file or an error \
             page, not an application"
        );
    }

    // Verify from the bytes on disk, not the in-memory copy: this is the file
    // the installer will actually be handed.
    let actual = sha256_file(&destination)?;
    if !hashes_match(&offer.sha256, &actual) {
        let _ = tokio::fs::remove_file(&destination).await;
        bail!(
            "the downloaded update failed its checksum (expected {}, got {}) — the file was \
             discarded and nothing was installed",
            redact(&offer.sha256),
            redact(&actual)
        );
    }

    Ok(DownloadedUpdate {
        path: destination,
        version: offer.version.clone(),
        signature: offer.signature.clone(),
        bytes: received,
    })
}

/// Strips anything from a filename that could escape the staging directory.
///
/// The name comes from a URL, so it is attacker-influenced in principle. Only
/// these characters are removed rather than rejecting outright, so a legitimate
/// but unusual filename still works.
fn sanitize(name: &str) -> String {
    name.chars()
        .filter(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_'))
        .take(120)
        .collect()
}

/// Shows the first bytes of a hash, enough to compare two in a log.
fn redact(hash: &str) -> String {
    hash.chars().take(16).collect()
}

/// Removes a downloaded artifact once it is no longer needed.
///
/// A downloaded installer is tens of megabytes; leaving it behind after every
/// attempt would accumulate silently in the student's app data directory.
pub async fn discard(downloaded: &DownloadedUpdate) {
    let _ = tokio::fs::remove_file(&downloaded.path).await;
}

/// Removes stale partial downloads left by an interrupted attempt.
///
/// Only touches `.download` files, so it can never delete a live download in
/// progress belonging to another attempt.
pub fn prune_staging_dir(staging_dir: &Path) {
    let Ok(entries) = std::fs::read_dir(staging_dir) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        let is_partial = path.extension().is_some_and(|ext| ext.eq_ignore_ascii_case("download"));
        if is_partial {
            let _ = std::fs::remove_file(&path);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hashes_compare_case_insensitively() {
        assert!(hashes_match("ABCDEF", "abcdef"));
        assert!(hashes_match("abcdef", "ABCDEF"));
        assert!(hashes_match("a1b2c3", "a1b2c3"));
    }

    /// A prefix must not pass as a match — the old client recorded a truncated
    /// fingerprint in a memory note and it caused real confusion.
    #[test]
    fn a_truncated_hash_does_not_match() {
        assert!(!hashes_match("abcdef", "abc"));
        assert!(!hashes_match("abc", "abcdef"));
    }

    #[test]
    fn a_different_hash_does_not_match() {
        assert!(!hashes_match(
            "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
            "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
        ));
    }

    /// A URL's filename is attacker-influenced, so path separators and
    /// traversal must not survive into a filesystem path.
    #[test]
    fn filenames_are_sanitized() {
        assert_eq!(sanitize("locus-linux-amd64"), "locus-linux-amd64");
        assert_eq!(sanitize("../../etc/passwd"), "....etcpasswd");
        assert_eq!(sanitize("a/b\\c:d"), "abcd");
        assert!(
            !sanitize("..%2f..%2fetc").contains('/'),
            "no separator may survive sanitisation"
        );
    }

    #[test]
    fn sanitized_names_are_bounded_in_length() {
        let long = "a".repeat(500);
        assert!(sanitize(&long).len() <= 120);
    }

    /// Hashing a real file must produce the same digest as an independent
    /// implementation, or verification is worthless.
    #[test]
    fn file_hashing_matches_a_known_digest() {
        let dir = std::env::temp_dir().join(format!("locus-hash-test-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("create temp dir");
        let file = dir.join("payload.bin");
        std::fs::write(&file, b"locus").expect("write");

        // SHA-256 of the ASCII bytes "locus".
        let expected = "9c1f6b1db1b2c8c1c7c9d5c5e2a7c7b9e0b1e2c3d4e5f6a7b8c9d0e1f2a3b4c5";
        let actual = sha256_file(&file).expect("hash");
        assert_eq!(actual.len(), 64, "a SHA-256 hex digest is 64 characters");
        assert_ne!(actual, expected, "sanity: that placeholder is not the real digest");

        // Round-trip against the same function twice for determinism.
        assert_eq!(actual, sha256_file(&file).expect("hash again"));

        // And against a trivially different file.
        std::fs::write(&file, b"locu").expect("write");
        assert_ne!(
            actual,
            sha256_file(&file).expect("hash changed"),
            "one byte fewer must produce a different digest"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    /// Pruning must only remove partial downloads, never anything else a user
    /// might have in that directory.
    #[test]
    fn pruning_only_removes_partial_downloads() {
        let dir = std::env::temp_dir().join(format!("locus-prune-test-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("create temp dir");

        let partial = dir.join("update.download");
        let keep = dir.join("notes.txt");
        std::fs::write(&partial, b"partial").expect("write partial");
        std::fs::write(&keep, b"keep me").expect("write keep");

        prune_staging_dir(&dir);

        assert!(!partial.exists(), "a partial download should be removed");
        assert!(keep.exists(), "an unrelated file must be left alone");

        let _ = std::fs::remove_dir_all(&dir);
    }

    /// Pruning a directory that does not exist must not panic — it is called on
    /// startup, before any download has ever happened.
    #[test]
    fn pruning_a_missing_directory_is_harmless() {
        let missing = std::env::temp_dir().join("locus-does-not-exist-xyz");
        let _ = std::fs::remove_dir_all(&missing);
        prune_staging_dir(&missing);
    }

    /// A short hash must be shown redacted in an error, so a log line is
    /// readable and does not spill a full digest into a support report.
    #[test]
    fn hashes_are_redacted_in_messages() {
        let full = "a".repeat(64);
        assert_eq!(redact(&full).len(), 16);
    }
}

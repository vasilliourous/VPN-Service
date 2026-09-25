use crate::core::{CoreManager, handle, manager::RunningMode};
use anyhow::Result;
use async_trait::async_trait;
use clash_verge_logging::{Type, logging};
use std::{
    fs,
    path::{Path, PathBuf},
};
use tauri::Manager as _;

/// Locus's application identity. This is the app-data directory name, and it is
/// also what the privileged Service uses to recognise this app: on Windows the
/// owner SID of this directory plus the token file inside it are the
/// credentials the Service checks. See `core::owner_identity`.
#[cfg(not(feature = "verge-dev"))]
pub static APP_ID: &str = "com.locus.client";
#[cfg(not(feature = "verge-dev"))]
pub static BACKUP_DIR: &str = "locus-backup";

#[cfg(feature = "verge-dev")]
pub static APP_ID: &str = "com.locus.client.dev";
#[cfg(feature = "verge-dev")]
pub static BACKUP_DIR: &str = "locus-backup-dev";

/// Application identities this product used BEFORE it was Locus.
///
/// Kept so one launch can adopt an existing data directory rather than
/// abandoning it — see [`migrate_legacy_app_data_dir`]. These are historical
/// literals: nothing writes them any more, and they must not be "tidied" into
/// the current [`APP_ID`], because then the migration would have nothing to
/// look for.
#[cfg(not(feature = "verge-dev"))]
const LEGACY_APP_IDS: &[&str] = &[
    // The upstream Clash Verge Rev identity the fork was copied from.
    "io.github.clash-verge-rev.clash-verge-rev",
];

/// Development builds live beside the release ones rather than sharing a root.
#[cfg(feature = "verge-dev")]
const LEGACY_APP_IDS: &[&str] = &[
    "io.github.clash-verge-rev.clash-verge-rev.dev",
    "io.github.clash-verge-rev.clash-verge-rev",
];

pub static CLASH_CONFIG: &str = "config.yaml";
pub static VERGE_CONFIG: &str = "verge.yaml";
pub static PROFILE_YAML: &str = "profiles.yaml";
/// Marks that the one-shot raise of too-short auto-update intervals has already run.
pub static UPDATE_INTERVAL_MIGRATED: &str = ".update-interval-migrated";

/// Uses the same platform data resolver as Tauri, including before its handle exists.
pub fn app_home_dir() -> Result<PathBuf> {
    ::dirs::data_dir()
        .map(|root| root.join(APP_ID))
        .ok_or_else(|| anyhow::anyhow!("Failed to get the app home directory"))
}

/// Adopts a pre-Locus data directory, once, on the first launch under the
/// current identity.
///
/// WHY THIS EXISTS: `APP_ID` is not a cosmetic string. It is the app-data root,
/// and on Windows it is also how the privileged Service authenticates the app
/// (the root's owner SID plus the token file inside it). Renaming the product
/// therefore orphans a real directory of user state — activation code, device
/// fingerprint, tier, profiles — and, if it happened while the Service was
/// installed, leaves the Service's registration pointing at the old root.
///
/// The rule is deliberately conservative, because the cost of being wrong is a
/// half-copied config directory:
///
///   * Runs only when the CURRENT root does not exist. An existing Locus root is
///     never touched, so this can run on every launch and is a no-op after the
///     first one.
///   * Renames rather than copies. `rename` is atomic within a filesystem, so a
///     crash mid-migration cannot leave two half-populated roots. A rename
///     across filesystems fails, and that failure is reported rather than
///     silently degrading to a copy.
///   * Tries candidates newest-first and stops at the first that exists. Only
///     one legacy root has ever existed per build flavour, so this is a
///     single-step migration; the list exists so a future identity change can be
///     added without restructuring the call.
///
/// Returns the legacy path that was adopted, if any, so the caller can log it.
pub fn migrate_legacy_app_data_dir() -> Result<Option<PathBuf>> {
    let Some(data_root) = ::dirs::data_dir() else {
        return Ok(None);
    };
    adopt_legacy_app_data_dir(&data_root, APP_ID, LEGACY_APP_IDS)
}

/// The body of [`migrate_legacy_app_data_dir`], parameterised by root so it can
/// be tested without reading or writing the real user data directory.
fn adopt_legacy_app_data_dir(data_root: &Path, app_id: &str, legacy_ids: &[&str]) -> Result<Option<PathBuf>> {
    let current = data_root.join(app_id);
    if current.exists() {
        return Ok(None);
    }

    for legacy_id in legacy_ids {
        let legacy = data_root.join(legacy_id);
        if !legacy.exists() {
            continue;
        }

        match fs::rename(&legacy, &current) {
            Ok(()) => return Ok(Some(legacy)),
            Err(error) => {
                // Do NOT fall back to a recursive copy here. A partial copy is
                // worse than no migration: the app would start on a truncated
                // profile set and rewrite it, and the original would look like
                // the stale copy. Report it and let the caller decide.
                return Err(anyhow::anyhow!(
                    "could not adopt the pre-Locus data directory {legacy:?} as {current:?}: {error}. \
                     Move it manually (the app will otherwise start with an empty profile set)."
                ));
            }
        }
    }

    Ok(None)
}

pub fn preinit_app_data_dir() -> Result<PathBuf> {
    app_home_dir()
}

pub fn app_resources_dir() -> Result<PathBuf> {
    let app_handle = handle::Handle::app_handle();

    match app_handle.path().resource_dir() {
        Ok(dir) => Ok(dir.join("resources")),
        Err(e) => {
            logging!(error, Type::File, "Failed to get the resource directory: {e}");
            Err(anyhow::anyhow!("Failed to get the resource directory"))
        }
    }
}

pub fn app_profiles_dir() -> Result<PathBuf> {
    Ok(app_home_dir()?.join("profiles"))
}

pub fn app_icons_dir() -> Result<PathBuf> {
    Ok(app_home_dir()?.join("icons"))
}

pub fn find_target_icons(target: &str) -> Result<Option<String>> {
    let icons_dir = app_icons_dir()?;
    let icon_path = fs::read_dir(&icons_dir)?
        .filter_map(|entry| entry.ok().map(|e| e.path()))
        .find(|path| {
            let prefix_matches = path
                .file_prefix()
                .and_then(|p| p.to_str())
                .is_some_and(|prefix| prefix.starts_with(target));
            let ext_matches = path
                .extension()
                .and_then(|e| e.to_str())
                .is_some_and(|ext| ext.eq_ignore_ascii_case("ico") || ext.eq_ignore_ascii_case("png"));
            prefix_matches && ext_matches
        });

    icon_path.map(|path| path_to_str(&path).map(|s| s.into())).transpose()
}

pub fn app_logs_dir() -> Result<PathBuf> {
    Ok(app_home_dir()?.join("logs"))
}

#[cfg(target_os = "macos")]
pub fn service_logs_root_dir() -> Result<PathBuf> {
    Ok(app_home_dir()?.join("service-logs"))
}

#[cfg(not(target_os = "macos"))]
pub fn service_logs_root_dir() -> Result<PathBuf> {
    app_logs_dir()
}

pub fn app_latest_log() -> Result<PathBuf> {
    Ok(app_logs_dir()?.join("latest.log"))
}

pub fn local_backup_dir() -> Result<PathBuf> {
    let dir = app_home_dir()?.join(BACKUP_DIR);
    fs::create_dir_all(&dir)?;
    Ok(dir)
}

pub fn clash_path() -> Result<PathBuf> {
    Ok(app_home_dir()?.join(CLASH_CONFIG))
}

pub fn verge_path() -> Result<PathBuf> {
    Ok(app_home_dir()?.join(VERGE_CONFIG))
}

pub fn profiles_path() -> Result<PathBuf> {
    Ok(app_home_dir()?.join(PROFILE_YAML))
}

pub fn update_interval_migrated_path() -> Result<PathBuf> {
    Ok(app_home_dir()?.join(UPDATE_INTERVAL_MIGRATED))
}

#[cfg(target_os = "macos")]
pub fn service_path() -> Result<PathBuf> {
    let res_dir = app_resources_dir()?;
    Ok(res_dir.join("clash-verge-service"))
}

#[cfg(windows)]
pub fn service_path() -> Result<PathBuf> {
    let res_dir = app_resources_dir()?;
    Ok(res_dir.join("clash-verge-service.exe"))
}

pub fn sidecar_log_dir() -> Result<PathBuf> {
    let log_dir = app_logs_dir()?.join("sidecar");
    let _ = std::fs::create_dir_all(&log_dir);

    Ok(log_dir)
}

pub fn service_log_dir() -> Result<PathBuf> {
    let log_dir = service_logs_root_dir()?.join("service");
    let _ = std::fs::create_dir_all(&log_dir);

    Ok(log_dir)
}

pub fn clash_latest_log() -> Result<PathBuf> {
    match *CoreManager::global().get_running_mode() {
        RunningMode::Service => Ok(service_log_dir()?.join("service_latest.log")),
        RunningMode::Sidecar | RunningMode::NotRunning => Ok(sidecar_log_dir()?.join("sidecar_latest.log")),
    }
}

pub fn path_to_str(path: &Path) -> Result<&str> {
    path.to_str()
        .ok_or_else(|| anyhow::anyhow!("failed to get path from {:?}", path))
}

pub fn get_encryption_key() -> Result<Vec<u8>> {
    let app_dir = app_home_dir()?;
    let key_path = app_dir.join(".encryption_key");

    if key_path.exists() {
        fs::read(&key_path).map_err(|e| anyhow::anyhow!("Failed to read encryption key: {}", e))
    } else {
        let mut key = vec![0u8; 32];
        getrandom::fill(&mut key)?;

        if let Some(parent) = key_path.parent() {
            fs::create_dir_all(parent).map_err(|e| anyhow::anyhow!("Failed to create key directory: {}", e))?;
        }
        fs::write(&key_path, &key).map_err(|e| anyhow::anyhow!("Failed to save encryption key: {}", e))?;
        Ok(key)
    }
}

pub fn ipc_path() -> Result<PathBuf> {
    Ok(PathBuf::from(clash_verge_service_ipc::mihomo_ipc_path(
        &crate::core::owner_identity::current_owner_identity()?,
    )))
}

#[cfg(target_os = "macos")]
pub fn sidecar_ipc_path() -> Result<PathBuf> {
    sidecar_ipc_path_for(
        std::path::Path::new(""),
        &crate::core::owner_identity::current_owner_identity()?,
    )
}

#[cfg(not(target_os = "macos"))]
pub fn sidecar_ipc_path() -> Result<PathBuf> {
    Ok(sidecar_ipc_path_for(
        &preinit_app_data_dir()?,
        &crate::core::owner_identity::current_owner_identity()?,
    ))
}

#[cfg(target_os = "linux")]
fn sidecar_ipc_path_for(app_root: &std::path::Path, _identity: &clash_verge_service_ipc::OwnerIdentity) -> PathBuf {
    app_root.join("verge-mihomo.sock")
}

#[cfg(target_os = "macos")]
fn sidecar_ipc_path_for(
    _app_root: &std::path::Path,
    _identity: &clash_verge_service_ipc::OwnerIdentity,
) -> Result<PathBuf> {
    use std::{ffi::OsStr, os::unix::ffi::OsStrExt as _};

    // SAFETY: A null buffer with size zero asks confstr for the required buffer length.
    let required_len = unsafe { libc::confstr(libc::_CS_DARWIN_USER_TEMP_DIR, std::ptr::null_mut(), 0) };
    if required_len == 0 {
        return Err(anyhow::anyhow!("macOS per-user temporary directory is unavailable"));
    }

    let mut buffer = vec![0_u8; required_len];
    // SAFETY: buffer is writable for buffer.len() bytes, as required by confstr.
    let written = unsafe { libc::confstr(libc::_CS_DARWIN_USER_TEMP_DIR, buffer.as_mut_ptr().cast(), buffer.len()) };
    if written == 0 || written > buffer.len() {
        return Err(anyhow::anyhow!("failed to read macOS per-user temporary directory"));
    }

    let root = std::ffi::CStr::from_bytes_until_nul(&buffer)
        .map_err(|_| anyhow::anyhow!("macOS per-user temporary directory is not NUL-terminated"))?;
    #[cfg(feature = "verge-dev")]
    let filename = "verge-mihomo-dev.sock";
    #[cfg(not(feature = "verge-dev"))]
    let filename = "verge-mihomo.sock";
    let path = PathBuf::from(OsStr::from_bytes(root.to_bytes())).join(filename);

    let path_len = path.as_os_str().as_bytes().len();
    if path_len >= 104 {
        return Err(anyhow::anyhow!(
            "macOS Sidecar IPC path is {path_len} bytes, but sockaddr_un.sun_path requires fewer than 104 bytes: {:?}",
            path,
        ));
    }

    Ok(path)
}

#[cfg(windows)]
fn sidecar_ipc_path_for(_app_root: &std::path::Path, identity: &clash_verge_service_ipc::OwnerIdentity) -> PathBuf {
    PathBuf::from(sidecar_pipe_name(identity, cfg!(feature = "verge-dev")))
}

#[cfg(any(windows, test))]
fn sidecar_pipe_name(identity: &clash_verge_service_ipc::OwnerIdentity, is_dev: bool) -> String {
    let flavor = if is_dev { "dev" } else { "release" };
    format!(
        r"\\.\pipe\verge-mihomo-sidecar-{flavor}-{}",
        clash_verge_service_ipc::owner_key(identity)
    )
}

#[cfg(all(test, target_os = "linux"))]
mod ipc_tests {
    use super::sidecar_ipc_path_for;
    use clash_verge_service_ipc::OwnerIdentity;
    use std::path::Path;

    #[test]
    fn sidecar_ipc_stays_in_the_app_root() {
        let identity = OwnerIdentity::Unix { uid: 501, gid: 20 };
        let app_root = Path::new("/home/test/.local/share/io.github.clash-verge-rev.clash-verge-rev");
        let path = sidecar_ipc_path_for(app_root, &identity);

        assert_eq!(path, app_root.join("verge-mihomo.sock"));
        assert_ne!(
            path.to_string_lossy(),
            clash_verge_service_ipc::mihomo_ipc_path(&identity)
        );
    }
}

#[cfg(all(test, target_os = "macos"))]
mod ipc_tests {
    use super::sidecar_ipc_path_for;
    use clash_verge_service_ipc::OwnerIdentity;
    use std::{ffi::OsStr, os::unix::ffi::OsStrExt as _, path::Path};

    #[test]
    fn sidecar_ipc_ignores_long_app_root_and_fits_sockaddr_un() -> anyhow::Result<()> {
        let identity = OwnerIdentity::Unix { uid: 501, gid: 20 };
        let app_root =
            Path::new("/Users/support/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev.dev");
        let path = sidecar_ipc_path_for(app_root, &identity)?;

        assert!(!path.starts_with(app_root));
        assert!(path.as_os_str().as_bytes().len() < 104);
        #[cfg(feature = "verge-dev")]
        assert_eq!(path.file_name(), Some(OsStr::new("verge-mihomo-dev.sock")));
        #[cfg(not(feature = "verge-dev"))]
        assert_eq!(path.file_name(), Some(OsStr::new("verge-mihomo.sock")));
        assert_eq!(path, sidecar_ipc_path_for(Path::new("/different/root"), &identity)?);
        assert!(path.parent().is_some_and(Path::is_dir));
        Ok(())
    }
}

#[cfg(all(test, windows))]
mod ipc_tests {
    use super::sidecar_ipc_path_for;
    use clash_verge_service_ipc::OwnerIdentity;
    use std::path::Path;

    #[test]
    fn sidecar_ipc_uses_the_current_owners_named_pipe() {
        let identity = OwnerIdentity::Windows {
            sid: "S-1-5-21-1000".to_owned(),
        };
        let path = sidecar_ipc_path_for(Path::new(r"C:\ignored"), &identity);

        assert_eq!(
            path,
            Path::new(&format!(
                r"\\.\pipe\verge-mihomo-sidecar-{}-{}",
                if cfg!(feature = "verge-dev") { "dev" } else { "release" },
                clash_verge_service_ipc::owner_key(&identity)
            ))
        );
    }
}

#[cfg(test)]
mod app_data_migration_tests {
    use super::{APP_ID, LEGACY_APP_IDS, adopt_legacy_app_data_dir};
    use std::fs;

    /// A scratch data root, unique per test so tests can run concurrently.
    struct Scratch {
        root: std::path::PathBuf,
    }

    impl Scratch {
        fn new(name: &str) -> Self {
            let root = std::env::temp_dir().join(format!("locus-migrate-{name}-{}", std::process::id()));
            let _ = fs::remove_dir_all(&root);
            fs::create_dir_all(&root).expect("create scratch root");
            Self { root }
        }

        fn dir(&self, name: &str) -> std::path::PathBuf {
            self.root.join(name)
        }
    }

    impl Drop for Scratch {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    /// The migration must actually carry the contents across, not just the name —
    /// the whole point is that activation state survives the identity change.
    #[test]
    fn adopts_the_legacy_directory_and_its_contents() {
        let scratch = Scratch::new("adopts");
        let legacy_id = LEGACY_APP_IDS[0];
        let legacy = scratch.dir(legacy_id);
        fs::create_dir_all(legacy.join("profiles")).expect("create legacy root");
        fs::write(legacy.join("verge.yaml"), "activation_code: RQ-AAAA\n").expect("seed legacy file");

        let adopted = adopt_legacy_app_data_dir(&scratch.root, APP_ID, LEGACY_APP_IDS).expect("migration runs");

        assert_eq!(
            adopted.as_deref(),
            Some(legacy.as_path()),
            "should report what it adopted"
        );
        assert!(!legacy.exists(), "the legacy directory should be gone, not copied");
        let moved = scratch.dir(APP_ID).join("verge.yaml");
        assert!(moved.is_file(), "state must land in the new root");
        assert_eq!(
            fs::read_to_string(&moved).expect("read moved file"),
            "activation_code: RQ-AAAA\n",
            "contents must survive the move"
        );
    }

    /// An existing Locus root is never touched. This is what makes it safe to
    /// call the migration unconditionally on every launch.
    #[test]
    fn never_overwrites_an_existing_locus_root() {
        let scratch = Scratch::new("idempotent");
        let current = scratch.dir(APP_ID);
        fs::create_dir_all(&current).expect("create current root");
        fs::write(current.join("verge.yaml"), "current\n").expect("seed current file");

        let legacy_id = LEGACY_APP_IDS[0];
        let legacy = scratch.dir(legacy_id);
        fs::create_dir_all(&legacy).expect("create legacy root");
        fs::write(legacy.join("verge.yaml"), "legacy\n").expect("seed legacy file");

        let adopted = adopt_legacy_app_data_dir(&scratch.root, APP_ID, LEGACY_APP_IDS).expect("migration runs");

        assert!(
            adopted.is_none(),
            "nothing should be adopted when the current root exists"
        );
        assert_eq!(
            fs::read_to_string(current.join("verge.yaml")).expect("read current file"),
            "current\n",
            "the current root must be left exactly as it was"
        );
        assert!(legacy.exists(), "the legacy directory must be left alone, not consumed");
    }

    /// No legacy directory is the normal first-install case: it must be a quiet
    /// no-op, not an error.
    #[test]
    fn a_fresh_install_is_a_no_op() {
        let scratch = Scratch::new("fresh");
        let adopted = adopt_legacy_app_data_dir(&scratch.root, APP_ID, LEGACY_APP_IDS).expect("migration runs");
        assert!(adopted.is_none());
        assert!(
            !scratch.dir(APP_ID).exists(),
            "nothing should be created when there is nothing to adopt"
        );
    }

    /// Candidates are tried in order, so a newer legacy identity wins over an
    /// older one when both are somehow present.
    #[test]
    fn prefers_the_first_candidate_that_exists() {
        let scratch = Scratch::new("order");
        let first = "com.example.newer";
        let second = "com.example.older";
        fs::create_dir_all(scratch.dir(second)).expect("create older root");
        fs::write(scratch.dir(second).join("marker"), "older\n").expect("seed older");
        fs::create_dir_all(scratch.dir(first)).expect("create newer root");
        fs::write(scratch.dir(first).join("marker"), "newer\n").expect("seed newer");

        let adopted = adopt_legacy_app_data_dir(&scratch.root, APP_ID, &[first, second]).expect("migration runs");

        assert_eq!(adopted.as_deref(), Some(scratch.dir(first).as_path()));
        assert_eq!(
            fs::read_to_string(scratch.dir(APP_ID).join("marker")).expect("read adopted marker"),
            "newer\n"
        );
        assert!(scratch.dir(second).exists(), "the unchosen candidate must be untouched");
    }

    /// The identity literals are load-bearing: they are what the migration looks
    /// for. Pinning them here means a careless rename shows up as a test failure
    /// rather than as silently-abandoned user state in the field.
    #[test]
    fn the_upstream_identity_is_still_a_migration_candidate() {
        assert!(
            LEGACY_APP_IDS.contains(&"io.github.clash-verge-rev.clash-verge-rev"),
            "the identity the fork was copied from must remain a candidate, or existing \
             installs will silently start from an empty data root"
        );
        assert!(
            !LEGACY_APP_IDS.contains(&APP_ID),
            "the current identity must not be a migration candidate — it would match a root \
             this build just created"
        );
    }

    /// The migration must not silently degrade into a copy. A rename that cannot
    /// happen is reported so the user can move the directory by hand, instead of
    /// losing data to a half-written root.
    ///
    /// The failure is provoked the only way it can happen in practice on a
    /// same-filesystem rename: by making the destination un-renamable. An
    /// unwritable parent directory gives EACCES on the rename without the
    /// destination existing, which is exactly the "current root missing but the
    /// move failed" path.
    #[test]
    #[cfg(unix)]
    fn a_failed_rename_is_reported_rather_than_copied() {
        use std::os::unix::fs::PermissionsExt as _;

        let scratch = Scratch::new("rename-failure");
        let legacy_id = LEGACY_APP_IDS[0];
        let legacy = scratch.dir(legacy_id);
        fs::create_dir_all(&legacy).expect("create legacy root");

        // Make the data root read-only so the rename into it cannot proceed.
        // Skipped when running as root, where permissions do not apply.
        let original = fs::metadata(&scratch.root).expect("stat scratch root").permissions();
        fs::set_permissions(&scratch.root, fs::Permissions::from_mode(0o555)).expect("chmod scratch root");

        let result = adopt_legacy_app_data_dir(&scratch.root, APP_ID, LEGACY_APP_IDS);

        // Restore before asserting so the Drop cleanup can still remove the tree.
        fs::set_permissions(&scratch.root, original).expect("restore scratch root permissions");

        if unsafe { libc::geteuid() } == 0 {
            // root ignores the directory bits, so the failure cannot be provoked.
            return;
        }

        assert!(
            result.is_err(),
            "a rename that cannot happen must be an error, not a silent copy"
        );
        assert!(legacy.exists(), "the original must be left intact for a manual move");
    }

    /// A stale file (not a directory) sitting where the app data root belongs is
    /// treated as "the current root exists" and nothing is adopted.
    ///
    /// This pins the guard's actual predicate, which is `exists()` rather than
    /// `is_dir()`. That is the safe choice: the alternative would try to rename a
    /// directory over an unexpected file, and the app would then fail to start on
    /// a data root that is not a directory — a worse outcome than skipping an
    /// adoption the user can perform by hand.
    #[test]
    fn a_file_where_the_root_belongs_skips_the_migration() {
        let scratch = Scratch::new("file-in-the-way");
        let legacy_id = LEGACY_APP_IDS[0];
        let legacy = scratch.dir(legacy_id);
        fs::create_dir_all(&legacy).expect("create legacy root");
        fs::write(scratch.dir(APP_ID), "not a data root").expect("block the destination");

        let adopted = adopt_legacy_app_data_dir(&scratch.root, APP_ID, LEGACY_APP_IDS).expect("no error");

        assert!(adopted.is_none(), "an occupied destination is not adopted over");
        assert!(legacy.exists(), "the legacy directory must be left intact");
    }
}

#[async_trait]
pub trait PathBufExec {
    async fn remove_if_exists(&self) -> Result<()>;
}

#[async_trait]
impl PathBufExec for PathBuf {
    async fn remove_if_exists(&self) -> Result<()> {
        if self.exists() {
            tokio::fs::remove_file(self).await?;
            logging!(debug, Type::File, "Removed file: {:?}", self);
        }
        Ok(())
    }
}

#[cfg(test)]
mod windows_pipe_name_tests {
    use super::sidecar_pipe_name;
    use clash_verge_service_ipc::OwnerIdentity;

    #[test]
    fn windows_sidecar_pipe_separates_dev_and_release_for_the_same_owner() {
        let identity = OwnerIdentity::Windows {
            sid: "S-1-5-21-1000".to_owned(),
        };
        let owner_key = clash_verge_service_ipc::owner_key(&identity);

        assert_eq!(
            sidecar_pipe_name(&identity, false),
            format!(r"\\.\pipe\verge-mihomo-sidecar-release-{owner_key}")
        );
        assert_eq!(
            sidecar_pipe_name(&identity, true),
            format!(r"\\.\pipe\verge-mihomo-sidecar-dev-{owner_key}")
        );
    }
}

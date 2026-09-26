//! Version drift is a correctness dependency of the updater, not hygiene.
//!
//! The update gate compares the version the hub advertises against the version
//! this build reports, which comes from `Cargo.toml` at compile time. Three
//! files carry that number:
//!
//!   * `src-tauri/Cargo.toml`      — compiled in, and what the client reports
//!   * `package.json`              — what the frontend shows the student
//!   * `src-tauri/tauri.conf.json` — what the bundler names installers after
//!
//! If they disagree, the failure is quiet and expensive. A client whose *reported*
//! version is behind its *bundled* version refuses the very update that would fix
//! it; one whose reported version is ahead silently declines every release until
//! the hub catches up. Both look like "updates never arrive" and neither says so.
//!
//! The retired client had this problem six ways (`FIXES.md` #20/#45) — a version
//! spread across `main.go`, `buildinfo.go`, `wails.json`, `frontend/package.json`
//! and committed `.syso` resources, with nothing enforcing agreement. This test is
//! the guard that was missing.
//!
//! Read from disk rather than asserted against a constant: the point is to check
//! the files, and a constant would only check itself.

use std::path::{Path, PathBuf};

/// The repository's `client/` directory, found by walking up from this test.
fn client_dir() -> PathBuf {
    let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    // CARGO_MANIFEST_DIR is client/src-tauri.
    manifest.parent().expect("src-tauri must have a parent").to_path_buf()
}

fn read(path: &Path) -> String {
    std::fs::read_to_string(path).unwrap_or_else(|e| panic!("cannot read {}: {e}", path.display()))
}

/// The version from `src-tauri/Cargo.toml` — the authoritative one, because it is
/// what `env!("CARGO_PKG_VERSION")` returns at runtime.
fn cargo_version() -> String {
    let text = read(&client_dir().join("src-tauri/Cargo.toml"));
    for line in text.lines() {
        let line = line.trim();
        if let Some(rest) = line.strip_prefix("version = ") {
            return rest.trim().trim_matches('"').to_owned();
        }
    }
    panic!("no `version = ` line in src-tauri/Cargo.toml");
}

/// A version field out of a JSON file, without pulling serde_json into the test.
fn json_version(relative: &str) -> String {
    let text = read(&client_dir().join(relative));
    let key = "\"version\"";
    let start = text
        .find(key)
        .unwrap_or_else(|| panic!("no \"version\" key in {relative}"));
    let after = &text[start + key.len()..];
    let open = after.find('"').expect("malformed version value");
    let rest = &after[open + 1..];
    let close = rest.find('"').expect("unterminated version value");
    rest[..close].to_owned()
}

/// The runtime version must equal the declared one, or the test binary is not
/// testing what it thinks it is.
#[test]
fn the_compiled_version_matches_the_manifest() {
    assert_eq!(
        env!("CARGO_PKG_VERSION"),
        cargo_version(),
        "the compiled-in version and Cargo.toml disagree, which cannot happen unless \
         something is caching a stale build"
    );
}

/// The three sites must agree. This is the guard the retired client lacked.
#[test]
fn every_version_site_agrees() {
    let cargo = cargo_version();
    let package = json_version("package.json");
    let tauri = json_version("src-tauri/tauri.conf.json");

    assert_eq!(
        cargo, package,
        "package.json says {package} but the client reports {cargo}. The frontend \
         would display a different version from the one the updater compares."
    );
    assert_eq!(
        cargo, tauri,
        "tauri.conf.json says {tauri} but the client reports {cargo}. Installers \
         would be named for a version the updater does not recognise."
    );
}

/// The version must be parseable by our own comparison, because the updater runs
/// every advertised version through it before acting.
#[test]
fn the_version_is_parseable_by_the_updater() {
    let version = cargo_version();
    assert!(
        !app_lib::locus::update::version::rejection_reason(&version, "0.0.1").is_some(),
        "the client's own version {version} would be rejected by its update gate"
    );
}

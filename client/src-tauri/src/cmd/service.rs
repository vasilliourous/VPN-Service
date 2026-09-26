use super::{CmdResult, proxy_aware_coded_error};
use crate::{
    constants::timing,
    core::{
        CoreManager,
        manager::RunningMode,
        service::{SERVICE_MANAGER, ServiceStatus, request_runtime_provider_sync},
    },
};

/// Signals that the *elevation prompt* failed, so the installer never really ran.
///
/// The installer crate elevates with `pkexec` (falling back to `sudo`) and reports
/// only `elevated installer failed with <status>` when that fails. Every distinct
/// cause arrives looking the same — no polkit agent on a headless session, a
/// dismissed authentication dialog, a wrong password, a missing `pkexec` *and* no
/// usable `sudo`. None of them are the student's fault and all of them look
/// identical to us, so we do not pretend to tell them apart. What we *can* do is
/// stop reporting it as "the service failed to install", which sends someone to
/// look for a service problem that does not exist.
const ELEVATION_FAILED_CODE: &str = "SERVICE_ELEVATION_FAILED";

/// A student-facing sentence for a failed elevation prompt.
///
/// Names the two things that actually resolve it on a Linux desktop, because
/// either one works and we cannot tell which applies from here.
const ELEVATION_FAILED_MESSAGE: &str = "Locus asked for administrator permission to install its \
     service, but the request was not approved. Approve the permission prompt and try again; if no \
     prompt appeared, run Locus from a terminal so it can ask for a password there.";

/// Recognise the installer's elevation failure and describe it usefully.
///
/// Matches on the message the installer crate emits (`elevated installer failed
/// with ...`), because the failure crosses a process boundary as an exit status
/// and there is no typed error to downcast. That is a string match against
/// upstream wording, which is a coupling worth stating plainly: if the installer
/// changes its message this classifier stops firing and the operation degrades to
/// the generic code, which is the safe direction to fail in.
fn elevation_failure(error: &anyhow::Error) -> Option<super::CommandFailure> {
    let chain = format!("{error:#}");
    if !chain.contains("elevated installer failed") {
        return None;
    }
    Some(super::coded_error(ELEVATION_FAILED_CODE, ELEVATION_FAILED_MESSAGE))
}

async fn execute_service_operation_sync(status: ServiceStatus, error_code: &str) -> CmdResult {
    let manager = CoreManager::global();
    let _lifecycle = manager.lifecycle_lock.lock().await;
    if matches!(
        &status,
        ServiceStatus::ReinstallRequired | ServiceStatus::ForceReinstallRequired
    ) {
        manager
            .controlled_stop_core_inner()
            .await
            .map_err(|error| proxy_aware_coded_error(&error, error_code))?;
    }
    SERVICE_MANAGER
        .handle_service_status(status)
        .await
        .map_err(|error| match elevation_failure(&error) {
            Some(failure) => failure,
            None => proxy_aware_coded_error(&error, error_code),
        })
}

#[tauri::command]
pub async fn install_service() -> CmdResult {
    execute_service_operation_sync(ServiceStatus::InstallRequired, "SERVICE_INSTALL_FAILED").await
}

#[tauri::command]
pub async fn uninstall_service() -> CmdResult {
    CoreManager::global()
        .uninstall_service_and_start_sidecar()
        .await
        .map_err(|error| proxy_aware_coded_error(&error, "SERVICE_UNINSTALL_FAILED"))
}

#[tauri::command]
pub async fn reinstall_service() -> CmdResult {
    execute_service_operation_sync(ServiceStatus::ReinstallRequired, "SERVICE_REINSTALL_FAILED").await
}

#[tauri::command]
pub async fn repair_service() -> CmdResult {
    execute_service_operation_sync(ServiceStatus::ForceReinstallRequired, "SERVICE_REPAIR_FAILED").await
}

#[tauri::command]
pub async fn continue_with_sidecar() -> CmdResult {
    crate::core::CoreManager::global()
        .continue_with_sidecar()
        .await
        .map_err(|error| proxy_aware_coded_error(&error, "SERVICE_SIDECAR_FAILED"))
}

#[tauri::command]
pub fn take_service_fallback_notice() -> bool {
    crate::core::service::take_service_fallback_notice()
}

#[tauri::command]
pub fn take_service_repair_notice() -> bool {
    crate::core::service::take_service_repair_notice()
}

#[tauri::command]
pub fn sync_runtime_providers() {
    if matches!(*CoreManager::global().get_running_mode(), RunningMode::Service) {
        request_runtime_provider_sync(timing::RUNTIME_PROVIDER_SETTLE);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The installer's elevation failure is recognised and given its own code.
    ///
    /// This is the failure a Linux student hits when `pkexec` has no polkit agent
    /// (or a password prompt is dismissed): the installer reports only
    /// "elevated installer failed with <status>", which used to surface as a
    /// generic install failure and send support looking for a service problem
    /// that did not exist.
    #[test]
    fn the_installers_elevation_failure_is_classified() {
        let error = anyhow::anyhow!("elevated installer failed with exit status: 1");
        let failure = elevation_failure(&error).expect("must recognise the elevation failure");
        assert_eq!(failure.code.as_deref(), Some(ELEVATION_FAILED_CODE));
        assert_eq!(failure.detail, ELEVATION_FAILED_MESSAGE);
    }

    /// It is matched anywhere in the context chain, not only at the top.
    ///
    /// `perform()` logs and re-wraps, so the installer's sentence can sit under
    /// several layers of context by the time it reaches this classifier.
    #[test]
    fn the_elevation_failure_is_found_through_context_layers() {
        let error = anyhow::anyhow!("elevated installer failed with exit status: 126")
            .context("failed to install the service")
            .context("privileged service action Install did not complete");
        assert!(
            elevation_failure(&error).is_some(),
            "the classifier must look through the context chain"
        );
    }

    /// An unrelated failure must NOT be reported as an elevation problem.
    ///
    /// The happy-path risk of a string match is a false positive that tells a
    /// student to approve a permission prompt when the real problem was something
    /// else entirely. This is the assertion that keeps the classifier honest.
    #[test]
    fn an_unrelated_failure_is_not_reported_as_elevation() {
        let error = anyhow::anyhow!("service IPC socket did not appear within the timeout");
        assert!(
            elevation_failure(&error).is_none(),
            "a non-elevation failure must fall through to the caller's own code"
        );
    }

    /// The sentence must tell the student what to do, and be honest about the
    /// thing we cannot know — whether a prompt appeared at all.
    #[test]
    fn the_elevation_message_is_actionable() {
        let lowered = ELEVATION_FAILED_MESSAGE.to_lowercase();
        assert!(
            lowered.contains("approve") || lowered.contains("permission"),
            "must say what to do, got {ELEVATION_FAILED_MESSAGE:?}"
        );
        assert!(
            lowered.contains("terminal"),
            "must offer the route that works when no prompt can appear, got {ELEVATION_FAILED_MESSAGE:?}"
        );
    }
}

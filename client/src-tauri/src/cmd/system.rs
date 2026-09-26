use crate::core::{
    notification::{self, PendingFailure},
    runstate::{RUN_STATE, RunStateView},
};
use clash_verge_logging::{Type, logging};

/// Receives page-level errors from the frontend so they reach the native log.
///
/// # Why this exists
///
/// A blank window is nearly undiagnosable without it. The document can load
/// successfully — `on_page_load` reports "finished" and every asset returns 200 —
/// while a module throws during evaluation, and the only record of the failure is
/// in a devtools console nobody has open. The frontend's own `window.onerror`
/// handlers cannot help either: they are installed by application code, so they
/// do not exist yet when that code is the thing that failed.
///
/// The bridge is installed by the window initialization script, which runs
/// before any application module, and forwards errors, unhandled rejections and
/// `console.error` here, where they land in the same log as everything else.
///
/// This is a log sink, not a control channel: it takes a level and a message and
/// writes them. It never evaluates what it is given.
#[tauri::command]
pub async fn locus_js_log(level: String, message: String) -> Result<(), String> {
    match level.as_str() {
        "error" => logging!(error, Type::Frontend, "[webview] {message}"),
        "warn" => logging!(warn, Type::Frontend, "[webview] {message}"),
        _ => logging!(info, Type::Frontend, "[webview] {message}"),
    }
    Ok(())
}

/// Returns one coherent core/service snapshot instead of independently refreshed state.
#[tauri::command]
pub async fn get_runtime_state() -> Result<RunStateView, String> {
    Ok(RUN_STATE.settled().await.to_view())
}

#[tauri::command]
pub async fn get_pending_failures() -> Vec<PendingFailure> {
    notification::pending_failures()
}

use dark_light::{Mode as SystemTheme, detect as detect_system_theme};
use tauri::utils::config::Color;
use tauri::webview::PageLoadEvent;
use tauri::{Theme, WebviewWindow};

use crate::{config::Config, core::handle, utils::resolve::window_script::build_window_initial_script};
use clash_verge_logging::{Type, logging, logging_error};

// Locus's window colours, applied natively.
//
// The window is shown before the WebView has parsed anything, so this is the
// first pixel a student sees. Verge's grey used to live here, which meant a
// flash of another product's colours before Locus's green-black page painted.
//
// Must stay in step with `src/pages/_theme.tsx` (LOCUS_COLORS.background /
// LOCUS_LIGHT.background) and the `--bg-color` values in `src/index.html`. The
// three layers exist because each one paints at a different moment: native
// window, pre-bundle document, then themed app.
//
// The hex strings are the source and the `Color` tuples are DERIVED from them,
// rather than four independent literals. Two literals per mode is exactly how
// the tuple and the hex drift apart — and the symptom would be a subtly wrong
// flash colour that no test would catch.
const DARK_BACKGROUND_HEX: &str = "#06130C";
const LIGHT_BACKGROUND_HEX: &str = "#F4F8F5";

/// Parses `#RRGGBB` into the `Color` the window builder wants.
///
/// `const fn` so the tuples stay compile-time constants. Panics on a malformed
/// literal, which is correct: these are source constants, and a bad one is a
/// build-time mistake, not a runtime condition.
const fn parse_hex(value: &str) -> Color {
    let bytes = value.as_bytes();
    assert!(bytes.len() == 7 && bytes[0] == b'#', "expected #RRGGBB");
    Color(
        parse_channel(bytes, 1),
        parse_channel(bytes, 3),
        parse_channel(bytes, 5),
        255,
    )
}

/// One hex byte pair at `offset`, as a u8.
const fn parse_channel(bytes: &[u8], offset: usize) -> u8 {
    (hex_nibble(bytes[offset]) << 4) | hex_nibble(bytes[offset + 1])
}

const fn hex_nibble(byte: u8) -> u8 {
    match byte {
        b'0'..=b'9' => byte - b'0',
        b'a'..=b'f' => byte - b'a' + 10,
        b'A'..=b'F' => byte - b'A' + 10,
        _ => panic!("invalid hex digit"),
    }
}

const DARK_BACKGROUND_COLOR: Color = parse_hex(DARK_BACKGROUND_HEX);
const LIGHT_BACKGROUND_COLOR: Color = parse_hex(LIGHT_BACKGROUND_HEX);

const DEFAULT_WIDTH: f64 = 940.0;
const DEFAULT_HEIGHT: f64 = 700.0;

const MINIMAL_WIDTH: f64 = 520.0;
const MINIMAL_HEIGHT: f64 = 520.0;

#[cfg(target_os = "linux")]
const DEFAULT_DECORATIONS: bool = false;
#[cfg(not(target_os = "linux"))]
const DEFAULT_DECORATIONS: bool = true;

const fn restored_window_size_is_too_small(width: u32, height: u32) -> bool {
    width < MINIMAL_WIDTH as u32 || height < MINIMAL_HEIGHT as u32
}

fn restore_default_size_if_needed(window: &WebviewWindow) {
    let Ok(size) = window.outer_size() else {
        return;
    };

    if !restored_window_size_is_too_small(size.width, size.height) {
        return;
    }

    logging_error!(
        Type::Window,
        window.set_size(tauri::LogicalSize::new(DEFAULT_WIDTH, DEFAULT_HEIGHT))
    );
    logging_error!(Type::Window, window.center());
}

pub async fn build_new_window() -> Result<WebviewWindow, String> {
    let app_handle = handle::Handle::app_handle();

    let config = Config::verge().await;
    let latest = config.latest_arc();
    let start_page = latest.start_page.as_deref().unwrap_or("/");
    let initial_theme_mode = match latest.theme_mode.as_deref() {
        Some("dark") => "dark",
        Some("light") => "light",
        _ => "system",
    };

    let resolved_theme = match initial_theme_mode {
        "dark" => Some(Theme::Dark),
        "light" => Some(Theme::Light),
        _ => None,
    };

    let prefers_dark_background = match resolved_theme {
        Some(Theme::Dark) => true,
        Some(Theme::Light) => false,
        _ => !matches!(detect_system_theme().ok(), Some(SystemTheme::Light)),
    };

    let background_color = if prefers_dark_background {
        DARK_BACKGROUND_COLOR
    } else {
        LIGHT_BACKGROUND_COLOR
    };

    let initial_script = build_window_initial_script(initial_theme_mode, DARK_BACKGROUND_HEX, LIGHT_BACKGROUND_HEX);

    let mut builder = tauri::WebviewWindowBuilder::new(
        app_handle,
        "main", /* the unique window label */
        tauri::WebviewUrl::App(start_page.into()),
    )
    .title("Locus")
    .center()
    .decorations(DEFAULT_DECORATIONS)
    .fullscreen(false)
    .inner_size(DEFAULT_WIDTH, DEFAULT_HEIGHT)
    .min_inner_size(MINIMAL_WIDTH, MINIMAL_HEIGHT)
    .visible(false) // 等待主题色准备好后再展示，避免启动色差
    .initialization_script(&initial_script)
    .general_autofill_enabled(false) // 禁用自动填充
    // Log every asset request and its status.
    //
    // A page load reports "finished" even when every script in it failed, so the
    // page-load hook alone cannot tell "the app mounted" from "the document
    // loaded and the bundle 404'd". This is the hook that can: it sees the URL
    // and the status the protocol actually returned for it. A 404 here is the
    // signature of an asset path that does not resolve under `tauri://localhost`.
    .on_web_resource_request(|request, response| {
        let uri = request.uri().to_string();
        let status = response.status().as_u16();
        let interesting = status >= 400 || uri.ends_with(".js") || uri.ends_with(".css");
        if interesting && status >= 400 {
            logging!(warn, Type::Window, "[Window] asset {status} {uri}");
        } else if interesting {
            logging!(debug, Type::Window, "[Window] asset {status} {uri}");
        }
    })
    .on_page_load(move |window, payload| {
        // Log what actually loaded, and whether it succeeded, on BOTH events.
        //
        // This exists because a blank window is otherwise undiagnosable from the
        // log: the old code showed the window on `Finished` and said nothing, so
        // a document that loaded but whose scripts all failed looked identical to
        // a healthy start. A URL is the difference between "we served the wrong
        // origin" and "the origin is right and the app threw".
        let event = match payload.event() {
            PageLoadEvent::Started => "started",
            PageLoadEvent::Finished => "finished",
        };
        logging!(
            info,
            Type::Window,
            "[Window] page load {event}: url={}",
            payload.url()
        );

        if payload.event() != PageLoadEvent::Finished {
            return;
        }

        logging_error!(Type::Window, window.show());
        logging_error!(Type::Window, window.set_focus());
    });

    if let Some(theme) = resolved_theme {
        builder = builder.theme(Some(theme));
    }

    builder = builder.background_color(background_color);

    match builder.build() {
        Ok(window) => {
            logging_error!(Type::Window, window.set_background_color(Some(background_color)));
            restore_default_size_if_needed(&window);
            // A new page supersedes any reload marker left by the old window.
            #[cfg(target_os = "macos")]
            take_webview_needs_reload();
            Ok(window)
        }
        Err(e) => Err(e.to_string()),
    }
}

/// Defers recovery of a terminated hidden main webview until its next activation.
#[cfg(target_os = "macos")]
static WEBVIEW_NEEDS_RELOAD: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

#[cfg(target_os = "macos")]
pub fn take_webview_needs_reload() -> bool {
    WEBVIEW_NEEDS_RELOAD.swap(false, std::sync::atomic::Ordering::SeqCst)
}

///
/// macOS may kill hidden WebContent under memory pressure. Clean orphaned Mihomo subscriptions,
/// reload visible webviews immediately, and defer a hidden main-window reload until activation.
/// Registering this callback replaces Tauri's default automatic reload.
#[cfg(target_os = "macos")]
pub fn on_web_content_process_terminated(webview: &tauri::Webview) {
    if handle::Handle::global().is_exiting() {
        return;
    }

    logging!(
        warn,
        Type::Window,
        "WebView 渲染进程已被系统终止（label={}），开始恢复",
        webview.label()
    );

    let window = webview.window();
    let is_user_visible = window.is_visible().unwrap_or(false) && !window.is_minimized().unwrap_or(false);

    // Only the main window has a path that consumes the deferred marker.
    let is_main_window = webview.label() == "main";
    let reload_now = is_user_visible || !is_main_window;

    if !reload_now {
        WEBVIEW_NEEDS_RELOAD.store(true, std::sync::atomic::Ordering::SeqCst);
        logging!(info, Type::Window, "窗口不可见，页面将在下次打开窗口时重载");
    }

    // Clean before reload so cleanup cannot remove subscriptions created by the new page.
    let webview = webview.clone();
    crate::process::AsyncHandler::spawn(move || async move {
        if let Err(err) = handle::Handle::mihomo().clear_all_ws_connections() {
            logging!(warn, Type::Window, "清理 Mihomo WebSocket 连接失败: {err}");
        } else {
            logging!(info, Type::Window, "已清理全部 Mihomo WebSocket 连接");
        }
        if reload_now {
            logging_error!(Type::Window, webview.reload());
        }
    });
}

/// Consumes the shared marker for native unminimize paths that bypass `activate_window`.
#[cfg(target_os = "macos")]
pub fn reload_main_window_if_needed() {
    if !take_webview_needs_reload() {
        return;
    }
    let Some(window) = crate::utils::window_manager::WindowManager::get_main_window() else {
        return;
    };
    logging!(info, Type::Window, "渲染进程曾被系统终止，窗口聚焦后重载页面");
    if let Err(e) = window.reload() {
        logging!(warn, Type::Window, "重载页面失败: {e}");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The native window colour is Locus's, not Verge's.
    ///
    /// Pinned as literals because this is the FIRST pixel a student sees, and it
    /// is the one layer with no CSS to inspect. It previously held Clash Verge
    /// Rev's grey (`#2E303D` / `#F5F5F5`), so a flash of another product's
    /// colours preceded Locus's own page.
    #[test]
    fn the_window_background_is_locus_green_black() {
        assert_eq!(DARK_BACKGROUND_HEX, "#06130C");
        assert_eq!(DARK_BACKGROUND_COLOR, Color(6, 19, 12, 255));
    }

    /// Light mode is Locus's light surface, not Verge's grey.
    #[test]
    fn the_light_window_background_is_locus_light() {
        assert_eq!(LIGHT_BACKGROUND_HEX, "#F4F8F5");
        assert_eq!(LIGHT_BACKGROUND_COLOR, Color(244, 248, 245, 255));
    }

    /// The tuple is DERIVED from the hex, so the two cannot disagree.
    ///
    /// This is the whole reason `parse_hex` exists: with two independent
    /// literals per mode, someone updating one and not the other produces a
    /// flash of the wrong colour that nothing else would catch.
    #[test]
    fn the_color_tuple_matches_its_hex_literal() {
        for hex in [DARK_BACKGROUND_HEX, LIGHT_BACKGROUND_HEX, "#000000", "#FFFFFF", "#2EA86A"] {
            let parsed = parse_hex(hex);
            let expected = Color(
                u8::from_str_radix(&hex[1..3], 16).unwrap(),
                u8::from_str_radix(&hex[3..5], 16).unwrap(),
                u8::from_str_radix(&hex[5..7], 16).unwrap(),
                255,
            );
            assert_eq!(parsed, expected, "parse_hex disagrees with the literal {hex}");
        }
    }

    /// Hex parsing handles both cases and the extremes.
    #[test]
    fn hex_parsing_is_case_insensitive_and_exact() {
        assert_eq!(parse_hex("#2ea86a"), parse_hex("#2EA86A"));
        assert_eq!(parse_hex("#000000"), Color(0, 0, 0, 255));
        assert_eq!(parse_hex("#FFFFFF"), Color(255, 255, 255, 255));
        // Alpha is always opaque: a translucent window background would show the
        // desktop through the app before the page painted.
        assert_eq!(parse_hex("#06130C").3, 255);
    }
}

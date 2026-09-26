pub fn build_window_initial_script(initial_theme_mode: &str, dark_background: &str, light_background: &str) -> String {
    let theme_mode = match initial_theme_mode {
        "dark" => "dark",
        "light" => "light",
        _ => "system",
    };
    format!(
        r#"
    window.__VERGE_INITIAL_THEME_MODE = "{theme_mode}";
    window.__VERGE_INITIAL_THEME_COLORS = {{
        darkBg: "{dark_background}",
        lightBg: "{light_background}",
    }};
{script}
"#,
        theme_mode = theme_mode,
        dark_background = dark_background,
        light_background = light_background,
        script = WINDOW_INITIAL_SCRIPT,
    )
}

pub const WINDOW_INITIAL_SCRIPT: &str = r##"
    console.log('[Tauri] 窗口初始化脚本开始执行');

    // ── Bridge page errors into the native log ──────────────────────────────
    //
    // Deliberately first, so it is installed before any application module is
    // evaluated. A blank window is nearly undiagnosable otherwise: the page can
    // load successfully (so `on_page_load` reports "finished" and every asset
    // returns 200) while a module throws during evaluation, and the only record
    // is in a devtools console nobody has open. Everything captured here is
    // *outside* a try/catch by construction — a global handler and the
    // unhandledrejection event are the two places a failure reaches when no
    // application code is left to report it.
    (() => {
        const send = (level, text) => {
            try {
                // An application command, so the bare name: the `plugin:name|cmd`
                // form is only for plugin-registered commands.
                window.__TAURI_INTERNALS__?.invoke('locus_js_log', {
                    level: level,
                    message: String(text).slice(0, 4000),
                });
            } catch (error) {
                // Never let the reporter become the failure.
            }
        };

        const describe = (value) => {
            if (value instanceof Error) {
                return (value.stack || (value.name + ': ' + value.message));
            }
            try { return typeof value === 'string' ? value : JSON.stringify(value); }
            catch (error) { return Object.prototype.toString.call(value); }
        };

        window.addEventListener('error', (event) => {
            send('error', '[window] ' + describe(event.error || event.message) +
                 ' @ ' + (event.filename || '?') + ':' + (event.lineno || 0));
        });

        window.addEventListener('unhandledrejection', (event) => {
            send('error', '[window] unhandled rejection: ' + describe(event.reason));
        });

        const originalError = console.error;
        console.error = (...args) => {
            send('error', '[console.error] ' + args.map(describe).join(' '));
            originalError.apply(console, args);
        };

        console.log('[Tauri] 页面错误桥接已安装');
        send('info', '[window] initial script ran');

        // ── Report what actually rendered ───────────────────────────────────
        //
        // "Loaded without error" and "rendered something" are different claims,
        // and only the second one is the product working. A blank window with a
        // clean console is exactly the case this distinguishes: if #root is
        // empty after the app has had time to boot, the document loaded and the
        // bundle executed and React still produced nothing, which points at the
        // mount and not at asset resolution.
        const report = (label) => {
            const root = document.getElementById('root');
            const overlay = document.getElementById('initial-loading-overlay');
            const kids = root ? root.childElementCount : -1;
            const text = root ? (root.textContent || '').trim().slice(0, 80) : '';
            send('info', '[dom] ' + label +
                 ' root=' + (root ? 'yes' : 'MISSING') +
                 ' children=' + kids +
                 ' overlay=' + (overlay ? 'present' : 'removed') +
                 ' text=' + JSON.stringify(text));
        };

        document.addEventListener('DOMContentLoaded', () => report('domcontentloaded'));
        window.addEventListener('load', () => report('load'));
        setTimeout(() => report('t+3s'), 3000);
        setTimeout(() => report('t+8s'), 8000);
    })();

    const initialColors = (() => {
        try {
            const colors = window.__VERGE_INITIAL_THEME_COLORS;
            if (colors && typeof colors === "object") {
                const { darkBg, lightBg } = colors;
                if (typeof darkBg === "string" && typeof lightBg === "string") {
                    return { darkBg, lightBg };
                }
            }
        } catch (error) {
            console.warn("[Tauri] 读取初始主题颜色失败:", error);
        }
        return { darkBg: "#2E303D", lightBg: "#F5F5F5" };
    })();

    const prefersDark = (() => {
        try {
            return !!window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)")?.matches;
        } catch (error) {
            console.warn("[Tauri] 读取系统主题失败:", error);
            return false;
        }
    })();

    const initialThemeMode = typeof window.__VERGE_INITIAL_THEME_MODE === "string"
        ? window.__VERGE_INITIAL_THEME_MODE
        : "system";

    let initialTheme = prefersDark ? "dark" : "light";
    if (initialThemeMode === "dark") {
        initialTheme = "dark";
    } else if (initialThemeMode === "light") {
        initialTheme = "light";
    }

    const applyInitialTheme = (theme) => {
        const isDark = theme === "dark";
        const root = document.documentElement;
        const bgColor = isDark ? initialColors.darkBg : initialColors.lightBg;
        const textColor = isDark ? "#ffffff" : "#333";
        if (root) {
            root.dataset.theme = theme;
            root.style.setProperty("--bg-color", bgColor);
            root.style.setProperty("--text-color", textColor);
            root.style.colorScheme = isDark ? "dark" : "light";
            root.style.color = textColor;
        }
        const paintBody = () => {
            if (!document.body) return;
            document.body.style.color = textColor;
        };
        if (document.readyState === "loading") {
            document.addEventListener("DOMContentLoaded", paintBody, { once: true });
        } else {
            paintBody();
        }
        try {
            localStorage.setItem("verge-theme-mode-cache", theme);
        } catch (error) {
            console.warn("[Tauri] 缓存主题模式失败:", error);
        }
        return isDark;
    };

    applyInitialTheme(initialTheme);

    console.log('[Tauri] 窗口初始化脚本执行完成');
"##;

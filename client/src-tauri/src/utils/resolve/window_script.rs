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
        // Visible text only. `textContent` includes <style> and <script> bodies,
        // which is how this probe spent two debugging sessions reporting an
        // @keyframes rule instead of the screen. Walk the elements that actually
        // render text and skip the non-visible tags.
        const visibleText = (node) => {
            if (!node) return '';
            let out = '';
            const walk = (el) => {
                for (const child of el.childNodes) {
                    if (child.nodeType === 3) {
                        out += child.nodeValue + ' ';
                    } else if (child.nodeType === 1) {
                        const tag = child.tagName.toLowerCase();
                        if (tag === 'style' || tag === 'script' || tag === 'noscript') continue;
                        walk(child);
                    }
                }
            };
            walk(node);
            return out.replace(/\s+/g, ' ').trim();
        };
        const report = (label) => {
            const root = document.getElementById('root');
            const overlay = document.getElementById('initial-loading-overlay');
            const kids = root ? root.childElementCount : -1;
            const text = visibleText(root).slice(0, 140);
            // Report the computed foreground colour and the body's, not just the
            // text content.
            //
            // "The text is in the DOM" and "the text is visible" are different
            // claims, and this probe previously only made the first one. The
            // activation screen shipped as an apparently blank white page while
            // every string was present and correct: the document body said
            // `color: var(--text-color)` (= white under a dark system scheme) and
            // MUI painted a white background, so the text was there and
            // invisible. A probe that cannot see contrast cannot catch that.
            //
            // `body_color` is included deliberately: it is the value the screen
            // must NOT inherit, so its presence here is what makes an inherited
            // colour immediately obvious in the log rather than requiring a
            // screenshot tool.
            let contrast = '';
            try {
                // The page background: the outermost painted element. This is
                // what proves the palette applied, on ANY screen — the old probe
                // keyed on an `h4`, so it silently reported nothing once the app
                // was past the activation gate.
                const shell = root && root.firstElementChild;
                const shellBg = shell ? getComputedStyle(shell).backgroundColor : '';
                const bodyBg = getComputedStyle(document.body).backgroundColor;
                const heading = root && root.querySelector('h4');
                contrast = ' page_bg=' + (shellBg || bodyBg);
                if (heading) {
                    const cs = getComputedStyle(heading);
                    contrast += ' heading_color=' + cs.color;
                }
                // The theme's own variables, which is where a leftover upstream
                // colour would show up even if the visible surfaces looked right.
                const rs = getComputedStyle(document.documentElement);
                contrast += ' accent=' + (rs.getPropertyValue('--primary-main') || 'unset');
            } catch (e) { contrast = ' contrast_err=' + e; }
            send('info', '[dom] ' + label +
                 ' root=' + (root ? 'yes' : 'MISSING') +
                 ' children=' + kids +
                 ' overlay=' + (overlay ? 'present' : 'removed') +
                 contrast +
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

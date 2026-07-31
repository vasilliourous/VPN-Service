# MyVPN UI / Aesthetics — Visual Design Spec

> **This spec describes the implemented UI.** The client uses a **Wails v2 +
> Vue 3** frontend (`v5/client/frontend/`) — the design tokens below are the
> actual values used in the components. The old Fyne-era spec was superseded
> when the GUI was migrated (see `WAILS-MIGRATION.md`).

---

## 1. Design Principles

1. **One glance, one action** — The user opens the window and instantly knows what to do.
2. **Status without reading** — Color + icon convey connection state at a glance.
3. **Tier sells itself** — Subtle visual cues differentiate Eco from Strike without extra UI complexity.

**Non-goals:**
- No custom animations (CSS transitions are kept minimal)
- No custom fonts (system fonts look native and load instantly)
- No heavy graphics (SVG icons only, keep the binary small)

---

## 2. Color Palette

```
Token              Hex       Purpose
─────────────────────────────────────────────
Background         #0D0D0F   Main window background
Surface            #1A1A1E   Card backgrounds, panels
SurfaceHover       #252529   Hover states on interactive elements
Border             #2E2E35   Dividers, input borders
TextPrimary        #F5F5F7   Main body text
TextSecondary      #8E8E96   Labels, hints, secondary info
Accent             #A855F7   Primary buttons, active indicators
AccentHover        #C084FC   Button hover
Success            #22C55E   Connected status
Error              #EF4444   Disconnected, errors
Warning            #F59E0B   Connecting, degraded
TierStrike         #EAB308   Strike tier badge (gold)
TierStealth        #A855F7   Stealth tier badge (purple)
TierEco            #8E8E96   Eco tier badge (grey)
```

### Implementation (Vue 3 + CSS)

The window background is also set natively (`WindowSetBackgroundColour(13, 13, 15, 255)`
in `app.go`) so there is no white flash while the WebView loads.

```css
body {
  background-color: #0D0D0F;
  color: #F5F5F7;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
  -webkit-font-smoothing: antialiased;
  user-select: none;
}

.app-container {
  background: linear-gradient(180deg, #0D0D0F 0%, #111114 100%);
  padding: 24px;
}

.card {
  background: #1A1A1E;
  border: 1px solid #2E2E35;
  border-radius: 12px;
}

.btn-primary { background: #A855F7; }        /* hover: #C084FC */
.btn-danger  { background: #EF4444; }
.btn-secondary { background: #2E2E35; }
.btn-accent   { background: #A855F7; }
```

---

## 3. Typography

| Use | Size | Weight | Align |
|-----|------|--------|-------|
| Brand title | 28px | 700 | Center |
| Card title | 18px | 600 | Left |
| Status text | 15px | 600 | Center |
| Body / labels | 13px | 400–500 | Left |
| Tier badge | 12px | 600 | Center |
| Code input | 16px | 400 (monospace) | Left |
| Diagnostics | 12px (monospace) | 400 | Left |

Font: system default stack (`-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, …`).
The activation code input uses a monospace stack (`'SF Mono', 'Fira Code', 'Cascadia Code'`).

---

## 4. Layout: Activation Screen

**Window:** 480×700 (min 380×500), Wails app. The activation screen is shown
when the device is not activated (`App.vue` → `v-if="!activated"`).

```
┌──────────────────────────────────────┐
│                                      │
│            [shield logo SVG]         │  brand icon, 48×48, purple #A855F7
│                 MyVPN                │  brand title, 28px bold
│          Secure School VPN           │  subtitle, 14px, #8E8E96
│                                      │
│  ┌────────────────────────────────┐  │
│  │  Activate                      │  │  card (max-width 380px)
│  │  Enter the code from your      │  │
│  │  activation card               │  │
│  │  ┌──────────────────────────┐  │  │
│  │  │ MYVPN-XXXX-XXXX-XXXX-C   │  │  │  code input, monospace, auto-format
│  │  └──────────────────────────┘  │  │
│  │  ✓ Valid code / ✗ error hint   │  │  live Luhn validation (green/red)
│  │  ┌──────────────────────────┐  │  │
│  │  │        Activate          │  │  │  button, #A855F7, spinner while loading
│  │  └──────────────────────────┘  │  │
│  └────────────────────────────────┘  │
│                                      │
│  ┌────────────────────────────────┐  │
│  │  ○ Eco      — $2/mo — Browsing │  │  tier info card
│  │  ◉ Stealth  — $4/mo — Streaming│  │
│  │  ⚡ Strike   — $8/mo — Gaming  │  │
│  └────────────────────────────────┘  │
└──────────────────────────────────────┘
```

**Implementation notes (ActivationScreen.vue):**
- Logo: inline SVG shield with checkmark, `#A855F7` fill
- Input: auto-inserts hyphens as the user types (max 23 chars), uppercases,
  and calls `ValidateCode` when complete — the hint turns green ("✓ Valid code")
  or red with the error message
- Button: disabled until ≥16 chars, shows a CSS spinner while loading
- Errors from the server appear in a red error box below the button

---

## 5. Layout: Main Screen

Shown when activated. Header + connection card + stats card + actions.

```
┌──────────────────────────────────────┐
│  ● Connected          [Strike ⚡]    │  header: StatusIndicator + label + TierBadge
│                                      │
│  ┌────────────────────────────────┐  │
│  │          [status SVG]          │  │  status circle (green shield / grey globe)
│  │          Connected             │  │  15px status text
│  │             Strike             │  │  tier name
│  │  ⚠ 3 days remaining           │  │  grace warning (graceDays ≤ 3, connected)
│  │  ┌──────────────────────────┐  │  │
│  │  │        Disconnect        │  │  │  btn-primary (Connect) / btn-danger (Disconnect)
│  │  └──────────────────────────┘  │  │  spinner while loading
│  └────────────────────────────────┘  │
│                                      │
│  ┌────────────────────────────────┐  │
│  │  State           running       │  │  stats card: engine state
│  │  Heartbeat       OK / 2 failures│  │  heartbeat failures
│  │  Grace Period    7 days        │  │  remaining grace
│  └────────────────────────────────┘  │
│                                      │
│  ┌────────────────────────────────┐  │
│  │  [ Diagnostics ] [Update 2.1..]│  │  actions; update button appears only
│  └────────────────────────────────┘  │  when update:available event fires
└──────────────────────────────────────┘
```

**Status colors:**
- Connected → green (`#22C55E`) status circle + "Connected"
- Engine crashed → red (`#EF4444`) + "Engine Error"
- Disconnected → grey (`#6B7280`) + "Disconnected"

**Diagnostics modal:** fixed overlay with a monospace `<pre>` report
(`GetDiagnostics` — version, OS, Go, activated, connected, tier, engine state,
heartbeat stats, grace days), Copy + Close buttons.

> **Note:** the original spec included a connection timer and a live speed
> progress bar. These are **not implemented** in the current client — the stats
> card (engine state / heartbeat / grace days) takes their place. A speed
> indicator remains a future enhancement.

---

## 6. Button States

```css
.btn { padding: 12px; border-radius: 8px; font-size: 15px; font-weight: 600; }
.btn:disabled { opacity: 0.4; cursor: not-allowed; }
```

| State | Label | Style |
|-------|-------|-------|
| Disconnected | "Connect" | `btn-primary` (#A855F7) |
| Connecting | spinner replaces label | disabled |
| Connected | "Disconnect" | `btn-danger` (#EF4444) |
| Loading | CSS spinner (16px ring) | disabled |

---

## 7. Tier Badge Styling

Implemented in `TierBadge.vue`:

| Tier | Icon | Color | Background |
|------|:----:|-------|------------|
| `strike` | ⚡ | `#EAB308` gold | `rgba(234, 179, 8, 0.2)` |
| `stealth` | ◉ | `#A855F7` purple | `rgba(168, 85, 247, 0.2)` |
| `eco` (default) | ○ | `#9CA3AF` grey | `rgba(107, 114, 128, 0.2)` |

Rendered as a 12px bold pill (`border-radius: 20px`, padding 4px 10px) next to
the status label in the main-screen header.

---

## 8. System Tray

The app window **starts hidden** (`StartHidden: true`) and **hides to the tray
when closed** instead of quitting (`setupSystemTray` in `app.go`). The tray
handlers listen for `tray:show` (show window) and `tray:quit` (exit) events.

> **Note:** the tray icon assets and the right-click tray menu described in the
> original Fyne-era spec are not yet implemented in the Wails client — only the
> close-to-tray behavior and the event hooks exist. This is a future
> enhancement.

---

## 9. Visual Testing Checklist

| Test | Expected |
|------|----------|
| App launches | No window shown initially (starts hidden). |
| Open from tray/dock | Window appears. Dark background, purple accents. |
| Activation screen | Brand icon, centered input, purple button, tier info. |
| Code typing | Auto-formats with hyphens, uppercases. |
| Invalid code | Red error hint below input (instant — no server call). |
| Valid code | Green "✓ Valid code". |
| Activate success | Transitions to main screen. |
| Connected | Green dot + "Connected". "Disconnect" button. |
| Engine crash | Red dot + "Engine Error". |
| Grace warning | Amber "⚠ X days remaining" when ≤3 days left and connected. |
| Stats card | State / Heartbeat / Grace Period values update. |
| Diagnostics | Modal opens with the full report; Copy works. |
| Update available | "Update X available" button appears. |
| Close window | Window hides. App keeps running (tray). |
| Eco badge | Grey pill + ○ icon. |
| Stealth badge | Purple pill + ◉ icon. |
| Strike badge | Gold pill + ⚡ icon. |
| High DPI | Everything renders crisp (WebView handles this). |

---

## 10. When to Do This

```
Phase 1–5: Backend (storage, activation, manager, heartbeat, updater)
Phase 6:   GUI functional (buttons work, screens exist, ugly)
               ↓
          NOW DO THIS UI POLISH
               ↓
Phase 6b:  Theme, icons, layout, tier badges, animations
Phase 8:   Test on all platforms, tweak spacing
```

The app should **function perfectly** before any UI polish. A beautiful app
that doesn't connect is useless. An ugly app that connects reliably is useful.
Make it work, then make it pretty.

# MyVPN UI / Aesthetics — Visual Design Spec

> **Implement AFTER the backend (activation, engine, updater) is fully working.**
> Make it work, then make it pretty.

---

## 1. Design Principles

1. **One glance, one action** — The user opens the window and instantly knows what to do.
2. **Status without reading** — Color + icon convey connection state at a glance.
3. **Tier sells itself** — Subtle visual cues differentiate Eco from Strike without extra UI complexity.

**Non-goals:**
- No custom animations (Fyne's animation support is limited; fighting it looks worse)
- No custom fonts (system fonts look native and load instantly)
- No heavy graphics (keep the binary small)

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

### Fyne Theme Implementation

```go
package gui

import (
    "image/color"
    "fyne.io/fyne/v2"
    "fyne.io/fyne/v2/theme"
)

type myVPNTheme struct{}

func (t *myVPNTheme) Color(c fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
    switch c {
    case theme.ColorNameBackground:
        return color.RGBA{13, 13, 15, 255}
    case theme.ColorNameButton:
        return color.RGBA{168, 85, 247, 255}
    case theme.ColorNamePrimary:
        return color.RGBA{168, 85, 247, 255}
    case theme.ColorNameForeground:
        return color.RGBA{245, 245, 247, 255}
    case theme.ColorNameInputBorder:
        return color.RGBA{46, 46, 53, 255}
    case theme.ColorNameDisabled:
        return color.RGBA{142, 142, 150, 255}
    case theme.ColorNameError:
        return color.RGBA{239, 68, 68, 255}
    case theme.ColorNameSuccess:
        return color.RGBA{34, 197, 94, 255}
    case theme.ColorNameWarning:
        return color.RGBA{245, 158, 11, 255}
    default:
        return theme.DefaultTheme().Color(c, v)
    }
}

func (t *myVPNTheme) Font(s fyne.TextStyle) fyne.Resource {
    return theme.DefaultTheme().Font(s)  // system font
}

func (t *myVPNTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
    return theme.DefaultTheme().Icon(n)
}

func (t *myVPNTheme) Size(s fyne.ThemeSizeName) float32 {
    return theme.DefaultTheme().Size(s)
}
```

---

## 3. Typography

| Use | Size | Weight | Align |
|-----|------|--------|-------|
| Connection status | 15px (`SizeNameBody`) | Normal | Left |
| Timer | 28px (`SizeNameHeading`) | Bold | Center |
| Tier badge | 11px (`SizeNameCaption`) | Normal | Right |
| Button text | 15px (`SizeNameBody`) | Bold | Center |
| Labels | 11px (`SizeNameCaption`) | Normal | Left |
| Code input | 15px (`SizeNameBody`) | Monospace (or system) | Center |

Font: system default. Fyne resolves this per-platform.

---

## 4. Layout: Activation Window

**Size:** 300×260px, fixed. Modal (blocks app until activated or quit).

```
┌──────────────────────────────┐  y=0
│         [logo mark]           │  y=20, 40×40px purple circle
│         MyVPN                 │  y=65, 20px bold
│                               │
│  Enter your activation code   │  y=110, 11px label, centered
│  ┌────────────────────────┐   │  y=130
│  │ ABC1-DEF2-GHI3         │   │  Entry widget, 15px centered
│  └────────────────────────┘   │  y=158
│                               │
│  ┌────────────────────────┐   │  y=180
│  │       Activate         │   │  Button, HighImportance, full width
│  └────────────────────────┘   │  y=210
│                               │
│  Invalid code — check & retry │  y=235, 11px error/success text
└──────────────────────────────┘  y=260
```

**Implementation notes:**
- Logo mark: simple `canvas.Circle` with accent fill, or a small embedded PNG
- Input: `widget.Entry`, centred text
- Button: `widget.Button`, `Importance: High`, full width via layout
- Status: `widget.Label` with colour set dynamically (red for error, green for success)

---

## 5. Layout: Control Panel Window

**Size:** 320×440px, fixed. Not modal — hides to tray on close.

```
┌──────────────────────────────────┐  y=0
│  MyVPN               — □ ×       │  y=0 (standard title bar)
│  ──────────────────────────────── │  y=30 (Separator)
│                                  │
│         ◉ Connected              │  y=50, status dot + text, 15px
│           00:12:34               │  y=75, timer, 28px center
│                                  │
│         ┌──────────────┐         │  y=120
│         │  DISCONNECT  │         │  Connect/Disconnect pill button
│         └──────────────┘         │  200×44px, HighImportance
│                                  │
│    Speed  42 Mbps                │  y=200, 11px label + value
│    ████████████████░░░░░░░░░░    │  y=218, ProgressBar, 260px wide
│                                  │
│  ──────────────────────────────── │  y=255 (Separator)
│  ⚙ Settings  ▸                   │  y=270, Accordion header
│  ──────────────────────────────── │  
│        Code: ABC1-****-GHI3      │  y=300, 11px label
│        Tier: Strike ⚡           │  y=316, 11px + badge
│        Version: 1.0.0            │  y=332, 11px
│                                  │
└──────────────────────────────────┘  y=440
```

### Widget Hierarchy

```go
content := container.NewBorder(
    // Top: status + timer
    container.NewVBox(
        statusRow,     // HBox: dot + status text
        timerLabel,    // centered, 28px
    ),
    // Bottom: settings accordion
    settingsAccord,
    nil, nil,
    // Center: connect button + speed
    container.NewVBox(
        connectBtn,    // centered, padded
        speedLabel,    // "Speed: XX Mbps"
        speedBar,      // ProgressBar
    ),
)
```

---

## 6. Button States

```go
type buttonState int
const (
    stateDisconnected buttonState = iota
    stateConnecting
    stateConnected
    stateDisconnecting
    stateError
    stateSuspended
)

func updateButton(state buttonState) {
    switch state {
    case stateDisconnected:
        connectBtn.SetText("Connect")
        connectBtn.Importance = widget.HighImportance   // purple
        connectBtn.Enable()
    case stateConnecting:
        connectBtn.SetText("Connecting...")
        connectBtn.Disable()
    case stateConnected:
        connectBtn.SetText("Disconnect")
        connectBtn.Importance = widget.HighImportance   // purple
        connectBtn.Enable()
    case stateDisconnecting:
        connectBtn.SetText("Disconnecting...")
        connectBtn.Disable()
    case stateError:
        connectBtn.SetText("Tap to Retry")
        connectBtn.Importance = widget.DangerImportance  // red
        connectBtn.Enable()
    case stateSuspended:
        connectBtn.SetText("Account Suspended")
        connectBtn.Disable()
    }
}
```

---

## 7. Tier Badge Styling

```go
func tierBadge(tier string) *canvas.Text {
    var txt *canvas.Text
    switch tier {
    case "strike":
        txt = canvas.NewText("Strike ⚡", color.RGBA{234, 179, 8, 255})   // gold
    case "stealth":
        txt = canvas.NewText("Stealth ◉", color.RGBA{168, 85, 247, 255})  // purple
    case "eco":
        txt = canvas.NewText("Eco ○", color.RGBA{142, 142, 150, 255})     // grey
    }
    txt.TextSize = 13
    txt.TextStyle = fyne.TextStyle{Bold: true}
    return txt
}
```

The badge sits next to the tier name in the settings section. It's a small
visual cue that reinforces the value ladder without any layout changes.

---

## 8. Speed Progress Bar

```go
speedBar := widget.NewProgressBar()
speedBar.Min = 0
speedBar.Max = 100

func updateSpeed(currentMbps int) {
    var maxMbps int
    switch tier {
    case "eco":      maxMbps = 5
    case "stealth":  maxMbps = 48
    case "strike":   maxMbps = probeResult  // or 50 as fallback
    }

    pct := float64(currentMbps) / float64(maxMbps) * 100
    if pct > 100 { pct = 100 }
    speedBar.SetValue(pct)
    speedLabel.SetText(fmt.Sprintf("Speed  %d Mbps", currentMbps))
}
```

The bar fills proportionally to how close you are to the tier cap.
Eco users will see it fill up fast (5 Mbps cap), Strike users will
see it hover around 40–80%.

---

## 9. System Tray Icons

Embed three 22×22 PNGs:

| State | Visual | Colour |
|-------|--------|--------|
| Disconnected | Grey circle with diagonal slash | `#8E8E96` |
| Connecting | Amber circle | `#F59E0B` |
| Connected | Green circle with small checkmark | `#22C55E` |

### Embedding

```go
//go:embed icons/tray_connected.png
var trayConnected []byte

var resourceTrayConnected = &fyne.StaticResource{
    StaticName: "tray_connected.png",
    StaticContent: trayConnected,
}
```

### Platform Notes
- **macOS:** Use template images (alpha-only). macOS tints them automatically.
  Name them with `Template` suffix (e.g. `tray_connectedTemplate.png`).
- **Windows:** Coloured PNGs work directly.
- **Linux:** Depends on DE. Coloured PNGs usually work.

---

## 10. Menu Structure

### Right-click tray menu

```
MyVPN
──────
Show Window
──────────
Connect        ← greyed out when connected
──────────
Settings
Diagnostics
──────────
Quit
```

---

## 11. Visual Testing Checklist

| Test | Expected |
|------|----------|
| App launches | System tray icon appears. No window shown initially. |
| Click tray icon | Control panel opens. Dark background, purple accents. |
| Activation screen | Clean input, centered, purple button. |
| Invalid code | Error text in red below input (instant — no server call). |
| Valid code | Success text in green. Transitions to main screen. |
| Connected | Green dot. "Disconnect" button. Timer counting up. |
| Speed bar | Moves when downloading. |
| Disconnected | Grey dot. "Connect" button. Timer reset. |
| Close window | Window hides. Tray icon remains. Connection stays active. |
| Right-click tray | Menu shows: Show, Connect/Disconnect, Settings, Quit. |
| Quit from menu | App exits. Engine stops. Tray icon disappears. |
| Eco badge | Grey text + ○ icon. |
| Stealth badge | Purple text + ◉ icon. |
| Strike badge | Gold text + ⚡ icon. |
| macOS | Tray icon is template (tinted by system). Native title bar. |
| Windows | Tray icon is coloured PNG. Native title bar. |
| High DPI | Everything renders crisp (Fyne handles this). |

---

## 12. When to Do This

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

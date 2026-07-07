# VPN Plan — Critical Review

**Reviewed documents:** `ACTION-PLAN.md` (v3 Hardened), `PLAN.md` (v2), `BUSINESS-OVERVIEW.md`

---

## Overall Verdict

**Is it comprehensive enough that an agent could build everything?** — Mostly yes for the business logic layer (activation, heartbeat, state machine, branding, update signing), but there are serious gaps in the platform-specific machinery that would stop a naive agent dead. The TUN networking, Windows service, macOS daemon, and routing edge cases are sketched at best.

---

## 🚩 1. The 4–5 Day Estimate Is Completely Unrealistic

This is the biggest red flag. Building a production VPN client with all of the following takes **4–6 weeks** for one developer, or **2–3 weeks** for a focused pair:

- Privileged Windows SYSTEM service + named pipe IPC
- macOS launchd daemon
- TUN device management on two platforms
- systray cross-platform GUI
- Two protocol engine integrations (Hysteria 2 + usque)
- Cryptographic update system
- Certificate-pinned heartbeat + telemetry
- Activation + device binding + token rotation
- Kill switch + DNS guard + split tunnel
- Process sandboxing + binary renaming
- Log rotation + error handling

Calling it 4–5 days sets an expectation that is not achievable even with a fully automated agent hitting zero bugs.

---

## 🪟 2. The Privileged Helper Service Is the Biggest Gap

The plan shows the IPC protocol (JSON-line over named pipe / Unix socket) and the `SendCommand` function in the client, but crucial pieces are missing:

| Missing Item | Impact |
|---|---|
| **Windows SCM registration** — no `StartService()`, `ServiceMain()`, or uninstall logic | Service can't be installed or managed reliably |
| **Windows crash recovery** — no service recovery actions configured | If the crash loop restarts, user has no internet |
| **macOS launchd plist** — no daemon lifecycle or socket activation config | Daemon won't auto-start, won't survive reboots |
| **macOS privilege drop** — no guidance on dropping root after TUN creation | Daemon runs as root full-time (security risk) |
| **Actual TUN creation code** — on Windows needs `wintun.dll` via `golang.org/x/sys/windows`, on macOS needs `/dev/tun` with `ioctl` or a `utun` interface | Without this, no tunnel actually exists |
| **Helper crash during active connection** — TUN orphaned, no recovery path | User loses internet until manual cleanup |

An agent could produce code that *compiles*, but getting it to actually create a working TUN interface and route real traffic is where the real engineering lives.

---

## 🧩 3. Conflicting Document Versions

`PLAN.md` (v2) promises support for **TUIC v5, VLESS-REALITY, and Xray-core** with four protocol modes. `ACTION-PLAN.md` (v3) narrows to **Hysteria 2 + usque/Warp only**.

An agent following both documents would get confused about what to actually build. The v3 reduction (Hysteria 2 primary + usque fallback) is the right call for MVP, but the v2 document hasn't been updated and still describes the broader scope as if it's in scope.

---

## 🔧 4. Hysteria 2 Process Management Is Underspecified

The plan says "launch as disguised process" but doesn't detail:

- How to generate the Hysteria 2 JSON config for the specific server
- How to pass per-user credentials (or if it uses a shared server secret)
- How to detect engine readiness (port open? stdout line? health endpoint?)
- How to gracefully stop the process (SIGINT? process tree kill?)
- How to distinguish engine crash from remote server failure
- How Hysteria 2's QUIC handles networks that block all UDP (many school WiFi networks do)

---

## 🌐 5. TUN Routing Edge Cases Are Deceptively Complex

The kill switch deletes the default route through the TUN interface. This works in the happy path but fails in several real-world scenarios:

| Scenario | Problem |
|---|---|
| **WiFi disconnects** | TUN interface still exists but has no carrier → all traffic blackholes |
| **Multiple network interfaces** (WiFi + Ethernet) | Only one default route is managed; other interfaces leak |
| **IPv6 traffic** | Plan is IPv4 only, but most school networks have IPv6 → IPv6 traffic leaks outside the tunnel |
| **macOS pfctl reloads** | macOS resets pf rules on network changes (WiFi roam, sleep/wake) → kill switch disarm |
| **Windows network profile changes** | Public/private/Domain network switches can reset firewall rules |

The plan acknowledges some routing complexity but doesn't provide robust solutions for these edge cases.

---

## 🍎 6. macOS Notarization & Gatekeeper — Unaddressed

The plan mentions Windows SmartScreen ("Click 'More info' → 'Run anyway'") but says **nothing about macOS**.

Without a **$99/yr Apple Developer account** and notarization, macOS users will see:

> "myvpn" cannot be opened because the developer cannot be verified.

This requires: System Settings → Privacy & Security → "Open Anyway". For non-technical students, this is a dealbreaker. Additionally:

- The **launchd helper daemon** needs to be signed with a hardened runtime entitlement
- The daemon requires a **com.apple.developer.system-extension.install** entitlement
- Without notarization, the helper daemon may be blocked by macOS security entirely

---

## 📱 7. Mobile Is Entirely Absent

The business model targets students on **school WiFi**. Students primarily use **phones**. Windows + macOS only means ~half the potential market is unreachable. This isn't necessarily a flaw — it's scoped — but worth flagging if the business model depends on user volume.

---

## 🧪 8. No Testing Strategy Whatsoever

Zero mentions of:

- Unit tests for the state machine, activation logic, token rotation
- Integration tests with a real Hysteria 2 server
- TUN routing test harness (can't test kill switch without actually creating a TUN)
- CI/CD pipeline
- Regression tests for the auto-reconnect loop

For a product where a bug can **leave a user without internet**, testing is not optional.

---

## 📦 9. "Bundled vs Runtime Download" Contradiction

Phase 1.7 mentions bundling Hysteria 2 in the installer (solving bootstrap), but Phase 1.8 is explicitly "Runtime Engine Download." These need reconciliation:

- **Hysteria 2** should be bundled in the installer (no internet needed for setup)
- **usque/Warp** is downloaded at runtime only if the primary engine fails and fallback is needed

The plan doesn't clearly state which is which.

---

## 🔄 10. No Rollback Strategy

The update system has downgrade protection (`min_version`), meaning if a bad update ships:

- There's no way to push a previous version
- You'd need to bump the min version AND ship a fix
- If the bad update breaks connectivity entirely, clients can't download the fix
- No recovery mode, no side-channel update, no rollback mechanism

---

## ✅ What IS Comprehensive Enough for an Agent

These parts are detailed enough that an agent could produce working code with little ambiguity:

| Component | Why it's sufficient |
|---|---|
| **Activation code format** | Luhn-mod-N checksum algorithm, 16-char format, full validation pseudocode |
| **PocketBase schema** | Table definitions with field types, indexes, rate-limit collection |
| **HMAC device binding** | Clear spec of `HMAC-SHA256("activation"+code, secret)`, client-held secret |
| **Connection state machine** | 6 explicit states with transitions, systray behaviour per state |
| **Heartbeat protocol** | Request/response fields, token rotation, grace period logic, remote commands |
| **Update signing scheme** | Signed message format, key rotation, platform scoping, Go verification code |
| **Branding layer** | Protocol → display name mapping, binary rename mapping, plan name mapping |
| **Log rotation** | 10 MB limit, 3 files kept, cross-platform |

---

## 🧰 What an Agent Would Need That the Plan Doesn't Provide

1. **A Windows build environment** (or cross-compile setup with CGO) for `wintun.dll` + the service binary
2. **A macOS build environment** with `codesign` for signing the app + daemon
3. **Access to a real VPS** to test the backend against
4. **Hysteria 2 + usque binaries** to bundle/test with (not provided, need to be downloaded)
5. **A test network** where actual TUN creation and routing can be verified
6. **Decision guidance on v2 vs v3 protocol scope** — which protocols actually ship?

---

## 📊 Summary Scorecard

| Dimension | Score (1–10) | Notes |
|---|---|---|
| Business logic specification | **8** | Activation, heartbeat, state machine well-defined |
| Platform-specific implementation | **3** | Windows service, macOS daemon, TUN routing severely underspecified |
| Security architecture | **7** | Good crypto choices, but some details missing (key storage, private key handling) |
| Testing / QA | **1** | Virtually absent |
| Build / deploy | **4** | Cross-compilation mentioned but not detailed; no CI/CD |
| Time estimate realism | **1** | 4–5 days vs realistic 3–6 weeks |
| Adaptive bandwidth probing | **9** | Phase 1.8 redesigned: fresh probe on every connect + continuous EWMA background monitor with RTT-based bufferbloat detection — handles QoS shaping fluctuations that previously caused Hysteria 2 buffering. Includes `monitor.go`, `probe.go`, `usque.go` with tests. |

---

**Final word:** An agent could produce a working **codebase skeleton** with all the business logic, auth, update signing, and backend from this plan. But to get a **working VPN** that actually routes traffic through a TUN interface with a kill switch and helper service, the platform-specific implementation needs substantially more detail — particularly for the Windows service and macOS daemon.

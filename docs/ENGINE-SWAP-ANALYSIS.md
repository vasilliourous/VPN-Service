# Engine & TUN Analysis: sing-box → mihomo, and the Linux Elevation Gap

**Status:** analysis only — no code changed
**Date:** 2026-08-03 (corrected 2026-08-03 after user context: the school test was on a BYOD Linux device)
**Trigger:** On the school network, Clash Rev Meta was the only client with consistently working TUN.
**Scope:** Root-cause analysis + what a fix entails. Two independent tracks: **(A) Linux TUN elevation** (the actual cause of the observed failure) and **(B) engine swap sing-box → mihomo** (optional, Windows-side rationale).

---

## 1. TL;DR

- The school test was **not** an engine comparison. The user tested on **their own Linux device** (school is BYOD; Wayland session, non-root).
- TUN on Linux requires **root / CAP_NET_ADMIN**. Clash Rev Meta worked because it **escalates properly**; hiddify failed because it didn't; **Locus has no Linux elevation path at all** — direct mode, helper binary not shipped → TUN is dead on arrival on any non-root Linux session.
- Fixing the observed failure = **giving the client a Linux TUN elevation mechanism** (pkexec/polkit or a revived privileged helper). This is engine-agnostic.
- The **sing-box → mihomo swap remains optional**: its rationale is Windows-side (WFP fragility documented in FIXES.md) plus mihomo's external-controller API for real egress checks. It does **not** fix the Linux TUN failure by itself.

---

## 2. Terminology (important correction)

**Clash Rev Meta is a GUI frontend (Tauri app).** The actual tunnel engine underneath is the **mihomo core** (a.k.a. Clash Meta core, `MetaCubeX/mihomo`) — a single Go binary, structurally identical to our sing-box subprocess model. The GUI's job includes **spawning the core with the right privileges** — and that plumbing is exactly what differed between the clients in the school test.

---

## 3. Correction: what actually happened on the school network

User context (2026-08-03):

> Clash Rev Meta worked because hiddify didn't properly escalate for TUN — the Wayland session wasn't owned by root, and TUN can't work properly without root. The school uses BYOD, so I was using my own Linux device.

**Root cause of the observed failure:** TUN elevation on Linux, not engine behavior and not the network (Clash Rev Meta tunneled through the *same* network fine once its core ran with privileges).

| Client | Linux TUN outcome | Why |
|---|---|---|
| Clash Rev Meta | ✅ worked | Escalates the mihomo core to root properly |
| hiddify | ❌ failed | Failed to escalate (Wayland session not owned by root) |
| Locus (ours) | ❌ fails (deterministic) | **No elevation path at all** — see §4 |

### 3.1 What the code shows (the gap is real, not hypothetical)

- `legacy/wails-client/app.go:157` → `a.mgr.SetHelperMode(false)` — **direct mode forced on all platforms**.
- `startDirect` (process.go) spawns the engine as a plain child of the app process: no pkexec, no sudo, no polkit, no setuid, no CAP_NET_ADMIN delegation.
- `autoStartHelper` (pkexec/sudo on Unix, UAC `RunAs` on Windows) exists but only drives the **legacy `locus-helper`** binary, which is **not shipped in V5** (moved to `v5/legacy/`) — the function can only fail with "locus-helper binary not found alongside sing-box". Dead code.
- `process_unix.go` — nothing but process-group detachment and a pgrep guard.
- Consequence: on any non-root Linux session, the engine cannot open `/dev/net/tun` or install routes → TUN fails before the tunnel can even start. On the user's Wayland BYOD session this is 100% reproducible, engine-independent.

### 3.2 Implication for the engine question

On Linux, **both sing-box and mihomo require root (or CAP_NET_ADMIN) for TUN**. mihomo gained no advantage in the school test — the winning difference was *who escalates the core*, which is application plumbing, not core TUN behavior. Swapping engines would have changed nothing about the observed failure.

---

## 4. Windows-specific: why sing-box's TUN is fragile (hypothesis — *not* what the school test demonstrated)

| | sing-box (current) | mihomo |
|---|---|---|
| TUN driver | wintun | wintun |
| auto-route mechanism | default route via TUN; Windows implementation interacts with **WFP** (Windows Filtering Platform) | routing-table based; no WFP involved |
| strict-route | **WFP filters**: "strictly block all connections not from the TUN", block port 53 on non-TUN interfaces | Windows **firewall rules** to prevent DNS leaks (default **off**) |
| auto-redirect | n/a | **Linux only** (iptables/nftables) |
| DNS hijack | sniff + `hijack-dns` rule action | `dns-hijack: [any:53]` in the TUN stack |

sing-box's Windows TUN is WFP-heavy; our own FIXES.md trail (Follow-ups 1–10) documents the damage: `strict_route: true` killed all connectivity on school laptops; orphaned sing-box processes left stale WFP filters that required reboot; DNS loops and sniffing workarounds all orbit the same WFP/network-stack interaction. mihomo's default Windows TUN avoids WFP (plain routes, strict-route off by default) — so *if* Windows school laptops still misbehave after the elevation fix, mihomo is the plausible alternative. This remains a **hypothesis to validate on Windows**, not the observed cause.

---

## 5. Current integration surface (what a fix touches)

### Track A — Linux TUN elevation (the actual fix)

| File | Change |
|---|---|
| `legacy/wails-client/app.go` | Don't force `SetHelperMode(false)` on Linux (or add a Linux-only elevation path for direct mode) |
| `legacy/wails-client/internal/manager/process.go` | Revive/replace `autoStartHelper` + helper IPC, or add `pkexec` elevation of the engine itself; keep Windows on direct mode |
| `v5/legacy/` (helper source) | Rebuild + ship a minimal privileged helper (TUN create/route via IPC), or replace with polkit policy + elevated engine spawn |
| Packaging (`build.yml`, zip contents) | Ship helper binary (+ `.service`/polkit policy file if used) |
| `process_unix.go` | Guard logic per-OS (unchanged shape) |

Options for the elevation mechanism (see §8 for recommendation):

1. **pkexec/polkit the engine directly** — `pkexec` spawns the engine as root with a GUI auth prompt (GNOME/KDE standard). Cheap, no helper binary; lifecycle caveat: the engine is no longer a direct child, but pid-based stop still works.
2. **Revive the privileged helper** — the old architecture's pattern (helper owns TUN + routes, engine stays unprivileged or is spawned by the helper). More moving parts (binary + IPC + packaging) but matches the design V5 was built from.
3. **systemd service / D-Bus + polkit policy** — the "proper" way Clash GUIs often do it (verge-mihomo service); most robust, most work.

### Track B — engine swap (unchanged surface, now optional)

| File | What it does today | Swap impact |
|---|---|---|
| `legacy/wails-client/internal/manager/process.go` | sing-box JSON config gen, spawn, health, guard | YAML config gen, `mihomo -f -d` spawn, guard patterns (`mihomo`, `clash-meta`, `verge-mihomo`) |
| `process_unix.go` / `process_windows.go` | guard per-OS | pattern rename |
| `process_test.go` | JSON invariant tests | YAML invariant tests |
| `app.go` | `findSingBox`, config filename | rename only; logic is engine-agnostic (verified) |
| `.github/workflows/build.yml` | sing-box asset download/bundle | mihomo assets (`.gz` raw on Linux/mac, `mihomo-windows-amd64-v3-vX.zip` on Windows; latest v1.19.29) |
| `engines/README.md`, docs | engine instructions | point at mihomo |

Frontend, updater, heartbeat, activation: **untouched** (verified engine-agnostic).

---

## 6. Config mapping (sing-box JSON → mihomo YAML, if Track B proceeds)

Invariants preserved: DNS through tunnel; server-domain resolution direct; `locus0` TUN, auto-route on, strict-route off; server-IP → direct; raw ss UDP; debug logs.

```yaml
log-level: debug

tun:
  enable: true
  device: locus0
  stack: mixed                # Clash Rev Meta default; gvisor is the other safe choice
  dns-hijack:
    - any:53
  auto-route: true
  auto-detect-interface: true
  strict-route: false         # same reason as sing-box: avoid Windows firewall/WFP issues

dns:
  enable: true
  ipv6: false
  enhanced-mode: redir-host   # real IPs — closest to current behavior; fake-ip later
  nameserver:
    - 'https://1.1.1.1/dns-query#PROXY'   # DNS through the tunnel (≈ sing-box detour=proxy)
  proxy-server-nameserver:
    - 'tls://8.8.8.8:853'                 # server-domain resolution direct (≈ default_domain_resolver)
  default-nameserver:
    - 8.8.8.8
    - 1.1.1.1

proxies:
  - name: PROXY
    type: ss
    server: networkingguides.duckdns.org  # per tier: 8443/8444/8445
    port: 8443
    cipher: aes-256-gcm
    password: ${from server_config}
    udp: true                             # raw ss UDP — same as today, no UoT

proxy-groups:
  - name: myvpn
    type: select
    proxies: [PROXY, DIRECT]

rules:
  - IP-CIDR,<server-ip>/32,DIRECT
  - MATCH,myvpn
```

Verified facts: `#PROXY` nameserver suffix exists in mihomo DNS docs (routes DNS connections through a proxy); `proxy-server-nameserver` avoids the proxy-domain resolution loop (sing-box issue #2207 equivalent); `tun.dns-hijack` replaces sniff + hijack-dns.

---

## 7. Process lifecycle mapping (Track B)

| Concern | sing-box today | mihomo |
|---|---|---|
| Config test | none (500ms exit probe) | `mihomo -t -f config.yaml` — first-class |
| Spawn | `sing-box run -c <json> -D <dir>` | `mihomo -f config.yaml -d <dir>` |
| stderr capture | bounded buffer | works (also `log-file`) |
| Health loop | unchanged | unchanged |
| Foreign-process guard | `foreignSingBoxRunning` | match `mihomo`, `clash-meta`, `verge-mihomo` |
| Elevation (Windows) | admin required | admin required — same |
| Elevation (Linux) | **none today — Track A required** | **none either — Track A required** |
| wintun.dll | bundled in sing-box zip | **verify**: mihomo Windows zip may not include it; fallback = official wintun.net driver (same driver) |

**Phase-2 bonus:** mihomo `external-controller` REST API (`external-controller: 127.0.0.1:9090`) → `GET /proxies/PROXY/delay` gives real egress checks, fixing the known "says Connected but no internet" gap that sing-box cannot offer without log parsing.

---

## 8. Options & recommendation

- **Option A — Fix Linux TUN elevation (the actual fix, recommended first).** Cheapest robust path: pkexec/polkit the engine spawn on Linux (GUI auth prompt, no helper binary), keep Windows direct mode. Fixes the observed school-network failure regardless of engine. ~0.5–1 day.
- **Option B — Engine swap to mihomo (optional, later).** Rationale is now Windows-side only (WFP avoidance hypothesis) + external-controller egress checks. Does not help Linux BYOD without Track A. ~1.5–2 days.
- **Option C — Both.** Do A now (unblocks the user's device today), validate on the school network, then decide B using Windows school-laptop data.
- **Option D — Do nothing / keep patching sing-box WFP issues on Windows.** Already 10 follow-up fixes; diminishing returns.

**Recommendation: A now, C if Windows data still misbehaves after A.** Keep sing-box code around for rollback either way.

---

## 9. Effort estimate

| Task | ~Effort |
|---|---|
| **A: Linux pkexec/polkit elevation of engine spawn + error UX** | **4–6 h** |
| A: packaging (polkit prompt deps are system-provided; no binary to ship) | 0.5 h |
| A: Linux smoke tests (GNOME/KDE Wayland, non-root, reboot) | 2 h |
| B: `generateConfig` → mihomo YAML | 3–4 h |
| B: spawn/stop/health/guard changes | 2–3 h |
| B: `process_test.go` rewrite | 2 h |
| B: CI download/packaging + docs | 2–3 h |
| School-network validation session (A alone, then A+B if doing C) | 0.5–1 day |

**Track A alone: ~1 dev day incl. validation.** A+B: ~2.5–3 dev days + validation.

---

## 10. Verification plan (school network, BYOD Linux)

1. **Elevation UX:** Connect on a non-root Wayland session → polkit prompt appears → auth → TUN comes up (`ip addr show locus0`, `ip route` shows split default). Cancel prompt → clean error, no half-state.
2. **Repeated connect/disconnect × 20** — no orphaned engine, TUN always cleaned, no polkit prompt spam.
3. **DNS:** real site resolution (through-tunnel DNS invariant); behavior with school DHCP DNS.
4. **Egress:** HTTP/HTTPS; UDP on Strike; TCP fallback on eco/stealth (UDP-blocked WiFi).
5. **WiFi roaming, sleep/resume.**
6. **Kill-mid-session recovery** without reboot.
7. **Second-instance guard** (foreign mihomo/clash-meta running → clean refusal).
8. **Fresh boot → first-try connect.**
9. If Track B is also in: same matrix, plus confirm `-t` config validation catches bad tier configs.

---

## 11. References

- User context (2026-08-03): school is BYOD; test device = user's Linux laptop, Wayland, non-root; Clash Rev Meta's edge = proper TUN escalation; hiddify failed to escalate.
- Code evidence: `legacy/wails-client/app.go:157` (`SetHelperMode(false)`), `process.go` (`startDirect`, `autoStartHelper` dead path), `process_unix.go`, `v5/legacy/`.
- mihomo TUN docs: https://wiki.metacubex.one/en/config/inbound/tun/
- mihomo DNS docs: https://wiki.metacubex.one/en/config/dns/
- mihomo releases: https://github.com/MetaCubeX/mihomo/releases (v1.19.29 latest at analysis time)
- sing-box TUN docs: https://sing-box.sagernet.org/configuration/inbound/tun/
- Our evidence trail: `docs/FIXES.md` (Follow-ups 1–10)

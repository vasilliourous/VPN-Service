# Engine & TUN Analysis: sing-box → mihomo, and the Linux Elevation Gap

> **⚠️ HISTORICAL — describes the RETIRED Wails client.** Both tracks are moot for
> the shipping fork (it bundles mihomo *and* inherits Verge's pkexec → root-service
> elevation). It has its own correction banner above §1. Nothing here is a task list
> for `client/`.

**Status:** analysis only — no code changed. **⚠️ Written about the RETIRED Wails client; Track A is now MOOT for the shipping fork — see the banner above §1.**
**Date:** 2026-08-03 (corrected 2026-08-03 after user context: the school test was on a BYOD Linux device)
**Trigger:** On the school network, Clash Rev Meta was the only client with consistently working TUN.
**Scope:** Root-cause analysis + what a fix entails. Two independent tracks: **(A) Linux TUN elevation** (the actual cause of the observed failure) and **(B) engine swap sing-box → mihomo** (optional, Windows-side rationale).

> ## ⚠️ CORRECTION (2026-09-26): Track A describes the RETIRED client and is MOOT
>
> Everything below about "Locus has no Linux elevation path" was measured against
> **`legacy/wails-client/`** — the Go + Wails + sing-box client that did not ship.
> Those statements are accurate about *that* client and are **false about the
> shipping fork in `client/`**.
>
> `client/` is built on Clash Verge Rev, and **inherits Verge's elevation path
> intact** — no helper binary of its own is needed:
>
> - `crates/…/clash_verge_service_ipc` → `management.rs::elevate()` runs the
>   installer under **`pkexec`**, falling back to **`sudo`** when `pkexec` is
>   missing or exits 127. `utils/help.rs::linux_elevator()` probes for `pkexec`.
>   (macOS: `osascript … with administrator privileges`. Windows: `Start-Process
>   -Verb RunAs`.)
> - It installs a **root service** — `core/service.rs::install_service()` →
>   `clash-verge-service-install` — and the service runs the core from its own
>   administrator-approved directory (`stage_approved_core`, digest-pinned).
> - `core/runstate/health.rs::tun_capable()` is
>   `self.is_admin || self.service_usable()`.
>
> So the chain is **pkexec → install root service → service runs mihomo as root →
> TUN works.** Track A's "fix" is not a piece of work that remains to be done; it
> is plumbing the fork already has, because upstream's machinery is *used, not
> replaced* (`client/docs/UPSTREAM-CHANGES.md` §1).
>
> **The one thing still genuinely open** is that nobody has **run** that chain on
> real Linux hardware in a non-root session — see `docs/STILL-OPEN.md` "Linux TUN
> elevation". That is a validation gap, not a missing feature.
>
> Read the rest of this document as **history**: it explains why the 2026-08-03
> school test failed and why the engine swap was considered. Do not act on its
> Track A recommendations for `client/`.

---

## 1. TL;DR

- The school test was **not** an engine comparison. The user tested on **their own Linux device** (school is BYOD; Wayland session, non-root).
- TUN on Linux requires **root / CAP_NET_ADMIN**. Clash Rev Meta worked because it **escalates properly**; hiddify failed because it didn't; **the retired Wails client had no Linux elevation path at all** — direct mode, helper binary not shipped → TUN was dead on arrival on any non-root Linux session. **The shipping fork does have one** (see the correction above).
- For the retired client, fixing the observed failure meant **giving it a Linux TUN elevation mechanism** (pkexec/polkit or a revived privileged helper) — engine-agnostic. The fork solved this by inheritance.
- The **sing-box → mihomo swap remains moot too**: the fork bundles mihomo, so Track B is settled by construction. Its original rationale was Windows-side (WFP fragility documented in FIXES.md) plus mihomo's external-controller API for real egress checks.

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
| Locus *(the RETIRED Wails client)* | ❌ fails (deterministic) | **No elevation path at all** — see §4. **The shipping fork is not this client**; it inherits Verge's pkexec → root service and reports `tun_capable() == true` |

### 3.1 What the code shows (the gap was real in the RETIRED client)

> Every bullet below is about `legacy/wails-client/`. None of it describes
> `client/`.

- `legacy/wails-client/app.go:157` → `a.mgr.SetHelperMode(false)` — **direct mode forced on all platforms**.
- `startDirect` (process.go) spawns the engine as a plain child of the app process: no pkexec, no sudo, no polkit, no setuid, no CAP_NET_ADMIN delegation.
- `autoStartHelper` (pkexec/sudo on Unix, UAC `RunAs` on Windows) exists but only drives the **legacy `locus-helper`** binary, which is **not shipped in V5** (moved to `v5/legacy/`) — the function can only fail with "locus-helper binary not found alongside sing-box". Dead code.
- `process_unix.go` — nothing but process-group detachment and a pgrep guard.
- Consequence: on any non-root Linux session, the engine cannot open `/dev/net/tun` or install routes → TUN fails before the tunnel can even start. On the user's Wayland BYOD session this is 100% reproducible, engine-independent.

### 3.2 Implication for the engine question

On Linux, **both sing-box and mihomo require root (or CAP_NET_ADMIN) for TUN**. mihomo gained no advantage in the school test — the winning difference was *who escalates the core*, which is application plumbing, not core TUN behavior. Swapping engines would have changed nothing about the observed failure.

**That conclusion is what the fork acted on, and it is the reason this document is
history.** Rather than repair the retired client's plumbing (Track A) or swap its
engine (Track B), the project rebuilt on Clash Verge Rev — which supplied both:
Verge's service/elevation machinery *and* mihomo. The plumbing diagnosis was right;
the remedy chosen was different.

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

### Track A — Linux TUN elevation (MOOT for the fork — historical, and never done for the retired client)

> **Nothing in this table is a piece of work for `client/`.** It is the change list
> that *would* have been required for the retired Wails client, which never
> received it. The fork inherited a working elevation path instead — see the
> correction banner above §1. Kept because it explains the 2026-08-03 failure.

| File (retired client) | Change that was needed |
|---|---|
| `legacy/wails-client/app.go` | Don't force `SetHelperMode(false)` on Linux (or add a Linux-only elevation path for direct mode) |
| `legacy/wails-client/internal/manager/process.go` | Revive/replace `autoStartHelper` + helper IPC, or add `pkexec` elevation of the engine itself; keep Windows on direct mode |
| `v5/legacy/` (helper source) | Rebuild + ship a minimal privileged helper (TUN create/route via IPC), or replace with polkit policy + elevated engine spawn |
| Packaging (`build.yml`, zip contents) | Ship helper binary (+ `.service`/polkit policy file if used) |
| `process_unix.go` | Guard logic per-OS (unchanged shape) |

The three mechanisms that were considered — and **what the fork actually does**:

1. **pkexec/polkit the installer, then run a root service** — ✅ **this is the
   fork's design.** `management.rs::elevate()` pkexecs the installer (sudo
   fallback), which registers `clash_verge_service_ipc`; the service runs the core
   as root. `tun_capable()` is `is_admin || service_usable()`.
2. **Revive a separate privileged helper with its own IPC** — ❌ not used. This is
   what the retired client had (and lost); the fork deliberately does not carry a
   helper binary, because `internal/helper`'s hand-rolled elevation was buggy three
   separate times (FIXES #17, #40, #41).
3. **systemd service / D-Bus + polkit policy** — ✅ effectively what option 1
   becomes: `clash-verge-service-install` registers a system service rather than
   spawning a transient privileged child.

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
| Elevation (Linux) | none — the *retired* client had no path (Track A) | none needed: both engines require root, and the fork gets it via pkexec → root service |
| wintun.dll | bundled in sing-box zip | **verify**: mihomo Windows zip may not include it; fallback = official wintun.net driver (same driver) |

**Phase-2 bonus:** mihomo `external-controller` REST API (`external-controller: 127.0.0.1:9090`) → `GET /proxies/PROXY/delay` gives real egress checks, fixing the known "says Connected but no internet" gap that sing-box cannot offer without log parsing.

---

## 8. Options & recommendation

> **All four options below were written for the retired Wails client.** Neither A
> nor B is outstanding for `client/`: **A is inherited** (pkexec → root service,
> see the banner above §1) and **B is settled by construction** (the fork bundles
> mihomo). The options are kept as the reasoning record. The only live remnant is
> **validating** the inherited Linux elevation path — §10's checks 1–3, 5 and 8
> remain a good script for doing that.

- **Option A — Fix Linux TUN elevation (the actual fix, recommended first).** Cheapest robust path: pkexec/polkit the engine spawn on Linux (GUI auth prompt, no helper binary), keep Windows direct mode. Fixes the observed school-network failure regardless of engine. ~0.5–1 day.
- **Option B — Engine swap to mihomo (optional, later).** Rationale is now Windows-side only (WFP avoidance hypothesis) + external-controller egress checks. Does not help Linux BYOD without Track A. ~1.5–2 days.
- **Option C — Both.** Do A now (unblocks the user's device today), validate on the school network, then decide B using Windows school-laptop data.
- **Option D — Do nothing / keep patching sing-box WFP issues on Windows.** Already 10 follow-up fixes; diminishing returns.

**Recommendation (historical): A now, C if Windows data still misbehaves after A.** As it turned out, the fork took neither path — it was rebuilt on Clash Verge Rev, which brought both the Verge service and mihomo with it.

---

## 9. Effort estimate

> **Historical.** All of this was scoped against the retired client and none of it
> was done. The fork paid the cost differently: as the work of building a product
> on top of Verge instead of repairing this client.

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

> **Retargeted 2026-09-26:** written for the retired client, but checks 1–3, 5 and
> 8 are still exactly the right script for the one live question — whether
> `client/`'s **inherited** pkexec → root-service → mihomo-as-root chain actually
> brings up TUN on a non-root Wayland session. Checks 7 and 9 name sing-box
> constructs that no longer apply (the fork's engine is mihomo; the foreign-process
> guard matches `verge-mihomo`).

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
- Code evidence (**retired client only**): `legacy/wails-client/app.go:157` (`SetHelperMode(false)`), `process.go` (`startDirect`, `autoStartHelper` dead path), `process_unix.go`, `v5/legacy/`.
- **Shipping fork's elevation path (read these instead, if that is what you are here for):** `crates/…/clash_verge_service_ipc` `management.rs::elevate()` (pkexec → sudo fallback); `client/src-tauri/src/utils/help.rs::linux_elevator()`; `core/service.rs` (`install_service`, `invoke_service_install`, `stage_approved_core`); `core/runstate/health.rs::tun_capable()`.
- mihomo TUN docs: https://wiki.metacubex.one/en/config/inbound/tun/
- mihomo DNS docs: https://wiki.metacubex.one/en/config/dns/
- mihomo releases: https://github.com/MetaCubeX/mihomo/releases (v1.19.29 latest at analysis time)
- sing-box TUN docs: https://sing-box.sagernet.org/configuration/inbound/tun/
- Our evidence trail: `docs/FIXES.md` (Follow-ups 1–10)

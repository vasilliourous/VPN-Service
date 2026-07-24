# MyVPN — VPN Service

A commercial VPN for students at N4L-managed NZ schools (Macleans College).

---

## V2 — Archived (Do Not Build)

The `simplified/` directory contains the V2 plan. It was built around **Hysteria 2** — a QUIC-based protocol that assumes UDP is available. V2 was designed before we could test against the actual school network.

**Why V2 doesn't work:**

| Assumption | Reality (N4L Network) |
|-----------|----------------------|
| UDP is available | N4L blocks all non-DNS UDP. Hysteria 2's QUIC handshake times out. |
| TLS disguise is sufficient | N4L fingerprints non-browser TLS (JA3) and detects SNI/IP mismatches. TLS proxies get classified and dropped. |
| A bandwidth probe can calibrate Brutal CC | TCP congestion control self-tunes — no probe needed. The entire `internal/probe/` package was Hysteria 2 baggage. |

V2 has been left in the repository for reference, but **should not be built or used as the basis for implementation.** The protocol assumptions are wrong for the target network.

---

## V3 — Shadowsocks Architecture (Replaced by V4)

The `v3/` directory contains the Shadowsocks-based plan. Replaces Hysteria 2 with **Shadowsocks-rust**, which bypasses N4L's Palo Alto firewall because it doesn't use TLS and runs over TCP only.

**V3 was the first working protocol choice** but had architectural issues that were fixed in V4. Key problems discovered during audit:

| Problem | Severity | V4 Fix |
|---------|:--------:|--------|
| Eco & Strike shared port 8443 with different passwords — only one could authenticate | **Critical** | Three separate ports (8443/8444/8445) each with own ssserver instance and password |
| Non-standard `"speed"` field in Shadowsocks JSON — sslocal ignores it, caps don't work | **Critical** | Server `tc` traffic shaping for Eco, Brutal sysctl target rate for Stealth |
| No rollback safety — bad update = dead app | **Critical** | Two-phase sentinel auto-rollback (restored from V1) |
| No offsite backups — VPS failure = total data loss | **Critical** | Hourly Backblaze B2 backups (restored from V1) |
| No heartbeat / grace period — hub downtime kills all connections | **High** | 5-min heartbeat with 7-day grace period |
| No activation rate limiting — brute-force possible | **High** | Caddy + PocketBase hook, 5 attempts per 10 min per IP |
| No Luhn checksum on codes — every typo hits the server | **Medium** | Client + server-side Luhn-mod-N validation |
| tun2socks is abandonware (last updated 2020) | **Medium** | Replaced by sing-box (actively maintained) |
| sslocal + tun2socks = 2 binaries, SOCKS5 hop adds latency | **Low** | Sing-box unifies TUN + Shadowsocks in one process |
| No code recovery on device failure | **Medium** | Admin unbind endpoint |

**V3 is kept for reference but V4 supersedes it.**

---

## V4 — Current Architecture (Build This)

The `v4/` directory contains the production-ready architecture. Sing-box replaces sslocal + tun2socks. The update system, backups, heartbeat, and activation flows are fully hardened for a real business.

### V4 Changes

#### From V3 to V4 — Technical Architecture

| Component | V3 | V4 |
|-----------|:--:|:--:|
| Client tunnel engine | sslocal + tun2socks (2 processes, 1 abandonware) | **sing-box** (1 process, actively maintained) |
| Client-to-server hop | App → sslocal (SOCKS5) → tun2socks (TUN) → server | **App → sing-box (TUN+SS direct) → server** |
| Server instances | 2 (shared Eco+Strike port with conflicting passwords) | **3** (Eco:8443, Stealth:8444, Strike:8445 — each isolated) |
| Bandwidth enforcement | Non-functional `"speed"` JSON field | **Server tc HTB qdisc** (Eco) + **Brutal sysctl target** (Stealth) |
| LD_PRELOAD wrappers | 2 (cubic-wrap + brutal-wrap) | **1** (brutal-wrap only — system default is BBR) |

#### From V3 to V4 — Reliability & Business

| Feature | V3 | V4 | Business Value |
|---------|:--:|:--:|----------------|
| Update rollback safety | ❌ Replace + restart. Crash = dead app. | ✅ **Two-phase sentinel** — auto-revert on crash, manual `--revert` | Customers don't lose VPN on bad updates |
| Offsite backups | ❌ Not mentioned | ✅ **Hourly SQLite backup to Backblaze B2** — plus daily SQL dump | VPS dies → restore in minutes, not start over |
| Heartbeat + grace period | ❌ None | ✅ **5-min heartbeat check, 7-day grace** | Hub downtime doesn't disconnect customers |
| Activation rate limiting | ❌ None | ✅ **Caddy + PocketBase hook** — 5 attempts / 10 min / IP | No brute-force attacks on codes |
| Luhn code checksum | ❌ 12-char, no validation | ✅ **Luhn-mod-N** — client detects typos instantly | Better activation UX, less server load |
| Code recovery on device failure | ❌ Permanent binding | ✅ **Admin unbind endpoint** — reset fingerprint | Customer breaks laptop → keeps subscription |
| Auto-reconnect | ❌ None | ✅ **Built into sing-box** | WiFi blips don't kill connection |
| Force-update capability | ❌ Wait 6h | ✅ **Heartbeat signals update** — app checks immediately | Push urgent security fixes fast |

### V4 Server Architecture

```
Three Shadowsocks instances, one per tier:

┌─────────┬──────┬──────────┬──────────────┬─────┬───────────────┐
│  Tier   │ Port│ Server CC│ Bandwidth     │ UDP │ Enforcement   │
├─────────┼──────┼──────────┼──────────────┼─────┼───────────────┤
│ Eco     │ 8443 │ BBR      │ 5 Mbps       │ ❌  │ tc HTB qdisc  │
│ Stealth │ 8444 │ Brutal   │ 48 Mbps      │ ❌  │ Brutal sysctl │
│ Strike  │ 8445 │ BBR      │ Unlimited    │ ✅  │ None needed   │
└─────────┴──────┴──────────┴──────────────┴─────┴───────────────┘

System default CC = BBR (tcp_bbr module loaded at boot)
```

### V4 Client Engine

```
V3:  sslocal ──SOCKS5──→ tun2socks ────→ TUN device
     ↑ 2 processes, 1 abandoned          ↑
     └─ extra hop, latency                   

V4:  sing-box ────────────────→ TUN device
     ↑ 1 process, actively maintained
     └─ direct, no intermediate hop
```

### V4 Update Flow

```
Bad update scenario (V3):                 Bad update scenario (V4):

1. Download new binary                    1. Save current as .prev
2. SHA256 verify                          2. Write .update-pending sentinel ← Phase 1
3. Replace current binary                 3. Write new binary
4. Restart                                4. Restart
5. New binary crashes on startup          5. New binary starts, renames sentinel ← Phase 2
6. App won't open                         6. Customer uses app ← fine
   → Customer stuck, you SSH              → If crash: .update-pending still exists
                                            → Auto-revert to .prev
                                            → Customer never notices
```

---

## Evolution Summary

```
V1 (originals/)         V2 (simplified/)         V3 (v3/)             V4 (v4/)
─────────────────       ─────────────────       ──────────────       ──────────────
Hysteria 2               Hysteria 2               Shadowsocks-rust     Shadowsocks-rust
sslocal + tun2socks     sslocal + tun2socks       sslocal + tun2socks  sing-box (unified)
Two-phase update         Simplified update         No rollback           Two-phase update
JWT heartbeat + grace   No heartbeat              No heartbeat          5-min heartbeat + grace
Offsite B2 backups      Manual backup only        No backups            Hourly B2 backups
4 tiers                  3 tiers                   3 tiers               3 tiers
Luhn code checksum       No checksum               No checksum           Luhn checksum
Rate limiting            No rate limiting          No rate limiting      Caddy + PB rate limit
Privileged helper        Engine --tun              tun2socks TUN         sing-box TUN

LEGEND:
✔ = Had it             ✘ = Had it, stripped it    ✘ = Never had it     ✔ = Everything fixed
```

---

## Directories

| Directory | Status | Contents |
|-----------|--------|----------|
| `v4/` | **Build this.** | Sing-box client, three-tier server, all reliability features. Production-ready. |
| `v3/` | **Reference only.** | Shadowsocks plan with bugs. V4 is the fixed version. |
| `simplified/` | **Archived.** | V2 Hysteria 2 plan. UDP blocked by N4L — won't work. |
| `originals/` | **Archived.** | V1 and V2 research documents. Pre-testing. |
| `CONTEXT.md` | **Reference.** | Full testing report. Every protocol tested against N4L. |

## Build Order

1. Read `v4/ARCHITECTURE.md` — understand the full system
2. Read `v4/IMPLEMENT.md` — step-by-step build guide
3. Deploy server: blank Ubuntu 22.04 → run setup script → configure B2 backups
4. Build the Go + Fyne client app with sing-box bundled
5. Test activation flow (including Luhn checksum)
6. Test update flow (including forced crash → auto-rollback)
7. Test heartbeat + grace period (kill the hub, verify VPN keeps working)
8. Deploy to production with PocketBase collections and seeded tier configs
9. Print code cards, staple to instructions, hand to middlemen
10. Set up uptime monitoring (UptimeRobot/PingPong) on `https://api.yourdomain.com/api/health`

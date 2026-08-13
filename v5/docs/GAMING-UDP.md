# Gaming UDP — Implementation Plan (Consolidated 2026-08-14)

> **Status:** PLANNED — no changes implemented, no testing possible right now.
> This document consolidates the analysis from the packet-level debugging
> narrative (diag/ toolkit, FIXES.md, CONTEXT.md) into an ordered change plan
> to make the Strike tier's gaming promise actually function on N4L school
> networks. Read `CONTEXT.md` and `FIXES.md` for the full debugging history.

---

## 1. Goal

Make **UDP gaming (SCP:Secret Laboratory etc.) work through the MyVPN tunnel**
on networks where UDP is hostile. The school network's UDP policy is
stateful/DPI-based, observed as: tiny flows pass (48B NTP round-trips),
QUIC is explicitly killed, game-sized flows get dropped or silence.

## 2. Why it fails today (root cause)

The client currently sends game UDP **raw** — standard SS UDP relay
(`process.go` ~line 718: `udp_over_tcp` deliberately NOT configured; Strike
server runs `tcp_and_udp`). Raw UDP from the school network is exactly what
the hostile policy drops.

UDP-over-TCP (UoT) was tried and removed because the **server** rejected it:
`shadowsocks-rust` does not implement SagerNet's proprietary UoT protocol
(magic domains `sp.udp-over-tcp.arpa` / `sp.v2.udp-over-tcp.arpa`) and RSTs
every UoT connection after ~300ms (confirmed in `seed-live.py` notes,
2026-08-01).

**Conclusion:** UoT was never the wrong protocol — it was the wrong server.
The fix is to carry UDP inside the TCP tunnel with a server that implements
UoT. sing-box (the engine already on every client) is that server.

## 3. The change stack (ordered)

### P0 — Transport fix: sing-box server + UDP-over-TCP (the core change)

Replace `shadowsocks-rust` with **sing-box in server mode** on the VPS,
enabling `udp_over_tcp` on the Strike tier both ends.

| End | Change |
|-----|--------|
| VPS | `v5/server/modules/02-shadowsocks.sh`: install sing-box (align version with client: 1.12.1) instead of `ssserver`; write per-tier shadowsocks **inbound** configs (Eco/Stealth `network: tcp` as today, Strike `network: tcp_and_udp` + `"udp_over_tcp": true`); keep systemd units per tier, password file, BBR + tc (04-tc.sh untouched) |
| Client | `v5/client/internal/manager/process.go` (~line 718): set `"udp_over_tcp": true` on the shadowsocks outbound when the tier advertises UDP (Strike) |
| Deployment | No new open ports: UoT rides the existing TCP 8445 connection; server still listens UDP 8445 for any raw fallback |

**Why this works:** the school firewall only ever sees TCP 8445 (identical to
the working TCP traffic). UDP is encapsulated inside it, so the hostile UDP
policy is bypassed entirely. MTU/fragmentation issues (the sizeladder
threshold) also disappear — TCP segments.

**Why sing-box server:** same engine as the client, one binary to deploy,
verified version (1.12.1), native UoT on both sides, same config dialect.
Alternatives (Xray SS with `uot`, custom UDP-in-TCP relay) are worse fits.

**Rollback:** keep the old `ssserver` binary + systemd units (`.bak` copies);
one-command revert in the module.

### P1 — Validation experiments (deferred until testing is possible)

These close the remaining diagnostic gap and MUST run before/with the P0
deploy:

1. **VPS-direct real-client test** — from the VPS (bypassing the school
   network entirely), replay a **byte-exact Wireshark-captured LiteNetLib
   ConnectRequest** (magic `SCPSL14` + protocol id + session key + connect
   data) against the 24 SCP:SL servers, or run an actual SCP:SL client.
   - Reply → school leg is the problem → P0 confirmed correct.
   - Silence → server list stale / whitelist-only / anti-DDoS blocks the VPS
     IP → pick different servers instead of changing transport.
2. **Packet-rate ladder** — current ladders test size only; add a rate test
   (1–100 pps through the tunnel) — a stateful policy often drops by flow
   rate, not size.
3. **Server aliveness** — cross-check the 24 IPs against the SCP:SL server
   registry; dead servers are indistinguishable from dropped packets
   client-side.
4. **Post-deploy end-to-end** — actual SCP:SL session through the UoT path;
   confirm UDP byte counters move both ways in `diag/watch.sh` (game ports
   7777/27018 flows currently show sent-bytes-with-no-return).

### P2 — Raw-UDP mitigations (only if P0 deployment is delayed)

If UoT cannot be deployed yet, reduce raw-UDP fragility:

- TUN **MTU 1500 → 1280** in the client config (fragmented UDP is a classic
  DPI drop; SS AEAD-UDP adds ~32B/packet so anything near 1472 already
  fragments)
- Keep `strict_route: false` and the DNS chain exactly as-is (the 7-layer
  #4–#11 fix stack — do NOT regress)
- Optional ~15s UDP keepalive if school NAT expires UDP mappings

These are stopgaps, not the fix.

### P3 — Product hardening (after P0 works)

1. **`UDPRelay` heartbeat flag** (`heartbeat.go`/`heartbeat.pb.js`, currently
   informational-only per `process.go:182`) → drive UoT enablement per tier
   server-side.
2. **Game-mode route rule** — pin known game-server IPs to the UoT-capable
   outbound; everything else stays plain TCP. Optionally serve a game-server
   list via heartbeat `server_config`.
3. **Tier accuracy** — update tier tables/docs: Strike = "TCP+UDP
   (UDP-over-TCP through sing-box server)".
4. **Silent-client enforcement** (orthogonal): server-side suspend still
   depends on client heartbeat — document the limitation; consider server-side
   config kill for suspended codes. Separate issue.

## 4. File-change map (checklist for implementation)

| File | Change |
|------|--------|
| `v5/server/modules/02-shadowsocks.sh` | Install sing-box server; per-tier inbound configs; UoT on Strike; rollback path |
| `v5/server/setup.sh` | Module chain unchanged; summary line "Strike: TCP+UDP (UoT)" |
| `v5/server/scripts/smoke-test.sh` | Verify Strike serves tcp+udp; UoT handshake check |
| `v5/server/scripts/seed-live.py` | Notes/config for sing-box server |
| `v5/server/templates/` | Optional sing-box server config template |
| `v5/client/internal/manager/process.go` | `udp_over_tcp: true` on outbound (Strike); MTU change (P2) |
| `v5/client/internal/heartbeat/heartbeat.go` + `v5/server/pb_hooks/heartbeat.pb.js` | UDPRelay semantics (P3) |
| `v5/docs/DEPLOY.md`, `OPS.md`, `ARCHITECTURE.md`, `CONTEXT.md`, tier tables | Reflect sing-box server + UoT |
| `v5/docs/FIXES.md` | Dated entry when implemented |

## 5. Risks / decision points

- **UoT latency:** TCP head-of-line blocking can add latency for UDP games
  under packet loss. Acceptable for school WiFi; note that a future "raw
  where allowed, UoT fallback" mode is not auto-detectable client-side —
  product targets N4L schools, so **default Strike UDP = UoT always**.
- **Server swap:** shadowsocks-rust → sing-box must not break the working
  TCP path (Eco/Stealth). Keep them plain shadowsocks TCP — only Strike
  gains UoT.
- **Migration:** already-deployed VPS needs the module re-run; the module is
  idempotent by design (FIXES.md R-series).

## 6. Sequence when testing resumes

1. P1.3 aliveness + P1.1 VPS-direct test (closes the school-leg vs server-leg
   gap — validates the whole premise)
2. P0 server swap on a **test port** (not 8445) + client UoT on a test build
3. P1.4 end-to-end with an actual SCP:SL session
4. Promote to 8445, update smoke test + docs
5. P3 hardening items

---

*Consolidated 2026-08-14 from the debugging narrative (diag/ toolkit,
FIXES.md, CONTEXT.md, seed-live.py notes, process.go config). No code changes
made as part of this plan.*

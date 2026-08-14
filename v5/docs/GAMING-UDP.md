# Gaming UDP — Implementation Plan (Consolidated 2026-08-14)

> **Status:** DEPLOYED (2026-08-14) — server UoT endpoint LIVE on the
> production VPS (134.199.155.166, port 8446) and client UoT support shipped.
> P1 validation (real game session) is still PENDING — see §P1.
> **P1 throughput baselines (2026-08-14, via the Stealth TCP path — clash
> config `clash-verge-stealth.yaml`):** school network 25.3 down / 113.6 up
> @ 51ms (single-flow: 2.47 down — school shapes downloads PER-FLOW ~2.5
> Mbps, aggregate scales with parallel connections); mobile (Optus) 81.7
> down / 9.5 up @ 121-150ms (mobile caps uploads); tunnel capacity from a
> non-school link ~45-80 Mbps. Conclusion: the tunnel/VPS is not the
> bottleneck — last-mile network shaping is. Per-flow download shaping is
> worked around with multi-connection downloaders; a per-IP total cap would
> need a second egress IP.
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

### P0 — Transport fix: UDP-over-TCP on a server that implements it (the core change)

✅ **DEPLOYED to production — 2026-08-14** (VPS 134.199.155.166). What
landed and is live:

| Piece | Where | Detail |
|-------|-------|--------|
| Server UoT endpoint | `v5/server/modules/02-shadowsocks.sh` | Gated `ENABLE_UOT=1` section: installs sing-box server (v1.12.1), shadowsocks inbound on `UOT_PORT` (default 8446) with Strike creds, systemd unit `sing-box-uot.service`. **Config corrections from live deploy:** sing-box REJECTS `"network": "tcp_and_udp"` and rejects `"udp_over_tcp"` on the INBOUND — both omitted (default = tcp+udp; UoT magic-domain connections handled automatically by the inbound). Verified listening on TCP+UDP 8446 |
| Advertising | `v5/server/scripts/seed-live.py`, `seed-pb.py` | With `ENABLE_UOT=1`, strike's tier_configs config gains `"uot_port"` + `udp_relay=true`. **Live:** strike tier_configs = `{"server":"networkingguides.duckdns.org","server_port":8445,"uot_port":8446,...}` |
| Client UoT outbound | `v5/client/internal/manager/process.go` | When `UDPRelay && ServerPortUOT > 0`: adds a `proxy-uot` shadowsocks outbound (`udp_over_tcp: true`, port = uot_port) + a `network: udp` route rule pinning UDP to it. TCP stays on the standard port/outbound — the working path never changes |
| Plumbing | `heartbeat.go`, `activation.go`, `storage.go`, `app.go` | `server_port_uot` parsed from server config in all three paths (activation, heartbeat refresh, persisted state) and applied to the manager Config |
| Verified live | activation + heartbeat | `POST /api/activate` → 200 with `server_config.uot_port:8446, udp_relay:true`; `POST /api/heartbeat` → 200 with the same config refresh |

**Deploy commands (repeatable):**
1. DNS for the domain must resolve to the VPS FIRST (00-env hard-fails)
2. `scp -r v5/server age-key.txt root@VPS:/root/server/`
3. `ENABLE_UOT=1 DOMAIN=… /root/server/setup.sh` (idempotent)
4. `ENABLE_UOT=1 DOMAIN=… python3 /root/server/scripts/seed-pb.py` (re-seed after any setup re-run)
5. Existing Strike clients pick up `uot_port` on their next heartbeat (no re-activation needed)
6. Disable: `systemctl disable --now sing-box-uot` + drop `uot_port` from the tier config

The original design rationale (additive shape, why it works, rollback) follows:

| End | Change |
|-----|--------|
| VPS | `v5/server/modules/02-shadowsocks.sh`: add an optional module section that installs sing-box server (version aligned with client 1.12.1) and writes a Strike-only shadowsocks **inbound** (`network: tcp_and_udp`, `"udp_over_tcp": true`) on a NEW port (e.g. 8446); keep existing ssserver units untouched; keep password file, BBR + tc (04-tc.sh untouched) |
| Client | `v5/client/internal/manager/process.go` (~line 718): set `"udp_over_tcp": true` on the shadowsocks outbound when the tier advertises UDP (Strike) and a UoT-capable server port is configured |
| Deployment | One new TCP port (e.g. 8446); school firewall sees plain Shadowsocks TCP wire format, identical to existing tiers. Server may also listen UDP 8446 for raw fallback |

**Why this works:** the school firewall only ever sees TCP (identical wire
format to the working traffic). UDP is encapsulated inside it, so the hostile
UDP policy is bypassed entirely. MTU/fragmentation issues (the sizeladder
threshold) also disappear — TCP segments.

**Rollback:** disable the new systemd unit; delete the new port from the
firewall module. Nothing else changes.

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

### 5a. Server choice — tradeoffs (OPEN DECISION, not settled)

sing-box server is the leading candidate but is NOT the only one. The deciding
factor is interoperability with the client's UoT framing and server-side
operating properties. Options:

| Option | Pros | Cons |
|--------|------|------|
| **sing-box server** (additive instance) | Same engine as client — guaranteed UoT framing match; one binary family to learn; verified version 1.12.1 | UoT is a **proprietary SagerNet protocol** (magic domains `sp.udp-over-tcp.arpa`); bigger attack surface than ssserver (full proxy platform, not a minimal relay); client↔server version coupling (engine upgrades must track); server mode is less battle-tested than shadowsocks-rust; heavier RAM/CPU on a 2GB VPS; **non-sing-box clients (Hiddify, Clash) lose UDP entirely** |
| **Xray (v2ray-core) SS inbound + `uot`** | Server-grade maturity; implements SagerNet UoT framing; keeps a dedicated server tool | Interop with sing-box client UoT is **reported but unverified** — needs a 5-min test; larger binary/feature set than needed; still depends on the proprietary UoT framing |
| **shadowsocks-rust + udp2raw** | Keeps the standard, working server untouched; raw UDP wrapped in fake-TCP; battle-tested for gaming | Second tunnel layer + extra process per tier; fake-TCP may be classified by DPI (unknown); single-maintainer dependency; latency overhead |
| **Raw UDP + P2 stopgaps only** | Zero change, zero risk; UDP works wherever the network allows it | Gaming UDP stays broken on N4L — the Strike promise is unfulfilled; this is the honest fallback if all transport options fail validation |

**The key open question (needs a 5-minute test, can't run now):** does
sing-box client UoT interoperate with Xray's SS `uot`? If yes, Xray is a
serious alternative to sing-box server. If no, sing-box server is the only
native option.

### 5b. Other risks / decision points

- **Proprietary protocol lock-in:** UoT is SagerNet-specific. Choosing it
  (with any server) ties the UDP path to a closed framing — if it changes or
  breaks, both ends must track it. This is the same class of dependency risk
  that engine churn already caused twice (1.10→1.12).
- **Third-party client loss:** with UoT on the server, Hiddify/Clash testing
  flows (used in the diag toolkit and `hiddify.pb.js` ss:// links) lose UDP.
  Acceptable for the shipped product (MyVPN client is sing-box) but kills a
  useful test path.
- **UoT latency:** TCP head-of-line blocking can add latency for UDP games
  under packet loss. Acceptable for school WiFi; product targets N4L schools,
  so **default Strike UDP = UoT always**; a per-network toggle is a possible
  future setting.
- **Migration:** the additive shape needs no migration of the working path;
  a full swap would. The module is idempotent by design (FIXES.md R-series).

## 6. Sequence when testing resumes

1. P1.3 aliveness + P1.1 VPS-direct test (closes the school-leg vs server-leg
   gap — validates the whole premise)
2. P0 on a **test port** (e.g. 8446, additive instance) + client UoT on a test build
3. P1.4 end-to-end with an actual SCP:SL session
4. Promote to the production Strike port, update smoke test + docs
5. P3 hardening items

---

*Consolidated 2026-08-14 from the debugging narrative (diag/ toolkit,
FIXES.md, CONTEXT.md, seed-live.py notes, process.go config). No code changes
made as part of this plan.*

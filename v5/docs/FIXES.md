---

## TIERS PAGE — STALE WARNING CONTRADICTED THE STRIKE DESIGN (2026-09-19)

### 27. The Web UI told operators to switch off the feature Strike exists for

The Tiers page carried this for every tier:

> **UDP relay** must stay off for this server: shadowsocks-rust does not
> implement sing-box's UDP-over-TCP, and leaving it on makes UDP traffic fail.

It was written 2026-08-01 and was **correct at the time**. The layout then was:
`udp_relay` unconditionally put `udp_over_tcp: true` on the client's shadowsocks
outbound, aimed at the ordinary shadowsocks-rust port. shadowsocks-rust does not
implement SagerNet's UoT protocol (magic domains `sp.udp-over-tcp.arpa`), so
every UoT dial was RST about 300ms later and UDP broke — FIXES.md Follow-up 9.

Two things changed on 2026-08-14 and the warning was never revisited:

1. **`udp_relay` no longer acts alone.** The client's condition is
   `uotEnabled := cfg.UDPRelay && cfg.ServerPortUOT > 0` — an AND. With no
   `uot_port` in the tier config, setting the flag is a no-op, so the old
   failure mode ("turning this on breaks UDP") became unreachable.
2. **The server changed.** `enable-uot.sh` installs a **sing-box** listener on
   port 8446, which *does* implement UoT. That was the whole conclusion of
   `GAMING-UDP.md`: "UoT was never the wrong protocol — it was the wrong server."

So the two fields now mean different things — `udp_relay` *declares* the tier
carries UDP; `uot_port` names the *mechanism*. The warning told operators to
disable the declaration that makes Strike's gaming feature work, and it would
have become actively harmful the moment someone enabled UoT properly.

### 28. `uot_port` was invisible and uneditable in the Web UI

`tiers.list` returned only `server`, `server_port` and `method` from the tier's
config JSON — `uot_port` was parsed over but never surfaced, and `tiers.update`
had no way to set it. An operator could therefore toggle the half of the switch
they could see while having no sight of, or control over, the half that decides
whether it does anything.

**Fixes:** `tiers.list` now returns `uot_port`; `tiers.update` accepts it
(0–65535, `0`/absent removes it) and is optional so an older console build keeps
working. The Tiers page gained a `UoT port (0 = none)` field and two
disagreement warnings — flag on without a port (a confusing no-op), and a port
set without the flag (an endpoint nobody is told about).

The replacement text states the real constraint: both switches are required, the
port must point at a sing-box UoT listener (not 8443/8444/8445), and on this
deployment no tier should set one because the UoT endpoint is not running.

### Verified live

- Web UI redeployed — asset `index-BC96z_48.js`.
- Hook deployed to `/opt/pocketbase/pb_hooks/` **and** the `/root/server/`
  staging copy; PocketBase restarted; previous hook backed up to
  `/root/admin_console.pb.js.bak.*`.
- `tiers.list` now returns `uot_port` for all three tiers, and the values are
  `0` for eco, stealth **and strike** — i.e. the live hub genuinely has no UoT
  endpoint, so the corrected warning describes the real state rather than
  guessing.

### Still open

Strike's gaming premise is **not currently realised**: the UoT endpoint was
deployed to the retired `134.199.155.166`, `enable-uot.sh` has never run on the
current host `170.64.196.179`, and no real game session has been validated
(`GAMING-UDP.md` lists that as the outstanding acceptance gate). The client code
path is correct and waiting; only the server side is missing.

---
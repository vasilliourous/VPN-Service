# Historical Research & Plans (Archive)

> **Purpose:** Preserved pre-V5 research and planning documents that are no
> longer the live architecture but still contain context future agents may
> need (business model, threat research, N4L environment analysis).
>
> **These are HISTORICAL.** Read `../CONTEXT.md` and `../ARCHITECTURE.md` for
> the current, authoritative picture. Files here describe the abandoned
> V2/V3 direction (Hysteria 2 / QUIC / usque-Warp) and early N4L research —
> the final architecture is **Shadowsocks TCP + BBR + tc caps** (see
> `../ARCHITECTURE.md`, `../DEPLOY.md`).

## Files

| File | Lines | What it is | Superseded by |
|------|------:|------------|---------------|
| `Business-Plan.md` | 207 | Real business model: middlemen, cash payments, tiers, market vs xVPN | `../CONTEXT.md` §1 (tier table) — but this is the full strategy |
| `Architectural-Plan.md` | 811 | Original v3 build plan: Hysteria 2, 4 tiers (Warp Lite → Gaming Max), privileged helper | `../ARCHITECTURE.md` (final Shadowsocks design) |
| `attacker-perspective.md` | 1840 | Adversarial/insider-threat analysis of covert telemetry & tracking | `../CONTEXT.md` §7, `../FIXES.md` (hardening decisions) |
| `defender-perspective.md` | 2079 | Defense-in-depth research: protecting users from the insider threat above | `../CONTEXT.md` §7 |
| `comprehensive-vpn-blocklist.md` | 492 | N4L/Palo Alto blocklist research (VPN detection fingerprints) | `../CONTEXT.md` §1 (protocol choice rationale) |
| `school-vpn-blocking-implementation.md` | 716 | How N4L implements VPN blocking at the network level | `../CONTEXT.md` §1, §7 |

## Not preserved here (recoverable from git history)

The rest of the 2026-08 cleanup deletions are NOT archived in-repo, but every
file is one command away:

```bash
# e.g. restore the full 4,771-line action plan
git show 72eec8a^:originals/Actionable-Plan.md > Actionable-Plan.md

# other removed material:
#   originals/V2-ACTION-PLAN.md, originals/V2-BOTTLENECK-REFERENCE.md
#   v3/  (alternative doc set: ARCHITECTURE/BUSINESS/DEPLOY/IMPLEMENT/OPS/UI-AESTHETICS)
#   simplified/  (Hysteria 2 alternative doc set)
#   v5/legacy/   (old Fyne GUI + helper)
#   modular-vps/ (early server modules; superseded by v5/server/)
#   root CONTEXT.md, v5/scripts/ (duplicates)
```

**Naming note:** files are stored with their original names (e.g.
`attacker-perspective.md`) — lowercase, no `HISTORY-` prefix — so git history
and any old references stay consistent.

**Missing links inside these files are expected:** several reference docs that
were never in this repo (e.g. `BUSINESS-OVERVIEW.md`, `ACTION-PLAN.md`,
`PLAN.md`, `QUICKSTART.md`, `threat-model-*.md`) — they lived in the private
`VPN-Service/` research directory. Treat those as "see private research",
not as repo breakage.

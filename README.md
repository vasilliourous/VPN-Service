# MyVPN — Two Plans

Two sets of plans live in this repo. Here's what each is for.

---

## `originals/` — V1 & V2 Research Archive

The original planning documents from before this project was simplified. Includes:

- **V1:** Architectural-Plan.md, Actionable-Plan.md — over-engineered, 6-state machines, helper services, EWMA monitors, 5,000+ lines of planning.
- **V2:** V2-ACTION-PLAN.md, V2-BOTTLENECK-REFERENCE.md — stripped down but still had 4 tiers, heartbeat, auto-updater sentinel, adaptive monitoring.
- **Research:** attacker-perspective.md, defender-perspective.md, school-vpn-blocking-implementation.md, comprehensive-vpn-blocklist.md — threat models and competitive intelligence.
- **Business:** Business-Plan.md — original business model docs.

**Status:** Archive. Read for reference, but don't build from these. The simplified plan replaces them.

---

## `simplified/` — Build This One

The re-engineered plan for 10–20 users at one school. Designed to be built in a weekend.

| File | What It Is |
|------|-----------|
| **README.md** | Quick overview, philosophy, 3 tiers, key numbers |
| **BUSINESS.md** | Business model, pricing, decoy effect, middlemen ops |
| **ARCHITECTURE.md** | Full system design + UI aesthetics plan |
| **IMPLEMENT.md** | Step-by-step build guide for a developer |
| **OPS.md** | Agent operations manual — API calls, SSH commands, update workflow |
| **DEPLOY.md** | Server setup and daily operations |
| **UI-AESTHETICS.md** | Visual design spec (do after backend works) |

### Key Simplifications vs V2

- 3 tiers instead of 4 (Eco / Stealth / Strike)
- No heartbeat, no telemetry, no grace period
- No hardware sandboxing, process disguise, cert pinning
- Single Hysteria 2 server with auth pools (not separate instances)
- Background systray app with auto-updater
- Fingerprinting kept as educational infrastructure
- Bandwidth probe kept for Strike tier only

---

## Build Order

```
1. Read simplified/IMPLEMENT.md
2. Set up the VPS (scripts/setup_vps.sh)
3. Build the client app (Go + Fyne)
4. Test activation + connection flow
5. Apply UI polish (UI-AESTHETICS.md)
6. Generate codes, print cards, hand to middlemen
```

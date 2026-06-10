# VPN Monetised Service — High Availability (Low-Cost, VPS-Only)

> Budget-constrained HA for a single-VPS architecture. No home server required.

---

## Current Single Points of Failure

| Component | Failure Impact | Recovery Time (manual) |
|-----------|---------------|------------------------|
| VPS | All clients lose service | Provision new VPS, restore backup, update DNS (~30-60 min) |
| VPS IP blocked by school | All clients lose service | Same as above |
| VPS provider outage | All clients lose service | Wait for provider or migrate |
| Protocol blocked (UDP) | Hysteria2/TUIC/AmneziaWG clients affected | Auto-fallback to VLESS-REALITY TCP (already built in) |

---

## Layer 1: Protocol-Level Fallback (Already Implemented — $0)

```
Hysteria2 (QUIC/UDP) ──→ Primary gaming path
        │
        ▼ (UDP blocked or throttled)
VLESS-REALITY (TCP/TLS) ──→ Fallback path, auto-selected by client
```

This is already configured. The VPN client tries Hysteria2 first and swings to VLESS-REALITY automatically when QUIC/UDP is blocked. No infrastructure changes needed.

**Survives:** UDP throttling, QUIC fingerprinting, protocol-specific blocks.

---

## Layer 2: Second VPS at a Different Provider (~$3.50/mo)

This is the biggest bang-for-buck. Instead of one $6-10 VPS, split the budget across two cheaper VPSes at different providers. Both run identical configs and NAT directly to the internet — no WireGuard tunnels needed.

```
                     ┌─→ VPS-A (Provider 1, ~$3.50/mo)
                     │     ├─ Hysteria2 :443/udp
                     │     ├─ VLESS-REALITY :443/tcp
                     │     ├─ TUIC V5 :8443/udp
                     │     ├─ AmneziaWG :9443/udp
                     │     └─ iptables MASQUERADE → Internet
Student ──→ DNS ────┤
                     │
                     └─→ VPS-B (Provider 2, ~$3.50/mo)
                           ├─ Hysteria2 :443/udp
                           ├─ VLESS-REALITY :443/tcp
                           ├─ TUIC V5 :8443/udp
                           ├─ AmneziaWG :9443/udp
                           └─ iptables MASQUERADE → Internet
```

### Provider Options

| Provider | Plan | Cost | vCPU | RAM | Traffic | Notes |
|----------|------|------|------|-----|---------|-------|
| **Oracle Cloud Free Tier** | ARM Ampere | $0 | 4 | 24GB | 10TB | Actually free forever. ARM builds available for all protocols |
| **Hetzner** | CX22 | €3.99 | 2 | 4GB | 20TB | Best performance/price in Europe |
| **BuyVM** | Slice 1024 | $3.50 | 1 | 1GB | Unmetered | Good for North America |
| **RackNerd** | Black Friday | ~$2-3 | 1-2 | 1-2GB | 3-8TB | Budget US option |
| **Netcup** | RS 1000 | ~€4 | 4 | 8GB | Unmetered | German provider, great specs |

### Setup

Both VPSes run the exact same implementation guide. The only differences:
- Different IPs
- Different provider accounts
- Each has its own activation DB (or you sync them)

The activation API can run on just one VPS (the "primary") or on both with a shared DB — simplest is just one VPS runs the API and panel, the other is protocol-only.

### DNS Distribution

**Option A: Health-Checked Load Balancer (Cloudflare Free Tier)**

Cloudflare free tier includes load balancing for up to 2 origins with health checks.

1. In Cloudflare dashboard → Traffic → Load Balancing
2. Create origin pool: VPS-A on TCP/443, VPS-B on TCP/443
3. Health check: TCP handshake every 60s, fail after 3 failures
4. Create load balancer with this pool
5. Point your domain's A record at the load balancer

If VPS-A dies, Cloudflare auto-routes all traffic to VPS-B in ~2-3 minutes.

**Option B: Round-Robin DNS (Simplest, No Failover)**

```
Type  Name   Content        Proxy
A     @      <VPS-A-IP>     ✅
A     @      <VPS-B-IP>     ✅
```

Cloudflare rotates between them. If one dies, ~50% of clients get the dead one until TTL expires. Not ideal but costs nothing extra.

### What This Gives You

| Failure | Recovery | Downtime |
|---------|----------|----------|
| VPS-A dies | Cloudflare LB → VPS-B | ~2-3 min |
| VPS-B dies | Cloudflare LB → VPS-A | ~2-3 min |
| Provider A outage | All traffic shifts to Provider B | ~2-3 min |
| VPS-A IP blocked | Only VPS-A affected; LB routes away | ~2-3 min |
| Both VPSes die | Nothing automatic — you're down | Manual |

**Total monthly cost: ~$7** (two cheap VPSes) instead of ~$6 (one mid-range VPS).

---

## Layer 3: Cloudflare Tunnel Emergency Fallback ($0)

If both VPSes go down, a Cloudflare Tunnel (`cloudflared`) running on any machine you control (old laptop, Raspberry Pi, even the home server if you kept one for management) provides an emergency fallback path.

```
Student ──→ Cloudflare DNS (LB, both origins down)
                │
                ▼ (auto-failover to fallback pool)
           Cloudflare Tunnel ($0)
                │
                ▼
           Any machine running cloudflared
                └─→ VLESS-REALITY over WebSocket (emergency mode)
```

### Setup

```bash
# On any always-on machine (home server, old laptop, Raspberry Pi)
wget https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
chmod +x cloudflared-linux-amd64
mv cloudflared-linux-amd64 /usr/local/bin/cloudflared

cloudflared tunnel login
cloudflared tunnel create vpn-fallback
cloudflared tunnel route dns vpn-fallback fallback.yourdomain.com

# Run a minimal Xray on this machine for VLESS+WS fallback
# cloudflared proxies HTTP to it on localhost

cat > /root/.cloudflared/config.yml << EOF
tunnel: <TUNNEL_UUID>
credentials-file: /root/.cloudflared/<TUNNEL_UUID>.json
ingress:
  - hostname: fallback.yourdomain.com
    service: http://localhost:8443
  - service: http_status:404
EOF

cloudflared service install
```

Add this as a fallback origin pool in Cloudflare Load Balancing, with lowest priority. It only activates when both VPSes are down.

**Cost: $0.** The fallback is TCP-only (VLESS+WS), slower, and relies on whatever machine you have running. But it keeps clients connected during a crisis.

---

## Layer 4: Emergency VPS Swap Script ($0)

When a VPS dies and you need to migrate to a new one:

```bash
#!/bin/bash
# Run on any machine with Cloudflare API access
# /usr/local/bin/vps-emergency-swap.sh

NEW_VPS_IP="$1"
if [ -z "$NEW_VPS_IP" ]; then
    echo "Usage: $0 <new-vps-ip>"
    exit 1
fi

CF_TOKEN="YOUR_CLOUDFLARE_API_TOKEN"
CF_ZONE_ID="YOUR_ZONE_ID"
DOMAIN="yourdomain.com"

# Get the A record ID
RECORD_ID=$(curl -s -X GET \
    "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?type=A&name=${DOMAIN}" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" | jq -r '.result[0].id')

# Update A record
curl -s -X PUT \
    "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${RECORD_ID}" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" \
    --data "{\"type\":\"A\",\"name\":\"@\",\"content\":\"${NEW_VPS_IP}\",\"proxied\":true}"

# Also update panel and api subdomains
for sub in panel api; do
    SUB_ID=$(curl -s -X GET \
        "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?type=A&name=${sub}.${DOMAIN}" \
        -H "Authorization: Bearer ${CF_TOKEN}" \
        -H "Content-Type: application/json" | jq -r '.result[0].id')
    curl -s -X PUT \
        "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${SUB_ID}" \
        -H "Authorization: Bearer ${CF_TOKEN}" \
        -H "Content-Type: application/json" \
        --data "{\"type\":\"A\",\"name\":\"${sub}\",\"content\":\"${NEW_VPS_IP}\",\"proxied\":true}"
done

echo "All DNS records updated to ${NEW_VPS_IP}. Propagation: 1-2 minutes."
```

**Cost: $0.** Combined with the backup script, you can be back online on a fresh VPS in ~10 minutes.

---

## Complete Resilience Matrix

| Failure Scenario | Recovery Mechanism | Est. Downtime | Cost Impact |
|-----------------|-------------------|---------------|-------------|
| Single VPS dies | Cloudflare LB → other VPS | 2-3 min | $0 |
| Both VPSes die | Cloudflare Tunnel → fallback machine | 2-3 min | $0 |
| UDP throttled at school | Auto-fallback to VLESS-REALITY TCP | ~5 sec | $0 |
| VPS IP blocked by school | Cloudflare LB routes to other VPS | 2-3 min | $0 |
| VPS provider terminated account | Migrate to other VPS (if one remains) or emergency swap | 2-10 min | $0 |

---

## Deployment Order (If Budget-Constrained)

| Priority | Layer | Cost | When to Implement |
|----------|-------|------|-------------------|
| **P0** | Protocol fallback (Layer 1) | $0 | Already done |
| **P1** | Second VPS (Layer 2) | ~$3.50/mo | When you have paying clients who expect uptime |
| **P2** | Cloudflare Tunnel fallback (Layer 3) | $0 | After second VPS, as safety net |
| **P3** | Emergency swap script (Layer 4) | $0 | Day one — takes 5 minutes to set up |

---

## Summary

For **~$7/month total** (two cheap VPSes at different providers) plus three zero-cost additions, you go from a single-VPS-takes-everything-down architecture to one that survives:

- Any single VPS failure (automatic, 2-3 min)
- Any single provider outage (automatic, 2-3 min)
- VPS IP block (automatic via LB, 2-3 min)
- Protocol-level blocks (automatic, ~5 sec, already built in)

The only scenario with no automatic recovery is both VPSes failing simultaneously at different providers — which is extraordinarily unlikely. The Cloudflare Tunnel fallback covers even that edge case.

---

## Beyond Two VPSes — The Orchestrator Model

When two VPSes aren't enough (you have 50+ paying clients and need to scale to 3, 5, or 10 VPSes), the two-VPS HA model hits its limit. You can't round-robin DNS across 10 IPs without losing clients every time one dies. You need a placement service.

The **[04-SCALE-OUT-ORCHESTRATION.md](04-SCALE-OUT-ORCHESTRATION.md)** document describes the full orchestrator model:

- **Placement service, not a load balancer** — the orchestrator assigns each new client to the least-loaded worker once (at activation), then gets out of the data path
- **Auto-migration** — when a worker dies, the client app detects it and automatically calls the migration endpoint to get a new config pointing to a live worker
- **Day-2 operations** — adding a worker is one line in a text file. Removing a worker is commenting out one line. No restarts, no migrations needed for existing clients
- **Orchestrator SPOF mitigation** — hot standby, cold standby (promote any worker), and frequent DB backups

This is the natural evolution: single VPS → two-VPS HA → orchestrator with N workers. The base implementation guide builds a single VPS. The two-VPS HA doc adds resilience. The orchestrator doc adds horizontal scale.

#!/bin/bash
# Full path-MTU map + UoT/SS transport probe against the LIVE hub and the
# candidate test box. Read-only except for /root/.ssh key provisioning, which is
# idempotent. Run via sshp.py --file.
set +e

KB64="$(cat <<'K'
c3NoLWVkMjU1MTkgQUFBQUMzTnphQzFsWkRJMU5URTVBQUFBSU1rcVUxWGJIWmh3SlF4dHdEZC90UE5VM2l2MWM3eFk3M21pVE12SGM5a2sgd2hhbGUtc2FuZGJveAo=
K
)"
mkdir -p /root/.ssh && chmod 700 /root/.ssh
if ! grep -qF "whale-sandbox" /root/.ssh/authorized_keys 2>/dev/null; then
  echo "$KB64" | base64 -d >> /root/.ssh/authorized_keys
  echo "[setup] added whale-sandbox key to authorized_keys"
else
  echo "[setup] whale-sandbox key already present"
fi
chmod 600 /root/.ssh/authorized_keys

echo "=== SSH reachability matrix (from candidate box) ==="
for ip in 170.64.196.179 114.23.136.183 114.23.136.1; do
  for port in 22 443 8443 8445 8446; do
    if timeout 6 bash -c "exec 3<>/dev/tcp/$ip/$port" 2>/dev/null; then
      echo "  $ip:$port OPEN"
    else
      echo "  $ip:$port --"
    fi
  done
done

echo "=== PATH MTU map (DF sweep, find the ceiling per path) ==="
sweep() {
  local dst="$1" lo="$2" hi="$3"
  local best=0
  for ((s=lo; s<=hi; s+=4)); do
    if ping -M do -s "$s" -c1 -W2 "$dst" >/dev/null 2>&1; then best=$s; else break; fi
  done
  echo "  $dst -> max DF payload $best (path MTU $((best+28)))"
}
sweep 114.23.136.1  1300 1500
sweep 114.23.116.1  1300 1500
sweep 1.1.1.1       1300 1500
sweep 8.8.8.8       1300 1500

echo "=== Path MTU to the LIVE hub (170.64.196.179) ==="
sweep 170.64.196.179 1300 1500

echo "=== where the tunnel endpoints differ: TCP MSS advertised by hub:443 ==="
command -v tcpdump >/dev/null && echo "  tcpdump present" || echo "  (no tcpdump)"

echo "=== UoT / SS transport handshake probe ==="
echo "  --- TCP connect + TLS SNI to live hub"
timeout 15 curl -sv -o /dev/null --max-time 12 https://170.64.196.179/api/health 2>&1 | grep -E "Connected to|SSL connection|subject:|HTTP/" | sed 's/^/     /'
echo "  --- live hub via its DNS name"
timeout 15 curl -s -o /dev/null -w "     networkingguides.duckdns.org/api/health -> %{http_code} (%{time_total}s)\n" --max-time 12 https://networkingguides.duckdns.org/api/health

echo "=== UDP-over-TCP reachability to live hub :8446 ==="
timeout 8 bash -c "exec 3<>/dev/tcp/170.64.196.179/8446 && echo '  :8446 TCP OPEN'" 2>/dev/null || echo "  :8446 unreachable"

echo "=== raw UDP egress to live hub :8445 (DNS-shaped payload) ==="
python3 - <<'PY'
import socket
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.settimeout(5)
try:
    s.sendto(b'\x00'*64,('170.64.196.179',8445)); print('  UDP send to hub:8445 OK')
except Exception as e: print('  UDP send failed:', e)
PY

echo "=== timing: school-laptop analogue (NZ residential -> endpoints) ==="
echo "  (candidate box is on a residential NZ line; RTT to hub below)"
timeout 10 ping -c4 -q 170.64.196.179 2>/dev/null | tail -1 | sed 's/^/     /'
timeout 10 ping -c4 -q 1.1.1.1 2>/dev/null | tail -1 | sed 's/^/     /'

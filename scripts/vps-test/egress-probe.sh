#!/bin/bash
# Egress profile from the candidate VPS (read-only). Run via sshp.py --file.
set +e

echo "=== 1. TCP throughput / RTT to arbitrary internet endpoints ==="
for h in 1.1.1.1 8.8.8.8 104.16.0.1; do
  echo -n "  $h: "
  timeout 6 bash -c "curl -s -o /dev/null -w 'http=%{http_code} connect=%{time_connect}s ttfb=%{time_starttransfer}s' --max-time 5 https://$h/" 2>/dev/null || echo -n "https-fail "
  timeout 6 ping -c3 -q "$h" 2>/dev/null | tail -1
done

echo "=== 2. Are SS/UoT DATA ports reachable FROM us (outbound TCP to high ports) ==="
for t in 1.1.1.1:8443 1.1.1.1:443 8.8.8.8:53 1.1.1.1:53; do
  ip=${t%%:*}; pt=${t##*:}
  echo -n "  tcp $t: "
  timeout 5 bash -c "exec 3<>/dev/tcp/$ip/$pt && echo OPEN" 2>/dev/null || echo closed
done

echo "=== 3. UDP egress (DNS via non-shim resolver + raw UDP to public) ==="
echo -n "  dig-ish via public resolver 1.1.1.1:53: "
timeout 6 python3 - <<'PY'
import socket,struct,random
q=b'\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00'
for p in 'example.com'.split('.'):
    q+=bytes([len(p)])+p.encode()
q+=b'\x00\x00\x01\x00\x01'
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.settimeout(5)
try:
    s.sendto(q,('1.1.1.1',53)); d,_=s.recvfrom(512); print('UDP-IN :53 OK (%d bytes)'%len(d))
except Exception as e: print('UDP-IN :53 FAIL', type(e).__name__)
PY
echo -n "  UDP to 8.8.8.8:53: "
timeout 6 python3 - <<'PY'
import socket
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.settimeout(5)
try:
    s.sendto(b'x'*16,('8.8.8.8',53)); print('send OK')
except Exception as e: print('FAIL', type(e).__name__)
PY

echo "=== 4. Inbound: does a random high port listen reach the internet? ==="
echo "  (testing outbound high-port TCP instead — inbound needs policy)"
for t in 1.1.1.1:8080 1.1.1.1:9000; do
  ip=${t%%:*}; pt=${t##*:}
  echo -n "  tcp $t: "
  timeout 4 bash -c "exec 3<>/dev/tcp/$ip/$pt && echo OPEN" 2>/dev/null || echo closed
done

echo "=== 5. Traceroute-ish first hops (does it look like a datacenter/ISP core?) ==="
timeout 25 bash -c 'for i in 1 2 3 4; do tracepath -m 4 -n 1.1.1.1 2>/dev/null | sed -n "1p;${i}p"; done' 2>/dev/null | head -8
command -v tracepath traceroute mtr 2>/dev/null

echo "=== 6. HTTPS to GitHub / typical game infra ==="
for u in https://github.com https://api.github.com https://store.steampowered.com; do
  echo -n "  $u: "
  timeout 12 curl -s -o /dev/null -w 'http=%{http_code} total=%{time_total}s\n' --max-time 10 "$u" || echo fail
done

echo "=== 7. Reverse DNS / hostname story ==="
echo "  hostname: $(hostname -f 2>/dev/null || hostname)"
echo "  public IP: $(timeout 10 curl -s https://api.ipify.org)"
echo "  ipify trace: $(timeout 10 curl -s https://api.ipify.org?format=json)"

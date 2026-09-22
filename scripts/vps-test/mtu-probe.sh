#!/bin/bash
# MTU / MSS / fragmentation path probe (read-only, no state change).
# This is the prime suspect for "residential VPS" tunnel packet problems.
set +e

IFACE=$(ip -4 route show default | awk '{print $5}' | head -1)
GW=$(ip -4 route show default | awk '{print $3}' | head -1)
echo "iface=$IFACE gw=$GW"
echo "-- interface MTU"
ip -4 -o addr show "$IFACE" | sed 's/^/  /'
ip link show "$IFACE" | grep -o 'mtu [0-9]*' | sed 's/^/  /'

echo "-- local path MTU to gateway (tracepath)"
timeout 20 tracepath -n -m 3 "$GW" 2>/dev/null | head -6 | sed 's/^/  /'

echo "-- path MTU discovery to public endpoints (tracepath)"
for h in 1.1.1.1 8.8.8.8 114.23.116.1; do
  echo "  -> $h"
  timeout 25 tracepath -n -m 5 "$h" 2>/dev/null | sed -n '1p;2p;3p;4p;5p' | sed 's/^/     /'
done

echo "-- TCP MSS clamp posture (what the host advertises/accepts)"
echo -n "  current net.ipv4.tcp_mtu_probing="; sysctl -n net.ipv4.tcp_mtu_probing
sysctl -a 2>/dev/null | grep -iE 'mtu|mss|tcp_mtu' | sed 's/^/  /'

echo "-- DF-probing: can a 1472-byte payload (1500 MTU, no frag) pass to the gateway?"
python3 - <<'PY'
import socket, struct
def probe(dst, size):
    # ICMP echo, DF set, payload = size
    payload = b'Q'*(size-8)
    hdr = struct.pack('!BBHHH', 8, 0, 0, 0, 0)
    csum = 0
    data = hdr + payload
    if len(data) % 2: data += b'\x00'
    for i in range(0, len(data), 2):
        csum += (data[i] << 8) + data[i+1]
    csum = (csum >> 16) + (csum & 0xffff)
    csum += csum >> 16
    hdr = struct.pack('!BBHHH', 8, 0, ~csum & 0xffff, 0, 0)
    # DF via IP_MTU_DISCOVER on a raw socket is messy; use UDP sendto with DF
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.setsockopt(socket.IPPROTO_IP, socket.IP_MTU_DISCOVER, socket.IP_PMTUDISC_DO)
    s.settimeout(3)
    try:
        s.sendto(b'X'*(size-28), (dst, 33434))
        return 'sent %d (no local EMSGSIZE)' % (size-28)
    except OSError as e:
        return 'EMSGSIZE at %d: %s' % (size-28, e)
    finally:
        s.close()
import time
for dst in ('114.23.116.1',):
    for sz in (1500, 1492, 1480, 1472, 1460, 1400, 1300):
        print('  udp payload for MTU %d to %s: %s' % (sz, dst, probe(dst, sz)))
PY

echo "-- does the local stack clamp MSS only, or is there a middlebox?"
echo -n "  iptables TCPSMSS rules: "; iptables -t mangle -S 2>/dev/null | grep -ci TCPMSS
echo -n "  nftables: "; nft list ruleset 2>/dev/null | grep -ci mss

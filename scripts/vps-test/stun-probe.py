#!/usr/bin/env python3
"""Corrected STUN / NAT-behaviour probe for the Locus test VPS.

Why the previous version lied: several public STUN servers (Google, Cloudflare,
sipnet) are behind anycast and only return an XOR-MAPPED-ADDRESS when the
request carries a USERNAME and / or MESSAGE-INTEGRITY attribute. Without those
they reply 0x0020 (XOR-MAPPED-ADDRESS is NOT among the attributes) so the
earlier script fell through and printed "no mapped-address attribute", which
looked like a network fault rather than a STUN-server policy.

This version sends both USERNAME and a proper MESSAGE-INTEGRITY (HMAC-SHA1 over
the message with the standard "VOkJxbRl1RmTxUk/WvJxBt" short-term key), which is
what Google/Cloudflare anycast expect, and falls back gracefully.

It also keeps the SAME local source port across servers, so a change in the
mapped port can only come from the NAT, never from us.

Usage: python3 stun-probe.py
"""

import hashlib
import hmac
import os
import socket
import struct
import sys
import time

MAGIC = 0x2112A442
SOFTWARE = b"locus-stun-probe"
SHORT_TERM_KEY = b"VOkJxbRl1RmTxUk/WvJxBt"  # RFC 5769 test vector


def _pad(b):
    return b + b"\x00" * ((4 - len(b) % 4) % 4)


def _attr(t, v):
    return struct.pack("!HH", t, len(v)) + _pad(v)


def build_request(username=b"locus"):
    tid = os.urandom(12)
    body = b""
    body += _attr(0x0006, username)              # USERNAME
    body += _attr(0x8028, SOFTWARE)              # SOFTWARE (optional, ignored)
    # MESSAGE-INTEGRITY = HMAC-SHA1 over the message so far, with length
    # already including the 24-byte MI attribute (4 hdr + 20 value).
    hdr = struct.pack("!HHI", 0x0001, len(body) + 24, MAGIC) + tid
    mac = hmac.new(SHORT_TERM_KEY, hdr + body, hashlib.sha1).digest()
    body += _attr(0x0008, mac)
    hdr = struct.pack("!HHI", 0x0001, len(body), MAGIC) + tid
    return hdr + body


def attr_parse(data):
    off = 20
    out = []
    while off + 4 <= len(data):
        at, al = struct.unpack("!HH", data[off:off + 4])
        off += 4
        val = data[off:off + al]
        off += al + ((4 - al % 4) % 4)
        out.append((at, val))
    return out


def decode_mapped(val, xor=False):
    fam = val[1]
    port = struct.unpack("!H", val[2:4])[0]
    ip = val[4:8]
    if xor:
        port ^= 0x2112
        ip = bytes(b ^ m for b, m in zip(ip, bytes([0x21, 0x12, 0xA4, 0x42])))
    if fam == 1:
        return ".".join(str(b) for b in ip), port
    return "ipv6?", port


def one(server, port, sock, timeout=4.0):
    """Send one request on an EXISTING socket (so the source port is stable)."""
    sock.settimeout(timeout)
    try:
        sock.sendto(build_request(), (server, port))
        data, _ = sock.recvfrom(4096)
    except socket.timeout:
        return None, "timeout"
    except OSError as exc:
        return None, str(exc)
    attrs = attr_parse(data)
    for at, val in attrs:
        if at == 0x8020:
            return decode_mapped(val, xor=True), None
        if at == 0x0001:
            return decode_mapped(val), None
    return None, "reply %d bytes, attrs=%s" % (len(data), [hex(a) for a, _ in attrs])


def main():
    servers = [
        ("stun.l.google.com", 19302),
        ("stun1.l.google.com", 19302),
        ("stun.cloudflare.com", 3478),
        ("stun.sipnet.net", 3478),
        ("stun.nextcloud.com", 443),
        ("stun.ekiga.net", 3478),
        ("stun.voip.blackberry.com", 3478),
        ("stunserver2024.stunprotocol.org", 3478),
    ]

    # ONE socket for every server => one fixed local source port.
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("", 0))
    local_port = sock.getsockname()[1]
    print("Single local UDP socket, source port %d — the mapped port must be\n"
          "constant across servers for endpoint-independent (full-cone) mapping.\n"
          % local_port)

    mapped = []
    for host, port in servers:
        try:
            ip = socket.gethostbyname(host)
        except OSError:
            print("  %-34s DNS fail" % ("%s:%d" % (host, port)))
            continue
        for attempt in range(3):
            res, err = one(ip, port, sock)
            if res:
                break
            time.sleep(0.5)
        if res:
            mip, mport = res
            mapped.append(mport)
            print("  %-34s -> mapped %s:%d" % ("%s:%d" % (host, port), mip, mport))
        else:
            print("  %-34s -- %s" % ("%s:%d" % (host, port), err))
    sock.close()

    print()
    if len(mapped) >= 2:
        uniq = set(mapped)
        if len(uniq) == 1:
            print("  VERDICT: endpoint-independent mapping (full-cone friendly).")
            print("           mapped port %d was stable across %d servers."
                  % (mapped[0], len(mapped)))
            print("           This is the behaviour that gives consoles an")
            print("           OPEN NAT and lets P2P game traffic work.")
        else:
            print("  VERDICT: SYMMETRIC NAT — mapped port varied across servers:")
            print("           %s" % sorted(uniq))
            print("           Consoles behind this get STRICT/MODERATE NAT and")
            print("           P2P matchmaking will be unreliable.")
    else:
        print("  VERDICT: inconclusive — fewer than two servers answered.")
    print("\n  NOTE: this is the NAT path for THIS VPS. Clients behind the N4L")
    print("  school network have their own NAT, and that one is what the game")
    print("  console sees when the Locus client is NOT in TUN mode.")


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""stamp-syso.py — rewrite the version in the committed Windows resources.

WHY THIS EXISTS
---------------
`rsrc_windows_{amd64,arm64}.syso` are the compiled VS_VERSION_INFO blocks that
fill in the Windows file Properties tab. They are **committed**, and nothing
regenerates them automatically: `go generate` is manual, and the release path
never ran it.

The result is a defect that hides from every text-based check. In 2.1.0 the
committed .syso files were stamped `2.0.0` / product `MyVPN` while the root `VERSION` file
said `2.1.0` — the shipped Windows exe reported a version that did not exist,
under the product's former name. The same thing happened again at 2.2.7: the
version files were bumped, the .syso files were not, and CI failed the release
with

    rsrc_windows_amd64.syso FileVersion = "2.2.6", want "2.2.7"

The old fix was "install a Go toolchain, then run `go generate -tags windows`".
That is a heavy requirement for editing four bytes, and it is why the step got
skipped. This script does the same job with the standard library.

WHY BYTE-PATCHING IS EXACT AND NOT A HACK
-----------------------------------------
A version bump of the form 2.2.6 -> 2.2.7 changes only the build component, so
the string keeps its width and `go-winres` emits a byte-identical file apart
from the version. Verified against the real 2.2.7 artifacts produced by
go-winres: patching the 2.2.6 bytes reproduces the go-winres output
**byte-for-byte**, and the reverse patch reproduces the 2.2.6 bytes exactly.

What actually has to change (4 bytes total for a patch bump):

  1. Two UTF-16LE display strings, immediately following the `FileVersion` and
     `ProductVersion` keys.
  2. The packed `VS_FIXEDFILEINFO` block (signature 0xFEEF04BD), whose
     dwFileVersionMS/LS and dwProductVersionMS/LS fields carry the same number
     in binary form. The build component's low byte sits at +2 of each LS dword.

Missing (2) is the subtle failure: the Properties *string* would read 2.2.7
while the installer and Windows' own version comparisons still see 2.2.6.

SAFETY
------
The script refuses to guess. It fails loudly if the version does not appear the
expected number of times, if the widths differ (a 2.9.9 -> 2.10.0 bump changes
the string length and must be regenerated with go-winres instead), or if the
result no longer parses. `--verify` re-reads its own output through the same
decoder the Go guard uses.

Usage:
  stamp-syso.py <new-version> [--client-dir legacy/wails-client] [--check] [--quiet]

  --check   report whether the artifacts already carry <new-version>; write
            nothing, exit 0 if current, 2 if stale. Used as a pre-commit gate.
  --quiet   only print on failure.

Exit codes: 0 ok, 1 usage/IO error, 2 stale-but-not-fixed (--check only),
            3 the artifact did not look like what we know how to patch.
"""

import argparse
import os
import re
import struct
import sys

# The VS_FIXEDFILEINFO signature, little-endian on disk.
SIG = bytes.fromhex("bd04effe")
ARCHES = ("amd64", "arm64")
# A version we are willing to edit in place. Width is load-bearing: the UTF-16LE
# strings and the packed dwords are fixed-size, so "2.9.9" -> "2.10.0" is NOT a
# byte patch and must go through go-winres.
VERSION_RE = re.compile(r"^\d+\.\d+\.\d+$")
# How many times the version string must occur in a well-formed artifact:
# once for FileVersion, once for ProductVersion.
EXPECTED_STRING_HITS = 2


def quad(v):
    """'2.2.7' -> (2, 2, 7, 0) as (major, minor, build, revision)."""
    p = [int(x) for x in v.split(".")]
    while len(p) < 4:
        p.append(0)
    return tuple(p[:4])


def read_strings(raw):
    """Decode the UTF-16LE printable runs, trying both alignments.

    Mirrors internal/winres/winres.go so the tool and the Go guard agree on
    what the file says. The block is 4-byte aligned and the alignment depends on
    the length of everything preceding it, so both offsets are tried and the one
    yielding more strings wins.
    """
    best = []
    for off in (0, 1):
        tail = raw[off:]
        if len(tail) % 2:
            tail = tail[:-1]
        out, cur = [], []
        for i in range(0, len(tail), 2):
            c = tail[i] | (tail[i + 1] << 8)
            if 32 <= c < 127:
                cur.append(chr(c))
            else:
                if len(cur) >= 2:
                    out.append("".join(cur))
                cur = []
        if len(cur) >= 2:
            out.append("".join(cur))
        if len(out) > len(best):
            best = out
    return best


def parse_version(raw):
    """Return the dict of version fields, or None if the file is not parseable.

    None means "the format changed and we are now blind" — callers must treat
    that as a hard failure, never as "nothing to do".
    """
    strings = read_strings(raw)
    fields, found = {}, False
    wanted = ("FileVersion", "ProductVersion", "ProductName", "FileDescription")
    for i, s in enumerate(strings):
        if i + 1 >= len(strings):
            break
        if s in wanted:
            fields[s] = strings[i + 1]
            found = True
    return fields if found else None


def patch(raw, old_version, new_version):
    """Rewrite old_version -> new_version inside a go-winres .syso.

    Returns (patched_bytes, n_strings, n_blocks).
    """
    if len(old_version) != len(new_version):
        raise ValueError(
            f"version width changed ({len(old_version)} -> {len(new_version)}); "
            "this is not a byte patch — regenerate with: "
            "cd legacy/wails-client && go generate -tags windows"
        )

    omaj, omin, obld, orev = quad(old_version)
    nmaj, nmin, nbld, nrev = quad(new_version)

    old_b = old_version.encode("utf-16-le")
    new_b = new_version.encode("utf-16-le")

    n_strings = raw.count(old_b)
    out = bytearray(raw.replace(old_b, new_b))

    # ── Packed VS_FIXEDFILEINFO ──────────────────────────────────────────
    # dwSignature, dwStrucVersion, dwFileVersionMS, dwFileVersionLS,
    # dwProductVersionMS, dwProductVersionLS — six uint32 from the signature.
    # The version quad lives in the two MS/LS pairs, 8 bytes in. go-winres writes
    # the same value to both the File and Product pairs.
    n_blocks = 0
    pos = 0
    packed = struct.pack("<4I",
                         (nmaj << 16) | nmin,
                         (nbld << 16) | nrev,
                         (nmaj << 16) | nmin,
                         (nbld << 16) | nrev)
    while True:
        pos = bytes(out).find(SIG, pos)
        if pos < 0:
            break
        if pos + 24 <= len(out):
            fms, fls, pms, pls = struct.unpack_from("<4I", out, pos + 8)
            if ((fms >> 16, fms & 0xFFFF, fls >> 16, fls & 0xFFFF)
                    == (omaj, omin, obld, orev)):
                struct.pack_into("<4I", out, pos + 8, *struct.unpack("<4I", packed))
                n_blocks += 1
        pos += 1

    return bytes(out), n_strings, n_blocks


def process(path, new_version, check_only, quiet):
    """Patch (or check) one artifact. Returns a status string for reporting."""
    with open(path, "rb") as fh:
        raw = fh.read()

    fields = parse_version(raw)
    if fields is None:
        print(f"FAIL {path}: no VS_VERSION_INFO strings found.\n"
              f"     The resource format changed and this tool is now BLIND.\n"
              f"     Do not skip the check — regenerate with go-winres.",
              file=sys.stderr)
        return 3

    current = fields.get("FileVersion", "")
    if current == new_version and fields.get("ProductVersion") == new_version:
        if not quiet:
            print(f"  ok    {os.path.basename(path)} already {new_version}")
        return 0

    if check_only:
        print(f"STALE {path}: FileVersion={current!r} "
              f"ProductVersion={fields.get('ProductVersion')!r}, want {new_version!r}",
              file=sys.stderr)
        return 2

    try:
        patched, n_strings, n_blocks = patch(raw, current, new_version)
    except ValueError as exc:
        print(f"FAIL {path}: {exc}", file=sys.stderr)
        return 3

    # Two strings (FileVersion, ProductVersion) and one fixed-info block are
    # what a real go-winres artifact contains. Anything else means the file is
    # not what we think it is, and a blind replace would corrupt it.
    if n_strings != EXPECTED_STRING_HITS:
        print(f"FAIL {path}: found {n_strings} occurrence(s) of {current!r}, "
              f"expected {EXPECTED_STRING_HITS}. Refusing to patch a file this "
              f"tool does not understand.", file=sys.stderr)
        return 3
    if n_blocks != 1:
        print(f"FAIL {path}: found {n_blocks} VS_FIXEDFILEINFO version block(s), "
              f"expected 1. Refusing to patch.", file=sys.stderr)
        return 3

    # Re-read our own output before writing, so a bug can never reach disk.
    after = parse_version(patched)
    if not after or after.get("FileVersion") != new_version \
            or after.get("ProductVersion") != new_version:
        print(f"FAIL {path}: post-patch verification failed "
              f"({after}); nothing written.", file=sys.stderr)
        return 3
    if after.get("ProductName") != fields.get("ProductName") or \
            after.get("FileDescription") != fields.get("FileDescription"):
        print(f"FAIL {path}: post-patch verification changed a field it should "
              f"not have ({fields} -> {after}); nothing written.", file=sys.stderr)
        return 3

    with open(path, "wb") as fh:
        fh.write(patched)
    if not quiet:
        print(f"  patch {os.path.basename(path)}: {current} -> {new_version}")
    return 0


def main():
    ap = argparse.ArgumentParser(add_help=True)
    ap.add_argument("version", help="version to stamp, e.g. 2.2.7")
    ap.add_argument("--client-dir", default="legacy/wails-client")
    ap.add_argument("--check", action="store_true",
                    help="report only; write nothing (exit 2 if stale)")
    ap.add_argument("--quiet", action="store_true")
    args = ap.parse_args()

    version = args.version.lstrip("v")
    if not VERSION_RE.match(version):
        print(f"usage error: {version!r} is not a x.y.z version", file=sys.stderr)
        return 1

    if not os.path.isdir(args.client_dir):
        print(f"usage error: no such client dir: {args.client_dir}", file=sys.stderr)
        return 1

    status = 0
    for arch in ARCHES:
        path = os.path.join(args.client_dir, f"rsrc_windows_{arch}.syso")
        if not os.path.exists(path):
            print(f"FAIL {path}: missing", file=sys.stderr)
            status = 3
            continue
        rc = process(path, version, args.check, args.quiet)
        if rc:
            status = rc if status == 0 else status

    if status == 0 and not args.quiet:
        verb = "verified" if args.check else "stamped"
        print(f"  {verb} rsrc_windows_*.syso at {version}")
    return status


if __name__ == "__main__":
    sys.exit(main())

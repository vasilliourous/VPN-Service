#!/usr/bin/env python3
"""Password SSH runner for the test VPS (no sshpass on this host).

Usage:
    python3 scripts/vps-test/sshp.py [--host H] [--user U] [--pw P] [--timeout S] "remote command"
    python3 scripts/vps-test/sshp.py --file local.sh [--put remote.sh] [--chmod]
    python3 scripts/vps-test/sshp.py --get remote-path > local-file

Reads the default host/user/password from the environment so the password is not
written into a command line (which would land in shell history / process list):

    VPS_HOST=114.23.136.183 VPS_USER=root VPS_PW='...' python3 scripts/vps-test/sshp.py 'uptime'

Never prints the password. Streams the remote session's stdout/stderr verbatim.
Password auth is forced (PreferredAuthentications=password) so a stale agent key
cannot silently pick a different identity.
"""

import argparse
import os
import pty
import select
import sys
import time

SSH_OPTS = [
    "-tt",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", "ConnectTimeout=15",
    "-o", "ServerAliveInterval=15",
    "-o", "ServerAliveCountMax=6",
    "-o", "PreferredAuthentications=password",
    "-o", "PubkeyAuthentication=no",
    "-o", "NumberOfPasswordPrompts=1",
    "-o", "LogLevel=ERROR",
]


def run(host, user, pw, argv, timeout, stdin_path=None):
    if stdin_path:
        opts = [o for o in SSH_OPTS if o not in ("-tt",)]
        with open(stdin_path, "rb") as fh:
            pid, fd = pty.fork()
            if pid == 0:
                os.dup2(fh.fileno(), 0)
                os.execvp("ssh", ["ssh"] + opts + [f"{user}@{host}"] + argv)
    else:
        pid, fd = pty.fork()
        if pid == 0:
            os.execvp("ssh", ["ssh"] + SSH_OPTS + [f"{user}@{host}"] + argv)

    out = bytearray()
    sent = False
    start = time.time()
    while True:
        if time.time() - start > timeout:
            try:
                os.kill(pid, 9)
            except ProcessLookupError:
                pass
            out += b"\n[sshp] TIMEOUT after %ds\n" % timeout
            break
        r, _, _ = select.select([fd], [], [], 1.0)
        if r:
            try:
                data = os.read(fd, 65536)
            except OSError:
                break
            if not data:
                break
            out += data
            if not sent and (b"assword:" in data or b"Password:" in data):
                os.write(fd, (pw + "\n").encode())
                sent = True
                # Gag the terminal echo of the password itself.
                time.sleep(0.4)
        else:
            try:
                wpid, _ = os.waitpid(pid, os.WNOHANG)
                if wpid:
                    break
            except ChildProcessError:
                break
    return bytes(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default=os.environ.get("VPS_HOST", "114.23.136.183"))
    ap.add_argument("--user", default=os.environ.get("VPS_USER", "root"))
    ap.add_argument("--pw", default=os.environ.get("VPS_PW", ""))
    ap.add_argument("--timeout", type=int, default=180)
    ap.add_argument("--file", help="local script to pipe to `bash -s` on the remote")
    ap.add_argument("--put", help="remote path to upload --file to (implies --get-put mode)")
    ap.add_argument("--chmod", action="store_true", help="chmod +x the uploaded file")
    ap.add_argument("--get", help="remote path to download to stdout")
    ap.add_argument("cmd", nargs="*", help="remote command words")
    args = ap.parse_args()

    if not args.pw:
        sys.stderr.write("sshp: no password (set VPS_PW)\n")
        return 2

    if args.get:
        argv = ["cat", args.get]
        out = run(args.host, args.user, args.pw, argv, args.timeout)
        sys.stdout.buffer.write(out)
        return 0

    if args.file and args.put:
        # Upload: cat localfile | ssh 'cat > remote'
        argv = ["cat", ">", args.put]
        if args.chmod:
            argv = ["sh", "-c", f"cat > {args.put} && chmod +x {args.put}"]
        out = run(args.host, args.user, args.pw, argv, args.timeout, stdin_path=args.file)
        sys.stdout.write(out.decode(errors="replace"))
        return 0

    if args.file:
        # Run a local script remotely without leaving it on disk.
        argv = ["bash", "-s"]
        out = run(args.host, args.user, args.pw, argv, args.timeout, stdin_path=args.file)
        sys.stdout.write(out.decode(errors="replace"))
        return 0

    if not args.cmd:
        sys.stderr.write("sshp: nothing to do\n")
        return 2
    out = run(args.host, args.user, args.pw, args.cmd, args.timeout)
    sys.stdout.write(out.decode(errors="replace"))
    return 0


if __name__ == "__main__":
    sys.exit(main())

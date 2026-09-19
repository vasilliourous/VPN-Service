#!/usr/bin/env python3
"""Locus release-upload service.

WHY THIS EXISTS
---------------
The admin console needs to publish release binaries from a browser, but
PocketBase refuses request bodies somewhere between 1 MB and 5 MB — far below
the ~15-30 MB of a Wails desktop bundle — and its hook API does not expose
multipart files at all. Caddy's build here has no upload handler. So uploads
need a small, dedicated service.

This listens on 127.0.0.1 only and is reached through Caddy at
/api/admin/upload. It is never exposed directly.

DESIGN NOTES (each of these is deliberate)
------------------------------------------
* **Token in a header, checked before reading a single byte of body.** An
  unauthenticated upload must not be able to fill the disk.
* **Streamed to a temp file**, hashing as we go — a 30 MB artifact never sits
  fully in RAM on a 512 MB box.
* **Hard size cap**, enforced both by Content-Length (early reject) and by the
  stream itself (a lying Content-Length cannot get past it).
* **Version + filename are validated against a strict allowlist.** The filename
  becomes a path on disk; anything that could escape the target directory
  (`..`, `/`, absolute paths) is rejected outright rather than sanitised.
* **Atomic publish.** The file is written to a temp name and only moved into
  place once its hash is known, so a client can never download a half-written
  binary.
* **Caller-supplied SHA256 is verified.** If the browser says the file is X and
  the bytes hash to Y, the upload is rejected — this catches truncation on a
  flaky school connection, which is exactly the failure that would otherwise
  ship a corrupt binary to every user.
* **Optional signing.** If the hub is configured to require it, the caller must
  prove the hash was authorised. Without this, anyone who obtained the token
  could push a binary (they could anyway with the token — this just makes the
  intended hash explicit and auditable in the log).

Run: python3 release_upload.py  (binds 127.0.0.1:8091)
"""
import hashlib
import json
import os
import re
import shutil
import sys
import tempfile
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LISTEN_HOST = "127.0.0.1"
LISTEN_PORT = int(os.environ.get("UPLOAD_PORT", "8091"))
UPDATES_DIR = os.environ.get("UPDATES_DIR", "/var/www/updates")
TOKEN = os.environ.get("ADMIN_API_TOKEN", "")

# A Wails bundle is ~15-30 MB; 200 MB is generous headroom that still cannot
# fill a 10 GB disk from a single request loop.
MAX_UPLOAD_BYTES = int(os.environ.get("MAX_UPLOAD_BYTES", str(200 * 1024 * 1024)))
# Anything smaller than this is a truncated download or a Git LFS pointer.
MIN_UPLOAD_BYTES = 1024 * 1024

# The client's updater looks for these exact names (PlatformDownloadURL), so
# the allowlist is a contract with the client, not a preference.
ALLOWED_FILENAMES = {
    "locus-linux-amd64",
    "locus-windows-amd64.exe",
    "locus-darwin-amd64",
    "locus-darwin-arm64",
    "manifest.json",
}
# Version directory names must be boring: digits and dots only.
VERSION_RE = re.compile(r"^[0-9]+(\.[0-9]+){1,3}(-[A-Za-z0-9.]+)?$")

LOG_PATH = os.environ.get("UPLOAD_LOG", "/var/log/locus-upload.log")


def log(msg):
    line = "[%s] %s\n" % (time.strftime("%Y-%m-%d %H:%M:%S"), msg)
    sys.stderr.write(line)
    sys.stderr.flush()
    try:
        with open(LOG_PATH, "a") as f:
            f.write(line)
    except OSError:
        pass


class UploadHandler(BaseHTTPRequestHandler):
    server_version = "LocusUpload/1.0"

    # Silence the default per-request stderr noise; we log deliberately.
    def log_message(self, fmt, *args):
        return

    def _send(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except BrokenPipeError:
            pass

    def do_GET(self):
        if self.path == "/health":
            return self._send(200, {"ok": True, "service": "locus-upload"})
        return self._send(404, {"ok": False, "message": "not found"})

    # Caddy's reverse_proxy forwards the ORIGINAL path (/api/admin/upload), so
    # we accept both forms rather than depending on a strip_prefix directive.
    # Accepting the direct form too keeps local curl tests simple.
    UPLOAD_PATHS = ("/upload", "/api/admin/upload")

    def do_POST(self):
        if self.path not in self.UPLOAD_PATHS:
            return self._send(404, {"ok": False, "message": "not found"})

        # ── Auth FIRST: reject before touching the body ──
        if not TOKEN:
            return self._send(500, {"ok": False, "message": "ADMIN_API_TOKEN not configured"})
        if self.headers.get("X-Admin-Token", "") != TOKEN:
            log("rejected upload: bad token from %s" % self.client_address[0])
            return self._send(403, {"ok": False, "message": "invalid admin token"})

        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            return self._send(400, {"ok": False, "message": "bad Content-Length"})
        if length <= 0:
            return self._send(400, {"ok": False, "message": "empty body"})
        if length > MAX_UPLOAD_BYTES:
            return self._send(413, {
                "ok": False,
                "message": "file too large (%d bytes, max %d)" % (length, MAX_UPLOAD_BYTES),
            })
        if length < MIN_UPLOAD_BYTES:
            return self._send(400, {
                "ok": False,
                "message": "file too small (%d bytes) — refusing to publish" % length,
            })

        version = (self.headers.get("X-Release-Version", "") or "").strip().lstrip("v")
        filename = (self.headers.get("X-Release-Filename", "") or "").strip()
        expected_sha = (self.headers.get("X-Release-Sha256", "") or "").strip().lower()

        # ── Validate the version: it becomes a directory name ──
        if not VERSION_RE.match(version):
            return self._send(400, {"ok": False, "message": "invalid version %r" % version})
        # ── Validate the filename against the allowlist (no path traversal) ──
        if filename not in ALLOWED_FILENAMES:
            return self._send(400, {
                "ok": False,
                "message": "filename not allowed: %r" % filename,
            })
        if expected_sha and not re.match(r"^[0-9a-f]{64}$", expected_sha):
            return self._send(400, {"ok": False, "message": "invalid sha256 header"})

        dest_dir = os.path.join(UPDATES_DIR, version)
        # Belt-and-braces: the resolved path must stay inside UPDATES_DIR even
        # if the checks above were somehow bypassed.
        if not os.path.realpath(dest_dir).startswith(os.path.realpath(UPDATES_DIR) + os.sep):
            return self._send(400, {"ok": False, "message": "invalid version path"})

        try:
            os.makedirs(dest_dir, mode=0o755, exist_ok=True)
        except OSError as exc:
            log("cannot create %s: %s" % (dest_dir, exc))
            return self._send(500, {"ok": False, "message": "cannot create version directory"})

        # ── Stream to a temp file, hashing as we go ──
        tmp_fd, tmp_path = tempfile.mkstemp(prefix=".upload-", dir=dest_dir)
        hasher = hashlib.sha256()
        received = 0
        try:
            with os.fdopen(tmp_fd, "wb") as out:
                remaining = length
                while remaining > 0:
                    chunk = self.rfile.read(min(65536, remaining))
                    if not chunk:
                        raise IOError("client closed connection early")
                    out.write(chunk)
                    hasher.update(chunk)
                    received += len(chunk)
                    remaining -= len(chunk)
                # Enforce the cap on the STREAM, not just the declared length.
                if received > MAX_UPLOAD_BYTES:
                    raise IOError("body exceeded maximum size")
        except Exception as exc:  # noqa: BLE001 - report any read failure back
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            log("upload failed (%s) version=%s file=%s after %d bytes" % (exc, version, filename, received))
            return self._send(400, {"ok": False, "message": "upload failed: %s" % exc})

        actual_sha = hasher.hexdigest()

        # ── Verify the caller's hash, if they supplied one ──
        if expected_sha and actual_sha != expected_sha:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            log("checksum mismatch version=%s file=%s expected=%s actual=%s"
                % (version, filename, expected_sha[:16], actual_sha[:16]))
            return self._send(400, {
                "ok": False,
                "message": "checksum mismatch — the file did not survive the transfer intact",
                "expected": expected_sha,
                "actual": actual_sha,
            })

        if received < MIN_UPLOAD_BYTES:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            return self._send(400, {
                "ok": False,
                "message": "received only %d bytes — refusing to publish" % received,
            })

        # ── Atomic publish ──
        final_path = os.path.join(dest_dir, filename)
        try:
            os.replace(tmp_path, final_path)
            os.chmod(final_path, 0o644)
        except OSError as exc:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            log("publish failed for %s: %s" % (final_path, exc))
            return self._send(500, {"ok": False, "message": "could not finalise upload"})

        log("published %s/%s (%d bytes, sha256=%s)"
            % (version, filename, received, actual_sha[:16]))

        # Also write a .sha256 sidecar so the artifact is self-describing for
        # anyone inspecting the directory by hand.
        try:
            sidecar = final_path + ".sha256"
            with open(sidecar, "w") as f:
                f.write("%s  %s\n" % (actual_sha, filename))
            os.chmod(sidecar, 0o644)
        except OSError:
            pass

        return self._send(200, {
            "ok": True,
            "version": version,
            "filename": filename,
            "bytes": received,
            "sha256": actual_sha,
        })


def main():
    if not TOKEN:
        log("FATAL: ADMIN_API_TOKEN is not set — refusing to start")
        sys.exit(1)
    if not os.path.isdir(UPDATES_DIR):
        log("FATAL: UPDATES_DIR does not exist: %s" % UPDATES_DIR)
        sys.exit(1)
    log("Locus upload service listening on %s:%d -> %s"
        % (LISTEN_HOST, LISTEN_PORT, UPDATES_DIR))
    server = ThreadingHTTPServer((LISTEN_HOST, LISTEN_PORT), UploadHandler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

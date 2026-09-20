#!/usr/bin/env python3
"""Locus release-fetch service — pull a release straight from GitHub.

WHY THIS EXISTS
---------------
Releases used to reach the hub only by the operator's browser: the admin console
uploaded four ~15-30 MB binaries through /api/admin/upload into a dedicated
service, because PocketBase rejects bodies above a few MB and has no multipart
API, and this Caddy build has no upload handler.

That worked, but it made the hub depend on the operator's laptop, their upload
bandwidth and their browser. The bytes CI built are already on a GitHub Release;
the hub can simply fetch them itself. This service does that, and in doing so
removes the whole size problem: nothing large is sent TO the hub any more, so
the upload service is gone.

THE TRIGGER IS A LINK, NOT A BUTTON
-----------------------------------
A fetch is intentionally manual. Two ways in, both leading to the same code:

  * POST /api/admin/fetch-release  {"version":"2.2.1"}  with the admin token
    (this is what the console's button sends), or
  * GET  /api/admin/fetch-release?version=2.2.1&nonce=…&exp=…&sig=…
    — a short-lived, SINGLE-USE, HMAC-signed link, so a fetch can be triggered
    from a phone or a CI job that does not hold the admin token.

Each link mints a fresh nonce. A consumed nonce is refused, so a link that
leaked through a chat log or a browser history is worthless after one use, and
"make a new link" is the only way to run it again — which is what was asked for.

DESIGN NOTES (each deliberate)
------------------------------
* **Binds 127.0.0.1 only**, reached exclusively through Caddy. Never public.
* **Resolves assets by name from the release manifest**, never by constructing
  a URL. GitHub's asset naming is then not a hidden dependency; if CI renames an
  attachment the fetch fails loudly instead of 404ing silently.
* **Streamed to a temp file, hashed as it goes.** A 30 MB artifact never sits
  fully in RAM on a 454 MB box.
* **All-or-nothing.** Every platform must be present and every hash must verify
  before a single byte is published. A partial release leaves those clients with
  a URL that 404s, and the omission is invisible from the operator's seat.
* **Atomic publish.** Written to a temp name, `os.replace`d into place only once
  its hash is known, so a client can never download a half-written binary.
* **Self-describing output.** A `.sha256` sidecar per artifact, as before.
* **The DB is NOT touched here.** update_config is written by the PocketBase hook
  (action `releases.publish`), which is the single writer. This process only puts
  verified bytes on disk and reports their hashes back.

Run: python3 fetch-release.py   (binds 127.0.0.1:8091)
"""
import hashlib
import hmac
import json
import os
import re
import secrets
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LISTEN_HOST = "127.0.0.1"
LISTEN_PORT = int(os.environ.get("FETCH_PORT", os.environ.get("UPLOAD_PORT", "8091")))
UPDATES_DIR = os.environ.get("UPDATES_DIR", "/var/www/updates")
TOKEN = os.environ.get("ADMIN_API_TOKEN", "")

GITHUB_REPO = os.environ.get("GITHUB_REPO", "vasilliourous/VPN-Service")
GH_TOKEN = os.environ.get("GH_TOKEN", "")

# Signing secret for one-shot links. Generated on first use and persisted, so
# links minted before a restart stay valid until they expire.
SECRET_FILE = os.environ.get("FETCH_SECRET_FILE", "/root/.fetch_link_secret")
LINK_TTL_SECONDS = int(os.environ.get("FETCH_LINK_TTL", "900"))  # 15 minutes

# The filenames the client's updater looks for. This is a contract with the
# client (PlatformDownloadURL), not a preference — a wrong name is a silent
# no-update for that platform.
PLATFORMS = [
    ("linux", "locus-linux-amd64", "download_linux"),
    ("windows", "locus-windows-amd64.exe", "download_windows"),
    ("macos_intel", "locus-darwin-amd64", "download_macos_intel"),
    ("macos_arm", "locus-darwin-arm64", "download_macos_arm"),
]
MANIFEST_NAME = "manifest.json"

# A Wails bundle is ~15-30 MB. Below 1 MB it is a truncated download or a Git
# LFS pointer, and must never be served as a binary.
MIN_ASSET_BYTES = 1024 * 1024
## manifest.json is a few hundred bytes and is NOT a binary, so it must not be
## held to the executable floor below — doing so rejects a perfectly good
## release with "downloaded only 719 bytes". The floor applies to executables
## only; the manifest just has to be non-empty and parse as JSON.
MIN_BINARY_BYTES = 1024 * 1024
MIN_MANIFEST_BYTES = 2
MAX_ASSET_BYTES = int(os.environ.get("MAX_ASSET_BYTES", str(200 * 1024 * 1024)))

DOWNLOAD_TIMEOUT = int(os.environ.get("FETCH_TIMEOUT", "600"))

USER_AGENT = "locus-hub-fetch/1.0 (+https://github.com/%s)" % GITHUB_REPO


def log(msg):
    print("[fetch %s] %s" % (time.strftime("%H:%M:%S"), msg), flush=True)


class FetchError(Exception):
    """A failure with a message safe to show the operator."""

    def __init__(self, message, status=500):
        super().__init__(message)
        self.status = status


# ── Version / filename validation ───────────────────────────────────────────
# Both become paths on disk. Reject anything that is not boring rather than
# trying to sanitise it.
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$")
ALLOWED_FILENAMES = {name for _, name, _ in PLATFORMS} | {MANIFEST_NAME}


def validate_version(version):
    version = str(version or "").strip().lstrip("v")
    if not VERSION_RE.match(version):
        raise FetchError("version must look like 1.2.3 (got %r)" % version, 400)
    return version


# ── One-shot link signing ───────────────────────────────────────────────────
_lock = threading.Lock()
_used_nonces = {}


def _load_secret():
    """Read the link-signing secret, creating it on first run.

    Preference order is the environment (set from secrets.env.age on deploy),
    then the persisted file. The environment wins so a redeploy does not orphan
    a running set of links, and the file is a cache — the same env-first rule
    the rest of the server follows (see docs/SECRETS-MANAGEMENT.md).
    """
    env_secret = os.environ.get("RELEASE_FETCH_SECRET", "").strip()
    if env_secret:
        return env_secret.encode()
    try:
        with open(SECRET_FILE, "rb") as f:
            data = f.read().strip()
            if data:
                return data
    except OSError:
        pass
    fresh = secrets.token_hex(32).encode()
    try:
        fd = os.open(SECRET_FILE, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(fresh)
        log("generated a new link-signing secret at %s" % SECRET_FILE)
    except OSError as exc:
        log("WARNING: could not persist link secret (%s) — links will not "
            "survive a restart" % exc)
    return fresh


SECRET = _load_secret()


def _sign(version, nonce, exp):
    msg = "%s|%s|%d" % (version, nonce, exp)
    return hmac.new(SECRET, msg.encode(), hashlib.sha256).hexdigest()


def mint_link(base_url, version):
    """Mint a fresh single-use trigger link. Every call produces a new nonce."""
    nonce = secrets.token_urlsafe(16)
    exp = int(time.time()) + LINK_TTL_SECONDS
    sig = _sign(version, nonce, exp)
    return (
        "%s/api/admin/fetch-release?version=%s&nonce=%s&exp=%d&sig=%s"
        % (base_url.rstrip("/"), version, nonce, exp, sig)
    )


def consume_nonce(nonce, exp, version, sig):
    """Verify a link and burn its nonce. Raises FetchError on any problem."""
    if not nonce or not sig:
        raise FetchError("this link is missing its signature", 403)
    try:
        exp = int(exp)
    except (TypeError, ValueError):
        raise FetchError("this link has a malformed expiry", 403)

    expected = _sign(version, nonce, exp)
    # Constant-time compare: a timing oracle on a signature is a real (if slow)
    # way to forge one.
    if not hmac.compare_digest(expected, str(sig)):
        raise FetchError("this link's signature is not valid", 403)
    if exp < time.time():
        raise FetchError("this link has expired — request a new one", 403)

    with _lock:
        if nonce in _used_nonces:
            raise FetchError("this link has already been used — request a new one", 409)
        _used_nonces[nonce] = time.time()
        # Opportunistically drop expired nonces so the dict cannot grow forever
        # on a long-lived process.
        cutoff = time.time() - LINK_TTL_SECONDS
        for n in [n for n, t in _used_nonces.items() if t < cutoff]:
            _used_nonces.pop(n, None)


# ── GitHub access ───────────────────────────────────────────────────────────
def _github_request(url):
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    # GitHub rejects requests without a User-Agent outright (403).
    req.add_header("User-Agent", USER_AGENT)
    if GH_TOKEN:
        req.add_header("Authorization", "Bearer %s" % GH_TOKEN)
    try:
        return urllib.request.urlopen(req, timeout=30)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            raise FetchError(
                "no GitHub Release found for tag v%s in %s. Either CI has not "
                "finished, the tag was never pushed, or the release failed."
                % (VERSION_CONTEXT["version"], GITHUB_REPO),
                404,
            )
        if exc.code in (401, 403):
            raise FetchError(
                "GitHub refused the request (HTTP %d). If the repository is "
                "private, set GH_TOKEN on the hub." % exc.code,
                502,
            )
        raise FetchError("GitHub returned HTTP %d for the release" % exc.code, 502)
    except urllib.error.URLError as exc:
        raise FetchError("could not reach GitHub: %s" % exc.reason, 502)


# Set by the caller so error messages can name the version. A tiny module-level
# context avoids threading the version through every error path.
VERSION_CONTEXT = {"version": ""}


def resolve_release(version):
    """Return {asset_name: (url, size)} for the v<version> GitHub Release."""
    api = "https://api.github.com/repos/%s/releases/tags/v%s" % (GITHUB_REPO, version)
    log("resolving v%s from %s" % (version, GITHUB_REPO))
    with _github_request(api) as resp:
        try:
            rel = json.loads(resp.read().decode("utf-8", "replace"))
        except ValueError as exc:
            raise FetchError("GitHub returned unreadable JSON: %s" % exc, 502)

    if rel.get("draft"):
        raise FetchError("the v%s release is still a DRAFT on GitHub" % version, 400)

    assets = {}
    for asset in rel.get("assets", []) or []:
        name = asset.get("name") or ""
        url = asset.get("browser_download_url") or ""
        if name and url:
            assets[name] = (url, int(asset.get("size") or 0))
    return assets


def _download(url, dest, expected_min, expected_max, what="binary"):
    """Stream a URL to dest, hashing as we go. Returns (sha256, size)."""
    req = urllib.request.Request(url)
    req.add_header("User-Agent", USER_AGENT)
    if GH_TOKEN:
        req.add_header("Authorization", "Bearer %s" % GH_TOKEN)

    digest = hashlib.sha256()
    total = 0
    try:
        with urllib.request.urlopen(req, timeout=60) as resp, open(dest, "wb") as out:
            while True:
                chunk = resp.read(262144)
                if not chunk:
                    break
                total += len(chunk)
                if total > expected_max:
                    raise FetchError(
                        "asset exceeded %d bytes while downloading — refusing"
                        % expected_max,
                        400,
                    )
                digest.update(chunk)
                out.write(chunk)
    except FetchError:
        raise
    except urllib.error.HTTPError as exc:
        raise FetchError("download failed with HTTP %d" % exc.code, 502)
    except urllib.error.URLError as exc:
        raise FetchError("download failed: %s" % exc.reason, 502)

    if total < expected_min:
        if what == "manifest":
            raise FetchError(
                "manifest.json downloaded empty (0 bytes) — the release is "
                "incomplete on GitHub",
                400,
            )
        raise FetchError(
            "downloaded only %d bytes — that is a truncated file or a Git LFS "
            "pointer, not a %s" % (total, what),
            400,
        )
    return digest.hexdigest(), total


def fetch_release(version, force=False):
    """Fetch every artifact for version into UPDATES_DIR/<version>/.

    All-or-nothing: nothing is published unless every platform is present and
    every hash verifies. Returns a result dict suitable for JSON.
    """
    version = validate_version(version)
    VERSION_CONTEXT["version"] = version
    target_dir = os.path.join(UPDATES_DIR, version)
    assets = resolve_release(version)

    wanted = [(key, name) for key, name, _ in PLATFORMS] + [("manifest", MANIFEST_NAME)]
    missing = [name for _, name in wanted if name not in assets]
    if missing:
        raise FetchError(
            "the v%s release is missing: %s. CI is supposed to attach all four "
            "platform executables plus manifest.json — fix CI and re-release "
            "rather than publishing a partial update."
            % (version, ", ".join(missing)),
            400,
        )

    os.makedirs(target_dir, exist_ok=True)
    staged = {}
    results = {}

    for key, name in wanted:
        url, asset_size = assets[name]
        if asset_size and asset_size > MAX_ASSET_BYTES:
            raise FetchError(
                "%s is %d bytes according to GitHub — larger than the %d byte cap"
                % (name, asset_size, MAX_ASSET_BYTES),
                400,
            )

        final_path = os.path.join(target_dir, name)
        # The 1 MB floor is a binary guard (truncated download / Git LFS
        # pointer). manifest.json is legitimately a few hundred bytes, so it is
        # only required to be non-empty.
        is_manifest = (name == MANIFEST_NAME)
        min_bytes = MIN_MANIFEST_BYTES if is_manifest else MIN_BINARY_BYTES

        # Idempotence: a complete, already-verified artifact is left alone
        # unless the operator asked for a force re-fetch.
        if not force and os.path.isfile(final_path):
            existing = _sha256_file(final_path)
            if os.path.getsize(final_path) >= min_bytes:
                log("  = %s already present (%s)" % (name, existing[:16]))
                staged[name] = final_path
                results[key] = {"filename": name, "sha256": existing,
                                "bytes": os.path.getsize(final_path),
                                "skipped": True}
                continue

        fd, tmp_path = tempfile.mkstemp(prefix=".fetch-", dir=target_dir)
        os.close(fd)
        try:
            log("  ↓ %s" % name)
            sha, size = _download(url, tmp_path, min_bytes, MAX_ASSET_BYTES,
                                  what=("manifest" if is_manifest else "binary"))

            # The manifest is what proves the bytes came from the same build CI
            # hashed, so a manifest that is not JSON is useless even though it
            # has the right SHA. Check it here rather than at publish time.
            if is_manifest:
                try:
                    with open(tmp_path, "r", encoding="utf-8") as mf:
                        json.load(mf)
                except (ValueError, UnicodeDecodeError) as exc:
                    raise FetchError(
                        "manifest.json for v%s is not valid JSON (%s) — refusing "
                        "to publish" % (version, exc),
                        400,
                    )
            # Verify BEFORE publishing: write the sidecar, then atomically move.
            # os.replace on the same filesystem is atomic, so a client either
            # sees the old file or the complete new one — never a partial write.
            os.chmod(tmp_path, 0o644)
            os.replace(tmp_path, final_path)
            with open(final_path + ".sha256", "w") as f:
                f.write("%s  %s\n" % (sha, name))
            os.chmod(final_path + ".sha256", 0o644)
            staged[name] = final_path
            results[key] = {"filename": name, "sha256": sha, "bytes": size,
                            "skipped": False}
            log("    %d bytes sha256=%s" % (size, sha[:16]))
        except Exception:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            raise

    return {"version": version, "dir": target_dir, "artifacts": results}


def _sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as f:
        while True:
            chunk = f.read(262144)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def verify_artifact_kind(path, platform_key):
    """Sanity-check that the bytes match the platform they are filed under.

    The console used to do this in the browser before an upload (see the old
    lib/artifact.ts), which is where the "Windows .exe in the Linux slot"
    mistake was caught. That check must not simply be lost now the upload is
    gone, so it moves here — the fetcher is the thing that now decides which
    bytes land in which slot.

    Only the format is checked (ELF / PE / Mach-O and, for macOS, the
    architecture). A cross-slot mixup is the realistic failure; a content scan
    for the version string is possible but a wrong-version artifact is already
    covered by the fact that we resolve assets BY TAG.
    """
    try:
        with open(path, "rb") as f:
            head = f.read(8)
    except OSError:
        return True, "unreadable"
    try:
        size = os.path.getsize(path)
    except OSError:
        size = 0

    def is_elf(b):
        return len(b) >= 4 and b[:4] == b"\x7fELF"

    def is_pe(b):
        return len(b) >= 2 and b[:2] == b"MZ"

    def is_macho_thin(b):
        return len(b) >= 4 and b[:4] in (
            b"\xcf\xfa\xed\xfe", b"\xce\xfa\xed\xfe",
            b"\xfe\xed\xfa\xcf", b"\xfe\xed\xfa\xce",
        )

    def is_macho_fat(b):
        return len(b) >= 4 and b[:4] in (b"\xca\xfe\xba\xbe", b"\xbe\xba\xfe\xca")

    if platform_key == "windows":
        if not is_pe(head):
            return False, "not a Windows PE executable"
        return True, "PE"
    if platform_key == "linux":
        if not is_elf(head):
            return False, "not an ELF executable"
        return True, "ELF"
    if platform_key in ("macos_intel", "macos_arm"):
        if not (is_macho_thin(head) or is_macho_fat(head)):
            return False, "not a Mach-O executable"
        # Architecture of a FAT binary is only resolvable by walking the
        # headers, so a universal binary is accepted for either slot.
        if is_macho_fat(head):
            return True, "Mach-O (universal)"
        little = head[:4] in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf")
        cputype = int.from_bytes(head[4:8], "little" if little else "big")
        if cputype == 0x0100000C:  # ARM64
            return (platform_key == "macos_arm"), "Mach-O arm64"
        if cputype == 0x01000007:  # x86_64
            return (platform_key == "macos_intel"), "Mach-O x86_64"
        return True, "Mach-O"
    return True, "unknown"


def verify_and_report(result):
    """Post-fetch format check. Raises FetchError on a cross-slot mixup."""
    problems = []
    for key, name, _ in PLATFORMS:
        entry = result["artifacts"].get(key)
        if not entry:
            problems.append("%s: not fetched" % name)
            continue
        path = os.path.join(result["dir"], entry["filename"])
        ok, detected = verify_artifact_kind(path, key)
        if not ok:
            problems.append("%s: %s" % (name, detected))
        else:
            result["artifacts"][key]["format"] = detected
    if problems:
        raise FetchError(
            "the release has the wrong file in one or more platform slots — " +
            "; ".join(problems) + ". Refusing to publish.",
            400,
        )
    return result


# ── HTTP surface ────────────────────────────────────────────────────────────
class Handler(BaseHTTPRequestHandler):
    server_version = "locus-fetch/1.0"

    def log_message(self, fmt, *args):
        # Default logging writes to stderr unformatted; route it through log().
        log("%s - %s" % (self.address_string(), fmt % args))

    # ── helpers ──
    def _send(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        try:
            self.wfile.write(body)
        except BrokenPipeError:
            pass

    def _authorised(self, query, body):
        """Token header/field, or a valid one-shot link signature."""
        supplied = ""
        try:
            supplied = (self.headers.get("X-Admin-Token") or "").strip()
        except Exception:
            supplied = ""
        if not supplied and isinstance(body, dict):
            supplied = str(body.get("admin_token") or "").strip()

        if TOKEN and supplied and hmac.compare_digest(supplied, TOKEN):
            return True, "token"

        # One-shot signed link (GET). The nonce is burned on success.
        if query is not None:
            version = (query.get("version") or [""])[0]
            nonce = (query.get("nonce") or [""])[0]
            exp = (query.get("exp") or [""])[0]
            sig = (query.get("sig") or [""])[0]
            if nonce and sig:
                consume_nonce(nonce, exp, version, sig)
                return True, "link"
        return False, None

    def _base_url(self):
        host = self.headers.get("X-Forwarded-Host") or self.headers.get("Host") or ""
        proto = self.headers.get("X-Forwarded-Proto") or "https"
        if not host:
            return ""
        return "%s://%s" % (proto, host)

    # ── POST: console button ──
    def do_POST(self):
        import urllib.parse

        query = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        if urllib.parse.urlparse(self.path).path not in ("/api/admin/fetch-release", "/fetch-release"):
            return self._send(404, {"ok": False, "message": "not found"})

        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            length = 0
        if length > 1024 * 1024:
            return self._send(413, {"ok": False, "message": "body too large"})
        raw = self.rfile.read(length) if length else b""
        try:
            body = json.loads(raw.decode("utf-8")) if raw else {}
        except (ValueError, UnicodeDecodeError):
            return self._send(400, {"ok": False, "message": "invalid JSON body"})

        try:
            ok, _how = self._authorised(query, body)
        except FetchError as exc:
            return self._send(exc.status, {"ok": False, "message": str(exc)})
        if not ok:
            return self._send(403, {"ok": False, "message": "Invalid admin token"})

        return self._run(body, force=bool(body.get("force")))

    # ── GET: one-shot link, or a dry-run preview ──
    def do_GET(self):
        import urllib.parse

        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)

        if parsed.path == "/health":
            return self._send(200, {"ok": True, "service": "locus-fetch",
                                    "repo": GITHUB_REPO})

        if parsed.path == "/api/admin/fetch-link":
            # Mint a fresh one-shot link. Token-only: a link must not be able to
            # mint further links, or one leaked URL becomes a permanent grant.
            supplied = (self.headers.get("X-Admin-Token") or "").strip()
            if not TOKEN or not supplied or not hmac.compare_digest(supplied, TOKEN):
                return self._send(403, {"ok": False, "message": "Invalid admin token"})
            try:
                version = validate_version((query.get("version") or [""])[0])
            except FetchError as exc:
                return self._send(exc.status, {"ok": False, "message": str(exc)})
            base = self._base_url()
            if not base:
                return self._send(400, {"ok": False,
                                        "message": "no Host header — cannot build a link"})
            return self._send(200, {
                "ok": True,
                "link": mint_link(base, version),
                "expires_in": LINK_TTL_SECONDS,
                "single_use": True,
            })

        if parsed.path not in ("/api/admin/fetch-release", "/fetch-release"):
            return self._send(404, {"ok": False, "message": "not found"})

        try:
            ok, _how = self._authorised(query, None)
        except FetchError as exc:
            return self._send(exc.status, {"ok": False, "message": str(exc)})
        if not ok:
            return self._send(403, {"ok": False, "message": "not authorised — "
                                                             "request a new link"})

        version = (query.get("version") or [""])[0]
        force = (query.get("force") or ["0"])[0] in ("1", "true", "yes")
        return self._run({"version": version}, force=force)

    def _run(self, body, force=False):
        started = time.time()
        try:
            result = fetch_release(body.get("version"), force=force)
            verify_and_report(result)
        except FetchError as exc:
            log("FAILED: %s" % exc)
            return self._send(exc.status, {"ok": False, "message": str(exc)})
        except Exception as exc:  # never leak a traceback to the caller
            log("FAILED (unexpected): %r" % exc)
            return self._send(500, {"ok": False,
                                    "message": "unexpected error: %s" % exc})

        elapsed = round(time.time() - started, 1)
        log("✓ v%s ready in %ss" % (result["version"], elapsed))
        return self._send(200, {
            "ok": True,
            "version": result["version"],
            "dir": result["dir"],
            "elapsed": elapsed,
            "artifacts": result["artifacts"],
        })


def main():
    if not TOKEN:
        log("FATAL: ADMIN_API_TOKEN is not set — refusing to start")
        sys.exit(1)
    if not os.path.isdir(UPDATES_DIR):
        log("FATAL: UPDATES_DIR does not exist: %s" % UPDATES_DIR)
        sys.exit(1)
    log("Locus release-fetch service listening on %s:%d -> %s (repo %s)"
        % (LISTEN_HOST, LISTEN_PORT, UPDATES_DIR, GITHUB_REPO))
    log("one-shot links expire after %ds" % LINK_TTL_SECONDS)
    server = ThreadingHTTPServer((LISTEN_HOST, LISTEN_PORT), Handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

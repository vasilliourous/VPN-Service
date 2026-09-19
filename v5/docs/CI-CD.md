# Locus V5 — CI/CD Pipeline Reference

> The Locus client is built and released via GitHub Actions.
> This document describes the pipeline, how to trigger releases,
> and how to interpret build artifacts.

---

## Pipeline Overview

```
Push to main / PR → Lint & Vet (ubuntu-latest)
                        ↓
               Build Matrix (4 targets in parallel)
     ┌────────────┬──────────────┬────────────┬────────────┐
     │  Linux     │   Windows    │ macOS      │ macOS      │
     │  amd64     │   amd64      │ amd64      │ arm64      │
     └─────┬──────┴──────┬───────┴─────┬──────┴─────┬──────┘
           ↓             ↓             ↓            ↓
     ┌─────────────────────────────────────────────────────┐
     │                  GitHub Release (tag v* only)        │
     │  zips + raw per-platform executables + manifest.json │
     │                + checksums.sha256                    │
     └─────────────────────────────────────────────────────┘
                        ↓
     publish-release.sh → hub /updates/<version>/ → update_config
```

**File:** `.github/workflows/build.yml` (repository ROOT — GitHub Actions only executes workflows from the root `.github/workflows/`; the old `v5/.github/` copy was deleted in the 2026-08 cleanup)

> **⚠️ IMPORTANT — Frontend build ordering & Wails tags.** The client embeds the
> Vue frontend via `//go:embed all:frontend/dist` in `v5/client/assets_embed.go`
> (build tag `frontend`). Every CI job builds the frontend first and compiles
> with the full Wails tag set:
>
> ```bash
> cd v5/client/frontend && npm install && npm run build
> cd v5/client && go build -tags "frontend desktop production" .
> ```
> `desktop` and `production` are **required Wails build tags** — they select the
> real desktop implementation. Without them the binary compiles but is the stub
> app that shows the "Wails applications will not build without the correct
> build tags" error dialog at runtime. (`wails build` adds them automatically;
> this workflow uses raw `go build`, so they must be passed explicitly.)
> **Linux only:** add `webkit2_41` — ubuntu-latest ships WebKitGTK 4.1, and the
> Wails Linux cgo code selects the webkit version via this build tag (without
> it, cgo looks for `webkit2gtk-4.0`, which is not available on 24.04).
> Without the `frontend` tag, Go compiles `assets_stub.go` (empty asset FS) —
> the binary builds but has no UI.

---

## Triggering a Release

Simply push a tag starting with `v`:

```bash
git tag v2.0.0
git push origin v2.0.0
```

The pipeline will:
1. Lint and vet all Go code (and build the admin console, to catch a broken
   `/admin/` base before deploy)
2. Build the Vue frontend, then the client — 4 platform targets in parallel
3. Download the matching sing-box engine binary (1.12.1) for each
4. Bundle into 4 platform ZIPs
5. Publish the raw per-platform executables + `manifest.json` + checksums,
   then create a GitHub Release

---

## Build Artifacts

Each release produces **4 platform bundles** (for humans/website downloads):

| File | Platform | Contents |
|------|----------|----------|
| `locus-Linux-amd64.zip` | Linux x86_64 | `locus` + `sing-box` |
| `locus-Windows-amd64.zip` | Windows x86_64 | `locus.exe` + `sing-box.exe` |
| `locus-macOS-amd64.zip` | macOS Intel | `locus` + `sing-box` |
| `locus-macOS-arm64.zip` | macOS Apple Silicon | `locus` + `sing-box` |

The zips contain only the two binaries; the release job also generates a
`checksums.sha256` file (SHA256 of each zip).

### Raw artifacts (what the auto-updater consumes)

Alongside the zips, CI publishes the **raw executables** and a manifest:

| File | Maps to platform |
|------|------------------|
| `locus-linux-amd64` | `linux` |
| `locus-windows-amd64.exe` | `windows` |
| `locus-darwin-amd64` | `macos_intel` |
| `locus-darwin-arm64` | `macos_arm` |
| `manifest.json` | version → filename + SHA256 per platform |

Every one also gets a `.sha256` companion. **The build fails if any platform
artifact is missing** — a release that silently omits a platform would leave
those clients unable to update.

This distinction is load-bearing: the in-app updater **replaces the app binary
in place and cannot unpack a zip**. Feeding it a zip would fail checksum
verification. Publishing to the hub is done with
`v5/server/scripts/publish-release.sh`, which uploads to a temp name, moves it
atomically on the server, then re-downloads each artifact to confirm the served
bytes hash correctly before writing `update_config`.

---

## Local Build (without CI)

For development builds without the full CI pipeline:

```bash
# Build for current platform
cd v5/client && make build

# Build everything locally
cd v5/client && make build-all

# Run quality checks
cd v5/client && make vet && make test
```

---

## Pipeline Jobs

### `lint` (required)

- Installs Linux WebView deps (`libgtk-3-dev`, `libwebkit2gtk-4.1-dev`,
  `libayatana-appindicator3-dev` — the last one provides the pkg-config file
  `getlantern/systray` compiles against)
- Builds the Vue frontend **and** the admin console (with a guard that
  `dist/index.html` references `/admin/assets/`)
- Runs `golangci-lint` with default linters
- Runs `go vet -tags "frontend desktop production webkit2_41" ./...`
- Runs `go build -tags "frontend desktop production webkit2_41" ./...`
- Runs `go test ./... -v -count=1`

> CI **builds** the admin console but does not publish it. The console is an
> operator-only tool, not a client artifact — deploy it with
> `v5/server/scripts/deploy-console.sh`. CI builds it purely so a change that
> breaks the build (or the `/admin/` base path) is caught before deploy rather
> than when an operator opens the page.

### `build` (matrix, 4 parallel)

Each platform build:
1. Sets `GOOS`/`GOARCH` appropriate for the target
2. Builds the main client binary with version ldflags
3. Downloads the correct sing-box release
4. Zips the 2 binaries into a platform bundle
5. Also emits a `.sha256` per artifact and a combined `manifest.json`, and
   **fails the build if any platform artifact is missing**

### `release` (tag only)

- Collects all 4 platform artifacts + raw executables + manifest
- Generates `checksums.sha256`
- Creates a GitHub Release with release notes (including the macOS Gatekeeper
  warning, since macOS builds are unsigned)

---

## Environment Variables

The workflow does not define configurable environment variables — values are
set inline per job:

| Value | Where |
|-------|-------|
| `VERSION` | Derived from the tag (`github.ref_name`, leading `v` stripped), falling back to `"dev"`; passed via `-X main.version` |
| `SING_BOX_VERSION` | Hardcoded as `1.12.1` in the "Download sing-box engine" step |
| `CGO_ENABLED` | `1` for Linux and macOS (Wails WebView), `0` for Windows (WebView2 COM, pure Go) |
| `CLIENT_DIR` / `FRONTEND_DIR` | `v5/client` / `v5/client/frontend` |

---

## Adding a New Platform

The current matrix is:

| Label | Runner | GOOS | GOARCH | CGO | Tags |
|-------|--------|------|--------|:---:|------|
| `Linux-amd64` | ubuntu-latest | linux | amd64 | 1 | `frontend desktop production webkit2_41` |
| `macOS-amd64` | macos-latest | darwin | amd64 | 1 | `frontend desktop production` |
| `macOS-arm64` | macos-latest | darwin | arm64 | 1 | `frontend desktop production` |
| `Windows-amd64` | windows-latest | windows | amd64 | 0 | `frontend desktop production` |

To add a new build target (e.g., `linux/arm64`), add a new entry to the
`matrix.include` block in `.github/workflows/build.yml`:

```yaml
- label: Linux-arm64
  os: ubuntu-latest
  goos: linux
  goarch: arm64
  ext: ""
  cgo: "1"
  tags: "frontend desktop production webkit2_41"
```

Then:
1. Add the sing-box download URL for that platform in the "Download sing-box
   engine" step.
2. **Add the raw artifact to `manifest.json`** and the platform→filename map.
   If you skip this the build intentionally fails — a platform in the matrix
   but not in the manifest means those clients cannot update.
3. Add the platform column (`download_<platform>`, `sha256_<platform>`) to the
   `update_config` schema, and the matching field to the heartbeat response in
   `pb_hooks/heartbeat.pb.js` plus `updater.PlatformDownloadURL()/PlatformSHA256()`.

---

## Troubleshooting

### Build fails with "CGO required"

Wails requires CGO on Linux (webview embed). Windows builds with
`CGO_ENABLED=0` (pure Go, WebView2 COM) — no MinGW needed in CI. Ensure:
- Linux: `sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev` (Ubuntu 24.04+; older Ubuntu 22.04 uses `libwebkit2gtk-4.0-dev`)
- Windows: no extra toolchain (CI); local `wails build` for Windows from Linux may need MinGW-w64

### Lint fails on unused imports

Run `go vet ./...` locally and fix any issues before pushing.

### Release not created

Check:
- Tag must start with `v` (e.g., `v2.0.0`, not `release-2.0.0`)
- GitHub Actions must have `contents: write` permission
- The tag must be pushed to the default branch

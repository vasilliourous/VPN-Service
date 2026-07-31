# MyVPN V5 — CI/CD Pipeline Reference

> The MyVPN client is built and released via GitHub Actions.
> This document describes the pipeline, how to trigger releases,
> and how to interpret build artifacts.

---

## Pipeline Overview

```
Push to main / PR → Lint & Vet (ubuntu-latest)
                        ↓
               Build Matrix (4 platforms in parallel)
              ┌───────┬───────┬───────┬───────┐
              │ Linux │macOS  │macOS  │Windows│
              │ amd64 │ amd64 │ arm64 │ amd64 │
              └───┬───┴───┬───┴───┬───┴───┬───┘
                  ↓       ↓       ↓       ↓
              ┌───────────────────────────────┐
              │       GitHub Release           │
              │  (tag pushes only: v*)         │
              └───────────────────────────────┘
```

**File:** `.github/workflows/build.yml` (repository ROOT — GitHub Actions only executes workflows from the root `.github/workflows/`; a stale copy under `v5/.github/` is inert and must not be edited)

> **⚠️ IMPORTANT — Frontend build ordering.** The client embeds the Vue frontend
> via `//go:embed all:frontend/dist` in `v5/client/assets_embed.go` (build tag
> `frontend`). Every CI job builds the frontend first and compiles with
> `-tags frontend`:
>
> ```bash
> cd v5/client/frontend && npm install && npm run build
> cd v5/client && go build -tags frontend .
> ```
>
> Without the `frontend` build tag, Go compiles `assets_stub.go` (empty asset FS)
> and the binary builds fine but shows no UI — so CI always passes the tag.
> `go build .` without the tag still compiles (useful for headless/CI-less checks).

---

## Triggering a Release

Simply push a tag starting with `v`:

```bash
git tag v2.0.0
git push origin v2.0.0
```

The pipeline will:
1. Lint and vet all Go code
2. Build for all 4 platform targets in parallel
3. Download the matching sing-box engine binary for each
4. Bundle into platform ZIPs
5. Create a GitHub Release with all artifacts + SHA256 checksums

---

## Build Artifacts

Each release produces 4 platform bundles:

| File | Platform | Contents |
|------|----------|----------|
| `myvpn-Linux-amd64.zip` | Linux x86_64 | `myvpn` + `sing-box` |
| `myvpn-macOS-amd64.zip` | macOS Intel | `myvpn` + `sing-box` |
| `myvpn-macOS-arm64.zip` | macOS Apple Silicon | `myvpn` + `sing-box` |
| `myvpn-Windows-amd64.zip` | Windows x86_64 | `myvpn.exe` + `sing-box.exe` |

Each bundle also includes a `checksums.sha256` file for integrity verification.

---

## Local Build (without CI)

For development builds without the full CI pipeline:

```bash
# Build for current platform
cd v5/client && make build

# Build everything locally
cd v5/client && make bundle-all

# Run quality checks
cd v5/client && make vet && make test
```

---

## Pipeline Jobs

### `lint` (required)

- Runs `golangci-lint` with default linters
- Runs `go vet ./...` for static analysis
- Runs `go build ./...` to verify compilation
- Runs `go test ./... -v -count=1` for unit tests

### `build` (matrix, 4 parallel)

Each platform build:
1. Sets `GOOS`/`GOARCH` appropriate for the target
2. Builds the main client binary with version ldflags
3. Builds the TUN helper (no CGO)
4. Downloads the correct sing-box release
5. Zips the 3 binaries into a platform bundle

### `release` (tag only)

- Downloads all 4 platform artifacts
- Generates SHA256 checksums
- Creates a GitHub Release with release notes

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VERSION` | tag name or "dev" | Sets the `main.version` ldflag |
| `SING_BOX_VERSION` | 1.10.0 | sing-box engine version to bundle |

---

## Adding a New Platform

To add a new build target (e.g., `linux/arm64`), add a new entry to the `matrix.include` block in `.github/workflows/build.yml`:

```yaml
- label: Linux-arm64
  os: ubuntu-latest
  goos: linux
  goarch: arm64
  ext: ""
```

Then add the sing-box download URL for that platform in the "Download sing-box engine" step.

---

## Troubleshooting

### Build fails with "CGO required"

Fyne requires CGO. Ensure:
- Linux: `sudo apt install libgl1-mesa-dev xorg-dev`
- macOS: Xcode Command Line Tools
- Windows: MinGW-w64 (`x86_64-w64-mingw32-gcc`)

### Lint fails on unused imports

Run `go vet ./...` locally and fix any issues before pushing.

### Release not created

Check:
- Tag must start with `v` (e.g., `v2.0.0`, not `release-2.0.0`)
- GitHub Actions must have `contents: write` permission
- The tag must be pushed to the default branch

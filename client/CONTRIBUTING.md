# Contributing — Locus client

This directory is a **fork of Clash Verge Rev v2.5.5**, adapted into the Locus
desktop client. It is the shipping Locus client. The predecessor Wails client is
archived at `legacy/wails-client/` and is not the product.

Agent-facing rules live in [`AGENTS.md`](AGENTS.md); read that first if you are
an AI agent. This file is the human-facing setup and submission guide.

## Scope

This fork is `UNLICENSED` and derived from GPL-3.0-only upstream. Do not assume
the inherited `LICENSE` describes this tree; licence obligations for distribution
are unresolved and must be settled before any release.

Upstream's contribution process (issue-first gating, the `ai-slop` review
workflow, signed-commit requirements) does **not** apply here — those files were
removed from the fork. Practically, that means:

- No issue-first requirement. Describe the problem in the commit message.
- No automated PR screening, and no required commit signing.
- Keep diffs minimal and mapped to a real problem. No drive-by refactors,
  renames, formatting churn, or dependency bumps.
- Do not pad with tests or speculative defensive code. Add a test when it
  reproduces a real regression or guards behaviour whose breakage would
  otherwise go unnoticed, and say why in the commit.

## Internationalization (i18n)

For contributing translations, see [docs/CONTRIBUTING_i18n.md](docs/CONTRIBUTING_i18n.md).

> **Note:** the fork still contains inherited `Clash Verge` strings, mostly in
> `src/locales/`. Several name the privileged system service in user-facing error
> messages. Do not blind find-replace them — a message a student cannot act on is
> worse than a stale brand name. See `docs/ARCHITECTURE.md`.

## Development Setup

Before contributing, you need to set up your development environment. Follow the steps below carefully.

### Prerequisites

1. **Install Rust and Node.js**  
   Our project requires both Rust and Node.js. Follow the official installation instructions [here](https://tauri.app/start/prerequisites/).

### Windows Users

> [!NOTE]  
> **Windows ARM users must also install [LLVM](https://github.com/llvm/llvm-project/releases) (including clang) and set the corresponding environment variables.**  
> The `ring` crate depends on `clang` when building on Windows ARM.

Additional steps for Windows:

- Ensure Rust and Node.js are added to your system `PATH`.

- Install the GNU `patch` tool.

- Use the MSVC toolchain for Rust:

```bash
rustup target add x86_64-pc-windows-msvc
rustup set default-host x86_64-pc-windows-msvc
```

### Install Node.js Package Manager

Enable `corepack`:

```bash
corepack enable
```

### Install Project Dependencies

Node.js dependencies:

```bash
pnpm install
```

Ubuntu-only system packages:

```bash
sudo apt-get install -y libxslt1.1 libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev patchelf
```

### Download the Mihomo Core Binary (Automatic)

```bash
pnpm run prebuild
pnpm run prebuild --force  # Re-download and overwrite Mihomo core and service binaries
```

### Run the Development Server

```bash
pnpm dev           # Standard
pnpm dev:diff      # If an app instance already exists
pnpm dev:tauri     # Run Tauri development mode
```

### Build the Project

Standard build:

```bash
pnpm build
```

Fast build for testing:

```bash
pnpm build:fast
```

### Clean Build

```bash
pnpm clean
```

### Portable Version (Windows Only)

```bash
pnpm portable
```


## Contributing Your Changes

### Before Committing

**Code quality checks:**

```bash
# Rust backend
cargo clippy-all
# Frontend
pnpm lint
```

**Code formatting:**

```bash
# Rust backend
cargo fmt
# Frontend
pnpm format
```

### Submitting Your Changes

This is a private, single-maintainer repository — there is no public fork-and-PR
process. Branch, commit with a clear message, and push. The repository root
`README.md` describes the release path.

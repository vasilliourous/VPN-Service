#!/usr/bin/env bash
# make-dmg.sh — package the macOS build as a Locus.app bundle inside a .dmg.
#
# WHY THIS EXISTS
#
# macOS shipped the client as a bare binary inside a zip, which left three
# problems that all trace to the same root:
#
#   1. There was no install location. internal/install checks for
#      /Applications/Locus.app, and without a bundle that check can never
#      match — so every macOS copy was "portable" and updated in place wherever
#      the user unzipped it (~/Downloads, usually).
#   2. A bare binary has no Info.plist, so macOS has no idea what it is. It is
#      treated as an unidentified executable rather than an application, which
#      makes the Gatekeeper warning worse, not better.
#   3. A .app is the unit macOS replaces. Updating the leaf binary alone would
#      leave the bundle's Info.plist and resources describing the old version.
#
# No signing is performed, and this is a known, documented gap: Gatekeeper will
# block the first launch of the downloaded app. The DMG includes a README
# explaining the right-click → Open workaround. Signing is tracked separately
# and deliberately NOT invented here.
#
# The zip with the bare binary keeps shipping alongside, so an existing
# portable user is not broken by this change.
#
# Usage: make-dmg.sh <version> <arch> <path-to-locus-binary> <path-to-sing-box> [outdir]
set -euo pipefail

VERSION="${1:?usage: make-dmg.sh <version> <arch> <locus-binary> <sing-box> [outdir]}"
ARCH="${2:?missing arch}"
LOCUS_BIN="${3:?missing locus binary}"
SINGBOX_BIN="${4:?missing sing-box binary}"
OUTDIR="${5:-dist}"

APP_NAME="Locus"
BUNDLE="${OUTDIR}/${APP_NAME}.app"
DMG_NAME="locus-setup-${VERSION}-macos-${ARCH}"
STAGE="${OUTDIR}/dmg-stage"

log() { printf '  %s\n' "$*"; }

[ -f "$LOCUS_BIN" ]   || { echo "ERROR: locus binary not found: $LOCUS_BIN" >&2; exit 1; }
[ -f "$SINGBOX_BIN" ] || { echo "ERROR: sing-box binary not found: $SINGBOX_BIN" >&2; exit 1; }

log "Building ${APP_NAME}.app (${VERSION}, ${ARCH})"

# ── Bundle skeleton ──────────────────────────────────────────────────────────
# The layout is the fixed, documented macOS structure:
#   Locus.app/Contents/MacOS/locus          the executable
#   Locus.app/Contents/Info.plist           identity
#   Locus.app/Contents/Resources/           bundled assets
# internal/install resolves the executable inside Contents/MacOS and treats the
# bundle root as the unit to replace, so this layout is load-bearing.
rm -rf "$BUNDLE" "$STAGE"
mkdir -p "${BUNDLE}/Contents/MacOS" "${BUNDLE}/Contents/Resources"

cp "$LOCUS_BIN" "${BUNDLE}/Contents/MacOS/locus"
chmod 0755 "${BUNDLE}/Contents/MacOS/locus"

# sing-box sits next to the client because findSingBox() looks alongside the
# executable first (see v5/client/app.go). Bundling it matters for the same
# reason as on Windows: the target network blocks downloads, so a runtime fetch
# would fail exactly where the app is needed.
cp "$SINGBOX_BIN" "${BUNDLE}/Contents/MacOS/sing-box"
chmod 0755 "${BUNDLE}/Contents/MacOS/sing-box"

# ── Info.plist ───────────────────────────────────────────────────────────────
# Written as a heredoc rather than a checked-in binary/plist-edited file so the
# version cannot drift: it comes from v5/VERSION via CI, the same source the Go
# binary and the Windows .syso are built from.
cat > "${BUNDLE}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>
    <string>com.locus.client</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundleExecutable</key>
    <string>locus</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleSignature</key>
    <string>????</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <!-- Regular app: it has a window and a Dock presence. A background-only
         bundle would be invisible and unquittable, and the client has no tray
         icon on macOS (Wails v2 has no tray API). -->
    <key>LSUIElement</key>
    <false/>
    <!-- The client creates a TUN interface via sing-box, which needs elevated
         privileges on macOS. There is no signing identity, so this is the
         right-click-to-open case rather than a notarized one. -->
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

plutil -lint "${BUNDLE}/Contents/Info.plist" >/dev/null || {
  echo "ERROR: generated Info.plist is not valid" >&2
  exit 1
}
log "Info.plist valid"

# ── Ad-hoc signature ─────────────────────────────────────────────────────────
# NOT a real signature and NOT a substitute for Developer ID signing +
# notarization. It signs the bundle so its own helper binaries are not treated
# as modified after install, which otherwise produces "Locus is damaged and
# can't be opened" instead of the far more accurate "unidentified developer".
# Best-effort: a failure here must not fail the build, since the output is
# still usable via right-click → Open.
if codesign --force --deep --sign - "$BUNDLE" 2>/dev/null; then
  log "ad-hoc signed (Gatekeeper will still warn — no Developer ID)"
else
  log "ad-hoc signing skipped (codesign unavailable); Gatekeeper warning expected"
fi

# ── Assembly notes ───────────────────────────────────────────────────────────
# The DMG carries an explanation, because an unsigned app in a DMG looks
# broken rather than merely untrusted, and the workaround is not obvious.
mkdir -p "$STAGE"
cp -R "$BUNDLE" "$STAGE/"
cat > "${STAGE}/READ ME FIRST.txt" <<'NOTES'
Locus — first launch on macOS
=============================

This build is NOT signed with an Apple Developer ID, so macOS Gatekeeper
will refuse to open it the first time. This is expected and is not a virus
warning; it means Apple has not been paid to vouch for the app.

To open it:

  1. Drag "Locus.app" into your Applications folder.
  2. In Applications, right-click (or Control-click) Locus and choose Open.
  3. Click Open in the dialog.

You only need to do this once. After that, Locus opens normally.

If macOS instead says the app "is damaged and can't be opened", run this in
Terminal and then repeat the steps above:

  xattr -cr /Applications/Locus.app

Why it is unsigned
------------------
Signing requires a paid Apple Developer account. It is tracked as a known
gap and will be handled separately.
NOTES

# A shortcut so the disk image has the usual drag-to-install target. A symlink
# is what Finder renders as the Applications folder icon.
ln -s /Applications "${STAGE}/Applications"

# ── Build the image ──────────────────────────────────────────────────────────
# UDZO (zlib) rather than LZFSE: it is universally readable by older macOS
# versions, and the size difference on a ~20MB payload is negligible.
rm -f "${OUTDIR}/${DMG_NAME}.dmg"
hdiutil create \
  -volname "${APP_NAME}" \
  -srcfolder "$STAGE" \
  -ov -format UDZO \
  "${OUTDIR}/${DMG_NAME}.dmg" >/dev/null

log "Created ${OUTDIR}/${DMG_NAME}.dmg ($(du -h "${OUTDIR}/${DMG_NAME}.dmg" | cut -f1))"

# Clean up the staging area; the bundle inside the dmg is the deliverable, and
# leaving Locus.app loose in dist/ would confuse the release upload globbing.
rm -rf "$STAGE" "$BUNDLE"

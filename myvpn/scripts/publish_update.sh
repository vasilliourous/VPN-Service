#!/bin/bash
# publish_update.sh — Compute SHA256 and output update JSON
# Usage: ./publish_update.sh <binary> <version> <platform>
set -euo pipefail

BINARY="$1"
VERSION="$2"
PLATFORM="$3"

if [ -z "$BINARY" ] || [ -z "$VERSION" ] || [ -z "$PLATFORM" ]; then
    echo "Usage: $0 <binary> <version> <platform>"
    echo "Example: $0 myvpn.exe 1.0.1 windows_amd64"
    exit 1
fi

if [ ! -f "$BINARY" ]; then
    echo "Error: binary not found: $BINARY"
    exit 1
fi

SHA256=$(sha256sum "$BINARY" | cut -d' ' -f1)
FILENAME=$(basename "$BINARY")

cat << JSONEOF
{
    "version": "${VERSION}",
    "platform": "${PLATFORM}",
    "url": "https://api.yourdomain.com/updates/${FILENAME}",
    "sha256": "${SHA256}",
    "min_version": "1.0.0"
}
JSONEOF

echo ""
echo "Copy ${BINARY} to /var/www/updates/${FILENAME} on the server." >&2
echo "Then send the update command via heartbeat or admin UI." >&2

#!/usr/bin/env bash
# Module 03: tcp-brutal Kernel Module + LD_PRELOAD Wrapper
#
# IMPORTANT: Brutal CC runs on the SERVER side (Linux VPS) only.
# It changes the server's TCP congestion control to aggressively
# fill bandwidth for the Stealth tier (port 8444).
#
# The LD_PRELOAD wrapper intercepts accept() calls and sets
# TCP_CONGESTION=brutal on each accepted client connection.
# This is necessary because ssserver does not set TCP_CONGESTION
# itself — it uses the system default.
#
# The tcp-brutal kernel module registers "brutal" as a congestion
# control algorithm in the Linux TCP stack.
#
# Repository: https://github.com/apernet/tcp-brutal
# NOTE: Was previously at apernet/tcp-brutal-ng (renamed/rewritten).
set -euo pipefail

log()  { echo "[03-brutal] $*"; }
warn() { echo "[03-brutal][WARN] $*"; }
fail() { echo "[03-brutal][FAIL] $*"; exit 1; }

BRUTAL_REPO="https://github.com/apernet/tcp-brutal.git"
BRUTAL_SRC="/usr/local/src/tcp-brutal"
BRUTAL_MODULE="brutal"
TARGET_RATE_MBITS=48  # Mbps — Brutal target rate for Stealth tier
WRAPPER_SO="/usr/local/lib/brutal-wrap.so"

# ── Check if already installed ──
if lsmod | grep -q "^${BRUTAL_MODULE}"; then
    log "✓ ${BRUTAL_MODULE} module already loaded"
fi

if [ -f "$WRAPPER_SO" ] && [ -s "$WRAPPER_SO" ]; then
    log "✓ LD_PRELOAD wrapper already installed"
fi

# ── Install build dependencies ──
log "Installing build dependencies..."
DEPS="build-essential linux-headers-$(uname -r) git dkms make gcc"
apt-get update -qq
DEPS_TO_INSTALL=""
for pkg in $DEPS; do
    if ! dpkg -l "$pkg" &>/dev/null 2>&1; then
        DEPS_TO_INSTALL="$DEPS_TO_INSTALL $pkg"
    fi
done
if [ -n "$DEPS_TO_INSTALL" ]; then
    # shellcheck disable=SC2086
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $DEPS_TO_INSTALL
    log "✓ Dependencies installed"
else
    log "✓ Build dependencies already present"
fi

# ── Clone or update source ──
if [ -d "$BRUTAL_SRC" ]; then
    log "Updating existing tcp-brutal source..."
    cd "$BRUTAL_SRC" && GIT_TERMINAL_PROMPT=0 git pull --ff-only 2>/dev/null || {
        warn "git pull failed. Using existing source."
    }
else
    log "Cloning tcp-brutal..."
    GIT_TERMINAL_PROMPT=0 git clone --depth=1 "$BRUTAL_REPO" "$BRUTAL_SRC"
fi

cd "$BRUTAL_SRC"

# ── Build kernel module ──
log "Building kernel module..."
make clean 2>/dev/null || true
make KERNEL_DIR="/lib/modules/$(uname -r)/build" 2>&1 | tail -5 || \
    fail "Build failed. Check kernel headers."
log "✓ Kernel module built"

# ── Find the built module ──
MODULE_SRC=""
for candidate in "${BRUTAL_MODULE}.ko" "brutal.ko"; do
    found=$(find . -name "$candidate" -type f 2>/dev/null | head -1)
    if [ -n "$found" ]; then
        MODULE_SRC="$found"
        log "✓ Found module: ${found}"
        break
    fi
done

if [ -z "$MODULE_SRC" ]; then
    fail "Kernel module (.ko) not found after build."
fi

# ── Build LD_PRELOAD wrapper from our bundled source ──
log "Building LD_PRELOAD wrapper..."
WRAPPER_C="/usr/local/src/brutal-wrap.c"

# Create wrapper source (bundled — not from repo, since the repo doesn't include it anymore)
cat > "$WRAPPER_C" << 'WRAPPER'
// MyVPN Brutal CC LD_PRELOAD Wrapper
// Intercepts accept/accept4 and sets TCP_CONGESTION to "brutal" on each
// accepted connection. Also sets target rate via TCP_BRUTAL_PARAMS.
//
// ssserver doesn't set TCP_CONGESTION itself, so we intercept accept()
// to apply Brutal on each client socket after creation.
//
// Compile: gcc -shared -fPIC -o brutal-wrap.so brutal-wrap.c -ldl
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <errno.h>

// Target rate in bytes per second (from build-time TARGET_RATE_MBITS)
// Convert: 48 Mbps = 48 * 1000 * 1000 / 8 = 6,000,000 bytes/sec
#define BRUTAL_RATE_BYTES (TARGET_RATE_MBITS_PLACEHOLDER * 1000 * 1000 / 8)

#ifndef TCP_BRUTAL_PARAMS
#define TCP_BRUTAL_PARAMS 50
#endif

struct tcp_brutal_params {
    unsigned long long rate;
};

typedef int (*orig_accept4_t)(int, struct sockaddr *, socklen_t *, int);
typedef int (*orig_accept_t)(int, struct sockaddr *, socklen_t *);
typedef int (*orig_setsockopt_t)(int, int, int, const void *, socklen_t);

static orig_accept4_t real_accept4 = NULL;
static orig_accept_t real_accept = NULL;
static orig_setsockopt_t real_setsockopt = NULL;

__attribute__((constructor))
static void init() {
    real_accept4 = (orig_accept4_t)dlsym(RTLD_NEXT, "accept4");
    real_accept = (orig_accept_t)dlsym(RTLD_NEXT, "accept");
    real_setsockopt = (orig_setsockopt_t)dlsym(RTLD_NEXT, "setsockopt");
    fprintf(stderr, "[brutal-wrap] Loaded (target_rate=%llu bps)\n",
            (unsigned long long)BRUTAL_RATE_BYTES);
}

static void set_brutal_cc(int fd) {
    if (!real_setsockopt) return;

    int ret = real_setsockopt(fd, IPPROTO_TCP, TCP_CONGESTION, "brutal", 6);
    if (ret != 0) {
        fprintf(stderr, "[brutal-wrap] Failed TCP_CONGESTION=brutal on fd %d: %s\n",
                fd, strerror(errno));
        return;
    }

#if BRUTAL_RATE_BYTES > 0
    struct tcp_brutal_params params = { .rate = BRUTAL_RATE_BYTES };
    ret = real_setsockopt(fd, IPPROTO_TCP, TCP_BRUTAL_PARAMS, &params, sizeof(params));
    if (ret != 0) {
        fprintf(stderr, "[brutal-wrap] Failed TCP_BRUTAL_PARAMS on fd %d: %s\n",
                fd, strerror(errno));
    }
#endif
}

int accept4(int sockfd, struct sockaddr *addr, socklen_t *addrlen, int flags) {
    if (!real_accept4) { errno = ENOSYS; return -1; }
    int fd = real_accept4(sockfd, addr, addrlen, flags);
    if (fd >= 0) set_brutal_cc(fd);
    return fd;
}

int accept(int sockfd, struct sockaddr *addr, socklen_t *addrlen) {
    if (!real_accept) { errno = ENOSYS; return -1; }
    int fd = real_accept(sockfd, addr, addrlen);
    if (fd >= 0) set_brutal_cc(fd);
    return fd;
}
WRAPPER

# Substitute the target rate placeholder
sed -i "s/TARGET_RATE_MBITS_PLACEHOLDER/${TARGET_RATE_MBITS}/g" "$WRAPPER_C"

# Compile wrapper
gcc -shared -fPIC -o "$WRAPPER_SO" "$WRAPPER_C" -ldl 2>&1 | tail -3
if [ $? -eq 0 ] && [ -f "$WRAPPER_SO" ] && [ -s "$WRAPPER_SO" ]; then
    chmod 644 "$WRAPPER_SO"
    log "✓ LD_PRELOAD wrapper compiled and installed to ${WRAPPER_SO}"
else
    warn "Failed to compile LD_PRELOAD wrapper. Stealth tier will use BBR fallback."
    warn "  To compile manually: gcc -shared -fPIC -o ${WRAPPER_SO} ${WRAPPER_C} -ldl"
fi

# ── Install kernel module ──
log "Installing kernel module..."
cp "$MODULE_SRC" "/lib/modules/$(uname -r)/kernel/net/ipv4/${BRUTAL_MODULE}.ko"
depmod -a
log "✓ Module installed to kernel module tree"

# ── Register with DKMS for auto-rebuild on kernel updates ──
if command -v dkms &>/dev/null; then
    DKMS_NAME="tcp-brutal"
    DKMS_VERSION="1.0"

    if dkms status | grep -q "${DKMS_NAME}/${DKMS_VERSION}"; then
        log "DKMS module already registered"
    else
        DKMS_SRC="/usr/src/${DKMS_NAME}-${DKMS_VERSION}"
        mkdir -p "$DKMS_SRC"
        # Copy source files needed for DKMS build
        cp -f Makefile brutal.c "$DKMS_SRC/" 2>/dev/null || true
        # Generate dkms.conf
        cat > "${DKMS_SRC}/dkms.conf" << 'DKMS'
PACKAGE_NAME="tcp-brutal"
PACKAGE_VERSION="1.0"
BUILT_MODULE_NAME="brutal"
DEST_MODULE_LOCATION="/kernel/net/ipv4"
AUTOINSTALL="yes"
MAKE[0]="make KERNEL_DIR=/lib/modules/${kernelver}/build"
CLEAN="make clean"
DKMS
        dkms add -m "$DKMS_NAME" -v "$DKMS_VERSION" 2>&1 | tail -1 || true
        dkms build -m "$DKMS_NAME" -v "$DKMS_VERSION" 2>&1 | tail -3 || \
            warn "DKMS build failed (non-fatal — module already installed manually)"
        log "✓ DKMS source registered"
    fi
else
    warn "DKMS not installed. Module won't auto-rebuild on kernel updates."
fi

# ── Load module ──
if ! lsmod | grep -q "^${BRUTAL_MODULE}"; then
    rmmod "${BRUTAL_MODULE}" 2>/dev/null || true
    modprobe "${BRUTAL_MODULE}" 2>/dev/null || {
        insmod "/lib/modules/$(uname -r)/kernel/net/ipv4/${BRUTAL_MODULE}.ko" 2>/dev/null || \
            fail "Failed to load module via modprobe or insmod"
    }
    log "✓ Module loaded"
fi

# ── Persist module load on boot ──
MODULES_LOAD="/etc/modules-load.d/tcp_brutal.conf"
if [ ! -f "$MODULES_LOAD" ]; then
    echo "${BRUTAL_MODULE}" > "$MODULES_LOAD"
    log "✓ Created ${MODULES_LOAD}"
fi

# ── Verify ──
log "══════════════════════════════════════════"
log " Verification Summary"
log "══════════════════════════════════════════"
if lsmod | grep -q "^${BRUTAL_MODULE}"; then
    log "✓ ${BRUTAL_MODULE} module is loaded"
    if sysctl net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -q "brutal"; then
        log "✓ brutal CC is available in kernel"
    fi
fi

if [ -f "$WRAPPER_SO" ] && [ -s "$WRAPPER_SO" ]; then
    log "✓ LD_PRELOAD wrapper at ${WRAPPER_SO}"
    log "  Target rate: ${TARGET_RATE_MBITS} Mbps"
else
    warn "LD_PRELOAD wrapper NOT installed — Stealth tier uses BBR fallback"
fi

log "✓ Brutal CC setup complete"
exit 0

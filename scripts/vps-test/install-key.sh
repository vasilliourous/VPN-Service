#!/bin/bash
# Install a key-based SSH path for the whale sandbox on this test VPS.
# Idempotent. The upstream password is used for this one bootstrap step only.
set +e
KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOdG2K16HsvUOk4hFuZ+Csbxg/Weg7+hTmUJ4VpFo5XT whale-vps-183"

mkdir -p /root/.ssh
chmod 700 /root/.ssh
printf '%s\n' "$KEY" > /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
command -v restorecon >/dev/null && restorecon -R /root/.ssh 2>/dev/null

echo "--- authorized_keys now:"
cat /root/.ssh/authorized_keys
echo "--- loopback pubkey test:"
ssh -o StrictHostKeyChecking=no -o BatchMode=yes -o ConnectTimeout=8 root@127.0.0.1 true \
  && echo "LOOPBACK KEY AUTH OK" || echo "LOOPBACK KEY AUTH STILL DENIED"

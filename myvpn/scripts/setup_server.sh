#!/bin/bash
# setup_server.sh — Deploy admin hub (Caddy + PocketBase) to a fresh VPS
# Usage: ssh root@your-vps 'bash -s' < setup_server.sh
set -euo pipefail

DOMAIN="${1:-api.yourdomain.com}"

echo "=== Updating system ==="
apt update -qq && apt upgrade -y -qq

echo "=== Installing Caddy ==="
apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main" > /etc/apt/sources.list.d/caddy.list
apt update -qq && apt install -y caddy

echo "=== Installing PocketBase ==="
mkdir -p /opt/pocketbase && cd /opt/pocketbase
wget -q https://github.com/pocketbase/pocketbase/releases/download/v0.22.0/pocketbase_linux_amd64.zip
unzip -q pocketbase_linux_amd64.zip
chmod +x pocketbase
useradd --system --no-create-home --shell /usr/sbin/nologin pocketbase 2>/dev/null || true
chown -R pocketbase:pocketbase /opt/pocketbase

cat > /etc/systemd/system/pocketbase.service << 'SERVICEEOF'
[Unit]
Description=PocketBase
After=network.target

[Service]
Type=simple
User=pocketbase
Group=pocketbase
WorkingDirectory=/opt/pocketbase
ExecStart=/opt/pocketbase/pocketbase serve --http=127.0.0.1:8090
Restart=always

[Install]
WantedBy=multi-user.target
SERVICEEOF

systemctl daemon-reload
systemctl enable --now pocketbase

echo "=== Configuring Caddy ==="
cat > /etc/caddy/Caddyfile << CADDYEOF
${DOMAIN} {
    reverse_proxy 127.0.0.1:8090

    rate_limit {
        zone heartbeat {
            key {remote_host}
            match_path /api/heartbeat
            rate 1/10s
            burst 3
        }
        zone activation {
            key {remote_host}
            match_path /api/collections/activations/*
            rate 5/10m
            burst 5
        }
        zone general {
            key {remote_host}
            rate 100/10s
            burst 50
        }
    }

    @speedtest {
        path /speedtest/*
    }
    handle @speedtest {
        root * /var/www/speedtest
        file_server
        header Cache-Control "no-store"
    }

    @updates {
        path /updates/*
    }
    handle @updates {
        root * /var/www/updates
        file_server
    }
}
CADDYEOF

mkdir -p /var/www/speedtest /var/www/updates
dd if=/dev/urandom bs=1048576 count=1 of=/var/www/speedtest/1mb.bin 2>/dev/null
dd if=/dev/urandom bs=102400 count=1 of=/var/www/speedtest/100kb.bin 2>/dev/null
chmod -R 755 /var/www

systemctl restart caddy

echo "=== Configuring UFW ==="
ufw --force enable
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 22/tcp

echo "=== Creating PocketBase hooks directory ==="
mkdir -p /opt/pocketbase/pb_hooks
chown -R pocketbase:pocketbase /opt/pocketbase/pb_hooks

echo "=== Setting up offsite backups (Backblaze B2) ==="
echo "Note: Configure B2 credentials and backup script manually."
echo "See: internal/notes/backup.md"

echo ""
echo "=== Deployment complete ==="
echo "1. Set TOKEN_SECRET in PocketBase environment"
echo "2. Create collections in PocketBase admin UI at https://${DOMAIN}/_/"
echo "3. Copy pb_hooks/*.pb.js to /opt/pocketbase/pb_hooks/"
echo "4. Restart PocketBase: systemctl restart pocketbase"

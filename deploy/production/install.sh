#!/bin/sh
# One-time setup of a fresh Ubuntu 24.04 server for Pulse, run as root:
#   git clone https://github.com/ranjeshgupta/pulse.git /opt/pulse
#   /opt/pulse/deploy/production/install.sh pulse.example.com go.example.com
# Safe to run again: each step checks what's already done.
set -eu
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
[ $# -eq 2 ] || { echo "usage: $0 app.domain pages.domain" >&2; exit 2; }
cd "$(dirname "$0")"
here=$(pwd)

echo "== packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq docker.io docker-compose-v2 ufw curl git openssl unattended-upgrades >/dev/null
systemctl enable --now docker >/dev/null

echo "== Docker log rotation"
if [ ! -f /etc/docker/daemon.json ]; then
	printf '{"log-driver":"json-file","log-opts":{"max-size":"50m","max-file":"5"}}\n' > /etc/docker/daemon.json
	systemctl restart docker
fi

echo "== swap (4 GB) and kernel settings"
if ! swapon --show | grep -q /swapfile; then
	fallocate -l 4G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile
	grep -q /swapfile /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi
cat > /etc/sysctl.d/90-pulse.conf <<'SYSCTL'
vm.swappiness = 10
vm.overcommit_memory = 1
vm.max_map_count = 262144
net.core.somaxconn = 4096
SYSCTL
sysctl --system >/dev/null

echo "== secrets"
./gen-secrets.sh "$1" "$2"

echo "== firewall (SSH + Cloudflare only on 80/443)"
./firewall.sh
cat > /etc/cron.d/pulse <<CRON
# Pulse: refresh Cloudflare ranges weekly, back up nightly.
SHELL=/bin/sh
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
17 3 * * 1 root $here/firewall.sh >/var/log/pulse-firewall.log 2>&1
40 2 * * * root $here/backup.sh >>/var/log/pulse-backup.log 2>&1
CRON

echo "== certificate"
mkdir -p certs backups/postgres backups/clickhouse
chmod 700 certs backups backups/postgres backups/clickhouse
if [ ! -s certs/origin.pem ] || [ ! -s certs/origin.key ]; then
	cat <<MSG
Put the Cloudflare Origin Certificate (covering $1 and $2) in:
  $here/certs/origin.pem   (certificate)
  $here/certs/origin.key   (private key, chmod 600)
then run this script again. (Cloudflare: SSL/TLS -> Origin Server -> Create Certificate.)
MSG
	exit 0
fi
chmod 600 certs/origin.key

echo "== build and start (the first build takes a few minutes)"
docker compose --env-file .env up -d --build
echo "waiting for Pulse to answer"
for _ in $(seq 1 90); do
	if curl -fsS http://127.0.0.1:7070/healthz >/dev/null 2>&1; then break; fi
	sleep 2
done
curl -fsS http://127.0.0.1:7070/healthz >/dev/null && echo "Pulse is up." || { echo "Pulse didn't answer; see: docker compose logs pulse" >&2; exit 1; }
cat <<MSG

Done. Next:
  1. Cloudflare DNS: $1 and $2 -> this server's IP, Proxied. SSL/TLS mode: Full (strict).
  2. Create the first workspace:  $here/create-workspace.sh "Your company" you@example.com
  3. Keep $here/.env safe (back it up off the server): its secret key decrypts stored credentials.
MSG

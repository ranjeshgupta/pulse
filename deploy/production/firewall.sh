#!/bin/sh
# Host firewall: SSH from anywhere (change SSH_FROM to your IP to lock it down), HTTP/HTTPS only
# from Cloudflare's published ranges (fetched fresh each run; install.sh schedules a weekly run).
# Pulse trusts the CF-Connecting-IP header only because nothing else can reach ports 80/443.
set -eu
SSH_FROM=${SSH_FROM:-any}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsS https://www.cloudflare.com/ips-v4 -o "$tmp/v4"
curl -fsS https://www.cloudflare.com/ips-v6 -o "$tmp/v6"
# Refuse to apply a suspiciously short list (a failed or odd download would lock the site out).
[ "$(wc -l < "$tmp/v4")" -ge 10 ] || { echo "Cloudflare IPv4 list looks wrong; firewall unchanged" >&2; exit 1; }

ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
if [ "$SSH_FROM" = any ]; then ufw allow 22/tcp; else ufw allow from "$SSH_FROM" to any port 22 proto tcp; fi
for r in $(cat "$tmp/v4" "$tmp/v6"); do
	ufw allow proto tcp from "$r" to any port 80,443 comment cloudflare >/dev/null
done
ufw --force enable
ufw status | head -5
echo "Firewall: SSH from $SSH_FROM; 80/443 from $(cat "$tmp/v4" "$tmp/v6" | wc -l) Cloudflare ranges only."

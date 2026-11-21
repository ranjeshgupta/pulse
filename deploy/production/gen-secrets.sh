#!/bin/sh
# Makes .env from env.example with fresh random secrets (once: an existing .env is kept, since its
# secrets encrypt stored data). Usage: ./gen-secrets.sh app.domain pages.domain
set -eu
cd "$(dirname "$0")"
if [ -f .env ]; then
	echo ".env exists: keeping it (its PULSE_SECRET_KEY encrypts stored credentials)."
	exit 0
fi
[ $# -eq 2 ] || { echo "usage: $0 app.domain pages.domain" >&2; exit 2; }
hex() { openssl rand -hex "$1"; }
umask 077
sed -e "s|^APP_DOMAIN=.*|APP_DOMAIN=$1|" \
    -e "s|^PAGES_DOMAIN=.*|PAGES_DOMAIN=$2|" \
    -e "s|^PULSE_SECRET_KEY=.*|PULSE_SECRET_KEY=$(hex 32)|" \
    -e "s|^PULSE_ADMIN_TOKEN=.*|PULSE_ADMIN_TOKEN=$(hex 32)|" \
    -e "s|^PG_PASSWORD=.*|PG_PASSWORD=$(hex 24)|" \
    -e "s|^CH_PASSWORD=.*|CH_PASSWORD=$(hex 24)|" \
    -e "s|^REDIS_PASSWORD=.*|REDIS_PASSWORD=$(hex 24)|" \
    env.example > .env
chmod 600 .env
echo "Wrote .env (root only). Back it up somewhere safe: without PULSE_SECRET_KEY, stored credentials can't be read."

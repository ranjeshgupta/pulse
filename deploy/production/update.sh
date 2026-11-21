#!/bin/sh
# Pulls the latest Pulse, rebuilds and restarts it (migrations run on start). Takes a backup first.
set -eu
cd "$(dirname "$0")"
./backup.sh
git -C ../.. pull --ff-only
docker compose --env-file .env up -d --build
docker image prune -f >/dev/null
echo "updated to $(git -C ../.. log -1 --format='%h %s')"

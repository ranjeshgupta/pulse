#!/bin/sh
# Nightly backups (install.sh adds the cron entry): Postgres (workspaces, users, campaigns, settings,
# contacts) every night, kept 14 days; ClickHouse (events, message history) every Sunday, kept 2;
# copied off the server with rclone when BACKUP_REMOTE is set in .env. Run by hand any time.
set -eu
umask 077   # dumps hold contact data: root only
cd "$(dirname "$0")"
. ./.env
stamp=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p backups/postgres backups/clickhouse
chmod 700 backups backups/postgres backups/clickhouse
chmod 600 backups/postgres/*.dump 2>/dev/null || true
docker compose exec -T postgres pg_dump -U pulse -Fc pulse > "backups/postgres/pulse-$stamp.dump"
find backups/postgres -name '*.dump' -mtime +14 -delete
if [ "$(date -u +%u)" = 7 ] || [ "${FULL:-}" = 1 ]; then
	docker compose exec -T clickhouse clickhouse-client --user pulse --password "$CH_PASSWORD" \
		-q "BACKUP DATABASE pulse TO Disk('backups', 'pulse-$stamp.zip')" >/dev/null
	ls -1t backups/clickhouse/pulse-*.zip 2>/dev/null | tail -n +3 | xargs -r rm -f
fi
if [ -n "${BACKUP_REMOTE:-}" ]; then
	rclone copy backups "$BACKUP_REMOTE" --max-age 8d
fi
echo "backup $stamp done"

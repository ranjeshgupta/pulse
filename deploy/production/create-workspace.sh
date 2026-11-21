#!/bin/sh
# Creates a workspace and its first admin (public signup is off in production).
# Usage: ./create-workspace.sh "Workspace name" admin@example.com
# The admin's password is generated and written to first-admin-<slug>.txt (root only); sign in,
# change it under Account, then delete the file. The workspace's API key is in the same file.
set -eu
cd "$(dirname "$0")"
. ./.env
[ $# -eq 2 ] || { echo "usage: $0 \"Workspace name\" admin@email" >&2; exit 2; }
api=${PULSE_LOCAL_API:-http://127.0.0.1:7070}
json() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
ws=$(curl -fsS -X POST "$api/v1/tenants" -H "Authorization: Bearer $PULSE_ADMIN_TOKEN" \
	-H 'Content-Type: application/json' -d "{\"name\":\"$(json "$1")\"}")
key=$(printf '%s' "$ws" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
slug=$(printf '%s' "$ws" | sed -n 's/.*"slug":"\([^"]*\)".*/\1/p')
[ -n "$key" ] || { echo "creating the workspace failed: $ws" >&2; exit 1; }
pw=$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-20)
curl -fsS -X POST "$api/v1/users" -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
	-d "{\"email\":\"$(json "$2")\",\"name\":\"Admin\",\"role\":\"admin\",\"password\":\"$pw\"}" >/dev/null
umask 077
f="first-admin-$slug.txt"
printf 'workspace: %s (%s)\nsign in: https://%s/login\nemail: %s\npassword: %s\napi key: %s\n' \
	"$1" "$slug" "$APP_DOMAIN" "$2" "$pw" "$key" > "$f"
echo "Workspace \"$1\" created. Sign-in details are in $(pwd)/$f (root only)."

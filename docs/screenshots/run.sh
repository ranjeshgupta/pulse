#!/bin/sh
# Runs inside mcr.microsoft.com/playwright:v$PLAYWRIGHT_VERSION-jammy (see `make docs-shots`).
# Installs the playwright npm package matching the image into /src/.cache/docs-shots once, then
# runs the capture scripts.
set -e
MODS=/src/.cache/docs-shots
want="${PLAYWRIGHT_VERSION:?set PLAYWRIGHT_VERSION}"
have=$(node -p "try { require('$MODS/node_modules/playwright/package.json').version } catch { '' }")
if [ "$have" != "$want" ]; then
  mkdir -p "$MODS"
  echo "installing playwright@$want into .cache/docs-shots"
  npm install --prefix "$MODS" --no-audit --no-fund --no-save --loglevel error "playwright@$want"
fi
exec node run.mjs

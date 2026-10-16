#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
if ! command -v node >/dev/null 2>&1; then
  echo 'Node.js 20+ is required to run the frontend development server.' >&2
  exit 1
fi
if ! node -e 'process.exit(Number(process.versions.node.split(".")[0]) >= 20 ? 0 : 1)'; then
  echo 'Node.js 20+ is required to run the frontend development server.' >&2
  exit 1
fi
exec node server.mjs

#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DIR="$(mktemp -d "${TMPDIR:-/tmp}/panel-e2e.XXXXXX")"
export AMNEZIA_DB_PATH="$DIR/amnezia.sqlite"
export AMNEZIA_CONFIG_PATH="$DIR/awg0.conf"
export AMNEZIA_STATUS_PATH="$DIR/status.json"
export AMNEZIA_BACKUPS_PATH="$DIR/backups"
export AMNEZIA_SECURE_COOKIES=
mkdir -p "$DIR/backups"
cd "$ROOT"
printf '%s\n' "e2e-password-correct-horse" | go run . auth add-user e2e --password-stdin
go run . server init "10.8.0.1/24" "51820" --endpoint "127.0.0.1:51820"
exec go run . serve --addr 127.0.0.1:18787

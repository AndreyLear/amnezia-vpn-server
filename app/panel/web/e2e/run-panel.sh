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
# Клиенты нужны, потому что обычный экран панели — это список, а он рисуется
# только когда есть кого показывать. Без них поднимался экран первого запуска,
# и половина проверок описывала панель, которой в этом состоянии не бывает
# (amnezia-vpn-server-e72j). Пустое состояние проверяется отдельно, поднятием
# фикстуры с AMNEZIA_E2E_NO_CLIENTS=1.
if [ "${AMNEZIA_E2E_NO_CLIENTS:-0}" != "1" ]; then
    go run . client add "alice" >/dev/null
    go run . client add "bob" >/dev/null
fi
exec go run . serve --addr "127.0.0.1:${AMNEZIA_E2E_PORT:-18787}"

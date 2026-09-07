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
# Полоса о новом выпуске рисуется по файлу, который на живом сервере пишет
# хост (amnezia-vpn-server-tjoq). Здесь его кладём мы: иначе проверить полосу
# можно было бы только дождавшись настоящего выпуска.
if [ "${AMNEZIA_E2E_UPDATE:-0}" = "1" ]; then
    # Без своей версии панель молчит, и правильно делает: сказать «вышла
    # версия N» тому, кто её, может быть, уже поставил, — хуже, чем промолчать.
    # На развёртывании версию задаёт compose из versions.lock.
    export AMNEZIA_VERSION="1.0.0"
    cat > "$DIR/update-latest.json" <<'JSON'
{"tag_name":"v99.9.9","body":"- Первое изменение\n- Второе изменение\n\namnezia-sha256: 0000000000000000000000000000000000000000000000000000000000000000\n"}
JSON
    printf '{"schema":"v1","checked_at_utc":"2026-09-07T09:00:00Z","result":"ok"}\n' \
        > "$DIR/update-check.json"
    # Снимок сторожа: без него окно «Состояние служб» проверялось бы только
    # на сервере, где сторож успел отработать (amnezia-vpn-server-eq82).
    cat > "$DIR/services.json" <<'JSON'
{"schema":"v1","checked_at_utc":"2026-09-07T09:00:00Z","services":[
{"name":"dns","state":"ok","reason":"","fails":0,"restarted_at_utc":"2026-09-07T08:40:00Z","restart_reason":"не отвечает на 10.8.0.1"},
{"name":"awg","state":"ok","reason":"","fails":0,"restarted_at_utc":"","restart_reason":""}]}
JSON
    printf '{"schema":"v1","os":"ubuntu 24.04 (noble)","docker":"27.3.1","watchdog":true,"fail2ban":true,"update_check":true}\n' \
        > "$DIR/deployment.json"
fi

exec go run . serve --addr "127.0.0.1:${AMNEZIA_E2E_PORT:-18787}"

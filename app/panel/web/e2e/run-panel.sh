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
# История скорости: без неё график в карточке проверять нечем
# (amnezia-vpn-server-tmjw). Кладём последние десять минут так, как их писал бы
# контейнер awg, — с провалом посередине, потому что ровную линию график
# нарисует и по ошибке.
if [ "${AMNEZIA_E2E_NO_CLIENTS:-0}" != "1" ]; then
    keys="$(awk '/^PublicKey/ {printf "%s ", substr($3, 1, 12)}' "$AMNEZIA_CONFIG_PATH")"
    awk -v now="$(date -u +%s)" -v keys="$keys" 'BEGIN {
        n = split(keys, k, " ")
        print "#speed v1"
        rx = 0; tx = 0
        for (i = 0; i < 120; i++) {
            step = (i >= 60 && i < 66) ? 1250000 : 62500000
            tx += step; rx += step / 10
            line = sprintf("%d", now - (120 - i) * 5)
            for (j = 1; j <= n; j++) line = line sprintf(" %s:%d:%d", k[j], rx, tx)
            print line
        }
    }' > "$DIR/speed.log"
fi

# Полоса о новом выпуске рисуется по файлу, который на живом сервере пишет
# хост (amnezia-vpn-server-tjoq). Здесь его кладём мы: иначе проверить полосу
# можно было бы только дождавшись настоящего выпуска.
if [ "${AMNEZIA_E2E_UPDATE:-0}" = "1" ]; then
    # Без своей версии панель молчит, и правильно делает: сказать «вышла
    # версия N» тому, кто её, может быть, уже поставил, — хуже, чем промолчать.
    # На развёртывании версию задаёт compose из versions.lock.
    export AMNEZIA_VERSION="1.0.0"
    # Список выпусков: человек, отставший на два, должен прочитать оба.
    cat > "$DIR/update-latest.json" <<'JSON'
[{"tag_name":"v99.9.9","body":"- Первое изменение\n\namnezia-sha256: 0000000000000000000000000000000000000000000000000000000000000000\n"},
 {"tag_name":"v99.9.8","body":"- Второе изменение\n"}]
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
    # Итог прошлого обновления: панель обязана показать его сама, потому что
    # обновление перезапускает её саму (amnezia-vpn-server-tjoq).
    printf '{"schema":"v1","state":"ok","from":"1.0.0","to":"99.9.9","step":"готово","message":"обновление до 99.9.9 завершено","at_utc":"2026-09-08T10:00:00Z"}\n' \
        > "$DIR/update-state.json"
fi

exec go run . serve --addr "127.0.0.1:${AMNEZIA_E2E_PORT:-18787}"

#!/usr/bin/env bash
#
# watchdog.sh — перезапускает сервис, который жив, но не работает
# (amnezia-vpn-server-ptuo).
#
# compose.yaml объявляет restart: unless-stopped, и этого хватает ровно на
# один случай: процесс умер. Зависший dnsmasq и замерший awg остаются в
# состоянии «контейнер поднят», для Docker это успех, а для пользователя —
# молчащий резолвер и мёртвый туннель до первой жалобы. Docker healthcheck
# здесь не помощник: статус unhealthy сам по себе ничего не перезапускает.
#
# Проверки нарочно разные по природе:
#
#   резолвер  спросить у него имя через адрес туннеля. Единственная проверка,
#             которая ловит «процесс жив, ответов нет».
#   туннель   свежесть status.json. Контейнер awg переписывает его каждые
#             AWG_CHECK_INTERVAL секунд (по умолчанию 5) и сам выходит, если
#             интерфейс исчез или UAPI не отвечает — поэтому смерть уже
#             закрыта политикой перезапуска. Не закрыт застой: цикл жив, а
#             файл не обновляется.
#
# Рукопожатия пиров намеренно НЕ являются поводом для перезапуска. Все
# клиенты могут быть законно офлайн (ночь, отпуск), и перезапуск туннеля из-за
# этого — вред без причины.
#
# Осторожность важнее рвения. Перезапуск сам по себе разрыв связи, поэтому:
#   * нужны два отказа подряд, одиночный сбой сети или секунда нагрузки не в
#     счёт;
#   * между двумя перезапусками одного сервиса выдерживается пауза, иначе
#     сервис, который не поднимается, попадёт в бесконечную карусель.
#
# Идемпотентен и молчалив при исправном стеке: пишет в журнал только когда
# что-то делает.

set -u

ROOT_DIR="${AMNEZIA_WATCHDOG_ROOT:-/opt/amnezia-vpn}"
STATE_DIR="${AMNEZIA_WATCHDOG_STATE:-/run/amnezia-vpn-watchdog}"
# Два отказа подряд до перезапуска.
FAIL_THRESHOLD="${AMNEZIA_WATCHDOG_THRESHOLD:-2}"
# Пауза между перезапусками одного сервиса, секунды.
RESTART_INTERVAL="${AMNEZIA_WATCHDOG_RESTART_INTERVAL:-600}"
# Предел давности status.json, секунды: awg пишет его раз в 5 секунд.
STATUS_MAX_AGE="${AMNEZIA_WATCHDOG_STATUS_MAX_AGE:-120}"
DOCKER="${AMNEZIA_WATCHDOG_DOCKER:-docker}"
DIG="${AMNEZIA_WATCHDOG_DIG:-dig}"

log() { printf 'watchdog: %s\n' "$*"; }

usage() {
    cat <<'EOF'
watchdog.sh — перезапускает сервис Amnezia VPN, который жив, но не работает.

Проверяет резолвер (отвечает ли он на запрос) и свежесть status.json,
который пишет туннель. Перезапускает контейнер после двух отказов подряд,
не чаще одного раза в 10 минут на сервис.

Использование:
  ./watchdog.sh

Ключи:
  --help    показать эту справку
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi
if [ "$#" -gt 0 ]; then
    printf 'watchdog: неизвестный аргумент: %s\n' "$1" >&2
    exit 2
fi

env_read() { # env_read КЛЮЧ — значение из .env развёртывания, "" если нет
    sed -n "s/^${1}=//p" "$ROOT_DIR/.env" 2>/dev/null | tail -1
}

mkdir -p "$STATE_DIR" 2>/dev/null || {
    log "не могу создать каталог состояния $STATE_DIR"
    exit 1
}

now="$(date +%s)"

counter_read() { # counter_read СЕРВИС
    local v
    v="$(cat "$STATE_DIR/$1.fail" 2>/dev/null || true)"
    case "$v" in
        ''|*[!0-9]*) printf '0' ;;
        *) printf '%s' "$v" ;;
    esac
}

restart_service() { # restart_service СЕРВИС ПРИЧИНА
    local service="$1" reason="$2" last age
    last="$(cat "$STATE_DIR/$service.restarted" 2>/dev/null || printf '0')"
    case "$last" in ''|*[!0-9]*) last=0 ;; esac
    age=$((now - last))
    if [ "$age" -lt "$RESTART_INTERVAL" ]; then
        # Не молчим: сервис по-прежнему сломан, просто перезапуск сейчас
        # навредил бы больше, чем помог.
        log "$service всё ещё не работает ($reason), но прошлый перезапуск был $age с назад — жду"
        return 0
    fi
    log "перезапускаю $service: $reason"
    if (cd "$ROOT_DIR" && "$DOCKER" compose --env-file versions.lock restart "$service") >/dev/null 2>&1; then
        printf '%s' "$now" > "$STATE_DIR/$service.restarted"
        printf '0' > "$STATE_DIR/$service.fail"
        log "$service перезапущен"
    else
        log "перезапустить $service не удалось"
    fi
}

record() { # record СЕРВИС ok|fail ПРИЧИНА
    local service="$1" verdict="$2" reason="${3:-}" count
    if [ "$verdict" = ok ]; then
        printf '0' > "$STATE_DIR/$service.fail"
        return 0
    fi
    count="$(counter_read "$service")"
    count=$((count + 1))
    printf '%s' "$count" > "$STATE_DIR/$service.fail"
    if [ "$count" -ge "$FAIL_THRESHOLD" ]; then
        restart_service "$service" "$reason"
    else
        log "$service: отказ $count из $FAIL_THRESHOLD ($reason)"
    fi
}

# --- резолвер ---------------------------------------------------------
#
# Пропускаем в режиме --no-tunnel-dns: там резолвер намеренно стоит в
# стороне и порт 53 не занимает, отсутствие ответа — это норма.
if [ "$(env_read TUNNEL_DNS_DISABLED)" = "1" ]; then
    :
else
    if ! command -v "$DIG" >/dev/null 2>&1; then
        # Без чем спросить проверка невозможна, и это не отказ резолвера.
        # Считать иначе значило бы перезапускать исправный сервис по кругу
        # из-за отсутствующего на хосте пакета.
        log "нечем проверить резолвер: $DIG не найден, проверка пропущена"
        record dns ok
        DNS_CHECKED=skipped
    fi
    tunnel_address="$(env_read TUNNEL_ADDRESS)"
    [ -n "$tunnel_address" ] || tunnel_address="10.8.0.1"
    # Имя спрашиваем внешнее и заведомо существующее: проверяется путь
    # целиком, от приёма запроса до ответа вышестоящего сервера.
    if [ "${DNS_CHECKED:-}" != skipped ]; then
        if "$DIG" +time=3 +tries=1 +short "@${tunnel_address}" example.com A >/dev/null 2>&1; then
            record dns ok
        else
            record dns fail "не отвечает на ${tunnel_address}"
        fi
    fi
fi

# --- туннель ----------------------------------------------------------
status_file="$ROOT_DIR/status/status.json"
if [ ! -f "$status_file" ]; then
    record awg fail "нет $status_file"
else
    # stat -c на Linux, stat -f на BSD: скрипт живёт на сервере, но
    # харнесс гоняют и на macOS, а проверка, которая там всегда видит
    # «файл из 1970», проверяла бы не то.
    mtime="$(stat -c %Y "$status_file" 2>/dev/null || stat -f %m "$status_file" 2>/dev/null || printf '0')"
    case "$mtime" in ''|*[!0-9]*) mtime=0 ;; esac
    age=$((now - mtime))
    if [ "$age" -gt "$STATUS_MAX_AGE" ]; then
        record awg fail "status.json не обновлялся $age с"
    else
        record awg ok
    fi
fi

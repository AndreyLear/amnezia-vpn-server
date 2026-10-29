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
#   Xray      (только при XRAY_ENABLED=1, amnezia-vpn-server-q1do.5) спросить
#             у него статистику через его же API: `xray api statssys` в
#             контейнере по сокету API. Это настоящий gRPC-вызов, на который
#             отвечает сам процесс, — «порт открыт» его не заменяет: зависшему
#             процессу ядро соединения принимает по-прежнему. Вдобавок вход
#             VLESS на 127.0.0.1:4432 должен принимать TCP — это ловит вход,
#             который пропал при живом API. Смерть процесса закрыта
#             restart: unless-stopped, зависание — нет.
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
# Вход VLESS Xray на петле хоста (internal/xray: inboundPort).
XRAY_PORT="${AMNEZIA_WATCHDOG_XRAY_PORT:-4432}"
# Сокет API Xray — путь внутри контейнера (internal/xray: DefaultAPIAddr).
XRAY_API_SOCKET=/run/xray/api.sock

log() { printf 'watchdog: %s\n' "$*"; }

usage() {
    cat <<'EOF'
watchdog.sh — перезапускает сервис Amnezia VPN, который жив, но не работает.

Проверяет резолвер (отвечает ли он на запрос), свежесть status.json,
который пишет туннель, и, если включён, Xray (отвечает ли его API и
принимает ли вход соединения). Перезапускает контейнер после двух отказов подряд,
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

# Xray объявлен в compose.yaml с профилем: без --profile compose его не
# видит (тот же вызов, что у amnezia-vpn-xray-restart.service).
compose_profile() { # compose_profile СЕРВИС
    [ "$1" != xray ] || printf -- '--profile xray'
}

# with_timeout СЕК КОМАНДА… — с пределом, если есть timeout(1): зависший
# docker не должен вешать сторожа. На macOS, где гоняют харнесс, его нет.
with_timeout() {
    local sec="$1"
    shift
    if command -v timeout >/dev/null 2>&1; then
        timeout "$sec" "$@"
    else
        "$@"
    fi
}

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
    if (cd "$ROOT_DIR" && "$DOCKER" compose $(compose_profile "$service") --env-file versions.lock restart "$service") >/dev/null 2>&1; then
        printf '%s' "$now" > "$STATE_DIR/$service.restarted"
        # Причина перезапуска переживает сам перезапуск: через минуту всё
        # будет исправно, и без этой записи в панели не останется следа,
        # что что-то вообще происходило.
        printf '%s' "$reason" > "$STATE_DIR/$service.restart_reason"
        printf '0' > "$STATE_DIR/$service.fail"
        log "$service перезапущен"
    else
        log "перезапустить $service не удалось"
    fi
}

record() { # record СЕРВИС ok|fail ПРИЧИНА
    local service="$1" verdict="$2" reason="${3:-}" count
    # Приговор и причина запоминаются для панели: отказ, который чинится
    # сам, сегодня невидим, и владелец узнаёт о нём от пользователей, а не
    # от панели — ровно та беда, из-за которой сторож и заводился
    # (amnezia-vpn-server-eq82).
    printf '%s' "$verdict" > "$STATE_DIR/$service.verdict"
    printf '%s' "$reason" > "$STATE_DIR/$service.reason"
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

# --- Xray (amnezia-vpn-server-q1do.5) -----------------------------------
XRAY_ON=0
[ "$(env_read XRAY_ENABLED)" != "1" ] || XRAY_ON=1
if [ "$XRAY_ON" = "1" ]; then
    xray_reason=""
    if (cd "$ROOT_DIR" && with_timeout 20 "$DOCKER" compose --profile xray --env-file versions.lock \
            exec -T xray xray api statssys "--server=unix:$XRAY_API_SOCKET" --timeout=5) >/dev/null 2>&1; then
        # Запомнить, что проверка в этой загрузке системы работает: STATE_DIR
        # живёт в /run и с перезагрузкой пропадает.
        : > "$STATE_DIR/xray.api_seen"
    elif [ -f "$STATE_DIR/xray.api_seen" ]; then
        xray_reason="xray не отвечает на запрос статистики"
    else
        # Проверка, которая ни разу не ответила, ничего не доказывает: так
        # выглядит и Xray, который ещё поднимается, и сама проверка, которой
        # здесь не на чем работать (другой образ, другой адрес сокета).
        # Перезапускать по ней раз в 10 минут — рвать VLESS без причины.
        log "API Xray ни разу не ответил с загрузки системы — его молчание не засчитывается"
    fi
    if [ -z "$xray_reason" ] && ! with_timeout 5 bash -c 'exec 3<>"/dev/tcp/127.0.0.1/$1"' _ "$XRAY_PORT" 2>/dev/null; then
        xray_reason="вход VLESS не принимает соединения на 127.0.0.1:$XRAY_PORT"
    fi
    if [ -n "$xray_reason" ]; then
        record xray fail "$xray_reason"
    else
        record xray ok
    fi
fi

# --- снимок для панели (amnezia-vpn-server-eq82) -----------------------
#
# Панель живёт в контейнере и ни про журнал, ни про systemd знать не может.
# Всё, что сторож выяснил за эту минуту, он и записывает — рядом со
# status.json, откуда панель уже читает и только на чтение.
#
# Отказ, который чинится сам, невидим ровно до тех пор, пока о нём негде
# прочитать. Здесь и есть это «где».
services_snapshot() {
    local dir="$ROOT_DIR/status" tmp file service
    [ -d "$dir" ] || return 0
    file="$dir/services.json"
    tmp="$file.tmp"

    entry() { # entry СЕРВИС — один объект JSON
        local service="$1" verdict reason fails restarted restart_reason
        verdict="$(cat "$STATE_DIR/$service.verdict" 2>/dev/null || printf 'unknown')"
        reason="$(cat "$STATE_DIR/$service.reason" 2>/dev/null || printf '')"
        fails="$(counter_read "$service")"
        restarted="$(cat "$STATE_DIR/$service.restarted" 2>/dev/null || printf '')"
        restart_reason="$(cat "$STATE_DIR/$service.restart_reason" 2>/dev/null || printf '')"
        # Время наружу уходит в том же виде, что и везде: UTC по RFC 3339.
        if [ -n "$restarted" ]; then
            restarted="$(date -u -r "$restarted" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
                || date -u -d "@$restarted" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || printf '')"
        fi
        printf '{"name":"%s","state":"%s","reason":"%s","fails":%s,"restarted_at_utc":"%s","restart_reason":"%s"}' \
            "$service" "$verdict" "$reason" "$fails" "$restarted" "$restart_reason"
    }

    {
        printf '{"schema":"v1","checked_at_utc":"%s","services":[' \
            "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        printf '%s,' "$(entry dns)"
        printf '%s' "$(entry awg)"
        [ "$XRAY_ON" != "1" ] || printf ',%s' "$(entry xray)"
        printf ']}\n'
    } > "$tmp" 2>/dev/null \
        && chmod 0644 "$tmp" \
        && mv -f "$tmp" "$file"
    return 0
}

services_snapshot

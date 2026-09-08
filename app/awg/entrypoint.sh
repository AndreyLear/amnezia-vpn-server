#!/bin/bash
set -euo pipefail

IFACE=${AWG_IFACE:-awg0}
CONFIG_SRC=${CONFIG_SRC:-/config/awg0.conf}
CONFIG_DEST=${CONFIG_DEST:-/etc/amnezia/amneziawg/awg0.conf}
SYNCCONF_TMP=${SYNCCONF_TMP:-/tmp/awg0.syncconf.conf}
CONFIG_TIMEOUT=${AWG_CONFIG_TIMEOUT:-300}
CHECK_INTERVAL=${AWG_CHECK_INTERVAL:-5}
STATUS_FILE=${AWG_STATUS_FILE:-/status/status.json}
AWGSTATUS_BIN=${AWGSTATUS_BIN:-/opt/awg/awgstatus}

log() { echo "[awg] $*" >&2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f /opt/awg/syncconf.sh ]; then
    # shellcheck source=/dev/null
    source /opt/awg/syncconf.sh
else
    # fallback for local runs / tests without the container layout
    # shellcheck source=/dev/null
    source "${SCRIPT_DIR}/syncconf.sh"
fi

# Regenerates status/status.json from the live UAPI dump (M5). The
# producer reads the runtime itself; a failure to generate status must
# never affect the M3.2 lifecycle, so only a warning is logged and the
# previous snapshot stays. When the producer binary is absent (local
# runs), status generation is skipped silently.
generate_status() {
    local producer="${AWGSTATUS_BIN}"
    if [ ! -x "${producer}" ]; then
        return 0
    fi
    if ! "${producer}" "${IFACE}" "${STATUS_FILE}"; then
        log "warning: status generation failed; keeping the previous ${STATUS_FILE}"
    fi
}

# generate_dns_seen: снимок множества dns_seen рядом со status.json.
#
# Панель работает без прав на сеть и прочитать nftables не может. Права есть
# здесь: контейнер живёт в сети хоста и с NET_ADMIN. Снимок обновляется тем же
# тиком, что и status.json, и ложится в тот же каталог, который панель уже
# читает только на чтение (amnezia-vpn-server-g0vd).
#
# Пустой файл — законное состояние: множества может не быть вовсе, если
# правила ещё не применены. Отсутствие записи о клиенте означает «не
# спрашивал», а отсутствие файла — «нечего сказать», и панель обязана
# различать эти два случая.
DNS_SEEN_FILE="${DNS_SEEN_FILE:-$(dirname "${STATUS_FILE}")/dns-seen.json}"

# Ничто здесь не имеет права уронить туннель. Снимок — диагностика для панели,
# а скрипт работает под set -e: неудачная запись завершила бы его целиком, и
# контейнер ушёл бы в перезапуск. Так и случилось в CI, где nft есть и функция
# доходила до записи в каталог, которого в харнессе не существует.
generate_dns_seen() {
    command -v nft >/dev/null 2>&1 || return 0
    local addrs tmp dir
    dir="$(dirname "${DNS_SEEN_FILE}")"
    [ -d "${dir}" ] || return 0
    addrs="$(
        {
            nft -j list set ip amnezia dns_seen 2>/dev/null || true
            nft -j list set ip6 amnezia dns_seen 2>/dev/null || true
        } | tr ',' '\n' \
          | sed -n 's/.*"val"[[:space:]]*:[[:space:]]*"\([0-9a-fA-F:.]*\)".*/\1/p' \
          | sort -u
    )"
    tmp="${DNS_SEEN_FILE}.tmp"
    {
        printf '{"schema":"v1","generated_at_utc":"%s","addresses":[' \
            "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        local first=1 a
        for a in ${addrs}; do
            [ "${first}" = "1" ] || printf ','
            printf '"%s"' "${a}"
            first=0
        done
        printf ']}\n'
    } > "${tmp}" 2>/dev/null && mv -f "${tmp}" "${DNS_SEEN_FILE}" || {
        log "warning: could not write ${DNS_SEEN_FILE}; the tunnel is unaffected"
        rm -f "${tmp}" 2>/dev/null || true
    }
    return 0
}

# Версии, которые несёт этот образ. Пишутся один раз при старте: они не
# меняются, пока не сменится образ, а панель должна уметь ответить, что стоит
# на сервере (amnezia-vpn-server-rdcz). Как и снимок dns-seen, файл лежит в
# каталоге, который панель читает только на чтение, и его отсутствие для неё
# означает «неизвестно», а не поломку.
VERSIONS_FILE="${VERSIONS_FILE:-$(dirname "${STATUS_FILE}")/versions.json}"

generate_versions() {
    local dir tmp
    dir="$(dirname "${VERSIONS_FILE}")"
    [ -d "${dir}" ] || return 0
    tmp="${VERSIONS_FILE}.tmp"
    {
        printf '{"schema":"v1","amneziawg_go":"%s","amneziawg_tools":"%s"}\n' \
            "${AMNEZIAWG_GO_VERSION:-}" "${AMNEZIAWG_TOOLS_VERSION:-}"
    } > "${tmp}" 2>/dev/null && mv -f "${tmp}" "${VERSIONS_FILE}" || {
        log "warning: could not write ${VERSIONS_FILE}; the tunnel is unaffected"
        rm -f "${tmp}" 2>/dev/null || true
    }
    return 0
}

wait_for_config() {
    local deadline=$((SECONDS + CONFIG_TIMEOUT))
    while [ ! -f "${CONFIG_SRC}" ]; do
        if [ "${SECONDS}" -ge "${deadline}" ]; then
            log "error: ${CONFIG_SRC} did not appear within ${CONFIG_TIMEOUT}s"
            exit 1
        fi
        log "waiting for ${CONFIG_SRC} ..."
        sleep 2
    done
}

# ipv6_usable: whether this host will accept an IPv6 address on a freshly
# created interface (amnezia-vpn-server-mmh6).
#
# A newly created interface inherits conf/default/disable_ipv6, and
# conf/all/disable_ipv6 overrides everything, so both must be clear.
# A kernel built without IPv6 has no /proc/net/if_inet6 at all. The awg
# container runs with network_mode: host, so these are the host's values.
#
# Unknown counts as unusable: guessing wrong in that direction costs the
# tunnel its IPv6, while guessing wrong in the other costs the tunnel its
# existence.
# AWG_PROC_ROOT exists so the harness can present a fake /proc; in the
# container it is empty and every path below is the real one.
ipv6_usable() {
    local proc="${AWG_PROC_ROOT:-}"
    [ -e "${proc}/proc/net/if_inet6" ] || return 1
    local all_disabled default_disabled
    all_disabled="$(cat "${proc}/proc/sys/net/ipv6/conf/all/disable_ipv6" 2>/dev/null || echo 1)"
    default_disabled="$(cat "${proc}/proc/sys/net/ipv6/conf/default/disable_ipv6" 2>/dev/null || echo 1)"
    [ "${all_disabled}" = "0" ] && [ "${default_disabled}" = "0" ]
}

# config_has_ipv6: does this configuration ask for anything IPv6? Only the
# two keys awg-quick turns into ip(8) calls are relevant — Address becomes
# `ip -6 address add`, AllowedIPs becomes `ip -6 route add`.
config_has_ipv6() {
    grep -qiE '^[[:space:]]*(Address|AllowedIPs)[[:space:]]*=.*:' "$1"
}

# strip_ipv6: drop every IPv6 entry from Address and AllowedIPs, leaving
# the rest of the file byte-for-byte alone. An entry is IPv6 when it
# contains a colon, which no IPv4 CIDR ever does.
strip_ipv6() {
    awk '
        BEGIN { FS = "="; OFS = "=" }
        /^[[:space:]]*(Address|AllowedIPs)[[:space:]]*=/ {
            key = $1
            value = substr($0, index($0, "=") + 1)
            n = split(value, parts, ",")
            kept = ""
            for (i = 1; i <= n; i++) {
                entry = parts[i]
                gsub(/^[[:space:]]+|[[:space:]]+$/, "", entry)
                if (entry == "" || index(entry, ":") > 0) continue
                kept = (kept == "") ? entry : kept ", " entry
            }
            # A peer left with no AllowedIPs at all would be rejected by
            # awg setconf, so such a line is passed through untouched and
            # the operator sees the original failure rather than a
            # confusing one from us.
            if (kept == "") { print; next }
            print key "= " kept
            next
        }
        { print }
    ' "$1"
}

install_config() {
    cp "${CONFIG_SRC}" "${CONFIG_DEST}"
    # mmh6: an IPv6 address on a host that refuses IPv6 does not cost the
    # tunnel its IPv6 — it costs the tunnel its existence. awg-quick runs
    # under set -e: `ip -6 address add` fails, and awg-quick deletes the
    # interface it had just created, so every client loses the VPN,
    # including those who never wanted IPv6. Reproduced on a test server
    # by setting disable_ipv6=1 with an IPv6 address in the config.
    #
    # This can arrive without anyone touching the product: an owner
    # following a "speed up your network" guide, a hoster withdrawing
    # IPv6, an image shipped with it off, or a backup restored onto a
    # host that never had it.
    if config_has_ipv6 "${CONFIG_DEST}" && ! ipv6_usable; then
        log "WARNING: the configuration asks for IPv6 but this host has it disabled;"
        log "WARNING: bringing the tunnel up over IPv4 only. Clients keep working."
        log "WARNING: re-enable IPv6 on the host (net.ipv6.conf.all.disable_ipv6=0)"
        log "WARNING: and restart this container to carry IPv6 again."
        strip_ipv6 "${CONFIG_DEST}" > "${CONFIG_DEST}.noipv6" \
            && mv -f "${CONFIG_DEST}.noipv6" "${CONFIG_DEST}"
    fi
    chown root:root "${CONFIG_DEST}"
    chmod 0600 "${CONFIG_DEST}"
}

stop_tunnel() {
    awg-quick down "${IFACE}" >/dev/null 2>&1 || true
}

signal_handler() {
    log "received signal, bringing the tunnel down"
    stop_tunnel
    rm -f "${SYNCCONF_TMP}"
    trap - TERM INT
    exit 0
}

interface_alive() {
    ip link show "${IFACE}" >/dev/null 2>&1
}

uapi_alive() {
    awg show "${IFACE}" dump >/dev/null 2>&1
}

config_mtime() {
    stat -c %Y "${CONFIG_SRC}"
}

# Applies a changed configuration in place: strips wg-quick-only keys,
# feeds the rest to `awg syncconf` via a temporary file (session state is
# preserved by the daemon), and refreshes the runtime copy. Any failure
# is fatal so the container exits 1 and stops.
# --- маршруты с собственным размером (amnezia-vpn-server-wc2l) ---------
#
# Интерфейс awg0 один на всех, и раньше он был осторожным: размер выбирали по
# худшей последней миле, какая может встретиться кому угодно. Роутер на оптике
# получал от этого половину выигрыша — быструю отдачу и прежнее скачивание,
# потому что обратное направление ограничено интерфейсом.
#
# Теперь интерфейс поднят до того, что тянет сервер, а осторожное значение
# раздаётся маршрутом каждому клиенту отдельно. Панель пишет размеры
# комментариями в awg0.conf: ключа для них в AmneziaWG нет и быть не должно —
# это свойство маршрута, а не пира.
ROUTE_STATE="${AWG_ROUTE_STATE:-/run/amnezia-awg-routes}"
IP_BIN="${AWG_IP_BIN:-ip}"

# route_mtu_plan: печатает «сеть размер» для каждого адреса каждого пира.
# Общее значение берётся из [Interface], собственное — из блока пира.
route_mtu_plan() {
    awk '
        function flush_peer(   i, n, parts, entry) {
            if (!in_peer || allowed == "") return
            n = split(allowed, parts, ",")
            for (i = 1; i <= n; i++) {
                entry = parts[i]
                gsub(/^[[:space:]]+|[[:space:]]+$/, "", entry)
                if (entry == "") continue
                if (peer_mtu != "") print entry, peer_mtu
                else if (common != "") print entry, common
            }
            allowed = ""; peer_mtu = ""
        }
        /^[[:space:]]*#[[:space:]]*amnezia-route-mtu[[:space:]]*=/ {
            value = $0
            sub(/.*=[[:space:]]*/, "", value)
            gsub(/[[:space:]]/, "", value)
            if (in_peer) peer_mtu = value; else common = value
            next
        }
        /^[[:space:]]*\[Peer\][[:space:]]*$/ { flush_peer(); in_peer = 1; next }
        /^[[:space:]]*\[/ { flush_peer(); in_peer = 0; next }
        /^[[:space:]]*[Aa][Ll][Ll][Oo][Ww][Ee][Dd][Ii][Pp][Ss][[:space:]]*=/ {
            value = $0
            sub(/.*=[[:space:]]*/, "", value)
            allowed = value
            next
        }
        END { flush_peer() }
    ' "$1"
}

# route_family: -6 для адреса с двоеточием, пусто для остальных.
route_family() {
    case "$1" in
        *:*) printf -- '-6\n' ;;
        *) printf '\n' ;;
    esac
}

# apply_route_mtu: приводит маршруты к плану и снимает те, что остались от
# ушедших клиентов. Список применённого хранится в файле: гадать, какой
# маршрут наш, а какой поставил awg-quick, значит однажды снести чужой.
#
# Возвращает 1, если хоть один маршрут не принял нужный размер. Молчать об
# этом нельзя: интерфейс теперь не осторожный, и клиент без маршрута получит
# пакеты крупнее, чем тянет его последняя миля.
apply_route_mtu() {
    local plan cidr mtu family rc=0
    plan="$(route_mtu_plan "${CONFIG_DEST}")" || return 1

    # Сначала снять лишнее: маршрут ушедшего клиента переживёт его удаление и
    # будет молча ограничивать чужой адрес, когда тот выдадут заново.
    if [ -f "${ROUTE_STATE}" ]; then
        while read -r cidr _; do
            [ -n "${cidr}" ] || continue
            printf '%s\n' "${plan}" | cut -d' ' -f1 | grep -qxF "${cidr}" && continue
            family="$(route_family "${cidr}")"
            # shellcheck disable=SC2086
            ${IP_BIN} ${family} route del "${cidr}" dev "${IFACE}" >/dev/null 2>&1 \
                && log "маршрут снят: ${cidr}"
        done < "${ROUTE_STATE}"
    fi

    printf '%s\n' "${plan}" | while read -r cidr mtu; do
        [ -n "${cidr}" ] && [ -n "${mtu}" ] || continue
        family="$(route_family "${cidr}")"
        # shellcheck disable=SC2086
        if ! ${IP_BIN} ${family} route replace "${cidr}" dev "${IFACE}" mtu "${mtu}" >/dev/null 2>&1; then
            log "error: не удалось задать размер ${mtu} для ${cidr}"
            exit 1
        fi
        # Проверяем, а не верим: маршрут, который молча не применился,
        # выглядит точно так же, как применившийся.
        # shellcheck disable=SC2086
        if ! ${IP_BIN} ${family} route show "${cidr}" dev "${IFACE}" 2>/dev/null | grep -q "mtu ${mtu}"; then
            log "error: маршрут ${cidr} не принял размер ${mtu}"
            exit 1
        fi
    done || rc=1

    printf '%s\n' "${plan}" > "${ROUTE_STATE}" 2>/dev/null || true
    return "${rc}"
}

# config_device_mtu: размер интерфейса, каким его назначила панель.
config_device_mtu() {
    sed -n 's/^[[:space:]]*MTU[[:space:]]*=[[:space:]]*//p' "${CONFIG_DEST}" \
        | head -1 | tr -d '[:space:]'
}

# --- предел скорости на клиента (amnezia-vpn-server-jzzu) --------------
#
# Плечо до клиента может терять пакеты под нагрузкой: измерено 12-18% там, где
# норма меньше 0.1%. Причина вне сервера, но следствие лечится здесь. Предел
# вдвое сокращает потери при той же полезной скорости — то есть перестаёт
# тратиться седьмая часть канала, и вместе с ней уходят задержка и дрожание.
# Скорость он НЕ увеличивает, и обещать этого нельзя.
#
# Предел на клиента, а не общий: канал сервера здоров, а плохо конкретному
# плечу. Общий наказал бы всех за одного.
RATE_STATE="${AWG_RATE_STATE:-/run/amnezia-awg-rates}"
TC_BIN="${AWG_TC_BIN:-tc}"

# rate_plan: печатает «сеть мегабиты» для каждого адреса ограниченного пира.
# Общего предела нет, поэтому и разбирать в [Interface] нечего.
rate_plan() {
    awk '
        function flush_peer(   i, n, parts, entry) {
            if (!in_peer || allowed == "" || rate == "") { allowed=""; rate=""; return }
            n = split(allowed, parts, ",")
            for (i = 1; i <= n; i++) {
                entry = parts[i]
                gsub(/^[[:space:]]+|[[:space:]]+$/, "", entry)
                if (entry != "") print entry, rate
            }
            allowed = ""; rate = ""
        }
        /^[[:space:]]*#[[:space:]]*amnezia-rate[[:space:]]*=/ {
            value = $0
            sub(/.*=[[:space:]]*/, "", value)
            gsub(/[[:space:]]/, "", value)
            if (in_peer) rate = value
            next
        }
        /^[[:space:]]*\[Peer\][[:space:]]*$/ { flush_peer(); in_peer = 1; next }
        /^[[:space:]]*\[/ { flush_peer(); in_peer = 0; next }
        /^[[:space:]]*[Aa][Ll][Ll][Oo][Ww][Ee][Dd][Ii][Pp][Ss][[:space:]]*=/ {
            value = $0
            sub(/.*=[[:space:]]*/, "", value)
            allowed = value
            next
        }
        END { flush_peer() }
    ' "$1"
}

# sync_rates: приводит очереди к плану.
#
# ПЛАН ПУСТ — НЕ ТРОГАЕМ НИЧЕГО. Корневая дисциплина заводит очередь там, где
# её не было (awg0 живёт с noqueue), и ставить её ради никого значит менять
# поведение всем сразу.
sync_rates() {
    local plan cidr rate family classid n=0
    plan="$(rate_plan "${CONFIG_DEST}")" || return 0

    if [ -z "${plan}" ]; then
        if [ -s "${RATE_STATE}" ]; then
            ${TC_BIN} qdisc del dev "${IFACE}" root >/dev/null 2>&1 \
                && log "пределы скорости сняты: их больше никому не задано"
            : > "${RATE_STATE}" 2>/dev/null || true
        fi
        return 0
    fi

    # Дисциплина пересобирается целиком: классы и фильтры дешевле выписать
    # заново, чем выяснять, чем нынешний набор отличается от нужного.
    #
    # `|| true` не для красоты: скрипт живёт под `set -e`, а удалять здесь
    # обычно нечего — очереди ещё нет. Без этого первый же вызов убивал весь
    # entrypoint, туннель падал, а awg0 оставался на хосте, и следующий
    # подъём упирался в «уже существует». Один незакрытый возврат превращался
    # в бесконечный перезапуск (amnezia-vpn-server-jzzu).
    ${TC_BIN} qdisc del dev "${IFACE}" root >/dev/null 2>&1 || true
    if ! ${TC_BIN} qdisc add dev "${IFACE}" root handle 1: htb default 99 r2q 100 >/dev/null 2>&1; then
        log "error: не удалось завести очередь на ${IFACE}; пределы скорости не применены"
        : > "${RATE_STATE}" 2>/dev/null || true
        return 1
    fi
    # Класс по умолчанию — без предела: те, кому ничего не задано, не должны
    # заметить разницы.
    ${TC_BIN} class add dev "${IFACE}" parent 1: classid 1:99 htb rate 10gbit ceil 10gbit >/dev/null 2>&1 || true

    printf '%s\n' "${plan}" | while read -r cidr rate; do
        [ -n "${cidr}" ] && [ -n "${rate}" ] || continue
        n=$((n + 1))
        classid="1:$((n + 100))"
        family="$(route_family "${cidr}")"
        if ! ${TC_BIN} class add dev "${IFACE}" parent 1: classid "${classid}" \
                htb rate "${rate}mbit" ceil "${rate}mbit" burst 64k >/dev/null 2>&1; then
            log "error: не удалось задать предел ${rate} Мбит для ${cidr}"
            exit 1
        fi
        # fq_codel под каждым классом: очередь должна быть короткой, иначе
        # выигрыш по задержке съедается ею же.
        ${TC_BIN} qdisc add dev "${IFACE}" parent "${classid}" fq_codel >/dev/null 2>&1 || true
        case "${family}" in
            -6) ${TC_BIN} filter add dev "${IFACE}" protocol ipv6 parent 1:0 prio 2 \
                    u32 match ip6 dst "${cidr}" flowid "${classid}" >/dev/null 2>&1 || true ;;
            *)  ${TC_BIN} filter add dev "${IFACE}" protocol ip parent 1:0 prio 1 \
                    u32 match ip dst "${cidr}" flowid "${classid}" >/dev/null 2>&1 || true ;;
        esac
        log "предел ${rate} Мбит для ${cidr}"
    done || {
        # Полумера хуже отсутствия: часть клиентов ограничена, часть нет, и
        # объяснить разницу потом нечем.
        log "WARNING: пределы разошлись не полностью; снимаю очередь целиком"
        ${TC_BIN} qdisc del dev "${IFACE}" root >/dev/null 2>&1 || true
        : > "${RATE_STATE}" 2>/dev/null || true
        return 1
    }

    printf '%s\n' "${plan}" > "${RATE_STATE}" 2>/dev/null || true
    return 0
}

# safe_device_mtu: общее осторожное значение из конфигурации. Оно и есть то,
# на что опускается интерфейс, если раздать маршруты не вышло.
safe_device_mtu() {
    sed -n 's/^[[:space:]]*#[[:space:]]*amnezia-route-mtu[[:space:]]*=[[:space:]]*//p' \
        "${CONFIG_DEST}" | head -1 | tr -d '[:space:]'
}

# sync_routes: применить план, а при неудаче — опустить интерфейс до
# осторожного значения. Так никому не станет хуже, чем было до этой
# возможности: выигрыш теряется, связь — нет.
sync_routes() {
    local safe device
    # Раскладывать нечего — не трогаем ничего: так выглядит развёртывание, где
    # потолок не измеряли и ни у кого нет своего размера.
    [ -n "$(route_mtu_plan "${CONFIG_DEST}")" ] || return 0
    safe="$(safe_device_mtu)"
    # Общего значения может не быть, а собственное у кого-то — быть: тогда
    # осторожным считается сам потолок, он же и есть сегодняшнее поведение.
    [ -n "${safe}" ] || safe="$(config_device_mtu)"
    # Размер устройства ставит awg-quick при подъёме, но горячая
    # перезагрузка до него не доходит: MTU — ключ awg-quick, и до syncconf
    # он не долетает. Без этой строки изменившийся потолок ждал бы
    # перезапуска контейнера, а маршруты с новым размером ядро бы не приняло.
    device="$(config_device_mtu)"
    if [ -n "${device}" ]; then
        ${IP_BIN} link set dev "${IFACE}" mtu "${device}" >/dev/null 2>&1 \
            || log "WARNING: не удалось задать ${IFACE} размер ${device}"
    fi
    if apply_route_mtu; then
        return 0
    fi
    log "WARNING: маршруты не разошлись; опускаю ${IFACE} до ${safe}, чтобы"
    log "WARNING: никто не получал пакеты крупнее, чем тянет его канал"
    ${IP_BIN} link set dev "${IFACE}" mtu "${safe}" >/dev/null 2>&1 \
        || log "error: не удалось опустить ${IFACE} до ${safe}"
    return 0
}

reload_config() {
    if [ ! -f "${CONFIG_SRC}" ]; then
        log "error: ${CONFIG_SRC} disappeared"
        exit 1
    fi
    if ! filter_syncconf_config "${CONFIG_SRC}" > "${SYNCCONF_TMP}"; then
        log "error: could not prepare syncconf input from ${CONFIG_SRC}"
        exit 1
    fi
    chmod 0600 "${SYNCCONF_TMP}"
    if ! awg syncconf "${IFACE}" "${SYNCCONF_TMP}"; then
        log "error: awg syncconf ${IFACE} failed; keeping the previous configuration"
        exit 1
    fi
    rm -f "${SYNCCONF_TMP}"
    install_config
    # Набор клиентов мог измениться, значит и набор маршрутов тоже.
    sync_routes
    # Предел скорости — удобство, а не условие работы туннеля: его неудача не
    # должна ронять связь.
    sync_rates || true
    log "configuration reloaded via awg syncconf"
}

wait_for_config

install_config

# Интерфейс переживает смерть контейнера: сеть у него хостовая, и awg0
# остаётся на хосте. Тогда awg-quick отказывается — «already exists», — и
# сервер не поднимется НИКОГДА: сторож перезапускает контейнер, а перезапуск и
# есть то, что не работает. Любая разовая ошибка после подъёма превращалась в
# отказ, из которого сервер сам не выходит (amnezia-vpn-server-544c).
#
# Живой awg0 в момент старта означает ровно одно: прошлый экземпляр умер без
# уборки. При штатной остановке его снимает обработчик сигнала, а другого
# владельца здесь взяться неоткуда — интерфейс создаёт только этот контейнер.
#
# Отвечающий UAPI не повод его беречь: им никто не управляет, изменения
# конфигурации к нему не применяются и статус не пишется. Сохранять такое
# состояние значило бы закреплять поломку.
if interface_alive; then
    log "${IFACE} остался от прошлого запуска; снимаю перед подъёмом"
    stop_tunnel
    if interface_alive; then
        # awg-quick down мог не справиться: он ходит по конфигурации, а она
        # с тех пор могла измениться. Интерфейс снимается напрямую.
        ip link del "${IFACE}" >/dev/null 2>&1 || true
    fi
    if interface_alive; then
        log "error: ${IFACE} существует и не снимается; подъём невозможен"
        exit 1
    fi
    log "${IFACE} снят; поднимаю заново"
fi

if ! awg-quick up "${IFACE}"; then
    log "error: awg-quick up ${IFACE} failed"
    exit 1
fi
LAST_MTIME="$(config_mtime)" || {
    log "error: cannot stat ${CONFIG_SRC}"
    exit 1
}

# Только после подъёма: размер маршрута не может быть больше размера
# устройства, а устройство появляется здесь.
sync_routes
sync_rates || true

generate_versions || true

trap signal_handler TERM INT

while true; do
    if ! interface_alive; then
        log "error: interface ${IFACE} is gone"
        exit 1
    fi
    if ! uapi_alive; then
        log "error: userspace AWG daemon behind UAPI socket for ${IFACE} is not responding"
        exit 1
    fi

    # M5: status generation runs after every successful UAPI health
    # check — at loop entry and then once per CHECK_INTERVAL. A config
    # reload does not trigger a separate generation; the next tick
    # rewrites status from the already-applied runtime.
    generate_status
    # Дважды подстрахованы: функция сама не падает, и её неудача всё равно не
    # прерывает цикл. Туннель важнее снимка.
    generate_dns_seen || true

    if [ "$(config_mtime)" != "${LAST_MTIME}" ]; then
        reload_config
        LAST_MTIME="$(config_mtime)" || {
            log "error: cannot stat ${CONFIG_SRC}"
            exit 1
        }
    fi

    sleep "${CHECK_INTERVAL}"
done
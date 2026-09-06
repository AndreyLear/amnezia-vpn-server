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

generate_dns_seen() {
    command -v nft >/dev/null 2>&1 || return 0
    local addrs tmp
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
    } > "${tmp}" && mv -f "${tmp}" "${DNS_SEEN_FILE}"
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
    log "configuration reloaded via awg syncconf"
}

wait_for_config

install_config

if ! awg-quick up "${IFACE}"; then
    log "error: awg-quick up ${IFACE} failed"
    exit 1
fi
LAST_MTIME="$(config_mtime)" || {
    log "error: cannot stat ${CONFIG_SRC}"
    exit 1
}

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
    generate_dns_seen

    if [ "$(config_mtime)" != "${LAST_MTIME}" ]; then
        reload_config
        LAST_MTIME="$(config_mtime)" || {
            log "error: cannot stat ${CONFIG_SRC}"
            exit 1
        }
    fi

    sleep "${CHECK_INTERVAL}"
done
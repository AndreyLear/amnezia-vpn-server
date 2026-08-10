#!/bin/bash
set -euo pipefail

IFACE=${AWG_IFACE:-awg0}
CONFIG_SRC=${CONFIG_SRC:-/config/awg0.conf}
CONFIG_DEST=${CONFIG_DEST:-/etc/amnezia/amneziawg/awg0.conf}
SYNCCONF_TMP=${SYNCCONF_TMP:-/tmp/awg0.syncconf.conf}
CONFIG_TIMEOUT=${AWG_CONFIG_TIMEOUT:-300}
CHECK_INTERVAL=${AWG_CHECK_INTERVAL:-5}

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

install_config() {
    cp "${CONFIG_SRC}" "${CONFIG_DEST}"
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

    if [ "$(config_mtime)" != "${LAST_MTIME}" ]; then
        reload_config
        LAST_MTIME="$(config_mtime)" || {
            log "error: cannot stat ${CONFIG_SRC}"
            exit 1
        }
    fi

    sleep "${CHECK_INTERVAL}"
done
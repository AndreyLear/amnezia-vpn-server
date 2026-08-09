#!/bin/bash
set -euo pipefail

IFACE=awg0
CONFIG_SRC=/config/awg0.conf
CONFIG_DEST=/etc/amnezia/amneziawg/awg0.conf
CONFIG_TIMEOUT=300
CHECK_INTERVAL=5

log() { echo "[awg] $*" >&2; }

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

stop_tunnel() {
    awg-quick down "${IFACE}" >/dev/null 2>&1 || true
}

signal_handler() {
    log "received signal, bringing the tunnel down"
    stop_tunnel
    trap - TERM INT
    exit 0
}

wait_for_config

cp "${CONFIG_SRC}" "${CONFIG_DEST}"
chown root:root "${CONFIG_DEST}"
chmod 0600 "${CONFIG_DEST}"

if ! awg-quick up "${IFACE}"; then
    log "error: awg-quick up ${IFACE} failed"
    exit 1
fi

trap signal_handler TERM INT

while true; do
    if ! ip link show "${IFACE}" >/dev/null 2>&1; then
        log "error: interface ${IFACE} is gone"
        exit 1
    fi
    if ! awg show "${IFACE}" dump >/dev/null 2>&1; then
        log "error: userspace AWG daemon behind UAPI socket for ${IFACE} is not responding"
        exit 1
    fi
    sleep "${CHECK_INTERVAL}"
done
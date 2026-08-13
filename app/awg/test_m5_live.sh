#!/bin/bash
# LIVE e2e for M5 status (items 19–21 of M5_AUDIT.md §11): full stack in
# Docker with the real amneziawg-go userspace daemon and real
# amneziawg-tools (awg/awg-quick):
#
#  19. container startup → status/status.json appears with
#      has_interface=true and the configured peer;
#  20. config reload (new peer) → the next CHECK_INTERVAL tick rewrites
#      status.json with the new peer;
#  21. daemon killed → the container exits 1 (existing M3.2 behavior,
#      status.json is NOT rewritten by the dead daemon).
#
# Additionally verified: mode 0600, secrets (private/preshared keys)
# absent from status.json, no temp file.
#
# Docker daemon required.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${SCRIPT_DIR}"

IMAGE=${AWG_CI_IMAGE:-local/amnezia/awg:test}
CT_NAME=${AWG_CI_CT:-awg-m5-e2e-$$}

WG_PRIV="4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
WG_PUB="qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6R6p+bqnw="    # public key of the above
PEER_PRIV_1="gOYtz2OZILLXQm5hQMqF/e8fP02sqoy6FKLsqI0nwWo="
PEER_PUB_1="mVPxUV7JQ4Lqoc5rdzxywINe3TxnWwTYjjyA47llt1k="
PEER_PRIV_2="AKue9KxHX7RapZHVBVm7DV2cmGmVF/OuDOdPjS5cHEM="
PEER_PUB_2="a1IqKSrVxcXN16cMZIUZ6kof0oU0kvwb0w6Q8bgGggQ="

cleanup() {
    set +e
    docker rm -f "${CT_NAME}" >/dev/null 2>&1
}
trap cleanup EXIT

echo "==> building image ${IMAGE}"
set -a; source ../../versions.lock; set +a
docker build -q -f Dockerfile \
    --build-arg "GO_VERSION=${GO_VERSION}" \
    --build-arg "ALPINE_VERSION=${ALPINE_VERSION}" \
    --build-arg "AMNEZIAWG_GO_VERSION=${AMNEZIAWG_GO_VERSION}" \
    --build-arg "AMNEZIAWG_GO_COMMIT=${AMNEZIAWG_GO_COMMIT}" \
    --build-arg "AMNEZIAWG_TOOLS_VERSION=${AMNEZIAWG_TOOLS_VERSION}" \
    --build-arg "AMNEZIAWG_TOOLS_COMMIT=${AMNEZIAWG_TOOLS_COMMIT}" \
    ../.. -t "${IMAGE}"

CT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/awg-m5-e2e-XXXXXX")"
mkdir -p "${CT_DIR}/status"
cat > "${CT_DIR}/awg0.conf" <<EOF
[Interface]
PrivateKey = ${WG_PRIV}
Address = 10.9.0.1/24
ListenPort = 51820
MTU = 1420
DNS = 1.1.1.1
Table = off
Jc = 3
Jmin = 21
Jmax = 31
S1 = 904
S2 = 737

[Peer]
PublicKey = ${PEER_PUB_1}
AllowedIPs = 10.9.0.2/32
EOF

echo "==> starting container"
if ! docker create --name "${CT_NAME}" --cap-add=NET_ADMIN \
        --device /dev/net/tun \
        -v "${CT_DIR}:/config" \
        -v "${CT_DIR}/status:/status" \
        -e AWG_CONFIG_TIMEOUT=60 "${IMAGE}" >/dev/null; then
    echo "FAIL: docker create"
    exit 1
fi
docker start "${CT_NAME}" >/dev/null

STATUS="${CT_DIR}/status/status.json"

echo "==> [19] waiting for interface awg0"
I=0
while ! docker exec "${CT_NAME}" ip link show awg0 >/dev/null 2>&1; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && { echo "FAIL: interface awg0 never came up"; docker logs "${CT_NAME}"; exit 1; }
    sleep 1
done

echo "==> [19] waiting for status/status.json"
I=0
while [ ! -f "${STATUS}" ]; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && { echo "FAIL: status.json never appeared"; docker logs "${CT_NAME}"; exit 1; }
    sleep 1
done
sleep 2

echo "==> [19] verifying status.json content"
FAIL=""
grep -q '"schema":"v1"' "${STATUS}" || FAIL="${FAIL} schema"
grep -q '"has_interface":true' "${STATUS}" || FAIL="${FAIL} has_interface"
grep -q '"iface":"awg0"' "${STATUS}" || FAIL="${FAIL} iface"
grep -q "\"public_key\":\"${PEER_PUB_1}\"" "${STATUS}" || FAIL="${FAIL} peer-1"
grep -q "\"awg_params\"" "${STATUS}" || FAIL="${FAIL} awg_params"
grep -q '"private_key"' "${STATUS}" && FAIL="${FAIL} private_key-leak"
grep -q '"preshared_key"' "${STATUS}" && FAIL="${FAIL} preshared_key-leak"
grep -q "${WG_PRIV}" "${STATUS}" && FAIL="${FAIL} private-key-material"
grep -q "${PEER_PRIV_1}\|${PEER_PRIV_2}" "${STATUS}" && FAIL="${FAIL} peer-key-material"
if [ -n "${FAIL}" ]; then
    echo "FAIL: status.json wrong:${FAIL}"; cat "${STATUS}"
    exit 1
fi
echo "OK: status.json valid, secrets absent"

MODE="$(docker exec "${CT_NAME}" stat -c %a /status/status.json)"
[ "${MODE}" = "600" ] || { echo "FAIL: status.json mode = ${MODE}, want 600"; exit 1; }
echo "OK: status.json mode 0600"

TMPLEFT="$(docker exec "${CT_NAME}" ls /status | grep -c '\.tmp-' || true)"
[ "${TMPLEFT}" = "0" ] || { echo "FAIL: temp file left in /status"; exit 1; }
echo "OK: no temp file in /status"

echo "==> [20] reloading config: adding peer 2"
cat >> "${CT_DIR}/awg0.conf" <<EOF

[Peer]
PublicKey = ${PEER_PUB_2}
AllowedIPs = 10.9.0.3/32
EOF
touch "${CT_DIR}/awg0.conf"

echo "==> [20] waiting for peer 2 in status.json (up to 35s)"
I=0
while ! grep -q "${PEER_PUB_2}" "${STATUS}" 2>/dev/null; do
    I=$((I + 1))
    [ "${I}" -gt 35 ] && { echo "FAIL: peer 2 never reached status.json"; docker logs "${CT_NAME}"; exit 1; }
    sleep 1
done
grep -q "\"public_key\":\"${PEER_PUB_1}\"" "${STATUS}" || { echo "FAIL: peer 1 lost from status.json"; exit 1; }
echo "OK: both peers in status.json after reload"

echo "==> [21] killing the AWG daemon"
KILLED=0
if docker exec "${CT_NAME}" pkill -TERM -x amneziawg-go 2>/dev/null \
    || docker exec "${CT_NAME}" sh -c 'kill $(pgrep -f amneziawg-go | head -1)' 2>/dev/null; then
    KILLED=1
    echo "==> [21] userspace daemon killed"
else
    echo "==> [21] no userspace daemon; kernel mode (module loaded), removing interface awg0"
    docker exec "${CT_NAME}" ip link del awg0 2>/dev/null
fi
if [ "${KILLED}" = "0" ] && docker exec "${CT_NAME}" ip link show awg0 >/dev/null 2>&1; then
    echo "FAIL: could not find amneziawg-go nor remove awg0"
    exit 1
fi

echo "==> [21] waiting for container exit (up to 60s)"
set +e
I=0
while docker inspect -f '{{.State.Running}}' "${CT_NAME}" 2>/dev/null | grep -q true; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && { echo "FAIL: container did not exit"; docker logs "${CT_NAME}"; exit 1; }
    sleep 1
done
RC="$(docker inspect -f '{{.State.ExitCode}}' "${CT_NAME}")"
set -e
if [ "${RC}" = "0" ]; then
    echo "FAIL: container exited 0 after daemon death (M3.2 lifecycle broken)"
    docker logs "${CT_NAME}"
    exit 1
fi
echo "OK: container exited ${RC} (M3.2 lifecycle preserved)"

grep -q '"has_interface":true' "${STATUS}" \
    || { echo "FAIL: status.json rewritten to has_interface=false by a dead daemon"; exit 1; }
echo "OK: status.json left stale, not falsified"

echo
echo "==> e2e OK: startup status (19), reload update (20), daemon death → exit 1 (21)"
exit 0
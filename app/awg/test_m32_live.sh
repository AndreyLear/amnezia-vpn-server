#!/bin/bash
# LIVE e2e for the awg runtime reload (M3.2): full stack in Docker —
# real amneziawg-go userspace daemon, real amneziawg-tools
# (awg/awg-quick). The container is started with a wireguard config, the
# config is edited (new peer), and we verify the reload happened: the
# peer appears in `awg show` and disappears when the config drops a
# peer before the next touch.
#
# Docker daemon required.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${SCRIPT_DIR}"

IMAGE=${AWG_CI_IMAGE:-local/amnezia/awg:test}
CT_NAME=${AWG_CI_CT:-awg-m32-e2e-$$}
CT_CFG=${AWG_CI_CFG:-/config/awg0.conf}

WG_PRIV="4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
WG_PUB="qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6R6p+bqnw="    # wg pubkey of the above
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

CT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/awg-e2e-XXXXXX")"
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
        -e AWG_CONFIG_TIMEOUT=60 "${IMAGE}" >/dev/null; then
    echo "FAIL: docker create"
    exit 1
fi
docker start "${CT_NAME}" >/dev/null

echo "==> waiting for awg-quick up"
I=0
while ! docker exec "${CT_NAME}" ip link show awg0 >/dev/null 2>&1; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && { echo "FAIL: interface awg0 never came up"; docker logs "${CT_NAME}"; exit 1; }
    sleep 1
done
echo "==> interface awg0 is up"

echo "==> settling (no-op reload within the grace window)"
sleep 8

HANDLES="$(docker exec "${CT_NAME}" cat /sys/class/net/awg0/ifindex)"
echo "==> interface ifindex: ${HANDLES}"

DUMP0="$(docker exec "${CT_NAME}" awg show awg0 dump)"
N_PEERS0="$(echo "${DUMP0}" | wc -l)"
echo "==> peers before edit: $((N_PEERS0 - 1))"

echo "==> editing config: adding peer 2, removing peer 1"
sed -i '' "/^\[Peer\]$/,/AllowedIPs = 10.9.0.2\/32/d" "${CT_DIR}/awg0.conf" 2>/dev/null \
    || sed -i "/^\[Peer\]$/,/AllowedIPs = 10.9.0.2\/32/d" "${CT_DIR}/awg0.conf"
cat >> "${CT_DIR}/awg0.conf" <<EOF

[Peer]
PublicKey = ${PEER_PUB_2}
AllowedIPs = 10.9.0.3/32
EOF
touch "${CT_DIR}/awg0.conf"

echo "==> waiting for reload (up to 20s)"
I=0
while true; do
    if docker exec "${CT_NAME}" awg show awg0 dump | grep -q "${PEER_PUB_2}"; then
        break
    fi
    I=$((I + 1))
    [ "${I}" -gt 20 ] && { echo "FAIL: peer 2 never appeared"; docker logs "${CT_NAME}"; exit 1; }
    sleep 1
done
sleep 3

echo "==> verifying full peer list after sync"
DUMP1="$(docker exec "${CT_NAME}" awg show awg0 dump)"
echo "-- dump:"; echo "${DUMP1}"
if echo "${DUMP1}" | grep -q "${PEER_PUB_1}"; then
    echo "FAIL: old peer 1 still present after removal"
    exit 1
fi
if ! echo "${DUMP1}" | grep -q "${PEER_PUB_2}"; then
    echo "FAIL: new peer 2 missing"
    exit 1
fi
echo "==> verifying interface survived reload (no restart, same ifindex)"
HANDLES_AFTER="$(docker exec "${CT_NAME}" cat /sys/class/net/awg0/ifindex)"
[ "${HANDLES}" != "${HANDLES_AFTER}" ] && { echo "FAIL: interface ifindex changed (tunnel restarted?)"; exit 1; }

echo
echo "==> e2e OK: live runtime applied config update via awg syncconf (peer 1 removed, peer 2 added, no restart)"
exit 0
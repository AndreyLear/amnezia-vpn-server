#!/bin/bash
# LIVE e2e for M8.7 (AUDITS/M8.7_AUDIT.md §live): the backup storage
# integration in the real compose stack:
#
#   static part (no daemon):
#     docker compose config --quiet  → the file parses and interpolates
#     with versions.lock;
#   live part (Docker daemon required):
#     panel sees /data/backups as a rw mount (write probe + real
#     `panel backup create` through the bind mount);
#     the archive lands in the host bind dir with 0600 and no
#     plaintext staging survives (M8.2 through the mount; the dir-0700
#     creation path is unit-covered by internal/backup);
#     awg has no /data/backups access: its mount list contains no
#     backups mount and the directory does not exist inside awg;
#     the runtime mount sets match the compose contract (panel: rw
#     /data /config /data/backups + ro /status; awg: ro /config +
#     rw /status).
#
# Everything runs in a temp directory (data/config/status/backups are
# never written inside the repo). Docker daemon required.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO}"

set -a; source versions.lock; set +a

echo "==> [0] compose config parses with versions.lock"
docker compose --env-file versions.lock config --quiet || {
    echo "FAIL: docker compose config"
    exit 1
}
echo "OK: compose config"

PROJ="m8-e2e-$$"
M8_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-m8-e2e-XXXXXX")"
M8_DATA="${M8_ROOT}/data"; M8_CONFIG="${M8_ROOT}/config"; M8_STATUS="${M8_ROOT}/status"; M8_BACKUPS="${M8_ROOT}/backups"
mkdir -p "${M8_DATA}" "${M8_CONFIG}" "${M8_STATUS}" "${M8_BACKUPS}"
# All bind sources are pre-created host-side (same pattern as
# test_m6_live.sh): Docker Desktop does not materialize a missing
# bind source on the host, which would leave the mount without a
# host backing. The 0700-on-creation path is unit-covered by
# internal/backup TestCreatePermissions; the live contract here is
# the rw mount and the 0600 archives.

M8_ADMIN="m8-admin"
M8_PASSWORD="m8-password-for-backup-42"
# A real x25519 recipient (public by design — it may live on the VPS).
# The matching identity stays out of the script: create only needs the
# recipient.
M8_RECIPIENT="age194rhdm90su5g8mt0z5qntsyukc9t36xemcmvhx7l3k75pdzyfudsthmshy"

# stat_mode prints the octal mode of a path (GNU on Linux CI, BSD on
# macOS hosts).
stat_mode() {
    if stat -c '%a' / >/dev/null 2>&1; then
        stat -c '%a' "$1"
    else
        stat -f '%Lp' "$1"
    fi
}

cleanup() {
    set +e
    if [ -n "${M8_KEEP:-}" ]; then
        echo "==> M8_KEEP set: leaving stack for debugging (project ${PROJ}, dir ${M8_ROOT})"
        return
    fi
    docker compose -p "${PROJ}" -f compose.yaml -f "${M8_ROOT}/override.yaml" down -v >/dev/null 2>&1
    rm -rf "${M8_ROOT}"
}
trap cleanup EXIT

cat > "${M8_ROOT}/override.yaml" <<EOF
services:
  panel:
    environment:
      - AGE_RECIPIENT=${M8_RECIPIENT}
    volumes:
      - ${M8_DATA}:/data
      - ${M8_CONFIG}:/config
      - ${M8_STATUS}:/status:ro
      - ${M8_BACKUPS}:/data/backups
  panel-init:
    volumes:
      - ${M8_DATA}:/data
      - ${M8_CONFIG}:/config
  awg:
    volumes:
      - ${M8_CONFIG}:/config:ro
      - ${M8_STATUS}:/status
EOF

COMPOSE=(docker compose -p "${PROJ}" -f compose.yaml -f "${M8_ROOT}/override.yaml")

fail() {
    echo "FAIL: $1"
    "${COMPOSE[@]}" logs --no-color 2>/dev/null | tail -40 || true
    exit 1
}

echo "==> building panel and awg images"
"${COMPOSE[@]}" build panel awg >/dev/null || fail "image build"

echo "==> [1] panel-init (migrate + awg0.conf)"
"${COMPOSE[@]}" run --rm panel-init /app/panel server init 10.8.0.1/24 51820 \
    --endpoint vpn.example.com:51820 >/dev/null 2>&1 || fail "server init"
"${COMPOSE[@]}" up -d panel-init >/dev/null
CT_NAME="$("${COMPOSE[@]}" ps -a --format '{{.Name}}' | grep 'panel-init' | head -1)"
[ -n "${CT_NAME}" ] || fail "panel-init container not found"
I=0
while [ "$(docker inspect -f '{{.State.Running}}' "${CT_NAME}" 2>/dev/null || echo missing)" = "true" ]; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && fail "panel-init did not complete"
    sleep 1
done
RC="$(docker inspect -f '{{.State.ExitCode}}' "${CT_NAME}")"
[ "${RC}" = "0" ] || fail "panel-init exited ${RC}: $(docker logs "${CT_NAME}" 2>&1 | tail -5)"

echo "==> [2] panel up, /data/backups is a writable mount"
"${COMPOSE[@]}" up -d panel >/dev/null || fail "panel start"
"${COMPOSE[@]}" exec -T panel sh -c '
    d=/data/backups
    [ -d "$d" ] || { echo "no /data/backups"; exit 1; }
    [ -w "$d" ] || { echo "/data/backups not writable"; exit 1; }
    : > "$d/.m8-probe" && rm -f "$d/.m8-probe"
' || fail "panel cannot write /data/backups"
echo "OK: panel sees a writable /data/backups mount"

echo "==> [3] real backup create through the mount (M8.2 perms)"
# The bind-mount root is created by Docker (0755 root); the M8.2 0700
# guarantee applies when the panel process creates the directory
# itself (unit-covered by internal/backup TestCreatePermissions). The
# live contract: the mount is writable, the archive lands in the host
# bind dir with 0600, and no plaintext staging survives.
"${COMPOSE[@]}" exec -T panel /app/panel backup create >/dev/null 2>&1 || fail "panel backup create"
ARCHIVE="$("${COMPOSE[@]}" exec -T panel /app/panel backup list | head -1)"
[ -n "${ARCHIVE}" ] || fail "backup list empty"
[ -f "${M8_BACKUPS}/${ARCHIVE}" ] || fail "archive not visible on the host bind dir"
AMODE="$(stat_mode "${M8_BACKUPS}/${ARCHIVE}")"
[ "${AMODE}" = "600" ] || fail "archive mode = ${AMODE}, want 600"
case "$(ls -A "${M8_BACKUPS}")" in
    *staging*) fail "staging dir leaked into backups: $(ls -A "${M8_BACKUPS}")" ;;
esac
echo "OK: archive 0600 on the host bind dir, no staging leftover"

echo "==> [4] awg has no access to the backup mount"
"${COMPOSE[@]}" up -d awg >/dev/null || fail "awg start"
AWG_ID="$("${COMPOSE[@]}" ps -q awg | head -1)"
[ -n "${AWG_ID}" ] || fail "no awg container"
# The mount set is the access control: no backups mount → no access.
docker inspect -f '{{range .Mounts}}{{.Destination}} {{end}}' "${AWG_ID}" \
    | grep -q backups && fail "awg gained a backups mount"
# Runtime probe (needs the container actually running — on hosts
# without /dev/net/tun awg may stay exited; the mount isolation is
# already proven by inspect above).
if [ "$(docker inspect -f '{{.State.Running}}' "${AWG_ID}")" = "true" ]; then
    if "${COMPOSE[@]}" exec -T awg ls /data/backups >/dev/null 2>&1; then
        fail "awg can see /data/backups"
    fi
    echo "OK: awg running — /data/backups does not exist inside it"
else
    echo "OK: awg not running on this host — mount isolation proven by inspect"
fi

echo "==> [5] runtime mount sets match the compose contract"
PANEL_MOUNTS="$("${COMPOSE[@]}" ps -q panel | xargs -I{} docker inspect -f '{{range .Mounts}}{{.Destination}}:{{.RW}} {{end}}' {})"
AWG_MOUNTS="$("${COMPOSE[@]}" ps -q awg | xargs -I{} docker inspect -f '{{range .Mounts}}{{.Destination}}:{{.RW}} {{end}}' {})"
echo "panel mounts: ${PANEL_MOUNTS}"
echo "awg mounts:   ${AWG_MOUNTS}"
echo "${PANEL_MOUNTS}" | grep -q '/data:true'    || fail "panel lost /data"
echo "${PANEL_MOUNTS}" | grep -q '/config:true'   || fail "panel lost /config"
echo "${PANEL_MOUNTS}" | grep -q '/status:false'  || fail "panel /status lost or rw"
echo "${PANEL_MOUNTS}" | grep -q '/data/backups:true' || fail "panel backups mount missing or ro"
echo "${AWG_MOUNTS}" | grep -q '/config:false'    || fail "awg /config lost or rw"
echo "${AWG_MOUNTS}" | grep -q '/status:true'     || fail "awg /status lost"
echo "${AWG_MOUNTS}" | grep -q 'backups'          && fail "awg gained a backups mount"
echo "OK: mount sets match the M8.7 contract"

echo
echo "==> e2e OK: compose config, panel rw backups, 0700/0600, awg isolated"
exit 0

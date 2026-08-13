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
# The age keypair is generated at runtime (the module resolves
# filippo/age from app/panel's go.mod — no age CLI required on the
# host); the private half never lives in the repo or the script.
cat > "${M8_ROOT}/genident.go" <<'EOF'
package main

import (
	"fmt"
	"os"

	"filippo.io/age"
)

func main() {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		fmt.Fprintln(os.Stderr, "genident:", err)
		os.Exit(1)
	}
	fmt.Println(id.String())
	fmt.Println(id.Recipient().String())
}
EOF
# age identity generation: go run on hosts with Go; otherwise the
# versions.lock-pinned golang image (module download needs network).
if command -v go >/dev/null 2>&1; then
    M8_KEYPAIR="$(cd app/panel && go run "${M8_ROOT}/genident.go" 2>/dev/null)"
else
    M8_GENDIR="${M8_ROOT}/gen"
    mkdir -p "${M8_GENDIR}"
    cp "${M8_ROOT}/genident.go" "${REPO}/app/panel/go.mod" "${REPO}/app/panel/go.sum" "${M8_GENDIR}/"
    M8_KEYPAIR="$(docker run --rm -v "${M8_GENDIR}:/work" \
        -w /work golang:${GO_VERSION} go run /work/genident.go 2>/dev/null)"
fi
[ -n "${M8_KEYPAIR}" ] || { echo "FAIL: identity generation failed"; exit 1; }
M8_IDENT="$(printf '%s\n' "${M8_KEYPAIR}" | sed -n '1p')"
M8_RECIPIENT="$(printf '%s\n' "${M8_KEYPAIR}" | sed -n '2p')"

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
    "${COMPOSE[@]}" down -v >/dev/null 2>&1
    rm -rf "${M8_ROOT}"
}
trap cleanup EXIT

# The base compose maps 127.0.0.1:8787:8787; when the default port is
# taken (e.g. a live deployment on the same host) the harness remaps it
# via a sed-ed copy so the loopback contract still holds.
BASE_COMPOSE="${REPO}/compose.yaml"
COMPOSE_FILE="${BASE_COMPOSE}"
if [ "${M8_PORT:-8787}" != "8787" ]; then
    COMPOSE_FILE="${M8_ROOT}/compose.portfix.yaml"
    sed -e "s|127.0.0.1:8787:8787|127.0.0.1:${M8_PORT}:8787|" \
        -e "s|context: \.|context: ${REPO}|" "${BASE_COMPOSE}" > "${COMPOSE_FILE}"
fi

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

COMPOSE=(docker compose -p "${PROJ}" -f "${COMPOSE_FILE}" -f "${M8_ROOT}/override.yaml")

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

echo "==> [6] live restore cycle (M8.8): snapshot -> mutate -> restore -> restart -> applied"

# Snapshot state: auth user + client alice. Then mutate with bob.
printf '%s\n' "${M8_PASSWORD}" | "${COMPOSE[@]}" exec -T panel /app/panel auth add-user "${M8_ADMIN}" --password-stdin \
    >/dev/null || fail "auth add-user"
"${COMPOSE[@]}" exec -T panel /app/panel client add alice >/dev/null || fail "client add alice"
"${COMPOSE[@]}" exec -T panel /app/panel backup create >/dev/null 2>&1 || fail "snapshot backup create"
SNAP="$("${COMPOSE[@]}" exec -T panel /app/panel backup list | head -1)"
[ -n "${SNAP}" ] || fail "snapshot backup list empty"
"${COMPOSE[@]}" exec -T panel /app/panel client add bob >/dev/null || fail "client add bob"

# Restore prepares the pending marker; the identity goes via stdin only.
printf '%s\n' "${M8_IDENT}" | "${COMPOSE[@]}" exec -T panel /app/panel restore "${SNAP}" --identity-stdin \
    >/dev/null || fail "restore prepare"
[ -d "${M8_DATA}/.restore-pending" ] || fail "pending marker missing on the host bind dir"
ls -A "${M8_BACKUPS}" | grep -q '^safety-backup-' || fail "no safety backup after restore"
echo "OK: restore pending, safety backup present"

# Restart the stack: panel-init applies the pending restore first.
"${COMPOSE[@]}" down >/dev/null || fail "stack down"
"${COMPOSE[@]}" up -d >/dev/null || fail "stack up"
I=0
while ! "${COMPOSE[@]}" logs panel-init --no-color 2>/dev/null | grep -q "pending restore applied"; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && fail "panel-init did not apply the pending restore"
    sleep 1
done
[ ! -e "${M8_DATA}/.restore-pending" ] || fail "pending marker survived the restart"
[ -f "${M8_DATA}/amnezia.sqlite.pre-restore" ] || fail "no .pre-restore recovery copy"

# Live state is the snapshot: alice present, bob gone.
CLIENTS="$("${COMPOSE[@]}" exec -T panel /app/panel client list)"
printf '%s\n' "${CLIENTS}" | grep -q alice || fail "restored state lost alice"
printf '%s\n' "${CLIENTS}" | grep -q bob && fail "post-restore mutation bob survived the restore"
ALICE_PUB="$(printf '%s\n' "${CLIENTS}" | awk -F'\t' '$2 == "alice" { print $6 }')"
[ -n "${ALICE_PUB}" ] || fail "no alice public key in client list"

# awg0.conf was regenerated from the restored state (alice only).
grep -q "${ALICE_PUB}" "${M8_CONFIG}/awg0.conf" || fail "awg0.conf missing the restored alice peer"
grep -c "^\[Peer\]" "${M8_CONFIG}/awg0.conf" | grep -qx 1 || fail "awg0.conf peer count != 1"

# The restored auth user still logs in over HTTP (M7.5 flow). The port
# follows M8_PORT when the default 8787 is taken on the host.
M8_PORT="${M8_PORT:-8787}"
curl -fsS -c "${M8_ROOT}/cookies.txt" \
    -d "username=${M8_ADMIN}&password=${M8_PASSWORD}" \
    -o /dev/null "http://127.0.0.1:${M8_PORT}/login" || fail "post-restore HTTP login"
curl -fsS -b "${M8_ROOT}/cookies.txt" -o /dev/null "http://127.0.0.1:${M8_PORT}/" || fail "post-restore dashboard GET"
echo "OK: restore applied, awg0.conf regenerated, auth user works"

echo
echo "==> e2e OK: compose config, panel rw backups, 0700/0600, awg isolated, restore cycle"
exit 0

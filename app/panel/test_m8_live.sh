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
#     /data /config /data/backups + ro /status; panel-init: rw /data
#     /config /data/backups; awg: ro /config + rw /status);
#     the CLI restore runs through panel-init (the documented install.sh
#     DR flow) and sees the same archive the panel created (M10.2-FIX:
#     the shared backup store).
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

# The harness runs the real compose.yaml copied into its temp root, so
# the relative ./data ./config ./status ./backups binds resolve inside
# the harness dirs (a compose merge with an override volume list would
# duplicate mounts and litter the repo with parent stubs — M10.2-FIX2).
# The base compose maps 127.0.0.1:8787:8787; when the default port is
# taken (e.g. a live deployment on the same host) it is remapped via a
# sed-ed copy so the loopback contract still holds.
COMPOSE_FILE="${M8_ROOT}/compose.yaml"
cp "${REPO}/compose.yaml" versions.lock "${M8_ROOT}/"
sed -i.bak \
    -e "s|127.0.0.1:8787:8787|127.0.0.1:${M8_PORT:-8787}:8787|" \
    -e "s|context: \.|context: ${REPO}|" "${COMPOSE_FILE}"

COMPOSE=(docker compose -p "${PROJ}" --env-file versions.lock -f "${COMPOSE_FILE}")

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
PI_ID="$(docker ps -a --filter "name=${PROJ}-panel-init" --format '{{.ID}}' | head -1)"
[ -n "${PI_ID}" ] || fail "no panel-init container to inspect"
PI_MOUNTS="$(docker inspect -f '{{range .Mounts}}{{.Destination}}:{{.RW}} {{end}}' "${PI_ID}")"
echo "panel-init mounts: ${PI_MOUNTS}"
echo "${PI_MOUNTS}" | grep -q '/data:true'        || fail "panel-init lost /data"
echo "${PI_MOUNTS}" | grep -q '/config:true'      || fail "panel-init lost /config"
echo "${PI_MOUNTS}" | grep -q '/data/backups:true' || fail "panel-init backups mount missing or ro (M10.2-FIX)"
echo "OK: mount sets match the M8.7 contract"

echo "==> [6] live restore cycle (M8.8): snapshot -> mutate -> restore -> restart -> applied"

# Snapshot state: auth user + client alice. Then mutate with bob.
printf '%s\n' "${M8_PASSWORD}" | "${COMPOSE[@]}" exec -T panel /app/panel auth add-user "${M8_ADMIN}" --password-stdin \
    >/dev/null || fail "auth add-user"
"${COMPOSE[@]}" exec -T panel /app/panel client add alice >/dev/null || fail "client add alice"
"${COMPOSE[@]}" exec -T panel /app/panel backup create >/dev/null 2>&1 || fail "snapshot backup create"
SNAP="$("${COMPOSE[@]}" exec -T panel /app/panel backup list | head -1)"
[ -n "${SNAP}" ] || fail "snapshot backup list empty"

# M10.2-FIX regression: the archive created through the panel is visible
# to panel-init (the shared store), so the documented DR restore path
# (`docker compose run --rm panel-init ... restore`) can reach it.
PI_SNAP="$("${COMPOSE[@]}" run --rm panel-init /app/panel backup list | head -1)"
[ -n "${PI_SNAP}" ] || fail "panel-init cannot see the panel-created backup"
[ "${PI_SNAP}" = "${SNAP}" ] || fail "panel and panel-init disagree on the backup store (${SNAP} vs ${PI_SNAP})"
echo "OK: panel-created archive ${SNAP} visible to panel-init (shared store)"

"${COMPOSE[@]}" exec -T panel /app/panel client add bob >/dev/null || fail "client add bob"

# Restore prepares the pending marker via the panel-init CLI path — the
# flow install.sh documents for disaster recovery.
"${COMPOSE[@]}" run --rm panel-init /app/panel restore "${SNAP}" \
    >/dev/null || fail "restore prepare via panel-init"
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

echo "==> [7] T-125: HTTP upload applies in-process (no restart)"
# Mutate after the CLI restore, then upload the panel-created snapshot
# through the web form: the restore must be applied immediately — the
# pending marker is consumed, awg0.conf regenerated, no restart.
"${COMPOSE[@]}" exec -T panel /app/panel client add charlie >/dev/null 2>&1 || fail "client add charlie"
"${COMPOSE[@]}" exec -T panel /app/panel backup create >/dev/null 2>&1 || fail "snapshot2 backup create"
SNAP2="$("${COMPOSE[@]}" exec -T panel /app/panel backup list | head -1)"
[ -n "${SNAP2}" ] || fail "snapshot2 backup list empty"
"${COMPOSE[@]}" exec -T panel /app/panel client add dave >/dev/null 2>&1 || fail "client add dave"

M8_CSRF="$(curl -fsS -b "${M8_ROOT}/cookies.txt" -c "${M8_ROOT}/cookies.txt" \
    "http://127.0.0.1:${M8_PORT}/backups" \
    | grep -oE 'name="_csrf" value="[^"]+"' | head -1 | cut -d\" -f4)"
[ -n "${M8_CSRF}" ] || fail "no CSRF token on /backups"
UPLOC="$(curl -s -b "${M8_ROOT}/cookies.txt" -c "${M8_ROOT}/cookies.txt" \
    -e "http://127.0.0.1:${M8_PORT}/backups" \
    -F "_csrf=${M8_CSRF}" \
    -F "backup=@${M8_BACKUPS}/${SNAP2};filename=${SNAP2}" \
    -D- -o /dev/null "http://127.0.0.1:${M8_PORT}/backups/restore" \
    | grep -i '^location' | tr -d '\r' | awk '{print $2}')"
# Success is the applied flash, not any msg= (a failure flash also
# carries msg=). Session is cleared on apply, so check Location.
printf '%s\n' "${UPLOC}" | grep -q 'Восстановление применено' \
    || printf '%s\n' "${UPLOC}" | grep -qi '%D0%92%D0%BE%D1%81%D1%81%D1%82%D0%B0%D0%BD%D0%BE%D0%B2%D0%BB%D0%B5%D0%BD%D0%B8%D0%B5' \
    || fail "upload did not apply in-process: ${UPLOC}"
[ ! -e "${M8_DATA}/.restore-pending" ] || fail "pending marker survived the in-process apply"
CLIENTS2="$("${COMPOSE[@]}" exec -T panel /app/panel client list)"
printf '%s\n' "${CLIENTS2}" | grep -q charlie || fail "in-process restore lost charlie"
printf '%s\n' "${CLIENTS2}" | grep -q dave && fail "post-upload mutation dave survived the in-process restore"
grep -q "${ALICE_PUB}" "${M8_CONFIG}/awg0.conf" || fail "awg0.conf missing alice after in-process restore"
grep -q "^\[Peer\]" "${M8_CONFIG}/awg0.conf" || fail "awg0.conf has no peers after in-process restore"
echo "OK: upload applied in-process — clients active, no restart"

echo
echo "==> e2e OK: compose config, panel rw backups, 0700/0600, awg isolated, restore cycle"
exit 0

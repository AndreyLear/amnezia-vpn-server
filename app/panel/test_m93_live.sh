#!/bin/bash
# LIVE e2e for M9.3 (AUDITS/M9.3_AUDIT.md): the init data-loss guards in
# the real compose stack.
#
#   static part (no daemon):
#     docker compose config --quiet  → the file parses and interpolates
#     with versions.lock;
#   live part (Docker daemon required):
#     fresh install: init on an empty data dir succeeds without a
#       sentinel and without a boot snapshot;
#     server init writes the .server-initialized sentinel (0600);
#     every successful init takes a boot snapshot (0600), rotation
#       keeps the newest three;
#     sentinel present + database missing → init refuses, does NOT
#       recreate the schema, and the boot snapshot is the recovery
#       path (copy back → init succeeds, awg0.conf regenerated);
#     sentinel present + schema without a server row (silent wipe) →
#       init refuses with the no-server-row diagnosis;
#     initialized deployment without a sentinel (upgrade path) →
#       init self-heals the marker;
#     full wipe (db + sentinel) → fresh install path still works.
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

PROJ="m93-e2e-$$"
M93_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-m93-e2e-XXXXXX")"
M93_DATA="${M93_ROOT}/data"; M93_CONFIG="${M93_ROOT}/config"; M93_STATUS="${M93_ROOT}/status"; M93_BACKUPS="${M93_ROOT}/backups"
mkdir -p "${M93_DATA}" "${M93_CONFIG}" "${M93_STATUS}" "${M93_BACKUPS}"

# M10.2-FIX2: the harness runs the real compose.yaml copied next to its
# temp dirs, so the relative ./data ./config ./status ./backups binds
# resolve inside the harness root (a compose merge with an override
# volume list would duplicate mounts and litter the repo with parent
# stubs; the copied file keeps the production mount contract exact).
cp compose.yaml versions.lock "${M93_ROOT}/"
# The copied compose keeps the production topology; the build context
# must point back at the repo (the harness dir has no app/ tree).
sed -i.bak -e "s|context: \.|context: ${REPO}|" "${M93_ROOT}/compose.yaml"

COMPOSE=(docker compose -p "${PROJ}" --env-file versions.lock -f "${M93_ROOT}/compose.yaml")

fail() {
    echo "FAIL: $1"
    "${COMPOSE[@]}" logs --no-color 2>/dev/null | tail -40 || true
    exit 1
}

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
    if [ -n "${M93_KEEP:-}" ]; then
        echo "==> M93_KEEP set: leaving stack for debugging (project ${PROJ}, dir ${M93_ROOT})"
        return
    fi
    "${COMPOSE[@]}" down -v >/dev/null 2>&1
    rm -rf "${M93_ROOT}"
}
trap cleanup EXIT

echo "==> building panel image"
"${COMPOSE[@]}" build panel >/dev/null || fail "panel image build"

echo "==> [1] fresh install: init on an empty data dir follows M3"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" && rc=0 || rc=$?
[ "${rc}" = "1" ] || fail "fresh init exit = ${rc}, want 1 (M3 no-server-row): ${out}"
case "${out}" in *"no server row"*) ;; *) fail "fresh init stdout: ${out}" ;; esac
[ -f "${M93_DATA}/.server-initialized" ] && fail "fresh install wrote a sentinel"
# .restore-apply.lock is the persistent advisory lock (kernel-released
# flock), by design never removed; it must be the only extra file.
# `backups` is the Docker-created parent mountpoint for the nested
# ./backups:/data/backups bind (M10.2-FIX2); Docker materializes it
# inside the /data bind source — production behaves the same.
LIVE_FILES="$(ls -A "${M93_DATA}" | grep -v '^\.restore-apply\.lock$' | grep -v '^backups$' | sort)"
[ "${LIVE_FILES}" = "amnezia.sqlite" ] || fail "fresh data dir content: $(ls -A "${M93_DATA}")"
echo "OK: M3 contract — init without a server row exits 1, no sentinel, no boot snapshot"

echo "==> [2] server init writes the sentinel; init takes a boot snapshot"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel server init 10.8.0.1/24 51820 --endpoint vpn.example.com:51820 2>&1)" \
    || fail "server init failed: ${out}"
[ -f "${M93_DATA}/.server-initialized" ] || fail "no sentinel after server init"
SENTMODE="$(stat_mode "${M93_DATA}/.server-initialized")"
[ "${SENTMODE}" = "600" ] || fail "sentinel mode = ${SENTMODE}, want 600"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" || fail "init after server init failed: ${out}"
SNAPS=("${M93_DATA}"/amnezia.sqlite.boot-*)
[ "${#SNAPS[@]}" = "1" ] || fail "boot snapshot count = ${#SNAPS[@]}, want 1"
SNAPMODE="$(stat_mode "${SNAPS[0]}")"
[ "${SNAPMODE}" = "600" ] || fail "boot snapshot mode = ${SNAPMODE}, want 600"
grep -q '10\.8\.0\.1/24' "${M93_CONFIG}/awg0.conf" || fail "awg0.conf missing the server address"
echo "OK: sentinel 0600, boot snapshot 0600, awg0.conf regenerated"

echo "==> [3] data-loss guard: sentinel present, database missing"
rm -f "${M93_DATA}/amnezia.sqlite"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" && rc=0 || rc=$?
[ "${rc}" = "1" ] || fail "init exit = ${rc}, want 1: ${out}"
case "${out}" in
    *.server-initialized* | *refusing*) ;;
    *) fail "refusal message: ${out}" ;;
esac
[ -e "${M93_DATA}/amnezia.sqlite" ] && fail "init recreated the database despite the sentinel"
[ "$(ls "${M93_DATA}" | grep -c 'amnezia.sqlite.boot-')" = "1" ] || fail "boot snapshot lost"
echo "OK: init refused, no schema recreated, boot snapshot kept"

echo "==> [4] recovery from the boot snapshot"
cp "${M93_DATA}"/amnezia.sqlite.boot-* "${M93_DATA}/amnezia.sqlite"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" || fail "post-recovery init failed: ${out}"
grep -q '10\.8\.0\.1/24' "${M93_CONFIG}/awg0.conf" || fail "awg0.conf not regenerated after recovery"
echo "OK: copied back the snapshot, init regenerated awg0.conf"

echo "==> [5] upgrade self-heal: deployment without a sentinel"
rm -f "${M93_DATA}/.server-initialized"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" || fail "self-heal init failed: ${out}"
[ -f "${M93_DATA}/.server-initialized" ] || fail "sentinel not self-healed"
echo "OK: sentinel re-created by init"

echo "==> [6] silent-wipe guard: sentinel present, no server row"
rm -f "${M93_DATA}/amnezia.sqlite"
rm -f "${M93_DATA}/.server-initialized"
# create the schema-only state: init without the sentinel rebuilds the
# schema (and exits 1 on the M3 no-server-row check)
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" && rc=0 || rc=$?
[ "${rc}" = "1" ] || fail "schema rebuild exit = ${rc}, want 1: ${out}"
[ -e "${M93_DATA}/amnezia.sqlite" ] || fail "fresh schema not created"
printf 'initialized\n' > "${M93_DATA}/.server-initialized"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" && rc=0 || rc=$?
[ "${rc}" = "1" ] || fail "init exit = ${rc}, want 1: ${out}"
case "${out}" in *"no server row was found"*) ;; *) fail "silent-wipe diagnosis: ${out}" ;; esac
echo "OK: schema-only database under the sentinel refused"

echo "==> [7] boot snapshot rotation (keep newest three)"
# re-seed the server row wiped by the guard scenarios above
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel server init 10.8.0.1/24 51820 --endpoint vpn.example.com:51820 2>&1)" \
    || fail "re-seed server init failed: ${out}"
rm -f "${M93_DATA}/.server-initialized"
for i in 1 2 3 4 5 6; do
    out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" || fail "rotation init #${i} failed: ${out}"
done
COUNT="$(ls "${M93_DATA}" | grep -c 'amnezia.sqlite.boot-')"
[ "${COUNT}" = "3" ] || fail "boot snapshot count = ${COUNT}, want 3"
echo "OK: rotation keeps 3 snapshots"

echo "==> [8] full wipe (db + sentinel) → M3 fresh path, no sentinel"
rm -f "${M93_DATA}/amnezia.sqlite" "${M93_DATA}/.server-initialized"
out="$("${COMPOSE[@]}" run --rm panel-init /app/panel init 2>&1)" && rc=0 || rc=$?
[ "${rc}" = "1" ] || fail "full-wipe init exit = ${rc}, want 1: ${out}"
case "${out}" in *"no server row"*) ;; *) fail "full-wipe init stdout: ${out}" ;; esac
[ -e "${M93_DATA}/amnezia.sqlite" ] || fail "fresh schema not created"
[ -f "${M93_DATA}/.server-initialized" ] && fail "full wipe recreated the sentinel"
echo "OK: documented M3 fresh path still works, no sentinel"

echo
echo "==> e2e OK: fresh install, sentinel, boot snapshots, wipe guards, recovery, self-heal, rotation"
exit 0

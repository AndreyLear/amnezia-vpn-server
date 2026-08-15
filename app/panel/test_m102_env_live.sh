#!/bin/bash
# LIVE e2e for M10.2-FIX2: deployment .env (AWG_PORT/VPN_SUBNET) is
# loaded into panel and panel-init only — never into awg. Backups are
# unencrypted tar.zst (T-143): create/restore need no AGE_RECIPIENT.
# The harness runs in a temp directory that mirrors the deployment
# layout: compose.yaml + versions.lock + .env side by side (install.sh
# copies the first two into ROOT_DIR and creates .env next to them).
#
# Docker daemon required. The awg container is not started (it needs
# the host tunnel device and would conflict with a live deployment);
# its env is inspected via `docker compose run` instead.
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

PROJ="m102-env-$$"
M102_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-m102-e2e-XXXXXX")"
M102_APP="${M102_ROOT}/app"
mkdir -p "${M102_APP}"

fail() {
    echo "FAIL: $1"
    docker compose -p "${PROJ}" -f "${M102_APP}/compose.yaml" logs --no-color 2>/dev/null | tail -20 || true
    exit 1
}
cleanup() {
    set +e
    if [ -n "${M102_KEEP:-}" ]; then
        echo "==> M102_KEEP set: leaving stack for debugging (project ${PROJ}, dir ${M102_ROOT})"
        return
    fi
    docker compose -p "${PROJ}" -f "${M102_APP}/compose.yaml" down -v >/dev/null 2>&1
    rm -rf "${M102_ROOT}"
}
trap cleanup EXIT

# Deployment layout: compose.yaml + versions.lock + .env in one dir.
cp compose.yaml versions.lock "${M102_APP}/"
# The base compose maps 127.0.0.1:8787:8787; when the port is taken
# (e.g. a live deployment on the same host) the harness remaps it so
# the loopback contract still holds (same pattern as test_m8_live.sh).
if [ "${M102_PORT:-8787}" != "8787" ]; then
    sed -i.bak -e "s|127.0.0.1:8787:8787|127.0.0.1:${M102_PORT}:8787|" \
        -e "s|context: \.|context: ${REPO}|" "${M102_APP}/compose.yaml"
fi

# Deployment .env — AWG_PORT/VPN_SUBNET (0600 like install.sh).
printf 'AWG_PORT=51820\nVPN_SUBNET=10.8.0.0/24\n' > "${M102_APP}/.env"
chmod 0600 "${M102_APP}/.env"

COMPOSE=(docker compose -p "${PROJ}" --env-file versions.lock -f "${M102_APP}/compose.yaml")

echo "==> [1] docker compose config parses with the deployment .env"
"${COMPOSE[@]}" config --quiet || fail "docker compose config with .env"
echo "OK: compose config"

echo "==> [2] panel-init: server init + client (M3 needs a server row first)"
"${COMPOSE[@]}" run --rm panel-init /app/panel server init 10.8.0.1/24 51820 \
    --endpoint vpn.example.com:51820 >/dev/null 2>&1 || fail "server init"
"${COMPOSE[@]}" run --rm panel-init /app/panel client add alice >/dev/null 2>&1 || fail "client add"

echo "==> [3] panel env carries AWG_PORT from the deployment .env"
"${COMPOSE[@]}" up -d panel-init panel >/dev/null 2>&1 || fail "panel-init/panel start"
sleep 2
PANEL_ID="$(docker ps -q --filter "name=${PROJ}-panel-1")"
[ -n "${PANEL_ID}" ] || fail "no panel container"
docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${PANEL_ID}" \
    | grep -q "^AWG_PORT=51820$" \
    || fail "panel env missing AWG_PORT from .env"
echo "OK: panel got AWG_PORT from the deployment .env"

echo "==> [4] awg environment has no AGE_RECIPIENT"
# awg's entrypoint blocks forever waiting for a usable /config/awg0.conf
# (it never reaches an overridden command), so the env probe uses
# `docker compose create` — the container is created but never started
# and its environment is inspectable without the entrypoint.
"${COMPOSE[@]}" create awg >/dev/null 2>&1 || fail "awg container create"
AWG_ID="$(docker ps -a --filter "name=${PROJ}-awg-" --format '{{.ID}}' | head -1)"
[ -n "${AWG_ID}" ] || fail "no created awg container"
AWG_ENV="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${AWG_ID}")"
printf '%s\n' "${AWG_ENV}" | grep -q '^AGE_RECIPIENT=' && fail "awg received AGE_RECIPIENT"
echo "OK: awg env is free of AGE_RECIPIENT"

echo "==> [5] panel backup create works without AGE_RECIPIENT"
"${COMPOSE[@]}" exec -T panel /app/panel backup create >/dev/null 2>&1 || fail "panel backup create"
ARCHIVE="$(ls -t "${M102_APP}/backups" | grep -E '^backup-' | head -1)"
[ -n "${ARCHIVE}" ] || fail "no archive in the deployment backups dir"
echo "OK: archive ${ARCHIVE} created"

echo "==> [6] panel-init restore prepare works without identity-stdin"
"${COMPOSE[@]}" run --rm panel-init /app/panel restore "${ARCHIVE}" \
    >/dev/null 2>&1 || fail "restore prepare"
[ -d "${M102_APP}/data/.restore-pending" ] || fail "pending marker missing"
echo "OK: restore pending"

echo
echo "==> e2e OK: .env AWG_PORT/VPN_SUBNET → panel/panel-init, backups without age"
exit 0

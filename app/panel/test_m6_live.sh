#!/bin/bash
# LIVE e2e for M6.4 + M6.5 + M7.6 + M7.7 (M6_AUDIT.md §9 "Docker E2E",
# M7 task §7): full compose stack in Docker:
#
#   panel-init → database + awg0.conf generated;
#   admin user  → panel auth add-user (M6.2);
#   panel serve → HTTP panel on 127.0.0.1:<port> (M6.4 loopback mapping);
#   login       → session cookie + dashboard CSRF token (M7.5/M7.6);
#   POST /clients/new (HTTP, real CSRF) → client row + regenerated
#     awg0.conf;
#   POST without CSRF → 403 and the DB row count is unchanged (M7.7);
#   GET  /clients/{id}/config → byte-identical to `panel client config`;
#   GET  /clients/{id}/qr     → valid PNG;
#   awg0.conf keeps working: awg container applies the new peer and
#   status.json reports it; disable/enable removes/restores the peer
#   via syncconf;
#   logout → the panel is closed again (M7.5/M7.6);
#   secrets stay out of HTML/status.json.
#
# Everything runs in a temp directory (data/config/status are never
# written inside the repo). Docker daemon required.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO}"

set -a; source versions.lock; set +a

PROJ="m6-e2e-$$"
M6_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-m6-e2e-XXXXXX")"
M6_DATA="${M6_ROOT}/data"; M6_CONFIG="${M6_ROOT}/config"; M6_STATUS="${M6_ROOT}/status"; M6_BACKUPS="${M6_ROOT}/backups"
mkdir -p "${M6_DATA}" "${M6_CONFIG}" "${M6_STATUS}"
# The M6.4 mapping under test is the fixed loopback port; override
# with M6_PORT when the default 8787 is taken (e.g. alongside a live
# deployment on the same host).
M6_PORT="${M6_PORT:-8787}"
# Admin credentials for the live HTTP login (M6.2/M7.5).
M6_ADMIN="e2e-admin"
M6_PASSWORD="e2e-password-for-csrf-42"

cleanup() {
    set +e
    if [ -n "${M6_KEEP:-}" ]; then
        echo "==> M6_KEEP set: leaving stack for debugging (project ${PROJ}, dir ${M6_ROOT})"
        return
    fi
    "${COMPOSE[@]}" down -v >/dev/null 2>&1
    rm -rf "${M6_ROOT}"
}
trap cleanup EXIT

# The harness runs the real compose.yaml copied into its temp root, so
# the relative ./data ./config ./status ./backups binds resolve inside
# the harness dirs (a compose merge with an override volume list would
# duplicate mounts and litter the repo with parent stubs — M10.2-FIX2).
# The base compose maps 127.0.0.1:8787:8787; when the default port is
# taken (e.g. a live deployment on the same host) it is remapped via a
# sed-ed copy so the M6.4 loopback contract still holds.
COMPOSE_FILE="${M6_ROOT}/compose.yaml"
cp "${REPO}/compose.yaml" versions.lock "${M6_ROOT}/"
sed -i.bak \
    -e "s|127.0.0.1:8787:8787|127.0.0.1:${M6_PORT}:8787|" \
    -e "s|context: \.|context: ${REPO}|" "${COMPOSE_FILE}"

COMPOSE=(docker compose -p "${PROJ}" --env-file versions.lock -f "${COMPOSE_FILE}")

fail() {
    echo "FAIL: $1"
    "${COMPOSE[@]}" logs --no-color 2>/dev/null | tail -40 || true
    exit 1
}

echo "==> building panel and awg images"
"${COMPOSE[@]}" build panel awg >/dev/null || fail "image build"

echo "==> [1] server init + panel-init (migrate + awg0.conf)"
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
[ -s "${M6_CONFIG}/awg0.conf" ] || fail "panel-init produced no awg0.conf"

echo "==> [1b] creating the admin user (M6.2)"
"${COMPOSE[@]}" run --rm panel-init /app/panel auth add-user "${M6_ADMIN}" --password-stdin \
    <<< "${M6_PASSWORD}" >/dev/null 2>&1 || fail "auth add-user"

echo "==> [2] starting panel"
"${COMPOSE[@]}" up -d panel >/dev/null || fail "panel start"
I=0
while ! curl -fsS -o /dev/null "http://127.0.0.1:${M6_PORT}/" 2>/dev/null; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && fail "panel never answered on 127.0.0.1:${M6_PORT}"
    sleep 1
done
echo "OK: panel up (host loopback 127.0.0.1:${M6_PORT} -> container 0.0.0.0:8787)"

echo "==> [2b] HTTP login + CSRF token (M7.5/M7.6)"
# Login: the session cookie lands in a jar; the redirect to / is not
# followed (curl -c saves cookies from the 303's Set-Cookie).
curl -fsS -c "${M6_ROOT}/cookies.txt" \
    -d "username=${M6_ADMIN}&password=${M6_PASSWORD}" \
    -o /dev/null "http://127.0.0.1:${M6_PORT}/login" || fail "login POST"
# A dashboard GET with the session cookie renders the CSRF token inside
# the hidden _csrf inputs of the mutation forms.
DASH="$(curl -fsS -b "${M6_ROOT}/cookies.txt" "http://127.0.0.1:${M6_PORT}/")" \
    || fail "dashboard GET"
CSRF="$(printf '%s' "${DASH}" | sed -n 's/.*name="_csrf" value="\([A-Za-z0-9_-]\{43\}\)".*/\1/p' | head -1)"
[ -n "${CSRF}" ] || fail "dashboard carried no CSRF token"
echo "${DASH}" | grep -q 'Signed in as' || fail "dashboard missing the signed-in identity"
echo "OK: logged in as ${M6_ADMIN}, CSRF token captured (len ${#CSRF})"

echo "==> [3] adding a client over HTTP (PRG, real CSRF)"
LOC="$(curl -s -b "${M6_ROOT}/cookies.txt" -D - -o /dev/null \
    -d "name=e2e-http&_csrf=${CSRF}" "http://127.0.0.1:${M6_PORT}/clients/new" \
    | grep -i '^location:' | tr -d '\r' | awk '{print $2}')"
echo "${LOC}" | grep -q 'msg=Client' || fail "add did not PRG to /?msg=Client... (got ${LOC})"

# A tokenless POST must be rejected with 403 (M7.6 live check) and
# must not mutate the database (M7.7: CSRF check runs before any DB
# write).
COUNT_BEFORE="$("${COMPOSE[@]}" exec -T panel /app/panel client list | wc -l | tr -d ' ')"
NO_CSRF_CODE="$(curl -s -b "${M6_ROOT}/cookies.txt" -o /dev/null -w '%{http_code}' \
    -d 'name=should-not-exist' "http://127.0.0.1:${M6_PORT}/clients/new")"
[ "${NO_CSRF_CODE}" = "403" ] || fail "tokenless POST answered ${NO_CSRF_CODE}, want 403"
COUNT_AFTER="$("${COMPOSE[@]}" exec -T panel /app/panel client list | wc -l | tr -d ' ')"
[ "${COUNT_BEFORE}" = "${COUNT_AFTER}" ] || fail "tokenless POST mutated the DB: ${COUNT_BEFORE} → ${COUNT_AFTER} clients"
"${COMPOSE[@]}" exec -T panel /app/panel client list | grep -q 'should-not-exist' \
    && fail "tokenless POST created a client anyway"
echo "OK: tokenless POST → 403, DB count unchanged (${COUNT_BEFORE} = ${COUNT_AFTER})"

ID="$("${COMPOSE[@]}" exec -T panel /app/panel client list | awk -F'\t' '$2 == "e2e-http" {print $1}' | head -1)"
[ -n "${ID}" ] || fail "new client missing from client list"
echo "OK: client id ${ID}"

echo "==> [4] config download == CLI bytes"
curl -fsS -b "${M6_ROOT}/cookies.txt" "http://127.0.0.1:${M6_PORT}/clients/${ID}/config" -o "${M6_ROOT}/dl.conf" || fail "config download"
"${COMPOSE[@]}" exec -T panel /app/panel client config "${ID}" > "${M6_ROOT}/cli.conf"
cmp -s "${M6_ROOT}/dl.conf" "${M6_ROOT}/cli.conf" || fail "download differs from CLI client config"
HDR="$(curl -s -b "${M6_ROOT}/cookies.txt" -D - -o /dev/null "http://127.0.0.1:${M6_PORT}/clients/${ID}/config")"
echo "${HDR}" | grep -qi 'Content-Type: text/plain' || fail "config Content-Type"
echo "${HDR}" | grep -qi 'Content-Disposition: attachment; filename="e2e-http.conf"' || fail "config Content-Disposition"
echo "OK: config bytes and headers"

echo "==> [5] QR endpoint returns a PNG"
curl -fsS -b "${M6_ROOT}/cookies.txt" "http://127.0.0.1:${M6_PORT}/clients/${ID}/qr" -o "${M6_ROOT}/qr.png" || fail "QR download"
file "${M6_ROOT}/qr.png" | grep -qi 'png image' || fail "QR is not a PNG: $(file "${M6_ROOT}/qr.png")"
echo "OK: QR is a valid PNG"

echo "==> [6] awg0.conf keeps working: awg starts AFTER the add"
# The awg container starts with the panel-generated config (peer
# already in place), so awg-quick up applies it directly — no startup
# mtime race.
"${COMPOSE[@]}" up -d awg >/dev/null || fail "awg start"
PUB="$("${COMPOSE[@]}" exec -T panel /app/panel client show "${ID}" | awk -F': ' '$1 == "public_key" {print $2}')"
grep -q "PublicKey = ${PUB}" "${M6_CONFIG}/awg0.conf" || fail "awg0.conf missing the new peer"
I=0
while ! grep -q "\"public_key\":\"${PUB}\"" "${M6_STATUS}/status.json" 2>/dev/null; do
    I=$((I + 1))
    [ "${I}" -gt 60 ] && fail "awg never applied the new peer (status.json)"
    sleep 1
done
echo "OK: awg-quick applied the peer from awg0.conf (status.json)"

echo "==> [6b] live reload: disable removes the peer, enable restores it"
"${COMPOSE[@]}" exec -T panel /app/panel client disable "${ID}" >/dev/null
I=0
while grep -q "\"public_key\":\"${PUB}\"" "${M6_STATUS}/status.json" 2>/dev/null; do
    I=$((I + 1))
    [ "${I}" -gt 40 ] && fail "syncconf did not remove the disabled peer: status.json still has it ($(grep -c "public_key" "${M6_STATUS}/status.json" 2>/dev/null || true) peers; awg0.conf: $(grep -c "^\[Peer\]" "${M6_CONFIG}/awg0.conf" 2>/dev/null || true) peer sections)"
    sleep 1
done
grep -q "PublicKey = ${PUB}" "${M6_CONFIG}/awg0.conf" && fail "disabled peer still in awg0.conf"
echo "OK: disable → panel regenerated awg0.conf → syncconf removed the peer"
"${COMPOSE[@]}" exec -T panel /app/panel client enable "${ID}" >/dev/null
I=0
while ! grep -q "\"public_key\":\"${PUB}\"" "${M6_STATUS}/status.json" 2>/dev/null; do
    I=$((I + 1))
    [ "${I}" -gt 40 ] && fail "syncconf did not restore the enabled peer (status.json peer count: $(grep -c "public_key" "${M6_STATUS}/status.json" 2>/dev/null || true))"
    sleep 1
done
echo "OK: enable → peer back in the runtime"

echo "==> [7] secret invariants in the live stack"
HTML="$(curl -fsS -b "${M6_ROOT}/cookies.txt" "http://127.0.0.1:${M6_PORT}/")"
echo "${HTML}" | grep -q 'e2e-http' || fail "dashboard missing the client card"
echo "${HTML}" | grep -q 'PrivateKey' && fail "private key leaked into HTML"
grep -q 'private_key' "${M6_STATUS}/status.json" && fail "private_key leaked into status.json"
grep -q 'preshared_key' "${M6_STATUS}/status.json" && fail "preshared_key leaked into status.json"
echo "OK: no secrets in HTML/status.json"

echo "==> [7b] logout with the CSRF token (M7.6)"
curl -fsS -b "${M6_ROOT}/cookies.txt" -c "${M6_ROOT}/cookies.txt" \
    -d "_csrf=${CSRF}" -o /dev/null "http://127.0.0.1:${M6_PORT}/logout" || fail "logout POST"
# After logout the session cookie is cleared; the dashboard is closed again.
curl -s -b "${M6_ROOT}/cookies.txt" -o /dev/null -w '%{http_code}' \
    "http://127.0.0.1:${M6_PORT}/" | grep -q '303' || fail "dashboard reachable after logout"
echo "OK: logout consumed the session"

echo
echo "==> e2e OK: panel-init, serve, add, config==CLI, QR PNG, awg0.conf live"
exit 0

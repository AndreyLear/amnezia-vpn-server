#!/bin/bash
# Тесты сторожа (amnezia-vpn-server-ptuo).
#
# Сторож перезапускает сервис, который поднят и не работает. Всё опасное в
# нём — решение перезапустить, поэтому харнесс подменяет docker и dig
# фальшивками на PATH и проверяет ровно то, что сторож решил сделать:
#
#   - исправный стек не трогается вовсе;
#   - одиночный отказ не приводит к перезапуску, второй подряд — приводит;
#   - удачная проверка сбрасывает счётчик, то есть два отказа с исправной
#     проверкой между ними перезапуском не заканчиваются;
#   - между двумя перезапусками одного сервиса выдерживается пауза;
#   - в режиме --no-tunnel-dns резолвер не проверяется;
#   - отсутствие dig не считается отказом резолвера;
#   - застой status.json перезапускает туннель, свежий файл — нет.
#
# Работает на обычном bash, без root, Docker и сети.
set -u

cd "$(dirname "$0")"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-watchdog-test.XXXXXX")"
cleanup() {
    if [ -n "${KEEP_TMP:-}" ]; then
        echo "TMP kept at: ${TMP}" >&2
        return
    fi
    rm -rf "${TMP}"
}
trap cleanup EXIT

check() { # check <имя> [not] <cmd...>
    local name="$1"
    shift
    local neg=0
    if [ "${1:-}" = "not" ]; then
        neg=1
        shift
    fi
    if "$@" >/dev/null 2>&1; then
        [ "${neg}" = "1" ] && { FAILED=$((FAILED + 1)); echo "FAIL: ${name}"; return; }
        PASSED=$((PASSED + 1))
        echo "PASS: ${name}"
    else
        [ "${neg}" = "1" ] && { PASSED=$((PASSED + 1)); echo "PASS: ${name}"; return; }
        FAILED=$((FAILED + 1))
        echo "FAIL: ${name}"
    fi
}

has() { grep -q -- "$2" "$1"; }

mkdir -p "${TMP}/bin"

# ---- фальшивый docker: пишет вызов в журнал ----------------------------
cat > "${TMP}/bin/docker" <<FAKE
#!/bin/sh
printf '%s\n' "\$*" >> "${TMP}/docker.log"
exit 0
FAKE
chmod +x "${TMP}/bin/docker"

# ---- фальшивый dig: отвечает или молчит по флагу ------------------------
cat > "${TMP}/bin/dig" <<FAKE
#!/bin/sh
if [ -f "${TMP}/dns-down" ]; then
    exit 1
fi
echo 93.184.216.34
FAKE
chmod +x "${TMP}/bin/dig"

# ---- развёртывание ------------------------------------------------------
ROOT="${TMP}/root"
mkdir -p "${ROOT}/status"
printf 'TUNNEL_ADDRESS=10.8.0.1\n' > "${ROOT}/.env"
printf 'IMAGE_VERSION=test\n' > "${ROOT}/versions.lock"
fresh_status() { printf '{}' > "${ROOT}/status/status.json"; }
fresh_status

STATE="${TMP}/state"

run() { # run — один прогон сторожа, вывод в ${TMP}/out
    PATH="${TMP}/bin:${PATH}" \
    AMNEZIA_WATCHDOG_DIG="${DIG_OVERRIDE:-dig}" \
    AMNEZIA_WATCHDOG_ROOT="${ROOT}" \
    AMNEZIA_WATCHDOG_STATE="${STATE}" \
    AMNEZIA_WATCHDOG_RESTART_INTERVAL="${RESTART_INTERVAL:-600}" \
        bash ./watchdog.sh > "${TMP}/out" 2>&1
}

reset_all() {
    rm -rf "${STATE}" "${TMP}/docker.log" "${TMP}/dns-down"
    fresh_status
}

# ---- исправный стек ----------------------------------------------------
reset_all
run
check "исправный стек: ничего не перезапускается" not test -f "${TMP}/docker.log"
check "исправный стек: сторож молчит" test ! -s "${TMP}/out"

# ---- один отказ резолвера не повод -------------------------------------
reset_all
touch "${TMP}/dns-down"
run
check "первый отказ резолвера: перезапуска нет" not test -f "${TMP}/docker.log"
check "первый отказ резолвера: сказано вслух" has "${TMP}/out" "отказ 1 из 2"

# ---- второй отказ подряд перезапускает ---------------------------------
run
check "второй отказ подряд: резолвер перезапущен" has "${TMP}/docker.log" "restart dns"
check "второй отказ подряд: туннель не тронут" not has "${TMP}/docker.log" "restart awg"

# ---- удачная проверка между отказами сбрасывает счётчик ----------------
reset_all
touch "${TMP}/dns-down"
run
rm -f "${TMP}/dns-down"
run
touch "${TMP}/dns-down"
run
check "отказ, успех, отказ: перезапуска нет" not test -f "${TMP}/docker.log"

# ---- пауза между перезапусками -----------------------------------------
reset_all
touch "${TMP}/dns-down"
run
run
check "пауза: первый перезапуск случился" has "${TMP}/docker.log" "restart dns"
run
run
count="$(grep -c "restart dns" "${TMP}/docker.log")"
check "пауза: второй перезапуск не выпущен" test "${count}" = "1"
check "пауза: причина названа в журнале" has "${TMP}/out" "жду"

# ---- пауза истекла ------------------------------------------------------
RESTART_INTERVAL=0
run
run
count="$(grep -c "restart dns" "${TMP}/docker.log")"
check "истёкшая пауза: перезапуск снова разрешён" test "${count}" -ge 2
unset RESTART_INTERVAL

# ---- режим --no-tunnel-dns ---------------------------------------------
reset_all
printf 'TUNNEL_ADDRESS=10.8.0.1\nTUNNEL_DNS_DISABLED=1\n' > "${ROOT}/.env"
touch "${TMP}/dns-down"
run
run
check "--no-tunnel-dns: резолвер не проверяется" not test -f "${TMP}/docker.log"
printf 'TUNNEL_ADDRESS=10.8.0.1\n' > "${ROOT}/.env"

# ---- нет dig ------------------------------------------------------------
reset_all
mv "${TMP}/bin/dig" "${TMP}/dig.hidden"
# Прячем и системный dig: на macOS он есть в /usr/bin, и без этого проверка
# нашла бы его вместо фальшивки и меряла бы не то.
DIG_OVERRIDE="${TMP}/bin/dig-absent"
run
run
check "нет dig: резолвер не перезапускается" not test -f "${TMP}/docker.log"
check "нет dig: сказано, почему проверки не было" has "${TMP}/out" "нечем проверить резолвер"
mv "${TMP}/dig.hidden" "${TMP}/bin/dig"
unset DIG_OVERRIDE

# ---- застой status.json -------------------------------------------------
reset_all
touch -t 202001010000 "${ROOT}/status/status.json"
run
check "застоявшийся status.json: первый отказ без перезапуска" not has "${TMP}/docker.log" "restart awg"
run
check "застоявшийся status.json: туннель перезапущен" has "${TMP}/docker.log" "restart awg"
check "застоявшийся status.json: резолвер не тронут" not has "${TMP}/docker.log" "restart dns"

# ---- пропавший status.json ---------------------------------------------
reset_all
rm -f "${ROOT}/status/status.json"
run
run
check "пропавший status.json: туннель перезапущен" has "${TMP}/docker.log" "restart awg"
fresh_status

# ---- свежий status.json -------------------------------------------------
reset_all
run
run
check "свежий status.json: туннель не трогают" not test -f "${TMP}/docker.log"

echo
echo "passed: ${PASSED}, failed: ${FAILED}"
[ "${FAILED}" -eq 0 ]

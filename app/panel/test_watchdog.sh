#!/bin/bash
# amnezia-vpn-server-ptuo / -eq82 tests for watchdog.sh.
#
# Сторож перезапускает сервис, который жив, но не работает, и с eq82 ещё и
# оставляет для панели снимок того, что выяснил. Проверяем оба обещания:
#
#   - исправный стек не трогается, и в снимке он выглядит исправным;
#   - отказ считается, но перезапуск наступает не с первого раза: сеть
#     моргает, и дёргать туннель из-за одной запинки — вред без причины;
#   - причина перезапуска переживает сам перезапуск, иначе через минуту в
#     панели не останется следа, что что-то происходило;
#   - --no-tunnel-dns не считается отказом резолвера: там он намеренно
#     стоит в стороне;
#   - отсутствие dig — не отказ резолвера, а невозможность проверки.
#
# Плоский bash (macOS или Linux), без root, без Docker и без сети.
set -u

cd "$(dirname "$0")/../.."
WATCHDOG="$PWD/watchdog.sh"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-watchdog-test.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

pass() { PASSED=$((PASSED + 1)); printf 'ok   %s\n' "$1"; }
fail() { FAILED=$((FAILED + 1)); printf 'FAIL %s\n' "$1"; }
check() { local n="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$n"; else fail "$n"; fi; }
check_not() { local n="$1"; shift; if "$@" >/dev/null 2>&1; then fail "$n"; else pass "$n"; fi; }

FAKE_DIR="$TMP/bin"
mkdir -p "$FAKE_DIR"

cat > "$FAKE_DIR/dig" <<'FAKE'
#!/bin/bash
echo "dig $*" >> "${WD_CALLS:?}"
exit "${DIG_RC:-0}"
FAKE
cat > "$FAKE_DIR/docker" <<'FAKE'
#!/bin/bash
echo "docker $*" >> "${WD_CALLS:?}"
exit "${DOCKER_RC:-0}"
FAKE
chmod +x "$FAKE_DIR"/*

ROOT="$TMP/deploy"
STATE="$TMP/state"
CALLS="$TMP/calls.log"

setup() { # setup [--no-dns] [--stale]
    rm -rf "$ROOT" "$STATE"
    mkdir -p "$ROOT/status" "$STATE"
    printf 'TUNNEL_ADDRESS=10.8.0.1\n' > "$ROOT/.env"
    for arg in "$@"; do
        [ "$arg" = "--no-dns" ] && printf 'TUNNEL_DNS_DISABLED=1\n' >> "$ROOT/.env"
    done
    printf '{"schema":"v1"}\n' > "$ROOT/status/status.json"
    : > "$CALLS"
}

run_watchdog() {
    env PATH="$FAKE_DIR:$PATH" WD_CALLS="$CALLS" \
        AMNEZIA_WATCHDOG_ROOT="$ROOT" \
        AMNEZIA_WATCHDOG_STATE="$STATE" \
        "$@" bash "$WATCHDOG"
}

snapshot() { cat "$ROOT/status/services.json" 2>/dev/null; }

# --- исправный стек ----------------------------------------------------
setup
run_watchdog >/dev/null 2>&1
check "исправный стек: сторож выходит с нулём" test "$?" = "0"
check_not "и ничего не перезапускает" grep -q "restart" "$CALLS"
check "снимок для панели появился" test -f "$ROOT/status/services.json"
check "резолвер отмечен исправным" grep -q '"name":"dns","state":"ok"' <<<"$(snapshot)"
check "туннель отмечен исправным" grep -q '"name":"awg","state":"ok"' <<<"$(snapshot)"
check "и записано, когда проверяли" \
    grep -qE '"checked_at_utc":"[0-9]{4}-[0-9]{2}-[0-9]{2}T' <<<"$(snapshot)"
mode="$(stat -c %a "$ROOT/status/services.json" 2>/dev/null \
        || stat -f %Lp "$ROOT/status/services.json")"
check "панель может его прочитать (права $mode)" test "$mode" = "644"

# --- резолвер молчит ---------------------------------------------------
# Первый отказ — ещё не повод дёргать туннель: сеть моргает, и перезапуск
# из-за одной запинки был бы вредом без причины.
setup
run_watchdog DIG_RC=9 >/dev/null 2>&1
check_not "первый отказ резолвера: перезапуска нет" \
    grep -q "restart dns" "$CALLS"
check "но отказ виден в снимке" grep -q '"name":"dns","state":"fail"' <<<"$(snapshot)"
check "и названа причина" grep -q '10.8.0.1' <<<"$(snapshot)"
check "и посчитан" grep -q '"fails":1' <<<"$(snapshot)"

# Второй отказ подряд — перезапуск.
run_watchdog DIG_RC=9 >/dev/null 2>&1
check "второй отказ подряд: резолвер перезапущен" \
    grep -q "restart dns" "$CALLS"
# Через минуту всё исправно, но след обязан остаться: иначе владелец так и
# не узнает, что сервер сам себя чинил.
run_watchdog >/dev/null 2>&1
check "после починки резолвер снова исправен" \
    grep -q '"name":"dns","state":"ok"' <<<"$(snapshot)"
check "но причина перезапуска сохранилась" \
    grep -q '"restart_reason":"не отвечает' <<<"$(snapshot)"
check "и время перезапуска тоже" \
    grep -qE '"restarted_at_utc":"[0-9]{4}-' <<<"$(snapshot)"

# --- туннель перестал писать статус ------------------------------------
setup
rm -f "$ROOT/status/status.json"
run_watchdog >/dev/null 2>&1
check "пропавший status.json — отказ туннеля" \
    grep -q '"name":"awg","state":"fail"' <<<"$(snapshot)"
check "и причина названа" grep -q 'status.json' <<<"$(snapshot)"

# --- резолвер выключен намеренно ---------------------------------------
# При --no-tunnel-dns он не занимает порт 53, и молчание — это норма.
setup --no-dns
run_watchdog DIG_RC=9 >/dev/null 2>&1
check_not "выключенный резолвер не считается отказом" \
    grep -q '"name":"dns","state":"fail"' <<<"$(snapshot)"
check_not "и не перезапускается" grep -q "restart dns" "$CALLS"

# --- нечем проверить ---------------------------------------------------
# Отсутствие dig — не отказ резолвера. Считать иначе значило бы
# перезапускать исправный сервис по кругу из-за отсутствующего пакета.
setup
rm -f "$FAKE_DIR/dig"
run_watchdog >/dev/null 2>&1
check_not "без dig резолвер не объявляется сломанным" \
    grep -q '"name":"dns","state":"fail"' <<<"$(snapshot)"
check_not "и не перезапускается" grep -q "restart dns" "$CALLS"
printf '#!/bin/bash\necho "dig $*" >> "${WD_CALLS:?}"\nexit "${DIG_RC:-0}"\n' > "$FAKE_DIR/dig"
chmod +x "$FAKE_DIR/dig"

echo
echo "passed: ${PASSED}, failed: ${FAILED}"
[ "${FAILED}" = "0" ] || exit 1

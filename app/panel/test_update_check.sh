#!/bin/bash
# amnezia-vpn-server-zklt tests for update-check.sh.
#
# The script asks GitHub about the latest release and leaves the answer
# beside status.json for the panel to read. What must hold:
#
#   - a good answer is stored verbatim, so the panel parses JSON instead
#     of the script pretending it can;
#   - an unreachable GitHub keeps the previous answer and says the check
#     failed — «never checked» and «checked, no luck» are different news;
#   - a reply that is not a release (a provider's block page, a captive
#     portal) never replaces a real one;
#   - the files are readable by the panel, which mounts status/ read-only.
#
# Runs on plain bash (macOS or Linux); no root, no Docker, no network:
# curl is a shim on PATH.
set -u

cd "$(dirname "$0")/../.."
SCRIPT="$PWD/update-check.sh"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-update-test.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

pass() { PASSED=$((PASSED + 1)); printf 'ok   %s\n' "$1"; }
fail() { FAILED=$((FAILED + 1)); printf 'FAIL %s\n' "$1"; }
check() { # check <name> <cmd...>
    local name="$1"; shift
    if "$@" >/dev/null 2>&1; then pass "$name"; else fail "$name"; fi
}
check_not() { # check_not <name> <cmd...> — the command must fail
    local name="$1"; shift
    if "$@" >/dev/null 2>&1; then fail "$name"; else pass "$name"; fi
}

STATUS_DIR="$TMP/status"
FAKE_DIR="$TMP/bin"
mkdir -p "$STATUS_DIR" "$FAKE_DIR"

# curl shim: the transport result comes from CURL_RC, the HTTP status from
# CURL_HTTP and the body from a file, so each case sets up what happened
# without touching the script. It prints the status the way `-w %{http_code}`
# does — including the 000 real curl prints when it never got an answer,
# which is the whole difference between «did not reach GitHub» and «reached
# it, got a 404».
cat > "$FAKE_DIR/curl" <<'FAKE'
#!/bin/bash
oarg=""; prev=""
for a in "$@"; do
    [ "$prev" = "-o" ] && oarg="$a"
    prev="$a"
done
if [ "${CURL_RC:-0}" != "0" ]; then
    printf '000'
    exit "${CURL_RC}"
fi
[ -n "$oarg" ] && cat "${CURL_BODY:?}" > "$oarg"
printf '%s' "${CURL_HTTP:-200}"
exit 0
FAKE
chmod +x "$FAKE_DIR/curl"

RELEASE="$TMP/release.json"
printf '{"tag_name":"v2.9.0","body":"Первая строка\\nвторая строка с \\"кавычками\\""}\n' > "$RELEASE"
BLOCKPAGE="$TMP/blockpage.html"
printf '<html><body>Доступ ограничен</body></html>\n' > "$BLOCKPAGE"

run() { # run [env assignments...]
    env PATH="$FAKE_DIR:$PATH" \
        AMNEZIA_UPDATE_STATUS_DIR="$STATUS_DIR" \
        AMNEZIA_UPDATE_URL="https://api.github.com/x" \
        "$@" bash "$SCRIPT"
}

# --- a good answer -----------------------------------------------------
out="$(run CURL_BODY="$RELEASE" 2>&1)"; rc=$?
check "a successful check exits 0" test "$rc" = "0"
check "it stores the release GitHub returned" \
    grep -q '"tag_name":"v2.9.0"' "$STATUS_DIR/update-latest.json"
# Кавычки и переносы в описании выпуска — обычное дело; скрипт их не
# пересобирает, а значит и не порвёт.
check "the release notes survive quotes and newlines untouched" \
    diff -q "$RELEASE" "$STATUS_DIR/update-latest.json"
check "it records the check as successful" \
    grep -q '"result":"ok"' "$STATUS_DIR/update-check.json"
# Причина есть только у неудачи: успех ничего не объясняет.
check_not "a successful check explains nothing away" \
    grep -q reason "$STATUS_DIR/update-check.json"
check "and when it happened" \
    grep -qE '"checked_at_utc":"[0-9]{4}-[0-9]{2}-[0-9]{2}T' "$STATUS_DIR/update-check.json"
mode="$(stat -c %a "$STATUS_DIR/update-latest.json" 2>/dev/null \
        || stat -f %Lp "$STATUS_DIR/update-latest.json")"
check "the panel can read the answer (mode $mode)" test "$mode" = "644"

# --- GitHub unreachable ------------------------------------------------
out="$(run CURL_RC=7 CURL_BODY="$RELEASE" 2>&1)"; rc=$?
check "an unreachable GitHub is not a failure of the script" test "$rc" = "0"
check "the previous answer stays where it was" \
    grep -q '"tag_name":"v2.9.0"' "$STATUS_DIR/update-latest.json"
check "the failure is recorded" \
    grep -q '"result":"failed"' "$STATUS_DIR/update-check.json"
# «Не достучались» и «достучались, а выпуска нет» советуют человеку разное:
# первое про его сеть, второе про нас.
check "and it says the server could not reach GitHub" \
    grep -q '"reason":"unreachable"' "$STATUS_DIR/update-check.json"

# --- GitHub answers, with nothing to offer -----------------------------
# Ровно этот случай перепутал живой сервер: выпусков в репозитории нет,
# releases/latest отвечает 404, а файл советовал чинить сеть.
out="$(run CURL_HTTP=404 CURL_BODY="$RELEASE" 2>&1)"; rc=$?
check "a 404 from GitHub is not a failure of the script" test "$rc" = "0"
check "a 404 is recorded as GitHub having no release" \
    grep -q '"reason":"no-release"' "$STATUS_DIR/update-check.json"
check_not "a 404 never reads as an unreachable server" \
    grep -q '"reason":"unreachable"' "$STATUS_DIR/update-check.json"
check "a 404 does not replace the release we already knew about" \
    grep -q '"tag_name":"v2.9.0"' "$STATUS_DIR/update-latest.json"

# --- something that is not a release -----------------------------------
out="$(run CURL_BODY="$BLOCKPAGE" 2>&1)"; rc=$?
check "a reply that is not a release is not a failure of the script" test "$rc" = "0"
check "a block page never replaces a real release" \
    grep -q '"tag_name":"v2.9.0"' "$STATUS_DIR/update-latest.json"
check "and it is recorded as a failed check" \
    grep -q '"result":"failed"' "$STATUS_DIR/update-check.json"
check "and the reason is the answer, not the network" \
    grep -q '"reason":"no-release"' "$STATUS_DIR/update-check.json"

# --- the very first check fails ----------------------------------------
rm -f "$STATUS_DIR/update-latest.json" "$STATUS_DIR/update-check.json"
run CURL_RC=7 CURL_BODY="$RELEASE" >/dev/null 2>&1
check_not "a first check that failed invents no release" \
    test -f "$STATUS_DIR/update-latest.json"
check "but it still says a check was attempted" \
    grep -q '"result":"failed"' "$STATUS_DIR/update-check.json"

# --- no deployment ------------------------------------------------------
env PATH="$FAKE_DIR:$PATH" AMNEZIA_UPDATE_STATUS_DIR="$TMP/nowhere" \
    CURL_BODY="$RELEASE" bash "$SCRIPT" >/dev/null 2>&1
check "a missing deployment is reported, not papered over" test "$?" != "0"

echo
echo "passed: ${PASSED}, failed: ${FAILED}"
[ "${FAILED}" = "0" ] || exit 1

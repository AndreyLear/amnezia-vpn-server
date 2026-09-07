#!/bin/bash
# amnezia-vpn-server-nukf tests for update-agent.sh.
#
# The agent runs as root on the host and rewrites the deployment, so what
# matters most is everything it refuses to do. What must hold:
#
#   - it never takes the request at its word: the version must look like a
#     version, must be newer than the installed one, and must exist as a
#     release. The panel is on the public internet behind a password, and
#     its word is not an order;
#   - only forward: whoever takes the panel must not be able to walk the
#     server back onto a release with a known hole;
#   - a checksum that does not match stops it before anything is touched;
#   - the request file is consumed, or the path unit would fire again on
#     the same doomed request;
#   - a failed install is rolled back from the snapshot, and a rollback
#     that also fails says so instead of reporting success.
#
# Runs on plain bash (macOS or Linux); no root, no Docker, no network.
# curl, sha256sum, docker and the installer are shims on PATH.
set -u

cd "$(dirname "$0")/../.."
AGENT="$PWD/update-agent.sh"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-agent-test.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

pass() { PASSED=$((PASSED + 1)); printf 'ok   %s\n' "$1"; }
fail() { FAILED=$((FAILED + 1)); printf 'FAIL %s\n' "$1"; }
check() { local n="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$n"; else fail "$n"; fi; }
check_not() { local n="$1"; shift; if "$@" >/dev/null 2>&1; then fail "$n"; else pass "$n"; fi; }

FAKE_DIR="$TMP/bin"
mkdir -p "$FAKE_DIR"

# docker: the snapshot asks the panel for a database copy. Whether that
# works must not decide the update, so the shim just records the call.
cat > "$FAKE_DIR/docker" <<'FAKE'
#!/bin/bash
echo "docker $*" >> "${AGENT_CALLS:?}"
exit "${DOCKER_RC:-0}"
FAKE

# curl: the release lookup and the download. The status code comes from the
# environment so a case can be "GitHub answered 404" rather than "the shim
# failed", which the agent must tell apart.
cat > "$FAKE_DIR/curl" <<'FAKE'
#!/bin/bash
echo "curl $*" >> "${AGENT_CALLS:?}"
oarg=""; prev=""; url=""
for a in "$@"; do
    [ "$prev" = "-o" ] && oarg="$a"
    case "$a" in https://*) url="$a" ;; esac
    prev="$a"
done
case "$url" in
    *releases/tags/*)
        if [ "${RELEASE_RC:-0}" != "0" ]; then printf '000'; exit "${RELEASE_RC}"; fi
        [ -n "$oarg" ] && cat "${RELEASE_BODY:?}" > "$oarg"
        printf '%s' "${RELEASE_HTTP:-200}"
        ;;
    *releases/download/*)
        if [ "${ASSET_RC:-0}" != "0" ]; then printf '000'; exit "${ASSET_RC}"; fi
        [ -n "$oarg" ] && cat "${ASSET_FILE:?}" > "$oarg"
        printf '%s' "${ASSET_HTTP:-200}"
        ;;
    *) printf '404' ;;
esac
exit 0
FAKE
chmod +x "$FAKE_DIR"/*

# --- a deployment to update -------------------------------------------
# Пересобирается перед каждым случаем: агент его меняет, и случаи не должны
# доставаться друг другу в наследство.
ROOT="$TMP/deploy"
setup_deployment() { # setup_deployment [installed_version]
    rm -rf "$ROOT"
    mkdir -p "$ROOT/data" "$ROOT/status" "$ROOT/app"
    printf 'IMAGE_VERSION=%s\n' "${1:-2.8.2}" > "$ROOT/versions.lock"
    printf 'AWG_PORT=4500\n' > "$ROOT/.env"
    printf 'services: {}\n' > "$ROOT/compose.yaml"
    printf 'app tree\n' > "$ROOT/app/marker"
    for f in docker-prune.sh watchdog.sh update-check.sh update-agent.sh install.sh; do
        printf '#!/bin/sh\nexit 0\n' > "$ROOT/$f"
        chmod +x "$ROOT/$f"
    done
    # Установщик прежней версии, который поднимет откат. Отмечается в файле,
    # чтобы было видно, что позвали именно его.
    cat > "$ROOT/install.sh" <<INST
#!/bin/bash
echo "rollback-installer \$*" >> "\${AGENT_CALLS:?}"
exit \${ROLLBACK_INSTALL_RC:-0}
INST
    chmod +x "$ROOT/install.sh"
}

# --- a release to update to -------------------------------------------
# Настоящий архив с настоящей суммой: подделывать sha256sum значило бы не
# проверять ровно то, ради чего он тут.
build_release() { # build_release <version>
    local v="$1"
    local dir="$TMP/rel-$v"
    rm -rf "$dir"; mkdir -p "$dir/amnezia-vpn-server-$v"
    cat > "$dir/amnezia-vpn-server-$v/install.sh" <<INST
#!/bin/bash
echo "new-installer \$*" >> "\${AGENT_CALLS:?}"
exit \${NEW_INSTALL_RC:-0}
INST
    chmod +x "$dir/amnezia-vpn-server-$v/install.sh"
    (cd "$dir" && tar -czf "sources.tar.gz" "amnezia-vpn-server-$v")
    ASSET="$dir/sources.tar.gz"
    SHA="$(shasum -a 256 "$ASSET" 2>/dev/null || sha256sum "$ASSET")"
    SHA="${SHA%% *}"
}

# Имя файла берётся из самой суммы: путь возвращается наружу и живёт до конца
# прогона, а общий файл молча подменял бы описание, выданное раньше. Счётчик
# тут не годится — вызов идёт в подстановке, то есть в отдельной оболочке, и
# наружу его значение не возвращается.
release_body() { # release_body <sha or empty>
    local f="$TMP/release-${1:-none}.json"
    if [ -n "$1" ]; then
        printf '{"tag_name":"v9.9.9","body":"Что нового\\n\\namnezia-sha256: %s\\n"}\n' "$1" > "$f"
    else
        printf '{"tag_name":"v9.9.9","body":"Что нового, без суммы"}\n' > "$f"
    fi
    printf '%s' "$f"
}

request() { # request <version>
    printf '{"schema":"v1","version":"%s","requested_by":"admin"}\n' "$1" \
        > "$ROOT/data/update-request.json"
}

CALLS="$TMP/calls.log"
run_agent() { # run_agent [env assignments...]
    : > "$CALLS"
    env PATH="$FAKE_DIR:$PATH" AGENT_CALLS="$CALLS" \
        AMNEZIA_UPDATE_ROOT="$ROOT" \
        AMNEZIA_UPDATE_API="https://api.example.invalid" \
        AMNEZIA_UPDATE_DOWNLOADS="https://dl.example.invalid" \
        "$@" bash "$AGENT"
}

state() { cat "$ROOT/status/update-state.json" 2>/dev/null; }

# --- the ordinary case -------------------------------------------------
setup_deployment 2.8.2
build_release 2.9.0
BODY="$(release_body "$SHA")"
request 2.9.0
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" >/dev/null 2>&1; rc=$?
check "an ordinary update succeeds" test "$rc" = "0"
check "it runs the installer that came with the release" \
    grep -q "new-installer --root $ROOT" "$CALLS"
# Без флагов намеренно: значения развёртывания установщик помнит в .env, а
# передавать их заново значило бы дать запросу из панели право их менять.
check_not "and it passes no deployment flags of its own" \
    grep -qE "new-installer .*--(awg-port|panel-port|domain|vpn-subnet|ipv6)" "$CALLS"
check "the state says it finished" grep -q '"state":"ok"' <<<"$(state)"
check "the request is consumed" test ! -f "$ROOT/data/update-request.json"
check "a copy of the database was asked for" grep -q "docker compose" "$CALLS"
check "the snapshot is cleared once it worked" test ! -d "$ROOT/.rollback"

# --- the request is not taken at its word ------------------------------
setup_deployment 2.8.2
request 2.7.0
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" >/dev/null 2>&1; rc=$?
check "walking backwards is refused" test "$rc" != "0"
check "and it says which versions it compared" \
    grep -q '2.7.0' <<<"$(state)"
check_not "nothing was downloaded" grep -q "releases/download" "$CALLS"
check "the refused request is consumed too" test ! -f "$ROOT/data/update-request.json"

setup_deployment 2.8.2
request 2.8.2
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" >/dev/null 2>&1
check "the version already installed is not work" grep -q '"state":"refused"' <<<"$(state)"

setup_deployment 2.8.2
printf '{"version":"$(reboot)"}\n' > "$ROOT/data/update-request.json"
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" >/dev/null 2>&1
check "a version that is not a version is refused" grep -q '"state":"refused"' <<<"$(state)"
check_not "and it never reaches the network" grep -q "curl" "$CALLS"

# --- the release has to exist ------------------------------------------
setup_deployment 2.8.2
request 2.9.0
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" RELEASE_HTTP=404 >/dev/null 2>&1
check "a version with no release is refused" grep -q '"state":"refused"' <<<"$(state)"
check_not "and nothing is downloaded" grep -q "releases/download" "$CALLS"

setup_deployment 2.8.2
request 2.9.0
run_agent RELEASE_BODY="$(release_body "")" ASSET_FILE="$ASSET" >/dev/null 2>&1
check "a release with no checksum is refused" grep -q '"state":"refused"' <<<"$(state)"
check_not "and nothing is downloaded" grep -q "releases/download" "$CALLS"

# --- the checksum stops it before anything is touched ------------------
setup_deployment 2.8.2
request 2.9.0
WRONG="$(release_body "0000000000000000000000000000000000000000000000000000000000000000")"
run_agent RELEASE_BODY="$WRONG" ASSET_FILE="$ASSET" >/dev/null 2>&1; rc=$?
check "a checksum that does not match is refused" test "$rc" != "0"
check "and the refusal names the checksum" grep -q 'сумма' <<<"$(state)"
check_not "no installer ran" grep -q "new-installer" "$CALLS"
check_not "no snapshot was taken" test -d "$ROOT/.rollback"

# --- a failed install is rolled back -----------------------------------
setup_deployment 2.8.2
request 2.9.0
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" NEW_INSTALL_RC=1 >/dev/null 2>&1; rc=$?
check "a failed install is reported as failed" test "$rc" != "0"
check "the previous installer was called to put it back" \
    grep -q "rollback-installer --root $ROOT" "$CALLS"
check "the state says the server is back on the old release" \
    grep -q '"state":"rolled-back"' <<<"$(state)"
check "and it names the release it is running" grep -q '2.8.2' <<<"$(state)"

# --- a rollback that fails too is not called success -------------------
setup_deployment 2.8.2
request 2.9.0
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" \
    NEW_INSTALL_RC=1 ROLLBACK_INSTALL_RC=1 >/dev/null 2>&1; rc=$?
check "a failed rollback exits non-zero" test "$rc" != "0"
check "and says the server needs a person" grep -q '"state":"failed"' <<<"$(state)"

# --- the journal survives ----------------------------------------------
check "the steps are written down for afterwards" test -s "$ROOT/status/update-log.txt"

# --- one at a time -----------------------------------------------------
# Два install.sh в одном каталоге подерутся, и разбирать это придётся руками
# на живом сервере. flock есть только на Linux — на машине разработчика этот
# случай проверить нечем, и притворяться, что проверили, не нужно.
if command -v flock >/dev/null 2>&1; then
    setup_deployment 2.8.2
    request 2.9.0
    # Замок держит посторонний процесс: так выглядит уже идущее обновление.
    ( flock 8; sleep 5 ) 8>"$ROOT/update-agent.lock" &
    holder=$!
    sleep 1
    run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" >/dev/null 2>&1; rc=$?
    check "a second request during an update is refused" test "$rc" != "0"
    # Запрос убирается, хотя выполнен не будет. Оставленный, он дождался бы
    # конца текущего прогона и запустил бы обновление снова — а после
    # неудачной попытки с откатом это значит, что сервер повторит её сам,
    # никого не спросив.
    check_not "и отклонённый запрос не ждёт своего часа" \
        test -f "$ROOT/data/update-request.json"
    check_not "nothing was installed by the second run" \
        grep -q "new-installer" "$CALLS"
    wait "$holder" 2>/dev/null
else
    printf 'skip a second request during an update: flock is Linux-only\n'
fi

# --- no request, no work -----------------------------------------------
setup_deployment 2.8.2
run_agent RELEASE_BODY="$BODY" ASSET_FILE="$ASSET" >/dev/null 2>&1
check "no request is not an error" test "$?" = "0"

echo
echo "passed: ${PASSED}, failed: ${FAILED}"
[ "${FAILED}" = "0" ] || exit 1

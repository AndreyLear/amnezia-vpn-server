#!/bin/bash
# T-164 cleanup.sh harness: compose down must pass --env-file versions.lock.
#
# Scripted fakes only (PATH prepend). Does not touch a live Docker daemon
# or /opt/amnezia-vpn. COMPOSE_DIR/SRC_DIR are redirected into a tempdir.
#
#   bash app/panel/test_m164_cleanup.sh
#
# Exit code: 0 when every test passes; 1 otherwise.

set -u

M164_ERRORS=0
M164_HOME="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLEANUP_SH="$M164_HOME/cleanup.sh"

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; M164_ERRORS=$((M164_ERRORS + 1)); }

FAKE_DIR="$(mktemp -d /tmp/m164-fakes.XXXXXX)"
FAKE_CALLS="$FAKE_DIR/calls.log"
FAKE_STATE="$FAKE_DIR/state.env"
TMP_TEST="$(mktemp -d /tmp/m164-run.XXXXXX)"
export FAKE_DIR FAKE_CALLS FAKE_STATE
trap 'rm -rf "$FAKE_DIR" "$TMP_TEST"' EXIT

fakes_reset() {
    : > "$FAKE_CALLS"
    cat > "$FAKE_STATE" <<EOF
CONTAINERS=
EOF
    rm -rf "$TMP_TEST/opt" "$TMP_TEST/src"
    mkdir -p "$TMP_TEST/opt" "$TMP_TEST/src"
}

cat > "$FAKE_DIR/id" <<'FAKE_EOF'
#!/bin/bash
if [ "${1:-}" = "-u" ]; then
    printf '0\n'
    exit 0
fi
exec /usr/bin/id "$@"
FAKE_EOF

cat > "$FAKE_DIR/docker" <<'FAKE_EOF'
#!/bin/bash
echo "docker $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
case "${1:-}" in
    compose)
        # Reproduce interpolation noise if --env-file is missing.
        has_env=0
        for a in "$@"; do
            if [ "$a" = "--env-file" ]; then
                has_env=1
                break
            fi
        done
        if [ "$has_env" -eq 0 ]; then
            printf '%s\n' \
                'error while interpolating services.panel.build.args.GO_VERSION: required variable GO_VERSION is missing a value: GO_VERSION must be set via versions.lock' >&2
            exit 1
        fi
        exit 0
        ;;
    ps)
        if [ -n "${CONTAINERS:-}" ]; then
            printf '%s\n' $CONTAINERS
        fi
        exit 0
        ;;
    images|volume)
        exit 0
        ;;
    rm)
        exit 0
        ;;
esac
exit 0
FAKE_EOF

cat > "$FAKE_DIR/nft" <<'FAKE_EOF'
#!/bin/bash
echo "nft $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF

cat > "$FAKE_DIR/ip" <<'FAKE_EOF'
#!/bin/bash
echo "ip $*" >> "${FAKE_CALLS:?}"
exit 1
FAKE_EOF

cat > "$FAKE_DIR/systemctl" <<'FAKE_EOF'
#!/bin/bash
echo "systemctl $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF

chmod +x "$FAKE_DIR/id" "$FAKE_DIR/docker" "$FAKE_DIR/nft" "$FAKE_DIR/ip" "$FAKE_DIR/systemctl"

plant_compose() { # plant_compose [with-lock|no-lock]
    printf 'services:\n  panel:\n    image: amnezia-vpn-server/panel:2.0.0\n' \
        > "$TMP_TEST/opt/compose.yaml"
    if [ "${1:-with-lock}" = "with-lock" ]; then
        printf 'GO_VERSION=1.25.7\nALPINE_VERSION=3.23\n' > "$TMP_TEST/opt/versions.lock"
    else
        rm -f "$TMP_TEST/opt/versions.lock"
    fi
}

run_cleanup() {
    COMPOSE_DIR="$TMP_TEST/opt" \
    SRC_DIR="$TMP_TEST/src" \
    PATH="$FAKE_DIR:$PATH" \
    bash "$CLEANUP_SH" --i-know-what-i-am-doing "$@" \
        > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    echo $?
}

assert_no_interpolation() {
    if grep -E 'required variable (GO_VERSION|ALPINE_VERSION)' "$TMP_TEST/out" "$TMP_TEST/err" >/dev/null 2>&1; then
        fail "$1: interpolation error printed"
        return 0
    fi
    pass "$1: no interpolation errors"
}

# --- tests -------------------------------------------------------------

test_bash_syntax() {
    bash -n "$CLEANUP_SH" && pass "bash -n cleanup.sh" || fail "bash -n cleanup.sh"
    bash -n "${BASH_SOURCE[0]}" && pass "bash -n test_m164_cleanup.sh" \
        || fail "bash -n test_m164_cleanup.sh"
}

test_dry_run_shows_env_file_when_lock_exists() {
    fakes_reset
    plant_compose with-lock
    rc="$(run_cleanup)"
    [ "$rc" = "0" ] || fail "dry-run with lock: exit $rc"
    grep -q "from $TMP_TEST/opt" "$TMP_TEST/out" \
        && pass "dry-run with lock: uses COMPOSE_DIR override" \
        || fail "dry-run with lock: COMPOSE_DIR override ignored"
    grep -q -- '--env-file versions.lock' "$TMP_TEST/out" \
        && pass "dry-run with lock: would-run includes --env-file versions.lock" \
        || fail "dry-run with lock: missing --env-file versions.lock in would-run"
    grep -q 'would run: docker compose --env-file versions.lock down' "$TMP_TEST/out" \
        && pass "dry-run with lock: compose down would-run line" \
        || fail "dry-run with lock: compose down would-run line missing"
    assert_no_interpolation "dry-run with lock"
}

test_dry_run_skips_compose_down_without_lock() {
    fakes_reset
    plant_compose no-lock
    rc="$(run_cleanup)"
    [ "$rc" = "0" ] || fail "dry-run without lock: exit $rc"
    if grep -q 'would run: docker compose' "$TMP_TEST/out"; then
        fail "dry-run without lock: should skip compose down"
    else
        pass "dry-run without lock: compose down skipped"
    fi
    grep -qi 'versions.lock' "$TMP_TEST/out" \
        && pass "dry-run without lock: skip mentions versions.lock" \
        || fail "dry-run without lock: skip does not mention versions.lock"
}

test_yes_passes_env_file_and_removes_stack_root() {
    fakes_reset
    plant_compose with-lock
    printf 'CONTAINERS=amnezia-vpn-panel-1\n' > "$FAKE_STATE"
    rc="$(run_cleanup --yes)"
    [ "$rc" = "0" ] || fail "--yes with lock: exit $rc ($(cat "$TMP_TEST/err"))"
    if grep -q 'docker compose' "$FAKE_CALLS"; then
        pass "--yes with lock: docker compose invoked"
    else
        fail "--yes with lock: docker compose never invoked ($(cat "$FAKE_CALLS"))"
    fi
    grep -q -- 'docker compose .*--env-file' "$FAKE_CALLS" \
        && pass "--yes with lock: docker compose got --env-file" \
        || fail "--yes with lock: docker compose missing --env-file ($(cat "$FAKE_CALLS"))"
    if grep 'docker compose' "$FAKE_CALLS" | grep -v -- '--env-file' >/dev/null; then
        fail "--yes with lock: compose invoked without --env-file"
    else
        pass "--yes with lock: every compose invocation has --env-file"
    fi
    grep -q 'docker rm -f amnezia-vpn-panel-1' "$FAKE_CALLS" \
        && pass "--yes with lock: name-based docker rm still runs" \
        || fail "--yes with lock: docker rm not called"
    [ ! -d "$TMP_TEST/opt" ] && [ ! -d "$TMP_TEST/src" ] \
        && pass "--yes with lock: COMPOSE_DIR and SRC_DIR removed" \
        || fail "--yes with lock: deploy roots still present"
    assert_no_interpolation "--yes with lock"
}

test_yes_skips_compose_down_without_lock_still_rms() {
    fakes_reset
    plant_compose no-lock
    printf 'CONTAINERS=amnezia-vpn-awg-1\n' > "$FAKE_STATE"
    rc="$(run_cleanup --yes)"
    [ "$rc" = "0" ] || fail "--yes without lock: exit $rc"
    if grep -q 'docker compose' "$FAKE_CALLS"; then
        fail "--yes without lock: compose down should be skipped"
    else
        pass "--yes without lock: compose down skipped"
    fi
    grep -q 'docker rm -f amnezia-vpn-awg-1' "$FAKE_CALLS" \
        && pass "--yes without lock: name-based docker rm still runs" \
        || fail "--yes without lock: docker rm not called"
    [ ! -d "$TMP_TEST/opt" ] \
        && pass "--yes without lock: COMPOSE_DIR still removed" \
        || fail "--yes without lock: COMPOSE_DIR still present"
    assert_no_interpolation "--yes without lock"
}

test_bash_syntax
test_dry_run_shows_env_file_when_lock_exists
test_dry_run_skips_compose_down_without_lock
test_yes_passes_env_file_and_removes_stack_root
test_yes_skips_compose_down_without_lock_still_rms

echo
if [ "$M164_ERRORS" -eq 0 ]; then
    echo "T-164 cleanup.sh: ALL TESTS PASSED"
    exit 0
fi
echo "T-164 cleanup.sh: $M164_ERRORS test(s) FAILED"
exit 1

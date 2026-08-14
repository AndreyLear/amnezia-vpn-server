#!/bin/bash
# M9.1 install.sh test harness (AUDITS/M9.1_AUDIT.md §критерии приёмки).
#
# Runs install.sh against scripted fakes: nothing on the real host is
# touched (no Docker config, no apt, no systemd, no sysctl, no /etc).
# Every privileged/system command is a fake under the harness FAKE_DIR,
# prepended to PATH; host paths that install.sh writes are redirected
# into the harness tempdir through the AMNEZIA_INSTALL_* hooks.
#
#   bash app/panel/test_m91_install.sh
#
# Exit code: 0 when every test passes; 1 otherwise.

set -u

M91_ERRORS=0
M91_HOME="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_SH="$M91_HOME/install.sh"

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; M91_ERRORS=$((M91_ERRORS + 1)); }

# --- fakes -------------------------------------------------------------

FAKE_DIR="$(mktemp -d /tmp/m91-fakes.XXXXXX)"
FAKE_STATE="$FAKE_DIR/state.env"
FAKE_CALLS="$FAKE_DIR/calls.log"
FAKE_FS="$FAKE_DIR/fs"
TMP_TEST="$(mktemp -d /tmp/m91-run.XXXXXX)"
export FAKE_DIR FAKE_STATE FAKE_CALLS FAKE_FS
trap 'rm -rf "$FAKE_DIR" "$TMP_TEST"' EXIT

setstate() { # portable in-place update: sed(1) -i differs on BSD/GNU
    local key="$1" value="$2" file="$3"
    sed "s|^${key}=.*|${key}=${value}|" "$file" > "$file.new" && mv "$file.new" "$file"
}

fakes_reset() {
    : > "$FAKE_CALLS"
    mkdir -p "$FAKE_FS"
    rm -rf "$ROOT" "$SYSCTL_TEST" "$KEYRING_TEST" "$SOURCES_TEST" \
        "$NFTABLES_DIR_TEST" "$SYSTEMD_DIR_TEST"
    rm -f "$NFTABLES_CONF_TEST"
    cat > "$FAKE_STATE" <<EOF
COMPOSE_VERSION=${1:-2.30.1}
NEW_COMPOSE_VERSION=
DAEMON=ok
IP_FORWARD=1
NFT_CHECK_RC=0
NFT_APPLY_RC=0
NFT_APPLIED=0
MODPROBE_OK=yes
DU_IN_ACCEPT=0
DU_OUT_ACCEPT=0
UP_RC=0
EOF
    # The panel-init log the installer inspects on `up -d` failure
    # (T-111); a dedicated file so the value with spaces never enters
    # the sourced state.
    printf '%s\n' "panel init: no server row (id=1); insert server configuration first" \
        > "$FAKE_DIR/pi_log.txt"
}

# fake binaries: bash shims that log to $FAKE_CALLS and answer from state.

cat > "$FAKE_DIR/docker" <<'FAKE_EOF'
#!/bin/bash
LOG="${FAKE_CALLS:?}"
echo "docker $*" >> "$LOG"
. "${FAKE_STATE:?}"
if [ "${1:-}" = "info" ]; then
    [ "$DAEMON" = "ok" ] || exit 1
    echo "Server: FAKE 27.0.0"
    exit 0
fi
if [ "${1:-}" = "compose" ]; then
    shift
    verb=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --env-file) shift 2 ;;
            version | config | build | up | ps | logs) verb="$1"; break ;;
            *) shift ;;
        esac
    done
    case "$verb" in
        version)
            echo "Docker Compose version v${COMPOSE_VERSION}"
            ;;
        config)
            exit "${COMPOSE_CONFIG_RC:-0}"
            ;;
        logs)
            # T-111: install.sh distinguishes the M3.1 fresh-install
            # state from real panel-init failures via the log text.
            cat "$FAKE_DIR/pi_log.txt"
            ;;
        ps)
            echo "amneziavpn-panel-init-1   panel-init   /app/panel init   Exited (1) 0 seconds ago"
            echo "amneziavpn-panel-1        panel        /app/panel serve  Created 0 seconds ago"
            echo "amneziavpn-awg-1          awg          /entrypoint.sh    Created 0 seconds ago"
            ;;
        build) exit 0 ;;
        up) exit "${UP_RC:-0}" ;;
    esac
    exit 0
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/apt-get" <<'FAKE_EOF'
#!/bin/bash
echo "apt-get $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
if [ "${1:-}" = "install" ]; then
    touch "$FAKE_FS/apt-installed"
    if [ -n "$NEW_COMPOSE_VERSION" ]; then
        sed "s|^COMPOSE_VERSION=.*|COMPOSE_VERSION=${NEW_COMPOSE_VERSION}|" "$FAKE_STATE" \
            > "$FAKE_STATE.new" && mv "$FAKE_STATE.new" "$FAKE_STATE"
    fi
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/systemctl" <<'FAKE_EOF'
#!/bin/bash
echo "systemctl $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF

cat > "$FAKE_DIR/sysctl" <<'FAKE_EOF'
#!/bin/bash
echo "sysctl $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
case "$*" in
    *"-n net.ipv4.ip_forward"*) echo "$IP_FORWARD" ;;
    *"-w net.ipv4.ip_forward=1"*)
        sed 's|^IP_FORWARD=.*|IP_FORWARD=1|' "$FAKE_STATE" > "$FAKE_STATE.new" \
            && mv "$FAKE_STATE.new" "$FAKE_STATE"
        echo "net.ipv4.ip_forward = 1"
        ;;
esac
exit 0
FAKE_EOF

cat > "$FAKE_DIR/nft" <<'FAKE_EOF'
#!/bin/bash
echo "nft $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
if [ "${1:-}" = "-c" ]; then
    # syntax-only check step (nft -c -f FILE)
    exit "${NFT_CHECK_RC:-0}"
fi
if [ "${1:-}" = "-f" ]; then
    # atomic apply (nft -f FILE); on failure install.sh rolls back
    [ "${NFT_APPLY_RC:-0}" = "0" ] || exit "${NFT_APPLY_RC}"
    sed 's|^NFT_APPLIED=.*|NFT_APPLIED=1|' "$FAKE_STATE" > "$FAKE_STATE.new" \
        && mv "$FAKE_STATE.new" "$FAKE_STATE"
    exit 0
fi
if [ "${1:-}" = "list" ]; then
    # post-reload verification (nft list table ip amnezia)
    [ "${NFT_APPLIED:-0}" = "1" ] || exit 1
    echo "table ip amnezia {"
    exit 0
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/curl" <<'FAKE_EOF'
#!/bin/bash
echo "curl $*" >> "${FAKE_CALLS:?}"
oarg=""
prev=""
for a in "$@"; do
    [ "$prev" = "-o" ] && oarg="$a"
    prev="$a"
done
if [ -n "$oarg" ]; then
    mkdir -p "$(dirname "$oarg")"
    printf 'FAKE-DOCKER-GPG-KEY\n' > "$oarg"
fi
exit 0
FAKE_EOF

chmod +x "$FAKE_DIR/docker" "$FAKE_DIR/apt-get" "$FAKE_DIR/systemctl" "$FAKE_DIR/sysctl" "$FAKE_DIR/nft" "$FAKE_DIR/curl"

# M9.2c fakes: the installer probes the AmneziaWG client stack and
# manages the docker/ufw forward-accept coexistence rules.
cat > "$FAKE_DIR/modprobe" <<'FAKE_EOF'
#!/bin/bash
echo "modprobe $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
[ "${MODPROBE_OK:-yes}" = "yes" ] || exit 1
exit 0
FAKE_EOF

cat > "$FAKE_DIR/awg" <<'FAKE_EOF'
#!/bin/bash
echo "awg $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
if [ "${1:-}" = "version" ]; then
    echo "awg 1.0.20260223"
    exit 0
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/uname" <<'FAKE_EOF'
#!/bin/bash
echo "uname $*" >> "${FAKE_CALLS:?}"
[ "${1:-}" = "-r" ] && echo "6.8.0-fake"
exit 0
FAKE_EOF

cat > "$FAKE_DIR/add-apt-repository" <<'FAKE_EOF'
#!/bin/bash
echo "add-apt-repository $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF

cat > "$FAKE_DIR/iptables" <<'FAKE_EOF'
#!/bin/bash
echo "iptables $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
act=""; chain=""; inf=""
for a in "$@"; do
    case "$a" in
        -L) act="L" ;;
        -C) act="C" ;;
        -I) act="I" ;;
        -t | filter | 1) ;;
        DOCKER-USER) chain="DU" ;;
        FORWARD) chain="FW" ;;
        -i) inf="IN" ;;
        -o) inf="OUT" ;;
        -j | ACCEPT) ;;
    esac
done
if [ "$act" = "L" ]; then
    [ "$chain" = "DU" ] && [ "${DU_CHAIN:-yes}" = "no" ] && exit 1
    exit 0
fi
if [ "$act" = "C" ]; then
    eval "v=\${${chain}_${inf}_ACCEPT:-0}"
    [ "$v" = "1" ] && exit 0 || exit 1
fi
setstate_val="${chain}_${inf}_ACCEPT"
sed "s/^${setstate_val}=.*/${setstate_val}=1/" "$FAKE_STATE" > "$FAKE_STATE.new" \
    && mv "$FAKE_STATE.new" "$FAKE_STATE"
exit 0
FAKE_EOF

chmod +x "$FAKE_DIR/modprobe" "$FAKE_DIR/awg" "$FAKE_DIR/uname" \
    "$FAKE_DIR/add-apt-repository" "$FAKE_DIR/iptables"

# --- harness plumbing ---------------------------------------------------

os_release() { # os_release ID VERSION CODENAME
    cat > "$TMP_TEST/os-release" <<EOF
PRETTY_NAME="$1 $2"
ID=$1
VERSION_ID="$2"
VERSION_CODENAME=$3
EOF
}

ROOT="$TMP_TEST/root"
SYSCTL_TEST="$TMP_TEST/sysctl.d"
KEYRING_TEST="$TMP_TEST/apt/keyrings"
SOURCES_TEST="$TMP_TEST/apt/sources.list.d"
NFTABLES_DIR_TEST="$TMP_TEST/nftables.d"
NFTABLES_CONF_TEST="$TMP_TEST/nftables.conf"
SYSTEMD_DIR_TEST="$TMP_TEST/systemd"

run_install() { # run_install [--root X] [--awg-port N] ... — stdout captured
    AMNEZIA_INSTALL_TEST=1 \
    AMNEZIA_INSTALL_FAKE_DIR="$FAKE_DIR" \
    AMNEZIA_INSTALL_OS_RELEASE="$TMP_TEST/os-release" \
    AMNEZIA_INSTALL_SYSCTL_DIR="$SYSCTL_TEST" \
    AMNEZIA_INSTALL_KEYRING_DIR="$KEYRING_TEST" \
    AMNEZIA_INSTALL_APT_SOURCES_DIR="$SOURCES_TEST" \
    AMNEZIA_INSTALL_NFTABLES_DIR="$NFTABLES_DIR_TEST" \
    AMNEZIA_INSTALL_NFTABLES_CONF="$NFTABLES_CONF_TEST" \
    AMNEZIA_INSTALL_SYSTEMD_DIR="$SYSTEMD_DIR_TEST" \
    PATH="$FAKE_DIR:$PATH" \
    bash "$INSTALL_SH" --root "$ROOT" "$@" > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    rc=$?
    if [ -n "${M91_DEBUG:-}" ] && [ "$rc" != "0" ]; then
        echo "=== DEBUG run rc=$rc ===" >&2
        cat "$TMP_TEST/out" "$TMP_TEST/err" >&2
    fi
    echo $rc
}

stdout() { cat "$TMP_TEST/out"; }
stderr() { cat "$TMP_TEST/err"; }

assert_contains() { # needle haystack-file
    grep -q "$1" "$2" || { fail "missing \"$1\" in $3"; return 0; }
    return 0
}

# --- tests ---------------------------------------------------------------

test_bash_syntax() {
    bash -n "$INSTALL_SH" && pass "bash -n install.sh" || fail "bash -n install.sh"
}

test_unsupported_os() {
    fakes_reset
    rm -rf "$ROOT"
    os_release arch rolling rolling
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "unsupported OS: exit $rc, want 1"
    [ -d "$ROOT" ] || pass "unsupported OS: deployment root not created" \
        || true
    [ -d "$ROOT" ] && fail "unsupported OS: deployment root was created"
    grep -q "unsupported OS" "$TMP_TEST/err" && pass "unsupported OS: rejection message" \
        || fail "unsupported OS: rejection message"
}

test_supported_os_matrix() {
    for spec in "debian:12:bookworm" "ubuntu:22.04:jammy" "ubuntu:24.04:noble"; do
        id="${spec%%:*}"; rest="${spec#*:}"; ver="${rest%%:*}"; code="${rest#*:}"
        fakes_reset
        rm -rf "$ROOT"
        os_release "$id" "$ver" "$code"
        rc="$(run_install)"
        if [ "$rc" = "0" ]; then
            pass "supported OS flow: $id $ver"
        else
            fail "supported OS flow: $id $ver (exit $rc)"
        fi
    done
}

test_compose_current_skips_apt() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "compose>=2.24.2 flow: exit $rc"
    if grep -q "apt-get" "$FAKE_CALLS"; then
        fail "compose>=2.24.2: apt-get was invoked although Docker was complete"
    else
        pass "compose>=2.24.2: apt-get not invoked"
    fi
}

test_docker_missing_installs() {
    # Docker "absent": the fake answers an empty compose version (no
    # usable engine/plugin), so the installer must go through the
    # official-repo installation path; the fake stays absent after the
    # install, so the run ends with the contract exit 1 — proving the
    # install attempt happened before the refusal. The fake always
    # shadows the real host docker CLI (no host lookup ever happens).
    fakes_reset
    setstate COMPOSE_VERSION "" "$FAKE_STATE"
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "docker missing: exit $rc, want 1 (still missing after install)"
    [ -f "$FAKE_FS/apt-installed" ] && pass "docker missing: apt installation ran" \
        || fail "docker missing: apt installation did not run"
    [ -f "$KEYRING_TEST/docker.asc" ] && pass "docker missing: keyring written via injected dir" \
        || fail "docker missing: keyring not written"
    grep -q "still missing or < 2.24.2" "$TMP_TEST/err" && pass "docker missing: refusal message" \
        || fail "docker missing: refusal message"
}

test_compose_old_upgraded() {
    fakes_reset "2.19.6"
    setstate NEW_COMPOSE_VERSION 2.30.0 "$FAKE_STATE"
    os_release ubuntu 22.04 jammy
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "compose upgrade path: exit $rc"
    grep -q "apt-get.*install.*docker-ce" "$FAKE_CALLS" && pass "compose<2.24.2: official repo install ran" \
        || fail "compose<2.24.2: official repo install did not run"
    grep -q "docker-compose-plugin" "$FAKE_CALLS" && pass "compose<2.24.2: compose plugin in install set" \
        || fail "compose<2.24.2: compose plugin missing from install set"
}

test_compose_old_still_old() {
    fakes_reset "2.19.6"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "compose still <2.24.2: exit $rc, want 1"
    grep -q "still missing or < 2.24.2" "$TMP_TEST/err" && pass "compose still <2.24.2: refusal message" \
        || fail "compose still <2.24.2: refusal message"
}

test_compose_minimum_boundary() {
    # T-112: the compose.yaml env_file long syntax needs >= 2.24.2;
    # 2.24.2 passes, 2.24.1 and 2.23.x stop with a clear refusal.
    for v in "2.24.2" "2.30.1"; do
        fakes_reset "$v"
        os_release debian 12 bookworm
        rc="$(run_install)"
        [ "$rc" = "0" ] || fail "compose $v accepted: exit $rc"
    done
    pass "compose 2.24.2+ accepted"
    for v in "2.23.4" "2.24.1" "2.19.6"; do
        fakes_reset "$v"
        os_release debian 12 bookworm
        rc="$(run_install)"
        [ "$rc" = "1" ] || fail "compose $v rejected: exit $rc, want 1"
    done
    pass "compose < 2.24.2 rejected with a clear message"
}

test_invalid_port() {
    fakes_reset
    os_release debian 12 bookworm
    for bad in 0 65536 abc ""; do
        rc="$(run_install --awg-port "$bad")"
        [ "$rc" = "2" ] || fail "invalid port '$bad': exit $rc, want 2"
    done
    pass "invalid AWG_PORT values rejected (exit 2)"
}

test_default_port() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "default port flow: exit $rc"
    grep -q "AWG_PORT=51820" "$ROOT/.env" && pass "default AWG_PORT=51820 in .env" \
        || fail "default AWG_PORT=51820 in .env"
}

test_custom_port() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --awg-port 23456)"
    [ "$rc" = "0" ] || fail "custom port flow: exit $rc"
    grep -q "AWG_PORT=23456" "$ROOT/.env" && pass "custom AWG_PORT=23456 in .env" \
        || fail "custom AWG_PORT=23456 in .env"
}

test_unknown_argument() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --bogus)"
    [ "$rc" = "2" ] || fail "unknown argument: exit $rc, want 2"
    pass "unknown arguments rejected (exit 2)"
}

test_panel_loopback_and_no_sock() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "loopback flow: exit $rc"
    grep -q "127.0.0.1:8787:8787" "$ROOT/compose.yaml" && pass "panel stays loopback: 127.0.0.1:8787:8787" \
        || fail "panel loopback mapping missing"
    if grep -Eq '^[[:space:]]*- "0\.0\.0\.0' "$ROOT/compose.yaml"; then
        fail "panel published on 0.0.0.0:8787"
    else
        pass "panel never published on 0.0.0.0"
    fi
    grep -q "docker.sock" "$ROOT/compose.yaml" && fail "docker.sock present in installed compose" \
        || pass "no docker.sock in installed compose"
}

test_layout_and_permissions() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "layout flow: exit $rc"
    mode() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1" 2>/dev/null; }
    [ "$(mode "$ROOT")" = "750" ] && pass "root dir 0750" || fail "root dir perms: $(mode "$ROOT")"
    for sub in data config status backups; do
        [ "$(mode "$ROOT/$sub")" = "700" ] && pass "$sub/ 0700" || fail "$sub/ perms: $(mode "$ROOT/$sub")"
    done
    [ "$(mode "$ROOT/.env")" = "600" ] && pass ".env 0600" || fail ".env perms: $(mode "$ROOT/.env")"
    [ -f "$ROOT/app/panel/Dockerfile" ] && pass "app/ build context installed" \
        || fail "app/ build context not installed"
}

test_versions_lock_used() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "versions.lock flow: exit $rc"
    if cmp -s "$M91_HOME/versions.lock" "$ROOT/versions.lock"; then
        pass "installed versions.lock identical to the repo pin"
    else
        fail "installed versions.lock differs from the repo pin"
    fi
    grep -q -- "--env-file" "$FAKE_CALLS" && grep -q "versions.lock" "$FAKE_CALLS" \
        && pass "compose invoked with --env-file versions.lock" \
        || fail "compose not invoked with --env-file versions.lock"
}

test_ip_forward_disabled() {
    fakes_reset
    setstate IP_FORWARD 0 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "ip_forward disabled flow: exit $rc"
    [ -f "$SYSCTL_TEST/99-amnezia-vpn.conf" ] && grep -q "net.ipv4.ip_forward = 1" "$SYSCTL_TEST/99-amnezia-vpn.conf" \
        && pass "ip_forward persisted via injected sysctl.d" \
        || fail "ip_forward not persisted"
    grep -q "sysctl -w net.ipv4.ip_forward=1" "$FAKE_CALLS" && pass "ip_forward applied immediately" \
        || fail "ip_forward not applied immediately"
}

test_ip_forward_already_enabled() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "ip_forward enabled flow: exit $rc"
    [ -f "$SYSCTL_TEST/99-amnezia-vpn.conf" ] && fail "unrelated sysctl file written although forward=1" \
        || pass "no sysctl file written when already enabled"
}

test_installed_compose_contract() {
    # M9.2b: install.sh copies compose.yaml verbatim — the installed
    # copy must carry the host-mode/restart contract.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "installed compose flow: exit $rc"
    [ "$(grep -c '^    network_mode: host$' "$ROOT/compose.yaml")" = "1" ] \
        && pass "installed compose: awg network_mode: host" \
        || fail "installed compose: awg network_mode: host"
    [ "$(grep -c '^    restart: unless-stopped$' "$ROOT/compose.yaml")" = "2" ] \
        && pass "installed compose: restart: unless-stopped on panel+awg" \
        || fail "installed compose: restart: unless-stopped on panel+awg"
    [ "$(grep -c '^    sysctls:$' "$ROOT/compose.yaml")" = "0" ] \
        && pass "installed compose: no awg sysctls" \
        || fail "installed compose: no awg sysctls"
    [ "$(grep -c '^    ports:$' "$ROOT/compose.yaml")" = "1" ] \
        && pass "installed compose: only the panel maps ports" \
        || fail "installed compose: only the panel maps ports"
}

test_m31_fresh_install_tolerated() {
    # T-111: `up -d` failing with panel-init "no server row" is the
    # tolerated M3.1 fresh-install state.
    fakes_reset
    setstate UP_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "M3.1 tolerance flow: exit $rc"
    grep -q "fresh-install state" "$TMP_TEST/out" && pass "M3.1: fresh-install state tolerated" \
        || fail "M3.1: fresh-install message missing"
}

test_panel_init_failure_not_masked() {
    # T-111: a real panel-init failure (e.g. broken pending restore)
    # must abort the install with the actual log, not be masked as the
    # fresh-install state.
    fakes_reset
    setstate UP_RC 1 "$FAKE_STATE"
    printf '%s\n' "panel init: pending restore apply failed" > "$FAKE_DIR/pi_log.txt"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "panel-init failure: exit $rc, want 1"
    grep -q "docker compose up -d failed" "$TMP_TEST/err" && pass "panel-init failure: installer refused" \
        || fail "panel-init failure: installer masked it"
    grep -q "pending restore apply failed" "$TMP_TEST/err" && pass "panel-init failure: real log surfaced" \
        || fail "panel-init failure: log not surfaced"
}

test_sentinel_guard_not_masked() {
    # T-111 rework: the sentinel-guard message ("no server row was
    # found — the database was lost") contains the loose substring
    # "no server row" but must NOT be tolerated as the M3.1
    # fresh-install state — the exact marker is "no server row (id=1)".
    fakes_reset
    setstate UP_RC 1 "$FAKE_STATE"
    printf '%s\n' "panel init: .server-initialized exists but no server row was found — the database was lost or reset, refusing to create a fresh one" > "$FAKE_DIR/pi_log.txt"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "sentinel guard: exit $rc, want 1"
    grep -q "docker compose up -d failed" "$TMP_TEST/err" && pass "sentinel guard: installer refused" \
        || fail "sentinel guard: installer masked a data-loss case"
}

test_doctor_failure() {
    fakes_reset
    setstate DAEMON fail "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "doctor failure: exit $rc, want 1"
    grep -q "self-check: the Docker daemon is not reachable" "$TMP_TEST/err" \
        && pass "doctor failure: message on stderr" \
        || fail "doctor failure: message missing"
}

test_ssh_hint() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "hint flow: exit $rc"
    grep -q "ssh -L 8787:127.0.0.1:8787" "$TMP_TEST/out" && pass "SSH tunnel hint printed" \
        || fail "SSH tunnel hint missing from stdout"
}

test_secrets_absent() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "secrets flow: exit $rc"
    for needle in "private_key" "password" "age identity" "docker.asc" "AWG_PORT=23456"; do
        if grep -qi "$needle" "$TMP_TEST/out" "$TMP_TEST/err" 2>/dev/null; then
            fail "secret-like output leaked: \"$needle\""
        fi
    done
    pass "no secrets/credentials in stdout+stderr"
}

test_idempotent_rerun() {
    fakes_reset "2.19.6"
    setstate NEW_COMPOSE_VERSION 2.30.0 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "rerun first pass: exit $rc"
    echo "user-data" > "$ROOT/data/keep.txt"
    marker_before="$(awk -F= '/AWG_PORT/{print $2}' "$ROOT/.env")"
    apt_calls_before="$(grep -c "apt-get" "$FAKE_CALLS")"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "rerun second pass: exit $rc"
    [ -f "$ROOT/data/keep.txt" ] && pass "rerun: application data untouched" \
        || fail "rerun: application data destroyed"
    [ "$(awk -F= '/AWG_PORT/{print $2}' "$ROOT/.env")" = "$marker_before" ] \
        && pass "rerun: .env not overwritten" \
        || fail "rerun: .env overwritten"
    [ "$(grep -c "apt-get" "$FAKE_CALLS")" = "$apt_calls_before" ] \
        && pass "rerun: no repeated docker installation" \
        || fail "rerun: docker installation repeated"
}

# --- main ---------------------------------------------------------------

test_bash_syntax
test_unsupported_os
test_supported_os_matrix
test_compose_current_skips_apt
test_docker_missing_installs
test_compose_old_upgraded
test_compose_old_still_old
test_compose_minimum_boundary
test_invalid_port
test_default_port
test_custom_port
test_unknown_argument
test_panel_loopback_and_no_sock
test_installed_compose_contract
test_layout_and_permissions
test_versions_lock_used
test_ip_forward_disabled
test_ip_forward_already_enabled
test_m31_fresh_install_tolerated
test_panel_init_failure_not_masked
test_sentinel_guard_not_masked
test_doctor_failure
test_ssh_hint
test_secrets_absent
test_idempotent_rerun

echo
if [ "$M91_ERRORS" -eq 0 ]; then
    echo "M9.1 install.sh: ALL TESTS PASSED"
    exit 0
fi
echo "M9.1 install.sh: $M91_ERRORS test(s) FAILED"
exit 1
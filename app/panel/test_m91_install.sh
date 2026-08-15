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
PUBLIC_IP=2.26.93.192
DNS_A=2.26.93.192
DNS_AAAA=
CERTBOT_RC=0
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
# T-121: the DNS pre-flight asks the public IP service.
if printf '%s' "$*" | grep -q "api.ipify.org"; then
    . "${FAKE_STATE:?}"
    printf '%s\n' "${PUBLIC_IP}"
    exit 0
fi
if [ -n "$oarg" ]; then
    mkdir -p "$(dirname "$oarg")"
    printf 'FAKE-DOCKER-GPG-KEY\n' > "$oarg"
fi
exit 0
FAKE_EOF

# T-121 domain-mode fakes: DNS pre-flight (dig), reverse proxy (nginx),
# certificate issuance (certbot).
cat > "$FAKE_DIR/dig" <<'FAKE_EOF'
#!/bin/bash
echo "dig $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
case "$*" in
    *" A"*) [ -n "${DNS_A}" ] && printf '%s\n' "${DNS_A}" ;;
    *" AAAA"*) [ -n "${DNS_AAAA}" ] && printf '%s\n' "${DNS_AAAA}" ;;
esac
exit 0
FAKE_EOF

cat > "$FAKE_DIR/nginx" <<'FAKE_EOF'
#!/bin/bash
echo "nginx $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF

cat > "$FAKE_DIR/certbot" <<'FAKE_EOF'
#!/bin/bash
echo "certbot $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
[ "${CERTBOT_RC:-0}" = "0" ] || exit 1
touch "${FAKE_FS}/certbot-ok"
exit 0
FAKE_EOF

# T-124 fakes: openssl req generates the self-signed pair into the
# requested paths, openssl x509 answers the sha256 fingerprint.
cat > "$FAKE_DIR/openssl" <<'FAKE_EOF'
#!/bin/bash
echo "openssl $*" >> "${FAKE_CALLS:?}"
if [ "${1:-}" = "req" ]; then
    key=""; out=""
    prev=""
    for a in "$@"; do
        case "$prev" in
            -keyout) key="$a" ;;
            -out) out="$a" ;;
        esac
        prev="$a"
    done
    [ -n "$key" ] && [ -n "$out" ] || exit 1
    mkdir -p "$(dirname "$key")" "$(dirname "$out")"
    printf 'FAKE-PRIVATE-KEY\n' > "$key"
    printf 'FAKE-CERTIFICATE\n' > "$out"
    exit 0
fi
if [ "${1:-}" = "x509" ]; then
    printf '%s\n' \
        "SHA256 Fingerprint=AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67"
    exit 0
fi
exit 0
FAKE_EOF

chmod +x "$FAKE_DIR/curl" "$FAKE_DIR/dig" "$FAKE_DIR/nginx" "$FAKE_DIR/certbot" "$FAKE_DIR/openssl"

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
    AMNEZIA_INSTALL_ACME_ROOT="$TMP_TEST/acme" \
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
    grep -q "AWG_PORT=443" "$ROOT/.env" && pass "default AWG_PORT=443 in .env" \
        || fail "default AWG_PORT=443 in .env"
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

test_domain_default_no_domain() {
    # T-121: without --domain the loopback-only contract is untouched —
    # no dig/nginx/certbot, no 80/443 rules, SSH tunnel hint kept.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "no-domain flow: exit $rc"
    if grep -q "certbot" "$FAKE_CALLS"; then fail "no-domain: certbot was invoked"; else pass "no-domain: certbot not invoked"; fi
    if grep -q "nginx " "$FAKE_CALLS"; then fail "no-domain: nginx was invoked"; else pass "no-domain: nginx not invoked"; fi
    if grep -q "dig " "$FAKE_CALLS"; then fail "no-domain: dig was invoked"; else pass "no-domain: dig not invoked"; fi
    if grep -qE "tcp dport (80|443)" "$ROOT/nftables/amnezia-vpn.nft"; then fail "no-domain: 80/443 opened"; else pass "no-domain: 80/443 stay closed"; fi
    if grep -q "ssh -L 8787" "$TMP_TEST/out"; then pass "no-domain: SSH tunnel hint kept"; else fail "no-domain: SSH hint missing"; fi
}

test_domain_mode_flow() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com)"
    [ "$rc" = "0" ] || fail "domain flow: exit $rc"
    grep -q "dig +short" "$FAKE_CALLS" && pass "domain: DNS pre-flight ran" || fail "domain: DNS pre-flight missing"
    grep -q "certbot certonly" "$FAKE_CALLS" && pass "domain: certbot invoked" || fail "domain: certbot not invoked"
    [ -f "$ROOT/nginx/panel.conf" ] || fail "domain: managed nginx config missing"
    grep -q "listen 443 ssl" "$ROOT/nginx/panel.conf" && pass "domain: TLS site present" || fail "domain: TLS site missing"
    grep -q "proxy_pass http://127.0.0.1:8787" "$ROOT/nginx/panel.conf" && pass "domain: proxy to loopback panel" || fail "domain: proxy missing"
    grep -qE "^[[:space:]]*tcp dport (80|443) accept" "$ROOT/nftables/amnezia-vpn.nft" \
        && pass "domain: 80/443 accept in the managed ruleset" \
        || fail "domain: nft 80/443 accept missing"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "domain: https hint printed" || fail "domain: https hint missing"
    grep -q "ssh -L 8787" "$TMP_TEST/out" && fail "domain: SSH tunnel hint still printed" || pass "domain: SSH hint replaced by https"
    grep -q "AMNEZIA_SECURE_COOKIES=1" "$ROOT/.env" && pass "domain: AMNEZIA_SECURE_COOKIES=1 in .env" \
        || fail "domain: AMNEZIA_SECURE_COOKIES missing from .env"
    if grep -q "openssl" "$FAKE_CALLS"; then fail "domain: openssl was invoked (LE must win)"; else pass "domain: no self-signed certificate"; fi
}

test_domain_dns_mismatch() {
    fakes_reset
    setstate DNS_A 1.2.3.4 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com)"
    [ "$rc" = "1" ] || fail "domain DNS mismatch: exit $rc, want 1"
    grep -q "Create an A record" "$TMP_TEST/err" && pass "domain DNS mismatch: actionable message" \
        || fail "domain DNS mismatch: message missing"
    grep -q "certbot" "$FAKE_CALLS" && fail "domain DNS mismatch: certbot was still invoked" \
        || pass "domain DNS mismatch: certbot not invoked (rate limit safe)"
}

test_domain_dns_missing() {
    fakes_reset
    setstate DNS_A "" "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com)"
    [ "$rc" = "1" ] || fail "domain DNS missing: exit $rc, want 1"
    grep -q "Create an A record" "$TMP_TEST/err" && pass "domain DNS missing: actionable message" \
        || fail "domain DNS missing: message missing"
    grep -q "certbot" "$FAKE_CALLS" && fail "domain DNS missing: certbot invoked" \
        || pass "domain DNS missing: certbot not invoked"
}

test_domain_certbot_failure() {
    fakes_reset
    setstate CERTBOT_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com)"
    [ "$rc" = "1" ] || fail "domain certbot fail: exit $rc, want 1"
    grep -q "Let's Encrypt issuance failed" "$TMP_TEST/err" && pass "domain certbot fail: error surfaced" \
        || fail "domain certbot fail: message missing"
}

test_domain_invalid_fqdn() {
    fakes_reset
    os_release debian 12 bookworm
    for bad in "bad domain" "..x" "x..y" "-x.com" "x-.com" "x." "UPPER.com"; do
        rc="$(run_install --domain "$bad")"
        [ "$rc" = "2" ] || fail "invalid domain '$bad': exit $rc, want 2 (usage)"
    done
    pass "invalid --domain values rejected (exit 2)"
}

# --- T-124: panel IP:port mode (self-signed TLS) ------------------------

test_panel_port_mode_flow() {
    # IP mode: openssl generates the pair (key 0600), nginx terminates
    # TLS on the chosen port and proxies to the loopback panel, nftables
    # opens tcp 8443 ONLY in this mode, the summary prints the https
    # URL + sha256 fingerprint, .env carries AMNEZIA_SECURE_COOKIES=1,
    # and neither dig nor certbot are involved.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --panel-port 8443)"
    [ "$rc" = "0" ] || fail "panel-port flow: exit $rc"
    grep -q "openssl req -x509" "$FAKE_CALLS" && pass "panel-port: openssl req invoked" \
        || fail "panel-port: openssl req not invoked"
    grep -q -- "-days 825" "$FAKE_CALLS" && pass "panel-port: ~825-day certificate" \
        || fail "panel-port: certificate lifetime argument missing"
    [ -f "$ROOT/tls/panel.crt" ] && [ -f "$ROOT/tls/panel.key" ] \
        && pass "panel-port: cert+key under the deployment root" \
        || fail "panel-port: cert/key missing"
    mode() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1" 2>/dev/null; }
    [ "$(mode "$ROOT/tls/panel.key")" = "600" ] && pass "panel-port: TLS key 0600" \
        || fail "panel-port: TLS key perms: $(mode "$ROOT/tls/panel.key")"
    grep -q "listen 8443 ssl" "$ROOT/nginx/panel.conf" && pass "panel-port: nginx listens 8443 ssl" \
        || fail "panel-port: nginx listen missing"
    grep -q "ssl_certificate $ROOT/tls/panel.crt" "$ROOT/nginx/panel.conf" && pass "panel-port: nginx uses the self-signed cert" \
        || fail "panel-port: nginx cert directive missing"
    grep -q "proxy_pass http://127.0.0.1:8787" "$ROOT/nginx/panel.conf" && pass "panel-port: proxy to loopback panel" \
        || fail "panel-port: proxy missing"
    grep -qE "^[[:space:]]*tcp dport 8443 accept" "$ROOT/nftables/amnezia-vpn.nft" \
        && pass "panel-port: tcp 8443 accept in the managed ruleset" \
        || fail "panel-port: nft 8443 accept missing"
    if grep -qE "tcp dport (80|443)" "$ROOT/nftables/amnezia-vpn.nft"; then fail "panel-port: 80/443 must not be opened"; else pass "panel-port: 80/443 stay closed"; fi
    grep -q "https://2.26.93.192:8443" "$TMP_TEST/out" && pass "panel-port: https://ip:port printed" \
        || fail "panel-port: https URL missing from the summary"
    grep -q "AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89" "$TMP_TEST/out" \
        && pass "panel-port: sha256 fingerprint in the summary" \
        || fail "panel-port: fingerprint missing from the summary"
    grep -q "ssh -L 8787" "$TMP_TEST/out" && fail "panel-port: SSH tunnel hint still printed" \
        || pass "panel-port: SSH hint replaced by https"
    grep -q "AMNEZIA_SECURE_COOKIES=1" "$ROOT/.env" && pass "panel-port: AMNEZIA_SECURE_COOKIES=1 in .env" \
        || fail "panel-port: AMNEZIA_SECURE_COOKIES missing from .env"
    if grep -q "certbot" "$FAKE_CALLS"; then fail "panel-port: certbot was invoked"; else pass "panel-port: certbot not invoked"; fi
    if grep -q "dig " "$FAKE_CALLS"; then fail "panel-port: dig was invoked"; else pass "panel-port: dig not invoked"; fi
}

test_panel_port_loopback_matrix() {
    # Loopback mode (no flags): no tcp panel rule in nft, no openssl,
    # no AMNEZIA_SECURE_COOKIES in .env, SSH hint kept.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "panel-port loopback flow: exit $rc"
    if grep -qE "^[[:space:]]*tcp dport" "$ROOT/nftables/amnezia-vpn.nft"; then
        fail "loopback: a tcp panel port was opened in nftables"
    else
        pass "loopback: no tcp panel port in nftables"
    fi
    if grep -q "openssl" "$FAKE_CALLS"; then fail "loopback: openssl was invoked"; else pass "loopback: openssl not invoked"; fi
    if grep -q "AMNEZIA_SECURE_COOKIES" "$ROOT/.env"; then fail "loopback: AMNEZIA_SECURE_COOKIES in .env"; else pass "loopback: no AMNEZIA_SECURE_COOKIES in .env"; fi
    grep -q "ssh -L 8787" "$TMP_TEST/out" && pass "loopback: SSH tunnel hint kept" \
        || fail "loopback: SSH hint missing"
}

test_panel_port_invalid_values() {
    fakes_reset
    os_release debian 12 bookworm
    for bad in 0 65536 abc ""; do
        rc="$(run_install --panel-port "$bad")"
        [ "$rc" = "2" ] || fail "invalid panel port '$bad': exit $rc, want 2"
    done
    pass "invalid --panel-port values rejected (exit 2)"
    rc="$(run_install --domain panel.example.com --panel-port 8443)"
    [ "$rc" = "2" ] || fail "domain+panel-port conflict: exit $rc, want 2"
    pass "--domain + --panel-port conflict rejected (exit 2)"
    rc="$(run_install --panel-tls-regen)"
    [ "$rc" = "2" ] || fail "--panel-tls-regen without --panel-port: exit $rc, want 2"
    pass "--panel-tls-regen without --panel-port rejected (exit 2)"
}

test_panel_port_idempotent() {
    # Second run keeps the certificate (single openssl req overall),
    # the nft rule stays unique, and .env keeps exactly one cookie line.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --panel-port 8443)"
    [ "$rc" = "0" ] || fail "panel-port idempotence: first pass exit $rc"
    cert_before="$(cat "$ROOT/tls/panel.crt")"
    rc="$(run_install --panel-port 8443)"
    [ "$rc" = "0" ] || fail "panel-port idempotence: second pass exit $rc"
    [ "$(grep -c "openssl req" "$FAKE_CALLS")" = "1" ] \
        && pass "panel-port idempotence: certificate generated once" \
        || fail "panel-port idempotence: certificate regenerated (openssl req count != 1)"
    [ "$(cat "$ROOT/tls/panel.crt")" = "$cert_before" ] && pass "panel-port idempotence: cert content kept" \
        || fail "panel-port idempotence: cert content changed"
    [ "$(grep -c "tcp dport 8443" "$ROOT/nftables/amnezia-vpn.nft")" = "1" ] \
        && pass "panel-port idempotence: single nft rule" \
        || fail "panel-port idempotence: nft rule duplicated"
    [ "$(grep -c "AMNEZIA_SECURE_COOKIES" "$ROOT/.env")" = "1" ] \
        && pass "panel-port idempotence: single cookie line in .env" \
        || fail "panel-port idempotence: cookie line duplicated"
}

test_panel_port_regen_flag() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --panel-port 8443)"
    [ "$rc" = "0" ] || fail "panel-port regen: first pass exit $rc"
    rc="$(run_install --panel-port 8443 --panel-tls-regen)"
    [ "$rc" = "0" ] || fail "panel-port regen: forced pass exit $rc"
    [ "$(grep -c "openssl req" "$FAKE_CALLS")" = "2" ] \
        && pass "panel-port regen: --panel-tls-regen forced a fresh certificate" \
        || fail "panel-port regen: certificate not regenerated (openssl req count != 2)"
}

test_panel_port_switch_back_to_loopback() {
    # Mode convergence: a rerun without --panel-port closes the port in
    # nftables, drops AMNEZIA_SECURE_COOKIES from .env and restores the
    # SSH hint; the certificate file itself is left on disk (managed
    # data, not application state).
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --panel-port 8443)"
    [ "$rc" = "0" ] || fail "panel-port switchback: first pass exit $rc"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "panel-port switchback: second pass exit $rc"
    if grep -qE "^[[:space:]]*tcp dport 8443" "$ROOT/nftables/amnezia-vpn.nft"; then
        fail "panel-port switchback: 8443 still open in nftables"
    else
        pass "panel-port switchback: 8443 closed in nftables"
    fi
    if grep -q "AMNEZIA_SECURE_COOKIES" "$ROOT/.env"; then
        fail "panel-port switchback: AMNEZIA_SECURE_COOKIES still in .env"
    else
        pass "panel-port switchback: AMNEZIA_SECURE_COOKIES removed from .env"
    fi
    grep -q "ssh -L 8787" "$TMP_TEST/out" && pass "panel-port switchback: SSH hint restored" \
        || fail "panel-port switchback: SSH hint missing"
    [ -f "$ROOT/tls/panel.crt" ] && pass "panel-port switchback: cert file left on disk" \
        || fail "panel-port switchback: cert file was deleted"
}

# --- T-129: client endpoint domain ---------------------------------------

test_client_domain_defaults_to_domain() {
    # --domain without --client-domain: the panel domain doubles as the
    # client endpoint — the next-steps hint carries <domain>:<port> and
    # the migration note, the IP endpoint line is gone, and the DNS
    # record is pre-flighted only once (shared hostname).
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com)"
    [ "$rc" = "0" ] || fail "client-domain default flow: exit $rc"
    grep -q "endpoint panel.example.com:443" "$TMP_TEST/out" \
        && pass "client-domain: hint carries domain:port endpoint" \
        || fail "client-domain: hint missing domain:port endpoint"
    grep -q "A-record change" "$TMP_TEST/out" && pass "client-domain: migration note printed" \
        || fail "client-domain: migration note missing"
    if grep -q "endpoint <public-ip>" "$TMP_TEST/out"; then
        fail "client-domain: IP endpoint hint still printed"
    else
        pass "client-domain: IP endpoint hint replaced"
    fi
    grep -q "CLIENT_DOMAIN=panel.example.com" "$ROOT/.env" \
        && pass "client-domain: CLIENT_DOMAIN persisted in .env" \
        || fail "client-domain: CLIENT_DOMAIN missing from .env"
    [ "$(grep -c "dig " "$FAKE_CALLS")" = "2" ] \
        && pass "client-domain: shared domain pre-flighted once (A+AAAA)" \
        || fail "client-domain: dig count $(grep -c "dig " "$FAKE_CALLS"), want 2"
}

test_client_domain_standalone() {
    # --client-domain without --domain: validated, DNS pre-flight runs,
    # hint carries vpn.example.com:PORT, no certbot (no panel domain),
    # and the panel keeps its loopback SSH hint.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --client-domain vpn.example.com)"
    [ "$rc" = "0" ] || fail "client-domain standalone flow: exit $rc"
    grep -q "endpoint vpn.example.com:443" "$TMP_TEST/out" \
        && pass "client-domain standalone: hint carries vpn.example.com:443" \
        || fail "client-domain standalone: hint missing endpoint"
    grep -q "A-record change" "$TMP_TEST/out" && pass "client-domain standalone: migration note printed" \
        || fail "client-domain standalone: migration note missing"
    grep -q "dig +short" "$FAKE_CALLS" && pass "client-domain standalone: DNS pre-flight ran" \
        || fail "client-domain standalone: DNS pre-flight missing"
    if grep -q "certbot" "$FAKE_CALLS"; then fail "client-domain standalone: certbot was invoked (no panel domain)"; else pass "client-domain standalone: certbot not invoked"; fi
    grep -q "CLIENT_DOMAIN=vpn.example.com" "$ROOT/.env" \
        && pass "client-domain standalone: CLIENT_DOMAIN persisted in .env" \
        || fail "client-domain standalone: CLIENT_DOMAIN missing from .env"
    grep -q "ssh -L 8787" "$TMP_TEST/out" && pass "client-domain standalone: panel loopback hint kept" \
        || fail "client-domain standalone: SSH hint missing"
}

test_client_domain_overrides_domain() {
    # --client-domain wins over the --domain default: the hint carries
    # vpn.example.com while the panel stays on panel.example.com; both
    # records are pre-flighted (2 dig calls each).
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com --client-domain vpn.example.com)"
    [ "$rc" = "0" ] || fail "client-domain override flow: exit $rc"
    grep -q "endpoint vpn.example.com:443" "$TMP_TEST/out" \
        && pass "client-domain override: hint carries vpn.example.com:443" \
        || fail "client-domain override: hint missing the client domain"
    grep -q "endpoint panel.example.com" "$TMP_TEST/out" && fail "client-domain override: panel domain leaked into the endpoint" \
        || pass "client-domain override: panel domain not used as endpoint"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "client-domain override: panel https hint kept" \
        || fail "client-domain override: panel https hint missing"
    [ "$(grep -c "dig " "$FAKE_CALLS")" = "4" ] \
        && pass "client-domain override: both domains pre-flighted" \
        || fail "client-domain override: dig count $(grep -c "dig " "$FAKE_CALLS"), want 4"
}

test_client_domain_preflight_before_certbot() {
    # A mismatched --client-domain (different from --domain) fails the
    # install BEFORE any certbot attempt — the Let's Encrypt rate limit
    # stays untouched.
    fakes_reset
    setstate DNS_A 1.2.3.4 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com --client-domain vpn.example.com)"
    [ "$rc" = "1" ] || fail "client-domain pre-flight order: exit $rc, want 1"
    grep -q "Create an A record" "$TMP_TEST/err" && pass "client-domain pre-flight: actionable message" \
        || fail "client-domain pre-flight: message missing"
    grep -q "certbot" "$FAKE_CALLS" && fail "client-domain pre-flight: certbot was invoked" \
        || pass "client-domain pre-flight: certbot not invoked (rate limit safe)"
}

test_client_domain_dns_mismatch_standalone() {
    fakes_reset
    setstate DNS_A 1.2.3.4 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install --client-domain vpn.example.com)"
    [ "$rc" = "1" ] || fail "client-domain DNS mismatch: exit $rc, want 1"
    grep -q "Create an A record" "$TMP_TEST/err" && pass "client-domain DNS mismatch: actionable message" \
        || fail "client-domain DNS mismatch: message missing"
}

test_client_domain_env_reuse() {
    # CLIENT_DOMAIN persists in the deployment .env (env_read pattern
    # like AWG_PORT) and is reused on a rerun without any domain flag:
    # the hint keeps the domain endpoint and the pre-flight reruns.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --client-domain vpn.example.com)"
    [ "$rc" = "0" ] || fail "client-domain reuse: first pass exit $rc"
    dig_after_first="$(grep -c "dig " "$FAKE_CALLS")"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "client-domain reuse: second pass exit $rc"
    grep -q "endpoint vpn.example.com:443" "$TMP_TEST/out" \
        && pass "client-domain reuse: endpoint from .env in the rerun hint" \
        || fail "client-domain reuse: .env CLIENT_DOMAIN not reused"
    [ "$(grep -c "dig " "$FAKE_CALLS")" -gt "$dig_after_first" ] \
        && pass "client-domain reuse: pre-flight reran on the second pass" \
        || fail "client-domain reuse: pre-flight missing on rerun"
}

test_client_domain_invalid_fqdn() {
    fakes_reset
    os_release debian 12 bookworm
    for bad in "bad domain" "..x" "x..y" "-x.com" "x-.com" "x." "UPPER.com"; do
        rc="$(run_install --client-domain "$bad")"
        [ "$rc" = "2" ] || fail "invalid client-domain '$bad': exit $rc, want 2 (usage)"
    done
    pass "invalid --client-domain values rejected (exit 2)"
}

test_no_domain_ip_endpoint_hint() {
    # No domain anywhere: the next-steps hint keeps the public-IP
    # endpoint exactly as before T-129, without the migration note.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "no-domain IP endpoint flow: exit $rc"
    grep -q "endpoint <public-ip>:443" "$TMP_TEST/out" \
        && pass "no-domain: IP endpoint hint kept" \
        || fail "no-domain: IP endpoint hint missing"
    grep -q "A-record change" "$TMP_TEST/out" && fail "no-domain: migration note printed without a domain" \
        || pass "no-domain: no migration note"
    if grep -q "dig " "$FAKE_CALLS"; then fail "no-domain: dig was invoked"; else pass "no-domain: no DNS pre-flight"; fi
    grep -q '^CLIENT_DOMAIN=' "$ROOT/.env" && pass "no-domain: empty CLIENT_DOMAIN= line in .env" \
        || fail "no-domain: CLIENT_DOMAIN line missing from .env"
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
test_domain_default_no_domain
test_domain_mode_flow
test_domain_dns_mismatch
test_domain_dns_missing
test_domain_certbot_failure
test_domain_invalid_fqdn
test_panel_port_mode_flow
test_panel_port_loopback_matrix
test_panel_port_invalid_values
test_panel_port_idempotent
test_panel_port_regen_flag
test_panel_port_switch_back_to_loopback
test_client_domain_defaults_to_domain
test_client_domain_standalone
test_client_domain_overrides_domain
test_client_domain_preflight_before_certbot
test_client_domain_dns_mismatch_standalone
test_client_domain_env_reuse
test_client_domain_invalid_fqdn
test_no_domain_ip_endpoint_hint
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
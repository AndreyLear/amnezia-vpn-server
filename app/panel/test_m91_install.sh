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
    unset AMNEZIA_INSTALL_SKIP_PRUNE
    : > "$FAKE_CALLS"
    mkdir -p "$FAKE_FS"
    rm -rf "$ROOT" "$SYSCTL_TEST" "$MODULES_TEST" "$KEYRING_TEST" "$SOURCES_TEST" \
        "$NFTABLES_DIR_TEST" "$SYSTEMD_DIR_TEST" "$JOURNALD_TEST"
    rm -f "$NFTABLES_CONF_TEST"
    cat > "$FAKE_STATE" <<EOF
COMPOSE_VERSION=${1:-2.30.1}
NEW_COMPOSE_VERSION=
DAEMON=ok
IP_FORWARD=1
TCP_CC="bbr cubic"
NFT_CHECK_RC=0
NFT_APPLY_RC=0
NFT_APPLIED=0
MODPROBE_OK=yes
MODPROBE_BBR_OK=yes
DU_IN_ACCEPT=0
DU_OUT_ACCEPT=0
UP_RC=0
COMPOSE_PULL_RC=0
BUILDER_PRUNE_RC=0
SYSCTL_IP_FORWARD_W_RC=0
SYSCTL_CONNTRACK_W_RC=0
SYSCTL_BBR_W_RC=0
SYSCTL_TUNING_W_RC=0
JOURNALD_RESTART_RC=0
PUBLIC_IP=2.26.93.192
DNS_A=2.26.93.192
DNS_AAAA=
CERTBOT_RC=0
APT_LOCK_FAILS=0
APT_LOCK_FILE=/var/lib/dpkg/lock-frontend
APT_LOCK_ALWAYS=0
APT_FAIL_MATCH=
ADD_APT_REPO_RC=0
APT_FUSER_BUSY_REMAINING=0
FAKE_PMTU=1500
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
            version | config | build | pull | up | ps | logs) verb="$1"; break ;;
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
        pull) exit "${COMPOSE_PULL_RC:-0}" ;;
        up) exit "${UP_RC:-0}" ;;
    esac
    exit 0
fi
# Build-cache prune after compose build (docker-prune.sh). builder
# prune can be forced to fail via BUILDER_PRUNE_RC; image/rmi stay
# successful so a prune-script failure is independent of rmi.
if [ "${1:-}" = "builder" ]; then
    exit "${BUILDER_PRUNE_RC:-0}"
fi
if [ "${1:-}" = "image" ] || [ "${1:-}" = "rmi" ]; then
    exit 0
fi
if [ "${1:-}" = "images" ]; then
    if [ "${2:-}" = "-q" ] && [ "${3:-}" = "golang" ]; then
        echo "aaa111golang"
        exit 0
    fi
    if printf '%s' "$*" | grep -q -- '--format'; then
        # Real `docker images --format '{{.Repository}}:{{.Tag}}'` is
        # one repo:tag per line, never "tag id" on the same line.
        echo "golang:1.25.12"
        echo "golang:1.25.12-alpine"
        echo "amnezia-vpn-server/panel:latest"
        echo "amnezia-vpn-server/awg:latest"
        echo "alpine:3.22.5"
        exit 0
    fi
    exit 0
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/apt-get" <<'FAKE_EOF'
#!/bin/bash
echo "apt-get $*" >> "${FAKE_CALLS:?}"
# Record the frontend settings: an apt that can still open a whiptail or
# debconf dialog will hang an install whose output nobody can see.
echo "apt-get-env DEBIAN_FRONTEND=${DEBIAN_FRONTEND:-unset} NEEDRESTART_MODE=${NEEDRESTART_MODE:-unset}" >> "${FAKE_CALLS:?}"
printf '%s\n' "Get:1 https://example.invalid stable/main amd64 Packages"
. "${FAKE_STATE:?}"
lock_msg() {
    # APT_LOCK_FILE lets a test reproduce the wording apt-get update emits:
    # it contends on the lists lock, not the dpkg frontend lock.
    printf '%s\n' "E: Could not get lock ${APT_LOCK_FILE:-/var/lib/dpkg/lock-frontend}. It is held by process 10849 (unattended-upgr)" >&2
}
# APT_FAIL_MATCH makes the matching invocation fail the way apt does when
# a package is missing: not a lock error, so it must not be retried.
if [ -n "${APT_FAIL_MATCH:-}" ] && printf '%s' "$*" | grep -q -- "${APT_FAIL_MATCH}"; then
    printf '%s\n' "E: Unable to locate package ${APT_FAIL_MATCH}" >&2
    exit 100
fi
if [ "${APT_LOCK_ALWAYS:-0}" = "1" ]; then
    lock_msg
    exit 100
fi
if [ "${APT_LOCK_FAILS:-0}" -gt 0 ] 2>/dev/null; then
    n=$((APT_LOCK_FAILS - 1))
    sed "s|^APT_LOCK_FAILS=.*|APT_LOCK_FAILS=${n}|" "$FAKE_STATE" \
        > "$FAKE_STATE.new" && mv "$FAKE_STATE.new" "$FAKE_STATE"
    lock_msg
    exit 100
fi
if [ "${1:-}" = "install" ]; then
    touch "$FAKE_FS/apt-installed"
    if [ -n "$NEW_COMPOSE_VERSION" ]; then
        sed "s|^COMPOSE_VERSION=.*|COMPOSE_VERSION=${NEW_COMPOSE_VERSION}|" "$FAKE_STATE" \
            > "$FAKE_STATE.new" && mv "$FAKE_STATE.new" "$FAKE_STATE"
    fi
    case " $* " in
        *" nginx "*)
            cat > "${FAKE_DIR}/nginx" <<'NGINX'
#!/bin/bash
echo "nginx $*" >> "${FAKE_CALLS:?}"
exit 0
NGINX
            chmod +x "${FAKE_DIR}/nginx"
            ;;
    esac
fi
exit 0
FAKE_EOF

# Shadows host fuser (macOS fuser is not the Debian dpkg helper).
cat > "$FAKE_DIR/fuser" <<'FAKE_EOF'
#!/bin/bash
echo "fuser $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
n="${APT_FUSER_BUSY_REMAINING:-0}"
if [ "$n" -gt 0 ] 2>/dev/null; then
    sed "s|^APT_FUSER_BUSY_REMAINING=.*|APT_FUSER_BUSY_REMAINING=$((n - 1))|" "$FAKE_STATE" \
        > "$FAKE_STATE.new" && mv "$FAKE_STATE.new" "$FAKE_STATE"
    # Real fuser prints the PIDs on stdout and only the "FILE:" header on
    # stderr; wait_for_dpkg_lock reads stdout, so a fake that inverts them
    # hides the "held by process N" path from every test.
    printf '%s:\n' "$1" >&2
    printf '%s\n' "10849"
    exit 0
fi
exit 1
FAKE_EOF

cat > "$FAKE_DIR/ping" <<'FAKE_EOF'
#!/bin/bash
# Fake ping for the tunnel-MTU pre-flight: answers unfragmented probes up
# to FAKE_PMTU (as a full IP packet), refuses anything larger, and can
# refuse everything (FAKE_PMTU=0) to emulate a transit that filters ICMP.
echo "ping $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
limit="${FAKE_PMTU:-1500}"
[ "$limit" = "0" ] && exit 1
size=0
while [ $# -gt 0 ]; do
    case "$1" in
        -s) size="$2"; shift 2 ;;
        *) shift ;;
    esac
done
[ $((size + 28)) -le "$limit" ] && exit 0
exit 1
FAKE_EOF

cat > "$FAKE_DIR/systemctl" <<'FAKE_EOF'
#!/bin/bash
echo "systemctl $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
case "$*" in
    "restart systemd-journald")
        exit "${JOURNALD_RESTART_RC:-0}"
        ;;
esac
exit 0
FAKE_EOF

cat > "$FAKE_DIR/sysctl" <<'FAKE_EOF'
#!/bin/bash
echo "sysctl $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
case "$*" in
    *"-n net.ipv4.ip_forward"*) echo "$IP_FORWARD" ;;
    *"-n net.ipv4.tcp_available_congestion_control"*) echo "${TCP_CC:-bbr cubic}" ;;
    *"-w net.ipv4.ip_forward=1"*)
        [ "${SYSCTL_IP_FORWARD_W_RC:-0}" = "0" ] || exit "${SYSCTL_IP_FORWARD_W_RC}"
        sed 's|^IP_FORWARD=.*|IP_FORWARD=1|' "$FAKE_STATE" > "$FAKE_STATE.new" \
            && mv "$FAKE_STATE.new" "$FAKE_STATE"
        echo "net.ipv4.ip_forward = 1"
        ;;
    *"-w net.netfilter.nf_conntrack_max="*)
        exit "${SYSCTL_CONNTRACK_W_RC:-0}"
        ;;
    *"-w net.core.default_qdisc=fq"* | *"-w net.ipv4.tcp_congestion_control=bbr"*)
        exit "${SYSCTL_BBR_W_RC:-0}"
        ;;
    *"-w net.core.rmem_max="* | *"-w net.core.wmem_max="* \
    | *"-w net.core.netdev_max_backlog="* | *"-w net.ipv4.ip_local_port_range="* \
    | *"-w net.ipv4.tcp_slow_start_after_idle="* \
    | *"-w net.netfilter.nf_conntrack_tcp_timeout_established="*)
        exit "${SYSCTL_TUNING_W_RC:-0}"
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

chmod +x "$FAKE_DIR/docker" "$FAKE_DIR/apt-get" "$FAKE_DIR/fuser" "$FAKE_DIR/systemctl" "$FAKE_DIR/sysctl" "$FAKE_DIR/nft" "$FAKE_DIR/curl" "$FAKE_DIR/ping"

# M9.2c fakes: the installer probes the AmneziaWG client stack and
# manages the docker/ufw forward-accept coexistence rules.
cat > "$FAKE_DIR/modprobe" <<'FAKE_EOF'
#!/bin/bash
echo "modprobe $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
if [ "$1" = "tcp_bbr" ]; then
    # A kernel that ships tcp_bbr as a module only advertises bbr in
    # tcp_available_congestion_control once it is loaded.
    [ "${MODPROBE_BBR_OK:-yes}" = "yes" ] || exit 1
    case "${TCP_CC:-}" in
        *bbr*) ;;
        *) sed "s|^TCP_CC=.*|TCP_CC=\"${TCP_CC} bbr\"|" "$FAKE_STATE" \
               > "$FAKE_STATE.new" && mv "$FAKE_STATE.new" "$FAKE_STATE" ;;
    esac
    exit 0
fi
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
. "${FAKE_STATE:?}"
exit "${ADD_APT_REPO_RC:-0}"
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
MODULES_TEST="$TMP_TEST/modules-load.d"
JOURNALD_TEST="$TMP_TEST/journald.conf.d"
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
    AMNEZIA_INSTALL_MODULES_DIR="$MODULES_TEST" \
    AMNEZIA_INSTALL_JOURNALD_DIR="$JOURNALD_TEST" \
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

test_apt_retries_after_dpkg_lock() {
    # First apt-get install nginx fails with lock-frontend (unattended-upgr);
    # after a short wait the next call succeeds. Install must exit 0, not
    # "apt-get install nginx failed".
    fakes_reset
    setstate APT_LOCK_FAILS 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rm -f "$FAKE_DIR/nginx"
    rc="$(AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=8 AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=0 run_install --domain panel.example.com)"
    [ "$rc" = "0" ] || fail "dpkg lock retry: exit $rc, want 0"
    if grep -q "apt-get install nginx failed" "$TMP_TEST/err"; then
        fail "dpkg lock retry: died with nginx apt-get failure"
    else
        pass "dpkg lock retry: did not abort as nginx apt-get failure"
    fi
    grep -q "apt-get install -y nginx" "$FAKE_CALLS" && pass "dpkg lock retry: apt-get install nginx ran" \
        || fail "dpkg lock retry: apt-get install nginx was not invoked"
    cat > "$FAKE_DIR/nginx" <<'FAKE_EOF'
#!/bin/bash
echo "nginx $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF
    chmod +x "$FAKE_DIR/nginx"
}

test_apt_dpkg_lock_timeout() {
    fakes_reset "2.19.6"
    setstate APT_LOCK_ALWAYS 1 "$FAKE_STATE"
    os_release ubuntu 22.04 jammy
    rc="$(AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=0 AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=0 run_install)"
    [ "$rc" = "1" ] || fail "dpkg lock timeout: exit $rc, want 1"
    grep -q "install: ERROR:.*lock-frontend" "$TMP_TEST/err" \
        && pass "dpkg lock timeout: names lock-frontend" \
        || fail "dpkg lock timeout: lock-frontend missing from install ERROR"
    grep -qi "rerun" "$TMP_TEST/err" && pass "dpkg lock timeout: rerun is safe" \
        || fail "dpkg lock timeout: rerun-safe message missing"
    grep -q "unattended-upgr" "$TMP_TEST/err" && pass "dpkg lock timeout: names locking process" \
        || fail "dpkg lock timeout: unattended-upgr missing from error"
    if grep -qiE 'rm[[:space:]]|delete.*lock|remove.*lock' "$TMP_TEST/err"; then
        fail "dpkg lock timeout: told operator to delete the lock"
    else
        pass "dpkg lock timeout: does not tell operator to delete the lock"
    fi
    if grep -q "still missing or < 2.24.2" "$TMP_TEST/err"; then
        fail "dpkg lock timeout: continued past apt instead of dying on the lock"
    else
        pass "dpkg lock timeout: stopped on the dpkg lock"
    fi
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

test_help_examples() {
    AMNEZIA_INSTALL_TEST=1 bash "$INSTALL_SH" --help \
        > "$TMP_TEST/help.out" 2> "$TMP_TEST/help.err"
    rc=$?
    [ "$rc" = "0" ] || fail "install --help: exit $rc"
    grep -q "Examples:" "$TMP_TEST/help.out" && pass "install --help: Examples section" \
        || fail "install --help: Examples section missing"
    grep -q -- "--panel-domain" "$TMP_TEST/help.out" && pass "install --help: --panel-domain" \
        || fail "install --help: --panel-domain missing"
    grep -q -- "--vpn-domain" "$TMP_TEST/help.out" && pass "install --help: --vpn-domain" \
        || fail "install --help: --vpn-domain missing"
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
    sysf="$SYSCTL_TEST/99-amnezia-vpn.conf"
    if [ -f "$sysf" ] && grep -q "net.ipv4.ip_forward = 1" "$sysf" \
        && grep -q "net.netfilter.nf_conntrack_max = 262144" "$sysf"; then
        pass "sysctl drop-in written when ip_forward already 1 (ip_forward + conntrack)"
    else
        fail "sysctl drop-in missing ip_forward/conntrack when already enabled"
    fi
}

# A VPN gateway runs on kernel defaults sized for a desktop unless the
# installer says otherwise: 208 KiB socket ceilings, ~28k NAT ports and a
# five-day conntrack timeout on a 2 GiB host.
# Compiling on the user's VPS means downloading an 800 MB Go toolchain and
# building amneziawg-go, amneziawg-tools and the panel on (usually) one
# vCPU, to produce ~60 MB that CI already built. Pulling is the default.
# needrestart ships enabled on Ubuntu 22.04/24.04 and opens a whiptail
# dialog during apt-get install. With apt's output captured into a variable
# the operator sees a blank terminal while apt blocks on stdin — an install
# that looks hung. Every apt call must be non-interactive.
# apt-get update contends on the lists lock, not the dpkg frontend lock,
# and reports a different path. A retry that only matches the frontend
# wording gives up in exactly the unattended-upgrades scenario it exists
# for, then the install continues with stale package lists.
# Every other operator-supplied value is validated before use; these two
# were not, and "10m" made bash treat the value as a variable name under
# set -u: the installer died with "10m: unbound variable" and exit 127
# mid-run, with no install: ERROR line to explain it.
# The fuser-based wait loop had no coverage at all: no test ever set
# APT_FUSER_BUSY_REMAINING above zero, so the "waiting for" log, the sleep
# and the deadline branch were never entered.
test_waits_while_dpkg_lock_is_held() {
    fakes_reset
    setstate APT_FUSER_BUSY_REMAINING 2 "$FAKE_STATE"
    os_release debian 12 bookworm
    rm -f "$FAKE_DIR/nginx"   # forces an apt install, i.e. a lock wait
    rc="$(AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=30 AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=0 run_install --domain panel.example.com)"
    [ "$rc" = "0" ] || fail "held-lock wait: exit $rc, want 0"
    stdout | grep -q "waiting for /var/lib/dpkg/lock-frontend" \
        && pass "the installer says it is waiting for the lock" \
        || fail "waiting for the lock must be visible"
    cat > "$FAKE_DIR/nginx" <<'FAKE_EOF'
#!/bin/bash
echo "nginx $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF
    chmod +x "$FAKE_DIR/nginx"
}

# A lock that never frees must end with the deadline message naming the
# holder, not with a later unrelated failure.
test_dpkg_lock_deadline_reports_the_holder() {
    fakes_reset
    setstate APT_FUSER_BUSY_REMAINING 9999 "$FAKE_STATE"
    os_release debian 12 bookworm
    rm -f "$FAKE_DIR/nginx"
    rc="$(AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=0 AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=0 run_install --domain panel.example.com)"
    [ "$rc" != "0" ] || fail "a lock held past the deadline must fail the install"
    stderr | grep -q "timed out waiting for /var/lib/dpkg/lock-frontend" \
        && pass "the deadline message names the lock" \
        || fail "the deadline message must name the lock"
    stderr | grep -q "held by process 10849" \
        && pass "the deadline message names the holder" \
        || fail "the deadline message must name the holding process"
    cat > "$FAKE_DIR/nginx" <<'FAKE_EOF'
#!/bin/bash
echo "nginx $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF
    chmod +x "$FAKE_DIR/nginx"
}

test_lock_timeout_must_be_numeric() {
    # An empty value is not a mistake: it means "unset", and the code
    # falls back to its default. Only a present, unusable value is an error.
    for bad in 10m abc -5; do
        fakes_reset
        os_release ubuntu 24.04 noble
        rc="$(AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC="$bad" run_install)"
        if [ "$rc" = "2" ]; then
            pass "AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=$bad rejected as usage error"
        else
            fail "AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=$bad: exit $rc, want 2"
        fi
        stderr | grep -q "AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC" \
            && pass "the message names the variable ($bad)" \
            || fail "the message must name the variable ($bad)"
    done
}

test_lock_poll_must_be_numeric() {
    fakes_reset
    os_release ubuntu 24.04 noble
    rc="$(AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=soon run_install)"
    [ "$rc" = "2" ] \
        && pass "AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=soon rejected as usage error" \
        || fail "AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=soon: exit $rc, want 2"
}

test_apt_retries_on_lists_lock() {
    fakes_reset
    setstate APT_LOCK_FAILS 1 "$FAKE_STATE"
    setstate APT_LOCK_FILE /var/lib/apt/lists/lock "$FAKE_STATE"
    os_release debian 12 bookworm
    rm -f "$FAKE_DIR/nginx"   # forces the nginx install, i.e. a real apt call
    rc="$(AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC=8 AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC=0 run_install --domain panel.example.com)"
    [ "$rc" = "0" ] || fail "lists-lock retry: exit $rc, want 0"
    stdout | grep -q "busy; retrying" \
        && pass "a lists-lock message is retried like a frontend lock" \
        || fail "lists lock must be retried too"
    cat > "$FAKE_DIR/nginx" <<'FAKE_EOF'
#!/bin/bash
echo "nginx $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF
    chmod +x "$FAKE_DIR/nginx"
}

# A failed package install has to stop the install where it happened. It
# used to be swallowed, and the run died later on an unrelated message.
test_failed_apt_install_stops_with_its_own_message() {
    fakes_reset "2.19.6"
    setstate NEW_COMPOSE_VERSION 2.30.0 "$FAKE_STATE"
    setstate APT_FAIL_MATCH docker-ce "$FAKE_STATE"
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" != "0" ] || fail "a failed docker-ce install must not exit 0"
    stderr | grep -qi "docker" \
        && pass "the failure names the step that failed" \
        || fail "the failure must name docker, not a later symptom"
    stderr | grep -qi "still missing or < 2.24.2" \
        && fail "the failure must not surface as the later compose symptom" \
        || pass "no misleading downstream message"
}

# add-apt-repository takes the apt locks and runs its own update; an
# unchecked failure here surfaced as "Unable to locate package amneziawg"
# and finally as a modprobe error pointing at the wrong thing.
test_failed_ppa_stops_with_its_own_message() {
    fakes_reset
    setstate ADD_APT_REPO_RC 1 "$FAKE_STATE"
    os_release ubuntu 24.04 noble
    rc="$(AMNEZIA_INSTALL_FORCE_AWG_INSTALL=1 run_install)"
    [ "$rc" != "0" ] || fail "a failed add-apt-repository must not exit 0"
    stderr | grep -qi "ppa\|repository" \
        && pass "the failure names the repository step" \
        || fail "the failure must name the repository step"
}

test_apt_is_never_interactive() {
    fakes_reset "2.19.6"   # too old: forces the docker-ce install path
    setstate NEW_COMPOSE_VERSION 2.30.0 "$FAKE_STATE"
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "apt frontend flow: exit $rc"
    if grep -q "apt-get-env DEBIAN_FRONTEND=unset" "$FAKE_CALLS"; then
        fail "an apt call ran without DEBIAN_FRONTEND=noninteractive"
    else
        pass "every apt call is non-interactive"
    fi
    grep -q "apt-get-env .*NEEDRESTART_MODE=a" "$FAKE_CALLS" \
        && pass "needrestart is answered automatically" \
        || fail "needrestart must not be able to prompt"
}

# apt's output must not be captured into a variable. The installer prints
# progress now (amnezia-vpn-server-62pg), and a multi-minute docker-ce or
# nginx install that prints nothing until it finishes reads as a hang —
# which is exactly what a swallowed stream produced before.
test_apt_output_is_not_swallowed() {
    if grep -qE 'out="\$\(cmd apt-get' "$INSTALL_SH"; then
        fail "apt output must not be captured into a variable"
    else
        pass "apt output is not captured into a variable"
    fi
    grep -q 'cmd apt-get "\$@" 2>&1 | tee' "$INSTALL_SH" \
        && pass "apt output is streamed while still being kept for analysis" \
        || fail "apt output must be streamed and kept (tee)"
}

test_images_are_pulled_by_default() {
    fakes_reset
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "pull flow: exit $rc"
    grep -q "compose .*pull" "$FAKE_CALLS" \
        && pass "published images are pulled" \
        || fail "install.sh must pull the published images"
    grep -q "compose .*build" "$FAKE_CALLS" \
        && fail "default install must not compile the images" \
        || pass "default install does not compile anything"
    # Nothing was built, so there is no toolchain or build cache to prune.
    grep -q "pruning Docker build cache" "$TMP_TEST/out" \
        && fail "prune has nothing to do after a pull" \
        || pass "prune skipped when nothing was built"
}

test_build_flag_compiles_locally() {
    fakes_reset
    os_release ubuntu 24.04 noble
    rc="$(run_install --build)"
    [ "$rc" = "0" ] || fail "--build flow: exit $rc"
    grep -q "compose .*build" "$FAKE_CALLS" \
        && pass "--build compiles the images here" \
        || fail "--build must compile the images"
    grep -q "compose .*pull" "$FAKE_CALLS" \
        && fail "--build must not pull" \
        || pass "--build does not pull"
    grep -q "pruning Docker build cache" "$TMP_TEST/out" \
        && pass "--build prunes the toolchain afterwards" \
        || fail "--build must prune the toolchain"
}

# A registry this host cannot reach must not end the install: fall back to
# the old path and say so.
test_unreachable_registry_falls_back_to_build() {
    fakes_reset
    os_release ubuntu 24.04 noble
    state_set COMPOSE_PULL_RC 1
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "registry fallback flow: exit $rc"
    stdout | grep -q "could not pull the prebuilt images" \
        && pass "failed pull is explained" \
        || fail "failed pull must be explained"
    grep -q "compose .*build" "$FAKE_CALLS" \
        && pass "failed pull falls back to building" \
        || fail "failed pull must fall back to building"
}

test_sysctl_host_tuning() {
    fakes_reset
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "host tuning flow: exit $rc"
    sysf="$SYSCTL_TEST/99-amnezia-vpn.conf"
    [ -f "$sysf" ] || { fail "sysctl drop-in missing"; return 0; }
    for kv in \
        "net.core.rmem_max = 16777216" \
        "net.core.wmem_max = 16777216" \
        "net.core.netdev_max_backlog = 16384" \
        "net.ipv4.ip_local_port_range = 10240 65535" \
        "net.ipv4.tcp_slow_start_after_idle = 0" \
        "net.netfilter.nf_conntrack_tcp_timeout_established = 86400"; do
        grep -q "^${kv}$" "$sysf" \
            && pass "tuning persisted: $kv" \
            || fail "tuning missing from drop-in: $kv"
    done
    # ...and applied to the running kernel, not just written to a file.
    grep -q "sysctl -w net.core.rmem_max=16777216" "$FAKE_CALLS" \
        && pass "tuning applied to the running kernel" \
        || fail "tuning must be applied with sysctl -w as well"
}

# A kernel that refuses one of the knobs must not fail the install.
test_sysctl_host_tuning_survives_refusal() {
    fakes_reset
    sed 's|^SYSCTL_TUNING_W_RC=.*|SYSCTL_TUNING_W_RC=1|' "$FAKE_STATE" > "$FAKE_STATE.new" \
        && mv "$FAKE_STATE.new" "$FAKE_STATE"
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "refused tuning must not abort the install: exit $rc"
    stdout | grep -q "WARNING: sysctl -w net.core.rmem_max" \
        && pass "refused tuning warns instead of aborting" \
        || fail "refused tuning must warn"
}

test_sysctl_bbr_when_advertised() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "bbr advertised flow: exit $rc"
    sysf="$SYSCTL_TEST/99-amnezia-vpn.conf"
    grep -q "net.core.default_qdisc = fq" "$sysf" \
        && grep -q "net.ipv4.tcp_congestion_control = bbr" "$sysf" \
        && pass "BBR sysctls present when fake advertises bbr" \
        || fail "BBR sysctls missing although tcp_available_congestion_control has bbr"
}

# On a clean Ubuntu 24.04 tcp_bbr ships as a module and is not loaded, so
# tcp_available_congestion_control reads "reno cubic" and a check made
# before modprobe concludes the kernel has no BBR — which is how the
# optimisation ended up never being applied on a real install.
test_sysctl_bbr_loaded_from_module() {
    fakes_reset
    sed 's|^TCP_CC=.*|TCP_CC="cubic reno"|' "$FAKE_STATE" > "$FAKE_STATE.new" \
        && mv "$FAKE_STATE.new" "$FAKE_STATE"
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "bbr module flow: exit $rc"
    grep -q "modprobe tcp_bbr" "$FAKE_CALLS" \
        && pass "tcp_bbr is loaded before deciding BBR is unavailable" \
        || fail "install.sh must try modprobe tcp_bbr"
    sysf="$SYSCTL_TEST/99-amnezia-vpn.conf"
    grep -q "net.ipv4.tcp_congestion_control = bbr" "$sysf" \
        && grep -q "net.core.default_qdisc = fq" "$sysf" \
        && pass "BBR+fq applied once the module is loaded" \
        || fail "BBR+fq missing although the module loaded"
    grep -rq "tcp_bbr" "$MODULES_TEST" 2>/dev/null \
        && pass "tcp_bbr persisted for the next boot" \
        || fail "tcp_bbr must be persisted in modules-load.d"
}

test_sysctl_bbr_skipped_when_absent() {
    fakes_reset
    sed 's|^TCP_CC=.*|TCP_CC="cubic reno"|' "$FAKE_STATE" > "$FAKE_STATE.new" \
        && mv "$FAKE_STATE.new" "$FAKE_STATE"
    # ...and the module cannot be loaded either: this kernel has no BBR.
    sed 's|^MODPROBE_BBR_OK=.*|MODPROBE_BBR_OK=no|' "$FAKE_STATE" > "$FAKE_STATE.new" \
        && mv "$FAKE_STATE.new" "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "bbr absent flow: exit $rc"
    sysf="$SYSCTL_TEST/99-amnezia-vpn.conf"
    [ -f "$sysf" ] || { fail "sysctl drop-in missing when bbr absent"; return 0; }
    if grep -q "tcp_congestion_control" "$sysf" || grep -q "default_qdisc" "$sysf"; then
        fail "BBR sysctls written although fake does not advertise bbr"
    else
        pass "BBR sysctls omitted when fake does not advertise bbr"
    fi
    grep -q "net.ipv4.ip_forward = 1" "$sysf" \
        && grep -q "net.netfilter.nf_conntrack_max = 262144" "$sysf" \
        && pass "ip_forward+conntrack still written without BBR" \
        || fail "ip_forward/conntrack missing when BBR skipped"
}

test_journald_cap() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "journald cap flow: exit $rc"
    jf="$JOURNALD_TEST/99-amnezia-vpn.conf"
    if [ -f "$jf" ] && grep -q "\[Journal\]" "$jf" && grep -q "SystemMaxUse=200M" "$jf"; then
        pass "journald drop-in SystemMaxUse=200M in injected dir"
    else
        fail "journald drop-in SystemMaxUse=200M missing"
    fi
}

test_weekly_prune_timer() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "prune timer flow: exit $rc"
    svc="$SYSTEMD_DIR_TEST/amnezia-vpn-prune.service"
    timer="$SYSTEMD_DIR_TEST/amnezia-vpn-prune.timer"
    [ -f "$svc" ] && [ -f "$timer" ] \
        && pass "prune timer+service installed" \
        || fail "prune timer+service missing"
    grep -Fq "ExecStart=${ROOT}/docker-prune.sh" "$svc" \
        && pass "prune service ExecStart uses \$ROOT" \
        || fail "prune service ExecStart does not use $ROOT/docker-prune.sh"
    grep -q "OnCalendar=weekly" "$timer" && grep -q "Persistent=true" "$timer" \
        && pass "prune timer OnCalendar=weekly Persistent=true" \
        || fail "prune timer calendar/persistent missing"
    grep -q "systemctl enable --now amnezia-vpn-prune.timer" "$FAKE_CALLS" \
        && pass "systemctl enable --now prune timer in FAKE_CALLS" \
        || fail "systemctl enable --now amnezia-vpn-prune.timer missing from FAKE_CALLS"
    up_n="$(grep -nE 'docker compose .*[[:space:]]up([[:space:]]|$)' "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    enable_n="$(grep -n 'systemctl enable --now amnezia-vpn-prune.timer' "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    if [ -n "$up_n" ] && [ -n "$enable_n" ] && [ "$enable_n" -gt "$up_n" ]; then
        pass "prune timer enable --now is after compose up"
    else
        fail "prune timer enable --now must be after compose up (up=$up_n enable=$enable_n)"
    fi
}

test_sysctl_ip_forward_apply_aborts() {
    fakes_reset
    setstate SYSCTL_IP_FORWARD_W_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" != "0" ] || fail "ip_forward -w fail: exit $rc, want non-zero"
    [ "$rc" != "0" ] && pass "ip_forward -w fail: install aborted (exit $rc)"
}

test_sysctl_conntrack_apply_warns() {
    fakes_reset
    setstate SYSCTL_CONNTRACK_W_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "conntrack -w fail: exit $rc, want 0"
    grep -q "WARNING:" "$TMP_TEST/out" && grep -qi "conntrack" "$TMP_TEST/out" \
        && pass "conntrack -w fail: WARNING logged, install continued" \
        || fail "conntrack -w fail: WARNING missing from stdout"
}

test_sysctl_bbr_apply_warns() {
    fakes_reset
    setstate SYSCTL_BBR_W_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "BBR -w fail: exit $rc, want 0"
    grep -q "WARNING:" "$TMP_TEST/out" \
        && pass "BBR -w fail: WARNING logged, install continued" \
        || fail "BBR -w fail: WARNING missing from stdout"
}

test_journald_restart_warns() {
    fakes_reset
    setstate JOURNALD_RESTART_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "journald restart fail: exit $rc, want 0"
    grep -q "WARNING:" "$TMP_TEST/out" && grep -q "systemd-journald" "$TMP_TEST/out" \
        && pass "journald restart fail: WARNING logged, install continued" \
        || fail "journald restart fail: WARNING missing from stdout"
}

test_installed_compose_contract() {
    # M9.2b: install.sh copies compose.yaml verbatim — the installed
    # copy must carry the host-mode/restart contract.
    #
    # Runs with --build: the prune assertions below are about cleaning up
    # after a local build, which the default (pull) path never does.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --build)"
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
    if awk '
        /^  panel:$/ { in_panel=1; in_awg=0; next }
        /^  awg:$/ { in_awg=1; in_panel=0; next }
        /^  [a-zA-Z0-9_-]+:/ { in_panel=0; in_awg=0 }
        in_panel && /driver: json-file/ { pdrv=1 }
        in_panel && /max-size: "10m"/ { psz=1 }
        in_panel && /max-file: "3"/ { pnf=1 }
        in_awg && /driver: json-file/ { adrv=1 }
        in_awg && /max-size: "10m"/ { asz=1 }
        in_awg && /max-file: "3"/ { anf=1 }
        END { exit (pdrv && psz && pnf && adrv && asz && anf) ? 0 : 1 }
    ' "$ROOT/compose.yaml"; then
        pass "installed compose: json-file max-size 10m / max-file 3 on panel and awg"
    else
        fail "installed compose: json-file rotation missing on panel/awg"
    fi
    [ "$(grep -c '^    ports:$' "$ROOT/compose.yaml")" = "1" ] \
        && pass "installed compose: only the panel maps ports" \
        || fail "installed compose: only the panel maps ports"
    grep -q "builder prune" "$FAKE_CALLS" && pass "install: docker builder prune after build" \
        || fail "install: docker builder prune after build"
    [ -x "$ROOT/docker-prune.sh" ] && pass "install: docker-prune.sh copied executable" \
        || fail "install: docker-prune.sh copied executable"
    if grep -qE 'rmi aaa111golang' "$FAKE_CALLS" || grep -qE 'rmi golang:' "$FAKE_CALLS"; then
        pass "install: golang image removal attempted"
    else
        fail "install: golang rmi (aaa111golang / golang:) not in FAKE_CALLS"
    fi
    grep -qx 'docker rmi golang:1.25.12' "$FAKE_CALLS" \
        && pass "install: rmi golang:1.25.12 as repo:tag" \
        || fail "install: rmi golang:1.25.12 must be a whole FAKE_CALLS line (repo:tag, not id suffix)"
    build_n="$(grep -nE 'docker compose .*[[:space:]]build([[:space:]]|$)' "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    prune_n="$(grep -nE 'docker builder prune' "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    up_n="$(grep -nE 'docker compose .*[[:space:]]up([[:space:]]|$)' "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    if [ -n "$build_n" ] && [ -n "$prune_n" ] && [ -n "$up_n" ] \
        && [ "$build_n" -lt "$prune_n" ] && [ "$prune_n" -lt "$up_n" ]; then
        pass "install: compose build, then builder prune, then compose up"
    else
        fail "install: prune order (build=$build_n prune=$prune_n up=$up_n)"
    fi
    if grep -q "rmi panelid" "$FAKE_CALLS" \
        || grep -q "rmi awgid" "$FAKE_CALLS" \
        || grep -q "rmi alpineid" "$FAKE_CALLS" \
        || grep -q "rmi amnezia-vpn-server/panel" "$FAKE_CALLS" \
        || grep -q "rmi amnezia-vpn-server/awg" "$FAKE_CALLS" \
        || grep -q "rmi alpine:3.22.5" "$FAKE_CALLS"; then
        fail "install: prune removed panel/awg/alpine images"
    else
        pass "install: prune leaves panel/awg/alpine images"
    fi
}

test_prune_soft_fail() {
    # docker-prune.sh swallows `docker builder prune` failures (`|| true`)
    # and always exits 0, so install.sh's WARNING branch is only reached
    # when the prune script itself exits 1. Stub the repo copy for this
    # case; restore it even if run_install dies.
    fakes_reset
    setstate BUILDER_PRUNE_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    prune_src="$M91_HOME/docker-prune.sh"
    prune_bak="$FAKE_DIR/docker-prune.sh.bak"
    cp -f "$prune_src" "$prune_bak"
    cat > "$prune_src" <<'EOF'
#!/usr/bin/env bash
# Test stub: docker builder prune fails, then the script exits 1 so
# install.sh logs WARNING and continues.
docker builder prune -af
exit 1
EOF
    chmod +x "$prune_src"
    restore_prune() { cp -f "$prune_bak" "$prune_src" && chmod +x "$prune_src"; }
    rc="$(run_install --build --build)"
    restore_prune
    [ "$rc" = "0" ] || fail "prune soft-fail: exit $rc, want 0"
    grep -q "WARNING: docker prune failed" "$TMP_TEST/out" \
        && pass "prune soft-fail: WARNING logged, install continued" \
        || fail "prune soft-fail: WARNING missing from stdout"
}

test_skip_prune() {
    fakes_reset
    os_release debian 12 bookworm
    # --build: the prune step only exists on the build path.
    rc="$(AMNEZIA_INSTALL_SKIP_PRUNE=1 run_install --build)"
    [ "$rc" = "0" ] || fail "SKIP_PRUNE=1 flow: exit $rc"
    if grep -q "builder prune" "$FAKE_CALLS"; then
        fail "SKIP_PRUNE=1: builder prune was invoked"
    else
        pass "SKIP_PRUNE=1: builder prune skipped"
    fi
    grep -q "AMNEZIA_INSTALL_SKIP_PRUNE" "$TMP_TEST/out" && pass "SKIP_PRUNE=1: skip logged" \
        || fail "SKIP_PRUNE=1: skip not logged"
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

test_hyphenated_fqdn_en_us() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(LC_ALL=en_US.UTF-8 run_install --panel-domain panel.super-space.com.de --vpn-domain super-space.com.de)"
    [ "$rc" = "0" ] || { fail "hyphenated FQDN en_US: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q "https://panel.super-space.com.de" "$TMP_TEST/out" \
        && pass "hyphenated FQDN en_US: --panel-domain panel.super-space.com.de accepted" \
        || fail "hyphenated FQDN en_US: panel URL missing (FQDN rejected under en_US?)"
    grep -q "endpoint super-space.com.de" "$TMP_TEST/out" \
        && pass "hyphenated FQDN en_US: --vpn-domain super-space.com.de accepted" \
        || fail "hyphenated FQDN en_US: vpn domain missing from endpoint hint"
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

# --- T-129 / T-156: client endpoint domain --------------------------------

test_domain_does_not_bind_clients() {
    # T-156: --domain / --panel-domain without --vpn-domain must NOT copy
    # the panel hostname onto client endpoints. Endpoint stays public IP.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --domain panel.example.com)"
    [ "$rc" = "0" ] || fail "domain-no-vpn-bind: exit $rc"
    grep -q "endpoint <public-ip>:443" "$TMP_TEST/out" \
        && pass "domain-no-vpn-bind: IP endpoint hint kept" \
        || fail "domain-no-vpn-bind: IP endpoint hint missing"
    grep -q "endpoint panel.example.com" "$TMP_TEST/out" \
        && fail "domain-no-vpn-bind: panel hostname leaked into the client endpoint" \
        || pass "domain-no-vpn-bind: panel hostname not used as endpoint"
    grep -q "A-record change" "$TMP_TEST/out" && fail "domain-no-vpn-bind: migration note printed" \
        || pass "domain-no-vpn-bind: no client-domain migration note"
    grep -qE "^CLIENT_DOMAIN=panel\.example\.com$" "$ROOT/.env" \
        && fail "domain-no-vpn-bind: CLIENT_DOMAIN copied from the panel domain" \
        || pass "domain-no-vpn-bind: CLIENT_DOMAIN not set to the panel domain"
    [ "$(grep -c "dig " "$FAKE_CALLS")" = "2" ] \
        && pass "domain-no-vpn-bind: only the panel domain pre-flighted (A+AAAA)" \
        || fail "domain-no-vpn-bind: dig count $(grep -c "dig " "$FAKE_CALLS"), want 2"
}

test_panel_domain_and_vpn_domain_aliases() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --panel-domain panel.example.com --vpn-domain vpn.example.com)"
    [ "$rc" = "0" ] || fail "aliases flow: exit $rc"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "aliases: panel URL uses --panel-domain" \
        || fail "aliases: panel URL missing"
    grep -q "endpoint vpn.example.com:443" "$TMP_TEST/out" \
        && pass "aliases: client endpoint uses --vpn-domain" \
        || fail "aliases: vpn endpoint missing"
    grep -q "CLIENT_DOMAIN=vpn.example.com" "$ROOT/.env" \
        && pass "aliases: CLIENT_DOMAIN persisted from --vpn-domain" \
        || fail "aliases: CLIENT_DOMAIN missing"
}

test_domain_with_panel_port() {
    # T-156: domain + panel-port is allowed. nginx TLS listens on the
    # chosen TCP port; ACME stays on 80; nftables opens 80 + PANEL_PORT
    # (not 443 TCP). Let's Encrypt, not a self-signed cert.
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --panel-domain panel.example.com --panel-port 8443)"
    [ "$rc" = "0" ] || fail "domain+panel-port: exit $rc"
    grep -q "listen 8443 ssl" "$ROOT/nginx/panel.conf" && pass "domain+panel-port: nginx TLS on 8443" \
        || fail "domain+panel-port: nginx listen 8443 missing"
    grep -q "listen 80" "$ROOT/nginx/panel.conf" && pass "domain+panel-port: ACME HTTP-01 on 80" \
        || fail "domain+panel-port: listen 80 missing"
    if grep -qE "listen 443" "$ROOT/nginx/panel.conf"; then
        fail "domain+panel-port: nginx still listens on 443 TCP"
    else
        pass "domain+panel-port: nginx does not listen on 443 TCP"
    fi
    grep -qE "^[[:space:]]*tcp dport 80 accept" "$ROOT/nftables/amnezia-vpn.nft" \
        && pass "domain+panel-port: nft tcp 80 for ACME" \
        || fail "domain+panel-port: nft 80 missing"
    grep -qE "^[[:space:]]*tcp dport 8443 accept" "$ROOT/nftables/amnezia-vpn.nft" \
        && pass "domain+panel-port: nft tcp 8443 for the panel" \
        || fail "domain+panel-port: nft 8443 missing"
    if grep -qE "^[[:space:]]*tcp dport 443 accept" "$ROOT/nftables/amnezia-vpn.nft"; then
        fail "domain+panel-port: nft still opens tcp 443"
    else
        pass "domain+panel-port: nft does not open tcp 443"
    fi
    grep -q "https://panel.example.com:8443" "$TMP_TEST/out" \
        && pass "domain+panel-port: panel URL includes the port" \
        || fail "domain+panel-port: https://host:port missing from the summary"
    grep -q "certbot certonly" "$FAKE_CALLS" && pass "domain+panel-port: certbot invoked" \
        || fail "domain+panel-port: certbot not invoked"
    if grep -q "openssl req" "$FAKE_CALLS"; then fail "domain+panel-port: openssl req (self-signed) invoked"; else pass "domain+panel-port: no self-signed certificate"; fi
    grep -q "udp dport 443 accept" "$ROOT/nftables/amnezia-vpn.nft" \
        && pass "domain+panel-port: UDP AWG 443 still open" \
        || fail "domain+panel-port: UDP AWG missing"
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

m91_run_all() {
test_bash_syntax
test_unsupported_os
test_supported_os_matrix
test_compose_current_skips_apt
test_docker_missing_installs
test_compose_old_upgraded
test_apt_retries_after_dpkg_lock
test_apt_dpkg_lock_timeout
test_compose_old_still_old
test_compose_minimum_boundary
test_invalid_port
test_default_port
test_custom_port
test_unknown_argument
test_help_examples
test_panel_loopback_and_no_sock
test_installed_compose_contract
test_prune_soft_fail
test_skip_prune
test_layout_and_permissions
test_versions_lock_used
test_ip_forward_disabled
test_ip_forward_already_enabled
test_sysctl_bbr_when_advertised
test_apt_output_is_not_swallowed
test_waits_while_dpkg_lock_is_held
test_dpkg_lock_deadline_reports_the_holder
test_lock_timeout_must_be_numeric
test_lock_poll_must_be_numeric
test_apt_retries_on_lists_lock
test_failed_apt_install_stops_with_its_own_message
test_failed_ppa_stops_with_its_own_message
test_apt_is_never_interactive
test_images_are_pulled_by_default
test_build_flag_compiles_locally
test_unreachable_registry_falls_back_to_build
test_sysctl_host_tuning
test_sysctl_host_tuning_survives_refusal
test_sysctl_bbr_loaded_from_module
test_sysctl_bbr_skipped_when_absent
test_journald_cap
test_weekly_prune_timer
test_sysctl_ip_forward_apply_aborts
test_sysctl_conntrack_apply_warns
test_sysctl_bbr_apply_warns
test_journald_restart_warns
test_m31_fresh_install_tolerated
test_panel_init_failure_not_masked
test_sentinel_guard_not_masked
test_domain_default_no_domain
test_domain_mode_flow
test_domain_dns_mismatch
test_domain_dns_missing
test_domain_certbot_failure
test_domain_invalid_fqdn
test_hyphenated_fqdn_en_us
test_panel_port_mode_flow
test_panel_port_loopback_matrix
test_panel_port_invalid_values
test_panel_port_idempotent
test_panel_port_regen_flag
test_panel_port_switch_back_to_loopback
test_domain_does_not_bind_clients
test_panel_domain_and_vpn_domain_aliases
test_domain_with_panel_port
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
test_pmtu_preflight_sizes_tunnel_for_a_tunnelled_uplink
test_pmtu_preflight_caps_at_the_default
test_pmtu_preflight_survives_filtered_icmp
test_pmtu_preflight_clamps_a_tiny_path
}

# --- tunnel MTU pre-flight (amnezia-vpn-server-6ztc) --------------------

# state_set KEY VALUE — overwrite one knob in the fake state file.
state_set() {
    sed "s|^${1}=.*|${1}=${2}|" "$FAKE_STATE" > "$FAKE_STATE.new" \
        && mv "$FAKE_STATE.new" "$FAKE_STATE"
}

# The uplink of a provider that tunnels its own traffic carries 1476, not
# 1500. A full tunnel packet costs MTU + 60, so the tunnel must be sized
# to 1416 — the operator is never asked to work this out.
test_pmtu_preflight_sizes_tunnel_for_a_tunnelled_uplink() {
    fakes_reset
    os_release ubuntu 24.04 noble
    state_set FAKE_PMTU 1476
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "pmtu flow: exit $rc"
    grep -q "^TUNNEL_MTU=1416$" "$ROOT/.env" \
        && pass "1476-byte uplink -> TUNNEL_MTU=1416 in .env" \
        || fail "1476-byte uplink -> TUNNEL_MTU: $(grep TUNNEL_MTU "$ROOT/.env" || echo missing)"
}

# A clean 1500-byte path must not push the tunnel above the historical
# WireGuard default: 1440 would work on this hop and break on the next one.
test_pmtu_preflight_caps_at_the_default() {
    fakes_reset
    os_release ubuntu 24.04 noble
    state_set FAKE_PMTU 1500
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "pmtu cap flow: exit $rc"
    grep -q "^TUNNEL_MTU=1420$" "$ROOT/.env" \
        && pass "1500-byte uplink -> TUNNEL_MTU capped at 1420" \
        || fail "1500-byte uplink -> TUNNEL_MTU: $(grep TUNNEL_MTU "$ROOT/.env" || echo missing)"
}

# Nothing about a filtered ICMP path should abort an install: the panel
# has a safe default, and the operator is told the measurement failed.
test_pmtu_preflight_survives_filtered_icmp() {
    fakes_reset
    os_release ubuntu 24.04 noble
    state_set FAKE_PMTU 0
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "filtered-ICMP flow: exit $rc"
    grep -q "^TUNNEL_MTU=" "$ROOT/.env" \
        && fail "filtered ICMP must not write a guessed TUNNEL_MTU" \
        || pass "filtered ICMP leaves TUNNEL_MTU unset (panel default applies)"
    stdout | grep -q "could not measure the uplink path MTU" \
        && pass "filtered ICMP warns the operator" \
        || fail "filtered ICMP must warn the operator"
}

# An absurdly small path must clamp at the IPv6 minimum rather than
# producing a tunnel MTU no stack will accept.
test_pmtu_preflight_clamps_a_tiny_path() {
    fakes_reset
    os_release ubuntu 24.04 noble
    state_set FAKE_PMTU 1300
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "tiny-path flow: exit $rc"
    grep -q "^TUNNEL_MTU=1280$" "$ROOT/.env" \
        && pass "1300-byte uplink -> TUNNEL_MTU clamped to 1280" \
        || fail "1300-byte uplink -> TUNNEL_MTU: $(grep TUNNEL_MTU "$ROOT/.env" || echo missing)"
}

if [ "$#" -gt 0 ]; then
    for t in "$@"; do
        # A renamed or mistyped test used to print "command not found" and
        # still finish with ALL TESTS PASSED and exit 0 — a CI job could go
        # green having run nothing.
        if ! declare -F "$t" >/dev/null; then
            echo "M9.1 install.sh: no such test: $t" >&2
            exit 2
        fi
        "$t"
    done
else
    m91_run_all
fi

echo
if [ "$M91_ERRORS" -eq 0 ]; then
    echo "M9.1 install.sh: ALL TESTS PASSED"
    exit 0
fi
echo "M9.1 install.sh: $M91_ERRORS test(s) FAILED"
exit 1
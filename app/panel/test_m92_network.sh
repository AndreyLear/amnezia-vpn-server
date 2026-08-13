#!/bin/bash
# M9.2 install.sh host networking / nftables test harness
# (contract: AUDITS/M9.2_AUDIT.md, ТЗ v2.0 §9).
#
# Runs install.sh against scripted fakes: nothing on the real host is
# touched (no Docker config, no apt, no systemd, no sysctl, no nft,
# no /etc). Every privileged/system command is a fake under the harness
# FAKE_DIR, prepended to PATH; host paths that install.sh writes are
# redirected into the harness tempdir through the AMNEZIA_INSTALL_* hooks.
#
#   bash app/panel/test_m92_network.sh
#
# Exit code: 0 when every test passes; 1 otherwise.

set -u

M92_ERRORS=0
M92_HOME="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_SH="$M92_HOME/install.sh"

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; M92_ERRORS=$((M92_ERRORS + 1)); }

# --- fakes -------------------------------------------------------------

FAKE_DIR="$(mktemp -d /tmp/m92-fakes.XXXXXX)"
FAKE_STATE="$FAKE_DIR/state.env"
FAKE_CALLS="$FAKE_DIR/calls.log"
FAKE_FS="$FAKE_DIR/fs"
TMP_TEST="$(mktemp -d /tmp/m92-run.XXXXXX)"
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
COMPOSE_VERSION=2.30.1
NEW_COMPOSE_VERSION=
DAEMON=ok
IP_FORWARD=1
NFT_CHECK_RC=0
NFT_APPLY_RC=0
NFT_APPLIED=0
NFTABLES_ACTIVE=yes
DOCKER_ACTIVE=yes
DU_CHAIN=yes
DU_IN_ACCEPT=0
DU_OUT_ACCEPT=0
FW_IN_ACCEPT=0
FW_OUT_ACCEPT=0
EOF
    # nft fake is hidden between runs (fakes_reset removes it) so the
    # "nft absent -> apt-get install nftables" path can be exercised;
    # the apt-get fake restores it from the hidden copy.
    rm -f "$FAKE_DIR/nft"
    cp "$FAKE_DIR/nft.hidden" "$FAKE_DIR/nft"
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
            version | config | build | up | ps) verb="$1"; break ;;
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
        ps)
            echo "amneziavpn-panel-init-1   panel-init   /app/panel init   Exited (1) 0 seconds ago"
            echo "amneziavpn-panel-1        panel        /app/panel serve  Created 0 seconds ago"
            echo "amneziavpn-awg-1          awg          /entrypoint.sh    Created 0 seconds ago"
            ;;
        build | up) ;;
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
    # installing nftables restores the nft fake (nft.hidden is the
    # pristine copy; fakes_reset removed the live one on purpose only
    # when the nft-absent path is under test)
    if [ ! -x "$FAKE_DIR/nft" ] && [ -f "$FAKE_DIR/nft.hidden" ]; then
        cp "$FAKE_DIR/nft.hidden" "$FAKE_DIR/nft"
        chmod +x "$FAKE_DIR/nft"
    fi
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
. "${FAKE_STATE:?}"
if [ "${1:-}" = "is-active" ]; then
    unit=""
    for a in "$@"; do
        case "$a" in
            -*) ;;
            is-active) ;;
            *) unit="$a" ;;
        esac
    done
    case "$unit" in
        nftables) [ "${NFTABLES_ACTIVE:-yes}" = "yes" ] && exit 0 || exit 3 ;;
        docker) [ "${DOCKER_ACTIVE:-yes}" = "yes" ] && exit 0 || exit 3 ;;
    esac
fi
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

chmod +x "$FAKE_DIR/docker" "$FAKE_DIR/apt-get" "$FAKE_DIR/systemctl" \
    "$FAKE_DIR/sysctl" "$FAKE_DIR/nft" "$FAKE_DIR/curl"

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

cat > "$FAKE_DIR/add-apt-repository" <<'FAKE_EOF'
#!/bin/bash
echo "add-apt-repository $*" >> "${FAKE_CALLS:?}"
exit 0
FAKE_EOF

cat > "$FAKE_DIR/iptables" <<'FAKE_EOF'
#!/bin/bash
echo "iptables $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
act=""; chain=""; dir=""; inf=""
for a in "$@"; do
    case "$a" in
        -L) act="L" ;;
        -C) act="C" ;;
        -I) act="I" ;;
        -t | filter | 1) ;;
        DOCKER-USER) chain="DU" ;;
        FORWARD) chain="FW" ;;
        awg0) dir="awg0" ;;
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

chmod +x "$FAKE_DIR/modprobe" "$FAKE_DIR/awg" "$FAKE_DIR/add-apt-repository" "$FAKE_DIR/iptables"
cp "$FAKE_DIR/nft" "$FAKE_DIR/nft.hidden"

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

NFT_SYS_FILE="$NFTABLES_DIR_TEST/amnezia-vpn.nft"
NFT_DEPLOY_FILE="$ROOT/nftables/amnezia-vpn.nft"

run_install() { # run_install [--root X] [--awg-port N] [--vpn-subnet CIDR]
    AMNEZIA_INSTALL_TEST=1 \
    AMNEZIA_INSTALL_FAKE_DIR="$FAKE_DIR" \
    AMNEZIA_INSTALL_OS_RELEASE="$TMP_TEST/os-release" \
    AMNEZIA_INSTALL_SYSCTL_DIR="$SYSCTL_TEST" \
    AMNEZIA_INSTALL_KEYRING_DIR="$KEYRING_TEST" \
    AMNEZIA_INSTALL_APT_SOURCES_DIR="$SOURCES_TEST" \
    AMNEZIA_INSTALL_NFTABLES_DIR="$NFTABLES_DIR_TEST" \
    AMNEZIA_INSTALL_NFTABLES_CONF="$NFTABLES_CONF_TEST" \
    AMNEZIA_INSTALL_SYSTEMD_DIR="$SYSTEMD_DIR_TEST" \
    AMNEZIA_INSTALL_MODULES_DIR="$TMP_TEST/modules-load.d" \
    PATH="${M92_PATH:-$FAKE_DIR:$PATH}" \
    bash "$INSTALL_SH" --root "$ROOT" "$@" > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    rc=$?
    if [ -n "${M92_DEBUG:-}" ] && [ "$rc" != "0" ]; then
        echo "=== DEBUG run rc=$rc ===" >&2
        cat "$TMP_TEST/out" "$TMP_TEST/err" >&2
    fi
    echo $rc
}

stdout() { cat "$TMP_TEST/out"; }
stderr() { cat "$TMP_TEST/err"; }

assert_in() { # assert_in needle file label
    grep -q "$1" "$2" && pass "$3" || fail "$3"
}
assert_not_in() {
    grep -q "$1" "$2" && fail "$3" || pass "$3"
}

# --- tests ---------------------------------------------------------------

test_bash_syntax() {
    bash -n "$INSTALL_SH" && pass "bash -n install.sh" || fail "bash -n install.sh"
    bash -n "${BASH_SOURCE[0]}" && pass "bash -n test_m92_network.sh" || fail "bash -n test_m92_network.sh"
}

test_help_lists_m92() {
    AMNEZIA_INSTALL_TEST=1 bash "$INSTALL_SH" --help \
        > "$TMP_TEST/help.out" 2> "$TMP_TEST/help.err"
    rc=$?
    [ "$rc" = "0" ] || fail "--help: exit $rc, want 0"
    grep -q -- "--vpn-subnet" "$TMP_TEST/help.out" && pass "--help lists --vpn-subnet" \
        || fail "--help lists --vpn-subnet"
    grep -q "nftables" "$TMP_TEST/help.out" && pass "--help mentions the nftables ruleset" \
        || fail "--help mentions the nftables ruleset"
    grep -q "M9.1" "$TMP_TEST/help.out" && fail "--help still says M9.1" \
        || pass "--help no longer claims M9.1-only scope"
}

test_invalid_subnets() {
    fakes_reset
    os_release debian 12 bookworm
    for bad in "abc" "10.0.0.0" "10.0.0.0/33" "10.0.0.0/0" "10.0.0.256/24" \
        "300.1.1.1/24" "10.0.0.0/24/" "10.0.0.0/-1" "1.2.3.4/24/33"; do
        rc="$(run_install --vpn-subnet "$bad")"
        [ "$rc" = "2" ] || fail "invalid subnet '$bad': exit $rc, want 2"
    done
    rc="$(run_install --vpn-subnet "")"
    [ "$rc" = "2" ] || fail "empty subnet: exit $rc, want 2"
    pass "invalid --vpn-subnet values rejected (exit 2)"
}

test_host_cidr_normalized() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --vpn-subnet 10.8.0.7/24)"
    [ "$rc" = "0" ] || fail "host-CIDR flow: exit $rc"
    assert_in "ip saddr 10.8.0.0/24 accept" "$NFT_SYS_FILE" "host CIDR normalized to network (10.8.0.7/24 -> 10.8.0.0/24)"
    assert_not_in "10.8.0.7" "$NFT_SYS_FILE" "host bits never appear in the ruleset"
}

test_default_rules() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "default flow: exit $rc"
    assert_in "^table ip amnezia$" "$NFT_SYS_FILE" "fresh install: idempotent add-table line present"
    assert_in "^flush table ip amnezia$" "$NFT_SYS_FILE" "fresh install: flush of own table present (no-op when empty)"
    assert_in "^table ip amnezia {" "$NFT_SYS_FILE" "table ip amnezia declared"
    assert_in "ip saddr 10.8.0.0/24 accept" "$NFT_SYS_FILE" "forward: vpn subnet saddr accepted"
    assert_in "ip daddr 10.8.0.0/24 accept" "$NFT_SYS_FILE" "forward: vpn subnet daddr accepted"
    assert_in "udp dport 51820 accept" "$NFT_SYS_FILE" "input: default UDP 51820 accepted"
    assert_in 'ip saddr 10.8.0.0/24 oifname != "awg0" masquerade' "$NFT_SYS_FILE" "postrouting: NAT for subnet, never into the tunnel"
}

test_atomic_replace_on_rerun() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "rerun flow first pass: exit $rc"
    cmp -s "$NFT_DEPLOY_FILE" "$NFT_SYS_FILE" || fail "first pass: fragments identical"
    cp "$NFT_SYS_FILE" "$TMP_TEST/ref.nft"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "rerun flow second pass: exit $rc"
    cmp -s "$NFT_SYS_FILE" "$TMP_TEST/ref.nft" && pass "second pass: rendered fragment byte-identical (deterministic)" \
        || fail "second pass: rendered fragment changed between passes"
    assert_in "^table ip amnezia$" "$NFT_SYS_FILE" "second pass: add-table line still present"
    assert_in "^flush table ip amnezia$" "$NFT_SYS_FILE" "second pass: flush clears only own table"
    assert_in "^table ip amnezia {" "$NFT_SYS_FILE" "second pass: table recreated in the same batch"
}

test_custom_values() {
    fakes_reset
    os_release ubuntu 22.04 jammy
    rc="$(run_install --awg-port 23456 --vpn-subnet 192.168.77.0/29)"
    [ "$rc" = "0" ] || fail "custom values flow: exit $rc"
    assert_in "udp dport 23456 accept" "$NFT_SYS_FILE" "custom UDP port in ruleset"
    assert_in "ip saddr 192.168.77.0/29 accept" "$NFT_SYS_FILE" "custom subnet in ruleset"
    assert_not_in "dport 51820" "$NFT_SYS_FILE" "default port absent when customized"
    assert_not_in "8787" "$NFT_SYS_FILE" "panel port never part of the ruleset"
}

test_no_flush_no_drop() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "no-flush flow: exit $rc"
    assert_not_in "flush ruleset" "$NFT_SYS_FILE" "never flushes the host ruleset"
    assert_not_in " drop" "$NFT_SYS_FILE" "no drop rules anywhere"
    assert_not_in "policy drop" "$NFT_SYS_FILE" "no drop policies"
}

test_fragments_identical() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "fragment flow: exit $rc"
    cmp -s "$NFT_DEPLOY_FILE" "$NFT_SYS_FILE" && pass "deploy and system fragments identical" \
        || fail "deploy and system fragments differ"
}

test_check_before_apply() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "ordering flow: exit $rc"
    n1="$(grep -n "^nft -c -f " "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    n2="$(grep -n "^nft -f " "$FAKE_CALLS" | head -1 | cut -d: -f1)"
    if [ -n "$n1" ] && [ -n "$n2" ] && [ "$n1" -lt "$n2" ]; then
        pass "syntax check (nft -c -f) precedes the apply (nft -f)"
    else
        fail "syntax check does not precede the apply (check=$n1 apply=$n2)"
    fi
    grep -q "nft list table ip amnezia" "$FAKE_CALLS" && pass "post-reload verification nft list table ran" \
        || fail "post-reload verification nft list table did not run"
}

test_syntax_failure_rollback() {
    fakes_reset
    setstate NFT_CHECK_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "syntax failure: exit $rc, want 1"
    grep -q "syntax check failed" "$TMP_TEST/err" && pass "syntax failure: rejection message" \
        || fail "syntax failure: rejection message"
    [ ! -f "$NFT_SYS_FILE" ] && [ ! -f "$NFT_DEPLOY_FILE" ] \
        && pass "syntax failure: both fragments removed" \
        || fail "syntax failure: fragments left behind"
    grep -q "^nft -f " "$FAKE_CALLS" && fail "syntax failure: apply was attempted" \
        || pass "syntax failure: apply never attempted"
    grep -q "systemctl enable nftables" "$FAKE_CALLS" && fail "syntax failure: persistence still ran" \
        || pass "syntax failure: persistence not reached"
}

test_apply_failure_rollback() {
    fakes_reset
    setstate NFT_APPLY_RC 1 "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "apply failure: exit $rc, want 1"
    grep -q "previous rules were not touched" "$TMP_TEST/err" && pass "apply failure: rollback message" \
        || fail "apply failure: rollback message"
    [ ! -f "$NFT_SYS_FILE" ] && [ ! -f "$NFT_DEPLOY_FILE" ] \
        && pass "apply failure: both fragments removed" \
        || fail "apply failure: fragments left behind"
    grep -q "systemctl enable nftables" "$FAKE_CALLS" && fail "apply failure: persistence still ran" \
        || pass "apply failure: persistence not reached"
}

test_persistence_first_run() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "persistence flow: exit $rc"
    [ -f "$NFTABLES_CONF_TEST" ] || fail "nftables.conf was not created"
    first="$(head -1 "$NFTABLES_CONF_TEST")"
    [ "$first" = "#!/usr/sbin/nft -f" ] && pass "nftables.conf created with managed shebang" \
        || fail "nftables.conf first line: '$first'"
    assert_in "^# --- amnezia-vpn begin ---" "$NFTABLES_CONF_TEST" "managed begin marker present"
    assert_in "^include \"$NFT_SYS_FILE\"" "$NFTABLES_CONF_TEST" "include points at the injected fragment"
    assert_in "^# --- amnezia-vpn end ---" "$NFTABLES_CONF_TEST" "managed end marker present"
    for cmd in "systemctl enable nftables" "systemctl daemon-reload"; do
        grep -q "$cmd" "$FAKE_CALLS" && pass "persistence: $cmd invoked" \
            || fail "persistence: $cmd not invoked"
    done
    grep -q "systemctl start nftables" "$FAKE_CALLS" && fail "persistence: start invoked on an active service (runtime flush guard)" \
        || pass "persistence: no start on an already-active nftables (runtime flush guard)"
    grep -q "systemctl restart docker" "$FAKE_CALLS" && fail "persistence: docker restart without an nftables start" \
        || pass "persistence: no docker restart when nftables was not started"
}

test_persistence_start_when_inactive() {
    fakes_reset
    setstate NFTABLES_ACTIVE no "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "persistence inactive flow: exit $rc"
    grep -q "systemctl start nftables" "$FAKE_CALLS" && pass "inactive nftables: service started" \
        || fail "inactive nftables: start not invoked"
    grep -q "systemctl restart docker" "$FAKE_CALLS" && pass "inactive nftables: docker restarted after the distro flush" \
        || fail "inactive nftables: docker restart missing after start"
    grep -q "systemctl enable nftables" "$FAKE_CALLS" && pass "inactive nftables: still enabled" \
        || fail "inactive nftables: enable missing"
}

test_persistence_foreign_content() {
    fakes_reset
    os_release debian 12 bookworm
    cat > "$NFTABLES_CONF_TEST" <<'EOF'
#!/usr/sbin/nft -f

# user custom rule
add table ip filter

# --- amnezia-vpn begin ---
include "/etc/nftables.d/amnezia-vpn.nft"
# --- amnezia-vpn end ---

# trailing user comment
EOF
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "persistence rerun 1: exit $rc"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "persistence rerun 2: exit $rc"
    grep -q "user custom rule" "$NFTABLES_CONF_TEST" && pass "foreign content preserved" \
        || fail "foreign content lost"
    grep -q "add table ip filter" "$NFTABLES_CONF_TEST" && pass "foreign rules preserved" \
        || fail "foreign rules lost"
    grep -q "trailing user comment" "$NFTABLES_CONF_TEST" && pass "trailing foreign content preserved" \
        || fail "trailing content lost"
    [ "$(grep -c "amnezia-vpn begin" "$NFTABLES_CONF_TEST")" = "1" ] \
        && pass "exactly one managed block after rerun" \
        || fail "managed block duplicated after rerun"
}

test_persistence_dropin() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "drop-in flow: exit $rc"
    dropin="$SYSTEMD_DIR_TEST/docker.service.d/amnezia-vpn-nftables.conf"
    [ -f "$dropin" ] || fail "docker drop-in missing"
    assert_in "After=nftables.service" "$dropin" "docker ordered after nftables at boot"
    mode() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1" 2>/dev/null; }
    [ "$(mode "$dropin")" = "644" ] && pass "drop-in mode 0644" || fail "drop-in mode: $(mode "$dropin")"
    [ "$(mode "$NFT_SYS_FILE")" = "644" ] && pass "fragment mode 0644" || fail "fragment mode: $(mode "$NFT_SYS_FILE")"
}

test_subnet_from_awg0_conf() {
    fakes_reset
    os_release debian 12 bookworm
    mkdir -p "$ROOT/config"
    printf '[Interface]\nAddress = 10.9.0.5/16\nListenPort = 51820\n' > "$ROOT/config/awg0.conf"
    rc="$(run_install --vpn-subnet 10.8.0.0/24)"
    [ "$rc" = "0" ] || fail "awg0.conf precedence flow: exit $rc"
    assert_in "ip saddr 10.9.0.0/16 accept" "$NFT_SYS_FILE" "server.address from awg0.conf wins over the flag"
    assert_not_in "10.8.0.0/24" "$NFT_SYS_FILE" "flag subnet ignored when awg0.conf is authoritative"
}

test_subnet_awg0_conf_invalid_fallback() {
    fakes_reset
    os_release debian 12 bookworm
    mkdir -p "$ROOT/config"
    printf '[Interface]\nAddress = not-a-cidr\n' > "$ROOT/config/awg0.conf"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "awg0.conf fallback flow: exit $rc"
    assert_in "ip saddr 10.8.0.0/24 accept" "$NFT_SYS_FILE" "invalid awg0.conf Address falls back to deployment value"
}

test_nft_absent_installs_package() {
    fakes_reset
    rm -f "$FAKE_DIR/nft"
    # Ubuntu/Debian ship a real nft(8) in /usr/sbin — the harness must
    # not resolve it; every other system path stays for the fakes.
    M92_PATH="$FAKE_DIR:$(printf '%s\n' "$PATH" | tr ':' '\n' | grep -Ev '^/(usr/)?sbin$' | tr '\n' ':')"
    os_release debian 12 bookworm
    rc="$(run_install)"
    M92_PATH=
    [ "$rc" = "0" ] || fail "nft-absent flow: exit $rc"
    grep -q "apt-get install -y nftables" "$FAKE_CALLS" && pass "nftables package installed when nft missing" \
        || fail "nftables package install not invoked"
    [ -f "$NFT_SYS_FILE" ] && pass "ruleset applied after package install (fake restored)" \
        || fail "ruleset missing after package install"
}

test_env_readback_on_rerun() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install --awg-port 23456 --vpn-subnet 10.20.0.0/24)"
    [ "$rc" = "0" ] || fail "read-back first pass: exit $rc"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "read-back second pass: exit $rc"
    assert_in "udp dport 23456 accept" "$NFT_SYS_FILE" "rerun without flags keeps .env AWG_PORT (read-back)"
    assert_in "ip saddr 10.20.0.0/24 accept" "$NFT_SYS_FILE" "rerun without flags keeps .env VPN_SUBNET (read-back)"
    rc="$(run_install --awg-port 44444 --vpn-subnet 10.30.0.0/24)"
    [ "$rc" = "0" ] || fail "read-back third pass: exit $rc"
    assert_in "udp dport 44444 accept" "$NFT_SYS_FILE" "explicit flag overrides the .env AWG_PORT"
    assert_in "ip saddr 10.30.0.0/24 accept" "$NFT_SYS_FILE" "explicit flag overrides the .env VPN_SUBNET"
    grep -q "AWG_PORT=23456" "$ROOT/.env" && pass ".env itself is never rewritten on rerun" \
        || fail ".env was modified on rerun"
}

test_env_vpn_subnet_missing() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "env-missing first pass: exit $rc"
    sed '/^VPN_SUBNET=/d' "$ROOT/.env" > "$ROOT/.env.new" && mv "$ROOT/.env.new" "$ROOT/.env"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "env-missing second pass: exit $rc"
    assert_in "ip saddr 10.8.0.0/24 accept" "$NFT_SYS_FILE" "missing .env VPN_SUBNET falls back to the default"
}

test_env_invalid_awg_port() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "env-invalid first pass: exit $rc"
    sed 's/^AWG_PORT=.*/AWG_PORT=99999/' "$ROOT/.env" > "$ROOT/.env.new" && mv "$ROOT/.env.new" "$ROOT/.env"
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "invalid .env AWG_PORT: exit $rc, want 1"
    grep -q "AWG_PORT in .env is out of range" "$TMP_TEST/err" && pass "invalid .env AWG_PORT rejected" \
        || fail "invalid .env AWG_PORT rejection message"
}

test_env_invalid_vpn_subnet() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "env-invalid first pass: exit $rc"
    sed 's/^VPN_SUBNET=.*/VPN_SUBNET=bad/' "$ROOT/.env" > "$ROOT/.env.new" && mv "$ROOT/.env.new" "$ROOT/.env"
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "invalid .env VPN_SUBNET: exit $rc, want 1"
    grep -q "VPN_SUBNET in .env is not a valid CIDR" "$TMP_TEST/err" && pass "invalid .env VPN_SUBNET rejected" \
        || fail "invalid .env VPN_SUBNET rejection message"
}

test_env_missing_awg_port() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "env-missing-port first pass: exit $rc"
    sed '/^AWG_PORT=/d' "$ROOT/.env" > "$ROOT/.env.new" && mv "$ROOT/.env.new" "$ROOT/.env"
    rc="$(run_install)"
    [ "$rc" = "1" ] || fail "missing .env AWG_PORT: exit $rc, want 1"
    grep -q "AWG_PORT unset in .env" "$TMP_TEST/err" && pass "missing .env AWG_PORT rejected" \
        || fail "missing .env AWG_PORT rejection message"
}

test_secrets_absent() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "secrets flow: exit $rc"
    for needle in "private_key" "password" "age identity" "docker.asc"; do
        if grep -qi "$needle" "$TMP_TEST/out" "$TMP_TEST/err" 2>/dev/null; then
            fail "secret-like output leaked: \"$needle\""
        fi
    done
    pass "no secrets/credentials in stdout+stderr"
}

test_ssh_hint() {
    fakes_reset
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "hint flow: exit $rc"
    grep -q "ssh -L 8787:127.0.0.1:8787" "$TMP_TEST/out" && pass "SSH tunnel hint printed" \
        || fail "SSH tunnel hint missing from stdout"
}

test_awg_stack_present_skips_install() {
    fakes_reset
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "awg-present flow: exit $rc"
    grep -q "add-apt-repository" "$FAKE_CALLS" && fail "awg stack reinstalled though module + awg are present" \
        || pass "awg stack skipped when module + awg tools are present"
    grep -q "apt-get install -y amneziawg" "$FAKE_CALLS" && fail "amneziawg packages installed though present" \
        || pass "no amneziawg package install when already present"
    [ ! -f "$TMP_TEST/modules-load.d/amneziawg.conf" ] && pass "no modules-load entry when already present" \
        || fail "modules-load entry written though module is present"
}

test_awg_stack_forced_install() {
    fakes_reset
    os_release ubuntu 24.04 noble
    AMNEZIA_INSTALL_FORCE_AWG_INSTALL=1 \
    AMNEZIA_INSTALL_TEST=1 \
    AMNEZIA_INSTALL_FAKE_DIR="$FAKE_DIR" \
    AMNEZIA_INSTALL_OS_RELEASE="$TMP_TEST/os-release" \
    AMNEZIA_INSTALL_SYSCTL_DIR="$SYSCTL_TEST" \
    AMNEZIA_INSTALL_KEYRING_DIR="$KEYRING_TEST" \
    AMNEZIA_INSTALL_APT_SOURCES_DIR="$SOURCES_TEST" \
    AMNEZIA_INSTALL_NFTABLES_DIR="$NFTABLES_DIR_TEST" \
    AMNEZIA_INSTALL_NFTABLES_CONF="$NFTABLES_CONF_TEST" \
    AMNEZIA_INSTALL_SYSTEMD_DIR="$SYSTEMD_DIR_TEST" \
    AMNEZIA_INSTALL_MODULES_DIR="$TMP_TEST/modules-load.d" \
    PATH="$FAKE_DIR:$PATH" \
    bash "$INSTALL_SH" --root "$ROOT" > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    rc=$?
    [ "$rc" = "0" ] || fail "awg forced flow: exit $rc"
    grep -q "add-apt-repository -y ppa:amnezia/ppa" "$FAKE_CALLS" && pass "official PPA added (ppa:amnezia/ppa)" \
        || fail "PPA addition missing"
    grep -q "apt-get install -y amneziawg amneziawg-tools" "$FAKE_CALLS" && pass "amneziawg + amneziawg-tools installed" \
        || fail "amneziawg package install missing"
    grep -q "apt-get install -y software-properties-common linux-headers-" "$FAKE_CALLS" \
        && pass "dkms build deps (software-properties-common, linux-headers) installed" \
        || fail "dkms build deps missing"
    grep -q "^amneziawg$" "$TMP_TEST/modules-load.d/amneziawg.conf" && pass "modules-load entry written" \
        || fail "modules-load entry missing"
    grep -q "modprobe amneziawg$" "$FAKE_CALLS" && pass "module probed after install" \
        || fail "post-install modprobe missing"
    grep -q "awg version" "$FAKE_CALLS" && pass "awg version reported" \
        || fail "awg version not reported"
}

test_forward_accept_docker_user() {
    fakes_reset
    os_release ubuntu 24.04 noble
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "forward-accept flow: exit $rc"
    grep -q "iptables -t filter -I DOCKER-USER 1 -i awg0 -j ACCEPT" "$FAKE_CALLS" \
        && pass "forward accept -i awg0 inserted into DOCKER-USER" \
        || fail "forward accept -i awg0 missing from DOCKER-USER"
    grep -q "iptables -t filter -I DOCKER-USER 1 -o awg0 -j ACCEPT" "$FAKE_CALLS" \
        && pass "forward accept -o awg0 inserted into DOCKER-USER" \
        || fail "forward accept -o awg0 missing from DOCKER-USER"
    grep -q "iptables -t filter -C DOCKER-USER" "$FAKE_CALLS" && pass "-C guard checked before insert" \
        || fail "-C guard missing"
    unit="$SYSTEMD_DIR_TEST/amnezia-vpn-forward.service"
    [ -f "$unit" ] || fail "forward-accept unit missing"
    grep -q "After=docker.service nftables.service" "$unit" && pass "unit ordered after docker+nftables" \
        || fail "unit ordering missing"
    grep -q "systemctl enable amnezia-vpn-forward.service" "$FAKE_CALLS" && pass "unit enabled" \
        || fail "unit not enabled"
    grep -q "systemctl start amnezia-vpn-forward.service" "$FAKE_CALLS" && pass "unit started" \
        || fail "unit not started"
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "forward-accept rerun: exit $rc"
    [ "$(grep -c "iptables -t filter -I DOCKER-USER" "$FAKE_CALLS")" = "2" ] \
        && pass "rerun: no duplicate inserts (-C guard) (2 inserts across two runs)" \
        || fail "rerun: inserts duplicated: $(grep -c "iptables -t filter -I DOCKER-USER" "$FAKE_CALLS")"
}

test_forward_accept_no_docker_user() {
    fakes_reset
    setstate DU_CHAIN no "$FAKE_STATE"
    os_release debian 12 bookworm
    rc="$(run_install)"
    [ "$rc" = "0" ] || fail "forward-accept no-DOCKER-USER flow: exit $rc"
    grep -q "iptables -t filter -I FORWARD 1 -i awg0 -j ACCEPT" "$FAKE_CALLS" \
        && pass "forward accept falls back to FORWARD top when DOCKER-USER absent" \
        || fail "FORWARD fallback insert missing"
    grep -q "iptables -t filter -I FORWARD 1 -o awg0 -j ACCEPT" "$FAKE_CALLS" \
        && pass "-o awg0 insert into FORWARD" \
        || fail "-o awg0 FORWARD insert missing"
}

# --- main ---------------------------------------------------------------

test_bash_syntax
test_help_lists_m92
test_invalid_subnets
test_host_cidr_normalized
test_default_rules
test_atomic_replace_on_rerun
test_custom_values
test_no_flush_no_drop
test_fragments_identical
test_check_before_apply
test_syntax_failure_rollback
test_apply_failure_rollback
test_persistence_first_run
test_persistence_start_when_inactive
test_persistence_foreign_content
test_persistence_dropin
test_subnet_from_awg0_conf
test_subnet_awg0_conf_invalid_fallback
test_nft_absent_installs_package
test_env_readback_on_rerun
test_env_vpn_subnet_missing
test_env_invalid_awg_port
test_env_invalid_vpn_subnet
test_env_missing_awg_port
test_secrets_absent
test_ssh_hint
test_awg_stack_present_skips_install
test_awg_stack_forced_install
test_forward_accept_docker_user
test_forward_accept_no_docker_user

echo
if [ "$M92_ERRORS" -eq 0 ]; then
    echo "M9.2 network/nftables install.sh: ALL TESTS PASSED"
    exit 0
fi
echo "M9.2 network/nftables install.sh: $M92_ERRORS test(s) FAILED"
exit 1
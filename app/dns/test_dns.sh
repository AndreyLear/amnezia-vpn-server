#!/bin/bash
# T-rnub tests for the in-tunnel resolver entrypoint.
#
# The entrypoint renders a dnsmasq configuration from the deployment
# environment and execs dnsmasq. Everything worth asserting is in that
# configuration, so the harness puts a fake dnsmasq on PATH that prints
# the file it was handed and exits.
#
# What must hold:
#   - the resolver binds the tunnel address only (never the wildcard),
#     otherwise it becomes an open resolver on the public internet;
#   - it survives a start before awg0 exists (bind-dynamic);
#   - the panel hostname resolves to the tunnel address, and only when
#     a hostname was configured;
#   - every upstream from UPSTREAM_DNS is forwarded to, and no resolver
#     is inherited from the host (no-resolv).
#
# Runs on plain bash (macOS or Linux); no root, no Docker, no network.
set -u

cd "$(dirname "$0")"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-dns-test.XXXXXX")"
cleanup() {
    if [ -n "${KEEP_TMP:-}" ]; then
        echo "TMP kept at: ${TMP}" >&2
        return
    fi
    rm -rf "${TMP}"
}
trap cleanup EXIT

check() { # check <name> [not] <cmd...>
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

# ---- fake dnsmasq: print the rendered configuration, then exit ----------
mkdir -p "${TMP}/bin"
cat > "${TMP}/bin/dnsmasq" <<'FAKE'
#!/bin/sh
for arg in "$@"; do
    case "$arg" in
        --conf-file=*) cat "${arg#--conf-file=}" ;;
    esac
done
exit 0
FAKE
chmod +x "${TMP}/bin/dnsmasq"

# `exec sleep infinity` would hang the harness; the fake records the call
# and returns, which is all the assertions need.
cat > "${TMP}/bin/sleep" <<'FAKE'
#!/bin/sh
echo "[dns] would idle: sleep $*"
exit 0
FAKE
chmod +x "${TMP}/bin/sleep"

# render [VAR=VALUE ...]: the configuration the entrypoint would run with.
render() {
    env -i PATH="${TMP}/bin:/usr/bin:/bin" "$@" sh ./entrypoint.sh 2>&1
}

has() { printf '%s\n' "$2" | grep -qx -- "$1"; }
lacks() { ! printf '%s\n' "$2" | grep -q -- "$1"; }

# ---- 1. panel hostname configured --------------------------------------
CONF="$(render PANEL_DOMAIN=panel.example.com TUNNEL_ADDRESS=10.8.0.1 UPSTREAM_DNS=1.1.1.1,8.8.8.8)"

check "binds the tunnel address" has 'listen-address=10.8.0.1' "$CONF"
check "binds dynamically (awg0 appears later)" has 'bind-dynamic' "$CONF"
check "never binds every interface" lacks 'bind-interfaces' "$CONF"
check "panel hostname answers with the tunnel address" \
    has 'address=/panel.example.com/10.8.0.1' "$CONF"
check "forwards to the first upstream" has 'server=1.1.1.1' "$CONF"
check "forwards to the second upstream" has 'server=8.8.8.8' "$CONF"
check "ignores the host resolver" has 'no-resolv' "$CONF"
check "ignores the host hosts file" has 'no-hosts' "$CONF"
check "caches answers" has 'cache-size=1000' "$CONF"
check "reports the mapping it installed" \
    grep -q 'panel.example.com -> 10.8.0.1' <<<"$CONF"

# ---- 2. no panel hostname: a plain forwarder ---------------------------
CONF="$(render TUNNEL_ADDRESS=10.8.0.1 UPSTREAM_DNS=1.1.1.1)"

check "no hostname override without a panel domain" lacks 'address=/' "$CONF"
check "still binds the tunnel address" has 'listen-address=10.8.0.1' "$CONF"
check "still forwards" has 'server=1.1.1.1' "$CONF"

# ---- 3. a different tunnel subnet --------------------------------------
CONF="$(render PANEL_DOMAIN=vpn.example.org TUNNEL_ADDRESS=10.20.0.1 UPSTREAM_DNS=9.9.9.9)"

check "honours a non-default tunnel address" has 'listen-address=10.20.0.1' "$CONF"
check "answers the hostname with that address" \
    has 'address=/vpn.example.org/10.20.0.1' "$CONF"

# ---- 4. defaults when the deployment passes nothing ---------------------
CONF="$(render)"

check "defaults to the standard tunnel address" has 'listen-address=10.8.0.1' "$CONF"
check "defaults to public upstreams" has 'server=1.1.1.1' "$CONF"
check "starts without a panel domain" lacks 'address=/' "$CONF"

# ---- 5. standing down for --no-tunnel-dns ------------------------------
# The flag exists because something else already owns port 53. Writing it
# into .env is not enough: the container has to actually leave the port
# alone, and it must not exit either, or restart:unless-stopped spins it.
OUT="$(render TUNNEL_DNS_DISABLED=1 PANEL_DOMAIN=panel.example.com FAKE_SLEEP=1 2>&1)"
check "disabled: says so plainly" grep -q "not binding port 53" <<<"$OUT"
check "disabled: renders no listen-address" lacks 'listen-address' "$OUT"
check "disabled: renders no upstreams" lacks 'server=' "$OUT"
check "disabled: does not answer the panel hostname" lacks 'address=/' "$OUT"

# 0 is not 1: only an explicit opt-out stands the resolver down.
OUT="$(render TUNNEL_DNS_DISABLED=0 TUNNEL_ADDRESS=10.8.0.1 UPSTREAM_DNS=1.1.1.1)"
check "TUNNEL_DNS_DISABLED=0 still serves" has 'listen-address=10.8.0.1' "$OUT"

echo
echo "passed: ${PASSED}, failed: ${FAILED}"
[ "${FAILED}" = "0" ]

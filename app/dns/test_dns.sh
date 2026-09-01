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
#     is inherited from the host (no-resolv);
#   - AAAA answers are filtered out while the tunnel carries IPv4 only.
#
# Runs on plain bash (macOS or Linux); no root, no Docker, no network.
# The entrypoint itself runs under a shell with the image's semantics,
# not the host's /bin/sh — see pick_entrypoint_shell below.
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

# The entrypoint is #!/bin/sh, but the shell that actually runs it is
# Alpine's busybox ash — never the host's /bin/sh. On Debian and Ubuntu
# /bin/sh is dash, which rejects the entrypoint's `set -o pipefail` and
# failed this whole harness on a script the image runs fine.
#
# busybox cannot be the runner either: distributions build it as a
# standalone shell, so its built-in `sleep` applet wins over the fake on
# PATH and the standing-down test idles forever. bash honours the fakes
# and is a superset of everything the entrypoint uses, so bash runs the
# script — and busybox, where the host has it, still parses it, which is
# what keeps the assertions honest about ash.
pick_entrypoint_shell() {
    local candidate
    if candidate="$(command -v bash 2>/dev/null)" &&
       "${candidate}" -c 'set -o pipefail' 2>/dev/null; then
        ENTRYPOINT_SHELL="${candidate}"
        return
    fi
    echo "no shell that supports 'set -o pipefail' (need bash)" >&2
    exit 1
}
pick_entrypoint_shell

# ---- 0. the image's shell accepts the entrypoint at all -----------------
# Cheap fidelity: parsing and option support are exactly what running it
# under the host's /bin/sh got wrong, and neither needs the script to run.
BUSYBOX="$(command -v busybox 2>/dev/null || true)"
if [ -n "${BUSYBOX}" ]; then
    check "the image's shell parses the entrypoint" \
        "${BUSYBOX}" sh -n ./entrypoint.sh
    check "the image's shell supports the entrypoint's shell options" \
        "${BUSYBOX}" sh -c 'set -o pipefail'
else
    echo "SKIP: busybox absent, ash fidelity unchecked on this host"
fi

# render [VAR=VALUE ...]: the configuration the entrypoint would run with.
render() {
    env -i PATH="${TMP}/bin:/usr/bin:/bin" "$@" \
        "${ENTRYPOINT_SHELL}" ./entrypoint.sh 2>&1
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
# The tunnel is IPv4-only, so an AAAA answer sends the client at an address
# nothing can reach — and ::/0 in AllowedIPs then swallows the packet. An
# owner met this as YouTube serving its page while every video card read
# "no internet connection". Answer with A records only until the tunnel
# carries IPv6 for real.
check "hides IPv6 addresses the tunnel cannot reach" has 'filter-AAAA' "$CONF"
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
OUT="$(render TUNNEL_DNS_DISABLED=1 PANEL_DOMAIN=panel.example.com 2>&1)"
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

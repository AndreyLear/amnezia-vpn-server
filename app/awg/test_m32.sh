#!/bin/bash
# M3.2 tests for the awg runtime reload.
#
# 1) Unit tests of filter_syncconf_config (pure awk).
# 2) Behavioral tests of entrypoint.sh against stub binaries
#    (awg-quick, ip, awg, stat, chown) driven by flag files/env.
# 3) Syntax checks.
#
# All tests run on plain bash (macOS or Linux); no root, no Docker.
set -u

cd "$(dirname "$0")"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/awg-m32-test.XXXXXX")"
PIDS=""
cleanup() {
    for p in ${PIDS}; do
        kill "${p}" 2>/dev/null
        wait "${p}" 2>/dev/null
    done
    if [ -n "${KEEP_TMP:-}" ]; then
        echo "TMP kept at: ${TMP}" >&2
        return
    fi
    rm -rf "${TMP}"
}
trap cleanup EXIT

# check <name> [not] <cmd...>: runs the command; "not" negates its result.
check() {
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

# ---- stubs ----------------------------------------------------------------

make_stubs() {
    mkdir -p "${TMP}/bin"
    cat > "${TMP}/bin/awg-quick" <<'SH'
#!/bin/bash
echo "awg-quick $*" >> "${AWG_STUB_LOG}"
if [ "${1:-}" = "up" ]; then
    echo up >> "${AWG_STUB_STATE}"
elif [ "${1:-}" = "down" ]; then
    echo down >> "${AWG_STUB_STATE}"
fi
exit 0
SH
    cat > "${TMP}/bin/ip" <<'SH'
#!/bin/bash
if [ "${1:-}" = "link" ] && [ -f "${AWG_STUB_FLAG_IP_GONE:-}" ]; then
    echo "no such interface" >&2
    exit 1
fi
exit 0
SH
    cat > "${TMP}/bin/awg" <<'SH'
#!/bin/bash
echo "awg $*" >> "${AWG_STUB_LOG}"
case "${1:-}" in
    syncconf)
        if [ -f "${AWG_STUB_FLAG_SYNCONF_FAIL:-}" ]; then
            echo "ERROR: input wireguard configuration is invalid" >&2
            exit 3
        fi
        mkdir -p "${AWG_STUB_CAPTURE:-}"
        cp "${3:-}" "${AWG_STUB_CAPTURE}/last-syncconf.conf"
        echo syncconf-ok >> "${AWG_STUB_STATE}"
        exit 0
        ;;
    show)
        if [ -f "${AWG_STUB_FLAG_UAPI_GONE:-}" ]; then
            echo "Unable to access interface" >&2
            exit 1
        fi
        exit 0
        ;;
esac
exit 0
SH
    cat > "${TMP}/bin/stat" <<'SH'
#!/bin/bash
cat "${AWG_STUB_MTIME_FILE}"
SH
    cat > "${TMP}/bin/chown" <<'SH'
#!/bin/bash
exit 0
SH
    chmod 0755 "${TMP}/bin/"*
}

STUB_LOG="${TMP}/stub.log"
STUB_STATE="${TMP}/stub.state"
STUB_CAPTURE="${TMP}/capture"
STUB_MTIME="${TMP}/mtime"
STUB_FLAG_DIR="${TMP}/flags"
mkdir -p "${STUB_CAPTURE}" "${STUB_FLAG_DIR}"

entrypoint_env() {
    local dir="$1"
    cat <<ENV
export PATH="${TMP}/bin:\$PATH"
export AWG_STUB_LOG="${STUB_LOG}"
export AWG_STUB_STATE="${STUB_STATE}"
export AWG_STUB_CAPTURE="${STUB_CAPTURE}"
export AWG_STUB_MTIME_FILE="${STUB_MTIME}"
export AWG_STUB_FLAG_IP_GONE="${STUB_FLAG_DIR}/ip-gone"
export AWG_STUB_FLAG_UAPI_GONE="${STUB_FLAG_DIR}/uapi-gone"
export AWG_STUB_FLAG_SYNCONF_FAIL="${STUB_FLAG_DIR}/syncconf-fail"
export CONFIG_SRC="${dir}/config/awg0.conf"
export CONFIG_DEST="${dir}/etc/awg0.conf"
export SYNCCONF_TMP="${dir}/syncconf.tmp"
export AWG_CONFIG_TIMEOUT=5
export AWG_CHECK_INTERVAL=1
ENV
}

run_entrypoint() {
    local dir="$1" tag="$2"
    {
        eval "$(entrypoint_env "${dir}")"
        exec bash entrypoint.sh
    } > "${TMP}/${tag}.out" 2>&1 &
    PIDS="${PIDS} $!"
}

stop_pid() {
    local pid="$1"
    kill "${pid}" 2>/dev/null
    wait "${pid}" 2>/dev/null
    echo "${?}" > /dev/null
    return 0
}

wait_for_line() {
    local file="$1" needle="$2" n="${3:-50}"
    local i=0
    while ! grep -q "${needle}" "${file}" 2>/dev/null; do
        i=$((i + 1))
        [ "${i}" -ge "${n}" ] && return 1
        sleep 0.1
    done
    return 0
}

# =====================================================================
# 1) syntax
# =====================================================================

check "syntax: entrypoint.sh" bash -n entrypoint.sh
check "syntax: syncconf.sh" bash -n syncconf.sh

# =====================================================================
# 2) filter_syncconf_config unit tests
# =====================================================================

source ./syncconf.sh

FULL_SAMPLE="${TMP}/full-sample.conf"
cat > "${FULL_SAMPLE}" <<'EOF'
[Interface]
Address = 10.8.0.1/24
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
ListenPort = 51820
DNS = 1.1.1.1
MTU = 1420
Table = off
PreUp = echo hello
PostUp = iptables -t nat -A POSTROUTING
PreDown = echo bye
PostDown = iptables -t nat -D POSTROUTING
SaveConfig = true
Jc = 3
Jmin = 1
Jmax = 5
S1 = 1
S2 = 2
S3 = 3
S4 = 4
H1 = 3-5
I1 = <t><r 4><b 0x01>
I2 = <r 8>

[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
PresharedKey = CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=
AllowedIPs = 10.8.0.2/32

[Peer]
PublicKey = DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=
AllowedIPs = 10.8.0.3/32
EOF

FILTERED="${TMP}/filtered.conf"
FILTER_OK=1
filter_syncconf_config "${FULL_SAMPLE}" > "${FILTERED}" || FILTER_OK=0
check "filter: runs without error" [ "${FILTER_OK}" = "1" ]

for key in Address DNS MTU Table PreUp PostUp PreDown PostDown SaveConfig ListenPort; do
    check "filter: drops ${key}" not grep -q "^\s*${key}\s*=" "${FILTERED}"
done

for key in PrivateKey Jc Jmin Jmax S1 S2 S3 S4 H1 I1 I2; do
    check "filter: keeps ${key}" grep -q "^\s*${key}\s*=" "${FILTERED}"
done

check "filter: keeps [Peer] sections" [ "$(grep -c '^\[Peer\]\s*$' "${FILTERED}")" = "2" ]
check "filter: keeps peer fields" grep -q "^PresharedKey = CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=$" "${FILTERED}"
check "filter: keeps allowed IPs" grep -q "^AllowedIPs = 10.8.0.3/32$" "${FILTERED}"
check "filter: preserves I1 value verbatim" grep -qx "I1 = <t><r 4><b 0x01>" "${FILTERED}"
check "filter: drops comments and blanks" [ "$(grep -c '^\s*#\|^\s*$' "${FILTERED}")" = "0" ]

VARIANTS="${TMP}/variants.conf"
cat > "${VARIANTS}" <<'EOF'
[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
address=10.8.0.1/24
dns=8.8.8.8
mtu=1500
table=off
preup=x
postup=y
predown=z
postdown=w
saveconfig=true
listenport=51820
Jc = 1
EOF
FILT_VAR="${TMP}/filtered-variants.conf"
filter_syncconf_config "${VARIANTS}" > "${FILT_VAR}"
check "filter: case-insensitive keys dropped" not grep -q "address=\|dns=\|mtu=\|table=\|preup=\|postup=\|predown=\|postdown=\|saveconfig=\|listenport=" "${FILT_VAR}"
check "filter: keeps PrivateKey/Jc in variants sample" grep -q "^PrivateKey =" "${FILT_VAR}"

# =====================================================================
# 3) behavioral tests with stubs
# =====================================================================

make_stubs

# --- 3.1 reload on mtime change; filtered config reaches syncconf ----
DIR_A="${TMP}/flow-a"
mkdir -p "${DIR_A}/config" "${DIR_A}/etc"
printf '100\n' > "${STUB_MTIME}"

cat > "${DIR_A}/config/awg0.conf" <<'EOF'
[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.8.0.1/24
ListenPort = 51820
DNS = 1.1.1.1
Jc = 3
S1 = 1

[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
AllowedIPs = 10.8.0.2/32
EOF

run_entrypoint "${DIR_A}" "flow-a"
PID_A=$!

check "flow-a: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
check "flow-a: interface copy installed before up" \
    grep -q '^PrivateKey' "${DIR_A}/etc/awg0.conf"

sleep 1.3
check "flow-a: no syncconf while mtime unchanged" not grep -q "syncconf" "${STUB_LOG}"

sed -i '' 's/10.8.0.2\/32/10.8.0.9\/32/' "${DIR_A}/config/awg0.conf" 2>/dev/null \
    || sed -i 's/10.8.0.2\/32/10.8.0.9\/32/' "${DIR_A}/config/awg0.conf"
printf '200\n' > "${STUB_MTIME}"

check "flow-a: syncconf invoked after mtime change" \
    wait_for_line "${STUB_LOG}" "syncconf awg0"

CAPTURED="${STUB_CAPTURE}/last-syncconf.conf"
check "flow-a: syncconf input captured" [ -f "${CAPTURED}" ]
check "flow-a: quick-only keys absent from syncconf input" \
    not grep -q "^\s*\(Address\|DNS\|ListenPort\)\s*=" "${CAPTURED}"
check "flow-a: AWG params present in syncconf input" \
    grep -q "^Jc = 3$\|^S1 = 1$" "${CAPTURED}"
check "flow-a: [Peer] present in syncconf input" grep -q "^\[Peer\]$" "${CAPTURED}"
check "flow-a: updated AllowedIPs in syncconf input" grep -q "^AllowedIPs = 10.8.0.9/32$" "${CAPTURED}"
check "flow-a: PresharedKey absent when config has none" \
    not grep -q "^PresharedKey" "${CAPTURED}"
check "flow-a: syncconf temp file removed" [ ! -f "${DIR_A}/syncconf.tmp" ]
check "flow-a: runtime copy refreshed" grep -q "10.8.0.9/32" "${DIR_A}/etc/awg0.conf"

sleep 1.3
N_SYNC=$(grep -c "syncconf awg0" "${STUB_LOG}" || true)
check "flow-a: no repeated syncconf after mtime refresh" [ "${N_SYNC}" = "1" ]

kill -TERM "${PID_A}" 2>/dev/null
wait "${PID_A}"; RC_A=$?
check "flow-a: SIGTERM exits 0" [ "${RC_A}" = "0" ]
check "flow-a: awg-quick down on signal" wait_for_line "${STUB_STATE}" "^down$"
check "flow-a: no secret in logs" not grep -q "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" "${TMP}/flow-a.out"

# --- 3.2 syncconf failure → exit 1 -----------------------------------
DIR_B="${TMP}/flow-b"
mkdir -p "${DIR_B}/config" "${DIR_B}/etc"
printf '100\n' > "${STUB_MTIME}"
cp "${DIR_A}/config/awg0.conf" "${DIR_B}/config/awg0.conf"

: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_B}" "flow-b"
PID_B=$!

check "flow-b: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 1.2
printf '300\n' > "${STUB_MTIME}"
touch "${STUB_FLAG_DIR}/syncconf-fail"

wait "${PID_B}"; RC_B=$?
check "flow-b: syncconf failure exits 1" [ "${RC_B}" = "1" ]
check "flow-b: failure logged" grep -q "syncconf awg0 failed" "${TMP}/flow-b.out"

# --- 3.3 missing config → timeout exit 1 (M1 contract preserved) ------
DIR_C="${TMP}/flow-c"
mkdir -p "${DIR_C}/config" "${DIR_C}/etc"
: > "${STUB_LOG}"; : > "${STUB_STATE}"
{
    eval "$(entrypoint_env "${DIR_C}")"
    AWG_CONFIG_TIMEOUT=3 bash entrypoint.sh
} > "${TMP}/flow-c.out" 2>&1 &
PIDS="${PIDS} $!"
PID_C=$!

wait "${PID_C}"; RC_C=$?
check "flow-c: missing config times out with exit 1" [ "${RC_C}" = "1" ]
check "flow-c: timeout message" grep -q "did not appear within 3s" "${TMP}/flow-c.out"

# --- 3.4 UAPI health check failure → exit 1 (M1 contract preserved) ----
DIR_D="${TMP}/flow-d"
mkdir -p "${DIR_D}/config" "${DIR_D}/etc"
printf '100\n' > "${STUB_MTIME}"
cp "${DIR_A}/config/awg0.conf" "${DIR_D}/config/awg0.conf"

: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_D}" "flow-d"
PID_D=$!

check "flow-d: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 1.2
touch "${STUB_FLAG_DIR}/uapi-gone"

wait "${PID_D}"; RC_D=$?
check "flow-d: UAPI failure exits 1" [ "${RC_D}" = "1" ]
check "flow-d: UAPI failure message" grep -q "not responding" "${TMP}/flow-d.out"

# --- 3.5 interface gone → exit 1 (M1 contract preserved) ---------------
DIR_E="${TMP}/flow-e"
mkdir -p "${DIR_E}/config" "${DIR_E}/etc"
printf '100\n' > "${STUB_MTIME}"
cp "${DIR_A}/config/awg0.conf" "${DIR_E}/config/awg0.conf"

: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_E}" "flow-e"
PID_E=$!

check "flow-e: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 1.2
touch "${STUB_FLAG_DIR}/ip-gone"

wait "${PID_E}"; RC_E=$?
check "flow-e: interface gone exits 1" [ "${RC_E}" = "1" ]
check "flow-e: interface gone message" grep -q "interface awg0 is gone" "${TMP}/flow-e.out"

# =====================================================================
echo
echo "passed: ${PASSED} failed: ${FAILED}"
[ "${FAILED}" = "0" ] || exit 1
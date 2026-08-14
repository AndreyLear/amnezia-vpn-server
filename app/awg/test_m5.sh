#!/bin/bash
# M5 tests for the status producer/consumer harness.
#
# 1) Producer binary tests: real Go awgstatus + stubbed `awg`:
#    happy path (dump → status.json), dump exit != 0 → has_interface
#    false, usage errors, secret discard, placeholders, IPv6 endpoint,
#    mode 0600, no temp files, no dump echo.
# 2) Entrypoint integration with stub binaries: status generated at
#    loop entry and every tick; config reload → the new runtime state is
#    reflected in status.json on the next tick; UAPI death exits 1
#    before the producer runs (M3.2 preserved; no stale rewrite).
# 3) Concurrency: two producers + a hot reader → no corrupted/partial
#    file, no temp leftovers.
#
# Runs on plain bash (macOS or Linux); no root, no Docker. Requires a
# local Go toolchain to build the producer binary.
set -u

cd "$(dirname "$0")"

PASSED=0
FAILED=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/awg-m5-test.XXXXXX")"
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

# ---- build the real producer binary -------------------------------------
# A local Go toolchain is preferred; when absent (e.g. the VPS), the
# producer is built in the versions.lock-pinned golang image (T-107).

M5_REPO="$(cd ../.. && pwd)"

if ! go version >/dev/null 2>&1; then
    # shellcheck disable=SC1091
    set -a; . "${M5_REPO}/versions.lock"; set +a
    mkdir -p "${TMP}/bin"
    if ! docker run --rm \
        -v "${M5_REPO}/app/panel:/work" \
        -w /work \
        -e CGO_ENABLED=0 \
        "golang:${GO_VERSION}" \
        go build -o /work/awgstatus ./cmd/awgstatus 2>/dev/null; then
        echo "FAIL: awgstatus build (containerized go)" >&2
        exit 1
    fi
    mv "${M5_REPO}/app/panel/awgstatus" "${TMP}/bin/awgstatus"
else
    mkdir -p "${TMP}/bin"
    if ! (cd ../panel && CGO_ENABLED=0 go build -o "${TMP}/bin/awgstatus" ./cmd/awgstatus); then
        echo "FAIL: awgstatus build" >&2
        exit 1
    fi
fi
PASSED=$((PASSED + 1))
echo "PASS: awgstatus builds (CGO_ENABLED=0)"

# ---- markers and canned dumps -------------------------------------------

IFACE_PUB="iface-public-key-AA"
MARKED_PRIV="MARKED-PRIVATE-KEY"
PEER_A="AAAA-peer-a-public-key"
PEER_B="BBBB-peer-b-public-key"
PEER_C="CCCC-peer-c-public-key"
PEER_Z="ZZZZ-peer-z-public-key"
MARKED_PSK="MARKED-PRESHARED-KEY"
IPV6_EP="[fd00:1::10]:51820"

# dump v1: interface + 3 peers (B, A, Z — deliberately unsorted, Z has
# (none) placeholders), IPv6 endpoint, last_handshake 0 and nonzero.
dump_v1() {
    printf '%s\t%s\t51820\t3\t21\t31\t904\t737\t0\t0\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\toff\n' \
        "${MARKED_PRIV}" "${IFACE_PUB}"
    printf '%s\t%s\t%s\t10.8.0.3/32\t1754299145\t100\t200\toff\n' \
        "${PEER_B}" "${MARKED_PSK}" "198.51.100.5:51820"
    printf '%s\t%s\t%s\t10.8.0.2/32,fd00::2/128\t0\t10\t20\t25\n' \
        "${PEER_A}" "${MARKED_PSK}" "${IPV6_EP}"
    printf '%s\t%s\t(none)\t(none)\t0\t0\t0\toff\n' \
        "${PEER_Z}" "${MARKED_PSK}"
}

# dump v2: interface + peer C (simulates the runtime state right after a
# reload adding peer C and dropping A/B).
dump_v2() {
    printf '%s\t%s\t51820\t3\t21\t31\t904\t737\t0\t0\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\toff\n' \
        "${MARKED_PRIV}" "${IFACE_PUB}"
    printf '%s\t%s\t%s\t10.8.0.4/32\t0\t7\t8\toff\n' \
        "${PEER_C}" "${MARKED_PSK}" "203.0.113.9:51820"
}

# ---- stubs ---------------------------------------------------------------

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
case "${1:-}-${2:-}-${3:-}" in
    syncconf-*)
        if [ -f "${AWG_STUB_FLAG_SYNCONF_FAIL:-}" ]; then
            echo "ERROR: input wireguard configuration is invalid" >&2
            exit 3
        fi
        mkdir -p "${AWG_STUB_CAPTURE:-}"
        cp "${3:-}" "${AWG_STUB_CAPTURE}/last-syncconf.conf"
        echo syncconf-ok >> "${AWG_STUB_STATE}"
        exit 0
        ;;
    show-*-dump)
        if [ -f "${AWG_STUB_FLAG_UAPI_GONE:-}" ]; then
            echo "Unable to access interface" >&2
            exit 1
        fi
        cat "${AWG_STUB_DUMP_FILE:-}"
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
STUB_DUMP="${TMP}/dump"
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
export AWG_STUB_DUMP_FILE="${STUB_DUMP}"
export CONFIG_SRC="${dir}/config/awg0.conf"
export CONFIG_DEST="${dir}/etc/awg0.conf"
export SYNCCONF_TMP="${dir}/syncconf.tmp"
export AWG_CONFIG_TIMEOUT=5
export AWG_CHECK_INTERVAL=1
export AWGSTATUS_BIN="${TMP}/bin/awgstatus"
export AWG_STATUS_FILE="${dir}/status/status.json"
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

wait_for_file() {
    local file="$1" n="${2:-50}"
    local i=0
    while [ ! -f "${file}" ]; do
        i=$((i + 1))
        [ "${i}" -ge "${n}" ] && return 1
        sleep 0.1
    done
    return 0
}

# read_status <file>: prints the file without the trailing newline.
read_status() {
    tr -d '\n' < "$1"
}

# =====================================================================
# 1) syntax
# =====================================================================

check "syntax: entrypoint.sh" bash -n entrypoint.sh

# =====================================================================
# 2) producer binary behavior
# =====================================================================

make_stubs
dump_v1 > "${STUB_DUMP}"
export AWG_STUB_DUMP_FILE="${STUB_DUMP}"
export AWG_STUB_FLAG_UAPI_GONE="${STUB_FLAG_DIR}/uapi-gone"
PRODUCER="${TMP}/bin/awgstatus"

# --- happy path ---
STATUS_A="${TMP}/status-a.json"
OUT_A="$(PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${STATUS_A}" 2>"${TMP}/producer-a.err")"
RC_A=$?
check "producer: exit 0 on happy path" [ "${RC_A}" = "0" ]
check "producer: silent on success (no stdout)" [ -z "${OUT_A}" ]
check "producer: silent on success (no stderr)" [ ! -s "${TMP}/producer-a.err" ]
check "producer: status.json exists" [ -f "${STATUS_A}" ]
check "producer: mode 0600" [ "$(stat -c %a "${STATUS_A}" 2>/dev/null || stat -f %Lp "${STATUS_A}")" = "600" ]
check "producer: schema v1" grep -q '"schema":"v1"' "${STATUS_A}"
check "producer: has_interface true" grep -q '"has_interface":true' "${STATUS_A}"
check "producer: iface name" grep -q '"iface":"awg0"' "${STATUS_A}"
check "producer: public key present" grep -q "\"public_key\":\"${IFACE_PUB}\"" "${STATUS_A}"
check "producer: awg_params kept jc" grep -q '"jc":3' "${STATUS_A}"
check "producer: awg_params excludes h1-i5" not grep -q '"h1"\|"i1"\|"h2"\|"i2"' "${STATUS_A}"
check "producer: peers present (1 iface key + 3 peers)" \
    [ "$(grep -o '"public_key":' "${STATUS_A}" | wc -l | tr -d ' ')" = "4" ]
check "producer: peers sorted by public_key" \
    grep -q "\"peers\":\[{\"public_key\":\"${PEER_A}\".*\"public_key\":\"${PEER_B}\".*\"public_key\":\"${PEER_Z}\"" "${STATUS_A}"
check "producer: IPv6 endpoint preserved" grep -F -q "\"endpoint\":\"${IPV6_EP}\"" "${STATUS_A}"
check "producer: endpoint (none) placeholder" grep -q '"endpoint":"(none)"' "${STATUS_A}"
check "producer: allowed_ips (none) -> []" grep -q '"allowed_ips":\[\]' "${STATUS_A}"
check "producer: last_handshake 0 -> null" grep -q '"last_handshake_utc":null' "${STATUS_A}"
check "producer: nonzero handshake ISO8601" grep -q '"last_handshake_utc":"2025-08-04T09:19:05Z"' "${STATUS_A}"
check "producer: keepalive off + number kept" grep -q '"persistent_keepalive":"off"' "${STATUS_A}"
check "producer: fwmark off kept" grep -q '"fwmark":"off"' "${STATUS_A}"
check "producer: allowed_ips split" grep -q '"allowed_ips":\["10.8.0.2/32","fd00::2/128"\]' "${STATUS_A}"
check "producer: private_key discarded" not grep -q "${MARKED_PRIV}" "${STATUS_A}"
check "producer: preshared_key discarded" not grep -q "${MARKED_PSK}" "${STATUS_A}"
check "producer: no key-material field names" not grep -q 'private_key\|preshared_key' "${STATUS_A}"
check "producer: no temp file left" not ls "${TMP}/status-a.json.tmp-*" 2>/dev/null

# --- dump exit != 0 → has_interface=false, exit 0 ---
STATUS_B="${TMP}/status-b.json"
touch "${STUB_FLAG_DIR}/uapi-gone"
PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${STATUS_B}" >/dev/null 2>&1
RC_B=$?
rm -f "${STUB_FLAG_DIR}/uapi-gone"
check "producer: dump failure still exits 0" [ "${RC_B}" = "0" ]
check "producer: dump failure -> interface null" grep -q '"interface":null' "${STATUS_B}"
check "producer: dump failure -> peers []" grep -q '"peers":\[\]' "${STATUS_B}"
check "producer: dump failure -> schema v1" grep -q '"schema":"v1"' "${STATUS_B}"
check "producer: dump failure -> mode 0600" [ "$(stat -c %a "${STATUS_B}" 2>/dev/null || stat -f %Lp "${STATUS_B}")" = "600" ]

# --- interface-only dump → peers [] ---
STATUS_C="${TMP}/status-c.json"
printf '%s\t%s\t51820\t3\t21\t31\t904\t737\t0\t0\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\toff\n' \
    "${MARKED_PRIV}" "${IFACE_PUB}" > "${STUB_DUMP}"
PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${STATUS_C}" >/dev/null 2>&1
check "producer: iface-only dump -> peers []" grep -q '"peers":\[\]' "${STATUS_C}"

# --- usage ---
PRODUCER_OUT="$(PATH="${TMP}/bin:$PATH" "${PRODUCER}" 2>&1 >/dev/null)"
check "producer: usage error exits 1" [ "$?" = "1" ]
check "producer: usage mentions args" printf '%s' "${PRODUCER_OUT}" | grep -q "usage: awgstatus"

# --- determinism: two runs 1s apart → identical except timestamp ---
STATUS_D1="${TMP}/status-d1.json"
STATUS_D2="${TMP}/status-d2.json"
dump_v1 > "${STUB_DUMP}"
PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${STATUS_D1}" >/dev/null 2>&1
sleep 1.1
PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${STATUS_D2}" >/dev/null 2>&1
gen1="$(sed -E 's/"generated_at_utc":"[^"]+"/"generated_at_utc":"<t>"/g' "${STATUS_D1}")"
gen2="$(sed -E 's/"generated_at_utc":"[^"]+"/"generated_at_utc":"<t>"/g' "${STATUS_D2}")"
check "producer: repeated runs identical except timestamp" [ "${gen1}" = "${gen2}" ]
raw1="$(tr -d '\n' < "${STATUS_D1}")"
raw2="$(tr -d '\n' < "${STATUS_D2}")"
check "producer: timestamps differ across runs" [ "${raw1}" != "${raw2}" ]

# =====================================================================
# 3) concurrency: 2 producers + hot reader
# =====================================================================

CONC_STATUS="${TMP}/conc/status.json"
mkdir -p "${TMP}/conc"
# two distinct dump inputs so concurrent writers produce conflicting content
CONC_A="${TMP}/dump-a"
CONC_B="${TMP}/dump-b"
dump_v1 > "${CONC_A}"
dump_v2 > "${CONC_B}"

READER_ERR="${TMP}/reader.err"
: > "${READER_ERR}"
(
    for i in $(seq 1 600); do
        if [ -f "${CONC_STATUS}" ]; then
            first_char="$(head -c 1 "${CONC_STATUS}")"
            last_char="$(tail -c 1 "${CONC_STATUS}")"
            if [ "${first_char}" != "{" ] || [ "${last_char}" != "}" ]; then
                echo "partial file (first=[${first_char}] last=[${last_char}]) at iteration $i" >> "${READER_ERR}"
                cp "${CONC_STATUS}" "${TMP}/reader-caught.json" 2>/dev/null
                break
            fi
            if ! grep -q '"schema":"v1"' "${CONC_STATUS}"; then
                echo "corrupt file at iteration $i" >> "${READER_ERR}"
                cp "${CONC_STATUS}" "${TMP}/reader-caught.json" 2>/dev/null
                break
            fi
            # every observation must be one complete payload: either the
            # dump-A run (peers A/B/Z) or the dump-B run (peer C), never
            # a mix or a fragment
            if grep -q "${PEER_A}\|${PEER_B}\|${PEER_Z}" "${CONC_STATUS}" \
                && grep -q "${PEER_C}" "${CONC_STATUS}"; then
                echo "mixed contents at iteration $i" >> "${READER_ERR}"
                cp "${CONC_STATUS}" "${TMP}/reader-caught.json" 2>/dev/null
                break
            fi
            if ! grep -q "${PEER_A}\|${PEER_B}\|${PEER_Z}\|${PEER_C}" "${CONC_STATUS}"; then
                echo "unknown contents at iteration $i" >> "${READER_ERR}"
                cp "${CONC_STATUS}" "${TMP}/reader-caught.json" 2>/dev/null
                break
            fi
        fi
        sleep 0.005
    done
) &
PIDS="${PIDS} $!"
READER_PID=$!

WRITER_A="${TMP}/writer-a.log"
WRITER_B="${TMP}/writer-b.log"
(
    for i in $(seq 1 25); do
        cp "${CONC_A}" "${STUB_DUMP}"
        PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${CONC_STATUS}" >/dev/null 2>&1 || true
    done
) >/dev/null 2>&1 &
WR_A=$!
(
    for i in $(seq 1 25); do
        cp "${CONC_B}" "${STUB_DUMP}"
        PATH="${TMP}/bin:$PATH" "${PRODUCER}" awg0 "${CONC_STATUS}" >/dev/null 2>&1 || true
    done
) >/dev/null 2>&1 &
WR_B=$!

wait "${WR_A}" "${WR_B}" 2>/dev/null
kill "${READER_PID}" 2>/dev/null
wait "${READER_PID}" 2>/dev/null
PIDS=""

check "concurrency: reader never saw a partial/corrupt file" [ ! -s "${READER_ERR}" ]
check "concurrency: final file valid" grep -q '"schema":"v1"' "${CONC_STATUS}"
check "concurrency: mode 0600" [ "$(stat -c %a "${CONC_STATUS}" 2>/dev/null || stat -f %Lp "${CONC_STATUS}")" = "600" ]
check "concurrency: no temp files left" not ls "${TMP}/conc/"*.tmp-* 2>/dev/null

# =====================================================================
# 4) entrypoint integration
# =====================================================================

dump_v1 > "${STUB_DUMP}"

# --- 4.1 status at loop entry, every tick, and after reload ------------
DIR_A="${TMP}/flow-a"
mkdir -p "${DIR_A}/config" "${DIR_A}/etc" "${DIR_A}/status"
printf '100\n' > "${STUB_MTIME}"

cat > "${DIR_A}/config/awg0.conf" <<'EOF'
[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.8.0.1/24
ListenPort = 51820
Jc = 3
S1 = 904

[Peer]
PublicKey = AAAA-peer-a-public-key
AllowedIPs = 10.8.0.2/32
EOF

STATUS_A_FILE="${DIR_A}/status/status.json"
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_A}" "flow-a"
PID_A=$!

check "flow-a: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
check "flow-a: status.json generated at loop entry" wait_for_file "${STATUS_A_FILE}"
check "flow-a: status has_interface true" grep -q '"has_interface":true' "${STATUS_A_FILE}"
check "flow-a: secret keys not in status.json" not grep -q "${MARKED_PRIV}\|${MARKED_PSK}" "${STATUS_A_FILE}"

TS1="$(grep -o '"generated_at_utc":"[^"]*"' "${STATUS_A_FILE}" | cut -d'"' -f4)"
sleep 1.5
TS2="$(grep -o '"generated_at_utc":"[^"]*"' "${STATUS_A_FILE}" | cut -d'"' -f4)"
check "flow-a: status regenerated every tick (timestamp advances)" [ "${TS1}" != "${TS2}" ] ||
    { echo "flow-a: generated_at_utc did not advance: ${TS1} vs ${TS2}" >&2; cat "${STATUS_A_FILE}" >&2; }

dump_v2 > "${STUB_DUMP}"
sed -i '' 's/AAAA-peer-a-public-key/CCCC-peer-c-public-key/' "${DIR_A}/config/awg0.conf" 2>/dev/null \
    || sed -i 's/AAAA-peer-a-public-key/CCCC-peer-c-public-key/' "${DIR_A}/config/awg0.conf"
printf '200\n' > "${STUB_MTIME}"

check "flow-a: syncconf invoked after config change" \
    wait_for_line "${STUB_LOG}" "syncconf awg0"
check "flow-a: new peer reaches status.json on the next tick" \
    wait_for_line "${STATUS_A_FILE}" "${PEER_C}"
check "flow-a: old peer gone from status.json" not grep -q "${PEER_A}\|${PEER_B}" "${STATUS_A_FILE}"
sleep 1.2
N_SYNC=$(grep -c "syncconf awg0" "${STUB_LOG}" || true)
check "flow-a: syncconf ran exactly once (no reload storms)" [ "${N_SYNC}" = "1" ]

kill -TERM "${PID_A}" 2>/dev/null
wait "${PID_A}"; RC_A=$?
check "flow-a: SIGTERM exits 0 (M3.2 unchanged)" [ "${RC_A}" = "0" ]
check "flow-a: awg-quick down on signal" wait_for_line "${STUB_STATE}" "^down$"
check "flow-a: no secret in container logs" not grep -q "${MARKED_PRIV}\|${MARKED_PSK}\|${IFACE_PUB}" "${TMP}/flow-a.out"

# --- 4.2 UAPI death → exit 1 before the producer runs (M3.2 order) -----
DIR_B="${TMP}/flow-b"
mkdir -p "${DIR_B}/config" "${DIR_B}/etc" "${DIR_B}/status"
printf '100\n' > "${STUB_MTIME}"
cp "${DIR_A}/config/awg0.conf" "${DIR_B}/config/awg0.conf"

STATUS_B_FILE="${DIR_B}/status/status.json"
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_B}" "flow-b"
PID_B=$!

check "flow-b: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
check "flow-b: first status.json generated" wait_for_file "${STATUS_B_FILE}"
sleep 1.5
T_B1="$(sed -E 's/"generated_at_utc":"[^"]+"/"generated_at_utc":"<t>"/g' "${STATUS_B_FILE}")"
touch "${STUB_FLAG_DIR}/uapi-gone"

wait "${PID_B}"; RC_B=$?
check "flow-b: UAPI failure exits 1 (M3.2 preserved)" [ "${RC_B}" = "1" ]
check "flow-b: UAPI failure message" grep -q "not responding" "${TMP}/flow-b.out"
T_B2="$(sed -E 's/"generated_at_utc":"[^"]+"/"generated_at_utc":"<t>"/g' "${STATUS_B_FILE}")"
check "flow-b: status.json not rewritten while UAPI dead" [ "${T_B1}" = "${T_B2}" ]

# =====================================================================
echo
echo "passed: ${PASSED} failed: ${FAILED}"
[ "${FAILED}" = "0" ] || exit 1
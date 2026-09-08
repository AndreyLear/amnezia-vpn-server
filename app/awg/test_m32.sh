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
    # ip держит игрушечную таблицу маршрутов в файле: проверка «маршрут
    # принял размер» есть в самом entrypoint, и заглушка, которая на show
    # молчит, проверяла бы не то (amnezia-vpn-server-wc2l).
    cat > "${TMP}/bin/ip" <<'SH'
#!/bin/bash
echo "ip $*" >> "${AWG_STUB_LOG}"
if [ "${1:-}" = "link" ] && [ "${2:-}" = "show" ] && [ -f "${AWG_STUB_FLAG_IP_GONE:-}" ]; then
    echo "no such interface" >&2
    exit 1
fi
TABLE="${AWG_STUB_ROUTES:-/dev/null}"
[ -f "$TABLE" ] || : > "$TABLE"
# Семейство роли не играет: адрес сам себя различает.
args=()
for a in "$@"; do [ "$a" = "-6" ] || args+=("$a"); done
set -- "${args[@]}"
case "${1:-}:${2:-}" in
    route:replace)
        [ -f "${AWG_STUB_FLAG_ROUTE_FAIL:-}" ] && exit 2
        cidr="$3"; mtu=""
        while [ "$#" -gt 0 ]; do [ "$1" = "mtu" ] && mtu="$2"; shift; done
        grep -v "^${cidr} " "$TABLE" > "$TABLE.new" 2>/dev/null || : > "$TABLE.new"
        printf '%s %s\n' "$cidr" "$mtu" >> "$TABLE.new"
        mv "$TABLE.new" "$TABLE"
        exit 0
        ;;
    route:show)
        cidr="$3"
        line="$(grep "^${cidr} " "$TABLE" 2>/dev/null | head -1)" || true
        [ -n "$line" ] || exit 0
        printf '%s dev awg0 mtu %s\n' "${line%% *}" "${line##* }"
        exit 0
        ;;
    route:del)
        cidr="$3"
        grep -q "^${cidr} " "$TABLE" 2>/dev/null || exit 2
        grep -v "^${cidr} " "$TABLE" > "$TABLE.new" 2>/dev/null || : > "$TABLE.new"
        mv "$TABLE.new" "$TABLE"
        exit 0
        ;;
esac
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
    # tc записывает вызовы и держит игрушечное состояние: «дисциплина стоит
    # или нет». Проверка «очередь не заведена, когда никому не задан предел»
    # иначе проверяла бы заглушку, а не entrypoint (amnezia-vpn-server-jzzu).
    cat > "${TMP}/bin/tc" <<'SH'
#!/bin/bash
echo "tc $*" >> "${AWG_STUB_LOG}"
STATE="${AWG_STUB_QDISC:-/dev/null}"
case "$1:$2" in
    qdisc:add)
        [ -f "${AWG_STUB_FLAG_TC_FAIL:-}" ] && exit 2
        echo root > "$STATE"
        ;;
    qdisc:del)
        # Настоящая tc отвечает ошибкой, когда удалять нечего. Заглушка,
        # которая всегда молчит успехом, проверяла бы замысел, а не
        # поведение: именно на этом entrypoint и падал под set -e
        # (amnezia-vpn-server-jzzu).
        [ -s "$STATE" ] || exit 2
        : > "$STATE"
        ;;
    class:add|filter:add)
        [ -f "${AWG_STUB_FLAG_TC_FAIL:-}" ] && exit 2
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
export AWG_STUB_FLAG_ROUTE_FAIL="${STUB_FLAG_DIR}/route-fail"
export AWG_STUB_FLAG_TC_FAIL="${STUB_FLAG_DIR}/tc-fail"
export AWG_STUB_QDISC="${dir}/qdisc.state"
export AWG_RATE_STATE="${dir}/rates.state"
export AWG_STUB_ROUTES="${dir}/routes.table"
export AWG_ROUTE_STATE="${dir}/routes.state"
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
        [ -n "${AWG_PROC_ROOT:-}" ] && export AWG_PROC_ROOT
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


# =====================================================================
# 2.5) amnezia-vpn-server-mmh6: IPv6 must never cost the tunnel its life
# =====================================================================
#
# awg-quick runs under set -e. On a host with IPv6 disabled, an IPv6
# address in the config makes `ip -6 address add` fail, and awg-quick
# deletes the interface it had just created — so every client loses the
# VPN, including those who never wanted IPv6. Reproduced on a live test
# server before this guard existed.

# The entrypoint runs work at the top level, so its functions are lifted
# out rather than sourced.
MMH6_FUNCS="${TMP}/mmh6-funcs.sh"
sed -n '/^ipv6_usable() {/,/^install_config() {/p' entrypoint.sh | sed '$d' > "${MMH6_FUNCS}"
# shellcheck source=/dev/null
source "${MMH6_FUNCS}"

MMH6_CONF="${TMP}/mmh6.conf"
cat > "${MMH6_CONF}" <<'MMH6EOF'
[Interface]
PrivateKey = SECRET
Address = 10.8.0.1/24, fd42:a11e:c0de::1/64
ListenPort = 4500
MTU = 1340

[Peer]
PublicKey = KEY1
AllowedIPs = 10.8.0.2/32, fd42:a11e:c0de::2/128

[Peer]
PublicKey = KEY2
AllowedIPs = 10.8.0.3/32
MMH6EOF

check "mmh6: config with IPv6 is recognised" config_has_ipv6 "${MMH6_CONF}"

MMH6_V4ONLY="${TMP}/mmh6-v4.conf"
sed '/^Address/s/, fd42.*$//; /^AllowedIPs/s/, fd42.*$//' "${MMH6_CONF}" > "${MMH6_V4ONLY}"
check "mmh6: IPv4-only config is not mistaken for IPv6" not config_has_ipv6 "${MMH6_V4ONLY}"

MMH6_OUT="${TMP}/mmh6-stripped.conf"
strip_ipv6 "${MMH6_CONF}" > "${MMH6_OUT}"
check "mmh6: interface keeps its IPv4 address" grep -qx "Address = 10.8.0.1/24" "${MMH6_OUT}"
check "mmh6: peer keeps its IPv4 allowed-ip" grep -qx "AllowedIPs = 10.8.0.2/32" "${MMH6_OUT}"
check "mmh6: an IPv4-only peer is left alone" grep -qx "AllowedIPs = 10.8.0.3/32" "${MMH6_OUT}"
check "mmh6: not one IPv6 entry survives" not grep -q ":" "${MMH6_OUT}"
check "mmh6: nothing else in the file is touched" grep -qx "PrivateKey = SECRET" "${MMH6_OUT}"
check "mmh6: MTU survives" grep -qx "MTU = 1340" "${MMH6_OUT}"
check "mmh6: the file keeps its shape" [ "$(grep -c '' "${MMH6_OUT}")" = "$(grep -c '' "${MMH6_CONF}")" ]

# The decision function, against a fake /proc. Unknown must read as
# unusable: guessing wrong that way costs the tunnel its IPv6, guessing
# wrong the other way costs the tunnel its existence.
mmh6_proc() { # mmh6_proc all default [--no-inet6]
    local root="${TMP}/proc-$1-$2-${3:-x}"
    mkdir -p "${root}/proc/net" "${root}/proc/sys/net/ipv6/conf/all" "${root}/proc/sys/net/ipv6/conf/default"
    [ "${3:-}" = "--no-inet6" ] || : > "${root}/proc/net/if_inet6"
    printf '%s\n' "$1" > "${root}/proc/sys/net/ipv6/conf/all/disable_ipv6"
    printf '%s\n' "$2" > "${root}/proc/sys/net/ipv6/conf/default/disable_ipv6"
    printf '%s' "${root}"
}
check "mmh6: healthy host is usable"          env AWG_PROC_ROOT="$(mmh6_proc 0 0)" bash -c "source '${MMH6_FUNCS}'; ipv6_usable"
check "mmh6: all.disable_ipv6=1 is unusable"  not env AWG_PROC_ROOT="$(mmh6_proc 1 0)" bash -c "source '${MMH6_FUNCS}'; ipv6_usable"
check "mmh6: default.disable_ipv6=1 is unusable" not env AWG_PROC_ROOT="$(mmh6_proc 0 1)" bash -c "source '${MMH6_FUNCS}'; ipv6_usable"
check "mmh6: a kernel without IPv6 is unusable"  not env AWG_PROC_ROOT="$(mmh6_proc 0 0 --no-inet6)" bash -c "source '${MMH6_FUNCS}'; ipv6_usable"
check "mmh6: an unreadable /proc reads as unusable" not env AWG_PROC_ROOT="${TMP}/nowhere" bash -c "source '${MMH6_FUNCS}'; ipv6_usable"

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

# Ждём завершения, а не начала. Подделка пишет строку в журнал ПЕРВОЙ, до
# того как скопирует поданный ей конфиг, поэтому ожидание журнала возвращалось
# раньше, чем появлялся файл, и следующая проверка падала примерно раз из пяти
# (amnezia-vpn-server-bfy3). Отметка syncconf-ok пишется ПОСЛЕ копирования и
# означает «сделано» — её и ждём.
check "flow-a: syncconf invoked after mtime change" \
    wait_for_line "${STUB_STATE}" "^syncconf-ok$"

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

# --- 3.6 маршруты с собственным размером (amnezia-vpn-server-wc2l) -----
#
# Интерфейс один на всех, поэтому размер клиенту задаёт маршрут. Проверяем,
# что план разошёлся, переживает горячую перезагрузку и что маршрут ушедшего
# клиента снимается вместе с ним.
DIR_F="${TMP}/flow-f"
mkdir -p "${DIR_F}/config" "${DIR_F}/etc"
printf '100\n' > "${STUB_MTIME}"
cat > "${DIR_F}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24, fded:a0b:d921::1/64
ListenPort = 51820
MTU = 1440
# amnezia-route-mtu = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32, fded:a0b:d921::2/128

[Peer]
PublicKey = bbb
AllowedIPs = 10.8.0.3/32, fded:a0b:d921::3/128
# amnezia-route-mtu = 1420
CONF

: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
# Хост с исправным IPv6: иначе install_config вырежет из конфигурации все
# адреса v6, и проверять маршруты для них будет не на чем.
AWG_PROC_ROOT="$(mmh6_proc 0 0)" run_entrypoint "${DIR_F}" "flow-f"
PID_F=$!
check "flow-f: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 0.6

ROUTES_F="${DIR_F}/routes.table"
# Клиент без своего размера получает общий — то же, что и до этой
# возможности, только теперь маршрутом, а не интерфейсом.
check "flow-f: обычный клиент получает общий размер" \
    grep -qx "10.8.0.2/32 1340" "${ROUTES_F}"
check "flow-f: и по IPv6 тоже" \
    grep -qx "fded:a0b:d921::2/128 1340" "${ROUTES_F}"
# Клиент со своим размером получает свой, в обе стороны.
check "flow-f: свой размер доходит до маршрута" \
    grep -qx "10.8.0.3/32 1420" "${ROUTES_F}"
check "flow-f: и по IPv6 тоже" \
    grep -qx "fded:a0b:d921::3/128 1420" "${ROUTES_F}"

# Горячая перезагрузка: клиент bbb ушёл, его маршруты обязаны уйти с ним,
# иначе они молча ограничат чужой адрес, когда тот выдадут заново.
cat > "${DIR_F}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24, fded:a0b:d921::1/64
ListenPort = 51820
MTU = 1440
# amnezia-route-mtu = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32, fded:a0b:d921::2/128
CONF
printf '200\n' > "${STUB_MTIME}"
check "flow-f: перезагрузка прошла" wait_for_line "${STUB_STATE}" "^syncconf-ok$"
sleep 0.6
check "flow-f: маршрут ушедшего клиента снят" \
    not grep -q "^10.8.0.3/32 " "${ROUTES_F}"
check "flow-f: и его IPv6 тоже" \
    not grep -q "^fded:a0b:d921::3/128 " "${ROUTES_F}"
check "flow-f: оставшийся клиент маршрут сохранил" \
    grep -qx "10.8.0.2/32 1340" "${ROUTES_F}"
# Потолок ставит awg-quick при подъёме, но горячая перезагрузка до него не
# доходит: MTU — ключ awg-quick, и до syncconf он не долетает.
check "flow-f: потолок устройства выставлен" \
    grep -q "ip link set dev awg0 mtu 1440" "${STUB_LOG}"
stop_pid "${PID_F}"

# --- 3.7 маршруты не разошлись → интерфейс опускается до осторожного ---
# Интерфейс теперь не осторожный, и клиент без маршрута получил бы пакеты
# крупнее, чем тянет его последняя миля. Потерять выигрыш лучше, чем связь.
DIR_G="${TMP}/flow-g"
mkdir -p "${DIR_G}/config" "${DIR_G}/etc"
printf '100\n' > "${STUB_MTIME}"
cp "${DIR_F}/config/awg0.conf" "${DIR_G}/config/awg0.conf"
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
touch "${STUB_FLAG_DIR}/route-fail"
run_entrypoint "${DIR_G}" "flow-g"
PID_G=$!
check "flow-g: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
check "flow-g: неудача маршрутов замечена" \
    wait_for_line "${TMP}/flow-g.out" "маршруты не разошлись"
check "flow-g: интерфейс опущен до осторожного значения" \
    grep -q "ip link set dev awg0 mtu 1340" "${STUB_LOG}"
check "flow-g: туннель при этом жив" not grep -q "^down$" "${STUB_STATE}"
stop_pid "${PID_G}"

# --- 3.8 развёртывание без потолка: маршруты не появляются -------------
# Там, где потолок не измеряли, всё обязано остаться ровно как было.
DIR_H="${TMP}/flow-h"
mkdir -p "${DIR_H}/config" "${DIR_H}/etc"
printf '100\n' > "${STUB_MTIME}"
cat > "${DIR_H}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24
ListenPort = 51820
MTU = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32
CONF
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_H}" "flow-h"
PID_H=$!
check "flow-h: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 0.6
check "flow-h: ни одного маршрута не поставлено" \
    not grep -q "route replace" "${STUB_LOG}"
stop_pid "${PID_H}"

# --- 3.9 старое развёртывание, но у клиента свой размер ----------------
# Потолка нет, общего значения нет — а собственное у клиента есть, и оно
# обязано доехать до маршрута, иначе настройка тихо не работает.
DIR_I="${TMP}/flow-i"
mkdir -p "${DIR_I}/config" "${DIR_I}/etc"
printf '100\n' > "${STUB_MTIME}"
cat > "${DIR_I}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24
ListenPort = 51820
MTU = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32

[Peer]
PublicKey = bbb
AllowedIPs = 10.8.0.3/32
# amnezia-route-mtu = 1300
CONF
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_I}" "flow-i"
PID_I=$!
check "flow-i: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 0.6
check "flow-i: свой размер доехал до маршрута" \
    grep -qx "10.8.0.3/32 1300" "${DIR_I}/routes.table"
# А тому, у кого своего нет, маршрут не нужен: интерфейс и так осторожный.
check "flow-i: остальным маршрут не выписан" \
    not grep -q "^10.8.0.2/32 " "${DIR_I}/routes.table"
stop_pid "${PID_I}"

# --- 3.10 предел скорости на клиента (amnezia-vpn-server-jzzu) ---------
#
# Плечо до клиента может терять пакеты под нагрузкой; предел вдвое сокращает
# потери при той же полезной скорости. Проверяем, что он ставится тому, кому
# задан, снимается вместе с ним и НЕ заводит очередь там, где никому ничего не
# задано: awg0 живёт с noqueue, и менять это ради никого нельзя.
DIR_J="${TMP}/flow-j"
mkdir -p "${DIR_J}/config" "${DIR_J}/etc"
printf '100\n' > "${STUB_MTIME}"
cat > "${DIR_J}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24
ListenPort = 51820
MTU = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32

[Peer]
PublicKey = bbb
AllowedIPs = 10.8.0.3/32
# amnezia-rate = 50
CONF
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_J}" "flow-j"
PID_J=$!
check "flow-j: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 0.6
check "flow-j: очередь заведена" grep -q "tc qdisc add dev awg0 root handle 1: htb" "${STUB_LOG}"
check "flow-j: предел ограниченному клиенту" \
    grep -q "tc class add dev awg0 parent 1: classid 1:101 htb rate 50mbit" "${STUB_LOG}"
check "flow-j: правило на его адрес" \
    grep -q "match ip dst 10.8.0.3/32" "${STUB_LOG}"
# Тому, кому ничего не задано, отдельный класс не нужен: он идёт в класс по
# умолчанию, который без предела.
check "flow-j: обычному клиенту предел не выписан" \
    not grep -q "match ip dst 10.8.0.2/32" "${STUB_LOG}"
check "flow-j: короткая очередь под классом" grep -q "fq_codel" "${STUB_LOG}"
# Туннель обязан пережить первую же попытку: очереди ещё нет, и удалять
# нечего. Настоящая tc отвечает на это ошибкой, а entrypoint живёт под
# set -e — здесь он и падал, роняя связь.
check "flow-j: туннель жив, хотя удалять было нечего" \
    not grep -q "^down$" "${STUB_STATE}"
check "flow-j: и продолжает работать" wait_for_line "${STUB_STATE}" "^up$"

# Предел снят на горячую — очередь должна уйти целиком.
cat > "${DIR_J}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24
ListenPort = 51820
MTU = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32

[Peer]
PublicKey = bbb
AllowedIPs = 10.8.0.3/32
CONF
printf '200\n' > "${STUB_MTIME}"
check "flow-j: перезагрузка прошла" wait_for_line "${STUB_STATE}" "^syncconf-ok$"
check "flow-j: снятый предел убирает очередь" \
    wait_for_line "${TMP}/flow-j.out" "пределы скорости сняты"
stop_pid "${PID_J}"

# --- 3.11 никому не задано — очередь не заводится вовсе ----------------
DIR_K="${TMP}/flow-k"
mkdir -p "${DIR_K}/config" "${DIR_K}/etc"
printf '100\n' > "${STUB_MTIME}"
cat > "${DIR_K}/config/awg0.conf" <<'CONF'
[Interface]
PrivateKey = kEY
Address = 10.8.0.1/24
ListenPort = 51820
MTU = 1340

[Peer]
PublicKey = aaa
AllowedIPs = 10.8.0.2/32
CONF
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
run_entrypoint "${DIR_K}" "flow-k"
PID_K=$!
check "flow-k: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
sleep 0.6
check "flow-k: очередь не заведена" not grep -q "tc qdisc add" "${STUB_LOG}"
check "flow-k: и не снималась зря" not grep -q "tc qdisc del" "${STUB_LOG}"
stop_pid "${PID_K}"

# --- 3.12 пределы разошлись не полностью — снимаем целиком -------------
# Полумера хуже отсутствия: часть клиентов ограничена, часть нет, и объяснить
# разницу потом нечем.
DIR_L="${TMP}/flow-l"
mkdir -p "${DIR_L}/config" "${DIR_L}/etc"
printf '100\n' > "${STUB_MTIME}"
cp "${DIR_J}/config/awg0.conf" "${DIR_L}/config/awg0.conf"
printf '# amnezia-rate = 50\n' >> "${DIR_L}/config/awg0.conf"
: > "${STUB_LOG}"; : > "${STUB_STATE}"
rm -f "${STUB_FLAG_DIR}"/*
touch "${STUB_FLAG_DIR}/tc-fail"
run_entrypoint "${DIR_L}" "flow-l"
PID_L=$!
check "flow-l: awg-quick up invoked" wait_for_line "${STUB_STATE}" "^up$"
check "flow-l: неудача замечена" \
    wait_for_line "${TMP}/flow-l.out" "пределы скорости не применены"
check "flow-l: туннель при этом жив" not grep -q "^down$" "${STUB_STATE}"
stop_pid "${PID_L}"

# =====================================================================
echo
echo "passed: ${PASSED} failed: ${FAILED}"
[ "${FAILED}" = "0" ] || exit 1
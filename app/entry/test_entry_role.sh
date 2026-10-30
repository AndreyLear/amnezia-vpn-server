#!/bin/bash
# Харнесс роли entry (amnezia-vpn-server-14y0.6): install.sh --role entry и
# entry-sync.py против подделок. На хосте не трогается ничего: systemctl,
# apt-get, sysctl, nginx, certbot — подделки в начале PATH, все пути — через
# AMNEZIA_INSTALL_* хуки, релиз Xray и geo-списки — файлы (file://), панель —
# маленький HTTP-сервер на петле, который пускает только по секрету узла.
#
#   bash app/entry/test_entry_role.sh
#
# Код выхода: 0 — всё прошло, 1 — нет.

set -u

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SYNC_SRC="$REPO/app/entry/entry-sync.py"
ERRORS=0
WORK="$(mktemp -d /tmp/entry-role.XXXXXX)"
PANEL_PID=""
PANEL6_PID=""
cleanup() {
    [ -z "$PANEL_PID" ] || { kill "$PANEL_PID" 2>/dev/null; wait "$PANEL_PID" 2>/dev/null; }
    [ -z "$PANEL6_PID" ] || { kill "$PANEL6_PID" 2>/dev/null; wait "$PANEL6_PID" 2>/dev/null; }
    if [ "$ERRORS" -gt 0 ]; then
        printf 'entry: вывод упавших запусков оставлен в %s\n' "$WORK" >&2
        return
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; ERRORS=$((ERRORS + 1)); }
check() { # check NAME CMD... — PASS, если команда успешна
    local name="$1"
    shift
    if "$@" >/dev/null 2>&1; then pass "$name"; else fail "$name"; fi
}

FAKE="$WORK/fakes"
CALLS="$WORK/calls.log"
STATE="$WORK/state.env"
mkdir -p "$FAKE"
export FAKE_CALLS="$CALLS" FAKE_STATE="$STATE" FAKE_WORK="$WORK"

# --- подделки -------------------------------------------------------------

cat > "$FAKE/systemctl" <<'EOF'
#!/bin/bash
echo "systemctl $*" >> "$FAKE_CALLS"
. "$FAKE_STATE"
active="$FAKE_WORK/active"
mkdir -p "$active"
case "$1" in
    is-active) [ -e "$active/${3:-$2}" ] ;;
    start | restart) touch "$active/$2"; [ "$2" != "xray-entry" ] || exit "${XRAY_START_RC:-0}" ;;
    enable)
        if [ "$2" = "--now" ]; then shift 2; for u in "$@"; do touch "$active/$u"; done; fi
        exit 0 ;;
    *) exit 0 ;;
esac
EOF
for tool in apt-get modprobe; do
    printf '#!/bin/bash\necho "%s $*" >> "$FAKE_CALLS"\nexit 0\n' "$tool" > "$FAKE/$tool"
done
cat > "$FAKE/sysctl" <<'EOF'
#!/bin/bash
echo "sysctl $*" >> "$FAKE_CALLS"
[ "$*" != "-n net.ipv4.tcp_available_congestion_control" ] || echo "reno cubic bbr"
exit 0
EOF
cat > "$FAKE/nginx" <<'EOF'
#!/bin/bash
echo "nginx $*" >> "$FAKE_CALLS"
. "$FAKE_STATE"
[ "$1" != "-t" ] || exit "${NGINX_T_RC:-0}"
exit 0
EOF
cat > "$FAKE/certbot" <<'EOF'
#!/bin/bash
echo "certbot $*" >> "$FAKE_CALLS"
. "$FAKE_STATE"
[ "${CERTBOT_RC:-0}" = 0 ] || exit 1
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-d" ]; then
        mkdir -p "$FAKE_WORK/le/$2" && touch "$FAKE_WORK/le/$2/fullchain.pem" "$FAKE_WORK/le/$2/privkey.pem"
    fi
    shift
done
EOF
printf '#!/bin/sh\nexit 0\n' > "$FAKE/fail2ban-server"
chmod +x "$FAKE"/*

# Подделка Xray: version и run -test. Проверка принимает конфиг, если это
# JSON и рядом лежат geo-списки, — и отказывает по XRAY_TEST_RC.
XRAY_VERSION=26.10.21
mkdir -p "$WORK/xray-src" "$WORK/xray-rel/v$XRAY_VERSION"
cat > "$WORK/xray-src/xray" <<EOF
#!/bin/bash
case "\$1" in
    version) echo "Xray $XRAY_VERSION (Xray, Penetrates Everything.) fake" ;;
    run)
        if [ "\$2" = "-test" ]; then
            echo "xray-test \$4" >> "\$FAKE_CALLS"
            . "\$FAKE_STATE"
            [ "\${XRAY_TEST_RC:-0}" = 0 ] || { echo "fake xray: config rejected"; exit 1; }
            [ -f "\$XRAY_LOCATION_ASSET/geosite.dat" ] || { echo "fake xray: no geosite.dat"; exit 1; }
            python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "\$4"
        fi ;;
    api)
        # Проверка сторожа (amnezia-vpn-server-q1do.5): ответил ли API.
        echo "xray-api \$*" >> "\$FAKE_CALLS"
        . "\$FAKE_STATE"
        exit "\${XRAY_API_RC:-0}" ;;
esac
EOF
make_zip() { # make_zip DEST SRC_FILE
    python3 -I -c '
import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    i = zipfile.ZipInfo("xray"); i.external_attr = 0o755 << 16
    z.writestr(i, open(sys.argv[2], "rb").read())
    z.writestr("LICENSE", "fake")' "$1" "$2"
}
sha256() { python3 -I -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"; }
make_zip "$WORK/xray-rel/v$XRAY_VERSION/Xray-linux-64.zip" "$WORK/xray-src/xray"
XRAY_ZIP_SHA="$(sha256 "$WORK/xray-rel/v$XRAY_VERSION/Xray-linux-64.zip")"
write_lock() { # write_lock SHA
    printf 'IMAGE_VERSION=2.12.2\nXRAY_VERSION=%s\nXRAY_SHA256_LINUX_64=%s\nXRAY_SHA256_LINUX_ARM64_V8A=\n' \
        "$XRAY_VERSION" "$1" > "$WORK/versions.lock"
}

GEO="$WORK/geo"
mkdir -p "$GEO"
publish_geo() { # publish_geo CONTENT [BAD]
    local f
    for f in geoip.dat geosite.dat; do
        printf '%s %s\n' "$f" "$1" > "$GEO/$f"
        printf '%s  %s\n' "$(sha256 "$GEO/$f")" "$f" > "$GEO/$f.sha256sum"
    done
    [ -z "${2:-}" ] || printf '%s  geosite.dat\n' "$(printf '0%.0s' $(seq 64))" > "$GEO/geosite.dat.sha256sum"
}
publish_geo v1

printf 'ID=ubuntu\nVERSION_ID="24.04"\nVERSION_CODENAME=noble\n' > "$WORK/os-release"

# --- панель -----------------------------------------------------------------

TOKEN="$(python3 -I -c 'import secrets; print(secrets.token_urlsafe(32))')"
UUID1=11111111-1111-4111-8111-111111111111
UUID2=22222222-2222-4222-8222-222222222222
UP_PBK=51jUque18F15SoLfGzd4Es0eKOqplVOyBTSAsZxmrGw
panel_clients() { # panel_clients EN1 EN2
    cat > "$WORK/panel.json" <<EOF
{"entry":"ru","clients":[{"client_id":1,"uuid":"$UUID1","enabled":$1},{"client_id":2,"uuid":"$UUID2","enabled":$2}],
 "upstream":{"address":"super-space.com.de","port":443,"path":"/96f96172","server_name":"super-space.com.de",
 "public_key":"$UP_PBK","short_id":"48651fdff605420e"}}
EOF
}
panel_clients true false
cat > "$WORK/panel.py" <<'EOF'
import http.server, os, socket, sys, threading
work, token = sys.argv[1], sys.argv[2]
# dual — та же панель на 127.0.0.1 и ::1 одним портом (amnezia-vpn-server-q1do.6):
# по какому семейству пришёл запрос, пишется в panel.family.
dual = sys.argv[3:] == ["dual"]
def family_log(h):
    if dual:
        with open(os.path.join(work, "panel.family"), "a") as f:
            f.write("%s %s\n" % (h.path, h.server.family))
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_GET(self):
        ok = self.headers.get("Authorization") == "Bearer " + token
        with open(os.path.join(work, "panel.log"), "a") as f:
            f.write("%s auth=%s\n" % (self.path, "ok" if ok else "bad"))
        family_log(self)
        status = open(os.path.join(work, "panel.status")).read().strip()
        if status == "302":
            self.send_response(302); self.send_header("Location", "/elsewhere"); self.end_headers(); return
        if self.path != "/api/entry/config":
            self.send_response(404); self.end_headers(); return
        if not ok:
            self.send_response(401); self.end_headers(); return
        if status != "200":
            self.send_response(int(status)); self.end_headers(); return
        body = open(os.path.join(work, "panel.json"), "rb").read()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers()
        self.wfile.write(body)
    def do_POST(self):
        # Отчёт о показателях (amnezia-vpn-server-q1do.1). report.status:
        # 204 — принят, 404 — панель старше отчётов, прочее — отказ.
        ok = self.headers.get("Authorization") == "Bearer " + token
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        with open(os.path.join(work, "report.log"), "a") as f:
            f.write("%s auth=%s type=%s\n" % (self.path, "ok" if ok else "bad", self.headers.get("Content-Type")))
        family_log(self)
        if self.path != "/api/entry/report" or not os.path.exists(os.path.join(work, "report.status")):
            self.send_response(404); self.end_headers(); return
        if not ok:
            self.send_response(401); self.end_headers(); return
        status = int(open(os.path.join(work, "report.status")).read().strip())
        if status == 204:
            open(os.path.join(work, "report.json"), "wb").write(body)
        self.send_response(status); self.end_headers()
if dual:
    class S6(http.server.HTTPServer):
        address_family = socket.AF_INET6
    srv = S6(("::1", 0), H)
    srv.family = "v6"
    s4 = http.server.HTTPServer(("127.0.0.1", srv.server_port), H)
    s4.family = "v4"
    threading.Thread(target=s4.serve_forever, daemon=True).start()
    open(os.path.join(work, "panel6.port"), "w").write(str(srv.server_port))
    srv.serve_forever()
srv = http.server.HTTPServer(("127.0.0.1", 0), H)
open(os.path.join(work, "panel.port"), "w").write(str(srv.server_port))
srv.serve_forever()
EOF
echo 200 > "$WORK/panel.status"
echo 204 > "$WORK/report.status"
python3 -I "$WORK/panel.py" "$WORK" "$TOKEN" &
PANEL_PID=$!
for _ in $(seq 50); do [ -s "$WORK/panel.port" ] && break; sleep 0.1; done
PANEL="http://127.0.0.1:$(cat "$WORK/panel.port")"

# --- запуск -------------------------------------------------------------------

ED="$WORK/opt/xray-entry"
reset_host() {
    rm -rf "$WORK/opt" "$WORK/systemd" "$WORK/sysctl" "$WORK/modules" "$WORK/f2b" "$WORK/sites" \
        "$WORK/le" "$WORK/acme" "$WORK/stub" "$WORK/active" "$WORK/panel.log" \
        "$WORK/report.log" "$WORK/report.json"
    mkdir -p "$WORK/sites"
    : > "$CALLS"
    echo 200 > "$WORK/panel.status"
    echo 204 > "$WORK/report.status"
    printf 'XRAY_TEST_RC=0\nNGINX_T_RC=0\nCERTBOT_RC=0\nXRAY_START_RC=0\nXRAY_API_RC=0\n' > "$STATE"
    write_lock "$XRAY_ZIP_SHA"
    panel_clients true false
    publish_geo v1
}
setstate() { sed "s|^$1=.*|$1=$2|" "$STATE" > "$STATE.new" && mv "$STATE.new" "$STATE"; }

# run_install ARGS… — install.sh --role entry; stdin — секрет для «-».
run_install() {
    env AMNEZIA_INSTALL_TEST=1 AMNEZIA_INSTALL_FAKE_DIR="$FAKE" \
        AMNEZIA_INSTALL_OS_RELEASE="$WORK/os-release" \
        AMNEZIA_INSTALL_ENTRY_DIR="$ED" \
        AMNEZIA_INSTALL_SYSTEMD_DIR="$WORK/systemd" \
        AMNEZIA_INSTALL_SYSCTL_DIR="$WORK/sysctl" \
        AMNEZIA_INSTALL_MODULES_DIR="$WORK/modules" \
        AMNEZIA_INSTALL_FAIL2BAN_JAIL="$WORK/f2b/jail.d/amnezia-vpn-sshd.conf" \
        AMNEZIA_INSTALL_VERSIONS_LOCK="$WORK/versions.lock" \
        AMNEZIA_INSTALL_UNAME_M=x86_64 \
        AMNEZIA_INSTALL_XRAY_BASE="file://$WORK/xray-rel" \
        AMNEZIA_INSTALL_GEO_BASE="file://$GEO" \
        AMNEZIA_INSTALL_NGINX_SITES_DIR="$WORK/sites" \
        AMNEZIA_INSTALL_LETSENCRYPT_DIR="$WORK/le" \
        AMNEZIA_INSTALL_ACME_ROOT="$WORK/acme" \
        AMNEZIA_INSTALL_ENTRY_STUB_ROOT="$WORK/stub" \
        bash "$REPO/install.sh" --role entry "$@" > "$WORK/out.log" 2>&1
    local rc=$?
    { printf '=== install.sh --role entry %s (rc=%s)\n' "$*" "$rc"; cat "$WORK/out.log"; } >> "$WORK/all-out.log"
    return "$rc"
}
# run_sync ARGS… — entry-sync.py так, как его зовёт таймер.
run_sync() {
    env PATH="$FAKE:$PATH" AMNEZIA_ENTRY_DIR="$ED" AMNEZIA_ENTRY_GEO_BASE="file://$GEO" \
        AMNEZIA_ENTRY_PROC="$WORK/proc" AMNEZIA_ENTRY_DISK_PATH="$WORK" \
        python3 -I "$ED/entry-sync.py" "$@" > "$WORK/sync.log" 2>&1
}
# cfg PYTHON-EXPR — истинность выражения над config.json (c) и settings.json (s).
cfg() {
    python3 -I - "$ED/config.json" "$ED/settings.json" "$1" <<'EOF'
import json, sys
c = json.load(open(sys.argv[1]))
s = json.load(open(sys.argv[2]))
inb = c["inbounds"][0]
rs = inb["streamSettings"]["realitySettings"]
outs = {o["tag"]: o for o in c["outbounds"]}
rules = c["routing"]["rules"]
emails = [x["email"] for x in inb["settings"]["clients"]]
sys.exit(0 if eval(sys.argv[3]) else 1)
EOF
}
# report PYTHON-EXPR — истинность выражения над последним принятым отчётом (r).
report() {
    python3 -I - "$WORK/report.json" "$1" <<'EOF'
import json, sys
r = json.load(open(sys.argv[1]))
sys.exit(0 if eval(sys.argv[2]) else 1)
EOF
}
# fake_proc UPTIME CPU_BUSY CPU_IDLE RX TX — поддельный /proc узла: основной
# интерфейс eth0 (маршрут по умолчанию), рядом туннель с бо́льшим трафиком.
fake_proc() {
    mkdir -p "$WORK/proc/net"
    printf '%s 1000.00\n' "$1" > "$WORK/proc/uptime"
    # user nice system idle iowait irq softirq steal: занято — user+system.
    printf 'cpu  %d 0 %d %d 0 0 0 0 0 0\ncpu0 1 0 1 1 0 0 0 0 0 0\n' "$(($2 / 2))" "$(($2 - $2 / 2))" "$3" > "$WORK/proc/stat"
    printf 'MemTotal:        2000000 kB\nMemFree:          100000 kB\nMemAvailable:    1500000 kB\n' > "$WORK/proc/meminfo"
    cat > "$WORK/proc/net/route" <<'EOF'
Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wg0	0000000A	00000000	0001	0	0	0	00FFFFFF	0	0	0
eth0	00000000	0102A8C0	0003	0	0	100	00000000	0	0	0
eth0	0002A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
EOF
    cat > "$WORK/proc/net/dev" <<EOF
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:     500       5    0    0    0     0          0         0      500       5    0    0    0     0       0          0
  eth0: $4    100    0    0    0     0          0         0 $5    90    0    0    0     0       0          0
   wg0: 999999999  100    0    0    0     0          0         0 999999999  90    0    0    0     0       0          0
EOF
}
calls() { grep -c -- "$1" "$CALLS" 2>/dev/null || true; }
mode_of() { python3 -I -c 'import os,sys; print(oct(os.stat(sys.argv[1]).st_mode & 0o777))' "$1"; }

# --- X25519 ------------------------------------------------------------------

# Ключ Reality считается без внешних пакетов — и должен совпадать с тем, что
# даёт RFC 7748 и `xray x25519` (пара из internal/xray/xray_test.go).
x25519_ok="$(python3 -I - "$SYNC_SRC" <<'EOF'
import importlib.util, sys
sys.dont_write_bytecode = True; spec = importlib.util.spec_from_file_location("es", sys.argv[1]); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
alice = bytes.fromhex("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
ok = m.x25519(alice, (9).to_bytes(32, "little")).hex() == "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a"
ok = ok and m.public_key("AAoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1Vw") == "u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ"
k = m.new_private_key()
ok = ok and m.public_key(k) == m.public_key(k) and len(k) == 43
print("ok" if ok else "bad")
EOF
)"
[ "$x25519_ok" = "ok" ] && pass "x25519: RFC 7748 и пара xray x25519" || fail "x25519: RFC 7748 и пара xray x25519"

# --- 1. новый узел на существующем сайте ----------------------------------------

reset_host
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - \
    --entry-dest 127.0.0.1:4441 --entry-server-names a.example.ru,www.a.example.ru --entry-xver 1
rc=$?
[ "$rc" = 0 ] && pass "новый узел: установка прошла" || fail "новый узел: установка прошла (rc=$rc)"
check "новый узел: секрет в token, 0600" test "$(cat "$ED/token" 2>/dev/null)" = "$TOKEN" -a "$(mode_of "$ED/token")" = 0o600
check "новый узел: settings.json 0600" test "$(mode_of "$ED/settings.json")" = 0o600
if grep -qF "$TOKEN" "$WORK/out.log"; then fail "новый узел: секрет не выводится"; else pass "новый узел: секрет не выводится"; fi
check "новый узел: Xray поставлен из архива, сверенного по sha256" test -x "$ED/xray" -a -f "$ED/xray.release"
check "новый узел: geo-списки на месте" test -f "$ED/geoip.dat" -a -f "$ED/geosite.dat"
check "новый узел: ключ Reality сгенерирован, публичный — из приватного" python3 -I - "$SYNC_SRC" "$ED/settings.json" <<'EOF'
import importlib.util, json, sys
sys.dont_write_bytecode = True; spec = importlib.util.spec_from_file_location("es", sys.argv[1]); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
s = json.load(open(sys.argv[2]))
assert m.public_key(s["private_key"]) == s["public_key"] and len(s["short_id"]) == 16 and s["path"].startswith("/")
EOF
check "конфиг: слушает :: 443, Reality на локальный сайт с xver 1 и его именами" \
    cfg 'inb["listen"] == "::" and inb["port"] == 443 and rs["target"] == "127.0.0.1:4441" and rs["xver"] == 1 and rs["serverNames"] == ["a.example.ru", "www.a.example.ru"] and rs["privateKey"] == s["private_key"] and rs["shortIds"] == [s["short_id"]] and inb["streamSettings"]["xhttpSettings"]["path"] == s["path"]'
check "конфиг: только включённые клиенты, email c<id>" cfg 'emails == ["c1"] and inb["settings"]["clients"][0]["id"] == "'$UUID1'"'
check "конфиг: клиент уходит на основной сервер под своим UUID" \
    cfg 'outs["upstream-c1"]["settings"]["vnext"][0]["users"][0]["id"] == "'$UUID1'" and outs["upstream-c1"]["settings"]["vnext"][0]["address"] == "super-space.com.de" and outs["upstream-c1"]["streamSettings"]["realitySettings"] == {"serverName": "super-space.com.de", "fingerprint": "chrome", "publicKey": "'$UP_PBK'", "shortId": "48651fdff605420e"} and outs["upstream-c1"]["streamSettings"]["xhttpSettings"]["path"] == "/96f96172" and "upstream-c2" not in outs'
# Исходящие к основному серверу (amnezia-vpn-server-q1do.6): Xray сам берёт
# A и AAAA имени и гонит соединения, IPv4 первым, IPv6 — через 250 мс.
UP_SOCKOPT='{"domainStrategy": "UseIP", "happyEyeballs": {"tryDelayMs": 250, "prioritizeIPv6": False, "interleave": 1, "maxConcurrentTry": 4}}'
check "конфиг: к основному — оба семейства, IPv4 первым, IPv6 через 250 мс" \
    cfg 'outs["upstream-c1"]["streamSettings"]["sockopt"] == '"$UP_SOCKOPT"
check "конфиг: правило по user ведёт клиента в его outbound" cfg '{"type": "field", "user": ["c1"], "outboundTag": "upstream-c1"} in rules'
check "конфиг: частные сети и QUIC — в блок, российское — напрямую (после правила API)" \
    cfg 'rules[1] == {"type": "field", "ip": ["geoip:private"], "outboundTag": "block"} and rules[2] == {"type": "field", "network": "udp", "port": "443", "outboundTag": "block"} and rules[3]["outboundTag"] == "direct" and "geosite:category-ru" in rules[3]["domain"] and "domain:xn--p1ai" in rules[3]["domain"] and rules[4] == {"type": "field", "ip": ["geoip:ru"], "outboundTag": "direct"}'
check "конфиг: всё прочее — в блок, напрямую — только IPv4" \
    cfg 'rules[-1] == {"type": "field", "network": "tcp,udp", "outboundTag": "block"} and outs["direct"]["streamSettings"]["sockopt"]["domainStrategy"] == "UseIPv4"'
check "конфиг: без журнала доступа" cfg 'c["log"]["access"] == "none"'
check "юниты: Xray, синхронизация раз в минуту, geo раз в неделю" \
    grep -q "ExecStart=$ED/xray run -c $ED/config.json" "$WORK/systemd/xray-entry.service"
check "юниты: таймер синхронизации — раз в минуту" grep -q '^OnUnitActiveSec=1min$' "$WORK/systemd/xray-entry-sync.timer"
check "юниты: таймер geo-списков" grep -q '^OnCalendar=Mon 04:30$' "$WORK/systemd/xray-entry-geo.timer"
check "юниты: синхронизация зовёт entry-sync.py sync" grep -q "python3 -I $ED/entry-sync.py sync" "$WORK/systemd/xray-entry-sync.service"
check "юниты: обновление — отдельный oneshot entry-sync.py update (amnezia-vpn-server-q1do.2)" \
    grep -q "python3 -I $ED/entry-sync.py update" "$WORK/systemd/xray-entry-update.service"
check "юниты: обновление — Type=oneshot" grep -q '^Type=oneshot$' "$WORK/systemd/xray-entry-update.service"
check "sysctl: BBR, fq, mtu_probing, буферы" grep -q 'net.ipv4.tcp_congestion_control = bbr' "$WORK/sysctl/90-vpn-entry-tcp.conf"
check "sysctl: mtu_probing" grep -q 'net.ipv4.tcp_mtu_probing = 1' "$WORK/sysctl/90-vpn-entry-tcp.conf"
check "fail2ban: jail для sshd" grep -q '^\[sshd\]' "$WORK/f2b/jail.d/amnezia-vpn-sshd.conf"
check "запуск: xray-entry запущен, таймеры включены" test "$(calls 'systemctl start xray-entry')" = 1 -a "$(calls 'enable --now xray-entry-sync.timer xray-entry-geo.timer')" = 1
check "запуск: без лишнего перезапуска" test "$(calls 'systemctl restart xray-entry')" = 0
check "панель: секрет в заголовке, не в адресе" test "$(cat "$WORK/panel.log")" = "/api/entry/config auth=ok"
check "показатели: после синка — POST /api/entry/report с секретом в заголовке, JSON" \
    test "$(cat "$WORK/report.log" 2>/dev/null)" = "/api/entry/report auth=ok type=application/json"
check "показатели: версия выпуска из versions.lock и версия Xray" \
    report 'r["release"] == "2.12.2" and r["xray_version"] == "'$XRAY_VERSION'"'
check "показатели: версия выпуска записана установщиком" test "$(cat "$ED/release" 2>/dev/null)" = 2.12.2
check "итог: команда регистрации узла в панели" grep -q "panel xray entry set <name> --host a.example.ru --sni a.example.ru" "$WORK/out.log"
check "итог: ссылки через узел подписаны «VLESS <имя>»" grep -q -- '--prefix "VLESS "' "$WORK/out.log"

# --- 2. синхронизация ------------------------------------------------------------

: > "$CALLS"
run_sync sync
check "синхронизация без изменений: конфиг не трогается, без перезапуска" \
    test $? = 0 -a "$(calls 'systemctl restart')" = 0
grep -q "без изменений" "$WORK/sync.log" && pass "синхронизация без изменений: так и сказано" || fail "синхронизация без изменений: так и сказано"

panel_clients false true
: > "$CALLS"
run_sync sync
check "клиент включён и отключён в панели: конфиг меняется, Xray перезапущен один раз" \
    test $? = 0 -a "$(calls 'systemctl restart xray-entry')" = 1
check "клиент включён и отключён в панели: в конфиге только включённый" cfg 'emails == ["c2"] and "upstream-c2" in outs and "upstream-c1" not in outs'
check "конфиг проверен xray run -test до замены" test "$(calls "xray-test $ED/config.new.json")" = 1
saved="$(cksum < "$ED/config.json")"

for status in 500 503 429; do
    echo "$status" > "$WORK/panel.status"
    panel_clients true true
    : > "$CALLS"
    run_sync sync
    rc=$?
    check "панель отвечает $status: узел на прежнем конфиге, без перезапуска" \
        test "$rc" = 1 -a "$(cksum < "$ED/config.json")" = "$saved" -a "$(calls 'systemctl restart')" = 0
done
grep -q "работаю на последнем" "$WORK/sync.log" && pass "нет связи с панелью: так и сказано" || fail "нет связи с панелью: так и сказано"
echo 200 > "$WORK/panel.status"

cp "$ED/token" "$WORK/token.keep"
printf '%s\n' "$(python3 -I -c 'import secrets; print(secrets.token_urlsafe(32))')" > "$ED/token"
: > "$CALLS"
run_sync sync
check "чужой секрет: 401, узел на прежнем конфиге" test $? = 1 -a "$(cksum < "$ED/config.json")" = "$saved"
grep -q "секрет узла не принят" "$WORK/sync.log" && pass "чужой секрет: так и сказано" || fail "чужой секрет: так и сказано"
cp "$WORK/token.keep" "$ED/token"

echo 302 > "$WORK/panel.status"
: > "$WORK/panel.log"
run_sync sync
check "перенаправление не выполняется: секрет не уходит на другой адрес" \
    test $? = 1 -a "$(grep -c elsewhere "$WORK/panel.log")" = 0 -a "$(cksum < "$ED/config.json")" = "$saved"
echo 200 > "$WORK/panel.status"

printf '{"clients":[{"client_id":1,"uuid":"not-a-uuid","enabled":true}],"upstream":{}}' > "$WORK/panel.json"
run_sync sync
check "битый ответ панели: узел на прежнем конфиге" test $? = 1 -a "$(cksum < "$ED/config.json")" = "$saved"
check "битый ответ панели не заменяет сохранённый" python3 -I -c 'import json,sys; assert json.load(open(sys.argv[1]))["clients"][0]["uuid"] == sys.argv[2]' "$ED/state/panel.json" "$UUID1"

panel_clients true true
setstate XRAY_TEST_RC 1
: > "$CALLS"
run_sync sync
check "Xray не принял новый конфиг: прежний на месте, без перезапуска" \
    test $? = 1 -a "$(cksum < "$ED/config.json")" = "$saved" -a "$(calls 'systemctl restart')" = 0 -a ! -e "$ED/config.new.json"
setstate XRAY_TEST_RC 0

# --- 2б. показатели узла (amnezia-vpn-server-q1do.1) ---------------------------------

# Первый запуск: CPU и трафик — разница между запусками, их ещё нет;
# память, диск и аптайм — сразу. Список клиентов — тот, что уже в
# конфиге: секция про отчёт, а не про перезапуски.
panel_clients false true
rm -f "$ED/state/metrics.json" "$WORK/report.json"
: > "$WORK/report.log"
fake_proc 3600.50 1000 9000 100000 50000
run_sync sync
check "показатели: первый запуск — синк прошёл, отчёт отправлен" test $? = 0 -a -s "$WORK/report.json"
check "показатели: первый запуск — без CPU и трафика" \
    report 'r["cpu_percent"] is None and r["rx_bytes"] is None and r["tx_bytes"] is None and r["interval_seconds"] is None'
check "показатели: память — занятое из MemAvailable, в байтах" \
    report 'r["ram_total_bytes"] == 2000000 * 1024 and r["ram_used_bytes"] == 500000 * 1024 and r["ram_percent"] == 25.0'
check "показатели: диск и аптайм" \
    report 'r["disk_total_bytes"] > 0 and 0 <= r["disk_used_bytes"] <= r["disk_total_bytes"] and 0 <= r["disk_percent"] <= 100 and r["uptime_seconds"] == 3600'
check "показатели: состояние для разницы — рядом, 0600" test "$(mode_of "$ED/state/metrics.json")" = 0o600

# Минуту спустя: занято +250 из +1000 — 25%, трафик основного интерфейса
# (eth0, не туннель) — за этот интервал.
fake_proc 3660.50 1250 9750 106000 53000
run_sync sync
check "показатели: CPU — по разнице /proc/stat" report 'r["cpu_percent"] == 25.0'
check "показатели: трафик основного интерфейса за интервал" \
    report 'r["rx_bytes"] == 6000 and r["tx_bytes"] == 3000 and r["interval_seconds"] == 60'

# Перезагрузка: счётчики сброшены — разницы нет, а не отрицательная.
fake_proc 30.00 10 90 500 400
run_sync sync
check "показатели: после перезагрузки — без CPU и трафика" \
    report 'r["cpu_percent"] is None and r["rx_bytes"] is None and r["uptime_seconds"] == 30'

# Без /proc (не Linux, нет прав) — отчёт без чисел, синк не падает.
mv "$WORK/proc" "$WORK/proc.off"
run_sync sync
check "показатели: без /proc — синк прошёл" test $? = 0
check "показатели: без /proc — числа пустые" \
    report 'r["cpu_percent"] is None and r["ram_percent"] is None and r["uptime_seconds"] is None'
mv "$WORK/proc.off" "$WORK/proc"

# Панель старше отчётов отвечает 404 — узел молча обходится без них.
rm -f "$WORK/report.status"
run_sync sync
rc=$?
check "старая панель: синк прошёл, код 0" test "$rc" = 0
if grep -q "показател" "$WORK/sync.log"; then fail "старая панель: в журнале тихо"; else pass "старая панель: в журнале тихо"; fi

# Отчёт отвергнут — работе узла это не мешает.
echo 400 > "$WORK/report.status"
run_sync sync
check "отчёт отвергнут: синк прошёл, код 0" test $? = 0
grep -q "панель не приняла показатели: ответила 400" "$WORK/sync.log" \
    && pass "отчёт отвергнут: так и сказано" || fail "отчёт отвергнут: так и сказано"
echo 204 > "$WORK/report.status"

# Синк не удался — отчёт не отправляется: неверный секрет не тратит
# лишнюю попытку из лимита панели.
echo 503 > "$WORK/panel.status"
: > "$WORK/report.log"
run_sync sync
check "панель ответила 503: отчёт не отправлялся" test ! -s "$WORK/report.log"
echo 200 > "$WORK/panel.status"

# --- 3. geo-списки ---------------------------------------------------------------

publish_geo v2
: > "$CALLS"
run_sync geo
check "geo: новые списки сверены, конфиг проверен с ними, Xray перезапущен" \
    test $? = 0 -a "$(cat "$ED/geosite.dat")" = "geosite.dat v2" -a "$(calls 'systemctl restart xray-entry')" = 1
publish_geo v3 bad
: > "$CALLS"
run_sync geo
check "geo: sha256 не сошлась — прежние списки, без перезапуска" \
    test $? = 1 -a "$(cat "$ED/geosite.dat")" = "geosite.dat v2" -a "$(calls 'systemctl restart')" = 0
publish_geo v4
setstate XRAY_TEST_RC 1
run_sync geo
check "geo: Xray не принял конфиг с новыми списками — прежние" test $? = 1 -a "$(cat "$ED/geosite.dat")" = "geosite.dat v2"
setstate XRAY_TEST_RC 0

# --- 4. повторный запуск -----------------------------------------------------------

panel_clients false true
cp "$ED/settings.json" "$WORK/settings.keep"
: > "$CALLS"
run_install
rc=$?
check "повторный запуск без флагов: прошёл" test "$rc" = 0
check "повторный запуск без флагов: settings.json тот же" cmp -s "$ED/settings.json" "$WORK/settings.keep"
check "повторный запуск: Xray не перекачивается и не перезапускается" \
    test "$(calls 'systemctl restart xray-entry')" = 0 -a "$(calls 'systemctl start xray-entry')" = 0

# --- 5. перевод ручной настройки --------------------------------------------------

# Как на 62.109.31.63: /opt/xray-entry с settings.json, ключами и build.py,
# Xray работает, у клиентов — ссылки на эти ключи, shortId, путь и имена.
seed_manual() {
    reset_host
    mkdir -p "$ED" "$WORK/systemd" "$WORK/active"
    cat > "$ED/settings.json" <<'EOF'
{
 "listen": "::",
 "port": 443,
 "path": "/r5",
 "private_key": "AAoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1Vw",
 "public_key": "u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ",
 "short_id": "a50c249d61c252d0",
 "server_names": ["super-space.ru", "www.super-space.ru", "salary.super-space.ru"],
 "dest": "127.0.0.1:4441",
 "xver": 1,
 "link_host": "super-space.ru",
 "link_port": 443,
 "link_server_name": "super-space.ru",
 "link_prefix": "RU ",
 "extra_clients": [],
 "upstream": {"address": "super-space.com.de", "port": 443, "id": "33333333-3333-4333-8333-333333333333",
              "path": "/96f96172", "server_name": "super-space.com.de",
              "public_key": "51jUque18F15SoLfGzd4Es0eKOqplVOyBTSAsZxmrGw", "short_id": "48651fdff605420e"},
 "direct_domains": ["geosite:category-ru", "domain:ru", "domain:su", "domain:xn--p1ai"],
 "direct_ips": ["geoip:ru"],
 "block_quic": true
}
EOF
    chmod 0600 "$ED/settings.json"
    printf '{"manual": true}\n' > "$ED/config.json"
    echo "# ручной build.py" > "$ED/build.py"
    echo "[]" > "$ED/users.json"
    printf 'geo\n' > "$ED/geoip.dat"
    printf 'geo\n' > "$ED/geosite.dat"
    printf '[Service]\nExecStart=/opt/xray-entry/xray run -c /opt/xray-entry/config.json\n' > "$WORK/systemd/xray-entry.service"
    touch "$WORK/active/xray-entry"
}
seed_manual
cp "$ED/settings.json" "$WORK/manual-settings.json"
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token -
rc=$?
check "перевод: установка прошла без флагов маскировки" test "$rc" = 0
check "перевод: ключи, shortId, путь, имена, dest и xver — прежние" python3 -I - "$WORK/manual-settings.json" "$ED/settings.json" <<'EOF'
import json, sys
a, b = json.load(open(sys.argv[1])), json.load(open(sys.argv[2]))
for k in ("private_key", "public_key", "short_id", "path", "server_names", "dest", "xver", "listen", "port",
          "link_host", "link_server_name", "link_prefix", "direct_domains", "direct_ips", "block_quic"):
    assert a[k] == b[k], k
EOF
check "перевод: конфиг собран на прежних ключах и именах" \
    cfg 'rs["privateKey"] == "AAoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1Vw" and rs["shortIds"] == ["a50c249d61c252d0"] and inb["streamSettings"]["xhttpSettings"]["path"] == "/r5" and rs["serverNames"] == ["super-space.ru", "www.super-space.ru", "salary.super-space.ru"] and rs["target"] == "127.0.0.1:4441" and rs["xver"] == 1 and emails == ["c1"]'
check "перевод: ровно один перезапуск Xray, без остановки" \
    test "$(calls 'systemctl restart xray-entry')" = 1 -a "$(calls 'systemctl stop')" = 0 -a "$(calls 'systemctl start xray-entry')" = 0
check "перевод: ручные файлы владельца не тронуты" test -f "$ED/build.py" -a -f "$ED/users.json"
check "перевод: geo-списки, что уже были, не перекачиваются" test "$(cat "$ED/geosite.dat")" = "geo"
check "перевод: регистрация в панели — те же имя и sni, что в выданных ссылках" \
    grep -q -- "--host super-space.ru --sni super-space.ru" "$WORK/out.log"
check "перевод: регистрация в панели — те же ключ, shortId и путь" \
    grep -q -- "--public-key u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ --short-id a50c249d61c252d0 --path /r5 " "$WORK/out.log"

# Панель недоступна во время перевода и списка от неё ещё не было: ручной
# конфиг остаётся как есть — клиентов не выкидывает.
seed_manual
echo 503 > "$WORK/panel.status"
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token -
rc=$?
check "перевод без связи с панелью: ручной конфиг на месте" test "$rc" = 0 -a "$(cat "$ED/config.json")" = '{"manual": true}'
check "перевод без связи с панелью: новый Xray подхвачен перезапуском" test "$(calls 'systemctl restart xray-entry')" = 1
grep -q "first sync with the panel failed" "$WORK/out.log" && pass "перевод без связи с панелью: так и сказано" || fail "перевод без связи с панелью: так и сказано"
echo 200 > "$WORK/panel.status"

# Импорт файла ручной настройки в пустой каталог.
reset_host
seed_manual
mv "$ED/settings.json" "$WORK/import.json"
rm -rf "$ED"
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - --import-settings "$WORK/import.json"
check "импорт settings.json: установка прошла" test $? = 0
check "импорт settings.json: ключи и имена перенесены" \
    cfg 'rs["privateKey"] == "AAoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1Vw" and rs["shortIds"] == ["a50c249d61c252d0"] and s["panel_url"] == "'"$PANEL"'"'
check "импорт settings.json: 0600" test "$(mode_of "$ED/settings.json")" = 0o600

# Свои учётки узла (extra_clients, например проба) ходят служебной учёткой
# ручной настройки (upstream.id); у клиентов панели дорога своя.
python3 -I - "$ED/settings.json" <<'EOF'
import json, sys
s = json.load(open(sys.argv[1]))
s["extra_clients"] = [{"id": "44444444-4444-4444-8444-444444444444", "email": "probe"}]
json.dump(s, open(sys.argv[1], "w"))
EOF
run_sync sync
check "extra_clients: проба уходит служебной учёткой, остальное не в блок" \
    cfg '"probe" in emails and outs["upstream"]["settings"]["vnext"][0]["users"][0]["id"] == "33333333-3333-4333-8333-333333333333" and rules[-1]["outboundTag"] == "upstream"'
check "перенос: служебная учётка к основному — тоже по обоим семействам (amnezia-vpn-server-q1do.6)" \
    cfg 'outs["upstream"]["streamSettings"]["sockopt"] == '"$UP_SOCKOPT"

# --- 6. своя заглушка --entry-domain ------------------------------------------------

reset_host
touch "$WORK/sites/default"
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - --entry-domain ru.example.com
rc=$?
check "заглушка: установка прошла" test "$rc" = 0
check "заглушка: Reality на 127.0.0.1:4433 без PROXY, имя — домен узла" \
    cfg 'rs["target"] == "127.0.0.1:4433" and rs["xver"] == 0 and rs["serverNames"] == ["ru.example.com"]'
check "заглушка: сертификат Let's Encrypt через webroot" grep -q "certbot certonly --webroot -w $WORK/acme -d ru.example.com" "$CALLS"
check "заглушка: сайт на петле с сертификатом домена" grep -q "listen 127.0.0.1:4433 ssl http2;" "$ED/nginx/entry.conf"
check "заглушка: ACME на 80" grep -q "location /.well-known/acme-challenge/" "$ED/nginx/entry.conf"
check "заглушка: сайт включён, default снят" test -L "$WORK/sites/amnezia-entry" -a ! -e "$WORK/sites/default"
check "заглушка: страница" test -f "$WORK/stub/index.html"

# --- 7. отказы ------------------------------------------------------------------------

reset_host
run_install --panel "$PANEL"
check "новый узел без маскировки: ошибка использования" test $? = 2
reset_host
run_install --panel "$PANEL" --entry-dest 127.0.0.1:4441
check "--entry-dest без имён: ошибка использования" test $? = 2
reset_host
run_install --panel "$PANEL" --entry-domain a.example.ru --entry-dest 127.0.0.1:4441
check "--entry-domain вместе с --entry-dest: ошибка использования" test $? = 2
reset_host
run_install --panel "$PANEL" --entry-domain a.example.ru --entry-token "bad secret value"
check "секрет не того вида: ошибка использования" test $? = 2
if grep -q "bad secret value" "$WORK/out.log"; then fail "секрет не того вида: не выводится"; else pass "секрет не того вида: не выводится"; fi
reset_host
run_install --entry-domain a.example.ru --bogus
check "неизвестный флаг роли: ошибка использования" test $? = 2
run_install_role() { bash "$REPO/install.sh" --role "$1" > "$WORK/out.log" 2>&1; }
run_install_role nope
check "неизвестная роль: ошибка использования" test $? = 2
reset_host
run_install --panel "ftp://panel.example.com" --entry-domain a.example.ru
check "адрес панели не https: отказ, ничего не поставлено" test $? != 0 -a ! -e "$ED/xray"

reset_host
write_lock "$(printf '0%.0s' $(seq 64))"
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - --entry-domain a.example.ru
check "архив Xray не сошёлся с sha256: отказ, бинарника нет" test $? = 1 -a ! -e "$ED/xray"
grep -q "sha256" "$WORK/out.log" && pass "архив Xray не сошёлся: так и сказано" || fail "архив Xray не сошёлся: так и сказано"
reset_host
write_lock ""
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - --entry-domain a.example.ru
check "в versions.lock нет sha256 Xray: отказ, бинарника нет" test $? = 1 -a ! -e "$ED/xray"

# Первый запуск ещё без секрета: Xray поднимается без клиентов и показывает
# маскировку, а синхронизация заработает, когда секрет дадут.
reset_host
run_install --panel "$PANEL" --entry-domain a.example.ru
rc=$?
check "без секрета: установка прошла" test "$rc" = 0
check "без секрета: узел поднят без клиентов" cfg 'emails == [] and rules[-1]["outboundTag"] == "block"'
grep -q "no --entry-token given yet" "$WORK/out.log" && pass "без секрета: так и сказано" || fail "без секрета: так и сказано"

# --- 8. самообновление по сигналу панели (amnezia-vpn-server-q1do.2) ------------------
#
# Панель в ответе синка называет только целевую версию (target_version) и,
# если владелец нажал «Обновить каскад», метку запроса (update_request).
# Синк лишь кладёт запрос и будит отдельный юнит; качает, сверяет и ставит
# entry-sync.py update. GitHub — файлы (file://), выпуск — настоящий
# tar.gz с поддельным install.sh, сумма — настоящая, из тела выпуска.

GH_API="$WORK/gh-api"
GH_DL="$WORK/gh-dl"
# publish_release VERSION [SHA_OVERRIDE] — выпуск на «GitHub»: описание с
# суммой и архив amnezia-vpn-server-VERSION.tar.gz.
publish_release() {
    local v="$1" dir="$WORK/rel-src/amnezia-vpn-server-$1" sha
    rm -rf "$WORK/rel-src"
    mkdir -p "$dir/app/entry" "$GH_API/repos/AndreyLear/avl/releases/tags" "$GH_DL/AndreyLear/avl/releases/download/v$v"
    printf 'IMAGE_VERSION=%s\n' "$v" > "$dir/versions.lock"
    printf '#!/bin/bash\n' > "$dir/app/entry/install-entry.sh"
    # Поддельный установщик: отмечается, по NEW_INSTALL_RC ломается, по
    # NEW_INSTALL_STOP гасит Xray. Ломаясь, портит каталог узла — откат
    # обязан вернуть его как был.
    cat > "$dir/install.sh" <<'INST'
#!/bin/bash
echo "new-installer $*" >> "$FAKE_CALLS"
. "$FAKE_STATE"
ed="$AMNEZIA_INSTALL_ENTRY_DIR"
v="$(sed -n 's/^IMAGE_VERSION=//p' "$(dirname "$0")/versions.lock")"
printf '%s\n' "$v" > "$ed/release"
if [ "${NEW_INSTALL_RC:-0}" != 0 ]; then
    echo broken > "$ed/settings.json"
    echo junk > "$ed/new-junk"
    exit "$NEW_INSTALL_RC"
fi
[ "${NEW_INSTALL_STOP:-0}" = 0 ] || rm -f "$FAKE_WORK/active/xray-entry"
touch "$ed/new-marker"
INST
    chmod +x "$dir/install.sh"
    (cd "$WORK/rel-src" && tar -czf "$GH_DL/AndreyLear/avl/releases/download/v$v/amnezia-vpn-server-$v.tar.gz" "amnezia-vpn-server-$v")
    sha="${2:-$(sha256 "$GH_DL/AndreyLear/avl/releases/download/v$v/amnezia-vpn-server-$v.tar.gz")}"
    printf '{"tag_name":"v%s","body":"Что нового\\n\\namnezia-sha256: %s\\n"}\n' "$v" "$sha" \
        > "$GH_API/repos/AndreyLear/avl/releases/tags/v$v"
}
# panel_update TARGET [REQUEST] — ответ панели с целевой версией (как есть,
# JSON-значение) и меткой запроса владельца. Заодно — адрес и сумма
# «другого выпуска»: узел не должен их брать.
panel_update() {
    python3 -I - "$WORK/panel.json" "$1" "${2:-}" <<'EOF'
import json, sys
p = json.load(open(sys.argv[1]))
for k in ("target_version", "update_request"):
    p.pop(k, None)
if sys.argv[2] != "-":
    p["target_version"] = json.loads(sys.argv[2])
if sys.argv[3]:
    p["update_request"] = sys.argv[3]
p["update_url"] = "file:///nonexistent/evil.tar.gz"
p["update_sha256"] = "0" * 64
json.dump(p, open(sys.argv[1], "w"))
EOF
}
run_update() {
    env PATH="$FAKE:$PATH" AMNEZIA_ENTRY_DIR="$ED" AMNEZIA_ENTRY_SYSTEMD_DIR="$WORK/systemd" \
        AMNEZIA_ENTRY_UPDATE_API="file://$GH_API" AMNEZIA_ENTRY_UPDATE_DOWNLOADS="file://$GH_DL" \
        AMNEZIA_ENTRY_PROC="$WORK/proc" AMNEZIA_ENTRY_DISK_PATH="$WORK" \
        python3 -I "$ED/entry-sync.py" update > "$WORK/update.log" 2>&1
}
# ustate PYTHON-EXPR — истинность выражения над state/update.json (u).
ustate() {
    python3 -I - "$ED/state/update.json" "$1" <<'EOF'
import json, sys
u = json.load(open(sys.argv[1]))
sys.exit(0 if eval(sys.argv[2]) else 1)
EOF
}
requested() { test -f "$ED/state/update-request.json"; }

reset_host
rm -rf "$GH_API" "$GH_DL"
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - --entry-dest 127.0.0.1:4441 --entry-server-names a.example.ru
check "самообновление: узел поставлен (выпуск 2.12.2)" test $? = 0 -a "$(cat "$ED/release")" = 2.12.2
fake_proc 100.0 10 90 1000 1000
printf 'NEW_INSTALL_RC=0\nNEW_INSTALL_STOP=0\n' >> "$STATE"
publish_release 2.13.0

# Без целевой версии — как раньше: ничего не просит.
panel_update -
: > "$CALLS"
run_sync sync
check "без целевой версии: синк прошёл, запроса нет" test $? = 0 -a ! -e "$ED/state/update-request.json"
check "без целевой версии: в отчёте — состояние idle" report 'r["update"]["state"] == "idle"'

# Мусор вместо версии: синк не падает, запрос не кладётся.
for junk in '"2.13.0; rm -rf /"' '"../../2.13.0"' '"v2.13.0"' '"2.13"' '"2.13.0.1"' '"02.13.0"' '2130' '{"v": "2.13.0"}'; do
    panel_update "$junk"
    : > "$CALLS"
    run_sync sync
    rc=$?
    check "мусорная версия $junk: синк прошёл, запроса нет, юнит не будили" \
        test "$rc" = 0 -a ! -e "$ED/state/update-request.json" -a "$(calls 'xray-entry-update')" = 0
done

# Понижение и та же версия — не работа.
for v in 2.12.1 2.12.2 1.99.99; do
    panel_update "\"$v\""
    : > "$CALLS"
    run_sync sync
    check "целевая $v не выше установленной 2.12.2: запроса нет" \
        test $? = 0 -a ! -e "$ED/state/update-request.json" -a "$(calls 'xray-entry-update')" = 0
done
# И сам обновитель понижать не станет, даже если запрос положили руками.
printf '{"version": "2.12.1", "request": ""}\n' > "$ED/state/update-request.json"
: > "$CALLS"
run_update
check "запрос на понижение руками: отказ, установщик не звали" \
    test $? = 1 -a "$(calls 'new-installer')" = 0 -a "$(cat "$ED/release")" = 2.12.2
check "запрос на понижение руками: запрос убран, причина — version" \
    test ! -e "$ED/state/update-request.json"
check "запрос на понижение руками: состояние failed/version" ustate 'u["state"] == "failed" and u["reason"] == "version"'
printf '{"state": "idle"}\n' > "$ED/state/update.json"

# Неудачная установка: откат из копии.
cp "$ED/settings.json" "$WORK/settings.before"
setstate NEW_INSTALL_RC 1
panel_update '"2.13.0"'
: > "$CALLS"
run_sync sync
check "целевая 2.13.0 выше 2.12.2: синк положил запрос и разбудил юнит" \
    test $? = 0 -a -f "$ED/state/update-request.json" -a "$(calls 'systemctl start --no-block xray-entry-update.service')" = 1
check "в запросе — только версия, без адреса и суммы из ответа панели" python3 -I -c '
import json, sys
r = json.load(open(sys.argv[1]))
assert r["version"] == "2.13.0" and set(r) <= {"version", "request"}, r' "$ED/state/update-request.json"
check "синк сам ничего не ставит" test "$(calls 'new-installer')" = 0
: > "$CALLS"
run_update
rc=$?
check "установка упала: код 1, установщик выпуска звали с --role entry" \
    test "$rc" = 1 -a "$(calls 'new-installer --role entry')" = 1
check "установка упала: откат вернул settings.json и версию" \
    test "$(cat "$ED/release")" = 2.12.2 -a ! -e "$ED/new-junk"
check "установка упала: settings.json — прежний" cmp -s "$ED/settings.json" "$WORK/settings.before"
check "установка упала: после отката Xray перезапущен" test "$(calls 'systemctl restart xray-entry')" -ge 1
check "установка упала: состояние failed/install, запрос убран" \
    ustate 'u["state"] == "failed" and u["reason"] == "install" and u["target"] == "2.13.0" and u["attempt_utc"]'
check "установка упала: запрос убран, копия для отката убрана" test ! -e "$ED/state/update-request.json"
run_sync sync
check "установка упала: в отчёте — failed, причина install, версия прежняя" \
    report 'r["update"]["state"] == "failed" and r["update"]["reason"] == "install" and r["update"]["target"] == "2.13.0" and r["release"] == "2.12.2"'

# Пауза после неудачи: следующий синк запроса не кладёт.
setstate NEW_INSTALL_RC 0
: > "$CALLS"
run_sync sync
check "пауза после неудачи: синк прошёл, запроса нет, юнит не будили" \
    test $? = 0 -a ! -e "$ED/state/update-request.json" -a "$(calls 'xray-entry-update')" = 0

# Запрос владельца из панели паузу пропускает — один раз.
panel_update '"2.13.0"' 2026-10-28T12:00:00Z
: > "$CALLS"
run_sync sync
check "запрос владельца: запрос положен несмотря на паузу" requested
check "запрос владельца: метка запроса запомнена и уходит в отчёт" \
    report 'r["update"]["request"] == "2026-10-28T12:00:00Z"'
rm -f "$ED/state/update-request.json"
: > "$CALLS"
run_sync sync
check "тот же запрос владельца второй раз паузу не пропускает" test ! -e "$ED/state/update-request.json"

# Пауза кончилась (попытка 31 минуту назад) — снова пробует.
python3 -I - "$ED/state/update.json" <<'EOF'
import json, sys, time
u = json.load(open(sys.argv[1]))
u["attempt_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - 31 * 60))
json.dump(u, open(sys.argv[1], "w"))
EOF
run_sync sync
check "пауза 30 минут кончилась: запрос положен" requested

# Xray не поднялся после установки — откат.
setstate NEW_INSTALL_STOP 1
: > "$CALLS"
run_update
check "Xray не поднялся после установки: код 1, откат, версия прежняя" \
    test $? = 1 -a "$(cat "$ED/release")" = 2.12.2 -a ! -e "$ED/new-marker"
check "Xray не поднялся: состояние failed/inactive" ustate 'u["state"] == "failed" and u["reason"] == "inactive"'
setstate NEW_INSTALL_STOP 0

# Сумма не сошлась — ничего не тронуто, установщик не звали.
publish_release 2.13.0 "$(printf 'a%.0s' $(seq 64))"
printf '{"version": "2.13.0", "request": ""}\n' > "$ED/state/update-request.json"
: > "$CALLS"
run_update
check "сумма не сошлась: отказ, установщик не звали" test $? = 1 -a "$(calls 'new-installer')" = 0
check "сумма не сошлась: состояние failed/checksum" ustate 'u["reason"] == "checksum"'
# Выпуска нет на GitHub.
printf '{"version": "2.14.0", "request": ""}\n' > "$ED/state/update-request.json"
run_update
check "выпуска нет: отказ" test $? = 1
check "выпуска нет: причина download" ustate 'u["reason"] == "download"'

# Другой обновитель уже идёт: второй выходит, ничего не трогая.
publish_release 2.13.0
printf '{"version": "2.13.0", "request": ""}\n' > "$ED/state/update-request.json"
python3 -I -c '
import fcntl, sys, time
f = open(sys.argv[1], "w"); fcntl.flock(f, fcntl.LOCK_EX); open(sys.argv[2], "w").close(); time.sleep(3)' \
    "$ED/state/update.lock" "$WORK/locked" &
lock_pid=$!
for _ in $(seq 50); do [ -e "$WORK/locked" ] && break; sleep 0.1; done
: > "$CALLS"
run_update
check "параллельный запуск: второй выходит с 0, установщик не звали" test $? = 0 -a "$(calls 'new-installer')" = 0
grep -q "уже идёт" "$WORK/update.log" && pass "параллельный запуск: так и сказано" || fail "параллельный запуск: так и сказано"
# Пока тот держит замок, синк не объявляет его прерванным.
printf '{"state": "updating", "target": "2.13.0"}\n' > "$ED/state/update.json"
run_sync sync
check "идёт обновление: синк не трогает состояние" ustate 'u["state"] == "updating"'
wait "$lock_pid"
rm -f "$WORK/locked"
# Замок свободен, а в состоянии updating — прогон умер, не дописав итог.
run_sync sync
check "прерванное обновление: failed/interrupted" ustate 'u["state"] == "failed" and u["reason"] == "interrupted"'

# Удачное обновление.
: > "$CALLS"
run_update
rc=$?
check "удачное обновление: код 0, установщик выпуска с --role entry" \
    test "$rc" = 0 -a "$(calls 'new-installer --role entry')" = 1 -a -e "$ED/new-marker"
check "удачное обновление: версия 2.13.0" test "$(cat "$ED/release")" = 2.13.0
check "удачное обновление: состояние idle" ustate 'u["state"] == "idle" and u["reason"] == "" and u["target"] == "2.13.0"'
check "удачное обновление: копия для отката убрана" test ! -e "$ED.rollback"
if grep -q evil "$WORK/update.log" "$CALLS"; then fail "адрес из ответа панели не использован"; else pass "адрес из ответа панели не использован"; fi
: > "$CALLS"
run_sync sync
check "после обновления: запроса нет" test ! -e "$ED/state/update-request.json" -a "$(calls 'xray-entry-update')" = 0
check "после обновления: в отчёте 2.13.0 и idle" report 'r["release"] == "2.13.0" and r["update"]["state"] == "idle"'

# --- 9. выбор --no-fail2ban запоминается (amnezia-vpn-server-0b80) ---------------------
#
# Повторный запуск без флагов — и самообновление, которое зовёт install.sh
# без флагов, — не должны снова включать fail2ban там, где владелец его
# выключил. Явный --fail2ban/--no-fail2ban меняет выбор и тоже запоминается.

JAIL="$WORK/f2b/jail.d/amnezia-vpn-sshd.conf"
f2b_setting() { # f2b_setting true|false — что записано в settings.json
    python3 -I -c 'import json,sys; v = json.load(open(sys.argv[1])).get("fail2ban"); sys.exit(0 if v is (sys.argv[2] == "true") else 1)' \
        "$ED/settings.json" "$1"
}
f2b_off() { test ! -e "$JAIL" -a "$(calls 'fail2ban')" = 0; }
f2b_on() { grep -q '^\[sshd\]' "$JAIL" && test "$(calls 'systemctl restart fail2ban')" = 1; }

reset_host
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - --no-fail2ban \
    --entry-dest 127.0.0.1:4441 --entry-server-names a.example.ru
check "--no-fail2ban: установка прошла, jail нет, fail2ban не ставили" test $? = 0 -a ! -e "$JAIL" -a "$(calls 'fail2ban')" = 0
check "--no-fail2ban: выбор записан в settings.json" f2b_setting false
: > "$CALLS"
run_install
check "повторный запуск без флагов: прошёл" test $? = 0
check "повторный запуск без флагов: fail2ban так и выключен" f2b_off
check "повторный запуск без флагов: так и сказано" grep -q 'fail2ban skipped' "$WORK/out.log"

# Самообновление: в выпуске — настоящий установщик этого дерева.
publish_real_release() { # publish_real_release VERSION
    local v="$1" dir="$WORK/rel-src/amnezia-vpn-server-$1"
    rm -rf "$WORK/rel-src"
    mkdir -p "$dir/app/entry" "$GH_API/repos/AndreyLear/avl/releases/tags" "$GH_DL/AndreyLear/avl/releases/download/v$v"
    cp "$REPO/install.sh" "$dir/install.sh"
    cp "$REPO/app/entry/install-entry.sh" "$REPO/app/entry/entry-sync.py" "$dir/app/entry/"
    sed "s/^IMAGE_VERSION=.*/IMAGE_VERSION=$v/" "$WORK/versions.lock" > "$dir/versions.lock"
    (cd "$WORK/rel-src" && tar -czf "$GH_DL/AndreyLear/avl/releases/download/v$v/amnezia-vpn-server-$v.tar.gz" "amnezia-vpn-server-$v")
    printf '{"tag_name":"v%s","body":"amnezia-sha256: %s\\n"}\n' "$v" \
        "$(sha256 "$GH_DL/AndreyLear/avl/releases/download/v$v/amnezia-vpn-server-$v.tar.gz")" \
        > "$GH_API/repos/AndreyLear/avl/releases/tags/v$v"
}
rm -rf "$GH_API" "$GH_DL"
publish_real_release 2.13.0
mkdir -p "$ED/state"
printf '{"version": "2.13.0", "request": ""}\n' > "$ED/state/update-request.json"
# update зовёт install.sh с окружением синка; хуки харнесса — через него же.
: > "$CALLS"
(
    export AMNEZIA_INSTALL_TEST=1 AMNEZIA_INSTALL_FAKE_DIR="$FAKE" \
        AMNEZIA_INSTALL_OS_RELEASE="$WORK/os-release" \
        AMNEZIA_INSTALL_SYSTEMD_DIR="$WORK/systemd" \
        AMNEZIA_INSTALL_SYSCTL_DIR="$WORK/sysctl" \
        AMNEZIA_INSTALL_MODULES_DIR="$WORK/modules" \
        AMNEZIA_INSTALL_FAIL2BAN_JAIL="$JAIL" \
        AMNEZIA_INSTALL_UNAME_M=x86_64 \
        AMNEZIA_INSTALL_XRAY_BASE="file://$WORK/xray-rel" \
        AMNEZIA_INSTALL_GEO_BASE="file://$GEO" \
        AMNEZIA_INSTALL_NGINX_SITES_DIR="$WORK/sites" \
        AMNEZIA_INSTALL_LETSENCRYPT_DIR="$WORK/le" \
        AMNEZIA_INSTALL_ACME_ROOT="$WORK/acme" \
        AMNEZIA_INSTALL_ENTRY_STUB_ROOT="$WORK/stub"
    run_update
)
rc=$?
check "самообновление настоящим установщиком: прошло, версия 2.13.0" test "$rc" = 0 -a "$(cat "$ED/release")" = 2.13.0
check "самообновление: установщик выпуска выполнился" grep -q 'settings: keeping' "$ED/state/update-log.txt"
check "самообновление: fail2ban так и выключен" f2b_off
check "самообновление: выбор в settings.json прежний" f2b_setting false

# Явный флаг меняет выбор — и он тоже запоминается.
: > "$CALLS"
run_install --fail2ban
check "--fail2ban: fail2ban включён" f2b_on
check "--fail2ban: выбор записан в settings.json" f2b_setting true
: > "$CALLS"
run_install
check "повторный запуск после --fail2ban: fail2ban включён" f2b_on
: > "$CALLS"
run_install --no-fail2ban
check "снова --no-fail2ban: выбор записан в settings.json" f2b_setting false
: > "$CALLS"
run_install
check "повторный запуск после --no-fail2ban: fail2ban не трогали" test "$(calls 'fail2ban')" = 0

# --- 10. сторож каскада (amnezia-vpn-server-q1do.5) ----------------------------------
#
# xray-entry закрыт от смерти Restart=always, от зависания — ничем. Сторож
# раз в минуту спрашивает у Xray статистику через его API и проверяет, что
# вход принимает TCP; два отказа подряд — перезапуск, между перезапусками
# пауза. Недоступный основной сервер поводом не служит: перезапуск входа
# его не вернёт. Пока идёт самообновление, сторож не вмешивается.

reset_host
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - \
    --entry-dest 127.0.0.1:4441 --entry-server-names a.example.ru
check "сторож: юнит — oneshot entry-sync.py watchdog" \
    grep -q "python3 -I $ED/entry-sync.py watchdog" "$WORK/systemd/xray-entry-watchdog.service"
check "сторож: таймер раз в минуту" grep -q '^OnUnitActiveSec=1min$' "$WORK/systemd/xray-entry-watchdog.timer"
check "сторож: таймер включён вместе с прочими" grep -q 'enable --now xray-entry-sync.timer xray-entry-geo.timer xray-entry-watchdog.timer' "$CALLS"
check "конфиг: API Xray на абстрактном сокете, только StatsService" \
    cfg 'c["api"] == {"tag": "api", "services": ["StatsService"]} and any(i["tag"] == "api" and i["listen"] == "@xray-entry-api" and i["protocol"] == "dokodemo-door" for i in c["inbounds"])'
check "конфиг: правило API — первым, до блока частных сетей" \
    cfg 'rules[0] == {"type": "field", "inboundTag": ["api"], "outboundTag": "api"}'

# Вход узла — слушатель на петле; settings.json указывает на него.
ENTRY_LISTEN_PID=""
entry_listen_start() {
    rm -f "$WORK/entry.port"
    python3 -I -c '
import socket, sys
s = socket.socket(); s.bind(("127.0.0.1", 0)); s.listen(16)
open(sys.argv[1], "w").write(str(s.getsockname()[1]))
while True:
    c, _ = s.accept(); c.close()' "$WORK/entry.port" &
    ENTRY_LISTEN_PID=$!
    for _ in $(seq 50); do [ -s "$WORK/entry.port" ] && break; sleep 0.1; done
    python3 -I -c 'import json,sys; p=sys.argv[1]; s=json.load(open(p)); s["port"]=int(sys.argv[2]); open(p,"w").write(json.dumps(s))' \
        "$ED/settings.json" "$(cat "$WORK/entry.port")"
    # Новый порт — новый конфиг: синк перезапустит Xray сейчас, а не
    # посреди проверок сторожа, где этот перезапуск сочли бы его.
    run_sync sync --offline
}
entry_listen_stop() {
    [ -z "$ENTRY_LISTEN_PID" ] || { kill "$ENTRY_LISTEN_PID" 2>/dev/null; wait "$ENTRY_LISTEN_PID" 2>/dev/null; ENTRY_LISTEN_PID=""; }
}
entry_listen_start
fake_proc 100000 0 0 0 0
mkdir -p "$WORK/proc/sys/kernel/random"
echo boot-1 > "$WORK/proc/sys/kernel/random/boot_id"
# run_watchdog [ENV=…] — entry-sync.py watchdog так, как его зовёт таймер.
run_watchdog() {
    env PATH="$FAKE:$PATH" AMNEZIA_ENTRY_DIR="$ED" AMNEZIA_ENTRY_PROC="$WORK/proc" "$@" \
        python3 -I "$ED/entry-sync.py" watchdog > "$WORK/watchdog.log" 2>&1
}
wd_restarts() { calls 'systemctl restart xray-entry'; }
wstate() { # wstate PYTHON-EXPR — над state/watchdog.json (w)
    python3 -I - "$ED/state/watchdog.json" "$1" <<'EOF'
import json, sys
w = json.load(open(sys.argv[1]))
sys.exit(0 if eval(sys.argv[2]) else 1)
EOF
}
wd_reset() { rm -f "$ED/state/watchdog.json" "$ED/state/update.json"; : > "$CALLS"; setstate XRAY_API_RC 0; }

wd_reset
run_watchdog
rc=$?
run_watchdog
check "исправный Xray: сторож выходит с нулём и молчит" test "$rc" = 0 -a ! -s "$WORK/watchdog.log"
check "исправный Xray: API спрошен через xray api statssys по абстрактному сокету" \
    test "$(calls 'xray-api api statssys --server=unix-abstract:xray-entry-api')" = 2
check "исправный Xray: перезапуска нет" test "$(wd_restarts)" = 0
run_sync sync
check "отчёт: перезапусков не было — watchdog пуст" report 'r["watchdog"] is None'

setstate XRAY_API_RC 1
run_watchdog
check "API молчит один раз: перезапуска нет" test "$(wd_restarts)" = 0
grep -q "отказ 1 из 2" "$WORK/watchdog.log" && pass "API молчит один раз: сказано вслух" || fail "API молчит один раз: сказано вслух"
run_watchdog
check "API молчит второй раз подряд: xray-entry перезапущен" test "$(wd_restarts)" = 1
check "причина и время перезапуска записаны" wstate 'w["restart_reason"] == "api" and w["restarted_at_utc"].endswith("Z")'
run_watchdog
run_watchdog
check "пауза: второй перезапуск не выпущен" test "$(wd_restarts)" = 1
grep -q "жду" "$WORK/watchdog.log" && pass "пауза: причина названа" || fail "пауза: причина названа"
run_watchdog AMNEZIA_ENTRY_WATCHDOG_RESTART_INTERVAL_SEC=0
check "истёкшая пауза: перезапуск снова разрешён" test "$(wd_restarts)" = 2
setstate XRAY_API_RC 0
run_sync sync
WD_AT="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["restarted_at_utc"])' "$ED/state/watchdog.json")"
check "отчёт: последний перезапуск — время и причина" \
    report 'r["watchdog"] == {"restarted_at_utc": "'"$WD_AT"'", "reason": "api"}'

wd_reset
run_watchdog
setstate XRAY_API_RC 1; run_watchdog
setstate XRAY_API_RC 0; run_watchdog
setstate XRAY_API_RC 1; run_watchdog
check "отказ, успех, отказ: перезапуска нет" test "$(wd_restarts)" = 0

# Проверка, ни разу не ответившая в этой загрузке системы, ничего не
# доказывает: старый конфиг без API, Xray, который ещё поднимается.
wd_reset
run_watchdog
echo boot-2 > "$WORK/proc/sys/kernel/random/boot_id"
setstate XRAY_API_RC 1
run_watchdog; run_watchdog; run_watchdog
check "API ни разу не ответил с загрузки: перезапуска нет" test "$(wd_restarts)" = 0
grep -q "ни разу не ответил" "$WORK/watchdog.log" && pass "и сказано, почему" || fail "и сказано, почему"
echo boot-1 > "$WORK/proc/sys/kernel/random/boot_id"

# Вход не принимает соединения — отказ и при живом API.
wd_reset
run_watchdog
entry_listen_stop
run_watchdog; run_watchdog
check "вход не принимает соединения: xray-entry перезапущен" test "$(wd_restarts)" = 1
check "причина — вход" wstate 'w["restart_reason"] == "port"'
entry_listen_start

# Основной сервер недоступен, а сам Xray жив — перезапуск не лечит.
wd_reset
echo 503 > "$WORK/panel.status"
run_watchdog
run_sync sync; run_watchdog
run_sync sync; run_watchdog
check "основной сервер недоступен, Xray отвечает: перезапуска нет" test "$(wd_restarts)" = 0
echo 200 > "$WORK/panel.status"

# Владелец остановил xray-entry (или systemd его поднимает) — не дело сторожа.
wd_reset
run_watchdog
rm -f "$WORK/active/xray-entry"
setstate XRAY_API_RC 1
run_watchdog; run_watchdog
check "xray-entry остановлен: сторож его не поднимает" test "$(wd_restarts)" = 0
touch "$WORK/active/xray-entry"

# Самообновление: установщик сам перезапускает Xray, сторож не вмешивается.
wd_reset
run_watchdog
printf '{"state": "updating", "target": "2.13.0"}\n' > "$ED/state/update.json"
setstate XRAY_API_RC 1
run_watchdog; run_watchdog; run_watchdog
check "идёт обновление (state updating): перезапуска нет" test "$(wd_restarts)" = 0
rm -f "$ED/state/update.json"
python3 -I -c 'import fcntl,sys,time; f=open(sys.argv[1],"w"); fcntl.flock(f, fcntl.LOCK_EX); open(sys.argv[2],"w").close(); time.sleep(30)' \
    "$ED/state/update.lock" "$WORK/wd-locked" &
LOCKER=$!
for _ in $(seq 50); do [ -e "$WORK/wd-locked" ] && break; sleep 0.1; done
run_watchdog; run_watchdog
check "идёт обновление (замок взят): перезапуска нет" test "$(wd_restarts)" = 0
kill "$LOCKER" 2>/dev/null; wait "$LOCKER" 2>/dev/null
rm -f "$WORK/wd-locked"
run_watchdog
check "после обновления счёт отказов начат заново: один отказ — не повод" test "$(wd_restarts)" = 0
run_watchdog
check "после обновления: второй отказ подряд — перезапуск" test "$(wd_restarts)" = 1
entry_listen_stop

# --- 11. связь с панелью по IPv4 и IPv6 (amnezia-vpn-server-q1do.6) ------------------------

# Инцидент 29.10.2026: TCP по IPv4 между каскадом и основным пропал, и
# синк падал «timed out», хотя IPv6 работал. Панель здесь — та же подделка
# на 127.0.0.1 и ::1 одним портом; что вернул бы DNS на имя панели,
# подменяет AMNEZIA_ENTRY_PANEL_ADDRS. Мёртвые адреса — 192.0.2.1 и 100::1
# (TEST-NET-1 и discard-префикс): SYN уходит в никуда, как в инциденте.
python3 -I "$WORK/panel.py" "$WORK" "$TOKEN" dual &
PANEL6_PID=$!
for _ in $(seq 50); do [ -s "$WORK/panel6.port" ] && break; sleep 0.1; done
PANEL6="http://localhost:$(cat "$WORK/panel6.port" 2>/dev/null)"

reset_host
printf '%s\n' "$TOKEN" | run_install --panel "$PANEL" --entry-token - \
    --entry-dest 127.0.0.1:4441 --entry-server-names a.example.ru
fake_proc 3600.50 1000 9000 100000 50000
run_sync settings --panel "$PANEL6"
check "IPv6: адрес панели — по имени" grep -q "\"panel_url\": \"$PANEL6\"" "$ED/settings.json"
rm -f "$ED/state/net.json"
# run_net АДРЕСА — синк, будто DNS вернул на имя панели эти адреса;
# попытка соединения, после которой берётся следующий адрес, — 1 с.
run_net() {
    : > "$WORK/panel.family"
    rm -f "$WORK/report.json"
    local t0
    t0=$(date +%s)
    env PATH="$FAKE:$PATH" AMNEZIA_ENTRY_DIR="$ED" AMNEZIA_ENTRY_GEO_BASE="file://$GEO" \
        AMNEZIA_ENTRY_PROC="$WORK/proc" AMNEZIA_ENTRY_DISK_PATH="$WORK" \
        AMNEZIA_ENTRY_PANEL_ADDRS="$1" AMNEZIA_ENTRY_CONNECT_TRY_SEC=1 \
        python3 -I "$ED/entry-sync.py" sync > "$WORK/sync.log" 2>&1
    NET_RC=$?
    NET_SEC=$(($(date +%s) - t0))
    { printf '=== sync через %s (rc=%s, %s с)\n' "$1" "$NET_RC" "$NET_SEC"; cat "$WORK/sync.log"; } >> "$WORK/all-out.log"
}
served() { # served PATH — семейство, по которому панель получила запрос
    awk -v p="$1" '$1 == p { f = $2 } END { print f }' "$WORK/panel.family"
}
net_family() { python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("family", ""))' "$ED/state/net.json" 2>/dev/null; }

run_net "127.0.0.1,::1"
check "оба семейства живы: синк прошёл" test "$NET_RC" = 0
check "оба семейства живы: запрос к панели — по IPv4, как раньше" test "$(served /api/entry/config)" = v4
check "оба семейства живы: отчёт — тоже по IPv4" test "$(served /api/entry/report)" = v4
check "оба семейства живы: в отчёте family v4" report 'r.get("family") == "v4"'
check "сработавшее семейство запомнено в state/net.json, 0600" test "$(net_family)" = v4 -a "$(mode_of "$ED/state/net.json")" = 0o600

run_net "192.0.2.1,::1"
check "IPv4 не проходит: синк прошёл по IPv6" test "$NET_RC" = 0 -a "$(served /api/entry/config)" = v6
check "IPv4 не проходит: отчёт сразу по IPv6" test "$(served /api/entry/report)" = v6
check "IPv4 не проходит: в отчёте family v6" report 'r.get("family") == "v6"'
check "IPv4 не проходит: на IPv4 — короткая попытка, а не весь срок запроса" test "$NET_SEC" -lt 8
check "IPv4 не проходит: IPv6 запомнен" test "$(net_family)" = v6
if grep -q "недоступна" "$WORK/sync.log"; then fail "IPv4 не проходит: в журнале тихо"; else pass "IPv4 не проходит: в журнале тихо"; fi

run_net "127.0.0.1,::1"
check "оба живы, в прошлый раз сработал IPv6: он и первый" \
    test "$NET_RC" = 0 -a "$(served /api/entry/config)" = v6 -a "$(net_family)" = v6

run_net "127.0.0.1,100::1"
check "IPv6 пропал, IPv4 есть: синк прошёл по IPv4 и запомнил его" \
    test "$NET_RC" = 0 -a "$(served /api/entry/config)" = v4 -a "$(net_family)" = v4

# Только IPv4 (у имени нет AAAA) — как раньше.
run_net "127.0.0.1"
check "только IPv4: синк как раньше" test "$NET_RC" = 0 -a "$(served /api/entry/config)" = v4

printf '{"family": "v9"}\n' > "$ED/state/net.json"
run_net "127.0.0.1,::1"
check "испорченный state/net.json: как без него — IPv4 первым" \
    test "$NET_RC" = 0 -a "$(served /api/entry/config)" = v4 -a "$(net_family)" = v4

# HTTPS: соединение — с адресом, а SNI и проверка сертификата — по имени
# панели. Подделка на ::1 читает ClientHello и закрывает соединение.
sni_ok="$(env AMNEZIA_ENTRY_DIR="$ED" AMNEZIA_ENTRY_PANEL_ADDRS="192.0.2.1,::1" AMNEZIA_ENTRY_CONNECT_TRY_SEC=1 \
    python3 -I - "$ED/entry-sync.py" <<'EOF'
import importlib.util, socket, ssl, sys, threading
sys.dont_write_bytecode = True; spec = importlib.util.spec_from_file_location("es", sys.argv[1]); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
srv = socket.socket(socket.AF_INET6, socket.SOCK_STREAM)
srv.bind(("::1", 0)); srv.listen(1)
hello = []
def serve():
    c, _ = srv.accept()
    c.settimeout(5)
    hello.append(c.recv(4096))
    c.close()
threading.Thread(target=serve, daemon=True).start()
try:
    m.panel_request({"panel_url": "https://panel.example.test:%d" % srv.getsockname()[1]}, m.CONFIG_PATH, timeout=5)
except Exception:
    pass
ctx = m._Https()._context
verify = ctx is None or (ctx.check_hostname and ctx.verify_mode == ssl.CERT_REQUIRED)
ok = hello and hello[0][:1] == b"\x16" and b"panel.example.test" in hello[0] and verify
print("ok" if ok else "bad %r" % (hello,))
EOF
)"
[ "$sni_ok" = ok ] && pass "HTTPS по адресу: SNI и проверка сертификата — по имени панели" \
    || fail "HTTPS по адресу: SNI и проверка сертификата — по имени панели ($sni_ok)"

echo
if [ "$ERRORS" -gt 0 ]; then
    printf 'entry: %d проверок не прошли\n' "$ERRORS"
    exit 1
fi
printf 'entry: все проверки прошли\n'
exit 0

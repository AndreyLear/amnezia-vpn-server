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
cleanup() {
    [ -z "$PANEL_PID" ] || { kill "$PANEL_PID" 2>/dev/null; wait "$PANEL_PID" 2>/dev/null; }
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
import http.server, os, sys
work, token = sys.argv[1], sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_GET(self):
        ok = self.headers.get("Authorization") == "Bearer " + token
        with open(os.path.join(work, "panel.log"), "a") as f:
            f.write("%s auth=%s\n" % (self.path, "ok" if ok else "bad"))
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
srv = http.server.HTTPServer(("127.0.0.1", 0), H)
open(os.path.join(work, "panel.port"), "w").write(str(srv.server_port))
srv.serve_forever()
EOF
echo 200 > "$WORK/panel.status"
python3 -I "$WORK/panel.py" "$WORK" "$TOKEN" &
PANEL_PID=$!
for _ in $(seq 50); do [ -s "$WORK/panel.port" ] && break; sleep 0.1; done
PANEL="http://127.0.0.1:$(cat "$WORK/panel.port")"

# --- запуск -------------------------------------------------------------------

ED="$WORK/opt/xray-entry"
reset_host() {
    rm -rf "$WORK/opt" "$WORK/systemd" "$WORK/sysctl" "$WORK/modules" "$WORK/f2b" "$WORK/sites" \
        "$WORK/le" "$WORK/acme" "$WORK/stub" "$WORK/active" "$WORK/panel.log"
    mkdir -p "$WORK/sites"
    : > "$CALLS"
    echo 200 > "$WORK/panel.status"
    printf 'XRAY_TEST_RC=0\nNGINX_T_RC=0\nCERTBOT_RC=0\nXRAY_START_RC=0\n' > "$STATE"
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
check "конфиг: правило по user ведёт клиента в его outbound" cfg '{"type": "field", "user": ["c1"], "outboundTag": "upstream-c1"} in rules'
check "конфиг: частные сети и QUIC — в блок, российское — напрямую" \
    cfg 'rules[0] == {"type": "field", "ip": ["geoip:private"], "outboundTag": "block"} and rules[1] == {"type": "field", "network": "udp", "port": "443", "outboundTag": "block"} and rules[2]["outboundTag"] == "direct" and "geosite:category-ru" in rules[2]["domain"] and "domain:xn--p1ai" in rules[2]["domain"] and rules[3] == {"type": "field", "ip": ["geoip:ru"], "outboundTag": "direct"}'
check "конфиг: всё прочее — в блок, напрямую — только IPv4" \
    cfg 'rules[-1] == {"type": "field", "network": "tcp,udp", "outboundTag": "block"} and outs["direct"]["streamSettings"]["sockopt"]["domainStrategy"] == "UseIPv4"'
check "конфиг: без журнала доступа" cfg 'c["log"]["access"] == "none"'
check "юниты: Xray, синхронизация раз в минуту, geo раз в неделю" \
    grep -q "ExecStart=$ED/xray run -c $ED/config.json" "$WORK/systemd/xray-entry.service"
check "юниты: таймер синхронизации — раз в минуту" grep -q '^OnUnitActiveSec=1min$' "$WORK/systemd/xray-entry-sync.timer"
check "юниты: таймер geo-списков" grep -q '^OnCalendar=Mon 04:30$' "$WORK/systemd/xray-entry-geo.timer"
check "юниты: синхронизация зовёт entry-sync.py sync" grep -q "python3 -I $ED/entry-sync.py sync" "$WORK/systemd/xray-entry-sync.service"
check "sysctl: BBR, fq, mtu_probing, буферы" grep -q 'net.ipv4.tcp_congestion_control = bbr' "$WORK/sysctl/90-vpn-entry-tcp.conf"
check "sysctl: mtu_probing" grep -q 'net.ipv4.tcp_mtu_probing = 1' "$WORK/sysctl/90-vpn-entry-tcp.conf"
check "fail2ban: jail для sshd" grep -q '^\[sshd\]' "$WORK/f2b/jail.d/amnezia-vpn-sshd.conf"
check "запуск: xray-entry запущен, таймеры включены" test "$(calls 'systemctl start xray-entry')" = 1 -a "$(calls 'enable --now xray-entry-sync.timer xray-entry-geo.timer')" = 1
check "запуск: без лишнего перезапуска" test "$(calls 'systemctl restart xray-entry')" = 0
check "панель: секрет в заголовке, не в адресе" test "$(cat "$WORK/panel.log")" = "/api/entry/config auth=ok"
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

echo
if [ "$ERRORS" -gt 0 ]; then
    printf 'entry: %d проверок не прошли\n' "$ERRORS"
    exit 1
fi
printf 'entry: все проверки прошли\n'
exit 0

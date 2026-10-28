#!/usr/bin/env python3
"""Узел-вход VPN (роль entry установщика, amnezia-vpn-server-14y0.6).

Российский вход — сервер без панели и базы: принимает VLESS+XHTTP+Reality
от клиентов внутри страны, российское (geosite/geoip ru, .ru/.su/.рф)
пускает напрямую, остальное отправляет на основной сервер. Клиентов он не
заводит сам: раз в минуту забирает у панели список {client_id, uuid,
enabled} и дорогу до основного сервера (GET /api/entry/config, секрет узла
в заголовке Authorization) и собирает из них свой конфиг Xray.

Поведение повторяет ручную настройку на 62.109.31.63 (build.py,
update-geo.sh, 28.10.2026):
  - каждый клиент уходит на основной сервер под СВОИМ UUID (outbound
    upstream-c<id> и правило по user): основной сервер сам считает трафик,
    график и последнюю активность и для дороги через вход;
  - частные сети и QUIC (udp/443) — в блок, российское — напрямую, с
    UseIPv4 (у входа нет IPv6: без этого первое соединение ждёт отказа v6);
  - конфиг меняется, только если изменился, и только после xray run -test;
    перезапуск — только при изменении;
  - нет связи с панелью — узел работает на последнем конфиге.

Ключи Reality узла живут только здесь, в settings.json (0600); панель о них
не знает ничего, кроме публичного ключа в ссылках. Секрет узла — в файле
token (0600) и только в заголовке запроса: ни в адресе, ни в журнале.

Подкоманды:
  sync                       забрать конфигурацию у панели, собрать, применить
  geo [--no-restart]         обновить geoip.dat/geosite.dat (сверка sha256)
  settings [флаги]           создать или поправить settings.json
  describe                   параметры узла для `panel xray entry set`
  install-xray --version V --sha256 H --arch A
                             поставить закреплённый Xray (сверка sha256 архива)
  pubkey                     публичный ключ Reality из settings.json

Каталог — AMNEZIA_ENTRY_DIR (по умолчанию /opt/xray-entry).
"""
import base64
import fcntl
import hashlib
import json
import os
import re
import secrets
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile

D = os.environ.get("AMNEZIA_ENTRY_DIR", "/opt/xray-entry")
SERVICE = "xray-entry"
CONFIG_PATH = "/api/entry/config"
XRAY_BASE = os.environ.get("AMNEZIA_ENTRY_XRAY_BASE", "https://github.com/XTLS/Xray-core/releases/download")
GEO_BASE = os.environ.get("AMNEZIA_ENTRY_GEO_BASE",
                          "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download")
GEO_FILES = ("geoip.dat", "geosite.dat")

# Маршруты «напрямую» по умолчанию — как на 62.109.31.63: российские сайты
# из списка Loyalsoldier, домены .ru/.su/.рф и российские адреса.
DEFAULT_DIRECT_DOMAINS = ["geosite:category-ru", "domain:ru", "domain:su", "domain:xn--p1ai"]
DEFAULT_DIRECT_IPS = ["geoip:ru"]

# Заглушка роли для --entry-domain: nginx на петле с настоящим
# сертификатом этого имени (как self-steal основного сервера).
STUB_DEST = "127.0.0.1:4433"

UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
SHORT_ID_RE = re.compile(r"^(?:[0-9a-f]{2}){0,8}$")
PATH_RE = re.compile(r"^/[A-Za-z0-9._~/-]*$")
HOST_RE = re.compile(r"^[A-Za-z0-9.:-]{1,253}$")
NAME_RE = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$")
KEY_RE = re.compile(r"^[A-Za-z0-9_-]{43}$")
CLIENT_EMAIL_RE = re.compile(r"^c[0-9]+$")


class Fail(Exception):
    """Ошибка, о которой достаточно одной строки в журнале."""


def log(msg):
    print("entry-sync: " + msg, flush=True)


def warn(msg):
    print("entry-sync: " + msg, file=sys.stderr, flush=True)


def path(name):
    return os.path.join(D, name)


# --- файлы -------------------------------------------------------------

def write_atomic(dest, data, mode=0o600):
    """Рядом, fsync, rename, fsync каталога: полузаписанного файла не бывает."""
    directory = os.path.dirname(dest) or "."
    fd, tmp = tempfile.mkstemp(prefix="." + os.path.basename(dest) + ".", dir=directory)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data if isinstance(data, bytes) else data.encode("utf-8"))
            f.flush()
            os.fsync(f.fileno())
        os.chmod(tmp, mode)
        os.replace(tmp, dest)
    except BaseException:
        try:
            os.unlink(tmp)
        except FileNotFoundError:
            pass
        raise
    dfd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(dfd)
    finally:
        os.close(dfd)


def read_json(name):
    with open(name, encoding="utf-8") as f:
        return json.load(f)


def dump(obj):
    return json.dumps(obj, indent=1, ensure_ascii=False) + "\n"


def sha256_file(name):
    h = hashlib.sha256()
    with open(name, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


def lock():
    """Синхронизация и обновление списков не должны перезапускать Xray
    одновременно и писать config.json вперемешку."""
    f = open(path(".lock"), "w")
    fcntl.flock(f, fcntl.LOCK_EX)
    return f


# --- X25519 (RFC 7748) -------------------------------------------------
# Ключи Reality без внешних пакетов: в стандартной библиотеке X25519 нет,
# а разбирать вывод `xray x25519` хрупко — его формат менялся от версии к
# версии.

_P = 2 ** 255 - 19


def x25519(scalar, u):
    k = bytearray(scalar)
    k[0] &= 248
    k[31] &= 127
    k[31] |= 64
    n = int.from_bytes(k, "little")
    ub = bytearray(u)
    ub[31] &= 127
    x1 = int.from_bytes(ub, "little")
    x2, z2, x3, z3, swap = 1, 0, x1, 1, 0
    for t in reversed(range(255)):
        bit = (n >> t) & 1
        swap ^= bit
        if swap:
            x2, x3, z2, z3 = x3, x2, z3, z2
        swap = bit
        a, b = x2 + z2, x2 - z2
        aa, bb = a * a % _P, b * b % _P
        e = (aa - bb) % _P
        c, d = x3 + z3, x3 - z3
        da, cb = d * a % _P, c * b % _P
        x3 = (da + cb) ** 2 % _P
        z3 = x1 * (da - cb) ** 2 % _P
        x2 = aa * bb % _P
        z2 = e * (aa + 121665 * e) % _P
    if swap:
        x2, z2 = x3, z3
    return (x2 * pow(z2, _P - 2, _P) % _P).to_bytes(32, "little")


def b64u(raw):
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def unb64u(text):
    return base64.urlsafe_b64decode(text + "=" * (-len(text) % 4))


def public_key(private_key):
    if not KEY_RE.match(private_key or ""):
        raise Fail("private_key в settings.json — не 32 байта base64url")
    raw = unb64u(private_key)
    if len(raw) != 32:
        raise Fail("private_key в settings.json — не 32 байта base64url")
    return b64u(x25519(raw, (9).to_bytes(32, "little")))


def new_private_key():
    raw = bytearray(secrets.token_bytes(32))
    raw[0] &= 248
    raw[31] &= 127
    raw[31] |= 64
    return b64u(bytes(raw))


# --- settings.json -----------------------------------------------------

def valid_dest(dest):
    host, sep, port = (dest or "").rpartition(":")
    return bool(sep) and bool(host) and port.isdigit() and 1 <= int(port) <= 65535 and \
        HOST_RE.match(host.strip("[]")) is not None


def check_settings(s):
    """Всё, без чего Xray не поднимется или ссылки клиентов разойдутся."""
    if not isinstance(s, dict):
        raise Fail("settings.json — не объект JSON")
    pub = public_key(s.get("private_key", ""))
    if s.get("public_key") and s["public_key"] != pub:
        raise Fail("public_key в settings.json не соответствует private_key")
    if not SHORT_ID_RE.match(s.get("short_id", "x")):
        raise Fail("short_id в settings.json — не 0–16 шестнадцатеричных цифр чётной длины")
    if not PATH_RE.match(s.get("path", "")):
        raise Fail("path в settings.json должен начинаться с / и состоять из безопасных для URL символов")
    names = s.get("server_names")
    if not isinstance(names, list) or not names or not all(isinstance(n, str) and NAME_RE.match(n) for n in names):
        raise Fail("server_names в settings.json — пустой список или не имена хостов")
    if not valid_dest(s.get("dest", "")):
        raise Fail("dest в settings.json — не host:port")
    if s.get("xver", 0) not in (0, 1, 2):
        raise Fail("xver в settings.json — не 0, 1 или 2")
    port = s.get("port", 443)
    if not isinstance(port, int) or not 1 <= port <= 65535:
        raise Fail("port в settings.json — не порт")
    for c in s.get("extra_clients", []) or []:
        if not isinstance(c, dict) or not UUID_RE.match(str(c.get("id", ""))) or \
                CLIENT_EMAIL_RE.match(str(c.get("email", ""))):
            raise Fail("extra_clients в settings.json: нужен id-UUID и email не вида c<число>")
    url = s.get("panel_url", "")
    if url:
        check_panel_url(url)
    return pub


def check_panel_url(url):
    """https — всегда; http — только на петлю (разработка и харнесс)."""
    u = urllib.parse.urlsplit(url)
    loopback = u.hostname in ("127.0.0.1", "localhost", "::1")
    if u.scheme != "https" and not (u.scheme == "http" and loopback):
        raise Fail("адрес панели должен быть https://")
    if not u.hostname or u.query or u.fragment or u.username or u.password:
        raise Fail("адрес панели — только https://имя[:порт][/путь], без запроса и учётных данных")


def load_settings():
    try:
        s = read_json(path("settings.json"))
    except FileNotFoundError:
        raise Fail("нет %s: узел не настроен (install.sh --role entry)" % path("settings.json"))
    except (OSError, ValueError) as e:
        raise Fail("settings.json не читается: %s" % e.__class__.__name__)
    check_settings(s)
    return s


def cmd_settings(args):
    """Создать settings.json или поправить в нём то, что передано флагами.

    Ключи Reality, shortId и путь создаются один раз и потом не меняются
    никогда: они уже в ссылках у клиентов. Перенос ручной настройки —
    --import: файл берётся как есть, проверяется и становится settings.json.
    """
    opts = parse_flags(args, {"--panel", "--domain", "--dest", "--server-names", "--xver", "--import"})
    target = path("settings.json")
    if "--import" in opts:
        src = opts["--import"]
        try:
            s = read_json(src)
        except (OSError, ValueError) as e:
            raise Fail("импорт %s: файл не читается как JSON (%s)" % (src, e.__class__.__name__))
    elif os.path.exists(target):
        s = read_json(target)
    else:
        if "--domain" not in opts and not ("--dest" in opts and "--server-names" in opts):
            raise Fail("новому узлу нужна маскировка: --entry-domain или --entry-dest с --entry-server-names")
        s = {"listen": "::", "port": 443,
             "path": "/" + secrets.token_hex(6),
             "private_key": new_private_key(),
             "short_id": secrets.token_hex(8),
             "direct_domains": DEFAULT_DIRECT_DOMAINS, "direct_ips": DEFAULT_DIRECT_IPS,
             "block_quic": True, "extra_clients": []}
    if "--domain" in opts:
        d = opts["--domain"]
        s.update({"server_names": [d], "dest": STUB_DEST, "xver": 0, "link_host": d, "link_server_name": d})
    if "--dest" in opts:
        s["dest"] = opts["--dest"]
    if "--server-names" in opts:
        s["server_names"] = [n.strip() for n in opts["--server-names"].split(",") if n.strip()]
    if "--xver" in opts:
        if not opts["--xver"].isdigit():
            raise Fail("--entry-xver — 0, 1 или 2")
        s["xver"] = int(opts["--xver"])
    if "--panel" in opts:
        s["panel_url"] = opts["--panel"].rstrip("/")
    s.setdefault("listen", "::")
    s.setdefault("port", 443)
    s.setdefault("direct_domains", DEFAULT_DIRECT_DOMAINS)
    s.setdefault("direct_ips", DEFAULT_DIRECT_IPS)
    s.setdefault("block_quic", True)
    s["public_key"] = check_settings(s)
    data = dump(s)
    old = open(target, encoding="utf-8").read() if os.path.exists(target) else None
    if data != old:
        write_atomic(target, data)
        log("settings.json записан")
    return 0


def cmd_describe(_args):
    """Параметры для `panel xray entry set`: по ним панель выдаёт ссылки."""
    s = load_settings()
    host = s.get("link_host") or s["server_names"][0]
    sni = s.get("link_server_name") or s["server_names"][0]
    print("host=%s" % host)
    print("port=%d" % s.get("link_port", s.get("port", 443)))
    print("sni=%s" % sni)
    print("public_key=%s" % public_key(s["private_key"]))
    print("short_id=%s" % s["short_id"])
    print("path=%s" % s["path"])
    return 0


def cmd_pubkey(_args):
    print(public_key(load_settings()["private_key"]))
    return 0


# --- ответ панели ------------------------------------------------------

def check_panel(p):
    """Ответ панели, прежде чем по нему что-то строить. Битый ответ
    равносилен отсутствию связи: узел остаётся на прежнем конфиге."""
    if not isinstance(p, dict) or not isinstance(p.get("clients"), list):
        raise Fail("ответ панели без списка клиентов")
    seen = set()
    for c in p["clients"]:
        if not isinstance(c, dict) or not isinstance(c.get("client_id"), int) or \
                isinstance(c.get("client_id"), bool) or not UUID_RE.match(str(c.get("uuid", ""))) or \
                not isinstance(c.get("enabled"), bool):
            raise Fail("ответ панели: клиент не вида {client_id, uuid, enabled}")
        if c["client_id"] in seen:
            raise Fail("ответ панели: клиент повторяется")
        seen.add(c["client_id"])
    up = p.get("upstream")
    if not isinstance(up, dict) or not HOST_RE.match(str(up.get("address", ""))) or \
            not isinstance(up.get("port"), int) or not 1 <= up["port"] <= 65535 or \
            not PATH_RE.match(str(up.get("path", ""))) or not NAME_RE.match(str(up.get("server_name", ""))) or \
            not KEY_RE.match(str(up.get("public_key", ""))) or not SHORT_ID_RE.match(str(up.get("short_id", "x"))):
        raise Fail("ответ панели: дорога до сервера неполна")
    return p


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    # Перенаправление унесло бы заголовок Authorization на другой адрес.
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise urllib.error.HTTPError(req.full_url, code, "перенаправление не принимается", headers, fp)


def fetch(settings):
    url = settings.get("panel_url", "")
    if not url:
        raise Fail("адрес панели не задан (install.sh --role entry --panel …)")
    check_panel_url(url)
    try:
        token = open(path("token"), encoding="utf-8").read().strip()
    except FileNotFoundError:
        raise Fail("секрета узла нет (install.sh --role entry --entry-token …)")
    req = urllib.request.Request(url.rstrip("/") + CONFIG_PATH, headers={
        "Authorization": "Bearer " + token, "Accept": "application/json",
        "User-Agent": "amnezia-entry-sync"})
    opener = urllib.request.build_opener(_NoRedirect)
    try:
        with opener.open(req, timeout=20) as r:
            body = r.read(1 << 20)
    except urllib.error.HTTPError as e:
        hint = {401: ": секрет узла не принят (panel xray entry token …)",
                429: ": слишком часто", 503: ": на сервере не настроен Xray"}.get(e.code, "")
        raise Fail("панель ответила %d%s" % (e.code, hint))
    except (urllib.error.URLError, OSError) as e:
        raise Fail("панель недоступна: %s" % getattr(e, "reason", e.__class__.__name__))
    try:
        return check_panel(json.loads(body))
    except ValueError:
        raise Fail("ответ панели — не JSON")


# --- конфиг Xray ---------------------------------------------------------

def reality_out(up, uuid, tag):
    return {"tag": tag, "protocol": "vless",
            "settings": {"vnext": [{"address": up["address"], "port": up["port"],
                                    "users": [{"id": uuid, "encryption": "none"}]}]},
            "streamSettings": {"network": "xhttp", "xhttpSettings": {"path": up["path"], "mode": "auto"},
                               "security": "reality",
                               "realitySettings": {"serverName": up["server_name"], "fingerprint": "chrome",
                                                   "publicKey": up["public_key"], "shortId": up["short_id"]}}}


def build(s, panel):
    """Конфиг Xray узла. panel — ответ панели или None (ещё ни разу не
    получен): тогда узел поднимается без клиентов и показывает маскировку."""
    users = [c for c in (panel or {}).get("clients", []) if c["enabled"]]
    up = (panel or {}).get("upstream")
    extra = s.get("extra_clients", []) or []
    clients = [{"id": c["uuid"], "email": "c%d" % c["client_id"]} for c in users] + extra
    rules = [{"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}]
    if s.get("block_quic", True):
        rules.append({"type": "field", "network": "udp", "port": "443", "outboundTag": "block"})
    rules += [{"type": "field", "domain": s.get("direct_domains", DEFAULT_DIRECT_DOMAINS), "outboundTag": "direct"},
              {"type": "field", "ip": s.get("direct_ips", DEFAULT_DIRECT_IPS), "outboundTag": "direct"}]
    outbounds = []
    if up:
        # Каждый клиент — под своим UUID: основной сервер считает его трафик
        # и для дороги через вход.
        for c in users:
            tag = "upstream-c%d" % c["client_id"]
            rules.append({"type": "field", "user": ["c%d" % c["client_id"]], "outboundTag": tag})
            outbounds.append(reality_out(up, c["uuid"], tag))
    # Служебная учётка входа на основном сервере (upstream.id в ручной
    # настройке) — только для своих учёток узла (extra_clients, например
    # проба): у клиентов панели дорога своя. Нет её — всё прочее в блок.
    service_id = (s.get("upstream") or {}).get("id", "")
    if up and extra and UUID_RE.match(service_id or ""):
        outbounds.append(reality_out(up, service_id, "upstream"))
        rules.append({"type": "field", "network": "tcp,udp", "outboundTag": "upstream"})
    else:
        rules.append({"type": "field", "network": "tcp,udp", "outboundTag": "block"})
    outbounds += [{"tag": "direct", "protocol": "freedom",
                   # У входа нет IPv6: без этого Xray сперва пробует v6-адрес
                   # российского сайта и теряет время на первом соединении.
                   "streamSettings": {"sockopt": {"domainStrategy": "UseIPv4"}}},
                  {"tag": "block", "protocol": "blackhole"}]
    return {
        # Журнал доступа не ведётся: на узле он рос бы без ротации, а
        # трафик по клиентам считает основной сервер. Ошибки — в journald.
        "log": {"loglevel": "warning", "access": "none"},
        "inbounds": [{"tag": "entry", "listen": s.get("listen", "::"), "port": s.get("port", 443),
                      "protocol": "vless",
                      "settings": {"clients": clients, "decryption": "none"},
                      "streamSettings": {"network": "xhttp",
                                         "xhttpSettings": {"path": s["path"], "mode": "auto"},
                                         "security": "reality",
                                         "realitySettings": {"target": s["dest"], "xver": s.get("xver", 0),
                                                             "serverNames": s["server_names"],
                                                             "privateKey": s["private_key"],
                                                             "shortIds": [s["short_id"]]}},
                      "sniffing": {"enabled": True, "destOverride": ["http", "tls", "quic"]}}],
        "outbounds": outbounds,
        "routing": {"domainStrategy": "IPIfNonMatch", "rules": rules},
    }


def xray_test(config_file, assets=None):
    env = dict(os.environ, XRAY_LOCATION_ASSET=assets or D)
    t = subprocess.run([path("xray"), "run", "-test", "-c", config_file],
                       capture_output=True, text=True, env=env)
    return t.returncode == 0, (t.stdout + t.stderr).strip()


def restart():
    subprocess.run(["systemctl", "restart", SERVICE], check=True)


def apply(cfg):
    """True — конфиг сменился и Xray перезапущен."""
    new = dump(cfg)
    try:
        old = open(path("config.json"), encoding="utf-8").read()
    except FileNotFoundError:
        old = ""
    if new == old:
        return False
    pending = path("config.new.json")
    write_atomic(pending, new)
    ok, out = xray_test(pending)
    if not ok:
        os.unlink(pending)
        raise Fail("новый конфиг не прошёл xray run -test, оставлен прежний\n" + out)
    os.replace(pending, path("config.json"))
    if old:
        restart()
    return True


def cmd_sync(args):
    """Забрать, собрать, применить. Без связи с панелью — последний
    полученный ответ (state/panel.json); без него — конфиг не трогается,
    если он уже есть, а первый поднимается без клиентов."""
    opts = parse_flags(args, {"--offline"})
    with lock():
        s = load_settings()
        cache = path("state/panel.json")
        fetch_error = None
        panel = None
        if "--offline" not in opts:
            try:
                panel = fetch(s)
                os.makedirs(path("state"), mode=0o700, exist_ok=True)
                write_atomic(cache, dump(panel))
            except Fail as e:
                fetch_error = str(e)
                warn(fetch_error + "; работаю на последнем полученном списке")
        if panel is None:
            try:
                panel = check_panel(read_json(cache))
            except FileNotFoundError:
                if os.path.exists(path("config.json")):
                    warn("списка клиентов от панели ещё не было, конфиг оставлен как есть")
                    return 1
            except (Fail, ValueError, OSError):
                warn("сохранённый список клиентов повреждён, конфиг оставлен как есть")
                return 1
        cfg = build(s, panel)
        clients = len(cfg["inbounds"][0]["settings"]["clients"])
        if apply(cfg):
            log("конфиг обновлён, вход перезапущен; клиентов: %d" % clients)
        else:
            log("без изменений; клиентов: %d" % clients)
    return 1 if fetch_error else 0


# --- geo-списки ---------------------------------------------------------

def download(url, dest, limit):
    req = urllib.request.Request(url, headers={"User-Agent": "amnezia-entry-sync"})
    with urllib.request.urlopen(req, timeout=60) as r, open(dest, "wb") as f:
        total = 0
        for chunk in iter(lambda: r.read(1 << 16), b""):
            total += len(chunk)
            if total > limit:
                raise Fail("%s: больше %d байт" % (url, limit))
            f.write(chunk)


def cmd_geo(args):
    """Списки российских ресурсов (Loyalsoldier/v2ray-rules-dat). Каждый
    файл сверяется с опубликованной sha256; новые заменяют старые, только
    если Xray принимает с ними текущий конфиг."""
    opts = parse_flags(args, {"--no-restart"})
    with lock(), tempfile.TemporaryDirectory(prefix=".geo.", dir=D) as tmp:
        try:
            for name in GEO_FILES:
                download(GEO_BASE + "/" + name, os.path.join(tmp, name), 64 << 20)
                download(GEO_BASE + "/" + name + ".sha256sum", os.path.join(tmp, name + ".sha256sum"), 4096)
                want = open(os.path.join(tmp, name + ".sha256sum")).read().split()[:1]
                if want != [sha256_file(os.path.join(tmp, name))]:
                    raise Fail("%s не сошёлся с опубликованной sha256, оставляю прежние списки" % name)
        except (urllib.error.URLError, OSError) as e:
            raise Fail("списки не скачались (%s), оставляю прежние" % getattr(e, "reason", e.__class__.__name__))
        have_config = os.path.exists(path("config.json"))
        if have_config:
            ok, out = xray_test(path("config.json"), assets=tmp)
            if not ok:
                raise Fail("новые списки не прошли проверку конфига, оставляю прежние\n" + out)
        if all(os.path.exists(path(n)) and sha256_file(path(n)) == sha256_file(os.path.join(tmp, n))
               for n in GEO_FILES):
            log("списки не изменились")
            return 0
        for name in GEO_FILES:
            os.chmod(os.path.join(tmp, name), 0o644)
            os.replace(os.path.join(tmp, name), path(name))
        if have_config and "--no-restart" not in opts:
            restart()
            log("списки обновлены, вход перезапущен")
        else:
            log("списки обновлены")
    return 0


# --- Xray ------------------------------------------------------------------

def cmd_install_xray(args):
    """Закреплённая версия Xray: архив релиза сверяется с sha256 из
    versions.lock, бинарник — с версией. Печатает installed или unchanged."""
    opts = parse_flags(args, {"--version", "--sha256", "--arch"})
    version, want, arch = opts.get("--version", ""), opts.get("--sha256", ""), opts.get("--arch", "")
    if not re.match(r"^[0-9][0-9.]*$", version) or not re.match(r"^[0-9a-f]{64}$", want) or \
            not re.match(r"^[a-z0-9-]+$", arch):
        raise Fail("install-xray: нужны --version, --sha256 (64 hex) и --arch")
    marker = path("xray.release")
    stamp = "%s %s %s\n" % (version, arch, want)
    if os.path.exists(path("xray")) and os.path.exists(marker) and open(marker).read() == stamp:
        print("unchanged")
        return 0
    url = "%s/v%s/Xray-linux-%s.zip" % (XRAY_BASE, version, arch)
    with tempfile.TemporaryDirectory(prefix=".xray.", dir=D) as tmp:
        archive = os.path.join(tmp, "xray.zip")
        try:
            download(url, archive, 128 << 20)
        except (urllib.error.URLError, OSError) as e:
            raise Fail("Xray %s не скачался: %s" % (version, getattr(e, "reason", e.__class__.__name__)))
        got = sha256_file(archive)
        if got != want:
            raise Fail("архив Xray %s не сошёлся с sha256 из versions.lock (%s), ничего не заменено" % (version, got))
        try:
            with zipfile.ZipFile(archive) as z, z.open("xray") as src:
                binary = src.read()
        except (zipfile.BadZipFile, KeyError):
            raise Fail("в архиве Xray нет бинарника xray")
        fresh = path(".xray.new")
        write_atomic(fresh, binary, 0o755)
        out = subprocess.run([fresh, "version"], capture_output=True, text=True).stdout
        if not out.startswith("Xray %s " % version):
            os.unlink(fresh)
            raise Fail("бинарник из архива — не Xray %s" % version)
        os.replace(fresh, path("xray"))
    write_atomic(marker, stamp, 0o644)
    print("installed")
    return 0


# --- разбор аргументов ------------------------------------------------------

def parse_flags(args, known):
    out = {}
    i = 0
    while i < len(args):
        a = args[i]
        if a not in known:
            raise Fail("неизвестный аргумент: %s" % a)
        if a in ("--offline", "--no-restart"):
            out[a] = ""
            i += 1
            continue
        if i + 1 >= len(args):
            raise Fail("%s: нужно значение" % a)
        out[a] = args[i + 1]
        i += 2
    return out


COMMANDS = {"sync": cmd_sync, "geo": cmd_geo, "settings": cmd_settings, "describe": cmd_describe,
            "install-xray": cmd_install_xray, "pubkey": cmd_pubkey}


def main(argv):
    if len(argv) < 1 or argv[0] not in COMMANDS:
        warn("использование: entry-sync.py {%s} [флаги]" % "|".join(sorted(COMMANDS)))
        return 2
    try:
        return COMMANDS[argv[0]](argv[1:])
    except Fail as e:
        warn(str(e))
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

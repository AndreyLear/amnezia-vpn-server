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
    UseIPv4 (так было на ручной настройке без IPv6; IPv6 у узла теперь
    может быть, но прямой выход остаётся на IPv4);
  - конфиг меняется, только если изменился, и только после xray run -test;
    перезапуск — только при изменении;
  - нет связи с панелью — узел работает на последнем конфиге.

После успешного синка узел отправляет панели свои показатели
(POST /api/entry/report тем же секретом, amnezia-vpn-server-q1do.1): CPU,
память, диск, трафик основного интерфейса за интервал между синками,
аптайм, версии Xray и выпуска, протокол, по которому ответила панель
(amnezia-vpn-server-q1do.6). Отчёт — не обязанность: панель старше
отчётов отвечает 404, и узел молча обходится без него; любой сбой отчёта
на синк и его код выхода не влияет.

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
  update                     обновить узел до выпуска из state/update-request.json
                             (юнит xray-entry-update.service, amnezia-vpn-server-q1do.2)
  watchdog                   сторож: перезапустить зависший xray-entry
                             (таймер xray-entry-watchdog.timer, amnezia-vpn-server-q1do.5)

Каталог — AMNEZIA_ENTRY_DIR (по умолчанию /opt/xray-entry).
"""
import base64
import calendar
import fcntl
import hashlib
import http.client
import json
import os
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

D = os.environ.get("AMNEZIA_ENTRY_DIR", "/opt/xray-entry")
SERVICE = "xray-entry"
CONFIG_PATH = "/api/entry/config"
REPORT_PATH = "/api/entry/report"
# Откуда брать показатели: /proc и корень — у узла; харнесс подменяет.
PROC = os.environ.get("AMNEZIA_ENTRY_PROC", "/proc")
DISK_PATH = os.environ.get("AMNEZIA_ENTRY_DISK_PATH", "/")
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
VERSION_RE = re.compile(r"^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$")

# Самообновление (amnezia-vpn-server-q1do.2). Откуда брать выпуск, зашито
# здесь, а не приходит от панели: секрет узла могли украсть, и его
# владелец не должен уметь подсунуть узлу свой архив. От панели — только
# номер выпуска, и тот проверяется строго: три числа без ведущих нулей.
# Переменные окружения — для харнесса; на узле их задаёт только юнит.
UPDATE_REPO = os.environ.get("AMNEZIA_ENTRY_UPDATE_REPO", "AndreyLear/avl")
UPDATE_API = os.environ.get("AMNEZIA_ENTRY_UPDATE_API", "https://api.github.com")
UPDATE_DOWNLOADS = os.environ.get("AMNEZIA_ENTRY_UPDATE_DOWNLOADS", "https://github.com")
SYSTEMD_DIR = os.environ.get("AMNEZIA_ENTRY_SYSTEMD_DIR", "/etc/systemd/system")
UPDATE_UNIT = "xray-entry-update.service"
# После неудачи — не чаще раза в полчаса: сломанный выпуск не должен
# каждую минуту качать архив и дёргать Xray.
UPDATE_PAUSE = int(os.environ.get("AMNEZIA_ENTRY_UPDATE_PAUSE_SEC", "1800"))
# Срок установщика: обычно он укладывается в минуту, но apt-get на
# свежем сервере ждёт блокировку dpkg до десяти минут.
INSTALL_TIMEOUT = int(os.environ.get("AMNEZIA_ENTRY_UPDATE_INSTALL_TIMEOUT_SEC", "1800"))
RELEASE_RE = re.compile(r"^(?:0|[1-9][0-9]{0,5})\.(?:0|[1-9][0-9]{0,5})\.(?:0|[1-9][0-9]{0,5})$")
REQUEST_RE = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")
SHA_IN_RELEASE_RE = re.compile(r"amnezia-sha256: ([0-9a-f]{64})")
# Причины неудачи — коды, а не текст: панель показывает свои слова, а не
# то, что прислал узел.
UPDATE_REASONS = ("download", "checksum", "archive", "install", "inactive", "rollback", "interrupted", "version")

# Сторож (amnezia-vpn-server-q1do.5): те же правила, что у watchdog.sh
# основного сервера — два отказа подряд, пауза между перезапусками.
WATCHDOG_THRESHOLD = 2
WATCHDOG_RESTART_INTERVAL = int(os.environ.get("AMNEZIA_ENTRY_WATCHDOG_RESTART_INTERVAL_SEC", "600"))
# Причины перезапуска — коды, как у обновления: слова — у панели.
WATCHDOG_REASONS = ("api", "port")
# API Xray узла — абстрактный Unix-сокет: файла нет, и брошенный после
# SIGKILL сокет не помешает следующему запуску (у основного сервера такой
# файл приходится убирать панели, internal/xray/socket.go). Права на него
# не ставятся, поэтому API открыт только на чтение: StatsService, без
# HandlerService, который меняет пользователей.
API_SOCKET = "@xray-entry-api"
API_TARGET = "unix-abstract:" + API_SOCKET[1:]


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
    if not isinstance(s.get("fail2ban", True), bool):
        raise Fail("fail2ban в settings.json — не true или false")
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
    opts = parse_flags(args, {"--panel", "--domain", "--dest", "--server-names", "--xver", "--import", "--fail2ban"})
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
    # Выбор --[no-]fail2ban — решение владельца для узла, а не одного запуска:
    # самообновление зовёт install.sh без флагов и иначе снова включало бы
    # fail2ban (amnezia-vpn-server-0b80). Нет ключа — включён.
    if "--fail2ban" in opts:
        if opts["--fail2ban"] not in ("0", "1"):
            raise Fail("--fail2ban — 0 или 1")
        s["fail2ban"] = opts["--fail2ban"] == "1"
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


def read_token():
    try:
        return open(path("token"), encoding="utf-8").read().strip()
    except FileNotFoundError:
        raise Fail("секрета узла нет (install.sh --role entry --entry-token …)")


# --- связь с панелью по IPv4 и IPv6 (amnezia-vpn-server-q1do.6) -------------
# Инцидент 29.10.2026: TCP по IPv4 между каскадом и основным пропал на 13 ч,
# IPv6 при этом работал, но urllib брал первый адрес из getaddrinfo и ждал
# его весь срок запроса — синк падал «timed out». Теперь адреса имени панели
# (A и AAAA) перебираются сами: семейство, сработавшее в прошлый раз
# (state/net.json), первым, без него — IPv4, как было; семейства чередуются
# (RFC 8305, 4); на каждый адрес, кроме последнего, — CONNECT_TRY секунд.
# Сокет соединяется с адресом, а HTTP и TLS идут по имени: заголовок Host,
# SNI и проверка сертификата — те же, что раньше.
#
# AMNEZIA_ENTRY_PANEL_ADDRS — только для харнесса: что «вернул бы DNS».
CONNECT_TRY = float(os.environ.get("AMNEZIA_ENTRY_CONNECT_TRY_SEC", "3"))
PANEL_ADDRS = os.environ.get("AMNEZIA_ENTRY_PANEL_ADDRS", "")
FAMILY_NAMES = {socket.AF_INET: "v4", socket.AF_INET6: "v6"}
# Семейство последнего удавшегося соединения с панелью в этом запуске.
_panel_family = None


def read_net_family():
    """Семейство, по которому панель ответила в прошлый раз, или None."""
    try:
        f = read_json(path("state/net.json")).get("family")
    except (OSError, ValueError, AttributeError):
        return None
    return f if f in ("v4", "v6") else None


def remember_net_family(family):
    if family and family != read_net_family():
        os.makedirs(path("state"), mode=0o700, exist_ok=True)
        write_atomic(path("state/net.json"), dump({"family": family}))


def panel_addresses(host, port):
    """[(семейство, sockaddr)] имени панели без повторов, в порядке DNS."""
    if PANEL_ADDRS:
        infos = []
        for a in PANEL_ADDRS.split(","):
            af = socket.AF_INET6 if ":" in a else socket.AF_INET
            infos.append((af, (a.strip(), port) if af == socket.AF_INET else (a.strip(), port, 0, 0)))
    else:
        infos = [(i[0], i[4]) for i in socket.getaddrinfo(host, port, socket.AF_UNSPEC, socket.SOCK_STREAM)]
    out = []
    for af, sa in infos:
        if af in FAMILY_NAMES and (af, sa) not in out:
            out.append((af, sa))
    return out


def order_addresses(addrs, first):
    """Чередование семейств, начиная с first (RFC 8305, 4)."""
    a = [x for x in addrs if FAMILY_NAMES[x[0]] == first]
    b = [x for x in addrs if FAMILY_NAMES[x[0]] != first]
    out = []
    for i in range(max(len(a), len(b))):
        out += a[i:i + 1] + b[i:i + 1]
    return out


def connect_panel(address, timeout=None, source_address=None):
    """Замена socket.create_connection для запросов к панели: адреса по
    очереди, короткая попытка на каждый, кроме последнего."""
    global _panel_family
    host, port = address[0], address[1]
    if not isinstance(timeout, (int, float)):
        timeout = None
    addrs = order_addresses(panel_addresses(host, port), read_net_family() or "v4")
    err = OSError("у имени %s нет адресов" % host)
    for i, (af, sa) in enumerate(addrs):
        s = socket.socket(af, socket.SOCK_STREAM)
        try:
            if source_address:
                s.bind(source_address)
            # Последнему адресу — весь срок: другого пути уже нет, а
            # медленная, но живая связь не должна обрываться раньше, чем
            # до этой правки.
            if i == len(addrs) - 1:
                s.settimeout(timeout)
            else:
                s.settimeout(CONNECT_TRY if timeout is None else min(CONNECT_TRY, timeout))
            s.connect(sa)
            s.settimeout(timeout)
        except OSError as e:
            s.close()
            err = e
            continue
        _panel_family = FAMILY_NAMES[af]
        return s
    raise err


def _panel_connection(base):
    class Conn(base):
        def __init__(self, *a, **kw):
            super().__init__(*a, **kw)
            # Шов http.client: только соединение сокета, всё прочее
            # (Host, TLS с server_hostname=имя) — как у base.
            self._create_connection = connect_panel
    return Conn


_PanelHTTP = _panel_connection(http.client.HTTPConnection)
_PanelHTTPS = _panel_connection(http.client.HTTPSConnection)


class _Http(urllib.request.HTTPHandler):
    def do_open(self, http_class, req, **kw):
        return super().do_open(_PanelHTTP, req, **kw)


class _Https(urllib.request.HTTPSHandler):
    def do_open(self, http_class, req, **kw):
        return super().do_open(_PanelHTTPS, req, **kw)


def panel_request(settings, api_path, body=None, timeout=20):
    """Запрос к панели с секретом узла в заголовке, без перенаправлений.
    body — JSON для POST. Возвращает тело ответа; ошибки — как у urllib."""
    url = settings.get("panel_url", "")
    if not url:
        raise Fail("адрес панели не задан (install.sh --role entry --panel …)")
    check_panel_url(url)
    headers = {"Authorization": "Bearer " + read_token(), "Accept": "application/json",
               "User-Agent": "amnezia-entry-sync"}
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url.rstrip("/") + api_path, data=data, headers=headers)
    opener = urllib.request.build_opener(_NoRedirect, _Http, _Https)
    with opener.open(req, timeout=timeout) as r:
        out = r.read(1 << 20)
    # Запомнить семейство — только когда панель ответила: соединиться мало,
    # если дальше по этому пути ничего не проходит.
    try:
        remember_net_family(_panel_family)
    except OSError as e:
        warn("не запомнено, по какому протоколу ответила панель: %s" % e.__class__.__name__)
    return out


def fetch(settings):
    try:
        body = panel_request(settings, CONFIG_PATH)
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


# --- показатели узла (amnezia-vpn-server-q1do.1) --------------------------
# Те же числа, что панель показывает про свой сервер: CPU — доля занятого
# времени между двумя запусками по /proc/stat, память — занятое по
# MemAvailable, диск — занятое по statvfs (зарезервированное root тоже
# недоступно). CPU и трафик — разница с state/metrics.json прошлого
# запуска; часы — /proc/uptime: аптайм заодно отличает перезагрузку (он
# меньше прежнего, счётчики сброшены), и разницы тогда нет, а не
# отрицательная. Всё, что не прочиталось, уходит пустым (null) — панель
# покажет прочерк.

def proc_text(name):
    with open(os.path.join(PROC, name), encoding="utf-8") as f:
        return f.read()


def read_uptime():
    try:
        return float(proc_text("uptime").split()[0])
    except (OSError, ValueError, IndexError):
        return None


def read_cpu():
    """Сумма счётчиков, idle и iowait строки cpu; None — не прочиталось."""
    try:
        for line in proc_text("stat").splitlines():
            f = line.split()
            if f and f[0] == "cpu":
                vals = [int(v) for v in f[1:9]]
                if len(vals) < 4:
                    return None
                return {"total": sum(vals), "idle": vals[3], "iowait": vals[4] if len(vals) > 4 else 0}
    except (OSError, ValueError):
        pass
    return None


def cpu_percent(prev, cur):
    # iowait может уменьшаться (так его считает ядро), поэтому сброс
    # счётчиков проверяется только по занятому времени — как в hostmetrics
    # панели.
    busy_prev = prev["total"] - prev["idle"] - prev["iowait"]
    busy_cur = cur["total"] - cur["idle"] - cur["iowait"]
    if busy_cur < busy_prev:
        return None
    busy = busy_cur - busy_prev
    idle = max(0, cur["idle"] - prev["idle"]) + max(0, cur["iowait"] - prev["iowait"])
    if busy + idle == 0:
        return None
    return round(min(100.0, busy * 100.0 / (busy + idle)), 1)


def read_memory():
    try:
        info = {}
        for line in proc_text("meminfo").splitlines():
            key, _, rest = line.partition(":")
            if key in ("MemTotal", "MemAvailable") and rest.split():
                info[key] = int(rest.split()[0]) * 1024
        total, avail = info["MemTotal"], info["MemAvailable"]
    except (OSError, ValueError, KeyError):
        return None, None, None
    if total <= 0:
        return None, None, None
    used = max(0, total - avail)
    return round(used * 100.0 / total, 1), used, total


def read_disk():
    try:
        st = os.statvfs(DISK_PATH)
    except OSError:
        return None, None, None
    total = st.f_blocks * st.f_frsize
    if total <= 0:
        return None, None, None
    used = max(0, total - st.f_bavail * st.f_frsize)
    return round(used * 100.0 / total, 1), used, total


def main_iface():
    """Интерфейс маршрута по умолчанию с наименьшей метрикой: через него
    узел и принимает клиентов, и ходит на основной сервер."""
    best = None
    try:
        for line in proc_text("net/route").splitlines()[1:]:
            f = line.split()
            if len(f) >= 7 and f[1] == "00000000" and int(f[3], 16) & 1:
                metric = int(f[6])
                if best is None or metric < best[0]:
                    best = (metric, f[0])
    except (OSError, ValueError):
        return None
    return best[1] if best else None


def read_iface_bytes(iface):
    try:
        for line in proc_text("net/dev").splitlines()[2:]:
            name, sep, rest = line.partition(":")
            f = rest.split()
            if sep and name.strip() == iface and len(f) >= 9:
                return int(f[0]), int(f[8])
    except (OSError, ValueError):
        pass
    return None


def release_version():
    """Версия выпуска, с которого поставлен узел: install-entry.sh пишет
    IMAGE_VERSION из versions.lock в файл release."""
    try:
        v = open(path("release"), encoding="utf-8").read().strip()
    except OSError:
        return None
    return v if VERSION_RE.match(v) else None


def xray_version():
    """Версия Xray — из отметки install-xray: «версия архитектура sha256»."""
    try:
        v = (open(path("xray.release"), encoding="utf-8").read().split() or [""])[0]
    except OSError:
        return None
    return v if VERSION_RE.match(v) else None


def collect_metrics(prev):
    """(отчёт, состояние для следующего запуска); prev — прошлое
    состояние или {}."""
    up = read_uptime()
    cpu = read_cpu()
    iface = main_iface()
    counters = read_iface_bytes(iface) if iface else None
    ram_pct, ram_used, ram_total = read_memory()
    disk_pct, disk_used, disk_total = read_disk()
    report = {"cpu_percent": None, "ram_percent": ram_pct, "ram_used_bytes": ram_used, "ram_total_bytes": ram_total,
              "disk_percent": disk_pct, "disk_used_bytes": disk_used, "disk_total_bytes": disk_total,
              "rx_bytes": None, "tx_bytes": None, "interval_seconds": None,
              "uptime_seconds": int(up) if up is not None else None,
              "xray_version": xray_version(), "release": release_version(),
              "update": update_report(), "watchdog": watchdog_report(),
              # По какому протоколу панель ответила на запрос конфига в
              # этом синке: v4 | v6 (amnezia-vpn-server-q1do.6).
              "family": read_net_family()}
    prev_up = prev.get("uptime")
    # Тот же запуск системы — аптайм вырос. Иначе перезагрузка или первый
    # запуск, и разницы нет.
    same_boot = isinstance(prev_up, (int, float)) and up is not None and up > prev_up
    if same_boot and cpu and isinstance(prev.get("cpu"), dict):
        try:
            report["cpu_percent"] = cpu_percent(prev["cpu"], cpu)
        except (KeyError, TypeError):
            pass
    pnet = prev.get("net")
    if same_boot and counters and isinstance(pnet, dict) and pnet.get("iface") == iface:
        try:
            rx, tx = counters[0] - int(pnet["rx"]), counters[1] - int(pnet["tx"])
        except (KeyError, TypeError, ValueError):
            rx = tx = -1
        if rx >= 0 and tx >= 0:
            report.update(rx_bytes=rx, tx_bytes=tx, interval_seconds=int(round(up - prev_up)))
    state = {"uptime": up, "cpu": cpu,
             "net": {"iface": iface, "rx": counters[0], "tx": counters[1]} if counters else None}
    return report, state


def send_report(settings):
    """Собрать показатели и отправить панели. Ничего не бросает: отчёт не
    должен мешать синку."""
    state_file = path("state/metrics.json")
    try:
        prev = read_json(state_file)
        if not isinstance(prev, dict):
            prev = {}
    except (OSError, ValueError):
        prev = {}
    try:
        report, state = collect_metrics(prev)
        os.makedirs(path("state"), mode=0o700, exist_ok=True)
        write_atomic(state_file, dump(state))
    except Exception as e:  # сбой сбора не должен ронять синк
        warn("показатели не собраны: %s (работе узла это не мешает)" % e.__class__.__name__)
        return
    try:
        panel_request(settings, REPORT_PATH, body=report, timeout=10)
    except urllib.error.HTTPError as e:
        # 404/405 — панель старше отчётов: молча, она их и не ждёт.
        if e.code not in (404, 405):
            warn("панель не приняла показатели: ответила %d (работе узла это не мешает)" % e.code)
    except (urllib.error.URLError, OSError) as e:
        warn("показатели не отправлены — панель недоступна: %s (работе узла это не мешает)"
             % getattr(e, "reason", e.__class__.__name__))
    except Fail as e:
        warn("показатели не отправлены: %s (работе узла это не мешает)" % e)


# --- самообновление (amnezia-vpn-server-q1do.2) ------------------------------
# Кто что решает. Панель называет целевой выпуск (target_version: по
# умолчанию свою версию) и, если владелец нажал «Обновить каскад», метку
# этого запроса (update_request). Всё остальное решает узел: выпуск ли это
# вообще (три числа), новее ли установленного (понижать — нет), не рано
# ли после неудачи (пауза UPDATE_PAUSE; свежий запрос владельца её
# пропускает один раз). Откуда качать и с чем сверять — тоже узел: адрес
# репозитория зашит выше, сумма — из тела выпуска на GitHub, как у
# update-agent.sh основного сервера. Адрес или сумма в ответе панели не
# читаются вовсе.
#
# Синк сам ничего не ставит: кладёт state/update-request.json и будит
# отдельный oneshot-юнит xray-entry-update.service (entry-sync.py update).
# Установщик выпуска заменяет и этот файл, и юниты, и перезапускает Xray —
# внутри синка с его сроком в 50 секунд и общим замком это бы не
# уживалось; у update-agent.sh основного сервера установщик к тому же
# переписывал исполняемый скрипт (amnezia-vpn-server-76mp.4). Python читает
# файл целиком до запуска, а установщик кладёт его через rename, так что
# исполняемое под ногами не меняется.
#
# Итог — state/update.json: state idle | updating | failed, target, reason
# (код из UPDATE_REASONS), at_utc, attempt_utc (начало последней попытки —
# от неё пауза), request (последний обработанный запрос владельца). Он
# уходит панели в каждом отчёте.

class UpdateFail(Exception):
    """Неудача обновления: code — для панели, str — для журнала."""

    def __init__(self, code, msg):
        super().__init__(msg)
        self.code = code


def release_tuple(v):
    if not isinstance(v, str) or not RELEASE_RE.match(v):
        return None
    return tuple(int(x) for x in v.split("."))


def utc_now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def utc_seconds(stamp):
    try:
        return calendar.timegm(time.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ"))
    except (TypeError, ValueError):
        return None


def read_update_state():
    try:
        st = read_json(path("state/update.json"))
    except (OSError, ValueError):
        st = {}
    if not isinstance(st, dict) or st.get("state") not in ("idle", "updating", "failed"):
        st = {"state": "idle"}
    return st


def write_update_state(st):
    os.makedirs(path("state"), mode=0o700, exist_ok=True)
    write_atomic(path("state/update.json"), dump(st))


def update_report():
    """Состояние обновления для отчёта панели — только проверенные поля."""
    st = read_update_state()
    reason = st.get("reason") if st.get("reason") in UPDATE_REASONS else ""
    return {"state": st["state"],
            "target": st.get("target") if release_tuple(st.get("target")) else "",
            "reason": reason if st["state"] == "failed" else "",
            "at_utc": st.get("at_utc") if utc_seconds(st.get("at_utc")) is not None else "",
            "request": st.get("request") if REQUEST_RE.match(str(st.get("request") or "")) else ""}


def update_lock(blocking):
    """Замок обновителя; None — его держит другой прогон."""
    os.makedirs(path("state"), mode=0o700, exist_ok=True)
    f = open(path("state/update.lock"), "w")
    try:
        fcntl.flock(f, fcntl.LOCK_EX | (0 if blocking else fcntl.LOCK_NB))
    except BlockingIOError:
        f.close()
        return None
    return f


def installed_release():
    v = release_version()
    return v if release_tuple(v) else None


def plan_update(panel):
    """Решить по ответу панели, просить ли обновление. Ничего не бросает
    наружу: обновление не должно мешать синку."""
    raw_target = panel.get("target_version")
    target = raw_target if release_tuple(raw_target) else None
    if raw_target is not None and target is None:
        warn("панель назвала целевым выпуском не номер выпуска; обновление пропущено")
    req = panel.get("update_request")
    req = req if isinstance(req, str) and REQUEST_RE.match(req) else ""
    st = read_update_state()
    before = dict(st)
    if st["state"] == "updating":
        held = update_lock(blocking=False)
        if held is None:
            return  # идёт
        held.close()
        # Замок свободен, а итога нет: прогон умер на полпути (срок юнита,
        # перезагрузка). Это неудача — с паузой, как любая другая.
        log("прежнее обновление не дошло до конца")
        st.update(state="failed", reason="interrupted", at_utc=utc_now(), attempt_utc=utc_now())
        write_update_state(st)
    fresh_request = bool(req) and req != st.get("request")
    if fresh_request:
        st["request"] = req
    installed = installed_release()
    if not target or not installed or release_tuple(target) <= release_tuple(installed):
        if target and installed and release_tuple(target) < release_tuple(installed):
            log("панель просит выпуск %s, а стоит %s; понижать не буду" % (target, installed))
        if st["state"] == "failed" and target and installed and release_tuple(installed) >= release_tuple(target):
            # Догнали вручную — прежняя неудача больше не про этот узел.
            st.update(state="idle", reason="", at_utc=utc_now())
        if st != before:
            write_update_state(st)
        return
    if st["state"] == "failed" and not fresh_request:
        last = utc_seconds(st.get("attempt_utc"))
        if last is not None and time.time() - last < UPDATE_PAUSE:
            return
    write_atomic(path("state/update-request.json"), dump({"version": target, "request": req}))
    write_update_state(st)
    log("запрошено обновление %s -> %s" % (installed, target))
    r = subprocess.run(["systemctl", "start", "--no-block", UPDATE_UNIT], capture_output=True, text=True)
    if r.returncode != 0:
        warn("юнит обновления не запустился: %s" % (r.stderr.strip() or r.returncode))


def safe_extract(archive, dest):
    """Только файлы и каталоги внутри dest: ни ссылок, ни устройств, ни
    путей наверх. Сумма архива уже сверена, но распаковщик — последний,
    кто видит имена до диска."""
    with tarfile.open(archive, "r:gz") as t:
        members = t.getmembers()
        for m in members:
            parts = m.name.split("/")
            if m.name.startswith("/") or ".." in parts or not (m.isfile() or m.isdir()):
                raise UpdateFail("archive", "в архиве выпуска недопустимый элемент: %r" % m.name)
        for m in members:
            target = os.path.join(dest, m.name)
            if m.isdir():
                os.makedirs(target, mode=0o755, exist_ok=True)
                continue
            os.makedirs(os.path.dirname(target), mode=0o755, exist_ok=True)
            with t.extractfile(m) as src, open(target, "wb") as out:
                shutil.copyfileobj(src, out)
            os.chmod(target, 0o755 if m.mode & 0o111 else 0o644)


def entry_units():
    try:
        names = os.listdir(SYSTEMD_DIR)
    except OSError:
        return []
    return sorted(n for n in names if n.startswith("xray-entry") and n.endswith((".service", ".timer")))


# Что откат не трогает: состояние обновления (его пишет этот прогон) и
# замок синка (его держит этот же прогон на время возврата).
KEEP_ON_ROLLBACK = ("state", ".lock")


def snapshot(backup):
    """Копия каталога узла и его юнитов перед установкой."""
    shutil.rmtree(backup, ignore_errors=True)
    os.makedirs(os.path.join(backup, "units"), mode=0o700)
    shutil.copytree(D, os.path.join(backup, "entry"), symlinks=True,
                    ignore=lambda d, names: [n for n in names if d == D and n in KEEP_ON_ROLLBACK])
    for unit in entry_units():
        shutil.copy2(os.path.join(SYSTEMD_DIR, unit), os.path.join(backup, "units", unit))


def xray_active():
    return subprocess.run(["systemctl", "is-active", "--quiet", SERVICE]).returncode == 0


def rollback(backup):
    """Вернуть каталог узла и юниты из копии и поднять Xray. True — Xray
    работает на прежнем."""
    with lock():
        saved = os.path.join(backup, "entry")
        for name in os.listdir(D):
            if name in KEEP_ON_ROLLBACK:
                continue
            p = path(name)
            if os.path.isdir(p) and not os.path.islink(p):
                shutil.rmtree(p)
            else:
                os.unlink(p)
        for name in os.listdir(saved):
            src = os.path.join(saved, name)
            if os.path.isdir(src) and not os.path.islink(src):
                shutil.copytree(src, path(name), symlinks=True)
            else:
                shutil.copy2(src, path(name), follow_symlinks=False)
        for unit in os.listdir(os.path.join(backup, "units")):
            shutil.copy2(os.path.join(backup, "units", unit), os.path.join(SYSTEMD_DIR, unit))
        subprocess.run(["systemctl", "daemon-reload"])
        subprocess.run(["systemctl", "restart", SERVICE])
    return xray_active()


def run_installer(installer):
    """install.sh --role entry из выпуска; код выхода, 124 — не уложился.
    Флагов нет: адрес панели, маскировка и ключи — в settings.json, секрет —
    в token, и повторный запуск берёт их оттуда."""
    env = dict(os.environ, AMNEZIA_INSTALL_ENTRY_DIR=D)
    logf = open(path("state/update-log.txt"), "a")
    try:
        p = subprocess.Popen(["bash", installer, "--role", "entry"], cwd=os.path.dirname(installer), env=env,
                             stdin=subprocess.DEVNULL, stdout=logf, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            return p.wait(timeout=INSTALL_TIMEOUT)
        except subprocess.TimeoutExpired:
            # Вместе со всем, что он запустил: потомки иначе продолжили бы
            # менять узел без присмотра.
            os.killpg(p.pid, signal.SIGKILL)
            p.wait()
            return 124
    finally:
        logf.close()


def fetch_release(target, work):
    """Скачать архив выпуска target и сверить с суммой из тела выпуска.
    Возвращает каталог установщика."""
    meta = os.path.join(work, "release.json")
    tarball = os.path.join(work, "sources.tar.gz")
    try:
        download("%s/repos/%s/releases/tags/v%s" % (UPDATE_API, UPDATE_REPO, target), meta, 1 << 20)
    except (urllib.error.URLError, OSError) as e:
        raise UpdateFail("download", "описание выпуска v%s не получено: %s"
                         % (target, getattr(e, "reason", e.__class__.__name__)))
    try:
        body = read_json(meta).get("body") or ""
    except (OSError, ValueError, AttributeError):
        raise UpdateFail("download", "описание выпуска v%s — не JSON" % target)
    m = SHA_IN_RELEASE_RE.search(body if isinstance(body, str) else "")
    if not m:
        raise UpdateFail("checksum", "в описании выпуска v%s нет контрольной суммы" % target)
    asset = "amnezia-vpn-server-%s.tar.gz" % target
    try:
        download("%s/%s/releases/download/v%s/%s" % (UPDATE_DOWNLOADS, UPDATE_REPO, target, asset), tarball, 256 << 20)
    except (urllib.error.URLError, OSError) as e:
        raise UpdateFail("download", "архив выпуска v%s не скачался: %s"
                         % (target, getattr(e, "reason", e.__class__.__name__)))
    if sha256_file(tarball) != m.group(1):
        raise UpdateFail("checksum", "контрольная сумма архива v%s не совпала с объявленной в выпуске" % target)
    src = os.path.join(work, "src")
    os.makedirs(src)
    try:
        safe_extract(tarball, src)
    except (tarfile.TarError, OSError, EOFError) as e:
        raise UpdateFail("archive", "архив выпуска не распаковался: %s" % e.__class__.__name__)
    top = os.path.join(src, "amnezia-vpn-server-%s" % target)
    installer = os.path.join(top, "install.sh")
    if not os.path.isfile(installer) or not os.path.isfile(os.path.join(top, "app/entry/install-entry.sh")):
        raise UpdateFail("archive", "в архиве выпуска нет install.sh с ролью entry")
    # Архив обязан быть тем выпуском, что назван: установщик пишет в
    # release версию из своего versions.lock.
    try:
        lock_text = open(os.path.join(top, "versions.lock"), encoding="utf-8").read()
    except OSError:
        lock_text = ""
    if ("IMAGE_VERSION=%s" % target) not in lock_text.splitlines():
        raise UpdateFail("archive", "versions.lock архива не называет выпуск %s" % target)
    return installer


def do_update(installed, target):
    work = tempfile.mkdtemp(prefix=".xray-entry-update.", dir=os.path.dirname(D.rstrip("/")) or "/")
    backup = D.rstrip("/") + ".rollback"
    try:
        installer = fetch_release(target, work)
        snapshot(backup)
        log("копия %s снята; ставлю выпуск %s" % (D, target))
        rc = run_installer(installer)
        if rc == 0 and not xray_active():
            failure = ("inactive", "после установки %s не работает" % SERVICE)
        elif rc == 0 and installed_release() != target:
            failure = ("install", "установщик завершился, но версия узла — не %s" % target)
        elif rc != 0:
            failure = ("install", "установщик выпуска %s завершился с кодом %d (журнал: %s)"
                       % (target, rc, path("state/update-log.txt")))
        else:
            shutil.rmtree(backup, ignore_errors=True)
            return
        log(failure[1] + "; возвращаю %s" % installed)
        try:
            ok = rollback(backup)
        except OSError as e:
            ok = False
            warn("откат прервался: %s" % e)
        if not ok:
            raise UpdateFail("rollback", failure[1] + "; ОТКАТ НЕ УДАЛСЯ, узлу нужен человек (копия: %s)" % backup)
        shutil.rmtree(backup, ignore_errors=True)
        raise UpdateFail(failure[0], failure[1] + "; узел вернулся на %s" % installed)
    finally:
        shutil.rmtree(work, ignore_errors=True)


def cmd_update(_args):
    """Обновитель: юнит xray-entry-update.service, запрос — от синка."""
    held = update_lock(blocking=False)
    if held is None:
        log("обновление уже идёт")
        return 0
    with held:
        req_file = path("state/update-request.json")
        try:
            req = read_json(req_file)
        except FileNotFoundError:
            log("запроса на обновление нет")
            return 0
        except (OSError, ValueError):
            req = {}
        # Запрос убирается сразу: исход у него один, повторять его будет
        # следующий синк, если сочтёт нужным.
        try:
            os.unlink(req_file)
        except FileNotFoundError:
            pass
        st = read_update_state()
        target = req.get("version") if isinstance(req, dict) else None
        installed = installed_release()
        if not release_tuple(target) or not installed or release_tuple(target) <= release_tuple(installed):
            warn("запрос на выпуск %r при установленном %s отклонён: только официальный выпуск новее установленного"
                 % (target, installed))
            st.update(state="failed", reason="version", at_utc=utc_now(), attempt_utc=utc_now(),
                      target=target if release_tuple(target) else "")
            write_update_state(st)
            return 1
        st.update(state="updating", target=target, reason="", at_utc=utc_now(), attempt_utc=utc_now())
        write_update_state(st)
        log("обновление %s -> %s" % (installed, target))
        try:
            do_update(installed, target)
        except UpdateFail as e:
            warn(str(e))
            st.update(state="failed", reason=e.code, at_utc=utc_now())
            write_update_state(st)
            return 1
        except Exception as e:  # что угодно ещё — тоже неудача, а не вечное updating
            warn("обновление прервано: %s" % e.__class__.__name__)
            st.update(state="failed", reason="interrupted", at_utc=utc_now())
            write_update_state(st)
            return 1
        st.update(state="idle", reason="", at_utc=utc_now())
        write_update_state(st)
        log("узел обновлён до %s" % target)
        return 0


# --- сторож (amnezia-vpn-server-q1do.5) -------------------------------------
# Restart=always у xray-entry закрывает смерть процесса, но не зависание:
# процесс жив, ядро принимает соединения, а клиенты стоят. Раз в минуту
# (xray-entry-watchdog.timer) сторож спрашивает у Xray статистику через его
# API — на это отвечает сам процесс — и проверяет, что вход принимает TCP.
# Два отказа подряд — перезапуск, между перезапусками — пауза: так же, как
# watchdog.sh основного сервера.
#
# Чего сторож нарочно не делает:
#   - не смотрит на основной сервер и панель: если недоступны они, а Xray
#     узла отвечает, перезапуск входа ничего не вернёт, только порвёт
#     соединения клиентов, у которых российские сайты идут напрямую;
#   - не поднимает остановленный xray-entry: упавший поднимает systemd, а
#     остановленный руками остановлен намеренно;
#   - не вмешивается в самообновление (state updating или взятый замок
#     обновителя): установщик сам перезапускает Xray и откатывается, и
#     перезапуск посреди этого спутал бы его проверку «работает ли Xray»;
#   - не верит проверке API, которая ни разу не ответила с загрузки
#     системы: так выглядят и Xray, который ещё поднимается, и конфиг,
#     собранный прежней версией без API. Перезапускать по ней раз в
#     10 минут — рвать связь без причины.
#
# Итог — state/watchdog.json; время и причина последнего перезапуска уходят
# панели в отчёте, «О версиях» показывает их у каскада.

def read_watchdog_state():
    try:
        st = read_json(path("state/watchdog.json"))
    except (OSError, ValueError):
        st = {}
    if not isinstance(st, dict):
        st = {}
    if not isinstance(st.get("fails"), int) or isinstance(st.get("fails"), bool):
        st["fails"] = 0
    return st


def write_watchdog_state(st):
    os.makedirs(path("state"), mode=0o700, exist_ok=True)
    write_atomic(path("state/watchdog.json"), dump(st))


def watchdog_report():
    """Последний перезапуск для отчёта панели; None — не было."""
    st = read_watchdog_state()
    at = st.get("restarted_at_utc")
    if utc_seconds(at) is None:
        return None
    reason = st.get("restart_reason")
    return {"restarted_at_utc": at, "reason": reason if reason in WATCHDOG_REASONS else ""}


def update_in_progress():
    if read_update_state()["state"] == "updating":
        return True
    held = update_lock(blocking=False)
    if held is None:
        return True
    held.close()
    return False


def boot_id():
    """Метка загрузки системы; без /proc — пустая, одна на все запуски."""
    try:
        return proc_text("sys/kernel/random/boot_id").strip()
    except OSError:
        return ""


def probe_api():
    try:
        r = subprocess.run([path("xray"), "api", "statssys", "--server=" + API_TARGET, "--timeout=5"],
                           stdin=subprocess.DEVNULL, capture_output=True, timeout=15)
    except (OSError, subprocess.TimeoutExpired):
        return False
    return r.returncode == 0


def probe_port(s):
    """Принимает ли вход TCP. Вход слушает все адреса — спрашиваем петлю."""
    listen = s.get("listen", "::")
    host = "127.0.0.1" if listen in ("", "::", "0.0.0.0") else listen
    try:
        socket.create_connection((host, s.get("port", 443)), timeout=5).close()
    except OSError:
        return False
    return True


def cmd_watchdog(_args):
    st = read_watchdog_state()
    before = dict(st)
    if update_in_progress():
        # Отказ до обновления и отказ после него — не «два подряд».
        st["fails"] = 0
        if st != before:
            write_watchdog_state(st)
        return 0
    if not xray_active():
        st["fails"] = 0
        if st != before:
            write_watchdog_state(st)
        return 0
    s = load_settings()
    boot = boot_id()
    reason = None
    if probe_api():
        st["api_boot"] = boot
    elif st.get("api_boot") == boot:
        reason = "api"
    else:
        log("API Xray ни разу не ответил с загрузки системы — его молчание не засчитывается")
    if reason is None and not probe_port(s):
        reason = "port"
    words = {"api": "API Xray не отвечает на запрос статистики",
             "port": "вход не принимает соединения на порту %s" % s.get("port", 443)}
    if reason is None:
        st["fails"] = 0
    else:
        st["fails"] += 1
        if st["fails"] < WATCHDOG_THRESHOLD:
            log("%s: отказ %d из %d (%s)" % (SERVICE, st["fails"], WATCHDOG_THRESHOLD, words[reason]))
        else:
            last = utc_seconds(st.get("restarted_at_utc"))
            age = int(time.time() - last) if last is not None else None
            if age is not None and age < WATCHDOG_RESTART_INTERVAL:
                log("%s всё ещё не работает (%s), но прошлый перезапуск был %d с назад — жду"
                    % (SERVICE, words[reason], age))
            else:
                # Под замком синка: не вперемешку с его перезапуском после
                # смены конфига и с откатом обновителя.
                with lock():
                    if update_in_progress():
                        st["fails"] = 0
                    else:
                        log("перезапускаю %s: %s" % (SERVICE, words[reason]))
                        r = subprocess.run(["systemctl", "restart", SERVICE], capture_output=True, text=True)
                        if r.returncode == 0:
                            st.update(fails=0, restarted_at_utc=utc_now(), restart_reason=reason)
                            log("%s перезапущен" % SERVICE)
                        else:
                            warn("перезапустить %s не удалось: %s" % (SERVICE, r.stderr.strip() or r.returncode))
    if st != before:
        write_watchdog_state(st)
    return 0


# --- конфиг Xray ---------------------------------------------------------

# Дорога до основного сервера по IPv4 и IPv6 (amnezia-vpn-server-q1do.6).
# Без sockopt Xray 26.x отдаёт имя системному dialer'у Go
# (transport/internet/dialer.go, DialSystem: sockopt == nil), и порядок
# семейств решает сортировка RFC 6724 на хосте с запасным через 300 мс —
# на узле с IPv6 это IPv6 первым, то есть смена пути при живом IPv4.
# Одна domainStrategy без happyEyeballs хуже: Xray берёт случайный адрес
# из ответа (dice.Roll) и с мёртвым IPv4 промахивался бы через раз. С
# domainStrategy UseIP Xray сам берёт A и AAAA (LookupForIP через
# системный резолвер: секции dns у узла нет), а happyEyeballs с
# tryDelayMs > 0 включает TcpRaceDial
# (transport/internet/happy_eyeballs.go): IPv4 первым (prioritizeIPv6
# false), следующий адрес — через 250 мс без ответа или сразу после отказа,
# побеждает первое установленное соединение. Живой IPv4 отвечает раньше —
# путь прежний; мёртвый — через 250 мс идёт IPv6. Нет AAAA или у узла нет
# IPv6 — один адрес или быстрый отказ v6, всё как раньше. UseIP, а не
# ForceIP: при сбое резолвера Xray, как и прежде, наберёт имя сам.
#
# Какое семейство первым. Панель и основной сервер — один хост, поэтому
# протокол, на котором панель ответила в этом синке (state/net.json),
# годится и для Xray: ответила по IPv6 — prioritizeIPv6 true, иначе с
# мёртвым IPv4 каждое новое соединение клиента теряло бы 250 мс (для XHTTP
# это заметно). Смена семейства меняет config.json, и apply() перезапускает
# Xray обычным путём «конфиг изменился»; то же семейство — тот же конфиг,
# без перезапуска.
def upstream_sockopt(v6_first):
    return {"domainStrategy": "UseIP",
            "happyEyeballs": {"tryDelayMs": 250, "prioritizeIPv6": bool(v6_first),
                              "interleave": 1, "maxConcurrentTry": 4}}


def reality_out(up, uuid, tag, v6_first=False):
    return {"tag": tag, "protocol": "vless",
            "settings": {"vnext": [{"address": up["address"], "port": up["port"],
                                    "users": [{"id": uuid, "encryption": "none"}]}]},
            "streamSettings": {"network": "xhttp", "xhttpSettings": {"path": up["path"], "mode": "auto"},
                               "sockopt": upstream_sockopt(v6_first),
                               "security": "reality",
                               "realitySettings": {"serverName": up["server_name"], "fingerprint": "chrome",
                                                   "publicKey": up["public_key"], "shortId": up["short_id"]}}}


def build(s, panel, v6_first=None):
    """Конфиг Xray узла. panel — ответ панели или None (ещё ни разу не
    получен): тогда узел поднимается без клиентов и показывает маскировку.
    v6_first — IPv6 первым к основному; None — по state/net.json."""
    if v6_first is None:
        v6_first = read_net_family() == "v6"
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
            outbounds.append(reality_out(up, c["uuid"], tag, v6_first))
    # Служебная учётка входа на основном сервере (upstream.id в ручной
    # настройке) — только для своих учёток узла (extra_clients, например
    # проба): у клиентов панели дорога своя. Нет её — всё прочее в блок.
    service_id = (s.get("upstream") or {}).get("id", "")
    if up and extra and UUID_RE.match(service_id or ""):
        outbounds.append(reality_out(up, service_id, "upstream", v6_first))
        rules.append({"type": "field", "network": "tcp,udp", "outboundTag": "upstream"})
    else:
        rules.append({"type": "field", "network": "tcp,udp", "outboundTag": "block"})
    outbounds += [{"tag": "direct", "protocol": "freedom",
                   # Российские сайты — только по IPv4, как на ручной
                   # настройке 62.109.31.63, где IPv6 не было: без этого
                   # Xray сперва пробовал v6-адрес сайта и терял время на
                   # первом соединении. IPv6 у каскада теперь может быть
                   # (для дороги к основному, amnezia-vpn-server-q1do.6), но
                   # прямой выход по нему не проверен — поведение прежнее.
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
                      "sniffing": {"enabled": True, "destOverride": ["http", "tls", "quic"]}},
                     # Как у основного сервера (internal/xray/config.go):
                     # dokodemo-door на сокете; адрес в settings ему нужен
                     # лишь для формы, наружу ничего не уходит.
                     {"tag": "api", "listen": API_SOCKET, "protocol": "dokodemo-door",
                      "settings": {"address": "127.0.0.1", "port": 10085, "network": "unix"}}],
        "outbounds": outbounds,
        "routing": {"domainStrategy": "IPIfNonMatch",
                    # API — первым: правило частных сетей перехватило бы его
                    # адрес 127.0.0.1 и отправило в блок.
                    "rules": [{"type": "field", "inboundTag": ["api"], "outboundTag": "api"}] + rules},
        # API для сторожа (amnezia-vpn-server-q1do.5): «процесс жив» и «порт
        # открыт» зависание не ловят, ответ на gRPC-вызов — ловит.
        "api": {"tag": "api", "services": ["StatsService"]},
        "stats": {},
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
        fetched = False
        if "--offline" not in opts:
            try:
                panel = fetch(s)
                fetched = True
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
        try:
            cfg = build(s, panel)
            clients = len(cfg["inbounds"][0]["settings"]["clients"])
            if apply(cfg):
                log("конфиг обновлён, вход перезапущен; клиентов: %d" % clients)
            else:
                log("без изменений; клиентов: %d" % clients)
        finally:
            # Показатели — только после успешного ответа панели: при
            # неверном секрете или без связи отчёт лишь тратил бы попытки
            # из лимита панели (amnezia-vpn-server-q1do.1). После apply —
            # чтобы новый конфиг не ждал отчёта.
            if fetched:
                # Обновление — до отчёта: метка обработанного запроса
                # владельца уходит панели в этом же синке
                # (amnezia-vpn-server-q1do.2). Его сбой синку не мешает.
                try:
                    plan_update(panel)
                except Exception as e:
                    warn("обновление не запрошено: %s (работе узла это не мешает)" % e.__class__.__name__)
                send_report(s)
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
            "install-xray": cmd_install_xray, "pubkey": cmd_pubkey, "update": cmd_update,
            "watchdog": cmd_watchdog}


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

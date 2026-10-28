#!/bin/bash
#
# install-entry.sh — роль entry установщика: узел-вход VPN
# (amnezia-vpn-server-14y0.6). Запускается из install.sh:
#
#   ./install.sh --role entry --panel https://panel.example.com \
#       --entry-token - [--entry-domain ru.example.com |
#       --entry-dest 127.0.0.1:4441 --entry-server-names a.ru,www.a.ru --entry-xver 1]
#
# Узел-вход — сервер без панели, Docker и базы: Xray закреплённой версии
# (бинарник из релиза XTLS, архив сверяется с sha256 из versions.lock),
# раз в минуту — синхронизация с панелью (entry-sync.py sync: клиенты и
# дорога до основного сервера по секрету узла), раз в неделю — списки
# российских ресурсов, sysctl для мобильных клиентов, fail2ban для SSH.
# Это ровно то, что 28.10.2026 было собрано руками на 62.109.31.63 в
# /opt/xray-entry; каталог, имена юнитов и settings.json те же, поэтому
# установка поверх ручной настройки забирает её на месте, не меняя ключей
# Reality, shortId, пути и имён — ссылки у клиентов остаются прежними.
#
# Маскировка Reality — одно из двух:
#   --entry-domain FQDN  своя заглушка: nginx на 127.0.0.1:4433 с
#                        сертификатом Let's Encrypt этого имени (TCP 80
#                        для HTTP-01); ссылки — на это имя;
#   --entry-dest HOST:PORT --entry-server-names N1,N2 [--entry-xver 0|1|2]
#                        уже работающий на этом сервере сайт: чужие
#                        подключения уходят на него (xver 1 — с PROXY
#                        protocol, если сайт слушает с proxy_protocol).
# На повторном запуске флаги не обязательны: всё записано в settings.json.
#
# Секрет узла (--entry-token, выдаёт `panel xray entry set|token`): «-» —
# прочитать со стандартного ввода, чтобы он не остался в истории оболочки
# и в списке процессов. Хранится в <каталог>/token (0600); в журнал и на
# экран не выводится.
#
# Хуки для харнесса (окружение):
#   AMNEZIA_INSTALL_TEST=1               без проверки root
#   AMNEZIA_INSTALL_FAKE_DIR=DIR         DIR в начало PATH (подделки
#                                        systemctl/apt-get/sysctl/nginx/…)
#   AMNEZIA_INSTALL_OS_RELEASE=FILE      вместо /etc/os-release
#   AMNEZIA_INSTALL_ENTRY_DIR=DIR        каталог узла (/opt/xray-entry)
#   AMNEZIA_INSTALL_SYSTEMD_DIR=DIR      юниты (/etc/systemd/system)
#   AMNEZIA_INSTALL_SYSCTL_DIR=DIR       sysctl.d (/etc/sysctl.d)
#   AMNEZIA_INSTALL_MODULES_DIR=DIR      modules-load.d (/etc/modules-load.d)
#   AMNEZIA_INSTALL_FAIL2BAN_JAIL=FILE   jail для sshd
#   AMNEZIA_INSTALL_FAIL2BAN_BIN=NAME    бинарник, по которому видно fail2ban
#   AMNEZIA_INSTALL_VERSIONS_LOCK=FILE   versions.lock (рядом с install.sh)
#   AMNEZIA_INSTALL_UNAME_M=ARCH         вместо `uname -m`
#   AMNEZIA_INSTALL_XRAY_BASE=URL        откуда качать релизы Xray
#   AMNEZIA_INSTALL_GEO_BASE=URL         откуда качать geo-списки
#   AMNEZIA_INSTALL_NGINX_SITES_DIR=DIR  sites-enabled (/etc/nginx/sites-enabled)
#   AMNEZIA_INSTALL_LETSENCRYPT_DIR=DIR  /etc/letsencrypt/live
#   AMNEZIA_INSTALL_ACME_ROOT=DIR        webroot HTTP-01 (/var/www/certbot)
#   AMNEZIA_INSTALL_ENTRY_STUB_ROOT=DIR  страница заглушки
#                                        (/var/www/amnezia-entry)

set -euo pipefail

ENTRY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$ENTRY_SCRIPT_DIR/../.." && pwd)"

if [ -n "${AMNEZIA_INSTALL_FAKE_DIR:-}" ]; then
    PATH="$AMNEZIA_INSTALL_FAKE_DIR:$PATH"
fi

OS_RELEASE="${AMNEZIA_INSTALL_OS_RELEASE:-/etc/os-release}"
ENTRY_DIR="${AMNEZIA_INSTALL_ENTRY_DIR:-/opt/xray-entry}"
SYSTEMD_DIR="${AMNEZIA_INSTALL_SYSTEMD_DIR:-/etc/systemd/system}"
SYSCTL_DIR="${AMNEZIA_INSTALL_SYSCTL_DIR:-/etc/sysctl.d}"
MODULES_DIR="${AMNEZIA_INSTALL_MODULES_DIR:-/etc/modules-load.d}"
FAIL2BAN_JAIL="${AMNEZIA_INSTALL_FAIL2BAN_JAIL:-/etc/fail2ban/jail.d/amnezia-vpn-sshd.conf}"
VERSIONS_LOCK="${AMNEZIA_INSTALL_VERSIONS_LOCK:-$REPO_DIR/versions.lock}"
NGINX_SITES_DIR="${AMNEZIA_INSTALL_NGINX_SITES_DIR:-/etc/nginx/sites-enabled}"
LE_LIVE_DIR="${AMNEZIA_INSTALL_LETSENCRYPT_DIR:-/etc/letsencrypt/live}"
ACME_ROOT="${AMNEZIA_INSTALL_ACME_ROOT:-/var/www/certbot}"
STUB_ROOT="${AMNEZIA_INSTALL_ENTRY_STUB_ROOT:-/var/www/amnezia-entry}"
# Имя файла — то, что было на 62.109.31.63: установка заменяет ручной
# файл на месте, а не кладёт второй рядом.
SYSCTL_FILE="$SYSCTL_DIR/90-vpn-entry-tcp.conf"
# Заглушка для --entry-domain — тот же адрес, что у self-steal основного
# сервера (entry-sync.py: STUB_DEST).
STUB_BACKEND=127.0.0.1:4433
NGINX_SITE=amnezia-entry
SYNC="$ENTRY_DIR/entry-sync.py"

log() { printf 'install: %s\n' "$*"; }
die() { printf 'install: ERROR: %s\n' "$2" >&2; exit "$1"; }
die_usage() { die 2 "$1"; }
die_op() { die 1 "$1"; }

usage() {
    cat <<'EOF'
install.sh --role entry — an entry node: Xray that takes clients in first,
sends sites from the direct-exit list straight out and everything else to the main server.

Usage:
  ./install.sh --role entry --panel URL --entry-token TOKEN|-
               (--entry-domain FQDN |
                --entry-dest HOST:PORT --entry-server-names N1[,N2…] [--entry-xver 0|1|2])
               [--import-settings FILE] [--[no-]fail2ban]

  --panel URL           the panel's https:// address; the node takes its
                        client list from URL/api/entry/config every minute
  --entry-token TOKEN   the node's secret from `panel xray entry set` or
                        `panel xray entry token`; "-" reads it from stdin
                        (keeps it out of shell history and ps)
  --entry-domain FQDN   Reality shows a stub site under FQDN with its own
                        Let's Encrypt certificate (nginx on 127.0.0.1:4433,
                        TCP 80 for HTTP-01); links point at FQDN
  --entry-dest HOST:PORT  Reality shows a site already running here instead
  --entry-server-names N1,N2  the names that site answers to (links use N1)
  --entry-xver N        PROXY protocol version Reality speaks to that site:
                        0 (none, default), 1 or 2
  --import-settings FILE  take over a hand-made entry: its settings.json
                        (Reality keys, shortId, path, names) as is, so the
                        links already handed out keep working
  --no-fail2ban         leave SSH without fail2ban; --fail2ban turns it back

On a rerun every flag is optional: the node keeps what settings.json says.
EOF
}

# --- аргументы -----------------------------------------------------------

PANEL_URL=""
TOKEN_ARG=""
TOKEN_SET=0
DOMAIN=""
DEST=""
SERVER_NAMES=""
XVER=""
IMPORT=""
FAIL2BAN=1
while [ "$#" -gt 0 ]; do
    case "$1" in
        --help | -h) usage; exit 0 ;;
        --panel | --entry-token | --entry-domain | --entry-dest | --entry-server-names | --entry-xver | --import-settings)
            [ "$#" -ge 2 ] || die_usage "$1 requires a value"
            case "$1" in
                --panel) PANEL_URL="$2" ;;
                --entry-token) TOKEN_ARG="$2"; TOKEN_SET=1 ;;
                --entry-domain) DOMAIN="$2" ;;
                --entry-dest) DEST="$2" ;;
                --entry-server-names) SERVER_NAMES="$2" ;;
                --entry-xver) XVER="$2" ;;
                --import-settings) IMPORT="$2" ;;
            esac
            shift 2
            ;;
        --fail2ban) FAIL2BAN=1; shift ;;
        --no-fail2ban) FAIL2BAN=0; shift ;;
        *) die_usage "unknown argument for --role entry: $1" ;;
    esac
done

if [ -n "$DOMAIN" ] && { [ -n "$DEST" ] || [ -n "$SERVER_NAMES" ] || [ -n "$XVER" ]; }; then
    die_usage "--entry-domain is a stub site of its own; it does not combine with --entry-dest/--entry-server-names/--entry-xver"
fi
if [ -n "$DEST" ] && [ -z "$SERVER_NAMES" ] && [ ! -f "$ENTRY_DIR/settings.json" ] && [ -z "$IMPORT" ]; then
    die_usage "--entry-dest needs --entry-server-names: the names that site answers to"
fi
if [ ! -f "$ENTRY_DIR/settings.json" ] && [ -z "$IMPORT" ] && [ -z "$DOMAIN" ] && [ -z "$DEST" ]; then
    die_usage "a new entry node needs a site for Reality to show: --entry-domain FQDN, or --entry-dest HOST:PORT with --entry-server-names (or --import-settings for a hand-made one)"
fi
case "$XVER" in "" | 0 | 1 | 2) ;; *) die_usage "--entry-xver must be 0, 1 or 2" ;; esac
if [ "$TOKEN_SET" = "1" ]; then
    if [ "$TOKEN_ARG" = "-" ]; then
        IFS= read -r TOKEN_ARG || true
    fi
    # Вид секрета — тот, что выдаёт панель (internal/xray: entryTokenPattern).
    [[ "$TOKEN_ARG" =~ ^[A-Za-z0-9_-]{43}$ ]] || die_usage "--entry-token: not a token issued by 'panel xray entry set' or 'panel xray entry token'"
fi

# --- root и ОС -------------------------------------------------------------

if [ -z "${AMNEZIA_INSTALL_TEST:-}" ] && [ "$(id -u)" -ne 0 ]; then
    die_op "root privileges are required (run with sudo or as root)"
fi
os_id="$(sed -n 's/^ID=//p' "$OS_RELEASE" 2>/dev/null | tr -d '"' | head -1)"
os_version="$(sed -n 's/^VERSION_ID=//p' "$OS_RELEASE" 2>/dev/null | tr -d '"' | head -1)"
case "${os_id}:${os_version}" in
    debian:12 | ubuntu:22.04 | ubuntu:24.04) log "OS check: $os_id $os_version" ;;
    *) die_op "unsupported OS: ${os_id:-unknown} ${os_version}; supported: Debian 12, Ubuntu 22.04, Ubuntu 24.04" ;;
esac

# apt ждёт занятую блокировку dpkg сам (unattended-upgrades на свежем
# сервере держит её минутами), а не падает сразу.
apt_install() {
    DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a \
        apt-get -o DPkg::Lock::Timeout=600 install -y "$@" || die_op "apt-get install $* failed"
}

command -v python3 >/dev/null 2>&1 || apt_install python3

# --- версии -----------------------------------------------------------------

lock_value() { sed -n "s/^$1=//p" "$VERSIONS_LOCK" 2>/dev/null | tail -1; }
XRAY_VERSION="$(lock_value XRAY_VERSION)"
[ -n "$XRAY_VERSION" ] || die_op "versions.lock has no XRAY_VERSION ($VERSIONS_LOCK)"
case "${AMNEZIA_INSTALL_UNAME_M:-$(uname -m)}" in
    x86_64 | amd64) XRAY_ARCH=64; XRAY_SHA_KEY=XRAY_SHA256_LINUX_64 ;;
    aarch64 | arm64) XRAY_ARCH=arm64-v8a; XRAY_SHA_KEY=XRAY_SHA256_LINUX_ARM64_V8A ;;
    *) die_op "unsupported CPU architecture: $(uname -m); the entry role supports x86_64 and aarch64" ;;
esac
XRAY_SHA256="$(lock_value "$XRAY_SHA_KEY")"
[[ "$XRAY_SHA256" =~ ^[0-9a-f]{64}$ ]] \
    || die_op "versions.lock has no $XRAY_SHA_KEY: the Xray $XRAY_VERSION archive cannot be verified, nothing was installed"

# --- каталог и настройки ------------------------------------------------------

export AMNEZIA_ENTRY_DIR="$ENTRY_DIR"
[ -z "${AMNEZIA_INSTALL_XRAY_BASE:-}" ] || export AMNEZIA_ENTRY_XRAY_BASE="$AMNEZIA_INSTALL_XRAY_BASE"
[ -z "${AMNEZIA_INSTALL_GEO_BASE:-}" ] || export AMNEZIA_ENTRY_GEO_BASE="$AMNEZIA_INSTALL_GEO_BASE"

mkdir -p "$ENTRY_DIR" || die_op "cannot create $ENTRY_DIR"
chmod 0700 "$ENTRY_DIR"
install_script() { # install_script SRC DEST — рядом, потом rename
    cp -p "$1" "$2.new" && chmod 0700 "$2.new" && mv -f "$2.new" "$2" || die_op "cannot install $2"
}
install_script "$ENTRY_SCRIPT_DIR/entry-sync.py" "$SYNC"
# Версия выпуска, с которого поставлен узел: синк отправляет её панели
# вместе с показателями, окно «О версиях» показывает, что обновлять
# (amnezia-vpn-server-q1do.1). Нет её в versions.lock — файла нет, и
# панель пишет «Неизвестно».
RELEASE_VERSION="$(lock_value IMAGE_VERSION)"
if [ -n "$RELEASE_VERSION" ]; then
    printf '%s\n' "$RELEASE_VERSION" > "$ENTRY_DIR/release.new" && chmod 0644 "$ENTRY_DIR/release.new" \
        && mv -f "$ENTRY_DIR/release.new" "$ENTRY_DIR/release" || die_op "cannot write $ENTRY_DIR/release"
else
    rm -f "$ENTRY_DIR/release"
fi

sync_py() { python3 -I "$SYNC" "$@"; }

if [ -f "$ENTRY_DIR/settings.json" ] && [ -z "$IMPORT" ]; then
    log "settings: keeping $ENTRY_DIR/settings.json (Reality keys, shortId and path stay as they are)"
fi
settings_args=()
[ -z "$IMPORT" ] || settings_args+=(--import "$IMPORT")
[ -z "$PANEL_URL" ] || settings_args+=(--panel "$PANEL_URL")
[ -z "$DOMAIN" ] || settings_args+=(--domain "$DOMAIN")
[ -z "$DEST" ] || settings_args+=(--dest "$DEST")
[ -z "$SERVER_NAMES" ] || settings_args+=(--server-names "$SERVER_NAMES")
[ -z "$XVER" ] || settings_args+=(--xver "$XVER")
out="$(sync_py settings ${settings_args[@]+"${settings_args[@]}"} 2>&1)" || die_op "settings: ${out#entry-sync: }"
[ -z "$out" ] || log "${out#entry-sync: }"
SETTINGS_PANEL="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("panel_url",""))' "$ENTRY_DIR/settings.json")"
SETTINGS_DEST="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["dest"])' "$ENTRY_DIR/settings.json")"

if [ "$TOKEN_SET" = "1" ]; then
    umask 077
    printf '%s\n' "$TOKEN_ARG" > "$ENTRY_DIR/token.new" && chmod 0600 "$ENTRY_DIR/token.new" \
        && mv -f "$ENTRY_DIR/token.new" "$ENTRY_DIR/token" || die_op "cannot write $ENTRY_DIR/token"
    umask 022
    unset TOKEN_ARG
    log "entry token saved to $ENTRY_DIR/token"
fi

# --- Xray ---------------------------------------------------------------------

XRAY_CHANGED=0
out="$(sync_py install-xray --version "$XRAY_VERSION" --sha256 "$XRAY_SHA256" --arch "$XRAY_ARCH" 2>&1)" \
    || die_op "xray: ${out#entry-sync: }"
case "$out" in
    *installed*) XRAY_CHANGED=1; log "xray $XRAY_VERSION installed to $ENTRY_DIR/xray (sha256 verified)" ;;
    *) log "xray $XRAY_VERSION already in place" ;;
esac

if [ ! -f "$ENTRY_DIR/geoip.dat" ] || [ ! -f "$ENTRY_DIR/geosite.dat" ]; then
    out="$(sync_py geo --no-restart 2>&1)" || die_op "geo lists: ${out#entry-sync: }"
    log "geo lists downloaded (sha256 verified)"
fi

# --- маскировка: своя заглушка --entry-domain ------------------------------------

stub_site() { # stub_site DOMAIN PHASE — порт 80 для HTTP-01, затем TLS на петле
    cat <<EOF
# Amnezia VPN entry node: the site Reality shows for $1
# (amnezia-vpn-server-14y0.6) — managed by install.sh --role entry.
server {
    listen 80;
    listen [::]:80;
    server_name $1;
    location /.well-known/acme-challenge/ {
        root ${ACME_ROOT};
    }
    location / {
        return 301 https://\$host\$request_uri;
    }
}
EOF
    [ "$2" = "phase2" ] || return 0
    cat <<EOF

server {
    listen ${STUB_BACKEND} ssl http2;
    server_name $1;
    ssl_certificate ${LE_LIVE_DIR}/$1/fullchain.pem;
    ssl_certificate_key ${LE_LIVE_DIR}/$1/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    root ${STUB_ROOT};
    index index.html;
    location / {
        try_files \$uri \$uri/ =404;
    }
}
EOF
}

nginx_reload() {
    nginx -t >/dev/null 2>&1 || die_op "nginx -t refused the entry stub site ($ENTRY_DIR/nginx/entry.conf)"
    if systemctl is-active --quiet nginx; then
        nginx -s reload >/dev/null 2>&1 || systemctl reload nginx || die_op "nginx reload failed"
    else
        systemctl enable --now nginx >/dev/null 2>&1 || die_op "systemctl start nginx failed"
    fi
}

stub_setup() {
    local domain="$1" phase=phase1
    command -v nginx >/dev/null 2>&1 || apt_install nginx
    command -v certbot >/dev/null 2>&1 || apt_install certbot
    mkdir -p "$ENTRY_DIR/nginx" "$ACME_ROOT" "$STUB_ROOT" "$NGINX_SITES_DIR" || die_op "cannot create the stub site directories"
    chmod 0755 "$STUB_ROOT"
    if [ ! -f "$STUB_ROOT/index.html" ]; then
        cat > "$STUB_ROOT/index.html" <<'EOF'
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Welcome</title></head>
<body><h1>Welcome</h1><p>This site is under construction.</p></body>
</html>
EOF
        chmod 0644 "$STUB_ROOT/index.html"
    fi
    [ -f "$LE_LIVE_DIR/$domain/fullchain.pem" ] && phase=phase2
    stub_site "$domain" "$phase" > "$ENTRY_DIR/nginx/entry.conf"
    ln -sfn "$ENTRY_DIR/nginx/entry.conf" "$NGINX_SITES_DIR/$NGINX_SITE"
    # nginx по умолчанию тоже слушает 80 с default_server — ACME ушёл бы к
    # нему. TCP 443 у этого сервера — Xray, сайтов на нём нет.
    rm -f "$NGINX_SITES_DIR/default"
    nginx_reload
    if [ "$phase" = "phase1" ]; then
        certbot certonly --webroot -w "$ACME_ROOT" -d "$domain" \
            --non-interactive --agree-tos --register-unsafely-without-email \
            --keep-until-expiring >/dev/null 2>&1 \
            || die_op "Let's Encrypt issuance failed for $domain (see /var/log/letsencrypt): is its DNS pointing here and TCP 80 open?"
        stub_site "$domain" phase2 > "$ENTRY_DIR/nginx/entry.conf"
        nginx_reload
    fi
    systemctl enable --now certbot.timer >/dev/null 2>&1 || true
    log "stub site for $domain on $STUB_BACKEND (Let's Encrypt, renewed by certbot.timer)"
}

if [ "$SETTINGS_DEST" = "$STUB_BACKEND" ]; then
    stub_domain="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["server_names"][0])' "$ENTRY_DIR/settings.json")"
    stub_setup "$stub_domain"
fi

# --- sysctl ---------------------------------------------------------------------

# Настройки TCP для клиентов на мобильной связи — как на 62.109.31.63:
# BBR держит скорость при потерях лучше cubic, fq нужен BBR для пейсинга;
# большие буферы — для каналов с большой задержкой; без сброса окна после
# простоя видео и ленты не грузятся рывками.
mkdir -p "$SYSCTL_DIR" || die_op "cannot create $SYSCTL_DIR"
have_bbr=0
if modprobe tcp_bbr 2>/dev/null || sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -qw bbr; then
    have_bbr=1
    mkdir -p "$MODULES_DIR" && printf 'tcp_bbr\n' > "$MODULES_DIR/amnezia-vpn-bbr.conf" \
        && chmod 0644 "$MODULES_DIR/amnezia-vpn-bbr.conf"
fi
{
    printf '# Amnezia VPN entry node (amnezia-vpn-server-14y0.6) — managed by install.sh --role entry.\n'
    if [ "$have_bbr" = "1" ]; then
        printf 'net.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\n'
    fi
    printf '%s\n' 'net.ipv4.tcp_slow_start_after_idle = 0' 'net.ipv4.tcp_mtu_probing = 1' \
        'net.core.rmem_max = 16777216' 'net.core.wmem_max = 16777216' \
        'net.ipv4.tcp_rmem = 4096 131072 16777216' 'net.ipv4.tcp_wmem = 4096 65536 16777216'
} > "$SYSCTL_FILE.new" && chmod 0644 "$SYSCTL_FILE.new" && mv -f "$SYSCTL_FILE.new" "$SYSCTL_FILE" \
    || die_op "cannot write $SYSCTL_FILE"
sysctl -p "$SYSCTL_FILE" >/dev/null 2>&1 || log "WARNING: some of $SYSCTL_FILE did not apply; it applies on the next boot"
[ "$have_bbr" = "1" ] || log "WARNING: this kernel has no bbr; TCP stays on its default congestion control"

# --- fail2ban -------------------------------------------------------------------

if [ "$FAIL2BAN" = "1" ]; then
    if ! command -v "${AMNEZIA_INSTALL_FAIL2BAN_BIN:-fail2ban-server}" >/dev/null 2>&1; then
        DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a apt-get -o DPkg::Lock::Timeout=600 install -y fail2ban \
            || log "WARNING: could not install fail2ban; SSH is left unguarded"
    fi
    if mkdir -p "$(dirname "$FAIL2BAN_JAIL")"; then
        # Тот же jail, что у основного сервера (install.sh, amnezia-vpn-server-rswn):
        # journald — потому что /var/log/auth.log на свежих системах нет.
        cat > "$FAIL2BAN_JAIL" <<'JAIL'
# amnezia-vpn managed (amnezia-vpn-server-rswn). Rerun install.sh to
# regenerate; delete this file and restart fail2ban to opt out by hand.
[sshd]
enabled = true
backend = systemd
banaction = nftables[type=multiport]
maxretry = 5
findtime = 10m
bantime = 1h
JAIL
        chmod 0644 "$FAIL2BAN_JAIL"
        systemctl enable fail2ban >/dev/null 2>&1 || true
        systemctl restart fail2ban >/dev/null 2>&1 && log "fail2ban guards SSH (5 attempts in 10 minutes, banned for an hour)" \
            || log "WARNING: fail2ban did not start; SSH is left unguarded"
    fi
else
    log "fail2ban skipped (--no-fail2ban)"
fi

# --- systemd --------------------------------------------------------------------

mkdir -p "$SYSTEMD_DIR" || die_op "cannot create $SYSTEMD_DIR"
UNITS_CHANGED=0
write_unit() { # write_unit NAME < content
    local dest="$SYSTEMD_DIR/$1" new
    new="$(cat)"
    if [ ! -f "$dest" ] || [ "$(cat "$dest")" != "$new" ]; then
        printf '%s\n' "$new" > "$dest.new" && chmod 0644 "$dest.new" && mv -f "$dest.new" "$dest" \
            || die_op "cannot write $dest"
        [ "$1" != "xray-entry.service" ] || UNITS_CHANGED=1
    fi
}

write_unit xray-entry.service <<EOF
[Unit]
Description=Xray entry node (amnezia-vpn-server-14y0.6)
After=network-online.target
Wants=network-online.target
[Service]
Environment=XRAY_LOCATION_ASSET=$ENTRY_DIR
ExecStart=$ENTRY_DIR/xray run -c $ENTRY_DIR/config.json
Restart=always
RestartSec=3
MemoryMax=200M
LimitNOFILE=65536
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
[Install]
WantedBy=multi-user.target
EOF

# Синхронизация с панелью раз в минуту. Ошибка связи — не авария: узел
# работает на последнем конфиге, а юнит помечается упавшим, чтобы это было
# видно в systemctl --failed.
write_unit xray-entry-sync.service <<EOF
[Unit]
Description=Xray entry node: take the client list from the panel
After=network-online.target
Wants=network-online.target
[Service]
Type=oneshot
Environment=AMNEZIA_ENTRY_DIR=$ENTRY_DIR
ExecStart=/usr/bin/python3 -I $SYNC sync
TimeoutStartSec=50
EOF
write_unit xray-entry-sync.timer <<'EOF'
[Unit]
Description=Xray entry node: sync with the panel every minute
[Timer]
OnBootSec=30s
OnUnitActiveSec=1min
AccuracySec=5s
[Install]
WantedBy=timers.target
EOF
write_unit xray-entry-geo.service <<EOF
[Unit]
Description=Xray entry node: update the geo lists
After=network-online.target
Wants=network-online.target
[Service]
Type=oneshot
Environment=AMNEZIA_ENTRY_DIR=$ENTRY_DIR
ExecStart=/usr/bin/python3 -I $SYNC geo
EOF
write_unit xray-entry-geo.timer <<'EOF'
[Unit]
Description=Xray entry node: weekly geo lists update
[Timer]
OnCalendar=Mon 04:30
RandomizedDelaySec=30min
Persistent=true
[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload || die_op "systemctl daemon-reload failed"

# --- первая синхронизация и запуск ------------------------------------------------

config_sum() { [ -f "$ENTRY_DIR/config.json" ] && cksum < "$ENTRY_DIR/config.json" || true; }
before="$(config_sum)"
SYNC_OK=1
if out="$(sync_py sync 2>&1)"; then
    log "sync: ${out##*entry-sync: }"
else
    SYNC_OK=0
    printf '%s\n' "$out" | sed 's/^entry-sync: /install: sync: /'
fi
[ -f "$ENTRY_DIR/config.json" ] || die_op "no Xray config was built in $ENTRY_DIR; see the sync messages above"
after="$(config_sum)"

systemctl enable xray-entry.service >/dev/null 2>&1 || die_op "systemctl enable xray-entry failed"
if ! systemctl is-active --quiet xray-entry; then
    systemctl start xray-entry || die_op "xray-entry did not start: is TCP 443 taken by something else? (journalctl -u xray-entry)"
elif [ "$before" = "$after" ] && { [ "$XRAY_CHANGED" = "1" ] || [ "$UNITS_CHANGED" = "1" ]; }; then
    # Новый бинарник или юнит, а конфиг тот же — синхронизация Xray не
    # перезапускала, и без этого продолжил бы работать прежний.
    systemctl restart xray-entry || die_op "xray-entry did not restart (journalctl -u xray-entry)"
fi
systemctl is-active --quiet xray-entry || die_op "xray-entry is not running (journalctl -u xray-entry)"
systemctl enable --now xray-entry-sync.timer xray-entry-geo.timer >/dev/null 2>&1 \
    || die_op "cannot enable the xray-entry timers"
log "xray-entry is running; sync every minute, geo lists weekly"

# --- итог -----------------------------------------------------------------------

params="$(sync_py describe)"
param() { printf '%s\n' "$params" | sed -n "s/^$1=//p"; }
port_flag=""
[ "$(param port)" = "443" ] || port_flag=" --port $(param port)"
cat <<EOF
install: entry node ready.
install: register it in the panel (on the main server), once:
install:   panel xray entry set <name> --host $(param host)${port_flag} --sni $(param sni) \\
install:       --public-key $(param public_key) --short-id $(param short_id) --path $(param path) --prefix "VLESS "
install: a new entry prints its token there; give it to this node with
install:   ./install.sh --role entry --entry-token -
EOF
[ -n "$SETTINGS_PANEL" ] || log "WARNING: no --panel given yet: the node has no clients until it can reach the panel"
[ -f "$ENTRY_DIR/token" ] || log "WARNING: no --entry-token given yet: the node has no clients until it has one"
[ "$SYNC_OK" = "1" ] || log "WARNING: the first sync with the panel failed (see above); the timer retries every minute"
exit 0

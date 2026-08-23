#!/bin/bash
#
# install.sh — M9.1/M9.2 production deployment of the AmneziaWG VPN
# Server stack (contract: AUDITS/M9.1_AUDIT.md, AUDITS/M9.2_AUDIT.md,
# ТЗ v2.0 §9).
#
# install.sh is infrastructure state, not application backup (ТЗ §9).
# It sets up the host (OS check, Docker Engine + Compose plugin from the
# official Docker Inc. repository, persistent ip_forward/conntrack/optional
# BBR, journald cap, weekly docker-prune timer) and deploys the
# compose stack under a deployment root. M9.2 adds the host networking
# part of ТЗ §9 within the constraints of the audit: a managed nftables
# ruleset (NAT/forward/UDP acceptance) in the single table `ip amnezia`,
# applied atomically, never flushing foreign rules, persisted through
# the distro nftables service with an explicit docker boot-order drop-in.
#
# Install flow (ТЗ §9 / M9.1+M9.2 contract):
#   1. OS check (Debian 12, Ubuntu 22.04, Ubuntu 24.04 only)
#   2. Docker/Compose installation or verification
#   3. verify Docker Compose >= 2.24.2
#   4. enable/start the Docker service
#   5. persist managed sysctl drop-in (ip_forward, conntrack, optional BBR)
#   6. AmneziaWG client stack (kernel module + tools, official PPA)
#   7. create the deployment layout under the root
#   8. secure directory permissions
#   9. install/copy repository deployment files
#  10. cap journald SystemMaxUse; write weekly docker-prune units (enable after up)
#  11. create deployment .env (deployment-specific values)
#  12. host networking: managed nftables ruleset (M9.2)
#  13. docker compose --env-file versions.lock config --quiet
#  14. build, prune golang/build cache, start the stack (compose contract)
#  15. minimal post-install self-check (never mutates application state)
#  16. final status + SSH tunnel hint for the loopback-only panel
#
# Arguments (only these are supported):
#   --root DIR      deployment root (default: /opt/amnezia-vpn)
#   --awg-port PORT external UDP port of the AWG runtime, 1..65535
#                   (default: 443; not 51820 — that is the well-known
#                   WireGuard port and is an easy DPI/block target)
#   --vpn-subnet CIDR  IPv4 subnet of the tunnel (default: 10.8.0.0/24).
#                   Authoritative source is server.address in the
#                   database: when the deployed config/awg0.conf exists,
#                   its Address CIDR wins over this parameter/.env
#                   (no second source of truth in steady state).
#   --domain FQDN / --panel-domain FQDN  panel hostname (T-121, T-156):
#                   public DNS A/AAAA to this server, Let's Encrypt
#                   (nginx + certbot). HTTP-01 stays on TCP 80. TLS
#                   listen is 443 unless --panel-port is also set.
#                   Without a panel domain the panel stays loopback-only
#                   (SSH tunnel hint) unless --panel-port is used.
#   --client-domain FQDN / --vpn-domain FQDN  client endpoint domain
#                   (T-129, T-156): client configs carry <fqdn>:<awg-port>.
#                   Omitted: endpoint is the public IP. The panel hostname
#                   is never copied onto clients. DNS is pre-flighted
#                   before any certificate attempt.
#   --panel-port PORT  nginx TLS listen port. With a panel domain:
#                   https://PANEL_DOMAIN:PORT (Let's Encrypt; nftables
#                   opens tcp 80 + tcp PORT). Without a panel domain
#                   (T-124): self-signed https://<server-ip>:PORT.
#                   Omitted with a panel domain: TCP 443 as today
#                   (UDP AWG 443 can coexist).
#   --build            compile the images here instead of pulling them
#   --panel-tls-regen  with --panel-port and no panel domain: force a
#                   fresh self-signed certificate.
#   --help          usage
#
# Testability hooks (environment, not arguments):
#   AMNEZIA_INSTALL_TEST=1             skip the root-user check
#   AMNEZIA_INSTALL_FAKE_DIR=DIR       prefix PATH with DIR so fakes can
#                                      stand in for docker/apt-get/
#                                      systemctl/sysctl/nft/curl: tests
#                                      never touch the real host
#   AMNEZIA_INSTALL_OS_RELEASE=FILE    os-release source (default
#                                      /etc/os-release)
#   AMNEZIA_INSTALL_SYSCTL_DIR=DIR     persistence dir for the managed
#                                      sysctl drop-in (default /etc/sysctl.d)
#   AMNEZIA_INSTALL_JOURNALD_DIR=DIR   journald drop-in dir (default
#                                      /etc/systemd/journald.conf.d)
#   AMNEZIA_INSTALL_KEYRING_DIR=DIR    Docker Inc. keyring dir
#                                      (default /etc/apt/keyrings)
#   AMNEZIA_INSTALL_APT_SOURCES_DIR=DIR   apt sources.d dir
#                                      (default /etc/apt/sources.list.d)
#   AMNEZIA_INSTALL_NFTABLES_DIR=DIR   managed nftables fragment dir
#                                      (default /etc/nftables.d)
#   AMNEZIA_INSTALL_NFTABLES_CONF=FILE nftables.conf to hook the include
#                                      into (default /etc/nftables.conf)
#   AMNEZIA_INSTALL_SYSTEMD_DIR=DIR    systemd unit dir for the docker
#                                      boot-order drop-in and the weekly
#                                      docker-prune timer
#                                      (default /etc/systemd/system)
#   AMNEZIA_INSTALL_MODULES_DIR=DIR    modules-load dir for the
#                                      amneziawg auto-load entry
#                                      (default /etc/modules-load.d)
#   AMNEZIA_INSTALL_FORCE_AWG_INSTALL=1  run the AmneziaWG client-stack
#                                      install even when already present
#                                      (testability)
#   AMNEZIA_INSTALL_SKIP_PRUNE=1       skip docker-prune.sh after compose
#                                      build (keep golang toolchain /
#                                      builder cache; testability)
#   AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC  how long to wait for a busy
#                                      dpkg lock-frontend (default 600)
#   AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC poll/sleep interval while waiting
#                                      (default 5; tests use 0)
#
# Security contract: no secrets are ever accepted, logged or printed;
# .env is created only when absent, with 0600; the panel stays
# loopback-only (127.0.0.1:8787:8787) and is never published; the
# self-signed TLS key of the IP:port mode is created 0600 and its
# value is never printed; nftables application never flushes the
# ruleset, never drops SSH, and is syntax-checked before it is applied.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${ROOT_DIR:-/opt/amnezia-vpn}"
AWG_PORT=443
VPN_SUBNET=10.8.0.0/24

OS_RELEASE="${AMNEZIA_INSTALL_OS_RELEASE:-/etc/os-release}"
SYSCTL_DIR="${AMNEZIA_INSTALL_SYSCTL_DIR:-/etc/sysctl.d}"
SYSCTL_FILE="${SYSCTL_DIR}/99-amnezia-vpn.conf"
JOURNALD_DIR="${AMNEZIA_INSTALL_JOURNALD_DIR:-/etc/systemd/journald.conf.d}"
JOURNALD_FILE="${JOURNALD_DIR}/99-amnezia-vpn.conf"
KEYRING_DIR="${AMNEZIA_INSTALL_KEYRING_DIR:-/etc/apt/keyrings}"
APT_SOURCES_DIR="${AMNEZIA_INSTALL_APT_SOURCES_DIR:-/etc/apt/sources.list.d}"
NFTABLES_DIR="${AMNEZIA_INSTALL_NFTABLES_DIR:-/etc/nftables.d}"
NFTABLES_CONF="${AMNEZIA_INSTALL_NFTABLES_CONF:-/etc/nftables.conf}"
SYSTEMD_DIR="${AMNEZIA_INSTALL_SYSTEMD_DIR:-/etc/systemd/system}"
ENV_FILE=".env"

FAIL_STYLE_NONE=0
FAIL_STYLE_USAGE=2
FAIL_STYLE_OP=1

usage() {
    cat <<'EOF'
install.sh — M9 deployment of the AmneziaWG VPN Server stack.

Usage:
  ./install.sh [--root DIR] [--awg-port PORT] [--vpn-subnet CIDR]
               [--panel-domain FQDN | --domain FQDN]
               [--vpn-domain FQDN | --client-domain FQDN]
               [--panel-port PORT] [--panel-tls-regen] [--build]

Options:
  --root DIR        deployment root (default: /opt/amnezia-vpn)
  --awg-port PORT   external UDP port of the AWG runtime,
                    1..65535 (default: 443; avoid 51820)
  --vpn-subnet CIDR VPN subnet the server assigns clients from;
                    IPv4 CIDR with prefix 1..32 (default: 10.8.0.0/24)
  --panel-domain FQDN  panel hostname (Let's Encrypt). Alias: --domain.
                    HTTP-01 on TCP 80. TLS on 443 unless --panel-port
                    is set. Without it the panel stays loopback-only
                    (SSH tunnel hint) unless --panel-port is used.
  --domain FQDN     alias of --panel-domain
  --vpn-domain FQDN client endpoint host (<fqdn>:<awg-port>). Alias:
                    --client-domain. Omitted: public IP. The panel
                    hostname is never copied onto clients. DNS is
                    verified before the install proceeds.
  --client-domain FQDN  alias of --vpn-domain
  --panel-port PORT nginx TLS listen port. With --panel-domain:
                    https://PANEL_DOMAIN:PORT (nftables: tcp 80 + tcp
                    PORT). Without a panel domain: self-signed
                    https://<server-ip>:PORT (T-124).
  --build       compile the images on this server instead of pulling the
                published ones (slow: it downloads a Go toolchain and
                builds amneziawg-go, amneziawg-tools and the panel)
  --panel-tls-regen with --panel-port and no panel domain: force a
                    fresh self-signed certificate
  --help            print this message

Examples:
  ./install.sh --panel-domain panel.example.com --vpn-domain example.com
  ./install.sh --panel-domain panel.example.com --panel-port 8443 --vpn-domain example.com
  ./install.sh --panel-port 8443
  ./install.sh --domain panel.example.com --client-domain vpn.example.com

Installs only supported OSes (Debian 12, Ubuntu 22.04, Ubuntu 24.04),
Docker Engine + Compose plugin (>= 2.24.2) from the official Docker Inc.
repository, persists net.ipv4.ip_forward, nf_conntrack_max and optional
BBR, caps journald and compose json-file logs, installs a weekly
docker-prune timer, installs a managed nftables
ruleset (NAT/forward for the VPN subnet, UDP AWG_PORT acceptance),
then builds and starts the stack under the deployment root.
EOF
}

log() { printf 'install: %s\n' "$*"; }

die() {
    printf 'install: ERROR: %s\n' "$2" >&2
    exit "$1"
}

die_usage() { die "$FAIL_STYLE_USAGE" "$1"; }
die_op() { die "$FAIL_STYLE_OP" "$1"; }

# --- argument parsing (only --root, --awg-port, --vpn-subnet,
# --- --panel-domain/--domain, --vpn-domain/--client-domain,
# --- --panel-port, --panel-tls-regen, --help) ----------

validate_port() {
    [ "$1" -ge 1 ] 2>/dev/null && [ "$1" -le 65535 ] 2>/dev/null
}

# validate_cidr: dotted-quad address + prefix in 1..32.
validate_cidr() {
    local addr prefix o1 o2 o3 o4 o
    addr="${1%/*}"
    prefix="${1#*/}"
    [ "$prefix" -ge 1 ] 2>/dev/null && [ "$prefix" -le 32 ] 2>/dev/null || return 1
    IFS=. read -r o1 o2 o3 o4 <<< "$addr"
    for o in "$o1" "$o2" "$o3" "$o4"; do
        [ "$o" -ge 0 ] 2>/dev/null && [ "$o" -le 255 ] 2>/dev/null || return 1
    done
    return 0
}

# network_of_host_cidr: 10.8.0.1/24 -> 10.8.0.0/24 (host bits cleared).
network_of_host_cidr() {
    local addr prefix o1 o2 o3 o4 net32 mask out
    addr="${1%/*}"; prefix="${1#*/}"
    IFS=. read -r o1 o2 o3 o4 <<< "$addr"
    net32=$(( (o1 << 24) | (o2 << 16) | (o3 << 8) | o4 ))
    mask=$(( 0xFFFFFFFF << (32 - prefix) & 0xFFFFFFFF ))
    out=$(( net32 & mask ))
    printf '%d.%d.%d.%d/%d\n' \
        $(( (out >> 24) & 0xFF )) $(( (out >> 16) & 0xFF )) \
        $(( (out >> 8) & 0xFF )) $(( out & 0xFF )) "$prefix"
}

AWG_PORT_SET=0
VPN_SUBNET_SET=0
DOMAIN_SET=0
DOMAIN="${DOMAIN:-}"
CLIENT_DOMAIN_SET=0
CLIENT_DOMAIN="${CLIENT_DOMAIN:-}"
PANEL_PORT_SET=0
PANEL_PORT=""
PANEL_TLS_REGEN=0
# Published images are the default; --build compiles everything on this
# host instead (development, or a registry this host cannot reach).
BUILD_FROM_SOURCE=0

while [ "$#" -gt 0 ]; do
    case "$1" in
        --help | -h)
            usage
            exit 0
            ;;
        --root)
            [ "$#" -ge 2 ] || die_usage "--root requires a directory argument"
            ROOT_DIR="$2"
            shift 2
            ;;
        --awg-port)
            [ "$#" -ge 2 ] || die_usage "--awg-port requires a port argument"
            AWG_PORT="$2"
            AWG_PORT_SET=1
            shift 2
            ;;
        --vpn-subnet)
            [ "$#" -ge 2 ] || die_usage "--vpn-subnet requires a CIDR argument"
            VPN_SUBNET="$2"
            VPN_SUBNET_SET=1
            shift 2
            ;;
        --domain | --panel-domain)
            [ "$#" -ge 2 ] || die_usage "$1 requires a FQDN argument"
            DOMAIN="$2"
            DOMAIN_SET=1
            shift 2
            ;;
        --client-domain | --vpn-domain)
            [ "$#" -ge 2 ] || die_usage "$1 requires a FQDN argument"
            CLIENT_DOMAIN="$2"
            CLIENT_DOMAIN_SET=1
            shift 2
            ;;
        --panel-port)
            [ "$#" -ge 2 ] || die_usage "--panel-port requires a port argument"
            PANEL_PORT="$2"
            PANEL_PORT_SET=1
            shift 2
            ;;
        --panel-tls-regen)
            PANEL_TLS_REGEN=1
            shift
            ;;
        --build)
            BUILD_FROM_SOURCE=1
            shift
            ;;
        *)
            die_usage "unknown argument: $1"
            ;;
    esac
done

[ -n "$ROOT_DIR" ] || die_usage "empty --root"
[ "$ROOT_DIR" != "/" ] || die_usage "--root must not be the filesystem root"
[ -n "$AWG_PORT" ] || die_usage "empty --awg-port"
validate_port "$AWG_PORT" || die_usage "--awg-port must be an integer in 1..65535 (got: $AWG_PORT)"
[ -n "$VPN_SUBNET" ] || die_usage "empty --vpn-subnet"
validate_cidr "$VPN_SUBNET" || die_usage "--vpn-subnet must be an IPv4 CIDR like 10.8.0.0/24 (got: $VPN_SUBNET)"

# validate_fqdn: lowercase letters/digits/hyphens per label, dots
# between labels, total length <= 253, no leading/trailing dot.
# Character classes like [A-Z] follow locale collation; under en_US*
# they match most lowercase letters. Force C so hyphenated FQDNs
# (e.g. panel.super-space.com.de) are accepted.
validate_fqdn() {
    (
        LC_ALL=C
        case "$1" in
            "" | *..* | -* | *- | .* | *. | *[!a-zA-Z0-9.-]* | *[A-Z_]*)
                exit 1
                ;;
        esac
        [ "${#1}" -le 253 ] || exit 1
        rest="$1."
        while [ -n "$rest" ]; do
            label="${rest%%.*}"
            rest="${rest#*.}"
            [ -n "$label" ] || exit 1
            [ "${#label}" -le 63 ] || exit 1
            case "$label" in
                -* | *- | *[!a-zA-Z0-9-]* | *[A-Z_]*) exit 1 ;;
            esac
        done
        exit 0
    )
}

if [ "$DOMAIN_SET" = "1" ]; then
    validate_fqdn "$DOMAIN" || die_usage "--panel-domain/--domain must be a valid FQDN (labels of letters/digits/hyphens; got: $DOMAIN)"
fi

if [ "$CLIENT_DOMAIN_SET" = "1" ]; then
    validate_fqdn "$CLIENT_DOMAIN" || die_usage "--vpn-domain/--client-domain must be a valid FQDN (labels of letters/digits/hyphens; got: $CLIENT_DOMAIN)"
fi

if [ "$PANEL_PORT_SET" = "1" ]; then
    validate_port "$PANEL_PORT" || die_usage "--panel-port must be an integer in 1..65535 (got: $PANEL_PORT)"
fi
if [ "$PANEL_TLS_REGEN" = "1" ] && [ "$PANEL_PORT_SET" = "0" ]; then
    die_usage "--panel-tls-regen requires --panel-port"
fi
if [ "$PANEL_TLS_REGEN" = "1" ] && [ -n "$DOMAIN" ]; then
    die_usage "--panel-tls-regen cannot be used with --panel-domain/--domain (Let's Encrypt, not a self-signed certificate)"
fi
if [ -n "$DOMAIN" ] && [ "$PANEL_PORT_SET" = "1" ] && [ "$PANEL_PORT" = "80" ]; then
    die_usage "--panel-port 80 conflicts with ACME HTTP-01 (TCP 80). Use 443 (default) or another port such as 8443"
fi

# --- root requirement (skipped in test mode only) ---------------------

if [ -z "${AMNEZIA_INSTALL_TEST:-}" ]; then
    if [ "$(id -u)" -ne 0 ]; then
        die_op "root privileges are required (run with sudo or as root)"
    fi
else
    log "test mode: root check skipped"
fi

# --- 1. OS check ------------------------------------------------------

# os-release detection; only exact ID/VERSION_ID pairs are supported.
os_id=""
os_version=""
os_codename=""
if [ -r "$OS_RELEASE" ]; then
    os_id="$(sed -n 's/^ID=//p' "$OS_RELEASE" | tr -d '"' | head -1)"
    os_version="$(sed -n 's/^VERSION_ID=//p' "$OS_RELEASE" | tr -d '"' | head -1)"
    os_codename="$(sed -n 's/^VERSION_CODENAME=//p' "$OS_RELEASE" | tr -d '"' | head -1)"
fi
[ -n "$os_id" ] && [ -n "$os_version" ] || die_op "cannot determine the OS (no readable $OS_RELEASE)"

supported_os() {
    case "${os_id}:${os_version}" in
        debian:12 | ubuntu:22.04 | ubuntu:24.04) return 0 ;;
    esac
    return 1
}

if ! supported_os; then
    die_op "unsupported OS: ${os_id} ${os_version}; supported: Debian 12, Ubuntu 22.04, Ubuntu 24.04"
fi
[ -n "$os_codename" ] || die_op "$os_id $os_version: VERSION_CODENAME missing from $OS_RELEASE"
log "OS check: $os_id $os_version ($os_codename)"

# --- helpers ----------------------------------------------------------

cmd() { command "$@"; }

DPKG_LOCK_FRONTEND="/var/lib/dpkg/lock-frontend"
DPKG_LOCK="/var/lib/dpkg/lock"

# run_apt_get: every apt-get update/install waits for a free dpkg lock
# (unattended-upgrades) instead of dying immediately. Does not kill the
# holder and does not remove lock files. Timeout/poll are overridable
# for tests via AMNEZIA_INSTALL_DPKG_LOCK_*.
dpkg_lock_held() {
    command -v fuser >/dev/null 2>&1 || return 1
    cmd fuser "$DPKG_LOCK_FRONTEND" >/dev/null 2>&1 && return 0
    cmd fuser "$DPKG_LOCK" >/dev/null 2>&1 && return 0
    return 1
}

dpkg_lock_die() {
    local extra="${1:-}"
    if [ -n "$extra" ]; then
        die_op "timed out waiting for ${DPKG_LOCK_FRONTEND} (${extra}); rerun install.sh is safe"
    fi
    die_op "timed out waiting for ${DPKG_LOCK_FRONTEND}; rerun install.sh is safe"
}

dpkg_lock_holder_from_apt() {
    printf '%s\n' "$1" | sed -n 's/.*held by process \([0-9][0-9]*\) (\([^)]*\)).*/held by process \1 (\2)/p' | head -1
}

wait_for_dpkg_lock() {
    local deadline="$1" poll holder
    poll="${AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC:-5}"
    while dpkg_lock_held; do
        if [ "$(date +%s)" -ge "$deadline" ]; then
            holder="$(cmd fuser "$DPKG_LOCK_FRONTEND" 2>/dev/null | tr -s '[:space:]' ' ' | sed 's/^ //;s/ $//')"
            if [ -n "$holder" ]; then
                dpkg_lock_die "held by process ${holder}"
            fi
            dpkg_lock_die
        fi
        log "waiting for ${DPKG_LOCK_FRONTEND}"
        sleep "$poll"
    done
}

run_apt_get() {
    local timeout poll deadline rc out holder
    timeout="${AMNEZIA_INSTALL_DPKG_LOCK_TIMEOUT_SEC:-600}"
    poll="${AMNEZIA_INSTALL_DPKG_LOCK_POLL_SEC:-5}"
    deadline=$(( $(date +%s) + timeout ))
    wait_for_dpkg_lock "$deadline"
    while true; do
        out="$(cmd apt-get "$@" 2>&1)"
        rc=$?
        [ -n "$out" ] && printf '%s\n' "$out" >&2
        if [ "$rc" -eq 0 ]; then
            return 0
        fi
        if printf '%s\n' "$out" | grep -q 'Could not get lock /var/lib/dpkg/lock-frontend'; then
            if [ "$(date +%s)" -ge "$deadline" ]; then
                holder="$(dpkg_lock_holder_from_apt "$out")"
                dpkg_lock_die "$holder"
            fi
            log "apt-get: ${DPKG_LOCK_FRONTEND} busy; retrying"
            sleep "$poll"
            wait_for_dpkg_lock "$deadline"
            continue
        fi
        return "$rc"
    done
}

compose_version_min() { # docker compose version -> true when >= 2.24.2
    # The compose.yaml contract (env_file long syntax with
    # `path`/`required`) needs Compose >= 2.24.2 — older 2.20–2.23
    # accept the file but fail at `config --quiet` with an unclear
    # error (T-112).
    local ver major minor patch out
    out="$("$@" version 2>/dev/null | head -1)"
    [ -n "$out" ] || return 1
    # portable extraction (BSD/GNU sed): drop the prefix, keep digits/dots
    ver="$(printf '%s\n' "$out" | sed 's/.*version[[:space:]]*//' | sed 's/[^0-9.]*//' | cut -d. -f1-3)"
    [ -n "$ver" ] || return 1
    major="${ver%%.*}"
    minor="$(printf '%s\n' "$ver" | cut -d. -f2)"
    patch="$(printf '%s\n' "$ver" | cut -d. -f3)"
    [ "$major" -gt 2 ] && return 0
    [ "$major" -lt 2 ] && return 1
    [ "$minor" -gt 24 ] && return 0
    [ "$minor" -lt 24 ] && return 1
    [ "$patch" -ge 2 ]
}

docker_compose() { # runs `docker compose <args>` from the deployment root
    (cd "$ROOT_DIR" && docker compose "$@")
}

# --- 2./3. Docker Engine + Compose plugin verification/installation ---
# Always the official Docker Inc. repository; distro packages with an
# insufficient Compose plugin are never used (M9.1 contract).

docker_compose_ok() {
    compose_version_min docker compose
}

docker_install() {
    log "installing Docker Engine + Compose plugin from the official Docker Inc. repository"
    run_apt_get update
    run_apt_get install -y ca-certificates curl
    mkdir -p "$KEYRING_DIR" || die_op "cannot create keyring dir $KEYRING_DIR"
    chmod 0755 "$KEYRING_DIR"
    cmd curl -fsSL "https://download.docker.com/linux/${os_id}/gpg" -o "$KEYRING_DIR/docker.asc"
    mkdir -p "$APT_SOURCES_DIR" || die_op "cannot create apt sources dir $APT_SOURCES_DIR"
    printf 'deb [arch=amd64 signed-by=%s] https://download.docker.com/linux/%s %s stable\n' \
        "$KEYRING_DIR/docker.asc" "$os_id" "$os_codename" \
        > "$APT_SOURCES_DIR/docker.list"
    run_apt_get update
    run_apt_get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

if docker_compose_ok; then
    log "Docker Compose: already present and >= 2.24.2"
else
    log "Docker Compose missing or < 2.24.2: installing from the official repository"
    docker_install
    if docker_compose_ok; then
        log "Docker Compose installed: $(docker compose version)"
    else
        die_op "Docker Compose is still missing or < 2.24.2 after installation; refusing to continue (deployment contract)"
    fi
fi

# --- 4. Docker service -------------------------------------------------

log "enabling and starting the Docker service"
cmd systemctl enable --now docker || die_op "systemctl enable --now docker failed"

# --- 5. managed sysctl drop-in (always overwrite) ---------------------

MODULES_DIR="${AMNEZIA_INSTALL_MODULES_DIR:-/etc/modules-load.d}"

ip_forward_value() { cmd sysctl -n net.ipv4.ip_forward 2>/dev/null | tr -d ' '; }

tcp_cc_has_bbr() {
    local avail
    avail="$(cmd sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null || true)"
    case " $avail " in
        *" bbr "*) return 0 ;;
    esac
    return 1
}

apply_sysctl_warn() { # apply_sysctl_warn KEY=VALUE — warning, do not abort
    if ! cmd sysctl -w "$1"; then
        log "WARNING: sysctl -w $1 failed; continuing"
        return 1
    fi
    return 0
}

log "persisting managed sysctl drop-in $SYSCTL_FILE"
mkdir -p "$SYSCTL_DIR" || die_op "cannot create sysctl dir $SYSCTL_DIR"
# ensure_bbr: tcp_available_congestion_control only lists what is loaded.
# On a clean Ubuntu/Debian tcp_bbr ships as a module and nothing has pulled
# it in yet, so the list reads "reno cubic" and a check made before the
# modprobe concludes the kernel has no BBR — which is why this optimisation
# never actually reached a fresh install. Load it first, then decide.
ensure_bbr() {
    tcp_cc_has_bbr && return 0
    cmd modprobe tcp_bbr 2>/dev/null || return 1
    tcp_cc_has_bbr || return 1
    # Keep it across reboots the same way the amneziawg module is kept.
    if mkdir -p "$MODULES_DIR" 2>/dev/null; then
        printf 'tcp_bbr\n' > "$MODULES_DIR/amnezia-vpn-bbr.conf" 2>/dev/null \
            && chmod 0644 "$MODULES_DIR/amnezia-vpn-bbr.conf" 2>/dev/null
    fi
    log "loaded the tcp_bbr module (it is not loaded by default)"
    return 0
}

HAVE_BBR=0
if ensure_bbr; then
    HAVE_BBR=1
else
    log "skipping BBR sysctls: this kernel does not provide bbr"
fi
# Gateway tuning. Kernel defaults are sized for a desktop; each line
# below is here for a reason that shows up on a VPN gateway:
#   rmem/wmem_max     the tunnel is one UDP socket carrying every client,
#                     and the default 208 KiB ceiling drops packets on
#                     bursts (a ceiling, not an allocation)
#   netdev_max_backlog  packets queued for softirq on a single-vCPU host
#   ip_local_port_range masquerade needs translation ports; the default
#                     32768-60999 leaves ~28k per destination
#   tcp_slow_start_after_idle=0  keeps the window after idle gaps, which
#                     is what long-lived downloads actually see
#   conntrack established timeout  five days of state on a 2 GiB host is
#                     memory spent on connections that ended long ago
TUNING_SYSCTLS='net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.core.netdev_max_backlog=16384
net.ipv4.ip_local_port_range=10240 65535
net.ipv4.tcp_slow_start_after_idle=0
net.netfilter.nf_conntrack_tcp_timeout_established=86400'

{
    printf 'net.ipv4.ip_forward = 1\n'
    printf 'net.netfilter.nf_conntrack_max = 262144\n'
    printf '%s\n' "$TUNING_SYSCTLS" | while IFS='=' read -r key value; do
        [ -n "$key" ] || continue
        printf '%s = %s\n' "$key" "$value"
    done
    if [ "$HAVE_BBR" = "1" ]; then
        printf 'net.core.default_qdisc = fq\n'
        printf 'net.ipv4.tcp_congestion_control = bbr\n'
    fi
} > "$SYSCTL_FILE"
chmod 0644 "$SYSCTL_FILE"

cmd sysctl -w net.ipv4.ip_forward=1 || die_op "sysctl -w net.ipv4.ip_forward=1 failed"
[ "$(ip_forward_value)" = "1" ] || die_op "net.ipv4.ip_forward is still disabled after enabling it"

cmd modprobe nf_conntrack 2>/dev/null || log "WARNING: modprobe nf_conntrack failed; continuing"
apply_sysctl_warn net.netfilter.nf_conntrack_max=262144 || true
# Applied one by one: a kernel without a given knob must warn, not abort.
printf '%s\n' "$TUNING_SYSCTLS" | while IFS= read -r kv; do
    [ -n "$kv" ] || continue
    apply_sysctl_warn "$kv" || true
done
if [ "$HAVE_BBR" = "1" ]; then
    apply_sysctl_warn net.core.default_qdisc=fq || true
    apply_sysctl_warn net.ipv4.tcp_congestion_control=bbr || true
fi

# --- 6. AmneziaWG client stack (amneziawg + amneziawg-tools) ----------
# The host keeps a kernel-native AmneziaWG stack (module + CLI tools)
# NEXT TO the userspace awg container: the awg runtime stays in Docker,
# while the kernel module lets the box itself act as a tunnel client
# (tests, diagnostics, management access) and guarantees the full
# client stack is present for a trouble-free operation. This is the
# official install path (amnezia-vpn/amneziawg-linux-kernel-module):
# PPA ppa:amnezia/ppa, packages amneziawg (DKMS module) and
# amneziawg-tools. Idempotent: a working module plus the awg binary
# skip the install; the module auto-loads at boot and DKMS rebuilds it
# after kernel upgrades.

MODULES_FILE="$MODULES_DIR/amneziawg.conf"

amneziawg_available() {
    cmd modprobe amneziawg 2>/dev/null && command -v awg >/dev/null 2>&1
}

if [ -z "${AMNEZIA_INSTALL_FORCE_AWG_INSTALL:-}" ] && amneziawg_available; then
    log "AmneziaWG client stack already present (kernel module + awg tools)"
else
    log "installing AmneziaWG client stack from the official PPA (ppa:amnezia/ppa)"
    run_apt_get update
    run_apt_get install -y software-properties-common linux-headers-"$(cmd uname -r)"
    cmd add-apt-repository -y ppa:amnezia/ppa
    DEBIAN_FRONTEND=noninteractive run_apt_get install -y amneziawg amneziawg-tools
    mkdir -p "$MODULES_DIR" || die_op "cannot create modules-load dir $MODULES_DIR"
    printf 'amneziawg\n' > "$MODULES_FILE"
    chmod 0644 "$MODULES_FILE"
    cmd modprobe amneziawg || die_op "amneziawg kernel module failed to load after installation"
    log "AmneziaWG client stack installed: $(cmd awg version 2>/dev/null | head -1)"
fi

# --- 7./8. deployment layout + permissions ----------------------------

mkdir -p "$ROOT_DIR" || die_op "cannot create deployment root $ROOT_DIR"
[ -d "$ROOT_DIR" ] || die_op "deployment root $ROOT_DIR is not a directory"
chmod 0750 "$ROOT_DIR"

for sub in data config status backups; do
    mkdir -p "$ROOT_DIR/$sub"
    chmod 0700 "$ROOT_DIR/$sub"
done
log "deployment layout created under $ROOT_DIR (data/config/status/backups: 0700)"

# --- 9. install/copy repository deployment files ----------------------

copy_tree() { # cp -a with per-file error stop
    if ! cp -a "$@" 2>/dev/null; then
        die_op "copying deployment files failed: $*"
    fi
}

copy_tree "$SCRIPT_DIR/compose.yaml" "$ROOT_DIR/"
copy_tree "$SCRIPT_DIR/versions.lock" "$ROOT_DIR/"
copy_tree "$SCRIPT_DIR/docker-prune.sh" "$ROOT_DIR/"
# Build context is the repository root (compose.yaml build.context = "."):
# the app/ tree (panel Dockerfile + Go module incl. embedded templates,
# awg Dockerfile + entrypoint scripts) must be present under the root.
copy_tree "$SCRIPT_DIR/app" "$ROOT_DIR/"
chmod 0644 "$ROOT_DIR/compose.yaml" "$ROOT_DIR/versions.lock"
chmod 0755 "$ROOT_DIR/docker-prune.sh"
log "deployment files installed (compose.yaml, versions.lock, docker-prune.sh, app/)"

# --- 10. journald cap + weekly docker-prune timer ---------------------

mkdir -p "$JOURNALD_DIR" || die_op "cannot create journald dir $JOURNALD_DIR"
cat > "$JOURNALD_FILE" <<'EOF'
[Journal]
SystemMaxUse=200M
EOF
chmod 0644 "$JOURNALD_FILE"
if ! cmd systemctl restart systemd-journald; then
    log "WARNING: systemctl restart systemd-journald failed; continuing"
fi
log "journald cap written ($JOURNALD_FILE SystemMaxUse=200M)"

mkdir -p "$SYSTEMD_DIR" || die_op "cannot create systemd dir $SYSTEMD_DIR"
cat > "$SYSTEMD_DIR/amnezia-vpn-prune.service" <<EOF
# amnezia-vpn managed: weekly Docker build-cache prune (never system prune -a).
[Unit]
Description=Amnezia VPN Docker build-cache prune

[Service]
Type=oneshot
ExecStart=${ROOT_DIR}/docker-prune.sh
EOF
chmod 0644 "$SYSTEMD_DIR/amnezia-vpn-prune.service"
cat > "$SYSTEMD_DIR/amnezia-vpn-prune.timer" <<'EOF'
# amnezia-vpn managed: weekly docker-prune.sh
[Unit]
Description=Weekly Amnezia VPN Docker prune

[Timer]
OnCalendar=weekly
Persistent=true

[Install]
WantedBy=timers.target
EOF
chmod 0644 "$SYSTEMD_DIR/amnezia-vpn-prune.timer"
cmd systemctl daemon-reload || die_op "systemctl daemon-reload failed (prune timer)"
log "weekly docker-prune units written (enable --now after compose up; ExecStart=$ROOT_DIR/docker-prune.sh)"

# --- 11. deployment .env (never overwrites an existing file) -----------

env_read() { # env_read KEY — value of KEY in the deployment .env, ""
    sed -n "s/^${1}=//p" "$ROOT_DIR/$ENV_FILE" 2>/dev/null | tail -1
}

env_set() { # env_set KEY VALUE — update or append one line, keep the rest
    local file="$ROOT_DIR/$ENV_FILE"
    if grep -q "^${1}=" "$file"; then
        sed "s|^${1}=.*|${1}=${2}|" "$file" > "$file.new" && mv -f "$file.new" "$file"
    else
        printf '%s=%s\n' "$1" "$2" >> "$file"
    fi
    chmod 0600 "$file"
}

env_unset() { # env_unset KEY — remove one line, keep the rest
    local file="$ROOT_DIR/$ENV_FILE"
    sed "/^${1}=/d" "$file" > "$file.new" && mv -f "$file.new" "$file"
    chmod 0600 "$file"
}

if [ -f "$ROOT_DIR/$ENV_FILE" ]; then
    log "$ENV_FILE already exists under $ROOT_DIR: keeping it untouched"
    # deployment-specific values may live only in .env: the flags of
    # this run fall back to them so the rules match the deployment.
    if [ "$AWG_PORT_SET" = "0" ]; then
        AWG_PORT="$(env_read AWG_PORT)"
        [ -n "$AWG_PORT" ] || die_op "AWG_PORT unset in $(basename "$ENV_FILE") and not given as an argument"
        validate_port "$AWG_PORT" || die_op "AWG_PORT in $(basename "$ENV_FILE") is out of range"
    fi
    if [ "$VPN_SUBNET_SET" = "0" ]; then
        VPN_SUBNET="$(env_read VPN_SUBNET)"
        [ -n "$VPN_SUBNET" ] || VPN_SUBNET=10.8.0.0/24
        validate_cidr "$VPN_SUBNET" || die_op "VPN_SUBNET in $(basename "$ENV_FILE") is not a valid CIDR"
    fi
    if [ "$CLIENT_DOMAIN_SET" = "0" ] && [ -z "$CLIENT_DOMAIN" ]; then
        CLIENT_DOMAIN="$(env_read CLIENT_DOMAIN)"
        if [ -n "$CLIENT_DOMAIN" ]; then
            validate_fqdn "$CLIENT_DOMAIN" || die_op "CLIENT_DOMAIN in $(basename "$ENV_FILE") is not a valid FQDN"
        fi
    fi
else
    cat > "$ROOT_DIR/$ENV_FILE" <<EOF
# AmneziaWG VPN Server — deployment-specific values (written by install.sh).
# versions.lock stays the single source of pinned versions: compose loads
# it with --env-file and it always wins over this file.
AWG_PORT=${AWG_PORT}
VPN_SUBNET=${VPN_SUBNET}
CLIENT_DOMAIN=${CLIENT_DOMAIN}
EOF
    chmod 0600 "$ROOT_DIR/$ENV_FILE"
    log "$ENV_FILE created with AWG_PORT=${AWG_PORT}, VPN_SUBNET=${VPN_SUBNET} (0600)"
fi

# T-124: the session cookie carries Secure only when the panel is
# reachable over TLS (--domain or --panel-port mode). The panel reads
# AMNEZIA_SECURE_COOKIES=1 from this file; in loopback mode the line
# must be absent again (a Secure cookie would never be sent back over
# plain HTTP and login would break). Additive: only this one line is
# ever touched here.
if [ -n "$DOMAIN" ] || [ -n "$PANEL_PORT" ]; then
    env_set AMNEZIA_SECURE_COOKIES 1
    log "AMNEZIA_SECURE_COOKIES=1 set in $ENV_FILE (TLS panel mode)"
elif grep -q '^AMNEZIA_SECURE_COOKIES=' "$ROOT_DIR/$ENV_FILE"; then
    env_unset AMNEZIA_SECURE_COOKIES
    log "AMNEZIA_SECURE_COOKIES removed from $ENV_FILE (loopback panel mode)"
fi

# --- 10b. client endpoint domain (T-129 / T-156): DNS pre-flight ------
# Optional --vpn-domain / --client-domain: the endpoint baked into
# client configs becomes <client-domain>:<AWG_PORT>. Omitted: public
# IP. The panel hostname is never copied onto clients.

# dns_preflight FQDN: the A/AAAA records of FQDN must resolve to the
# public IP of this server; dies with an actionable message otherwise.
dns_preflight() {
    local fqdn="$1" pub_ip dns_a dns_aaaa
    if ! command -v dig >/dev/null 2>&1; then
        run_apt_get install -y dnsutils || die_op "apt-get install dnsutils failed (DNS pre-flight)"
    fi
    pub_ip="$(cmd curl -fsS https://api.ipify.org 2>/dev/null | tr -d '[:space:]')"
    [ -n "$pub_ip" ] || die_op "cannot determine the public IP of this server (curl api.ipify.org)"
    dns_a="$(cmd dig +short @1.1.1.1 "$fqdn" A 2>/dev/null | tr -d '[:space:]')"
    dns_aaaa="$(cmd dig +short @1.1.1.1 "$fqdn" AAAA 2>/dev/null | tr -d '[:space:]')"
    if [ "$dns_a" != "$pub_ip" ] && [ "$dns_aaaa" != "$pub_ip" ]; then
        die_op "DNS pre-flight failed for $fqdn: public record '${dns_a} ${dns_aaaa}' does not match this server ($pub_ip). Create an A record $fqdn -> $pub_ip and rerun install.sh"
    fi
    log "DNS pre-flight ok: $fqdn -> $pub_ip"
}

client_domain_setup() {
    [ -n "$CLIENT_DOMAIN" ] || return 0
    # The panel-domain pre-flight (13b) already covers the same
    # hostname; a separate dig would be redundant.
    [ -n "$DOMAIN" ] && [ "$CLIENT_DOMAIN" = "$DOMAIN" ] && return 0
    log "client domain $CLIENT_DOMAIN: DNS pre-flight (endpoint of client configs)"
    dns_preflight "$CLIENT_DOMAIN"
}

client_domain_setup

# --- 10c. tunnel MTU pre-flight (T-6ztc) ------------------------------
# A full-size tunnel packet costs MTU + 60 bytes on the wire (32 AmneziaWG
# header/tag + 8 UDP + 20 IPv4). awg-quick would size the tunnel from the
# MTU of the default-route interface (1500 - 80 = 1420), which is wrong
# whenever the uplink is itself tunnelled: hosting providers running
# GRE/VXLAN hand out a 1500-byte interface over a 1476-byte path, and the
# resulting 1480-byte packets are silently lost. Nobody installing this is
# expected to work that out, so measure the path and store the answer;
# `panel server init --mtu` picks it up from the deployment .env.
TUNNEL_ENCAP_OVERHEAD=60
TUNNEL_MTU_CEILING=1420   # never exceed the historical WireGuard default
TUNNEL_MTU_FLOOR=1280     # IPv6 minimum link MTU: every path must carry it
# ${VAR-default}, not ${VAR:-default}: an explicitly empty value means
# "do not measure" (the harnesses set it that way for the runs that are
# not about MTU), while an unset variable takes the real targets.
UPLINK_PMTU_TARGETS="${AMNEZIA_INSTALL_PMTU_TARGETS-1.1.1.1 8.8.8.8}"

# probe_pmtu TARGET: largest ICMP payload that reaches TARGET unfragmented,
# as a full IP packet size; empty when the target does not answer at all.
probe_pmtu() {
    local target="$1" lo=1200 hi=1472 mid
    # Most uplinks are clean 1500: check that first so the common case
    # costs one probe instead of a full binary search.
    if cmd ping -c1 -W2 -M do -s "$hi" "$target" >/dev/null 2>&1; then
        printf '%s\n' "$(( hi + 28 ))"
        return 0
    fi
    cmd ping -c1 -W2 -M do -s "$lo" "$target" >/dev/null 2>&1 || return 1
    while [ $((hi - lo)) -gt 1 ]; do
        mid=$(( (lo + hi) / 2 ))
        if cmd ping -c1 -W2 -M do -s "$mid" "$target" >/dev/null 2>&1; then
            lo="$mid"
        else
            hi="$mid"
        fi
    done
    printf '%s\n' "$(( lo + 28 ))"
}

# measure_uplink_pmtu: the smallest path MTU seen across the probe targets,
# so the tunnel is sized for the worst path, not the luckiest one.
measure_uplink_pmtu() {
    local target probe best=0
    for target in $UPLINK_PMTU_TARGETS; do
        probe="$(probe_pmtu "$target")" || continue
        [ -n "$probe" ] || continue
        if [ "$best" -eq 0 ] || [ "$probe" -lt "$best" ]; then
            best="$probe"
        fi
    done
    [ "$best" -gt 0 ] && printf '%s\n' "$best"
}

tunnel_mtu_preflight() {
    local pmtu mtu
    pmtu="$(measure_uplink_pmtu)"
    if [ -z "$pmtu" ]; then
        log "WARNING: could not measure the uplink path MTU (ICMP filtered?); the built-in default applies"
        return 0
    fi
    mtu=$(( pmtu - TUNNEL_ENCAP_OVERHEAD ))
    if [ "$mtu" -gt "$TUNNEL_MTU_CEILING" ]; then
        mtu="$TUNNEL_MTU_CEILING"
    fi
    if [ "$mtu" -lt "$TUNNEL_MTU_FLOOR" ]; then
        log "WARNING: uplink path MTU $pmtu is unusually small; clamping the tunnel to $TUNNEL_MTU_FLOOR"
        mtu="$TUNNEL_MTU_FLOOR"
    fi
    if [ "$pmtu" -lt 1500 ]; then
        log "uplink path MTU is $pmtu, not the usual 1500 (this uplink tunnels its own traffic)"
    fi
    log "tunnel MTU $mtu (a full packet costs $((mtu + TUNNEL_ENCAP_OVERHEAD)) bytes on a $pmtu-byte path)"
    env_set TUNNEL_MTU "$mtu"
}

tunnel_mtu_preflight

# --- 11. host networking: managed nftables ruleset (M9.2) --------------

NFT_FRAGMENT="amnezia-vpn.nft"
NFT_DEPLOY_DIR="$ROOT_DIR/nftables"
NFT_DEPLOY_FILE="$NFT_DEPLOY_DIR/$NFT_FRAGMENT"
NFT_SYS_FILE="$NFTABLES_DIR/$NFT_FRAGMENT"
NFT_BEGIN='# --- amnezia-vpn begin ---'
NFT_END='# --- amnezia-vpn end ---'

# vpn_subnet_effective: awg0.conf (authoritative server.address) wins
# over .env/parameter; a missing config falls back to the deployment
# value. The comments explain the precedence contract (M9.2 audit).
vpn_subnet_effective() {
    local addr
    if [ -f "$ROOT_DIR/config/awg0.conf" ]; then
        addr="$(sed -n 's/^[[:space:]]*Address[[:space:]]*=[[:space:]]*//p' "$ROOT_DIR/config/awg0.conf" | head -1)"
        if [ -n "$addr" ] && validate_cidr "$addr"; then
            log "VPN subnet derived from config/awg0.conf (server.address): $addr" >&2
            network_of_host_cidr "$addr"
            return 0
        fi
        log "config/awg0.conf exists but has no valid Address: falling back to the deployment value" >&2
    fi
    # stdout carries only the CIDR value (validated by net_setup)
    log "VPN subnet from the deployment value: $VPN_SUBNET" >&2
    network_of_host_cidr "$VPN_SUBNET"
}

# render_nftables: the managed ruleset fragment. The table owns only
# what the ТЗ §9 contract needs: NAT for vpn subnet -> WAN (never to
# the tunnel itself), forwarding in both directions for the subnet,
# UDP AWG_PORT acceptance. No policies, no drops: SSH and all foreign
# traffic are untouched by construction. $3 carries pre-rendered extra
# input-chain accepts, additively and only for a panel TLS mode: with
# a panel domain (T-121) tcp 80 for ACME plus tcp 443 (or --panel-port);
# with --panel-port and no domain (T-124) only that tcp port. Loopback-only
# installs never open any of them.
render_nftables() {
    local input_rules="${3:-}"
    cat <<EOF
# Amnezia VPN managed nftables fragment (M9.2) — generated by install.sh.
# Do not edit; rerun install.sh to regenerate. Applies idempotently in a
# single batch (add + flush own table + recreate); never flushes foreign
# rules.

table ip amnezia {
    chain forward {
        # priority -100: evaluated before foreign filter chains (ufw/
        # docker/iptables all register at priority 0), so the VPN-subnet
        # accept is seen even when a host firewall defaults FORWARD to
        # DROP — without touching any foreign rule.
        type filter hook forward priority -100; policy accept;
        # Clamp TCP MSS to the outgoing route MTU. Path MTU Discovery is
        # the only other thing keeping segments small enough for the
        # tunnel, and it fails silently wherever the transit drops the
        # ICMP fragmentation-needed reply (mobile carriers, PPPoE at
        # 1492, plenty of home routers): small requests get through and
        # large transfers stall. The clamp must precede the accepts —
        # accept terminates the chain.
        tcp flags syn tcp option maxseg size set rt mtu
        ip saddr $1 accept
        ip daddr $1 accept
    }

    chain input {
        type filter hook input priority -100; policy accept;
        udp dport $2 accept
${input_rules}
    }

    chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        ip saddr $1 oifname != "awg0" masquerade
    }
}
EOF
}

# render_nftables_deploy: the same core ruleset, made idempotent in one
# batch: `table ip amnezia` (add) is a no-op when the table already
# exists, `flush table ip amnezia` clears only our own previous content,
# and the definition below re-creates it. Safe on a fresh boot (table
# absent: add creates it, flushing an empty table is a no-op) and on
# reinstall (table present: add no-ops, flush clears, rules recreated).
# Never touches foreign tables and never fails on missing state.
render_nftables_deploy() {
    {
        printf 'table ip amnezia\n'
        printf 'flush table ip amnezia\n'
        render_nftables "$1" "$2" "${3:-}"
    }
}

# nftables_persist: hook the fragment into the distro nftables.conf
# inside a managed marker block and pin the boot order in front of
# Docker. Foreign content of nftables.conf is left untouched.
nftables_persist() {
    local dir
    dir="$(dirname "$NFTABLES_CONF")"
    mkdir -p "$dir" || die_op "cannot create directory for $NFTABLES_CONF"
    if [ ! -f "$NFTABLES_CONF" ]; then
        printf '#!/usr/sbin/nft -f\n' > "$NFTABLES_CONF"
        chmod 0644 "$NFTABLES_CONF"
        log "$NFTABLES_CONF did not exist: created a minimal managed file"
    fi
    # replace the previous managed block, keep everything else
    BEGIN="$NFT_BEGIN" END="$NFT_END" awk '
        $0 == ENVIRON["BEGIN"] { skip = 1; next }
        $0 == ENVIRON["END"]   { skip = 0; next }
        skip { next }
        { print }
    ' "$NFTABLES_CONF" > "$NFTABLES_CONF.new" && mv "$NFTABLES_CONF.new" "$NFTABLES_CONF"
    {
        printf '\n%s\ninclude "%s"\n%s\n' "$NFT_BEGIN" "$NFT_SYS_FILE" "$NFT_END"
    } >> "$NFTABLES_CONF"

    cmd systemctl enable nftables || die_op "systemctl enable nftables failed"
    # Runtime flush guard: the distro nftables.conf may start with
    # `flush ruleset`, which wipes docker's own iptables-nft chains if
    # the service is (re)started while docker runs. Start the service
    # only when it is not active yet; on an already-active host the
    # ruleset fragment was applied directly by net_setup() and a reload
    # would destroy docker state for zero benefit. If we do start it
    # (fresh host), restart docker afterwards so it rebuilds its chains.
    # Restart via docker.socket then docker.service: Ubuntu 24 socket-
    # activated dockerd fails with "no sockets found via socket
    # activation" if only docker.service is bounced.
    if ! cmd systemctl is-active --quiet nftables; then
        cmd systemctl start nftables || die_op "systemctl start nftables failed"
        if cmd systemctl is-active --quiet docker; then
            log "restarting docker.socket and docker.service after nftables first start: already-running Docker and its containers will be bounced"
            cmd systemctl restart docker.socket docker.service \
                || die_op "systemctl restart docker.socket docker.service failed after nftables start"
            log "docker restarted via socket activation: rebuilt its iptables state after the nftables flush"
        fi
    fi
    cmd nft list table ip amnezia >/dev/null 2>&1 \
        || die_op "nftables was applied but the amnezia table is not present after reload"

    mkdir -p "$SYSTEMD_DIR/docker.service.d" || die_op "cannot create systemd drop-in dir"
    cat > "$SYSTEMD_DIR/docker.service.d/amnezia-vpn-nftables.conf" <<EOF
# amnezia-vpn managed (M9.2): the NAT/forward rules must exist before
# Docker and the AWG container start at boot — no tunnel-before-NAT race.
[Unit]
After=nftables.service
EOF
    chmod 0644 "$SYSTEMD_DIR/docker.service.d/amnezia-vpn-nftables.conf"
    cmd systemctl daemon-reload || die_op "systemctl daemon-reload failed"
}

net_setup() {
    log "host networking (nftables, M9.2): preparing the managed ruleset"

    if ! command -v nft >/dev/null 2>&1; then
        log "nft(8) missing: installing the nftables package"
        run_apt_get install -y nftables || die_op "apt-get install nftables failed"
    fi

    local subnet port input_rules
    subnet="$(vpn_subnet_effective)"
    validate_cidr "$subnet" || die_op "effective VPN subnet is invalid: $subnet"
    port="$AWG_PORT"
    validate_port "$port" || die_op "effective AWG port is invalid: $port"

    # Panel TLS modes open their tcp ports additively and ONLY in that
    # mode (T-121/T-156: 80 + 443 or --panel-port; T-124: the chosen
    # port for the self-signed IP mode). Loopback-only installs add nothing.
    input_rules=""
    if [ -n "$DOMAIN" ]; then
        local tls_tcp="${PANEL_PORT:-443}"
        input_rules=$(
            cat <<RULES
        tcp dport 80 accept
        tcp dport ${tls_tcp} accept
RULES
        )
    elif [ -n "$PANEL_PORT" ]; then
        input_rules="        tcp dport ${PANEL_PORT} accept"
    fi

    mkdir -p "$NFT_DEPLOY_DIR" || die_op "cannot create $NFT_DEPLOY_DIR"
    chmod 0750 "$NFT_DEPLOY_DIR"
    render_nftables_deploy "$subnet" "$port" "$input_rules" > "$NFT_DEPLOY_FILE"
    chmod 0644 "$NFT_DEPLOY_FILE"

    mkdir -p "$NFTABLES_DIR" || die_op "cannot create $NFTABLES_DIR"
    # system copy is byte-identical to the deploy copy
    cp "$NFT_DEPLOY_FILE" "$NFT_SYS_FILE"
    chmod 0644 "$NFT_SYS_FILE"

    # Syntax validation before anything is applied (M9.2 contract).
    if ! cmd nft -c -f "$NFT_SYS_FILE" >/dev/null 2>&1; then
        rm -f "$NFT_SYS_FILE" "$NFT_DEPLOY_FILE"
        die_op "nftables syntax check failed on the generated ruleset; nothing was applied"
    fi
    log "nftables: syntax check passed"

    # Idempotent apply: add (no-op when present) + flush own table +
    # recreate in one batch; on failure the previous rules stay
    # untouched (nft batches are atomic).
    if ! cmd nft -f "$NFT_SYS_FILE" >/dev/null 2>&1; then
        rm -f "$NFT_SYS_FILE" "$NFT_DEPLOY_FILE"
        die_op "nftables apply failed; the previous rules were not touched"
    fi
    log "nftables: ruleset applied (table ip amnezia; no foreign rules touched)"

    nftables_persist
    log "nftables: persisted via $NFTABLES_CONF + nftables.service; docker ordered after nftables"

    # A host firewall (ufw and/or docker) registers filter chains at the
    # same hook priority as the managed table and commonly defaults
    # FORWARD to DROP; nft filter chains cannot use a negative priority,
    # so the managed forward-accept would never be reached on such hosts
    # and tunnel egress would silently die (only ICMP passes, via the
    # firewall's own echo rule). The forward accept for the tunnel
    # interface is therefore ALSO inserted at the TOP of the docker
    # DOCKER-USER chain (docker's designated user hook, which docker
    # never rewrites) — or of FORWARD when DOCKER-USER is absent.
    # Additive only: nothing is flushed, nothing is dropped, and a
    # re-run never duplicates the rules (iptables -C guard).
    ensure_forward_accept() {
        command -v iptables >/dev/null 2>&1 || {
            log "forward accept: iptables not present; relying on the nft ruleset"
            return 0
        }
        # Boot persistence: docker/ufw rebuild their chains on every
        # boot, so the insertion runs again from a one-shot unit
        # (idempotent; no-op when the rules are already present).
        # Quoted delimiter: the unit file must receive the literal
        # "$chain"/"$d" (expanded at boot by /bin/sh), NOT the
        # installer's PID ("$$" would be expanded by this shell).
        cat > "$SYSTEMD_DIR/amnezia-vpn-forward.service" <<'EOF'
# amnezia-vpn managed (M9.2): tunnel egress forward accept for
# docker/ufw coexistence. Runs after docker and nftables, never
# flushes or drops anything, idempotent on every boot.
[Unit]
Description=Amnezia VPN forward accept for the tunnel interface
After=docker.service nftables.service
Wants=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'command -v iptables >/dev/null 2>&1 || exit 0; chain=FORWARD; iptables -t filter -L DOCKER-USER >/dev/null 2>&1 && chain=DOCKER-USER; for d in "-i awg0" "-o awg0"; do iptables -t filter -C "$chain" $d -j ACCEPT 2>/dev/null || iptables -t filter -I "$chain" 1 $d -j ACCEPT; done'

[Install]
WantedBy=multi-user.target
EOF
        chmod 0644 "$SYSTEMD_DIR/amnezia-vpn-forward.service"
        local chain="FORWARD" d=""
        if cmd iptables -t filter -L DOCKER-USER >/dev/null 2>&1; then
            chain="DOCKER-USER"
        fi
        for d in "-i awg0" "-o awg0"; do
            if ! cmd iptables -t filter -C "$chain" $d -j ACCEPT >/dev/null 2>&1; then
                cmd iptables -t filter -I "$chain" 1 $d -j ACCEPT \
                    || die_op "iptables forward accept failed ($chain $d)"
                log "forward accept: inserted $d -j ACCEPT into $chain"
            fi
        done
        cmd systemctl daemon-reload >/dev/null 2>&1 \
            || die_op "systemctl daemon-reload failed (forward accept unit)"
        cmd systemctl enable amnezia-vpn-forward.service >/dev/null 2>&1 \
            || die_op "systemctl enable amnezia-vpn-forward.service failed"
        cmd systemctl start amnezia-vpn-forward.service >/dev/null 2>&1 \
            || die_op "systemctl start amnezia-vpn-forward.service failed"
        log "forward accept: persisted via amnezia-vpn-forward.service"
    }

    ensure_forward_accept
}

net_setup

# --- 12. compose config validation ------------------------------------

log "validating: docker compose --env-file versions.lock config --quiet"
docker_compose --env-file versions.lock config --quiet \
    || die_op "docker compose config failed under $ROOT_DIR"

# --- 13. build and start the stack ------------------------------------

# Pull the images CI published rather than compiling them here: building
# on a 1-vCPU VPS means downloading an 800 MB Go toolchain, cloning
# amneziawg-go and amneziawg-tools and compiling four binaries, all to
# produce ~60 MB that GitHub already built. --build keeps the old path for
# development, and an unreachable registry falls back to it rather than
# failing the install.
IMAGES_BUILT=0
build_images() {
    log "building the stack images (pinned versions from versions.lock)"
    docker_compose --env-file versions.lock build \
        || die_op "docker compose build failed"
    IMAGES_BUILT=1
}

if [ "$BUILD_FROM_SOURCE" = "1" ]; then
    build_images
else
    log "pulling the prebuilt stack images (this replaces a long local build)"
    if ! docker_compose --env-file versions.lock pull; then
        log "WARNING: could not pull the prebuilt images; building them here instead"
        build_images
    fi
fi

if [ "$IMAGES_BUILT" = "1" ]; then
    if [ "${AMNEZIA_INSTALL_SKIP_PRUNE:-}" = "1" ]; then
        log "skipping docker prune (AMNEZIA_INSTALL_SKIP_PRUNE=1)"
    else
        log "pruning Docker build cache and golang toolchain images"
        if ! "$ROOT_DIR/docker-prune.sh"; then
            log "WARNING: docker prune failed; continuing"
        fi
    fi
fi
log "starting the stack"
if ! docker_compose --env-file versions.lock up -d; then
    # M3.1 contract: on a fresh install there is no server row yet, so
    # panel-init exits 1 and `up` fails through depends_on
    # (service_completed_successfully). The failure is tolerated ONLY
    # when the panel-init log of this very `up` actually reports the
    # fresh-install state; any other exit-1 (broken pending restore,
    # corrupted database, boot-snapshot error, sentinel guard) is a real
    # failure and must surface to the operator (T-111).
    PI_LOG="$(docker_compose --env-file versions.lock logs --no-color panel-init 2>/dev/null || true)"
    # Exact M3.1 marker (awgconf.ErrNoServerRow): "no server row (id=1); ...".
    # A looser match would also hit the sentinel-guard message
    # ("no server row was found — the database was lost") and mask a
    # real data-loss case as a fresh install (T-111 rework).
    if printf '%s\n' "$PI_LOG" | grep -q "no server row (id=1)"; then
        log "fresh-install state: panel-init exited 1 (M3.1, no server row yet); stack created-but-not-running"
    else
        printf '%s\n' \
            "install: ERROR: docker compose up -d failed (panel-init exited 1 but not the M3.1 no-server-row state)" \
            "install: panel-init log:" "$PI_LOG" >&2
        exit "$FAIL_STYLE_OP"
    fi
fi

# Enable the weekly prune timer only after compose up (and after this
# install's own docker-prune.sh). Persistent=true would otherwise fire a
# missed weekly slot immediately and prune builder/golang during the
# first compose build.
cmd systemctl enable --now amnezia-vpn-prune.timer \
    || die_op "systemctl enable --now amnezia-vpn-prune.timer failed"
log "weekly docker-prune timer enabled (ExecStart=$ROOT_DIR/docker-prune.sh)"

# --- 13b. panel domain: reverse proxy + Let's Encrypt (T-121) ---------
# Optional --domain mode: nginx terminates TLS in front of the
# loopback-only panel and certbot provisions a Let's Encrypt
# certificate (HTTP-01). The DNS pre-flight runs BEFORE any certbot
# call so a missing/mismatched record never burns the rate limit.
# Without --domain this whole section is a no-op and ports 80/443 stay
# closed.

NGINX_CONF_DIR="$ROOT_DIR/nginx"
NGINX_SITE="amnezia-panel"
ACME_ROOT="${AMNEZIA_INSTALL_ACME_ROOT:-/var/www/certbot}"

render_nginx_conf() { # render_nginx_conf DOMAIN PHASE [TLS_PORT]
    local domain="$1" phase="$2" tls_port="${3:-443}"
    local cert="/etc/letsencrypt/live/${domain}/fullchain.pem"
    local key="/etc/letsencrypt/live/${domain}/privkey.pem"
    local https_redirect="https://\$host\$request_uri"
    if [ "$tls_port" != "443" ]; then
        https_redirect="https://\$host:${tls_port}\$request_uri"
    fi
    cat <<EOF
# Amnezia VPN panel reverse proxy (T-121) — managed by install.sh.
# The panel stays loopback-only (127.0.0.1:8787); TLS terminates here.
server {
    listen 80;
    listen [::]:80;
    server_name ${domain};
    location /.well-known/acme-challenge/ {
        root ${ACME_ROOT};
    }
    location / {
        return 301 ${https_redirect};
    }
}
EOF
    if [ "$phase" = "phase2" ]; then
        cat <<EOF

server {
    listen ${tls_port} ssl;
    listen [::]:${tls_port} ssl;
    server_name ${domain};
    ssl_certificate ${cert};
    ssl_certificate_key ${key};
    ssl_protocols TLSv1.2 TLSv1.3;
    location / {
        proxy_pass http://127.0.0.1:8787;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
    fi
}

domain_setup() {
    [ -n "$DOMAIN" ] || return 0

    local tls_port="${PANEL_PORT:-443}"
    local panel_url="https://${DOMAIN}"
    if [ "$tls_port" != "443" ]; then
        panel_url="https://${DOMAIN}:${tls_port}"
    fi

    log "panel domain $DOMAIN: DNS pre-flight before any certificate attempt"
    dns_preflight "$DOMAIN"

    if ! command -v nginx >/dev/null 2>&1; then
        run_apt_get install -y nginx || die_op "apt-get install nginx failed"
    fi
    mkdir -p "$NGINX_CONF_DIR" "$ACME_ROOT" || die_op "cannot create nginx/acme dirs"
    chmod 0750 "$NGINX_CONF_DIR"
    render_nginx_conf "$DOMAIN" "phase1" "$tls_port" > "$NGINX_CONF_DIR/panel.conf"
    chmod 0644 "$NGINX_CONF_DIR/panel.conf"
    ln -sf "$NGINX_CONF_DIR/panel.conf" "/etc/nginx/sites-enabled/$NGINX_SITE"
    rm -f /etc/nginx/sites-enabled/default
    cmd nginx -t >/dev/null 2>&1 || die_op "nginx configuration test failed"
    cmd systemctl enable nginx >/dev/null 2>&1 || die_op "systemctl enable nginx failed"
    if ! cmd systemctl is-active --quiet nginx; then
        cmd systemctl start nginx || die_op "systemctl start nginx failed"
    else
        cmd nginx -s reload >/dev/null 2>&1 || true
    fi

    if ! command -v certbot >/dev/null 2>&1; then
        run_apt_get install -y certbot || die_op "apt-get install certbot failed"
    fi
    if ! certbot certonly --webroot -w "$ACME_ROOT" -d "$DOMAIN" \
        --non-interactive --agree-tos --register-unsafely-without-email \
        --keep-until-expiring >/dev/null 2>&1; then
        die_op "Let's Encrypt issuance failed for $DOMAIN (see /var/log/letsencrypt); nothing was published"
    fi
    cmd systemctl enable certbot.timer >/dev/null 2>&1 || true
    cmd systemctl start certbot.timer >/dev/null 2>&1 || true

    render_nginx_conf "$DOMAIN" "phase2" "$tls_port" > "$NGINX_CONF_DIR/panel.conf"
    chmod 0644 "$NGINX_CONF_DIR/panel.conf"
    cmd nginx -t >/dev/null 2>&1 || die_op "nginx TLS configuration test failed"
    cmd nginx -s reload >/dev/null 2>&1 || true
    log "panel domain: $panel_url ready (certbot.timer renews automatically)"
}

domain_setup

# --- 13c. panel IP:port mode: self-signed TLS (T-124) ------------------
# No domain, but --panel-port given: the panel stays loopback-only in
# compose; nginx terminates TLS in front of it with a self-signed
# certificate (Let's Encrypt does not issue for bare IPs) and proxies
# to 127.0.0.1:8787. With a panel domain, TLS is Let's Encrypt
# (domain_setup) even when --panel-port is set — this section is skipped.
#
# The summary prints the sha256 fingerprint: browsers reject the
# untrusted certificate once and the operator verifies the fingerprint
# before proceeding.

PANEL_TLS_DIR="$ROOT_DIR/tls"
PANEL_TLS_CERT="$PANEL_TLS_DIR/panel.crt"
PANEL_TLS_KEY="$PANEL_TLS_DIR/panel.key"
PANEL_TLS_IP=""
PANEL_TLS_FINGERPRINT=""

render_nginx_ip_conf() { # render_nginx_ip_conf PORT CERT KEY
    cat <<EOF
# Amnezia VPN panel reverse proxy (T-124) — managed by install.sh.
# The panel stays loopback-only (127.0.0.1:8787); TLS terminates here
# with the self-signed certificate.
server {
    listen $1 ssl;
    listen [::]:$1 ssl;
    ssl_certificate $2;
    ssl_certificate_key $3;
    ssl_protocols TLSv1.2 TLSv1.3;
    location / {
        proxy_pass http://127.0.0.1:8787;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
}

panel_port_setup() {
    [ -n "$PANEL_PORT" ] || return 0
    [ -z "$DOMAIN" ] || return 0

    log "panel IP:port mode: nginx TLS on port $PANEL_PORT (self-signed)"
    if ! command -v openssl >/dev/null 2>&1; then
        run_apt_get install -y openssl || die_op "apt-get install openssl failed"
    fi
    local pub_ip
    pub_ip="$(cmd curl -fsS https://api.ipify.org 2>/dev/null | tr -d '[:space:]')"
    [ -n "$pub_ip" ] || die_op "cannot determine the public IP of this server (curl api.ipify.org)"
    PANEL_TLS_IP="$pub_ip"

    mkdir -p "$PANEL_TLS_DIR" || die_op "cannot create $PANEL_TLS_DIR"
    chmod 0700 "$PANEL_TLS_DIR"

    if [ "$PANEL_TLS_REGEN" = "1" ] || [ ! -f "$PANEL_TLS_CERT" ] || [ ! -f "$PANEL_TLS_KEY" ]; then
        if [ "$PANEL_TLS_REGEN" = "1" ]; then
            log "regenerating the self-signed TLS certificate (--panel-tls-regen)"
        else
            log "generating a self-signed TLS certificate (CN=$pub_ip, ~825 days)"
        fi
        # openssl refuses to overwrite an existing key non-interactively:
        # remove any stale half-pair first, then write both files fresh.
        rm -f "$PANEL_TLS_CERT" "$PANEL_TLS_KEY"
        if ! cmd openssl req -x509 -nodes -newkey rsa:2048 -sha256 -days 825 \
            -keyout "$PANEL_TLS_KEY" -out "$PANEL_TLS_CERT" -subj "/CN=$pub_ip" \
            >/dev/null 2>&1; then
            rm -f "$PANEL_TLS_CERT" "$PANEL_TLS_KEY"
            die_op "openssl self-signed certificate generation failed"
        fi
        chmod 0600 "$PANEL_TLS_KEY"
        chmod 0644 "$PANEL_TLS_CERT"
        log "TLS certificate written: cert $PANEL_TLS_CERT (0644), key $PANEL_TLS_KEY (0600)"
    else
        log "TLS certificate already present under $PANEL_TLS_DIR: keeping it"
    fi

    if ! command -v nginx >/dev/null 2>&1; then
        run_apt_get install -y nginx || die_op "apt-get install nginx failed"
    fi
    mkdir -p "$NGINX_CONF_DIR" || die_op "cannot create nginx conf dir"
    chmod 0750 "$NGINX_CONF_DIR"
    render_nginx_ip_conf "$PANEL_PORT" "$PANEL_TLS_CERT" "$PANEL_TLS_KEY" > "$NGINX_CONF_DIR/panel.conf"
    chmod 0644 "$NGINX_CONF_DIR/panel.conf"
    ln -sf "$NGINX_CONF_DIR/panel.conf" "/etc/nginx/sites-enabled/$NGINX_SITE"
    rm -f /etc/nginx/sites-enabled/default
    cmd nginx -t >/dev/null 2>&1 || die_op "nginx configuration test failed"
    cmd systemctl enable nginx >/dev/null 2>&1 || die_op "systemctl enable nginx failed"
    if ! cmd systemctl is-active --quiet nginx; then
        cmd systemctl start nginx || die_op "systemctl start nginx failed"
    else
        cmd nginx -s reload >/dev/null 2>&1 || true
    fi

    PANEL_TLS_FINGERPRINT="$(cmd openssl x509 -in "$PANEL_TLS_CERT" -noout -fingerprint -sha256 2>/dev/null | sed 's/.*=//')"
    [ -n "$PANEL_TLS_FINGERPRINT" ] || die_op "cannot compute the certificate fingerprint (openssl x509 -fingerprint)"
    log "panel IP:port mode: https://$pub_ip:$PANEL_PORT ready (fingerprint $PANEL_TLS_FINGERPRINT)"
}

panel_port_setup

# --- 14. minimal post-install self-check -------------------------------
# Reads-only: no application state is mutated. Awaiting `panel server
# init` or a restore, panel-init exits 1 by design (M3.1 contract) and
# the panel/awg services stay created-but-not-running: this is the
# expected post-install state, so the self-check verifies the
# infrastructure, not the application bootstrap.

if ! cmd docker info --format '{{.ServerVersion}}' >/dev/null 2>&1; then
    die_op "self-check: the Docker daemon is not reachable"
fi
log "self-check: Docker daemon reachable ($(cmd docker info --format '{{.ServerVersion}}' 2>/dev/null))"

PS_OUT="$(docker_compose --env-file versions.lock ps -a 2>/dev/null || true)"
for svc in panel-init panel awg; do
    printf '%s\n' "$PS_OUT" | grep -q "$svc" \
        || die_op "self-check: service $svc is absent from the stack (docker compose ps -a)"
done
log "self-check: services panel-init/panel/awg present in the stack"

# --- 15. final status + SSH tunnel hint --------------------------------

HOST_HINT="<server-ip>"
if command -v hostname >/dev/null 2>&1 && hostname -I 2>/dev/null | grep -q '[0-9]'; then
    HOST_HINT="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi

cat <<EOF

install: DONE — the AmneziaWG VPN Server stack is deployed under $ROOT_DIR.
install: Next steps (application bootstrap is intentionally manual):
install:   panel-init needs a server row: run inside the deployed stack
EOF
if [ -n "$CLIENT_DOMAIN" ]; then
    cat <<EOF
install:     docker compose --env-file versions.lock run --rm panel-init \\
install:       /app/panel server init 10.8.0.1/24 ${AWG_PORT} --endpoint ${CLIENT_DOMAIN}:${AWG_PORT} --dns 1.1.1.1,8.8.8.8
install:   (clients are bound to ${CLIENT_DOMAIN}: moving to another server
install:   is an A-record change — clients reconnect themselves, configs are
install:   not re-issued; migrate the database with the backup buttons)
EOF
else
    cat <<EOF
install:     docker compose --env-file versions.lock run --rm panel-init \\
install:       /app/panel server init 10.8.0.1/24 ${AWG_PORT} --endpoint <public-ip>:${AWG_PORT} --dns 1.1.1.1,8.8.8.8
EOF
fi
cat <<EOF
install:   (--dns is recommended: client configs only get a DNS line when the
install:   server has one; without it a full-tunnel client (AllowedIPs
install:   0.0.0.0/0) loses the local network's DNS, and internet goes dark.
install:   DNS can also be set later with: /app/panel server update --dns ...)
install:   or restore an existing database:
install:     docker compose --env-file versions.lock run --rm panel-init \\
install:       /app/panel restore <archive>
EOF
if [ -n "$DOMAIN" ]; then
    panel_url="https://${DOMAIN}"
    if [ -n "$PANEL_PORT" ] && [ "$PANEL_PORT" != "443" ]; then
        panel_url="https://${DOMAIN}:${PANEL_PORT}"
    fi
    cat <<EOF
install: Panel: ${panel_url} (Let's Encrypt, auto-renewed by certbot.timer)
EOF
elif [ -n "$PANEL_PORT" ]; then
    cat <<EOF
install: Panel: https://${PANEL_TLS_IP}:${PANEL_PORT} (self-signed TLS certificate)
install:   TLS fingerprint (SHA256): ${PANEL_TLS_FINGERPRINT}
install:   The browser will warn about the untrusted certificate on first
install:   connect: compare the fingerprint above with the one the browser
install:   shows, then proceed. Verify it yourself at any time:
install:     openssl s_client -connect ${PANEL_TLS_IP}:${PANEL_PORT} </dev/null 2>/dev/null \\
install:       | openssl x509 -noout -fingerprint -sha256
EOF
else
    cat <<EOF
install: The panel is bound to the host loopback only:
install:   ssh -L 8787:127.0.0.1:8787 root@${HOST_HINT}
install: then open http://127.0.0.1:8787 in the local browser.
EOF
fi
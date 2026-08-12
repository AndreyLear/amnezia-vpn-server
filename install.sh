#!/bin/bash
#
# install.sh — M9.1 production deployment of the AmneziaWG VPN Server
# stack (contract: AUDITS/M9.1_AUDIT.md, ТЗ v2.0 §9).
#
# install.sh is infrastructure state, not application backup (ТЗ §9).
# It sets up the host (OS check, Docker Engine + Compose plugin from the
# official Docker Inc. repository, persistent ip_forward) and deploys the
# compose stack under a deployment root. Full nftables/NAT/forward rules
# and host networking are NOT part of M9.1 (separate M9 networking task).
#
# Install flow (ТЗ §9 / M9.1 contract):
#   1. OS check (Debian 12, Ubuntu 22.04, Ubuntu 24.04 only)
#   2. Docker/Compose installation or verification
#   3. verify Docker Compose >= 2.20
#   4. enable/start the Docker service
#   5. verify/persist host net.ipv4.ip_forward
#   6. create the deployment layout under the root
#   7. secure directory permissions
#   8. install/copy repository deployment files
#   9. create deployment .env (deployment-specific values)
#  10. docker compose --env-file versions.lock config --quiet
#  11. build and start the stack (current compose contract)
#  12. minimal post-install self-check (never mutates application state)
#  13. final status + SSH tunnel hint for the loopback-only panel
#
# Arguments (only these are supported):
#   --root DIR      deployment root (default: /opt/amnezia-vpn)
#   --awg-port PORT external UDP port of the AWG runtime, 1..65535
#                   (default: 51820)
#   --help          usage
#
# Testability hooks (environment, not arguments):
#   AMNEZIA_INSTALL_TEST=1             skip the root-user check
#   AMNEZIA_INSTALL_FAKE_DIR=DIR       prefix PATH with DIR so fakes can
#                                      stand in for docker/apt-get/
#                                      systemctl/sysctl/curl: tests never
#                                      touch the real host
#   AMNEZIA_INSTALL_OS_RELEASE=FILE    os-release source (default
#                                      /etc/os-release)
#   AMNEZIA_INSTALL_SYSCTL_DIR=DIR     persistence dir for the ip_forward
#                                      override (default /etc/sysctl.d)
#   AMNEZIA_INSTALL_KEYRING_DIR=DIR    Docker Inc. keyring dir
#                                      (default /etc/apt/keyrings)
#   AMNEZIA_INSTALL_APT_SOURCES_DIR=DIR   apt sources.d dir
#                                      (default /etc/apt/sources.list.d)
#
# Security contract: no secrets are ever accepted, logged or printed;
# .env is created only when absent, with 0600; the panel stays
# loopback-only (127.0.0.1:8787:8787) and is never published.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${ROOT_DIR:-/opt/amnezia-vpn}"
AWG_PORT=51820

OS_RELEASE="${AMNEZIA_INSTALL_OS_RELEASE:-/etc/os-release}"
SYSCTL_DIR="${AMNEZIA_INSTALL_SYSCTL_DIR:-/etc/sysctl.d}"
SYSCTL_FILE="${SYSCTL_DIR}/99-amnezia-vpn.conf"
KEYRING_DIR="${AMNEZIA_INSTALL_KEYRING_DIR:-/etc/apt/keyrings}"
APT_SOURCES_DIR="${AMNEZIA_INSTALL_APT_SOURCES_DIR:-/etc/apt/sources.list.d}"
ENV_FILE=".env"

FAIL_STYLE_NONE=0
FAIL_STYLE_USAGE=2
FAIL_STYLE_OP=1

usage() {
    cat <<'EOF'
install.sh — M9.1 deployment of the AmneziaWG VPN Server stack.

Usage:
  ./install.sh [--root DIR] [--awg-port PORT]

Options:
  --root DIR       deployment root (default: /opt/amnezia-vpn)
  --awg-port PORT  external UDP port of the AWG runtime,
                   1..65535 (default: 51820)
  --help           print this message

Installs only supported OSes (Debian 12, Ubuntu 22.04, Ubuntu 24.04),
Docker Engine + Compose plugin (>= 2.20) from the official Docker Inc.
repository, persists net.ipv4.ip_forward, then builds and starts the
stack under the deployment root. nftables/NAT rules are not part of
M9.1. The panel always stays loopback-only; an SSH tunnel directive is
printed after a successful install.
EOF
}

log() { printf 'install: %s\n' "$*"; }

die() {
    printf 'install: ERROR: %s\n' "$2" >&2
    exit "$1"
}

die_usage() { die "$FAIL_STYLE_USAGE" "$1"; }
die_op() { die "$FAIL_STYLE_OP" "$1"; }

# --- argument parsing (only --root, --awg-port, --help) ---------------

validate_port() {
    [ "$1" -ge 1 ] 2>/dev/null && [ "$1" -le 65535 ] 2>/dev/null
}

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
            shift 2
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

compose_version_min() { # docker compose version -> "X.Y" when >= 2.20
    local ver major minor out
    out="$("$@" version 2>/dev/null | head -1)"
    [ -n "$out" ] || return 1
    # portable extraction (BSD/GNU sed): drop the prefix, keep digits/dots
    ver="$(printf '%s\n' "$out" | sed 's/.*version[[:space:]]*//' | sed 's/[^0-9.]*//' | cut -d. -f1,2)"
    [ -n "$ver" ] || return 1
    major="${ver%%.*}"
    minor="${ver#*.}"
    minor="${minor%%.*}"
    [ "$major" -ge 2 ] && { [ "$major" -gt 2 ] || [ "$minor" -ge 20 ]; }
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
    cmd apt-get update
    cmd apt-get install -y ca-certificates curl
    mkdir -p "$KEYRING_DIR" || die_op "cannot create keyring dir $KEYRING_DIR"
    chmod 0755 "$KEYRING_DIR"
    cmd curl -fsSL "https://download.docker.com/linux/${os_id}/gpg" -o "$KEYRING_DIR/docker.asc"
    mkdir -p "$APT_SOURCES_DIR" || die_op "cannot create apt sources dir $APT_SOURCES_DIR"
    printf 'deb [arch=amd64 signed-by=%s] https://download.docker.com/linux/%s %s stable\n' \
        "$KEYRING_DIR/docker.asc" "$os_id" "$os_codename" \
        > "$APT_SOURCES_DIR/docker.list"
    cmd apt-get update
    cmd apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

if docker_compose_ok; then
    log "Docker Compose: already present and >= 2.20"
else
    log "Docker Compose missing or < 2.20: installing from the official repository"
    docker_install
    if docker_compose_ok; then
        log "Docker Compose installed: $(docker compose version)"
    else
        die_op "Docker Compose is still missing or < 2.20 after installation; refusing to continue (deployment contract)"
    fi
fi

# --- 4. Docker service -------------------------------------------------

log "enabling and starting the Docker service"
cmd systemctl enable --now docker || die_op "systemctl enable --now docker failed"

# --- 5. net.ipv4.ip_forward (verify; persist only when disabled) ------

ip_forward_value() { cmd sysctl -n net.ipv4.ip_forward 2>/dev/null | tr -d ' '; }

if [ "$(ip_forward_value)" = "1" ]; then
    log "net.ipv4.ip_forward already enabled"
else
    log "net.ipv4.ip_forward disabled: enabling and persisting it (no other sysctl is touched)"
    mkdir -p "$SYSCTL_DIR"
    printf 'net.ipv4.ip_forward = 1\n' > "$SYSCTL_FILE"
    chmod 0644 "$SYSCTL_FILE"
    cmd sysctl -w net.ipv4.ip_forward=1 || die_op "sysctl -w net.ipv4.ip_forward=1 failed"
    [ "$(ip_forward_value)" = "1" ] || die_op "net.ipv4.ip_forward is still disabled after enabling it"
fi

# --- 6./7. deployment layout + permissions ----------------------------

mkdir -p "$ROOT_DIR" || die_op "cannot create deployment root $ROOT_DIR"
[ -d "$ROOT_DIR" ] || die_op "deployment root $ROOT_DIR is not a directory"
chmod 0750 "$ROOT_DIR"

for sub in data config status backups; do
    mkdir -p "$ROOT_DIR/$sub"
    chmod 0700 "$ROOT_DIR/$sub"
done
log "deployment layout created under $ROOT_DIR (data/config/status/backups: 0700)"

# --- 8. install/copy repository deployment files ----------------------

copy_tree() { # cp -a with per-file error stop
    if ! cp -a "$@" 2>/dev/null; then
        die_op "copying deployment files failed: $*"
    fi
}

copy_tree "$SCRIPT_DIR/compose.yaml" "$ROOT_DIR/"
copy_tree "$SCRIPT_DIR/versions.lock" "$ROOT_DIR/"
# Build context is the repository root (compose.yaml build.context = "."):
# the app/ tree (panel Dockerfile + Go module incl. embedded templates,
# awg Dockerfile + entrypoint scripts) must be present under the root.
copy_tree "$SCRIPT_DIR/app" "$ROOT_DIR/"
chmod 0644 "$ROOT_DIR/compose.yaml" "$ROOT_DIR/versions.lock"
log "deployment files installed (compose.yaml, versions.lock, app/)"

# --- 9. deployment .env (never overwrites an existing file) -----------

if [ -f "$ROOT_DIR/$ENV_FILE" ]; then
    log "$ENV_FILE already exists under $ROOT_DIR: keeping it untouched"
else
    cat > "$ROOT_DIR/$ENV_FILE" <<EOF
# AmneziaWG VPN Server — deployment-specific values (written by install.sh).
# versions.lock stays the single source of pinned versions: compose loads
# it with --env-file and it always wins over this file.
AWG_PORT=${AWG_PORT}
EOF
    chmod 0600 "$ROOT_DIR/$ENV_FILE"
    log "$ENV_FILE created with AWG_PORT=${AWG_PORT} (0600)"
fi

# --- 10. compose config validation ------------------------------------

log "validating: docker compose --env-file versions.lock config --quiet"
docker_compose --env-file versions.lock config --quiet \
    || die_op "docker compose config failed under $ROOT_DIR"

# --- 11. build and start the stack ------------------------------------

log "building the stack images (pinned versions from versions.lock)"
docker_compose --env-file versions.lock build \
    || die_op "docker compose build failed"
log "starting the stack"
docker_compose --env-file versions.lock up -d \
    || die_op "docker compose up -d failed"

# --- 12. minimal post-install self-check -------------------------------
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

# --- 13. final status + SSH tunnel hint --------------------------------

HOST_HINT="<server-ip>"
if command -v hostname >/dev/null 2>&1 && hostname -I 2>/dev/null | grep -q '[0-9]'; then
    HOST_HINT="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi

cat <<EOF

install: DONE — the AmneziaWG VPN Server stack is deployed under $ROOT_DIR.
install: Next steps (application bootstrap is intentionally manual):
install:   panel-init needs a server row: run inside the deployed stack
install:     docker compose --env-file versions.lock run --rm panel-init \
install:       /app/panel server init 10.8.0.1/24 51820 --endpoint <public-ip>:${AWG_PORT}
install:   or restore an existing database:
install:     docker compose --env-file versions.lock run --rm panel-init \
install:       /app/panel restore <archive> --identity-stdin  (M8 restore flow)
install: The panel is bound to the host loopback only:
install:   ssh -L 8787:127.0.0.1:8787 root@${HOST_HINT}
install: then open http://127.0.0.1:8787 in the local browser.
EOF
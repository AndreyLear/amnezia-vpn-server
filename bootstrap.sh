#!/bin/bash
#
# bootstrap.sh — operator-side one-shot installer (T-123).
#
# Runs on the OPERATOR machine (not the VPS): collect answers or CI
# flags, copy the project over SSH, run install.sh on the server,
# create the admin user, print one summary (panel URL + login +
# temporary password). The password is never saved and never placed
# on a process argv.
#
# Usage:
#   ./bootstrap.sh
#   ./bootstrap.sh --ip HOST [--user USER] [--key FILE|--password-env VAR]
#                  [--panel-domain FQDN|--domain FQDN]
#                  [--vpn-domain FQDN|--client-domain FQDN]
#                  [--panel-port PORT] [--awg-port PORT] [--root DIR] [--source URL]
#
# Testability: prepend a fake PATH (ssh/scp/tar/sshpass/curl/openssl)
# as in app/panel/test_m91_install.sh. Nothing here mutates the operator
# host beyond a temp directory that is removed on exit.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEFAULT_USER=root
# Host nginx TLS port for IP:port mode (T-124). Must not be 8787: compose
# already binds the panel to 127.0.0.1:8787.
DEFAULT_PANEL_PORT=8443
DEFAULT_AWG_PORT=443
DEFAULT_ROOT=/opt/amnezia-vpn
SSH_CONNECT_TIMEOUT="${AMNEZIA_BOOTSTRAP_SSH_TIMEOUT:-15}"
CURL_TIMEOUT=20

FAIL_STYLE_OP=1
FAIL_STYLE_USAGE=2

SSH_HOST=""
SSH_USER=""
KEY_FILE=""
PASSWORD_ENV=""
DOMAIN=""
CLIENT_DOMAIN=""
PANEL_PORT=""
AWG_PORT=""
ROOT_DIR=""
SOURCE_URL=""
PANEL_PORT_SET=0
DOMAIN_SET=0
CLIENT_DOMAIN_SET=0
NONINTERACTIVE=0
USE_COLOR=0
C_RESET=""
C_BOLD=""
C_CYAN=""
C_GREEN=""
C_RED=""

BIND_CLIENTS=0
AUTH_MODE="" # key | password
SSH_PASSWORD=""
ADMIN_PASSWORD=""
PUBLIC_IP=""
ENDPOINT=""
PANEL_URL=""
FINGERPRINT=""
REMOTE_TMP=""
BUNDLE=""

usage() {
    cat <<'EOF'
bootstrap.sh — one-shot AmneziaWG VPN Server install from the operator machine.

Usage:
  ./bootstrap.sh
  ./bootstrap.sh --ip HOST [--user USER] [--key FILE|--password-env VAR]
                 [--panel-domain FQDN | --domain FQDN]
                 [--vpn-domain FQDN | --client-domain FQDN]
                 [--panel-port PORT] [--awg-port PORT] [--root DIR] [--source URL]

Options:
  --ip HOST            VPS address (hostname or IPv4). With this flag,
                       the script does not prompt (CI mode).
  --user USER          SSH user (default: root)
  --key FILE           SSH private key (default: ~/.ssh/id_ed25519, else
                       ~/.ssh/id_rsa)
  --password-env VAR   read the SSH password from the environment variable
                       VAR (never from argv). Requires sshpass or expect.
  --panel-domain FQDN  panel HTTPS with Let's Encrypt (passed to
                       install.sh). Alias: --domain.
  --domain FQDN        alias of --panel-domain
  --panel-port PORT    nginx TLS listen port. With --panel-domain:
                       https://PANEL_DOMAIN:PORT. Without a panel domain
                       (default in CI: 8443): self-signed https://IP:PORT.
  --vpn-domain FQDN    bind client configs to FQDN:awg-port. Alias:
                       --client-domain. Omitted: public IP. The panel
                       hostname is never copied onto clients.
  --client-domain FQDN alias of --vpn-domain
  --awg-port PORT      AmneziaWG UDP port (default: 443; avoid 51820)
  --root DIR           deployment root on the server (default: /opt/amnezia-vpn)
  --source URL         download a release tarball instead of packing the
                       local repository
  --help               print this message

Examples:
  ./bootstrap.sh --ip 203.0.113.10 --panel-domain panel.example.com --vpn-domain example.com
  ./bootstrap.sh --ip 203.0.113.10 --panel-domain panel.example.com --panel-port 8443 --vpn-domain example.com
  ./bootstrap.sh --ip 203.0.113.10 --panel-port 8443

Without --ip, starts a numbered Russian wizard (Enter accepts defaults).
EOF
}

log() { printf 'bootstrap: %s\n' "$*" >&2; }

die() {
    printf '%sbootstrap: ERROR: %s%s\n' "$C_RED" "$2" "$C_RESET" >&2
    exit "$1"
}
die_usage() { die "$FAIL_STYLE_USAGE" "$1"; }
die_op() { die "$FAIL_STYLE_OP" "$1"; }

# Refuse values that would break remote quoting or look like injection.
validate_safe() {
    case "$1" in
        "" | *$'\n'* | *\'* | *\"* | *'$'* | *';'* | *'`'* | *'\\'* )
            return 1
            ;;
    esac
    return 0
}

validate_port() {
    [ "$1" -ge 1 ] 2>/dev/null && [ "$1" -le 65535 ] 2>/dev/null
}

validate_ipv4() {
    local o1 o2 o3 o4 o
    IFS=. read -r o1 o2 o3 o4 <<EOF
$1
EOF
    [ -n "$o4" ] || return 1
    case "$1" in
        *[!0-9.]* | *.*.*.*.*) return 1 ;;
    esac
    for o in "$o1" "$o2" "$o3" "$o4"; do
        [ "$o" -ge 0 ] 2>/dev/null && [ "$o" -le 255 ] 2>/dev/null || return 1
    done
    return 0
}

validate_fqdn() {
    # Character classes like [A-Z] follow the current locale collation,
    # so under en_US* they match most lowercase letters and reject valid
    # hyphenated FQDNs (e.g. panel.super-space.com.de). Force C.
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
                -* | *- | *[!a-z0-9-]* ) exit 1 ;;
            esac
        done
        exit 0
    )
}

validate_ident() {
    case "$1" in
        "" | [0-9]* | *[!A-Za-z0-9_]* ) return 1 ;;
    esac
    return 0
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --help | -h)
            usage
            exit 0
            ;;
        --ip)
            [ "$#" -ge 2 ] || die_usage "--ip requires a host argument"
            SSH_HOST="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        --user)
            [ "$#" -ge 2 ] || die_usage "--user requires a user argument"
            SSH_USER="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        --key)
            [ "$#" -ge 2 ] || die_usage "--key requires a file argument"
            KEY_FILE="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        --password-env)
            [ "$#" -ge 2 ] || die_usage "--password-env requires an environment variable name"
            PASSWORD_ENV="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        --domain | --panel-domain)
            [ "$#" -ge 2 ] || die_usage "$1 requires a FQDN argument"
            DOMAIN="$2"
            DOMAIN_SET=1
            NONINTERACTIVE=1
            shift 2
            ;;
        --panel-port)
            [ "$#" -ge 2 ] || die_usage "--panel-port requires a port argument"
            PANEL_PORT="$2"
            PANEL_PORT_SET=1
            NONINTERACTIVE=1
            shift 2
            ;;
        --client-domain | --vpn-domain)
            [ "$#" -ge 2 ] || die_usage "$1 requires a FQDN argument"
            CLIENT_DOMAIN="$2"
            CLIENT_DOMAIN_SET=1
            BIND_CLIENTS=1
            NONINTERACTIVE=1
            shift 2
            ;;
        --awg-port)
            [ "$#" -ge 2 ] || die_usage "--awg-port requires a port argument"
            AWG_PORT="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        --root)
            [ "$#" -ge 2 ] || die_usage "--root requires a directory argument"
            ROOT_DIR="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        --source)
            [ "$#" -ge 2 ] || die_usage "--source requires a URL argument"
            SOURCE_URL="$2"
            NONINTERACTIVE=1
            shift 2
            ;;
        *)
            die_usage "unknown argument: $1"
            ;;
    esac
done

init_wizard_color() {
    USE_COLOR=0
    C_RESET=""
    C_BOLD=""
    C_CYAN=""
    C_GREEN=""
    C_RED=""
    # Interactive chrome only. Piped harnesses (test_m123) have no TTY.
    [ "$NONINTERACTIVE" = "0" ] || return 0
    [ -z "${NO_COLOR:-}" ] || return 0
    if [ -t 1 ] || [ -t 2 ]; then
        USE_COLOR=1
        C_RESET=$'\033[0m'
        C_BOLD=$'\033[1m'
        C_CYAN=$'\033[36m'
        C_GREEN=$'\033[32m'
        C_RED=$'\033[31m'
    fi
}

step_label() { # n total text — color the N/M prefix on a TTY
    printf '%s%s/%s%s %s' "$C_CYAN" "$1" "$2" "$C_RESET" "$3"
}

prompt() { # prompt VAR "question" [default]
    local __var="$1" __q="$2" __def="${3:-}" __ans
    if [ -n "$__def" ]; then
        printf '%s [%s]: ' "$__q" "$__def" >&2
    else
        printf '%s: ' "$__q" >&2
    fi
    IFS= read -r __ans || true
    if [ -z "$__ans" ]; then
        __ans="$__def"
    fi
    printf -v "$__var" '%s' "$__ans"
}

prompt_secret() { # prompt_secret VAR "question"
    local __var="$1" __q="$2" __ans
    printf '%s: ' "$__q" >&2
    IFS= read -r -s __ans || true
    printf '\n' >&2
    printf -v "$__var" '%s' "$__ans"
}

if [ "$NONINTERACTIVE" = "0" ]; then
    init_wizard_color
    printf '%sAmneziaWG VPN Server%s — установка (Enter = значение в скобках)\n' "$C_BOLD" "$C_RESET" >&2
    prompt SSH_HOST "$(step_label 1 6 "IP сервера")"
    [ -n "$SSH_HOST" ] || die_op "IP сервера обязателен"
    prompt SSH_USER "$(step_label 2 6 "Пользователь SSH")" "$DEFAULT_USER"
    printf '%s3/6%s Вход на сервер [1=пароль, 2=ключ]: ' "$C_CYAN" "$C_RESET" >&2
    IFS= read -r AUTH_CHOICE || true
    AUTH_NORM="$(printf '%s' "$AUTH_CHOICE" | tr '[:upper:]' '[:lower:]')"
    case "$AUTH_CHOICE" in
        "" | пароль | 1)
            AUTH_MODE=password
            ;;
        ключ | 2)
            AUTH_MODE=key
            ;;
        *)
            case "$AUTH_NORM" in
                password | pass)
                    AUTH_MODE=password
                    ;;
                key)
                    AUTH_MODE=key
                    ;;
                *)
                    die_op "укажите 1 или 2 (1=пароль, 2=ключ)"
                    ;;
            esac
            ;;
    esac
    if [ "$AUTH_MODE" = "password" ]; then
        prompt_secret SSH_PASSWORD "Пароль SSH (ввод скрыт)"
        [ -n "$SSH_PASSWORD" ] || die_op "пароль SSH пустой"
    fi
    prompt DOMAIN "$(step_label 4 6 "Домен панели (пусто = панель на IP; например panel.example.com)")"
    if [ -n "$DOMAIN" ]; then
        DOMAIN_SET=1
        printf '%s5/6%s Порт панели [443]: ' "$C_CYAN" "$C_RESET" >&2
        IFS= read -r PANEL_PORT || true
        if [ -n "$PANEL_PORT" ]; then
            PANEL_PORT_SET=1
        fi
    else
        prompt PANEL_PORT "$(step_label 5 6 "Порт панели")" "$DEFAULT_PANEL_PORT"
        PANEL_PORT_SET=1
    fi
    prompt CLIENT_DOMAIN "$(step_label 6 6 "Домен VPN для клиентов (пусто = IP сервера; например example.com)")"
    if [ -n "$CLIENT_DOMAIN" ]; then
        CLIENT_DOMAIN_SET=1
        BIND_CLIENTS=1
    else
        CLIENT_DOMAIN_SET=0
        BIND_CLIENTS=0
    fi
fi

[ -n "$SSH_HOST" ] || die_usage "server host is required (--ip, or answer the prompt)"
validate_safe "$SSH_HOST" || die_usage "invalid --ip / host (unsupported characters)"
SSH_USER="${SSH_USER:-$DEFAULT_USER}"
validate_safe "$SSH_USER" || die_usage "invalid --user (unsupported characters)"
[ -n "$SSH_USER" ] || die_usage "empty --user"
ROOT_DIR="${ROOT_DIR:-$DEFAULT_ROOT}"
validate_safe "$ROOT_DIR" || die_usage "invalid --root (unsupported characters)"
[ -n "$ROOT_DIR" ] || die_usage "empty --root"
[ "$ROOT_DIR" != "/" ] || die_usage "--root must not be the filesystem root"
AWG_PORT="${AWG_PORT:-$DEFAULT_AWG_PORT}"
validate_port "$AWG_PORT" || die_usage "--awg-port must be an integer in 1..65535 (got: $AWG_PORT)"

if [ "$DOMAIN_SET" = "1" ] && [ -n "$DOMAIN" ]; then
    if ! validate_fqdn "$DOMAIN"; then
        if [ "$NONINTERACTIVE" = "0" ]; then
            die_op "некорректный домен панели (буквы, цифры, дефис; точки между частями; например panel.example.com; пусто = IP)"
        fi
        die_usage "--panel-domain/--domain must be a valid FQDN (got: $DOMAIN)"
    fi
    validate_safe "$DOMAIN" || die_usage "invalid --panel-domain/--domain (unsupported characters)"
fi
if [ "$CLIENT_DOMAIN_SET" = "1" ]; then
    [ -n "$CLIENT_DOMAIN" ] || die_usage "empty --vpn-domain/--client-domain"
    if ! validate_fqdn "$CLIENT_DOMAIN"; then
        if [ "$NONINTERACTIVE" = "0" ]; then
            die_op "некорректный домен VPN (буквы, цифры, дефис; точки между частями; например example.com; пусто = IP)"
        fi
        die_usage "--vpn-domain/--client-domain must be a valid FQDN (got: $CLIENT_DOMAIN)"
    fi
    validate_safe "$CLIENT_DOMAIN" || die_usage "invalid --vpn-domain/--client-domain (unsupported characters)"
    BIND_CLIENTS=1
fi
# T-156: flag mode binds clients only when --vpn-domain/--client-domain
# is set. --panel-domain/--domain never copies onto the VPN endpoint.
if [ "$NONINTERACTIVE" = "1" ]; then
    if [ "$CLIENT_DOMAIN_SET" = "1" ]; then
        BIND_CLIENTS=1
    else
        BIND_CLIENTS=0
    fi
fi
if [ "$PANEL_PORT_SET" = "1" ]; then
    validate_port "$PANEL_PORT" || die_usage "--panel-port must be an integer in 1..65535 (got: $PANEL_PORT)"
fi
if [ -n "$DOMAIN" ] && [ "$PANEL_PORT_SET" = "1" ] && [ "$PANEL_PORT" = "80" ]; then
    die_usage "--panel-port 80 conflicts with ACME HTTP-01 (TCP 80). Use 443 (default) or another port such as 8443"
fi
# One-shot install always exposes the panel: IP:port when no domain.
if [ "$DOMAIN_SET" = "0" ] || [ -z "$DOMAIN" ]; then
    if [ "$PANEL_PORT_SET" = "0" ]; then
        PANEL_PORT="$DEFAULT_PANEL_PORT"
        PANEL_PORT_SET=1
    fi
fi
if [ -n "$SOURCE_URL" ]; then
    validate_safe "$SOURCE_URL" || die_usage "invalid --source URL (unsupported characters)"
fi

# --- SSH authentication ------------------------------------------------

default_key() {
    if [ -f "${HOME}/.ssh/id_ed25519" ]; then
        printf '%s\n' "${HOME}/.ssh/id_ed25519"
        return 0
    fi
    if [ -f "${HOME}/.ssh/id_rsa" ]; then
        printf '%s\n' "${HOME}/.ssh/id_rsa"
        return 0
    fi
    return 1
}

if [ "$NONINTERACTIVE" = "0" ]; then
    if [ "$AUTH_MODE" = "key" ]; then
        KEY_FILE="$(default_key)" || die_op "SSH-ключ не найден (~/.ssh/id_ed25519, ~/.ssh/id_rsa)"
        AUTH_MODE=key
    elif [ "$AUTH_MODE" = "password" ]; then
        [ -n "$SSH_PASSWORD" ] || die_op "пароль SSH пустой"
    else
        die_op "не выбран способ входа на сервер"
    fi
elif [ -n "$KEY_FILE" ]; then
    [ -f "$KEY_FILE" ] || die_op "SSH key not found: $KEY_FILE"
    AUTH_MODE=key
elif [ -n "$PASSWORD_ENV" ]; then
    validate_ident "$PASSWORD_ENV" || die_usage "--password-env must be a valid environment variable name"
    SSH_PASSWORD="${!PASSWORD_ENV-}"
    [ -n "$SSH_PASSWORD" ] || die_op "--password-env $PASSWORD_ENV is unset or empty"
    AUTH_MODE=password
elif KEY_FILE="$(default_key)"; then
    AUTH_MODE=key
else
    die_op "no SSH key found (~/.ssh/id_ed25519, ~/.ssh/id_rsa); pass --key or --password-env"
fi

if [ "$AUTH_MODE" = "password" ]; then
    if command -v sshpass >/dev/null 2>&1; then
        PASS_HELPER=sshpass
    elif command -v expect >/dev/null 2>&1; then
        PASS_HELPER=expect
    else
        if [ "$NONINTERACTIVE" = "0" ]; then
            die_op "для входа по паролю нужен sshpass или expect (brew install sshpass / apt install sshpass)"
        else
            die_op "password authentication requires sshpass or expect (install one of them, or use --key)"
        fi
    fi
fi

cleanup() {
    if [ -n "${BUNDLE:-}" ] && [ -f "$BUNDLE" ]; then
        rm -f "$BUNDLE"
    fi
    if [ -n "${REMOTE_TMP:-}" ] && [ -n "${AUTH_MODE:-}" ] && [ -n "${SSH_HOST:-}" ]; then
        # Best-effort; ignore failures so a previous error is preserved.
        remote_cmd "rm -rf '$REMOTE_TMP'" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

# ssh/scp wrappers. Password is only in the environment (sshpass -e) or
# in the expect script's env — never on argv. Commands that carry a
# secret on stdin are not logged.

ssh_base_opts() {
    SSH_OPTS=(
        -o "ConnectTimeout=${SSH_CONNECT_TIMEOUT}"
        -o "StrictHostKeyChecking=accept-new"
        -o "ServerAliveInterval=5"
        -o "ServerAliveCountMax=3"
    )
    if [ "$AUTH_MODE" = "key" ]; then
        SSH_OPTS+=(-i "$KEY_FILE" -o BatchMode=yes -o IdentitiesOnly=yes)
    else
        SSH_OPTS+=(-o BatchMode=no -o PreferredAuthentications=password -o PubkeyAuthentication=no)
    fi
}

run_sshpass() { # run_sshpass ssh|scp args...
    set +x
    SSHPASS="$SSH_PASSWORD" sshpass -e "$@"
}

run_expect_ssh() {
    # argv of expect does not include the password; expect reads SSHPASS.
    set +x
    SSHPASS="$SSH_PASSWORD" expect -c '
        set timeout '"$SSH_CONNECT_TIMEOUT"'
        log_user 1
        eval spawn -noecho $env(AMNEZIA_EXPECT_ARGV)
        expect {
            -re "(?i)password:" { send -- "$env(SSHPASS)\r"; exp_continue }
            eof {}
            timeout { exit 124 }
        }
        catch wait result
        exit [lindex $result 3]
    '
}

remote_cmd() { # remote_cmd "shell text" — stdout of the remote command
    local rc
    ssh_base_opts
    if [ "$AUTH_MODE" = "key" ]; then
        ssh "${SSH_OPTS[@]}" "${SSH_USER}@${SSH_HOST}" "$1"
        return $?
    fi
    if [ "$PASS_HELPER" = "sshpass" ]; then
        run_sshpass ssh "${SSH_OPTS[@]}" "${SSH_USER}@${SSH_HOST}" "$1"
        return $?
    fi
    AMNEZIA_EXPECT_ARGV="ssh ${SSH_OPTS[*]} ${SSH_USER}@${SSH_HOST} $1"
    export AMNEZIA_EXPECT_ARGV
    run_expect_ssh
}

remote_cmd_in() { # stdin is forwarded (used for --password-stdin). Do not log.
    set +x
    ssh_base_opts
    if [ "$AUTH_MODE" = "key" ]; then
        ssh "${SSH_OPTS[@]}" "${SSH_USER}@${SSH_HOST}" "$1"
        return $?
    fi
    if [ "$PASS_HELPER" = "sshpass" ]; then
        run_sshpass ssh "${SSH_OPTS[@]}" "${SSH_USER}@${SSH_HOST}" "$1"
        return $?
    fi
    AMNEZIA_EXPECT_ARGV="ssh ${SSH_OPTS[*]} ${SSH_USER}@${SSH_HOST} $1"
    export AMNEZIA_EXPECT_ARGV
    run_expect_ssh
}

remote_copy() { # remote_copy local remote-path
    ssh_base_opts
    if [ "$AUTH_MODE" = "key" ]; then
        scp "${SSH_OPTS[@]}" "$1" "${SSH_USER}@${SSH_HOST}:$2"
        return $?
    fi
    if [ "$PASS_HELPER" = "sshpass" ]; then
        run_sshpass scp "${SSH_OPTS[@]}" "$1" "${SSH_USER}@${SSH_HOST}:$2"
        return $?
    fi
    AMNEZIA_EXPECT_ARGV="scp ${SSH_OPTS[*]} $1 ${SSH_USER}@${SSH_HOST}:$2"
    export AMNEZIA_EXPECT_ARGV
    run_expect_ssh
}

log "connecting to ${SSH_USER}@${SSH_HOST} (${AUTH_MODE} auth)"
REMOTE_TMP="$(remote_cmd 'mktemp -d /tmp/amnezia-bootstrap.XXXXXX')" || \
    die_op "SSH failed: cannot connect to ${SSH_USER}@${SSH_HOST} (timeout, refused, or authentication failure). Check --ip/--user/--key or --password-env"
REMOTE_TMP="$(printf '%s' "$REMOTE_TMP" | tr -d '[:space:]')"
[ -n "$REMOTE_TMP" ] || die_op "SSH failed: the server did not return a temporary directory"
validate_safe "$REMOTE_TMP" || die_op "SSH failed: unexpected mktemp path from the server"

# --- pack or download the project --------------------------------------

BUNDLE="$(mktemp "${TMPDIR:-/tmp}/amnezia-bootstrap-src.XXXXXX")"

if [ -n "$SOURCE_URL" ]; then
    log "downloading source from --source"
    if ! curl -fsSL --max-time 60 -o "$BUNDLE" "$SOURCE_URL"; then
        die_op "failed to download --source $SOURCE_URL"
    fi
else
    packed=0
    if command -v git >/dev/null 2>&1 \
        && git -C "$SCRIPT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        log "packing the local repository (git archive)"
        if git -C "$SCRIPT_DIR" archive --format=tar HEAD > "$BUNDLE"; then
            packed=1
        fi
    fi
    if [ "$packed" -eq 0 ]; then
        log "packing the local repository (excluding .git/.beads/.worktrees and other bulk)"
        if tar -C "$SCRIPT_DIR" \
            --exclude=.git \
            --exclude=.beads \
            --exclude=.worktrees \
            --exclude=node_modules \
            --exclude=tmp \
            --exclude=.dolt \
            --exclude=.agents \
            --exclude=.cursor \
            --exclude=.claude \
            --exclude=.codex \
            --exclude=AUDITS \
            --exclude=docs \
            -cf "$BUNDLE" . 2>/dev/null; then
            packed=1
        fi
    fi
    [ "$packed" -eq 1 ] || die_op "failed to pack the local repository"
fi

log "copying the project to the server"
if ! remote_copy "$BUNDLE" "${REMOTE_TMP}/src.tar"; then
    die_op "SCP failed: cannot copy the project to ${SSH_USER}@${SSH_HOST}"
fi

# --- install.sh on the server ------------------------------------------

INSTALL_FLAGS="--root '$ROOT_DIR' --awg-port '$AWG_PORT'"
if [ "$DOMAIN_SET" = "1" ] && [ -n "$DOMAIN" ]; then
    INSTALL_FLAGS="$INSTALL_FLAGS --domain '$DOMAIN'"
fi
if [ "$BIND_CLIENTS" = "1" ] && [ -n "$CLIENT_DOMAIN" ]; then
    INSTALL_FLAGS="$INSTALL_FLAGS --client-domain '$CLIENT_DOMAIN'"
fi
if [ "$PANEL_PORT_SET" = "1" ] && [ -n "$PANEL_PORT" ]; then
    INSTALL_FLAGS="$INSTALL_FLAGS --panel-port '$PANEL_PORT'"
fi

REMOTE_INSTALL=$(cat <<EOF
set -e
cd '$REMOTE_TMP'
tar -xf src.tar
if [ ! -f install.sh ]; then
    found=""
    for f in */install.sh; do
        if [ -f "\$f" ]; then
            found="\$f"
            break
        fi
    done
    [ -n "\$found" ] || { echo "bootstrap: ERROR: install.sh missing from the transferred archive" >&2; exit 1; }
    cd "\$(dirname "\$found")"
fi
bash ./install.sh $INSTALL_FLAGS
EOF
)

log "running install.sh on the server"
INSTALL_OUT="$(mktemp "${TMPDIR:-/tmp}/amnezia-bootstrap-install.XXXXXX")"
remote_cmd "$REMOTE_INSTALL" > "$INSTALL_OUT" 2>&1
INSTALL_RC=$?
if [ "$INSTALL_RC" -ne 0 ]; then
    if grep -q "Create an A record" "$INSTALL_OUT" 2>/dev/null; then
        printf 'bootstrap: ERROR: install.sh failed: DNS record does not match this server.\n' >&2
        grep "Create an A record" "$INSTALL_OUT" >&2 || true
        rm -f "$INSTALL_OUT"
        die_op "fix the A/AAAA record and rerun bootstrap.sh"
    fi
    printf 'bootstrap: ERROR: install.sh failed on the server (exit %s).\n' "$INSTALL_RC" >&2
    tail -n 20 "$INSTALL_OUT" >&2 || true
    rm -f "$INSTALL_OUT"
    die_op "install.sh failed on the server"
fi

if grep -q "TLS fingerprint (SHA256):" "$INSTALL_OUT"; then
    FINGERPRINT="$(sed -n 's/.*TLS fingerprint (SHA256):[[:space:]]*//p' "$INSTALL_OUT" | head -1 | tr -d '[:space:]')"
fi
rm -f "$INSTALL_OUT"

# --- public IP / endpoint ----------------------------------------------

if validate_ipv4 "$SSH_HOST"; then
    PUBLIC_IP="$SSH_HOST"
else
    PUBLIC_IP="$(remote_cmd 'curl -fsS --max-time 10 https://api.ipify.org' | tr -d '[:space:]')" || true
fi
[ -n "$PUBLIC_IP" ] || die_op "cannot determine the public IP of the server (needed for the client endpoint)"
validate_safe "$PUBLIC_IP" || die_op "unexpected public IP from the server"

if [ "$BIND_CLIENTS" = "1" ] && [ -n "$CLIENT_DOMAIN" ]; then
    ENDPOINT="${CLIENT_DOMAIN}:${AWG_PORT}"
else
    ENDPOINT="${PUBLIC_IP}:${AWG_PORT}"
fi

if [ "$DOMAIN_SET" = "1" ] && [ -n "$DOMAIN" ]; then
    PANEL_URL="https://${DOMAIN}"
    if [ "$PANEL_PORT_SET" = "1" ] && [ -n "$PANEL_PORT" ] && [ "$PANEL_PORT" != "443" ]; then
        PANEL_URL="https://${DOMAIN}:${PANEL_PORT}"
    fi
else
    PANEL_URL="https://${PUBLIC_IP}:${PANEL_PORT}"
fi

# --- application bootstrap ---------------------------------------------

log "initializing the server row"
# install.sh measured the uplink path MTU and stored the resulting tunnel
# MTU in the deployment .env; read it there (on the server, hence the
# escaping) so the operator never has to work the number out. Missing value
# = the panel's own safe default.
INIT_CMD="cd '$ROOT_DIR' && MTU_ARG=\"\"; if [ -f .env ]; then TUNNEL_MTU=\"\$(sed -n 's/^TUNNEL_MTU=//p' .env | tail -1)\"; if [ -n \"\$TUNNEL_MTU\" ]; then MTU_ARG=\"--mtu \$TUNNEL_MTU\"; fi; fi; docker compose --env-file versions.lock run --rm panel-init /app/panel server init 10.8.0.1/24 '$AWG_PORT' --endpoint '$ENDPOINT' --dns 1.1.1.1,8.8.8.8 \$MTU_ARG"
if ! remote_cmd "$INIT_CMD" >/dev/null; then
    die_op "application bootstrap failed: panel server init did not succeed"
fi

log "starting the compose stack"
if ! remote_cmd "cd '$ROOT_DIR' && docker compose --env-file versions.lock up -d" >/dev/null; then
    die_op "application bootstrap failed: docker compose up -d did not succeed"
fi

set +x
ADMIN_PASSWORD="$(openssl rand -base64 18 2>/dev/null | tr -d '\n')"
[ -n "$ADMIN_PASSWORD" ] || die_op "cannot generate a temporary admin password (openssl rand)"

log "creating the admin user"
set +x
if ! printf '%s\n' "$ADMIN_PASSWORD" | remote_cmd_in "cd '$ROOT_DIR' && docker compose --env-file versions.lock run --rm -T panel-init /app/panel auth add-user admin --password-stdin" >/dev/null; then
    die_op "application bootstrap failed: panel auth add-user admin did not succeed"
fi

# --- panel health check ------------------------------------------------

log "checking that the panel responds"
CURL_ARGS=(-sS -o /dev/null -w '%{http_code}' --max-time "$CURL_TIMEOUT")
if [ "$DOMAIN_SET" = "0" ] || [ -z "$DOMAIN" ]; then
    CURL_ARGS+=(-k)
fi
PANEL_CODE=""
i=0
while [ "$i" -lt 8 ]; do
    PANEL_CODE="$(curl "${CURL_ARGS[@]}" "$PANEL_URL/" 2>/dev/null || true)"
    case "$PANEL_CODE" in
        200 | 301 | 302 | 303 | 401 | 403) break ;;
    esac
    i=$((i + 1))
    sleep 1
done
case "$PANEL_CODE" in
    200 | 301 | 302 | 303 | 401 | 403) ;;
    *)
        die_op "the panel did not respond at $PANEL_URL (HTTP ${PANEL_CODE:-none}). install.sh finished, but the health check failed"
        ;;
esac

# --- one summary on stdout ---------------------------------------------

{
    printf '\n'
    if [ "$USE_COLOR" = "1" ]; then
        printf '%sOK%s  bootstrap: DONE — run this summary in a trusted environment; the password is not saved.\n' "$C_GREEN" "$C_RESET"
    else
        printf 'bootstrap: DONE — run this summary in a trusted environment; the password is not saved.\n'
    fi
    printf 'Panel:     %s\n' "$PANEL_URL"
    printf 'Login:     admin\n'
    printf 'Password:  %s\n' "$ADMIN_PASSWORD"
    printf 'Endpoint:  %s\n' "$ENDPOINT"
    if [ -n "$FINGERPRINT" ]; then
        printf 'TLS SHA256 fingerprint: %s\n' "$FINGERPRINT"
    fi
    printf '\n'
    printf 'Change the password at /account.\n'
    printf 'Upload a backup on Backups to restore clients (does not change the panel user).\n'
} 

exit 0

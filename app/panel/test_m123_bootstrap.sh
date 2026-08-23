#!/bin/bash
# T-123 bootstrap.sh test harness.
#
# Runs bootstrap.sh against scripted fakes: nothing on the real host is
# touched (no SSH, no scp, no network). Fake ssh/scp/tar/sshpass/curl/
# openssl are prepended to PATH, matching app/panel/test_m91_install.sh.
#
#   bash app/panel/test_m123_bootstrap.sh
#
# Exit code: 0 when every test passes; 1 otherwise.

set -u

M123_ERRORS=0
M123_HOME="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BOOTSTRAP_SH="$M123_HOME/bootstrap.sh"

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; M123_ERRORS=$((M123_ERRORS + 1)); }

FAKE_DIR="$(mktemp -d /tmp/m123-fakes.XXXXXX)"
FAKE_STATE="$FAKE_DIR/state.env"
FAKE_CALLS="$FAKE_DIR/calls.log"
FAKE_HOME="$FAKE_DIR/home"
TMP_TEST="$(mktemp -d /tmp/m123-run.XXXXXX)"
export FAKE_DIR FAKE_STATE FAKE_CALLS FAKE_HOME
trap 'rm -rf "$FAKE_DIR" "$TMP_TEST"' EXIT

setstate() {
    local key="$1" value="$2" file="$3"
    sed "s|^${key}=.*|${key}=${value}|" "$file" > "$file.new" && mv "$file.new" "$file"
}

fakes_reset() {
    : > "$FAKE_CALLS"
    mkdir -p "$FAKE_HOME/.ssh"
    printf 'FAKE-ED25519-KEY\n' > "$FAKE_HOME/.ssh/id_ed25519"
    printf 'FAKE-RSA-KEY\n' > "$FAKE_HOME/.ssh/id_rsa"
    rm -f "$FAKE_DIR/install.err" "$FAKE_DIR/last-bundle"
    if [ -f "$FAKE_DIR/tar.orig" ]; then
        cp -f "$FAKE_DIR/tar.orig" "$FAKE_DIR/tar"
        chmod +x "$FAKE_DIR/tar"
    fi
    if [ -f "$FAKE_DIR/git.orig" ]; then
        cp -f "$FAKE_DIR/git.orig" "$FAKE_DIR/git"
        chmod +x "$FAKE_DIR/git"
    fi
    cat > "$FAKE_STATE" <<EOF
SSH_RC=0
INSTALL_RC=0
INIT_RC=0
INIT_STDERR=
CURL_SOURCE_RC=0
UP_RC=0
ADDUSER_RC=0
CURL_PANEL_RC=0
CURL_PANEL_CODE=200
PUBLIC_IP=2.26.93.192
SOURCE_BODY=fake-tarball
EOF
}

cat > "$FAKE_DIR/ssh" <<'FAKE_EOF'
#!/bin/bash
LOG="${FAKE_CALLS:?}"
echo "ssh $*" >> "$LOG"
# Never record stdin: the admin password is piped here.
if [ ! -t 0 ]; then
    cat >/dev/null
fi
. "${FAKE_STATE:?}"
[ "${SSH_RC:-0}" = "0" ] || exit "${SSH_RC}"

cmd="$*"
case "$cmd" in
    *mktemp*)
        printf '%s\n' "/tmp/amnezia-bootstrap.fake"
        exit 0
        ;;
    *install.sh*)
        if [ "${INSTALL_RC:-0}" != "0" ]; then
            if [ -f "${FAKE_DIR}/install.err" ]; then
                cat "${FAKE_DIR}/install.err" >&2
            else
                printf '%s\n' "install: ERROR: simulated install failure" >&2
            fi
            exit "${INSTALL_RC}"
        fi
        # Every install replays the same milestone log; the panel-port
        # variant only adds its TLS lines on top.
        printf '%s\n' \
            "install: OS check: ubuntu 24.04 (noble) supported" \
            "install: installing Docker Engine + Compose plugin from the official Docker Inc repository" \
            "install: persisting managed sysctl drop-in /etc/sysctl.d/99-amnezia-vpn.conf" \
            "install: AmneziaWG client stack already present (kernel module + awg tools)" \
            "install: deployment files installed (compose.yaml, versions.lock, docker-prune.sh)" \
            "install: tunnel MTU 1416 (a full packet costs 1476 bytes on a 1476-byte path)" \
            "install: WARNING: sysctl -w net.core.rmem_max=16777216 failed; continuing" \
            "install: building the stack images (pinned versions from versions.lock)" \
            "install: starting the stack" \
            "install: self-check: Docker daemon reachable (docker info)"
        if printf '%s' "$cmd" | grep -q -- "--panel-port"; then
            printf '%s\n' \
                "install: DONE — the AmneziaWG VPN Server stack is deployed." \
                "install: Panel: https://2.26.93.192:8443 (self-signed TLS certificate)" \
                "install:   TLS fingerprint (SHA256): AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67"
        else
            printf '%s\n' "install: DONE — the AmneziaWG VPN Server stack is deployed."
        fi
        exit 0
        ;;
    *'server init'*)
        [ -n "${INIT_STDERR:-}" ] && printf '%s\n' "${INIT_STDERR}" >&2
        exit "${INIT_RC:-0}"
        ;;
    *'up -d'*)
        exit "${UP_RC:-0}"
        ;;
    *'add-user'*)
        exit "${ADDUSER_RC:-0}"
        ;;
    *api.ipify.org*)
        printf '%s\n' "${PUBLIC_IP}"
        exit 0
        ;;
    *'rm -rf'*)
        exit 0
        ;;
esac
exit 0
FAKE_EOF

cat > "$FAKE_DIR/scp" <<'FAKE_EOF'
#!/bin/bash
echo "scp $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
[ "${SSH_RC:-0}" = "0" ] || exit "${SSH_RC}"
src=""
for a in "$@"; do
    case "$a" in
        *:*) continue ;;
    esac
    if [ -f "$a" ]; then
        src="$a"
    fi
done
if [ -n "$src" ]; then
    cp -f "$src" "${FAKE_DIR}/last-bundle"
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/sshpass" <<'FAKE_EOF'
#!/bin/bash
echo "sshpass $*" >> "${FAKE_CALLS:?}"
# Password lives in SSHPASS — never write it to the call log.
if [ "${1:-}" = "-e" ]; then
    shift
    exec "$@"
fi
if [ "${1:-}" = "-p" ]; then
    echo "sshpass-argv-password" >> "${FAKE_CALLS:?}"
    shift 2
    exec "$@"
fi
exit 1
FAKE_EOF

cat > "$FAKE_DIR/tar" <<'FAKE_EOF'
#!/bin/bash
echo "tar $*" >> "${FAKE_CALLS:?}"
out=""
prev=""
for a in "$@"; do
    if [ "$prev" = "-cf" ]; then
        out="$a"
    fi
    prev="$a"
done
if [ -n "$out" ] && [ "$out" != "-" ]; then
    printf 'FAKE-TAR\n' > "$out"
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/curl" <<'FAKE_EOF'
#!/bin/bash
echo "curl $*" >> "${FAKE_CALLS:?}"
. "${FAKE_STATE:?}"
oarg=""
want_code=0
prev=""
for a in "$@"; do
    [ "$prev" = "-o" ] && oarg="$a"
    [ "$a" = "-w" ] && want_code=1
    prev="$a"
done
if printf '%s' "$*" | grep -q "api.ipify.org"; then
    printf '%s\n' "${PUBLIC_IP}"
    exit 0
fi
if [ -n "$oarg" ] && [ "$oarg" != "/dev/null" ]; then
    # CURL_SOURCE_RC lets a test make the project download fail.
    if printf '%s' "$*" | grep -q "codeload\|github"; then
        [ "${CURL_SOURCE_RC:-0}" = "0" ] || exit "${CURL_SOURCE_RC}"
    fi
    mkdir -p "$(dirname "$oarg")"
    printf '%s\n' "${SOURCE_BODY}" > "$oarg"
    exit 0
fi
if [ "$want_code" = "1" ]; then
    [ "${CURL_PANEL_RC:-0}" = "0" ] || exit "${CURL_PANEL_RC}"
    printf '%s' "${CURL_PANEL_CODE:-200}"
    exit 0
fi
exit "${CURL_PANEL_RC:-0}"
FAKE_EOF

cat > "$FAKE_DIR/openssl" <<'FAKE_EOF'
#!/bin/bash
echo "openssl $*" >> "${FAKE_CALLS:?}"
if [ "${1:-}" = "rand" ]; then
    printf '%s' "TmpP4ssw0rd+/BASE64"
    exit 0
fi
exit 0
FAKE_EOF

cat > "$FAKE_DIR/git" <<'FAKE_EOF'
#!/bin/bash
echo "git $*" >> "${FAKE_CALLS:?}"
dir="."
if [ "$1" = "-C" ]; then
    dir="$2"
    shift 2
fi
case "${1:-}" in
    rev-parse)
        # Answer like the real thing: a directory without .git is not a work
        # tree. A fake that always says yes hides the standalone path, where
        # bootstrap.sh has no checkout beside it.
        [ -d "$dir/.git" ] || exit 128
        printf 'true\n'
        exit 0
        ;;
    archive)
        [ -d "$dir/.git" ] || exit 128
        printf 'FAKE-GIT-ARCHIVE\n'
        exit 0
        ;;
esac
exit 0
FAKE_EOF

chmod +x "$FAKE_DIR/ssh" "$FAKE_DIR/scp" "$FAKE_DIR/sshpass" \
    "$FAKE_DIR/tar" "$FAKE_DIR/curl" "$FAKE_DIR/openssl" "$FAKE_DIR/git"
cp -f "$FAKE_DIR/tar" "$FAKE_DIR/tar.orig"
cp -f "$FAKE_DIR/git" "$FAKE_DIR/git.orig"

run_bootstrap() {
    HOME="$FAKE_HOME" \
    PATH="$FAKE_DIR:$PATH" \
    bash "$BOOTSTRAP_SH" "$@" > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    rc=$?
    echo $rc
}

stdout() { cat "$TMP_TEST/out"; }
stderr() { cat "$TMP_TEST/err"; }

assert_no_ansi() {
    if grep -q $'\033' "$TMP_TEST/out" "$TMP_TEST/err" 2>/dev/null; then
        fail "$1: ANSI escape found on piped stdout/stderr"
        return 1
    fi
    pass "$1: no ANSI when piped"
}

# --- tests ---------------------------------------------------------------

test_bash_syntax() {
    bash -n "$BOOTSTRAP_SH" && pass "bash -n bootstrap.sh" || fail "bash -n bootstrap.sh"
}

test_help() {
    fakes_reset
    rc="$(run_bootstrap --help)"
    [ "$rc" = "0" ] || fail "help: exit $rc"
    grep -q -- "--ip HOST" "$TMP_TEST/out" && pass "help: usage printed" \
        || fail "help: usage missing"
    grep -q "Examples:" "$TMP_TEST/out" && pass "help: Examples section" \
        || fail "help: Examples section missing"
    grep -q -- "--panel-domain" "$TMP_TEST/out" && pass "help: --panel-domain" \
        || fail "help: --panel-domain missing"
    grep -q -- "--vpn-domain" "$TMP_TEST/out" && pass "help: --vpn-domain" \
        || fail "help: --vpn-domain missing"
    grep -q "Russian wizard" "$TMP_TEST/out" && pass "help: no-args Russian wizard" \
        || fail "help: no-args Russian wizard line missing"
    assert_no_ansi "help"
}

test_unknown_argument() {
    fakes_reset
    rc="$(run_bootstrap --bogus)"
    [ "$rc" = "2" ] || fail "unknown argument: exit $rc, want 2"
    pass "unknown arguments rejected (exit 2)"
}

test_domain_and_panel_port_combined() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
        --panel-domain panel.example.com --panel-port 8443 --vpn-domain vpn.example.com)"
    [ "$rc" = "0" ] || { fail "domain+panel-port: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.example.com'" "$FAKE_CALLS" && pass "domain+panel-port: panel domain passed" \
        || fail "domain+panel-port: --domain not passed to install.sh"
    grep -q -- "--panel-port '8443'" "$FAKE_CALLS" && pass "domain+panel-port: --panel-port passed" \
        || fail "domain+panel-port: --panel-port not passed"
    grep -q -- "--client-domain 'vpn.example.com'" "$FAKE_CALLS" && pass "domain+panel-port: vpn domain passed" \
        || fail "domain+panel-port: --client-domain not passed"
    grep -q "https://panel.example.com:8443" "$TMP_TEST/out" \
        && pass "domain+panel-port: panel URL includes the port" \
        || fail "domain+panel-port: panel URL missing host:port"
    grep -q "Endpoint:  vpn.example.com:443" "$TMP_TEST/out" \
        && pass "domain+panel-port: endpoint is --vpn-domain" \
        || fail "domain+panel-port: endpoint missing/wrong"
}

test_key_flags_domain_client_domain() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --user root --key "$FAKE_HOME/.ssh/id_ed25519" \
        --domain panel.example.com --client-domain vpn.example.com --awg-port 51820)"
    [ "$rc" = "0" ] || { fail "key+domain: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q "BatchMode=yes" "$FAKE_CALLS" && pass "key+domain: ssh BatchMode=yes" \
        || fail "key+domain: BatchMode=yes missing"
    grep -q "install.sh" "$FAKE_CALLS" && pass "key+domain: install.sh invoked over SSH" \
        || fail "key+domain: install.sh not invoked"
    grep -q -- "--domain 'panel.example.com'" "$FAKE_CALLS" && pass "key+domain: --domain passed" \
        || fail "key+domain: --domain not passed to install.sh"
    grep -q -- "--client-domain 'vpn.example.com'" "$FAKE_CALLS" && pass "key+domain: --client-domain passed" \
        || fail "key+domain: --client-domain not passed"
    grep -q "server init" "$FAKE_CALLS" && grep -q -- "--endpoint 'vpn.example.com:51820'" "$FAKE_CALLS" \
        && pass "key+domain: server init endpoint is the client domain" \
        || fail "key+domain: server init endpoint missing/wrong"
    # The tunnel MTU install.sh measured must reach `server init`; nobody
    # running the wizard is expected to know the number, so it travels
    # through the deployment .env rather than being asked for.
    grep -q 'TUNNEL_MTU=' "$FAKE_CALLS" && grep -q -- '--mtu' "$FAKE_CALLS" \
        && pass "server init picks the measured tunnel MTU out of .env" \
        || fail "server init must pass the measured tunnel MTU"
    grep -q "add-user admin --password-stdin" "$FAKE_CALLS" && pass "key+domain: admin via password-stdin" \
        || fail "key+domain: add-user missing"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "key+domain: panel URL in summary" \
        || fail "key+domain: panel URL missing from summary"
    grep -q "Login:     admin" "$TMP_TEST/out" && pass "key+domain: login in summary" \
        || fail "key+domain: login missing"
    grep -q "Password:  TmpP4ssw0rd+/BASE64" "$TMP_TEST/out" && pass "key+domain: temp password in summary" \
        || fail "key+domain: password missing from summary"
    # The panel no longer changes passwords; the summary hands over the
    # server command instead of pointing at a form that does not exist.
    grep -q "auth set-password" "$TMP_TEST/out" && pass "key+domain: change-password command in summary" \
        || fail "key+domain: set-password command missing from summary"
    grep -q "Backups" "$TMP_TEST/out" && pass "key+domain: restore-backup hint" \
        || fail "key+domain: Backups hint missing"
    grep -q "Endpoint:  vpn.example.com:51820" "$TMP_TEST/out" && pass "key+domain: endpoint in summary" \
        || fail "key+domain: endpoint missing from summary"
    grep -q "sshpass" "$FAKE_CALLS" && fail "key+domain: sshpass was invoked" \
        || pass "key+domain: sshpass not invoked"
}

test_password_env_panel_port() {
    fakes_reset
    rm -f "$FAKE_HOME/.ssh/id_ed25519" "$FAKE_HOME/.ssh/id_rsa"
    SSH_TEST_PW='s3cret-NOT-on-argv'
    export SSH_TEST_PW
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" SSH_TEST_PW="$SSH_TEST_PW" \
        bash "$BOOTSTRAP_SH" --ip 2.26.93.192 --password-env SSH_TEST_PW --panel-port 8443 \
            > "$TMP_TEST/out" 2> "$TMP_TEST/err"
        echo $?
    )"
    unset SSH_TEST_PW
    [ "$rc" = "0" ] || { fail "password-env+panel-port: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q "sshpass -e" "$FAKE_CALLS" && pass "password-env: sshpass -e (no password on argv)" \
        || fail "password-env: sshpass -e missing"
    grep -q "sshpass -p" "$FAKE_CALLS" && fail "password-env: sshpass -p used (password on argv)" \
        || pass "password-env: sshpass -p not used"
    if grep -F "s3cret-NOT-on-argv" "$FAKE_CALLS" "$TMP_TEST/err" >/dev/null 2>&1; then
        fail "password-env: SSH password leaked into argv/logs"
    else
        pass "password-env: SSH password absent from argv/logs"
    fi
    grep -q -- "--panel-port '8443'" "$FAKE_CALLS" && pass "password-env: --panel-port passed" \
        || fail "password-env: --panel-port not passed"
    grep -q "https://2.26.93.192:8443" "$TMP_TEST/out" && pass "password-env: IP:port URL in summary" \
        || fail "password-env: IP:port URL missing"
    grep -q "AB:CD:EF:01:23:45:67:89" "$TMP_TEST/out" && pass "password-env: cert fingerprint in summary" \
        || fail "password-env: fingerprint missing from summary"
    grep -q -- "--endpoint '2.26.93.192:443'" "$FAKE_CALLS" \
        && pass "password-env: server init uses the public IP endpoint" \
        || fail "password-env: IP endpoint missing from server init"
    grep -q "BatchMode=yes" "$FAKE_CALLS" && fail "password-env: BatchMode=yes used with password auth" \
        || pass "password-env: BatchMode=yes not used"
}

test_flags_domain_without_vpn_domain_uses_ip_endpoint() {
    # T-156: --domain / --panel-domain without --vpn-domain must NOT copy
    # the panel hostname onto client endpoints.
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
        --domain panel.example.com)"
    [ "$rc" = "0" ] || { fail "domain-no-vpn-bind: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--endpoint '2.26.93.192:443'" "$FAKE_CALLS" \
        && pass "domain-no-vpn-bind: server init endpoint is the public IP" \
        || fail "domain-no-vpn-bind: server init endpoint missing/wrong"
    grep -q -- "--endpoint 'panel.example.com:443'" "$FAKE_CALLS" \
        && fail "domain-no-vpn-bind: panel hostname leaked into server init" \
        || pass "domain-no-vpn-bind: panel hostname not used as endpoint"
    grep -q "Endpoint:  2.26.93.192:443" "$TMP_TEST/out" \
        && pass "domain-no-vpn-bind: summary endpoint is the public IP" \
        || fail "domain-no-vpn-bind: summary endpoint missing/wrong"
    grep -q -- "--client-domain" "$FAKE_CALLS" && fail "domain-no-vpn-bind: --client-domain passed without --vpn-domain" \
        || pass "domain-no-vpn-bind: --client-domain not passed"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "domain-no-vpn-bind: panel URL is the domain" \
        || fail "domain-no-vpn-bind: panel URL missing"
}

test_flags_panel_port_uses_ip_endpoint() {
    # IP endpoint only when there is no panel domain and no --client-domain.
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "0" ] || { fail "panel-port IP endpoint: exit $rc"; return 0; }
    grep -q -- "--endpoint '2.26.93.192:443'" "$FAKE_CALLS" \
        && pass "panel-port IP endpoint: server init uses PUBLIC_IP:awg-port" \
        || fail "panel-port IP endpoint: server init endpoint missing/wrong"
    grep -q "Endpoint:  2.26.93.192:443" "$TMP_TEST/out" \
        && pass "panel-port IP endpoint: summary uses the public IP" \
        || fail "panel-port IP endpoint: summary endpoint missing/wrong"
}

test_interactive_auth_prompt_shows_digits() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
2
panel.example.com

vpn.example.com
ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "auth prompt digits: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q 'Вход на сервер \[1=пароль, 2=ключ\]' "$TMP_TEST/err" \
        && pass "auth prompt digits: 1=пароль, 2=ключ before typing" \
        || fail "auth prompt digits: expected [1=пароль, 2=ключ] in the step 3 prompt"
}

test_interactive_domain_prompts_include_examples() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
2


ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "domain prompt examples: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q 'Домен панели (пусто = панель на IP; например panel.example.com)' "$TMP_TEST/err" \
        && pass "domain prompt examples: step 4 example hostname" \
        || fail "domain prompt examples: step 4 missing example hostname"
    grep -q 'Домен VPN для клиентов (пусто = IP сервера; например example.com)' "$TMP_TEST/err" \
        && pass "domain prompt examples: step 6 example hostname" \
        || fail "domain prompt examples: step 6 missing example hostname"
    if grep -q -- '--panel-domain\|--vpn-domain\|--client-domain' "$TMP_TEST/err"; then
        fail "domain prompt examples: wizard mentioned a flag name"
    else
        pass "domain prompt examples: wizard does not mention flag names"
    fi
}

test_hyphenated_fqdn_flag_en_us() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" LC_ALL=en_US.UTF-8 \
        bash "$BOOTSTRAP_SH" --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
            --panel-domain panel.super-space.com.de --vpn-domain super-space.com.de \
            > "$TMP_TEST/out" 2> "$TMP_TEST/err"
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "hyphenated FQDN flags en_US: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.super-space.com.de'" "$FAKE_CALLS" \
        && pass "hyphenated FQDN flags en_US: --panel-domain accepted" \
        || fail "hyphenated FQDN flags en_US: panel domain not passed to install.sh"
    grep -q -- "--client-domain 'super-space.com.de'" "$FAKE_CALLS" \
        && pass "hyphenated FQDN flags en_US: --vpn-domain accepted" \
        || fail "hyphenated FQDN flags en_US: vpn domain not passed to install.sh"
}

test_hyphenated_fqdn_interactive_en_us() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" LC_ALL=en_US.UTF-8 \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
2
panel.super-space.com.de

super-space.com.de
ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "hyphenated FQDN interactive en_US: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.super-space.com.de'" "$FAKE_CALLS" \
        && pass "hyphenated FQDN interactive en_US: panel domain accepted" \
        || fail "hyphenated FQDN interactive en_US: panel domain rejected or not passed"
    grep -q -- "--client-domain 'super-space.com.de'" "$FAKE_CALLS" \
        && pass "hyphenated FQDN interactive en_US: vpn domain accepted" \
        || fail "hyphenated FQDN interactive en_US: vpn domain rejected or not passed"
}

test_interactive_invalid_fqdn_russian_error() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
2
not a domain


ANSWERS
        echo $?
    )"
    [ "$rc" = "1" ] || fail "interactive invalid FQDN: exit $rc, want 1 (die_op)"
    if grep -q -- '--panel-domain' "$TMP_TEST/err"; then
        fail "interactive invalid FQDN: English flag-mode usage leaked (--panel-domain)"
    else
        pass "interactive invalid FQDN: no --panel-domain in the error"
    fi
    grep -qi 'например panel.example.com' "$TMP_TEST/err" \
        && pass "interactive invalid FQDN: Russian error with example hostname" \
        || fail "interactive invalid FQDN: expected a Russian error with an example hostname"
}

test_interactive_key_explicit_domains() {
    # FAKE_HOME still has keys; wizard must be told «ключ» (or 2) to use them.
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
ключ
panel.example.com

vpn.example.com
ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive domains: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.example.com'" "$FAKE_CALLS" && pass "interactive domains: --domain passed" \
        || fail "interactive domains: --domain missing"
    grep -q -- "--client-domain 'vpn.example.com'" "$FAKE_CALLS" \
        && pass "interactive domains: --client-domain is the typed VPN domain" \
        || fail "interactive domains: --client-domain missing/wrong"
    grep -q -- "--panel-port" "$FAKE_CALLS" && fail "interactive domains: empty panel port still passed --panel-port" \
        || pass "interactive domains: empty panel port omits --panel-port"
    grep -q "Endpoint:  vpn.example.com:443" "$TMP_TEST/out" \
        && pass "interactive domains: endpoint is the VPN domain" \
        || fail "interactive domains: endpoint missing/wrong"
    grep -q "sshpass" "$FAKE_CALLS" && fail "interactive domains: sshpass invoked despite ключ" \
        || pass "interactive domains: key auth (no sshpass)"
    grep -q "AmneziaWG VPN Server" "$TMP_TEST/err" && pass "interactive domains: wizard header" \
        || fail "interactive domains: wizard header missing"
    assert_no_ansi "interactive domains"
}

test_interactive_empty_vpn_domain_uses_ip_endpoint() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
2
panel.example.com


ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive empty-vpn: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.example.com'" "$FAKE_CALLS" && pass "interactive empty-vpn: --domain passed" \
        || fail "interactive empty-vpn: --domain missing"
    grep -q -- "--endpoint '2.26.93.192:443'" "$FAKE_CALLS" \
        && pass "interactive empty-vpn: server init uses PUBLIC_IP:awg-port" \
        || fail "interactive empty-vpn: IP endpoint missing from server init"
    grep -q "Endpoint:  2.26.93.192:443" "$TMP_TEST/out" \
        && pass "interactive empty-vpn: summary uses the public IP" \
        || fail "interactive empty-vpn: summary endpoint missing/wrong"
    grep -q -- "--client-domain" "$FAKE_CALLS" && fail "interactive empty-vpn: --client-domain passed" \
        || pass "interactive empty-vpn: --client-domain not passed"
    grep -q -- "--endpoint 'panel.example.com:443'" "$FAKE_CALLS" \
        && fail "interactive empty-vpn: panel hostname leaked into endpoint" \
        || pass "interactive empty-vpn: panel hostname not used as endpoint"
}

test_interactive_no_domain_panel_port() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
ключ

8443

ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive IP:port: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--panel-port '8443'" "$FAKE_CALLS" && pass "interactive IP:port: --panel-port passed" \
        || fail "interactive IP:port: --panel-port missing"
    grep -q "https://2.26.93.192:8443" "$TMP_TEST/out" && pass "interactive IP:port: URL in summary" \
        || fail "interactive IP:port: URL missing"
    grep -q -- "--client-domain" "$FAKE_CALLS" && fail "interactive IP:port: --client-domain passed" \
        || pass "interactive IP:port: empty VPN domain omits --client-domain"
}

test_interactive_password_with_keys_present() {
    # Keys exist in FAKE_HOME; empty/пароль/1 must use sshpass, not default_key.
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root

s3cret-interactive

8443

ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive password: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q "sshpass -e" "$FAKE_CALLS" && pass "interactive password: sshpass -e" \
        || fail "interactive password: sshpass -e missing"
    grep -q "id_ed25519" "$FAKE_CALLS" && fail "interactive password: used ~/.ssh/id_ed25519 despite password choice" \
        || pass "interactive password: did not auto-pick id_ed25519"
    grep -q "BatchMode=yes" "$FAKE_CALLS" && fail "interactive password: BatchMode=yes used with password auth" \
        || pass "interactive password: BatchMode=yes not used"
    if grep -F "s3cret-interactive" "$FAKE_CALLS" "$TMP_TEST/err" >/dev/null 2>&1; then
        fail "interactive password: SSH password leaked into argv/logs"
    else
        pass "interactive password: SSH password absent from argv/logs"
    fi
    grep -q -- "--panel-port '8443'" "$FAKE_CALLS" && pass "interactive password: default panel port 8443" \
        || fail "interactive password: --panel-port 8443 missing"
}

test_ssh_failure() {
    fakes_reset
    setstate SSH_RC 255 "$FAKE_STATE"
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "1" ] || fail "SSH fail: exit $rc, want 1"
    grep -qi "SSH failed" "$TMP_TEST/err" && pass "SSH fail: readable error" \
        || fail "SSH fail: message missing"
}

test_install_failure() {
    fakes_reset
    setstate INSTALL_RC 1 "$FAKE_STATE"
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "1" ] || fail "install fail: exit $rc, want 1"
    grep -q "install.sh failed" "$TMP_TEST/err" && pass "install fail: readable error" \
        || fail "install fail: message missing"
}

test_dns_mismatch() {
    fakes_reset
    setstate INSTALL_RC 1 "$FAKE_STATE"
    printf '%s\n' "install: ERROR: DNS pre-flight failed for panel.example.com: public record '1.2.3.4' does not match this server (2.26.93.192). Create an A record panel.example.com -> 2.26.93.192 and rerun install.sh" \
        > "$FAKE_DIR/install.err"
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --domain panel.example.com)"
    [ "$rc" = "1" ] || fail "DNS mismatch: exit $rc, want 1"
    grep -q "DNS record does not match" "$TMP_TEST/err" && pass "DNS mismatch: readable bootstrap error" \
        || fail "DNS mismatch: bootstrap message missing"
    grep -q "Create an A record" "$TMP_TEST/err" && pass "DNS mismatch: install.sh hint surfaced" \
        || fail "DNS mismatch: A-record hint missing"
}

test_admin_password_not_in_call_log() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "0" ] || fail "password-log: exit $rc"
    if grep -F "TmpP4ssw0rd+/BASE64" "$FAKE_CALLS" >/dev/null 2>&1; then
        fail "password-log: admin password appeared in fake-call argv logs"
    else
        pass "password-log: admin password not in fake-call logs"
    fi
    grep -q "Password:  TmpP4ssw0rd+/BASE64" "$TMP_TEST/out" \
        && pass "password-log: password still printed once in the summary" \
        || fail "password-log: summary missing the password"
}

test_tar_excludes_git_and_beads() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "0" ] || fail "tar excludes: exit $rc"
    if grep -q "archive --format=tar" "$FAKE_CALLS"; then
        pass "pack: git archive used when .git/git is available"
    elif grep -q -- "--exclude=.git" "$FAKE_CALLS" \
        && grep -q -- "--exclude=.beads" "$FAKE_CALLS" \
        && grep -q -- "--exclude=.worktrees" "$FAKE_CALLS" \
        && grep -q -- "--exclude=node_modules" "$FAKE_CALLS"; then
        pass "pack: tar excludes .git, .beads, .worktrees, node_modules"
    else
        fail "pack: expected git archive, or tar --exclude=.git/.beads/.worktrees/node_modules"
    fi
}

# Real tar/git (not PATH fakes): a bulky .worktrees tree must not land in src.tar.
run_bootstrap_real_pack() {
    local script="$1"
    shift
    HOME="$FAKE_HOME" \
    PATH="$FAKE_DIR:$PATH" \
    bash "$script" "$@" > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    echo $?
}

test_pack_skips_worktrees_contents() {
    local pack_root listed
    pack_root="$(mktemp -d /tmp/m123-pack.XXXXXX)"
    mkdir -p "$pack_root/.worktrees/amnezia-vpn-server-xx" \
        "$pack_root/node_modules/pkg" \
        "$pack_root/tmp" \
        "$pack_root/.dolt"
    printf 'WORKTREE-BULK\n' > "$pack_root/.worktrees/amnezia-vpn-server-xx/huge.bin"
    printf 'NODE-BULK\n' > "$pack_root/node_modules/pkg/index.js"
    printf 'TMP-BULK\n' > "$pack_root/tmp/scratch"
    printf 'DOLT-BULK\n' > "$pack_root/.dolt/data"
    cp -f "$BOOTSTRAP_SH" "$pack_root/bootstrap.sh"
    printf '%s\n' '#!/bin/bash' 'echo ok' > "$pack_root/install.sh"
    chmod +x "$pack_root/bootstrap.sh" "$pack_root/install.sh"

    git -C "$pack_root" init -q
    printf '%s\n' '.worktrees/' 'node_modules/' 'tmp/' '.dolt/' > "$pack_root/.gitignore"
    git -C "$pack_root" add bootstrap.sh install.sh .gitignore
    git -C "$pack_root" \
        -c user.email=m123@example.invalid \
        -c user.name=m123 \
        commit -qm 'fixture'

    fakes_reset
    rm -f "$FAKE_DIR/tar" "$FAKE_DIR/git"
    rc="$(run_bootstrap_real_pack "$pack_root/bootstrap.sh" \
        --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "0" ] || {
        fail "pack skip worktrees: exit $rc"
        cat "$TMP_TEST/err" >&2
        rm -rf "$pack_root"
        return 0
    }
    [ -f "$FAKE_DIR/last-bundle" ] || {
        fail "pack skip worktrees: scp did not capture the archive"
        rm -rf "$pack_root"
        return 0
    }
    listed="$(tar -tf "$FAKE_DIR/last-bundle" 2>/dev/null || true)"
    if printf '%s\n' "$listed" | grep -F '.worktrees' >/dev/null; then
        fail "pack skip worktrees: archive contains .worktrees"
    else
        pass "pack skip worktrees: .worktrees not in archive"
    fi
    if printf '%s\n' "$listed" | grep -F 'node_modules' >/dev/null; then
        fail "pack skip worktrees: archive contains node_modules"
    else
        pass "pack skip worktrees: node_modules not in archive"
    fi
    if printf '%s\n' "$listed" | grep -q 'install.sh'; then
        pass "pack skip worktrees: install.sh is in the archive"
    else
        fail "pack skip worktrees: install.sh missing from archive"
    fi
    rm -rf "$pack_root"
}

test_pack_tar_fallback_excludes_worktrees() {
    local pack_root listed
    pack_root="$(mktemp -d /tmp/m123-pack-tar.XXXXXX)"
    mkdir -p "$pack_root/.worktrees/amnezia-vpn-server-xx" "$pack_root/node_modules/pkg"
    printf 'WORKTREE-BULK\n' > "$pack_root/.worktrees/amnezia-vpn-server-xx/huge.bin"
    printf 'NODE-BULK\n' > "$pack_root/node_modules/pkg/index.js"
    cp -f "$BOOTSTRAP_SH" "$pack_root/bootstrap.sh"
    printf '%s\n' '#!/bin/bash' 'echo ok' > "$pack_root/install.sh"
    chmod +x "$pack_root/bootstrap.sh" "$pack_root/install.sh"

    fakes_reset
    rm -f "$FAKE_DIR/tar"
    # Fake git reports "not a work tree" so packing must use tar.
    cat > "$FAKE_DIR/git" <<'FAKE_EOF'
#!/bin/bash
echo "git $*" >> "${FAKE_CALLS:?}"
exit 1
FAKE_EOF
    chmod +x "$FAKE_DIR/git"

    rc="$(run_bootstrap_real_pack "$pack_root/bootstrap.sh" \
        --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "0" ] || {
        fail "tar fallback excludes: exit $rc"
        cat "$TMP_TEST/err" >&2
        rm -rf "$pack_root"
        return 0
    }
    [ -f "$FAKE_DIR/last-bundle" ] || {
        fail "tar fallback excludes: scp did not capture the archive"
        rm -rf "$pack_root"
        return 0
    }
    listed="$(tar -tf "$FAKE_DIR/last-bundle" 2>/dev/null || true)"
    if printf '%s\n' "$listed" | grep -F '.worktrees' >/dev/null; then
        fail "tar fallback excludes: archive contains .worktrees"
    else
        pass "tar fallback excludes: .worktrees not in archive"
    fi
    if printf '%s\n' "$listed" | grep -F 'node_modules' >/dev/null; then
        fail "tar fallback excludes: archive contains node_modules"
    else
        pass "tar fallback excludes: node_modules not in archive"
    fi
    rm -rf "$pack_root"
}

test_source_url() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
        --panel-port 8443 --source https://example.invalid/release.tar)"
    [ "$rc" = "0" ] || { fail "source URL: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q "curl " "$FAKE_CALLS" && grep -q "example.invalid/release.tar" "$FAKE_CALLS" \
        && pass "source URL: tarball downloaded" \
        || fail "source URL: curl of --source missing"
    grep -q "scp " "$FAKE_CALLS" && pass "source URL: scp of the tarball" \
        || fail "source URL: scp missing"
}

test_sshpass_missing() {
    fakes_reset
    rm -f "$FAKE_HOME/.ssh/id_ed25519" "$FAKE_HOME/.ssh/id_rsa" "$FAKE_DIR/sshpass"
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR" SSH_TEST_PW=x \
        /bin/bash "$BOOTSTRAP_SH" --ip 2.26.93.192 --password-env SSH_TEST_PW --panel-port 8443 \
            > "$TMP_TEST/out" 2> "$TMP_TEST/err"
        echo $?
    )"
    [ "$rc" = "1" ] || fail "sshpass missing: exit $rc, want 1"
    grep -q "sshpass or expect" "$TMP_TEST/err" && pass "sshpass missing: readable error" \
        || fail "sshpass missing: message missing"
}

test_invalid_panel_port() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 0)"
    [ "$rc" = "2" ] || fail "invalid panel port: exit $rc, want 2"
    pass "invalid --panel-port rejected (exit 2)"
}

test_default_panel_port_without_domain() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519")"
    [ "$rc" = "0" ] || fail "default panel-port: exit $rc"
    grep -q -- "--panel-port '8443'" "$FAKE_CALLS" && pass "default panel-port: 8443 in IP mode" \
        || fail "default panel-port: 8443 not passed"
    grep -q -- "--endpoint '2.26.93.192:443'" "$FAKE_CALLS" \
        && pass "default panel-port: server init uses the public IP endpoint" \
        || fail "default panel-port: IP endpoint missing"
    assert_no_ansi "default panel-port"
}

# Somebody who just pasted a curl command should never be left staring at a
# still terminal: the image build alone takes ten to thirty minutes on the
# 1-vCPU hosts this gets installed on.
test_install_progress_is_visible() {
    fakes_reset
    rm -f "$FAKE_HOME/.ssh/id_ed25519" "$FAKE_HOME/.ssh/id_rsa"
    SSH_TEST_PW='s3cret-NOT-on-argv'
    export SSH_TEST_PW
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" SSH_TEST_PW="$SSH_TEST_PW" \
        bash "$BOOTSTRAP_SH" --ip 2.26.93.192 --password-env SSH_TEST_PW \
            > "$TMP_TEST/out" 2> "$TMP_TEST/err"
        echo $?
    )"
    unset SSH_TEST_PW
    [ "$rc" = "0" ] || { fail "progress flow: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    for marker in \
        "[1/9] Проверяю систему" \
        "[2/9] Устанавливаю Docker" \
        "[7/9] Собираю образы" \
        "[8/9] Запускаю сервисы" \
        "[9/9] Проверяю установку"; do
        stderr | grep -qF "$marker" \
            && pass "progress shown: $marker" \
            || fail "progress step missing: $marker"
    done
    stderr | grep -qF "Полная загрузка процессора в это время нормальна" \
        && pass "the long build explains itself" \
        || fail "the build step must explain the wait and the CPU load"
    stderr | grep -q "WARNING: sysctl -w net.core.rmem_max" \
        && pass "installer warnings still reach the operator" \
        || fail "installer warnings must not be swallowed"
    assert_no_ansi "progress"
}

# A busy 1-vCPU host can stay silent on the SSH channel for longer than a
# few seconds while docker creates a container. Cutting the connection at
# 15 seconds turned a working install into a red error.
test_ssh_keepalive_tolerates_a_busy_host() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519")"
    [ "$rc" = "0" ] || fail "keepalive flow: exit $rc"
    interval=$(grep -o "ServerAliveInterval=[0-9]*" "$FAKE_CALLS" | head -1 | cut -d= -f2)
    count=$(grep -o "ServerAliveCountMax=[0-9]*" "$FAKE_CALLS" | head -1 | cut -d= -f2)
    budget=$(( ${interval:-0} * ${count:-0} ))
    if [ "$budget" -ge 240 ]; then
        pass "silence budget is ${budget}s (interval ${interval}, count ${count})"
    else
        fail "silence budget ${budget}s is too tight for a busy host"
    fi
}

# After such a cut the server row exists but the wizard exited with an
# error. Running it again is the first thing anyone tries, and it used to
# die on "server row (id=1) already exists".
test_rerun_survives_existing_server_row() {
    fakes_reset
    setstate INIT_RC 1 "$FAKE_STATE"
    setstate INIT_STDERR "panel server init: db: server row (id=1) already exists" "$FAKE_STATE"
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519")"
    [ "$rc" = "0" ] || fail "rerun with an existing server row: exit $rc, want 0"
    stderr | grep -qi "already initialized\|уже" \
        && pass "the rerun explains that the server was already initialized" \
        || fail "the rerun must say the server row already existed"
    grep -q "add-user" "$FAKE_CALLS" \
        && pass "the rerun goes on to create the admin" \
        || fail "the rerun must continue past init"
}

# A genuine init failure must still stop the wizard.
test_real_init_failure_still_aborts() {
    fakes_reset
    setstate INIT_RC 1 "$FAKE_STATE"
    setstate INIT_STDERR "panel server init: db: disk I/O error" "$FAKE_STATE"
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519")"
    [ "$rc" != "0" ] \
        && pass "a real init failure still aborts" \
        || fail "a real init failure must abort"
}

# The panel has no password form any more, so the summary must not send
# people to /account for it.
test_summary_points_at_the_server_for_passwords() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519")"
    [ "$rc" = "0" ] || fail "summary flow: exit $rc"
    stdout | grep -q "Change the password at /account" \
        && fail "the summary still points at the removed /account form" \
        || pass "the summary no longer points at /account"
    stdout | grep -q "auth set-password" \
        && pass "the summary shows the server command instead" \
        || fail "the summary must show the set-password command"
}

# The README tells people to download bootstrap.sh on its own and run it.
# With no checkout beside it the script used to tar whatever directory it
# happened to sit in — Downloads, or the home directory — and die on
# "failed to pack the local repository". It has to fetch the project itself.
test_standalone_downloads_the_project() {
    fakes_reset
    lonely="$TMP_TEST/lonely"
    rm -rf "$lonely"; mkdir -p "$lonely"
    cp "$BOOTSTRAP_SH" "$lonely/bootstrap.sh"
    # a bulky neighbour: packing this directory is exactly what must not happen
    mkdir -p "$lonely/unrelated" && printf 'x%.0s' $(seq 1 200) > "$lonely/unrelated/big.bin"
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$lonely/bootstrap.sh" --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
            > "$TMP_TEST/out" 2> "$TMP_TEST/err"
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "standalone run: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    stderr | grep -qi "downloading the project" \
        && pass "a lone bootstrap.sh downloads the project" \
        || fail "a lone bootstrap.sh must download the project, not pack its directory"
    grep -q "unrelated" "$FAKE_CALLS" \
        && fail "the neighbouring directory was packed" \
        || pass "nothing from the surrounding directory was packed"
}

# When packing does fail, the operator has to see why.
test_failed_pack_explains_itself() {
    fakes_reset
    setstate CURL_SOURCE_RC 1 "$FAKE_STATE"
    lonely="$TMP_TEST/lonely2"
    rm -rf "$lonely"; mkdir -p "$lonely"
    cp "$BOOTSTRAP_SH" "$lonely/bootstrap.sh"
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$lonely/bootstrap.sh" --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
            > "$TMP_TEST/out" 2> "$TMP_TEST/err"
        echo $?
    )"
    [ "$rc" != "0" ] || fail "a failed download must abort"
    stderr | grep -qi "github\|скачать\|download" \
        && pass "the failure says what could not be fetched" \
        || fail "the failure must name what it tried to fetch"
}

# --- main ---------------------------------------------------------------

test_bash_syntax
test_help
test_unknown_argument
test_domain_and_panel_port_combined
test_key_flags_domain_client_domain
test_password_env_panel_port
test_flags_domain_without_vpn_domain_uses_ip_endpoint
test_flags_panel_port_uses_ip_endpoint
test_interactive_auth_prompt_shows_digits
test_interactive_domain_prompts_include_examples
test_hyphenated_fqdn_flag_en_us
test_hyphenated_fqdn_interactive_en_us
test_interactive_invalid_fqdn_russian_error
test_interactive_key_explicit_domains
test_interactive_empty_vpn_domain_uses_ip_endpoint
test_interactive_no_domain_panel_port
test_interactive_password_with_keys_present
test_ssh_failure
test_install_failure
test_dns_mismatch
test_admin_password_not_in_call_log
test_tar_excludes_git_and_beads
test_pack_skips_worktrees_contents
test_pack_tar_fallback_excludes_worktrees
test_source_url
test_sshpass_missing
test_invalid_panel_port
test_default_panel_port_without_domain
test_install_progress_is_visible
test_standalone_downloads_the_project
test_failed_pack_explains_itself
test_summary_points_at_the_server_for_passwords
test_ssh_keepalive_tolerates_a_busy_host
test_rerun_survives_existing_server_row
test_real_init_failure_still_aborts

echo
if [ "$M123_ERRORS" -eq 0 ]; then
    echo "T-123 bootstrap.sh: ALL TESTS PASSED"
    exit 0
fi
echo "T-123 bootstrap.sh: $M123_ERRORS test(s) FAILED"
exit 1

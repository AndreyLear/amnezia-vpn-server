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
    rm -f "$FAKE_DIR/install.err"
    cat > "$FAKE_STATE" <<EOF
SSH_RC=0
INSTALL_RC=0
INIT_RC=0
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
if [ "$1" = "-C" ]; then
    shift 2
fi
if [ "${1:-}" = "archive" ]; then
    printf 'FAKE-GIT-ARCHIVE\n'
    exit 0
fi
exit 0
FAKE_EOF

chmod +x "$FAKE_DIR/ssh" "$FAKE_DIR/scp" "$FAKE_DIR/sshpass" \
    "$FAKE_DIR/tar" "$FAKE_DIR/curl" "$FAKE_DIR/openssl" "$FAKE_DIR/git"

run_bootstrap() {
    HOME="$FAKE_HOME" \
    PATH="$FAKE_DIR:$PATH" \
    bash "$BOOTSTRAP_SH" "$@" > "$TMP_TEST/out" 2> "$TMP_TEST/err"
    rc=$?
    echo $rc
}

stdout() { cat "$TMP_TEST/out"; }
stderr() { cat "$TMP_TEST/err"; }

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
}

test_unknown_argument() {
    fakes_reset
    rc="$(run_bootstrap --bogus)"
    [ "$rc" = "2" ] || fail "unknown argument: exit $rc, want 2"
    pass "unknown arguments rejected (exit 2)"
}

test_domain_and_panel_port_conflict() {
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --domain panel.example.com --panel-port 8443)"
    [ "$rc" = "2" ] || fail "domain+panel-port: exit $rc, want 2"
    pass "--domain + --panel-port conflict rejected"
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
    grep -q "add-user admin --password-stdin" "$FAKE_CALLS" && pass "key+domain: admin via password-stdin" \
        || fail "key+domain: add-user missing"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "key+domain: panel URL in summary" \
        || fail "key+domain: panel URL missing from summary"
    grep -q "Login:     admin" "$TMP_TEST/out" && pass "key+domain: login in summary" \
        || fail "key+domain: login missing"
    grep -q "Password:  TmpP4ssw0rd+/BASE64" "$TMP_TEST/out" && pass "key+domain: temp password in summary" \
        || fail "key+domain: password missing from summary"
    grep -q "/account" "$TMP_TEST/out" && pass "key+domain: change-password hint" \
        || fail "key+domain: /account hint missing"
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
    grep -q -- "--endpoint '2.26.93.192:51820'" "$FAKE_CALLS" \
        && pass "password-env: server init uses the public IP endpoint" \
        || fail "password-env: IP endpoint missing from server init"
    grep -q "BatchMode=yes" "$FAKE_CALLS" && fail "password-env: BatchMode=yes used with password auth" \
        || pass "password-env: BatchMode=yes not used"
}

test_flags_domain_without_client_domain_binds_panel_domain() {
    # T-129 default: --domain without --client-domain binds clients to
    # the panel domain (same as install.sh).
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" \
        --domain panel.example.com)"
    [ "$rc" = "0" ] || { fail "domain-default-bind: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--endpoint 'panel.example.com:51820'" "$FAKE_CALLS" \
        && pass "domain-default-bind: server init endpoint is the panel domain" \
        || fail "domain-default-bind: server init endpoint missing/wrong"
    grep -q "Endpoint:  panel.example.com:51820" "$TMP_TEST/out" \
        && pass "domain-default-bind: summary endpoint is the panel domain" \
        || fail "domain-default-bind: summary endpoint missing/wrong"
    grep -q "https://panel.example.com" "$TMP_TEST/out" && pass "domain-default-bind: panel URL is the domain" \
        || fail "domain-default-bind: panel URL missing"
}

test_flags_panel_port_uses_ip_endpoint() {
    # IP endpoint only when there is no panel domain and no --client-domain.
    fakes_reset
    rc="$(run_bootstrap --ip 2.26.93.192 --key "$FAKE_HOME/.ssh/id_ed25519" --panel-port 8443)"
    [ "$rc" = "0" ] || { fail "panel-port IP endpoint: exit $rc"; return 0; }
    grep -q -- "--endpoint '2.26.93.192:51820'" "$FAKE_CALLS" \
        && pass "panel-port IP endpoint: server init uses PUBLIC_IP:awg-port" \
        || fail "panel-port IP endpoint: server init endpoint missing/wrong"
    grep -q "Endpoint:  2.26.93.192:51820" "$TMP_TEST/out" \
        && pass "panel-port IP endpoint: summary uses the public IP" \
        || fail "panel-port IP endpoint: summary endpoint missing/wrong"
}

test_interactive_key_bind_yes() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
panel.example.com
y

51820
/opt/amnezia-vpn
ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive bind: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.example.com'" "$FAKE_CALLS" && pass "interactive bind: --domain passed" \
        || fail "interactive bind: --domain missing"
    grep -q -- "--client-domain 'panel.example.com'" "$FAKE_CALLS" \
        && pass "interactive bind: client domain defaults to the panel domain" \
        || fail "interactive bind: --client-domain missing"
    grep -q "Endpoint:  panel.example.com:51820" "$TMP_TEST/out" \
        && pass "interactive bind: endpoint in summary" \
        || fail "interactive bind: endpoint missing"
}

test_interactive_bind_no_uses_ip_endpoint() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root
panel.example.com
n
51820
/opt/amnezia-vpn
ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive no-bind: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--domain 'panel.example.com'" "$FAKE_CALLS" && pass "interactive no-bind: --domain passed" \
        || fail "interactive no-bind: --domain missing"
    grep -q -- "--endpoint '2.26.93.192:51820'" "$FAKE_CALLS" \
        && pass "interactive no-bind: server init uses PUBLIC_IP:awg-port" \
        || fail "interactive no-bind: IP endpoint missing from server init"
    grep -q "Endpoint:  2.26.93.192:51820" "$TMP_TEST/out" \
        && pass "interactive no-bind: summary uses the public IP" \
        || fail "interactive no-bind: summary endpoint missing/wrong"
}

test_interactive_no_domain_panel_port() {
    fakes_reset
    rc="$(
        HOME="$FAKE_HOME" PATH="$FAKE_DIR:$PATH" \
        bash "$BOOTSTRAP_SH" > "$TMP_TEST/out" 2> "$TMP_TEST/err" <<'ANSWERS'
2.26.93.192
root

8443
51820
/opt/amnezia-vpn
ANSWERS
        echo $?
    )"
    [ "$rc" = "0" ] || { fail "interactive IP:port: exit $rc"; cat "$TMP_TEST/err" >&2; return 0; }
    grep -q -- "--panel-port '8443'" "$FAKE_CALLS" && pass "interactive IP:port: --panel-port passed" \
        || fail "interactive IP:port: --panel-port missing"
    grep -q "https://2.26.93.192:8443" "$TMP_TEST/out" && pass "interactive IP:port: URL in summary" \
        || fail "interactive IP:port: URL missing"
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
    grep -q -- "--exclude=.git" "$FAKE_CALLS" && grep -q -- "--exclude=.beads" "$FAKE_CALLS" \
        && pass "tar excludes: .git and .beads" \
        || fail "tar excludes: --exclude .git/.beads missing"
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
    grep -q -- "--endpoint '2.26.93.192:51820'" "$FAKE_CALLS" \
        && pass "default panel-port: server init uses the public IP endpoint" \
        || fail "default panel-port: IP endpoint missing"
}

# --- main ---------------------------------------------------------------

test_bash_syntax
test_help
test_unknown_argument
test_domain_and_panel_port_conflict
test_key_flags_domain_client_domain
test_password_env_panel_port
test_flags_domain_without_client_domain_binds_panel_domain
test_flags_panel_port_uses_ip_endpoint
test_interactive_key_bind_yes
test_interactive_bind_no_uses_ip_endpoint
test_interactive_no_domain_panel_port
test_ssh_failure
test_install_failure
test_dns_mismatch
test_admin_password_not_in_call_log
test_tar_excludes_git_and_beads
test_source_url
test_sshpass_missing
test_invalid_panel_port
test_default_panel_port_without_domain

echo
if [ "$M123_ERRORS" -eq 0 ]; then
    echo "T-123 bootstrap.sh: ALL TESTS PASSED"
    exit 0
fi
echo "T-123 bootstrap.sh: $M123_ERRORS test(s) FAILED"
exit 1

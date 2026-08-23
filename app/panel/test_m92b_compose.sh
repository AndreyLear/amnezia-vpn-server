#!/bin/bash
# M9.2b compose topology static test harness.
#
# Asserts the M9.2b decisions directly against the repository compose.yaml:
#   - awg runs in the host network namespace (network_mode: host);
#   - awg has no ports: mapping, no sysctls: (host ip_forward is owned by
#     install.sh) and keeps its cap_add/cap_drop/devices/mounts;
#   - panel stays loopback-only (127.0.0.1:8787:8787) with
#     restart: unless-stopped;
#   - awg gets restart: unless-stopped; panel-init stays a one-shot job
#     without a restart policy;
#   - no docker.sock anywhere; M1-M8 mount contract unchanged.
#
#   bash app/panel/test_m92b_compose.sh
#
# Exit code: 0 when every check passes; 1 otherwise.

set -u

M92B_ERRORS=0
M92B_HOME="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE="$M92B_HOME/compose.yaml"

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; M92B_ERRORS=$((M92B_ERRORS + 1)); }

# label every compose line with its owning service: "awg:  <line>".
# A new service starts only at a 2-space-indented key ("  awg:"); the
# top-level "services:" key resets the current service.
labeled() {
    awk '
        /^[a-zA-Z0-9_-]+:/ { svc = ""; next }
        /^  [a-zA-Z0-9_-]+:/ { svc = $1; sub(":", "", svc); next }
        /^[[:space:]]/ && svc != "" { print svc ": " $0 }
    ' "$COMPOSE"
}

L="$(labeled)"

check_in() { # check_in needle label — grep on the labeled output
    grep -q "$1" <<< "$L" && pass "$2" || fail "$2"
}
check_out() { # check_out needle label — absence check on the labeled output
    grep -q "$1" <<< "$L" && fail "$2" || pass "$2"
}

test_bash_syntax() {
    bash -n "${BASH_SOURCE[0]}" && pass "bash -n test_m92b_compose.sh" || fail "bash -n test_m92b_compose.sh"
}

test_awg_topology() {
    check_in '^awg:.*network_mode: host' "awg: network_mode: host"
    check_out '^awg: +ports:' "awg: no ports: mapping (UDP is served in the host netns)"
    check_out '^awg:.*sysctls:' "awg: no sysctls: (ip_forward is owned by install.sh)"
}

test_panel_topology() {
    check_in '^panel:.*127\.0\.0\.1:8787:8787' "panel: loopback-only 127.0.0.1:8787:8787"
    check_out '^panel:.*- "0\.0\.0\.0' "panel: no 0.0.0.0 port mapping"
    check_in '^panel:.*restart: unless-stopped' "panel: restart: unless-stopped"
}

test_log_rotation() {
    # Colon+space after the service name so ^panel: does not also
    # match labeled panel-init lines (panel-init: …).
    check_in '^panel:[[:space:]].*driver: json-file' "panel: json-file logging driver"
    check_in '^panel:[[:space:]].*max-size: "10m"' "panel: log max-size 10m"
    check_in '^panel:[[:space:]].*max-file: "3"' "panel: log max-file 3"
    check_in '^panel-init:[[:space:]].*driver: json-file' "panel-init: json-file logging driver"
    check_in '^awg:[[:space:]].*driver: json-file' "awg: json-file logging driver"
    check_in '^awg:[[:space:]].*max-size: "10m"' "awg: log max-size 10m"
    check_in '^awg:[[:space:]].*max-file: "3"' "awg: log max-file 3"
    check_in '^dns:[[:space:]].*driver: json-file' "dns: json-file logging driver"
    check_in '^dns:[[:space:]].*max-size: "10m"' "dns: log max-size 10m"
}

test_restart_policy() {
    check_in '^awg:.*restart: unless-stopped' "awg: restart: unless-stopped"
    check_out '^panel-init:.*restart:' "panel-init: one-shot job without restart policy"
}

test_capabilities_devices() {
    check_in '^awg:.*- NET_ADMIN' "awg: CAP_NET_ADMIN present"
    check_in '^awg:.*- NET_RAW' "awg: NET_RAW dropped (cap_drop)"
    check_in '^awg:.*/dev/net/tun:/dev/net/tun' "awg: /dev/net/tun device mapped"
}

# T-rnub: the resolver answers the panel hostname with the tunnel
# address. It has to live in the host netns for the same reason awg
# does — 10.8.0.1 exists nowhere else — and it must publish no ports:
# a resolver reachable from the internet is an open resolver.
test_dns_topology() {
    check_in '^dns:.*network_mode: host' "dns: network_mode: host (10.8.0.1 lives there)"
    check_out '^dns: +ports:' "dns: no ports: mapping (never exposed to the WAN)"
    check_in '^dns:.*restart: unless-stopped' "dns: restart: unless-stopped"
    check_in '^dns:.*- NET_RAW' "dns: NET_RAW dropped (cap_drop)"
    check_out '^dns:.*/data' "dns: no data mount"
    check_out '^dns:.*/config' "dns: no config mount"
    check_in '^dns:.*path: \.env' "dns: deployment .env loaded (panel domain, upstreams)"
}

test_mount_contract() {
    check_in '^awg:.*- ./config:/config:ro' "awg: config mounted read-only"
    check_in '^awg:.*- ./status:/status$' "awg: status mounted read-write"
    check_out '^awg:.*./data' "awg: no /data mount"
    check_out '^awg:.*backups' "awg: no backups mount"
    check_in '^panel:.*- ./data:/data$' "panel: data mounted read-write"
    check_in '^panel:.*- ./config:/config$' "panel: config mounted read-write"
    check_in '^panel:.*- ./status:/status:ro' "panel: status mounted read-only"
    check_in '^panel:.*- ./backups:/data/backups' "panel: backups mounted read-write"
    check_in '^panel-init:.*- ./backups:/data/backups' "panel-init: backups mounted read-write (M10.2-FIX shared store)"
    check_in '^panel:.*env_file:' "panel: deployment .env loaded"
    check_in '^panel:.*path: \.env' "panel: env_file is the deployment .env"
    check_in '^panel-init:.*env_file:' "panel-init: deployment .env loaded (M10.2-FIX2)"
    check_in '^panel-init:.*path: \.env' "panel-init: env_file is the deployment .env"
    check_out '^awg:.*env_file:' "awg: no deployment .env"
    check_out 'docker\.sock' "no service mounts the docker socket"
}

test_startup_order() {
    # Named per service, not counted: a new service used to break this
    # check without anything actually being wrong.
    check_in '^panel:.*service_completed_successfully' "panel waits for panel-init to finish"
    check_in '^awg:.*service_completed_successfully' "awg waits for panel-init to finish"
    check_in '^dns:.*service_completed_successfully' "dns waits for panel-init to finish"
}

test_depends_on_preserved() {
    check_in '^awg:.*depends_on:' "awg: depends_on preserved"
    check_in '^panel:.*depends_on:' "panel: depends_on preserved"
}

# --- main ---------------------------------------------------------------

test_bash_syntax
test_awg_topology
test_panel_topology
test_log_rotation
test_restart_policy
test_capabilities_devices
test_dns_topology
test_mount_contract
test_startup_order
test_depends_on_preserved

echo
if [ "$M92B_ERRORS" -eq 0 ]; then
    echo "M9.2b compose topology: ALL TESTS PASSED"
    exit 0
fi
echo "M9.2b compose topology: $M92B_ERRORS test(s) FAILED"
exit 1
#!/usr/bin/env bash
# cleanup.sh — remove amnezia-vpn artifacts from the server (T-119).
#
# Dry-run by default: prints the plan, changes nothing.
#   --yes                      execute the plan
#   --i-know-what-i-am-doing   allow cleanup when foreign amnezia-*
#                              artifacts (e.g. amnezia-wg-easy) are present
#
# Removes only artifacts created by install.sh:
#   - docker compose down / down --volumes from /opt/amnezia-vpn
#   - containers amnezia-vpn-*
#   - images amnezia-vpn-server/* (including the ghcr.io/<owner>/ prefix)
#   - volumes amnezia-vpn_*
#   - nft table ip amnezia and the marker block in /etc/nftables.conf
#   - systemd units amnezia-vpn-forward.service and
#     docker.service.d/amnezia-vpn-nftables.conf
#   - /etc/modules-load.d/amneziawg.conf, /etc/modules-load.d/amnezia-vpn-bbr.conf,
#     /etc/sysctl.d/99-amnezia-vpn.conf, ip link awg0
#   - nginx site amnezia-panel
#   - /opt/amnezia-vpn, /opt/amnezia-vpn-src
#
set -u

COMPOSE_DIR="${COMPOSE_DIR:-/opt/amnezia-vpn}"
SRC_DIR="${SRC_DIR:-/opt/amnezia-vpn-src}"
NFT_CONF=/etc/nftables.conf
NFT_BEGIN='# --- amnezia-vpn begin ---'
NFT_END='# --- amnezia-vpn end ---'
SYSTEMD_DIR=/etc/systemd/system
NGINX_SITE=/etc/nginx/sites-enabled/amnezia-panel
MODULES_FILE=/etc/modules-load.d/amneziawg.conf
BBR_MODULES_FILE=/etc/modules-load.d/amnezia-vpn-bbr.conf
SYSCTL_FILE=/etc/sysctl.d/99-amnezia-vpn.conf

DO_IT=0
FORCE=0

usage() {
    echo "usage: $0 [--yes] [--i-know-what-i-am-doing]"
    echo "  default:                  dry-run (print the plan, change nothing)"
    echo "  --yes:                    execute the plan"
    echo "  --i-know-what-i-am-doing: proceed even if foreign amnezia-* artifacts exist"
}

for arg in "$@"; do
    case "$arg" in
        --yes) DO_IT=1 ;;
        --i-know-what-i-am-doing) FORCE=1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "$0: unknown argument: $arg" >&2; usage >&2; exit 1 ;;
    esac
done

log() { printf 'cleanup: %s\n' "$*"; }

if [ "$(id -u)" -ne 0 ]; then
    log "root is required (docker, systemctl, nft, ip)"
    exit 1
fi

if [ "$DO_IT" -eq 1 ]; then
    log "mode: execute"
else
    log "mode: dry-run (nothing will be changed; use --yes to execute)"
fi

# --- guard: foreign amnezia-* artifacts --------------------------------

foreign_artifacts() {
    docker ps -a --format '{{.Names}}' 2>/dev/null \
        | awk '/^amnezia-/ && !/^amnezia-vpn-/'
    # Since the images moved to GHCR they carry a registry prefix
    # (ghcr.io/<owner>/amnezia-vpn-server/panel). Without it here, cleanup
    # mistook its own images for another project's and refused to run.
    docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null \
        | awk '/amnezia-/ && !/(^|\/)amnezia-vpn-server\//'
    docker volume ls --format '{{.Name}}' 2>/dev/null \
        | awk '/^amnezia-/ && !/^amnezia-vpn_/'
    ls "$SYSTEMD_DIR" 2>/dev/null \
        | awk '/^amnezia-/ && !/^amnezia-vpn-/'
    ls /etc/nginx/sites-enabled 2>/dev/null \
        | awk '/^amnezia-/ && $0 != "amnezia-panel"'
    nft list tables 2>/dev/null \
        | awk '/table (ip|ip6|inet) amnezia/ && $0 != "table ip amnezia"'
    local f
    for f in /opt/amnezia-*; do
        [ -e "$f" ] || continue
        case "$f" in
            "$COMPOSE_DIR"|"$SRC_DIR") ;;
            *) printf '%s\n' "$f" ;;
        esac
    done
}

FOREIGN="$(foreign_artifacts)"
if [ -n "$FOREIGN" ] && [ "$FORCE" -ne 1 ]; then
    log "foreign amnezia-* artifacts detected (another amnezia project?):"
    printf '%s\n' "$FOREIGN" | sed 's/^/cleanup:   /'
    log "refusing to run; re-run with --i-know-what-i-am-doing to proceed"
    exit 1
fi

# --- docker compose stack from /opt/amnezia-vpn ------------------------

if [ -f "$COMPOSE_DIR/compose.yaml" ]; then
    if [ -f "$COMPOSE_DIR/versions.lock" ]; then
        if [ "$DO_IT" -eq 1 ]; then
            log "docker compose --env-file versions.lock down (from $COMPOSE_DIR)"
            docker compose --project-directory "$COMPOSE_DIR" \
                --env-file "$COMPOSE_DIR/versions.lock" \
                -f "$COMPOSE_DIR/compose.yaml" down
            log "docker compose --env-file versions.lock down --volumes (from $COMPOSE_DIR)"
            docker compose --project-directory "$COMPOSE_DIR" \
                --env-file "$COMPOSE_DIR/versions.lock" \
                -f "$COMPOSE_DIR/compose.yaml" down --volumes
        else
            log "would run: docker compose --env-file versions.lock down (from $COMPOSE_DIR)"
            log "would run: docker compose --env-file versions.lock down --volumes (from $COMPOSE_DIR)"
        fi
    else
        log "skip: $COMPOSE_DIR/versions.lock not found; compose down skipped"
    fi
else
    log "skip: $COMPOSE_DIR/compose.yaml not found"
fi

# --- leftover containers amnezia-vpn-* ---------------------------------

containers="$(docker ps -a --format '{{.Names}}' 2>/dev/null | awk '/^amnezia-vpn-/')"
if [ -n "$containers" ]; then
    log "containers to remove:"
    printf '%s\n' "$containers" | sed 's/^/cleanup:   /'
    if [ "$DO_IT" -eq 1 ]; then
        printf '%s\n' "$containers" | while read -r c; do
            [ -n "$c" ] || continue
            docker rm -f "$c" >/dev/null && log "removed container: $c"
        done
    fi
else
    log "skip: no containers amnezia-vpn-*"
fi

# --- images amnezia-vpn-server/* ---------------------------------------

images="$(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | awk '/(^|\/)amnezia-vpn-server\//')"
if [ -n "$images" ]; then
    log "images to remove:"
    printf '%s\n' "$images" | sed 's/^/cleanup:   /'
    if [ "$DO_IT" -eq 1 ]; then
        printf '%s\n' "$images" | while read -r i; do
            [ -n "$i" ] || continue
            docker rmi "$i" >/dev/null && log "removed image: $i"
        done
    fi
else
    log "skip: no images amnezia-vpn-server/*"
fi

# --- volumes amnezia-vpn_* ---------------------------------------------

volumes="$(docker volume ls --format '{{.Name}}' 2>/dev/null | awk '/^amnezia-vpn_/')"
if [ -n "$volumes" ]; then
    log "volumes to remove (full names):"
    printf '%s\n' "$volumes" | sed 's/^/cleanup:   /'
    if [ "$DO_IT" -eq 1 ]; then
        printf '%s\n' "$volumes" | while read -r v; do
            [ -n "$v" ] || continue
            docker volume rm "$v" >/dev/null && log "removed volume: $v"
        done
    fi
else
    log "skip: no volumes amnezia-vpn_*"
fi

# --- nft table ip amnezia ----------------------------------------------

if nft list tables 2>/dev/null | grep -qx 'table ip amnezia'; then
    if [ "$DO_IT" -eq 1 ]; then
        nft delete table ip amnezia && log "removed nft table ip amnezia"
    else
        log "would run: nft delete table ip amnezia"
    fi
else
    log "skip: nft table ip amnezia not present"
fi

# --- marker block in /etc/nftables.conf --------------------------------

if [ -f "$NFT_CONF" ] && grep -Fq "$NFT_BEGIN" "$NFT_CONF"; then
    if [ "$DO_IT" -eq 1 ]; then
        BEGIN="$NFT_BEGIN" END="$NFT_END" awk '
            $0 == ENVIRON["BEGIN"] { skip = 1; next }
            $0 == ENVIRON["END"]   { skip = 0; next }
            skip { next }
            { print }
        ' "$NFT_CONF" > "$NFT_CONF.new" && mv "$NFT_CONF.new" "$NFT_CONF"
        log "removed amnezia-vpn block from $NFT_CONF"
    else
        log "would remove amnezia-vpn block from $NFT_CONF"
    fi
else
    log "skip: no amnezia-vpn block in $NFT_CONF"
fi

# --- systemd units -----------------------------------------------------

if [ -f "$SYSTEMD_DIR/amnezia-vpn-forward.service" ]; then
    if [ "$DO_IT" -eq 1 ]; then
        systemctl disable --now amnezia-vpn-forward.service >/dev/null 2>&1 || true
        rm -f "$SYSTEMD_DIR/amnezia-vpn-forward.service"
        systemctl daemon-reload
        log "removed $SYSTEMD_DIR/amnezia-vpn-forward.service"
    else
        log "would remove $SYSTEMD_DIR/amnezia-vpn-forward.service"
    fi
else
    log "skip: amnezia-vpn-forward.service not present"
fi

DROPIN="$SYSTEMD_DIR/docker.service.d/amnezia-vpn-nftables.conf"
if [ -f "$DROPIN" ]; then
    if [ "$DO_IT" -eq 1 ]; then
        rm -f "$DROPIN"
        systemctl daemon-reload
        log "removed $DROPIN"
    else
        log "would remove $DROPIN"
    fi
else
    log "skip: $DROPIN not present"
fi

# --- modules-load and sysctl ------------------------------------------
#
# install.sh writes three files outside the deploy root: the amneziawg
# module, the tcp_bbr module and the managed sysctl drop-in. Leaving the
# last two behind means a host that no longer runs this product still loads
# tcp_bbr at boot and still carries our ip_forward, conntrack and buffer
# settings.

for managed in "$MODULES_FILE" "$BBR_MODULES_FILE" "$SYSCTL_FILE"; do
    if [ -f "$managed" ]; then
        if [ "$DO_IT" -eq 1 ]; then
            rm -f "$managed"
            log "removed $managed"
        else
            log "would remove $managed"
        fi
    else
        log "skip: $managed not present"
    fi
done

# --- awg0 interface ----------------------------------------------------

if ip link show awg0 >/dev/null 2>&1; then
    if [ "$DO_IT" -eq 1 ]; then
        ip link del awg0 && log "deleted interface awg0"
    else
        log "would run: ip link del awg0"
    fi
else
    log "skip: interface awg0 not present"
fi

# --- nginx site amnezia-panel ------------------------------------------

if [ -e "$NGINX_SITE" ] || [ -L "$NGINX_SITE" ]; then
    if [ "$DO_IT" -eq 1 ]; then
        rm -f "$NGINX_SITE"
        log "removed $NGINX_SITE"
        if systemctl is-active --quiet nginx 2>/dev/null; then
            nginx -s reload 2>/dev/null || true
            log "nginx reloaded"
        fi
    else
        log "would remove $NGINX_SITE"
    fi
else
    log "skip: nginx site amnezia-panel not present"
fi

# --- deployment directories --------------------------------------------

for d in "$COMPOSE_DIR" "$SRC_DIR"; do
    if [ -d "$d" ]; then
        if [ "$DO_IT" -eq 1 ]; then
            rm -rf "$d"
            log "removed $d"
        else
            log "would run: rm -rf $d"
        fi
    else
        log "skip: $d not present"
    fi
done

if [ "$DO_IT" -eq 1 ]; then
    log "done"
else
    log "dry-run complete; nothing was changed"
fi

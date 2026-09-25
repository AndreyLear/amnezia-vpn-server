#!/usr/bin/env bash
#
# docker-prune.sh — free golang toolchain images and Docker build cache
# after install.sh builds the stack, and the project's images of versions
# no longer deployed. The images of the version in versions.lock and of the
# version the update agent keeps for a rollback, alpine, compose services
# and /opt data are left alone.
#
# Idempotent: a second run is a no-op when there is nothing left.
# Never uses `docker system prune -a` or `compose down`.
#
# Missing docker, empty cache, and already-removed images do not abort
# the caller: this script exits 0 unless --help parsing fails.

set -u

usage() {
    cat <<'EOF'
docker-prune.sh — remove golang toolchain images, Docker build cache and
the project images of versions no longer deployed.

Keeps the images of the version in versions.lock and of the rollback
snapshot, alpine, compose stacks and /opt data.

Usage:
  ./docker-prune.sh

Options:
  --help    print this message

Examples:
  ./docker-prune.sh
EOF
}

log() { printf 'docker-prune: %s\n' "$*"; }

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi
if [ "$#" -gt 0 ]; then
    printf 'docker-prune: unknown argument: %s\n' "$1" >&2
    printf '  ./docker-prune.sh\n' >&2
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    printf 'docker-prune: docker not found\n' >&2
    exit 0
fi

log "pruning build cache (docker builder prune -af)"
docker builder prune -af >/dev/null 2>&1 || true

log "pruning dangling images (docker image prune -f)"
docker image prune -f >/dev/null 2>&1 || true

log "removing golang toolchain images"
golang_ids="$(docker images -q golang 2>/dev/null || true)"
if [ -n "$golang_ids" ]; then
    # Word-split image IDs from `docker images -q`. Failures (already
    # gone) must not abort; never rmi panel/awg/alpine here.
    # shellcheck disable=SC2086
    docker rmi $golang_ids >/dev/null 2>&1 || true
fi
golang_tags="$(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null || true)"
if [ -n "$golang_tags" ]; then
    printf '%s\n' "$golang_tags" | while IFS= read -r tag; do
        case "$tag" in
            golang:*)
                docker rmi "$tag" >/dev/null 2>&1 || true
                ;;
        esac
    done
fi

# Project images of earlier versions (amnezia-vpn-server-76mp.34). Every
# update pulls a full set under a new tag and nothing ever removed the old
# one: `image prune` only takes untagged layers, and on a small VPS disk the
# sets pile up release after release.
#
# Kept: the version in versions.lock (what runs) and the version in the
# update agent's rollback snapshot (update-agent.sh copies versions.lock
# into ROLLBACK_DIR before it installs; while that snapshot exists the old
# images are what a rollback starts from, possibly with the network being
# the very thing that failed). Without a readable versions.lock nothing is
# removed — "current" is then unknown. Only repositories under the
# project's own IMAGE_REGISTRY are touched, and only tagged ones; a
# running container's image refuses rmi, which is fine.
PRUNE_ROOT="${AMNEZIA_PRUNE_ROOT:-$(cd "$(dirname "$0")" && pwd)}"
ROLLBACK_DIR="${AMNEZIA_UPDATE_ROLLBACK:-${PRUNE_ROOT}/.rollback}"

lock_value() { # lock_value FILE KEY
    sed -n "s/^$2=//p" "$1" 2>/dev/null | tail -1
}

registry="$(lock_value "${PRUNE_ROOT}/versions.lock" IMAGE_REGISTRY)"
current="$(lock_value "${PRUNE_ROOT}/versions.lock" IMAGE_VERSION)"
rollback="$(lock_value "${ROLLBACK_DIR}/versions.lock" IMAGE_VERSION)"
if [ -z "$registry" ] || [ -z "$current" ]; then
    log "versions.lock not readable under ${PRUNE_ROOT}: project images left alone"
    exit 0
fi
log "removing project images other than ${current}${rollback:+ and rollback ${rollback}}"
docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | while IFS= read -r ref; do
    case "$ref" in
        "${registry}"/*:*) ;;
        *) continue ;;
    esac
    tag="${ref##*:}"
    case "$tag" in
        "<none>" | "$current") continue ;;
    esac
    [ -n "$rollback" ] && [ "$tag" = "$rollback" ] && continue
    docker rmi "$ref" >/dev/null 2>&1 || true
done

exit 0

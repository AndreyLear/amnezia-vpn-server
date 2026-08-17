#!/usr/bin/env bash
#
# docker-prune.sh — free golang toolchain images and Docker build cache
# after install.sh builds the stack. Runtime panel/awg/alpine images,
# compose services, and /opt data are left alone.
#
# Idempotent: a second run is a no-op when there is nothing left.
# Never uses `docker system prune -a` or `compose down`.
#
# Missing docker, empty cache, and already-removed images do not abort
# the caller: this script exits 0 unless --help parsing fails.

set -u

usage() {
    cat <<'EOF'
docker-prune.sh — remove golang toolchain images and Docker build cache.

Does not remove runtime panel/awg images, alpine, compose stacks, or /opt data.

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

exit 0

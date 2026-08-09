# syntax=docker/dockerfile:1
#
# Docker build context: repository root (NOT app/awg):
#   docker build -f app/awg/Dockerfile \
#     --build-arg GO_VERSION=<from versions.lock> \
#     --build-arg ALPINE_VERSION=<from versions.lock> \
#     --build-arg AMNEZIAWG_GO_VERSION=<from versions.lock> \
#     --build-arg AMNEZIAWG_GO_COMMIT=<from versions.lock> \
#     --build-arg AMNEZIAWG_TOOLS_VERSION=<from versions.lock> \
#     --build-arg AMNEZIAWG_TOOLS_COMMIT=<from versions.lock> \
#     .
#
# All versions are injected from versions.lock at the repo root by the
# build invocation; nothing is hardcoded here. Builds always checkout the
# exact pinned commit (never master/main/latest or a floating tag):
# floating refs would make the image non-reproducible.

ARG GO_VERSION
ARG ALPINE_VERSION

# ---- build stage: userspace AmneziaWG daemon (amneziawg-go) ----
FROM golang:${GO_VERSION} AS amneziawg-go-build

ARG AMNEZIAWG_GO_VERSION
ARG AMNEZIAWG_GO_COMMIT

RUN git clone --quiet https://github.com/amnezia-vpn/amneziawg-go.git /src/amneziawg-go \
    && cd /src/amneziawg-go \
    && git fetch --quiet --depth 1 origin "${AMNEZIAWG_GO_COMMIT}" \
    && git checkout --quiet FETCH_HEAD \
    && git rev-parse HEAD

WORKDIR /src/amneziawg-go

ENV CGO_ENABLED=0 \
    GOOS=linux

RUN go build -trimpath -ldflags="-s -w" -o /out/amneziawg-go .

# ---- build stage: amneziawg-tools (awg, awg-quick) ----
# Alpine variant of the same pinned go version: build tooling (gcc/make)
# comes from apk; the tag is deterministic because GO_VERSION is pinned.
FROM golang:${GO_VERSION}-alpine AS amneziawg-tools-build

ARG AMNEZIAWG_TOOLS_VERSION
ARG AMNEZIAWG_TOOLS_COMMIT

RUN apk add --no-cache git make gcc musl-dev linux-headers

RUN git clone --quiet https://github.com/amnezia-vpn/amneziawg-tools.git /src/amneziawg-tools \
    && cd /src/amneziawg-tools \
    && git fetch --quiet --depth 1 origin "${AMNEZIAWG_TOOLS_COMMIT}" \
    && git checkout --quiet FETCH_HEAD \
    && git rev-parse HEAD

WORKDIR /src/amneziawg-tools

RUN make -C src \
    && mkdir -p /out \
    && cp src/wg /out/awg \
    && cp src/wg-quick/linux.bash /out/awg-quick \
    && chmod 0755 /out/awg /out/awg-quick

# ---- runtime stage: only binaries + tools required to run ----
FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache bash iproute2 iptables

COPY --from=amneziawg-go-build /out/amneziawg-go /usr/bin/amneziawg-go
COPY --from=amneziawg-tools-build /out/awg /out/awg-quick /usr/bin/

RUN mkdir -p /etc/amnezia/amneziawg /var/run/amneziawg

COPY --chmod=0755 app/awg/entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
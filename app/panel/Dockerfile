# syntax=docker/dockerfile:1
#
# Docker build context: repository root (NOT app/panel):
#   docker build -f app/panel/Dockerfile \
#     --build-arg GO_VERSION=<from versions.lock> \
#     --build-arg ALPINE_VERSION=<from versions.lock> \
#     .
#
# Image versions are NOT hardcoded here: GO_VERSION and ALPINE_VERSION
# are injected from versions.lock at the repo root by the build
# invocation (compose/Makefile/CI). If they are missing, the build
# fails early on the unresolved FROM tag.

# ---- build stage: compile the panel binary without CGO ----
ARG GO_VERSION
ARG ALPINE_VERSION
FROM golang:${GO_VERSION} AS build

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOOS=linux

COPY app/panel/go.mod app/panel/go.sum ./
RUN go mod download

COPY app/panel/ ./
RUN go build -trimpath -ldflags="-s -w" -o /out/panel .

# --- runtime stage: Alpine, only the binary ----
FROM alpine:${ALPINE_VERSION}

COPY --from=build /out/panel /app/panel
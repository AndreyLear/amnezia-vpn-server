# Architecture

## Core principle

SQLite is the single source of truth.

Everything else is derived state and must be reproducible from SQLite.

## Containers

The application consists of three Compose services:

- `panel-init`
- `panel`
- `awg`

## Ownership

### panel-init

- initializes and migrates SQLite (`data/amnezia.sqlite`, schema from
  TECHNICAL_SPEC_v2.0.md §3; idempotent, schema_version recorded in
  `schema_meta` — supported since M2)
- generates the initial `awg0.conf` (M3 — `awg0.conf generator`; not yet
  implemented; `init` succeeds without it and states so)

Compose `depends_on: condition: service_completed_successfully` passes once
the SQLite migration lands; the remaining pieces of a full stack start
still wait for the M3 generator.

### panel

- reads and writes SQLite
- owns `awg0.conf`
- generates client configurations on demand
- reads `status.json`

### awg

- does not access SQLite
- does not access Docker socket
- reads `awg0.conf` as read-only
- runs the AmneziaWG interface
- writes `status.json`

## Communication

There is no HTTP/REST API between `panel` and `awg`.

The containers communicate through shared files:

- `config/awg0.conf`
- `status/status.json`

## Derived state

The following files are disposable:

- `config/awg0.conf`
- `status/status.json`
- generated client configuration files

Deleting them must never result in data loss.

## Security invariants

- `panel` must never access `/var/run/docker.sock`.
- `awg` must never access SQLite.
- `awg` must never write `awg0.conf`.
- Application backups must not contain derived state.
- Application backups must always be encrypted with `age`.
- The `age` identity/private key must never be stored on the VPS.

## Deployment

Application state and VPS infrastructure are separate.

Migration consists of:

1. `install.sh`
2. restore application backup
3. `docker compose up`

## AWG Runtime Architecture

### Build reproducibility

- The AWG runtime image is built from the exact commits pinned in `versions.lock`:
  - `amneziawg-go` at `AMNEZIAWG_GO_COMMIT=1cc94272ca8e9e223a5fe76382f5880f09d3c12d` (tag `v0.2.19` points to the same SHA).
  - `amneziawg-tools` at `AMNEZIAWG_TOOLS_COMMIT=5d6179a6d0842e98dfb349c28cf1bd8e4b9d1079` (tag `v1.0.20260223` points to the same SHA).
- The Docker build checks out the pinned SHA. Even if a tag resolves to the same SHA, the build must checkout the SHA, never a floating ref.
- `master`, `main`, `latest` and other floating refs are never used in builds.
- `versions.lock` is the single source of pinned external versions; values are passed to the Dockerfile as Compose build args guarded by `${VAR:?}`.

### AWG userspace model

`awg-quick` follows this chain:

```text
awg-quick
  → kernel implementation, if available
  → userspace fallback
  → amneziawg-go
  → UAPI Unix socket
  → /var/run/amneziawg/<interface>.sock
```

For the container design the primary expected path is the userspace implementation through `amneziawg-go`. The container never loads kernel modules.

### Runtime contents

The runtime image contains only runtime components:

- `amneziawg-go`
- `awg`
- `awg-quick`
- `bash`
- `iproute2`
- `iptables`

Compiler/toolchain and other build dependencies never reach the runtime image (multi-stage build; `CGO_ENABLED=0` for the Go daemon).

### Linux privileges

The AWG container gets:

- `CAP_NET_ADMIN`
- `/dev/net/tun`

It does NOT get:

- `privileged`
- `SYS_MODULE`
- `NET_RAW`

This matches the security rules in `TECHNICAL_SPEC_v2.0.md` §2.

### Mount contract

AWG gets:

- `./config:/config:ro`
- `./status:/status`

AWG does NOT get:

- `./data` and therefore no SQLite access
- `/var/run/docker.sock`
- `backups`
- any other application state mounts

`panel` and `panel-init` remain the owners of SQLite and `/config`.

### Configuration flow

Source:

```text
/config/awg0.conf
```

Runtime copy:

```text
/etc/amnezia/amneziawg/awg0.conf
```

Reason: `awg-quick` expects the configuration at the standard system path (`/etc/amnezia/amneziawg/`), while `/config` is a read-only mount. AWG never modifies `/config/awg0.conf` directly.

### Initial runtime lifecycle

First phase of M1:

1. wait for `/config/awg0.conf`;
2. copy it to the runtime configuration path with safe permissions;
3. run `awg-quick up awg0`;
4. keep the userspace daemon under the container's control;
5. on SIGTERM/SIGINT run `awg-quick down awg0`;
6. exit the container cleanly.

### Not included in the first AWG image

Deliberately NOT part of the initial image implementation:

- `status.json` collector;
- hot reload by mtime/inotify;
- `awg syncconf`;
- configuration generation from SQLite;
- database access;
- HTTP API.

These are verified in separate stages after the basic AWG runtime starts successfully.

### Open questions

1. Live compatibility `awg` tools `1.0.20260223` ↔ `amneziawg-go` `0.2.19`.
2. Real `dump` output of the userspace AWG.
3. Real `awg syncconf` behavior against a userspace daemon.
4. `ip_forward` behavior inside the Docker netns on the target VPS.

None of these are confirmed facts until verified by tests.
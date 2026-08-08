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

- initializes and migrates SQLite
- generates the initial `awg0.conf`

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
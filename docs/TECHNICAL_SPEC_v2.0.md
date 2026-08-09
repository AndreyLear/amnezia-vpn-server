# AmneziaWG VPN Server — Technical Specification v2.0

**Status:** architecture fixed. This document is the project baseline.

## 1. Goal

Self-hosted VPN server on AmneziaWG 2.0+ with a Go web panel. SQLite is the **single source of truth**; `config/awg0.conf` and `status/status.json` are disposable derived state.

Migration to a new VPS:

`install.sh` → `restore backup` → `docker compose up`

## 2. Architecture

Three Docker Compose services:

- **panel-init** — one-shot job: database migrations and initial `awg0.conf` generation.
- **panel** — HTTP server and the only service that reads/writes SQLite.
- **awg** — AmneziaWG runtime; no SQLite access.

The AWG container is a small runtime wrapper. It waits for `/config/awg0.conf`, runs `awg-quick up` on initial startup, polls the config mtime, uses `awg syncconf` after changes, and converts `awg show awg0 dump` into an atomic `status/status.json`.

```text
/opt/amnezia-vpn/
├── compose.yaml
├── .env
├── data/
│   └── amnezia.sqlite
├── config/
│   └── awg0.conf
├── status/
│   └── status.json
└── backups/
```

There is no `clients/*.conf`; client configs are generated on demand.

### Security and ownership

- `panel` must **never** mount `/var/run/docker.sock`.
- `panel` and `panel-init`: RW `/data` and `/config`.
- `panel`: RO `/status`.
- `awg`: RO `/config`, RW `/status`.
- `awg` has no `/data` mount.
- `awg` never writes `config/awg0.conf`.
- No inter-container HTTP/REST API.

For M0, AWG uses directory-level read-only access to `/config`. File-level isolation can be tightened later without changing ownership.

## 3. Database

```sql
CREATE TABLE server (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    private_key TEXT NOT NULL,
    public_key TEXT NOT NULL,
    address TEXT NOT NULL,
    listen_port INTEGER NOT NULL,
    dns TEXT,
    awg_params TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE clients (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    private_key TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    preshared_key TEXT,
    address TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    expires_at TEXT
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE auth (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    totp_secret TEXT
);

CREATE TABLE schema_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

`public_key` is stored explicitly. `awg_params` is JSON and is not a protocol contract; the generator knows which parameters the installed AWG version supports.

## 4. Lifecycle

First start:

```text
panel-init
  → migrations / DB creation
  → generate awg0.conf
  → exit successfully
  → panel + awg start
```

Runtime change:

```text
SQLite transaction
  → generate awg0.conf.tmp
  → fsync
  → rename → awg0.conf
  → AWG detects mtime
  → awg syncconf
```

Status:

```text
awg show awg0 dump
  → parse
  → status.json.tmp
  → fsync
  → rename → status.json
  → panel reads status
```

`panel-init` must be idempotent.

## 5. Backup / Restore

Backup format:

```text
backup-YYYY-MM-DD.tar.zst.age
├── manifest.json
└── amnezia.sqlite
```

The SQLite snapshot **must use the SQLite Backup API**. Direct file copying is forbidden.

Archives are **always encrypted with `age`**.

`manifest.json`:

```json
{
  "format": 1,
  "application": "amnezia-vpn-server",
  "application_version": "2.0.0",
  "schema_version": 3,
  "created_at": "2026-08-08T10:00:00Z"
}
```

Derived files are excluded.

`age` roles:

- **recipient** — public key; may be on the VPS and is used for encryption.
- **identity** — private key; never stored on the VPS and supplied manually during restore.

Loss of the identity makes the backup unrecoverable and must be stated in UI/docs.

Restore is a restart workflow:

```text
upload .age
→ decrypt to temporary directory
→ unpack
→ validate manifest
→ SQLite integrity_check
→ schema compatibility check
→ safety-backup current state
→ mark restored DB as pending
→ restart required
→ panel-init migrations
→ regenerate awg0.conf
→ AWG startup/restart
```

If validation fails before the safety-backup step, the working state is untouched.

The panel does **not** use Docker socket access to restart itself.

## 6. Panel

Client operations:

| Action | Mechanism |
|---|---|
| Add | Generate keys → DB → regenerate `awg0.conf` |
| Delete | DB → regenerate `awg0.conf` |
| Enable/disable | DB flag → regenerate `awg0.conf` |
| Download config | Generate on demand |
| QR | Generate on demand |

Client card: name, online/offline, last handshake, RX/TX, creation date.

Online means handshake within 3 minutes.

Authentication in MVP: username/password, Argon2 or bcrypt, cookie session. TOTP is M9.

UI: server-side `html/template`, vertical client-card stack, minimal JS, no SPA/framework.

The panel is one Go binary with subcommands:

```text
/app/panel init
/app/panel serve
/app/panel client ...
/app/panel backup ...
/app/panel restore ...
```

## 7. Technology

| Component | Decision |
|---|---|
| AWG | `amneziawg-go` + `amneziawg-tools`, pinned |
| Backend | Go, mostly stdlib |
| SQLite | `modernc.org/sqlite`, no ORM |
| Frontend | Server-side HTML |
| Backup | SQLite Backup API → `tar.zst` → `age` |
| Orchestration | Docker Compose |
| Firewall | nftables |

## 8. Version policy

`versions.lock` is the canonical version inventory. No `latest` or floating production dependency.

Current pins:

```text
GO_VERSION=1.25.12
ALPINE_VERSION=3.22.5

AMNEZIAWG_GO_VERSION=0.2.19
AMNEZIAWG_GO_COMMIT=1cc94272ca8e9e223a5fe76382f5880f09d3c12d

AMNEZIAWG_TOOLS_VERSION=1.0.20260223
AMNEZIAWG_TOOLS_COMMIT=5d6179a6d0842e98dfb349c28cf1bd8e4b9d1079

SQLITE_DRIVER=modernc.org/sqlite
SQLITE_DRIVER_VERSION=1.54.0
MODERNC_LIBC_VERSION=1.74.1

AGE_VERSION=1.3.1
ZSTD_VERSION=1.5.7
```

`modernc.org/sqlite` is pure Go / CGO-free. Its transitive `modernc.org/libc` version is pinned.

## 9. install.sh

`install.sh` is infrastructure state, not application backup.

Responsibilities:

1. Check OS.
2. Install Docker + Compose plugin.
3. Configure IP forwarding.
4. Configure nftables (NAT, forwarding, UDP port).
5. Create `/opt/amnezia-vpn` and directories.
6. Install `compose.yaml`.
7. Start stack.
8. Run doctor checks.

VPS migration remains two independent operations:

`install.sh` + `restore backup`.

## 10. Milestones

```text
M0  Repository structure, compose skeleton, version pins, panel build skeleton
M1  awg-container: clean AWG 2.0+, reads awg0.conf
M2  SQLite schema + panel-init
M3  awg0.conf generator + syncconf/mtime polling
M4  CLI client management
M5  status.json producer/consumer + atomic write
M6  Basic panel CRUD + client cards
M7  Authentication
M8  Backup/Restore UI
M9  Hardening
    M9.1 install.sh
    M9.2 healthcheck
    M9.3 doctor
    M9.4 nftables
    M9.5 TOTP
    M9.6 VPS → VPS migration test
```

### M0 criteria

- repository skeleton
- three-service Compose architecture
- panel Dockerfile and Go module
- pinned versions
- SQLite + libc pins
- correct Git ignore rules
- no Docker socket
- AWG has no SQLite access
- AWG config is RO
- panel data/config RW and status RO
- guarded Docker build args
- every M0 change committed to Git

## 11. MVP exclusions

M0–M8 do not include:

- TOTP
- doctor / integrated diagnostics
- traffic history graphs
- multiple roles
- Vue/React/Node/Express
- ORM
- Redis/PostgreSQL
- inter-container HTTP/REST API
- custom WireGuard/AWG implementation
- derived public-key computation; public keys remain stored explicitly

## 12. Git workflow

Every logical repository change is committed separately.

```bash
git status
git diff
git add <files>
git diff --cached
git commit -m "M0: <short description>"
git status
```

The working tree should be clean after each completed subtask.

# Deployment

This document describes how to build and run the AmneziaWG VPN Server stack
with Docker Compose. It is the operational counterpart of
`docs/TECHNICAL_SPEC_v2.0.md`.

## Prerequisites

- Linux host with Docker Engine and the Compose v2 plugin.
- Docker Compose **v2.20 or newer** — `depends_on.condition:
  service_completed_successfully` requires at least v2.20.

```sh
docker compose version
```

## Version policy

`versions.lock` in the repository root is the **single source of pinned
versions**. It must be passed to every Compose invocation with the
`--env-file` flag:

```sh
docker compose --env-file versions.lock build
docker compose --env-file versions.lock up -d
```

Roles:

- `GO_VERSION` / `ALPINE_VERSION` feed the `panel` build args in
  `compose.yaml`. The Compose file guards them with a `:?` expansion, so a
  missing value fails the command instead of silently building with an
  implicit version.
- The remaining pins (`AMNEZIAWG_*`, `SQLITE_DRIVER*`, `AGE_VERSION`,
  `ZSTD_VERSION`) are reserved for later milestones and are not yet consumed
  by Compose.
- No base image or dependency is ever selected via `latest` or a floating
  version.
- Never hardcode versions elsewhere; edit `versions.lock` and commit it.

## Deployment-specific environment

Deployment-specific variables must not be added to `versions.lock`. The
pinned inventory stays in `versions.lock`; host-specific overrides belong in
a local `.env` file, which Compose loads automatically when present.

`.env` is ignored by Git (see `.gitignore`) and does not override the
`--env-file versions.lock` values; `versions.lock` is explicitly passed and
thus wins.

## Services

- **panel-init** — one-shot job (`/app/panel init`): creates the SQLite
  database and generates an initial `config/awg0.conf`, then exits. It owns
  `data/` and `config/` (RW) and does not touch `status/`.
- **panel** — HTTP server (`/app/panel serve`); the only service that
  reads/writes SQLite. It mounts `data/` and `config/` RW and `status/`
  RO.
- **awg** — AmneziaWG runtime; reads `config/` RO, writes `status/`
  (`status.json`) RW. It has no `data/` (SQLite) access. The `awg` image is
  provided by milestone M1; until then the service does not start.

Startup order is handled by `depends_on` with
`condition: service_completed_successfully`: `panel` and `awg` wait for
`panel-init` to complete.

## Security model

- **No Docker socket.** No service mounts `/var/run/docker.sock`, and
  Compose itself does not need it to manage the stack lifecycle.
- **Ownership.** `panel`/`panel-init` are the owners of SQLite and
  `config/awg0.conf`; `awg` can never write `config`.
- **No inter-container HTTP/REST API.** Services exchange state only
  through the mounted volumes (`config/awg0.conf`, `status/status.json`).
- **SQLite is the single source of truth.** Everything under `config/` and
  `status/` is derived state that can be regenerated.

## Ports

No `ports:` mapping is defined in M0. The panel HTTP port will be exposed
in a later milestone (M6), when the web UI lands. Until then the stack is
reachable only through the externally published UDP port configured by
`install.sh`/nftables (M9), and `panel` listening port is not published
outside the default Compose network.

## Workflow

Build and validate the configuration:

```sh
docker compose --env-file versions.lock config --quiet
docker compose --env-file versions.lock build
```

Start the stack:

```sh
docker compose --env-file versions.lock up -d
```

Inspect the result:

```sh
docker compose --env-file versions.lock ps
```

The build tags the built image as `amnezia-vpn-server/panel:2.0.0`; both
`panel-init` and `panel` reference it, so the local build is reused by both
services. When the required components are ready, `docker compose up -d`
starts `panel-init` first and the other services only after it completes
successfully.

There are no Makefiles, shell wrappers, CI pipelines, or other indirections:
the commands above are the only build/run interface.
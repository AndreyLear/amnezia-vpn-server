# AmneziaWG VPN Server

Self-hosted VPN server based on AmneziaWG 2.0+.

## Architecture

- Go web panel
- SQLite as the single source of truth
- AmneziaWG runtime in a separate container
- Docker Compose
- Server-side HTML
- Encrypted backups with age

## Backup and restore (M8)

- `panel backup create` — encrypted snapshot (manifest + SQLite) in the
  configured backups directory, `panel backup list/download` to list and
  fetch, `panel backup restore` to restore from an encrypted archive.
- A restore prepares the database and waits: `panel-init` applies it on
  the next restart (before opening the panel database), regenerates
  `awg0.conf` and re-initializes the tunnel. The pre-restore database is
  kept on disk as a recovery copy.
- Backups are created/restored through the web panel UI as well
  (Backups section), with the same CLI pipeline underneath.

## Install

From the operator machine, one-shot install is `bootstrap.sh` (T-123):
SSH to the VPS, run `install.sh`, create admin, print panel URL +
temporary password. CI flags: `--ip` `--domain` / `--panel-port`
(`--panel-port` defaults to 8443; do not use 8787 — that port is the
loopback panel). `--domain` binds VPN clients to that hostname unless
`--client-domain` overrides.

```sh
./bootstrap.sh --ip <server-ip> --domain panel.example.com
```

`install.sh` remains the on-server installer (Ubuntu/Debian
24.04/22.04, Docker Compose v2.24.2+):

```sh
./install.sh [--root DIR] [--awg-port PORT] [--vpn-subnet CIDR]
             [--domain FQDN] [--client-domain FQDN]
```

The installer creates the deployment layout (data/config/status/backups),
installs the host nftables ruleset and the forward-accept unit, builds and
starts the stack, and keeps the panel loopback-only. Without `bootstrap.sh`,
application init is still:

```sh
docker compose --env-file versions.lock run --rm panel-init \
  /app/panel server init 10.8.0.1/24 51820 --endpoint <public-ip>:51820 --dns 1.1.1.1,8.8.8.8
ssh -L 8787:127.0.0.1:8787 root@<server-ip>
```

With a domain (`--domain`, T-121) the panel also gets HTTPS (nginx +
Let's Encrypt, ports 80/443). `--client-domain FQDN` (defaults to
`--domain`) binds client configs to `<fqdn>:<awg-port>` instead of the
public IP: clients resolve the domain at connect time, so **moving the
server to another host is an A-record change** — clients reconnect
themselves, configs are never re-issued. The installer pre-flights the
DNS record before proceeding. Migration flow: on the new host restore
the database from an age backup (Backup & restore below), then point
the A-record at the new host; on the old host `install.sh` refuses to
run when the record no longer matches (until the domain is re-pointed
back or the new host finishes installing).

To restore an existing database on a fresh install, pass the age identity
(only the `AGE-SECRET-KEY-1...` line) to the documented restore flow — see
`docs/DEPLOYMENT.md` (Backup & restore). The deployment `.env` must carry
`AGE_RECIPIENT` for web/CLI backups (the `panel` and `panel-init` services
load it; `awg` never does).

## Development

Development is performed locally.

Production deployment is performed separately through `install.sh` (see
Install).

## Testing

- Scripted harnesses (no Docker required): `app/panel/test_m123_bootstrap.sh`,
  `app/panel/test_m91_install.sh`,
  `app/panel/test_m92b_compose.sh`, `app/panel/test_m92_network.sh`,
  `app/awg/test_m32.sh`, `app/awg/test_m5.sh` — `test_m5.sh` needs a local Go
  toolchain.
- Live harnesses (Docker daemon required): `app/panel/test_m6_live.sh`,
  `test_m8_live.sh`, `test_m93_live.sh`, `test_m102_env_live.sh`,
  `app/awg/test_m32_live.sh`, `test_m5_live.sh`.
- Live harnesses run the real `compose.yaml` in a temp directory and must
  not collide with a running deployment on the same host: they need the
  loopback port 8787 free (remap with `M6_PORT`/`M8_PORT` where supported)
  and, because `awg` uses `network_mode: host`, a free `awg0` interface.
  On a VPS with a live stack, stop the stack first (`docker compose ...
  stop panel awg`) or use another host.

## Status

**v2.0.0** (tag `v2.0.0`) — production-ready. Milestones M1–M10.2
completed; full regression green (`go test -race`, all scripted and live
harnesses) and verified on a clean VPS: fresh install, external full-tunnel
client, backup/restore DR cycle, reboot survival.

Known accepted risks (future milestones, out of scope for v2.0.0): no TLS
for the panel (loopback + SSH tunnel), no login rate limiting,
TOTP/RBAC/password reset not implemented.
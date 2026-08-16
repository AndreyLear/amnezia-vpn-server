# AmneziaWG VPN Server

Self-hosted VPN server based on AmneziaWG 2.0+.

## Architecture

- Go web panel
- SQLite as the single source of truth
- AmneziaWG runtime in a separate container
- Docker Compose
- Server-side HTML
- Database backups as tar.zst (download / upload in the panel)

## Backup and restore (M8)

- In the panel, **Бэкап** is a dropdown: **Скачать** streams a fresh
  `backup-YYYY-MM-DD.tar.zst` (manifest + SQLite), **Загрузить** restores
  from an uploaded archive. No passwords or age keys.
- `panel backup create` / `panel backup list` write and list archives in
  the backups directory. `panel restore <archive>` prepares a restore
  that `panel-init` applies on the next restart.
- A restore keeps the pre-restore database on disk as a recovery copy.
  The web upload path applies in-process (no restart) when it can.

## Install

From the operator machine, one-shot install is `bootstrap.sh` (T-123):
SSH to the VPS, run `install.sh`, create admin, print panel URL +
temporary password. Panel and VPN endpoints are independent. CI flags:
`--ip`, `--panel-domain` (alias `--domain`), `--vpn-domain` (alias
`--client-domain`), optional `--panel-port` (without a panel domain,
CI defaults to 8443; do not use 8787 — that port is the loopback
panel). `--panel-domain` does **not** bind VPN clients; only
`--vpn-domain` / `--client-domain` does. Restore is an upload in the
panel (Backups) and does not change the panel user (T-155).

```sh
./bootstrap.sh --ip HOST --panel-domain panel.example.com --vpn-domain example.com
```

Optional panel TLS port (Let's Encrypt still uses TCP 80 for HTTP-01):

```sh
./bootstrap.sh --ip HOST --panel-domain panel.example.com --vpn-domain example.com --panel-port 8443
```

`install.sh` remains the on-server installer (Ubuntu/Debian
24.04/22.04, Docker Compose v2.24.2+):

```sh
./install.sh [--root DIR] [--awg-port PORT] [--vpn-subnet CIDR]
             [--panel-domain FQDN] [--vpn-domain FQDN] [--panel-port PORT]
```

The installer creates the deployment layout (data/config/status/backups),
installs the host nftables ruleset and the forward-accept unit, builds and
starts the stack, and keeps the panel loopback-only behind nginx when a
domain or `--panel-port` is set. Without `bootstrap.sh`,
application init is still:

```sh
docker compose --env-file versions.lock run --rm panel-init \
  /app/panel server init 10.8.0.1/24 443 --endpoint <public-ip>:443 --dns 1.1.1.1,8.8.8.8
ssh -L 8787:127.0.0.1:8787 root@<server-ip>
```

With a panel domain (`--panel-domain` / `--domain`, T-121) the panel
gets HTTPS (nginx + Let's Encrypt). HTTP-01 stays on TCP 80. TLS
listens on 443 unless `--panel-port` is set (`https://PANEL_DOMAIN:PORT`).
`--vpn-domain` / `--client-domain` binds client configs to
`<fqdn>:<awg-port>` instead of the public IP: clients resolve the domain
at connect time, so **moving the server to another host is an A-record
change** — clients reconnect themselves, configs are never re-issued.
The installer pre-flights the DNS record before proceeding. Migration
flow: on the new host restore the database from a tar.zst backup
(Backup and restore above), then point the A-record at the new host; on
the old host `install.sh` refuses to run when the record no longer
matches (until the domain is re-pointed back or the new host finishes
installing).

To restore an existing database after install, upload the archive in the
panel (Backups). That restore does not change the panel user. The CLI
`panel restore <archive>` path remains for operators without the web UI.

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
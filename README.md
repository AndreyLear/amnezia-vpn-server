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

## Development

Development is performed locally.

Production deployment is performed separately through `install.sh`.

## Testing

- Scripted harnesses (no Docker required): `app/panel/test_m91_install.sh`,
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

Project is currently in M0 — repository bootstrap.
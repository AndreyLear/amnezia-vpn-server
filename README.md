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

## Status

Project is currently in M0 — repository bootstrap.
# AmneziaWG VPN Server

Self-hosted VPN server based on AmneziaWG 2.0+.

## Architecture

- Go web panel
- SQLite as the single source of truth
- AmneziaWG runtime in a separate container
- Docker Compose
- Server-side HTML
- Encrypted backups with age

## Development

Development is performed locally.

Production deployment is performed separately through `install.sh`.

## Status

Project is currently in M0 — repository bootstrap.
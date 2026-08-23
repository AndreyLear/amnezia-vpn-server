# AmneziaWG VPN Server

Self-hosted VPN server based on AmneziaWG 2.0+. Released under the MIT
License (see `LICENSE`).

Русская версия: [README.ru.md](README.ru.md).

Operator security notes: [SECURITY.md](SECURITY.md).

## Architecture

- Go web panel
- SQLite as the single source of truth
- AmneziaWG runtime in a separate container
- Docker Compose
- Embedded React SPA (`go:embed` `dist`)
- Database backups as tar.zst (download / upload in the panel)
- Client configs route `::/0` into the tunnel as well. The interface has no
  IPv6 address, so v6 is blackholed on the client and dual-stack services
  (YouTube, Instagram) go over IPv4 through the VPN instead of bypassing it.
## Backup and restore (M8)

- In the panel, **Бэкап** is a dropdown: **Скачать** streams a fresh
  `backup-YYYY-MM-DD.tar.zst` (manifest + SQLite), **Загрузить** restores
  from an uploaded archive. No passwords or age keys.
- `panel backup create` / `panel backup list` write and list archives in
  the backups directory. `panel restore <archive>` prepares a restore
  that `panel-init` applies on the next restart.
- Settings that belong to one machine (the address baked into client
  configs, and the MTU) are not carried over silently when the archive
  comes from another server: the panel shows both values and asks.
- A restore keeps the pre-restore database on disk as a recovery copy.
  The web upload path applies in-process (no restart) when it can.

## Install

From the operator machine, run the Russian wizard. Enter accepts the
default in brackets. Password SSH needs `sshpass` or `expect` on the
operator host (`brew install sshpass` / `apt install sshpass`).

```sh
./bootstrap.sh
```

The wizard asks for server IP, SSH user (`[root]`), password or key
(password is the default; a key in `~/.ssh` is not used unless you
answer `ключ` or `2`), panel domain (empty = panel on the server IP,
then panel port `[8443]`), panel port `[443]` when a domain is set
(empty = TLS on 443), and VPN client domain (empty = public IP — never
copied from the panel hostname). AmneziaWG stays on UDP 443 and the
deploy root is `/opt/amnezia-vpn`.

The same wizard can be fetched with:

```sh
curl -fsSL https://raw.githubusercontent.com/AndreyLear/amnezia-vpn-server/main/bootstrap.sh -o bootstrap.sh && bash bootstrap.sh
```

CI / flags: `bootstrap.sh` SSHes to the VPS, runs `install.sh`, creates
admin, prints panel URL + temporary password. Panel and VPN endpoints
are independent. Flags: `--ip`, `--panel-domain` (alias `--domain`),
`--vpn-domain` (alias `--client-domain`), optional `--panel-port`
(without a panel domain, CI defaults to 8443; do not use 8787 — that
port is the loopback panel). `--panel-domain` does **not** bind VPN
clients; only `--vpn-domain` / `--client-domain` does. Restore is an
upload in the panel (Backups) and does not change the panel user (T-155).

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
             [--build]
```

The stack images are pulled from GHCR
(`ghcr.io/andreylear/amnezia-vpn-server`) at the version pinned in
`versions.lock`, so a fresh install downloads ~60 MB instead of compiling
amneziawg-go, amneziawg-tools and the panel on the VPS. `--build` compiles
them locally instead — that is the development path, and the installer
falls back to it on its own when the registry cannot be reached.

The installer creates the deployment layout (data/config/status/backups),
installs the host nftables ruleset and the forward-accept unit, pulls the
published images and starts the stack, and keeps the panel loopback-only behind nginx when a
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
at connect time. The installer pre-flights the DNS record before
proceeding. On the old host `install.sh` refuses to run when the record
no longer matches (until the domain is re-pointed back or the new host
finishes installing).

To restore an existing database after install, upload the archive in the
panel (Backups). That restore does not change the panel user. The CLI
`panel restore <archive>` path remains for operators without the web UI.
Moving the whole deployment to a new VPS is **Move to another VPS**.

## Move to another VPS

Checklist (test this on a spare host before you need it):

1. While the **old** panel is still reachable, download a backup
   `tar.zst` from Backups.
2. From a laptop, run `./bootstrap.sh` (wizard) or the same flags onto
   the **new** IP. Set `--panel-domain` / `--vpn-domain` as they are
   today.
3. Log into the **new** panel with the temporary password printed by
   bootstrap.
4. Upload the archive under Backups. The panel user and password are
   **not** replaced (T-155).
5. The panel notices the archive came from another server and asks which
   address to use:
   - **Keep the backup's address** — for clients that connect by domain
     (`--vpn-domain`). Repoint that domain's A record at the new IP and
     clients reconnect on their own; existing configs keep working, since
     the server and client keys travel with the archive.
   - **Use this server's address** — for clients that connected to the old
     IP. Their configs have to be reissued: download them again in the
     panel and hand them out.
6. The tunnel MTU is not carried over: the installer measures the new
   uplink and pins its own value.

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

**1.0** — install via `./bootstrap.sh` (wizard) or flags, then
`install.sh` on the VPS. License: MIT (`LICENSE`). Compose image tags
and backup `application_version` stay **2.0.0** (stack generation);
the public product label is 1.0.

In 1.0:

- Panel TLS for users: nginx + Let's Encrypt when `--panel-domain` is
  set; without a domain, HTTPS on `--panel-port` (CI default 8443,
  self-signed). Loopback `8787` stays the compose panel port.
- Login rate limiting (T-105).
- Password-only panel login (username + password). TOTP is not a 1.0
  panel feature.
- CLI `panel auth set-password` (password reset). Restore (panel
  upload / `panel restore`) does not replace the panel user.
- Independent panel vs VPN hostnames (`--panel-domain` /
  `--vpn-domain`).

Out of 1.0: T-104 in-process panel TLS (TLS in the Go process instead
of nginx) and T-106 RBAC only.

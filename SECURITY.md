# Security

This note is for a **single-operator VPS**: one trusted admin, one host,
no multi-tenant panel. Compromise of the host, SSH, the panel account,
or a backup archive is compromise of the VPN.

## Threat model

Assume the operator machine and the VPS root account are trusted. The
stack does not defend against a hostile co-admin, a shared panel, or a
compromised Docker host. Attackers on the network can probe public
ports (AmneziaWG UDP, HTTPS for the panel when TLS is enabled, and
TCP 80 for Let's Encrypt HTTP-01). The Go panel process itself is not
published on the public interface.

## Panel

Docker Compose binds the panel to **127.0.0.1:8787** only
(`127.0.0.1:8787:8787` in `compose.yaml`). Do not publish 8787 on
`0.0.0.0`.

Public HTTPS is **nginx + Let's Encrypt** when `--panel-domain` is set.
Without a domain, `--panel-port` terminates TLS on the host (self-signed)
and still proxies to loopback 8787. Loopback-only mode (no domain, no
`--panel-port`) is intended for an SSH tunnel:
`ssh -L 8787:127.0.0.1:8787`.

## Panel login

Fresh `bootstrap.sh` creates a **password-only** admin. TOTP is not a
1.0 panel feature: login is username + password only.

Failed POSTs to login are rate-limited (**T-105**): **5 failures / 15
minutes per client IP**, then **HTTP 429**. The limiter is in-memory; a
panel restart clears it. nginx `X-Real-IP` is used as the key when it
is a single valid IP.

## Restore and panel auth (T-155)

Restore from the panel (**Загрузить**) or `panel restore` applies the
SQLite backup. It does **not** replace the live panel username or password.
After a move to a new VPS, keep the credentials printed by
`bootstrap.sh` (or set later with CLI) — not the user from the archive.

Password reset in 1.0 is CLI only: `panel auth set-password`. There is
no in-panel password reset and no RBAC.

## Backups

Backups are **unencrypted `tar.zst`**: a JSON manifest plus the SQLite
database (peer keys, configs, panel auth hashes). Treat every archive
like a secret. Store it only on the operator machine or the VPS
`backups/` directory. Do **not** email, chat, or ticket-attach backups.

## SSH

`bootstrap.sh` defaults to **password** SSH. That path needs `sshpass`
or `expect` on the operator host. A key in `~/.ssh` is **opt-in**
(wizard: `ключ` / `2`, or `--key`). Prefer a key for ongoing access;
password is a first-install convenience.

## Host firewall (nftables)

`install.sh` installs a managed **nftables** fragment (NAT/forward for
the VPN subnet; UDP accept for **AmneziaWG**, default **UDP 443**). It
does not flush unrelated host rules.

Extra TCP accepts depend on panel TLS mode:

- **`--panel-domain` (Let's Encrypt):** TCP 80 for ACME **HTTP-01**, plus
  TCP 443 (or `--panel-port` if set) for panel TLS. Port 80 must stay
  reachable from the internet for issuance and renewal.
- **IP:port mode (no domain, `--panel-port` set):** only
  `tcp dport $PANEL_PORT` (wizard empty domain defaults to 8443). No
  HTTP-01 / TCP 80.
- **Loopback-only (no domain and no `--panel-port`):** no extra panel
  TCP.

The compose panel port 8787 is never opened in nftables.

## Out of 1.0

Not in this release:

- **T-104** — TLS inside the Go panel process (instead of nginx).
- **T-106** — RBAC only (`panel auth set-password` already exists).

## Product vs stack version

Docker Compose image tags (`amnezia-vpn-server/panel:2.0.0`,
`amnezia-vpn-server/awg:2.0.0`) and backup `application_version` stay
**2.0.0** (stack generation); the public product label is **1.0**. Do
not retag live compose images.

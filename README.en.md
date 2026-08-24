# AmneziaWG VPN Server

Your own VPN on your own server: one command in the terminal and a few minutes
later you have a working VPN with a web panel. Clients are added in a couple of
clicks and connect by scanning a QR code.

Built on AmneziaWG: the traffic looks ordinary, so it gets through where plain
VPN protocols are blocked.

Русская версия: [README.md](README.md) · Security: [SECURITY.md](SECURITY.md)
· Development: [DEVELOPMENT.md](DEVELOPMENT.md)

## What you need

**A server** with root access over SSH:

| | Minimum | Note |
| --- | --- | --- |
| System | Ubuntu 24.04 | recommended; Ubuntu 22.04 and Debian 12 are accepted too |
| CPU | 1 core | measured on one core: 3–5 minutes to install, up to 45 MB/s throughput |
| Memory | 1 GB, 2 GB is better | 2 GB leaves room as the client count grows |
| Disk | 10 GB | images and the database take under 1 GB |
| Ports | UDP 4500, TCP 443 or 8443 | plus TCP 80 if you want a Let's Encrypt certificate |

Such a server costs 3–5 dollars a month at most hosting providers. Watch the
traffic allowance: a VPN uses as much as its users do.

**Which system to pick.** Use Ubuntu 24.04 — that is what this is tested on.
AmneziaWG is installed from `ppa:amnezia/ppa`, an Ubuntu mechanism, and it
brings a kernel module: the encryption runs in the kernel rather than in a
separate process, which is where the throughput comes from. Ubuntu 22.04 works
the same way.

Debian 12 is accepted but untested here: the PPA is not native to it, and the
module may fail to rebuild after a kernel upgrade, which stops the tunnel from
coming up. Anything else — Rocky, Alma, Fedora, Arch, Alpine — is refused
outright.

There is nothing to gain from another distribution. Throughput comes from the
host's uplink and core count, not from the OS.

**A domain** is optional. Without one everything works over the server's IP
address. With one you get a real certificate instead of a self-signed one, and
you can move to another server without touching anyone's settings.

**Your computer** — a Mac or Linux machine with a terminal. Install `sshpass`
if you will log into the server with a password:

```sh
brew install sshpass          # macOS
sudo apt install sshpass      # Ubuntu, Debian
```

**Time** — about five minutes.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/AndreyLear/amnezia-vpn-server/main/bootstrap.sh -o bootstrap.sh
bash bootstrap.sh
```

The wizard asks for the server address, a password or key, and a domain. Enter
accepts the value in brackets — when in doubt, press Enter.

You can pass everything up front instead:

```sh
./bootstrap.sh --ip 203.0.113.10 --vpn-domain example.com
```

It then walks through nine steps and prints the panel address, the login, a
temporary password and the certificate fingerprint. **Keep that** — the
password is stored nowhere else.

## After the install

Open the panel at the address from the summary. Without a domain your browser
will warn about the self-signed certificate: compare the fingerprint with the
one printed and continue.

Change the password (see below), then add your first client.

## The panel

**The header** shows how the server is doing: CPU, memory and disk usage, plus
the state of the tunnel. **The body** is one card per client: name, assigned
address, whether it is enabled, when it last connected and how much traffic it
has moved. **The Backup button** downloads or uploads the file with everything
you have configured.

The panel opens at the same address whether the VPN is on or off. The server
runs its own resolver inside the tunnel: for connected devices it answers the
panel's hostname with the panel's in-tunnel address. The certificate stays
valid — the name does not change.

## Clients

A client is one device: a phone, a laptop, a router. Each has its own keys, so
disabling one leaves the others alone.

Add one by pressing the add button and typing a name you will recognise. The
panel assigns an address and creates the keys.

- **QR code** — for phones. Client menu → QR code, scan it in the app
- **Config file** — for computers and routers. Client menu → download, then
  import the file. It is generated on download and stored nowhere

You can also disable a client (its settings stay valid but stop working),
enable it again, delete it for good, or leave yourself a note in its
description. Addresses come from `10.8.0.0/24`, so 253 devices — you will run
out of bandwidth long before that.

## Apps

The server speaks AmneziaWG, so any client that understands the protocol works.

**AmneziaWG** — the app for the protocol itself:

- iOS — [App Store](https://apps.apple.com/app/amneziawg/id6478942365)
- Android — [Google Play](https://play.google.com/store/apps/details?id=org.amnezia.awg)
- Windows — [GitHub releases](https://github.com/amnezia-vpn/amneziawg-windows-client/releases)

**AmneziaVPN** — Amnezia's general client, several protocols at once, for
Windows, macOS, Linux, Android and iOS: [amnezia.org/downloads](https://amnezia.org/downloads)

**Routers.** OpenWrt with the `amneziawg` package works: set the router up as
an ordinary client and the whole household goes through the VPN.

## Common tasks

### Changing the panel password

There is deliberately no form for this in the panel, so the password cannot be
guessed through a browser:

```sh
ssh root@203.0.113.10 "cd /opt/amnezia-vpn && docker compose --env-file versions.lock \
  run --rm -T panel-init /app/panel auth set-password admin --password-stdin"
```

The command asks for the new password and does not echo it. Forgetting the old
one does not matter — it is not asked for.

### Backups

A backup is a single file holding everything you configured: the clients, their
keys and the server's own keys. Restoring it brings all of that back, and the
settings you handed out keep working, because the server keys are inside too.

Download it in the panel: **Backup** → **Download**. Do that after adding or
removing clients, and before reinstalling or changing hosts. Keep the file
anywhere except the server itself.

To restore: **Backup** → **Upload**. Clients come back immediately, no restart.
The panel password stays the one currently in effect.

### Moving to another server

1. While the old panel is up, download a backup
2. Install on the new server with the same wizard
3. Upload the backup in the new panel
4. The panel notices the backup came from another server and asks which address
   to use:
   - **keep the backup's address** — for users connecting by domain. Repoint
     the domain's A record and everyone reconnects on their own
   - **use this server's address** — for users who connected by IP. Hand them
     new settings from the panel

### Updating

```sh
ssh root@203.0.113.10 "cd /opt/amnezia-vpn && docker compose --env-file versions.lock pull \
  && docker compose --env-file versions.lock up -d"
```

Under a minute, and clients are not disturbed.

### Uninstalling

```sh
ssh root@203.0.113.10 "bash /opt/amnezia-vpn/cleanup.sh"
```

It first prints what it would remove and changes nothing. Add `--yes` to go
ahead. Only what the installer created is removed.

### When something is wrong

**The panel stopped opening after a reinstall.** That was the behaviour
before 2.3.0: running the installer without the domain flag returned the
panel to loopback. Settings are remembered now, so a plain rerun changes
nothing. On an older version, pass the domain again.

**Pages load but video does not.** Almost always the MTU: small packets get
through while the large ones carrying video are dropped on the way. The
installer picks a size that survives mobile networks too, so a reinstall
usually settles it. If it does not, lower the MTU in the client app itself —
1280 instead of 1340.

**A client stopped connecting after a move.** Check where the domain points:
`dig +short example.com`.

**The panel does not open.** Check the services: `ssh root@203.0.113.10 "docker ps"`.
`amnezia-vpn-panel-1` and `amnezia-vpn-awg-1` should both be up.

## How it works

Four containers: the panel (which owns the SQLite database), AmneziaWG, a
resolver for connected clients, and a one-shot init job. The panel listens on
the server's loopback only; nginx serves it to the outside with a
certificate.

SQLite is the single source of truth — everything else is rebuilt from it,
which is why a backup of the database restores both clients and server keys.

IPv6 is disabled on the client: otherwise YouTube, Instagram and other
dual-stack sites would go around the tunnel instead of through it.

The tunnel runs on UDP 4500. Port 443 would look like the convenient
choice — it is open almost everywhere — but it is the QUIC port, and
mobile carriers throttle it to slow video down. A tunnel sitting there
inherits the whole limit: measured on a live deployment, the same tunnel
ran at 20 Mbit/s on UDP 443 and 45 Mbit/s on 4500, against 40 with no
tunnel at all. Port 4500 belongs to IPsec, which corporate VPNs use, so
carriers let it through.

The in-tunnel resolver exists because the panel lives at the same address the
tunnel ends at. A packet sent to that address never enters the tunnel, and iOS
creates no bypass route for it — so without answering the hostname differently,
the panel would be unreachable from exactly the devices connected to it.
Clients get two resolvers, ours and a public fallback, so stopping the service
never leaves a client without name resolution.

More in [DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT, see [LICENSE](LICENSE)

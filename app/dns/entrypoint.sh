#!/bin/sh
# Renders the dnsmasq configuration from the deployment environment and
# runs it in the foreground.
#
#   PANEL_DOMAIN   the hostname to answer with TUNNEL_ADDRESS (optional:
#                  without it the service only forwards)
#   TUNNEL_DNS_DISABLED=1
#                  stand down: bind nothing and leave port 53 to whatever
#                  else the host runs there (install.sh --no-tunnel-dns)
#   TUNNEL_ADDRESS the server's address inside the tunnel (10.8.0.1)
#   TUNNEL_ADDRESS6
#                  the server's IPv6 address inside the tunnel, as a CIDR
#                  (fd..::1/64). Empty means the tunnel carries IPv4 only,
#                  which is what every deployment did before
#                  amnezia-vpn-server-mhea.
#   UPSTREAM_DNS   comma-separated resolvers for everything else
set -eu
# Alpine's ash supports pipefail, and the upstream list below is built
# through a pipeline whose failures would otherwise pass unnoticed.
set -o pipefail

# Standing down has to be a real state, not a comment: --no-tunnel-dns
# exists because something else on the host already owns port 53, and a
# resolver that kept binding would restart-loop against it. Exiting is not
# an option either — the service restarts unless-stopped — so it idles.
if [ "${TUNNEL_DNS_DISABLED:-0}" = "1" ]; then
    echo "[dns] disabled by --no-tunnel-dns: not binding port 53"
    exec sleep infinity
fi

TUNNEL_ADDRESS="${TUNNEL_ADDRESS:-10.8.0.1}"
# The prefix length is for the interface, not for a listen address.
TUNNEL_ADDRESS6="${TUNNEL_ADDRESS6:-}"
TUNNEL_ADDRESS6="${TUNNEL_ADDRESS6%%/*}"
UPSTREAM_DNS="${UPSTREAM_DNS:-1.1.1.1,8.8.8.8}"
CONF=/tmp/dnsmasq.conf

{
    # Only inside the tunnel: never answer queries arriving from the
    # internet, which would make this an open resolver.
    printf 'listen-address=%s\n' "$TUNNEL_ADDRESS"
    # Listening on the tunnel's IPv6 address as well: a client whose
    # config names this resolver reaches it over whichever family it
    # happens to prefer, and with IPv6 in the tunnel that is often IPv6.
    # Still not an open resolver — these are addresses inside the tunnel,
    # and bind-dynamic never binds the wildcard.
    if [ -n "$TUNNEL_ADDRESS6" ]; then
        printf 'listen-address=%s\n' "$TUNNEL_ADDRESS6"
    fi
    # bind-dynamic, not bind-interfaces: awg0 does not exist yet when the
    # stack starts, and a hard bind would make dnsmasq exit. bind-dynamic
    # binds the address as soon as the interface appears and never binds
    # the wildcard, so this never becomes an open resolver.
    printf 'bind-dynamic\n'
    printf 'no-resolv\n'
    printf 'no-hosts\n'
    printf 'domain-needed\n'
    printf 'bogus-priv\n'
    printf 'cache-size=1000\n'
    # When the tunnel carries IPv4 only, an AAAA answer points the client
    # at an address nothing can reach: ::/0 sits in its AllowedIPs to stop
    # IPv6 escaping around the VPN (amnezia-vpn-server-2064), so the
    # packets go into the tunnel and die there.
    #
    # An owner saw this as YouTube loading its page while every video card
    # came back "no internet connection": opening the feed fires dozens of
    # parallel requests to i.ytimg.com, yt3.ggpht.com and the API, and
    # every one of them is offered an address that leads nowhere first.
    # Dropping ::/0 from the client cured it and reopened the leak it was
    # added to close, so the answer was to stop offering the addresses
    # (amnezia-vpn-server-3jbg).
    #
    # Once the tunnel does carry IPv6 the filter turns from a shield into
    # a blindfold: it would hide half of a working internet from the
    # client. So it is tied to the same deployment value as everything
    # else about IPv6 (amnezia-vpn-server-ypxl).
    if [ -z "$TUNNEL_ADDRESS6" ]; then
        printf 'filter-AAAA\n'
    fi
    printf '%s\n' "$UPSTREAM_DNS" | tr ',' '\n' | while read -r server; do
        [ -n "$server" ] && printf 'server=%s\n' "$server"
    done
    if [ -n "${PANEL_DOMAIN:-}" ]; then
        printf 'address=/%s/%s\n' "$PANEL_DOMAIN" "$TUNNEL_ADDRESS"
    fi
} > "$CONF"

echo "[dns] serving on ${TUNNEL_ADDRESS}:53"
if [ -n "${PANEL_DOMAIN:-}" ]; then
    echo "[dns] ${PANEL_DOMAIN} -> ${TUNNEL_ADDRESS}"
fi
echo "[dns] forwarding everything else to ${UPSTREAM_DNS}"

exec dnsmasq --keep-in-foreground --log-facility=- --conf-file="$CONF"

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
UPSTREAM_DNS="${UPSTREAM_DNS:-1.1.1.1,8.8.8.8}"
CONF=/tmp/dnsmasq.conf

{
    # Only inside the tunnel: never answer queries arriving from the
    # internet, which would make this an open resolver.
    printf 'listen-address=%s\n' "$TUNNEL_ADDRESS"
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
    # The tunnel carries IPv4 only: awg0 has no IPv6 address, forwarding for
    # it is off, and the server accepts nothing but the peer's IPv4. Handing
    # a client AAAA records therefore points it at addresses nothing can
    # reach — and the client dutifully routes them into the tunnel, because
    # ::/0 sits in AllowedIPs to stop those packets escaping around the VPN.
    #
    # An owner saw this as YouTube loading its page while every video card
    # came back "no internet connection": opening the feed fires dozens of
    # parallel requests to i.ytimg.com, yt3.ggpht.com and the API, and every
    # one of them is offered an address that leads nowhere first. Dropping
    # ::/0 from the client cured it and reopened the leak it was added to
    # close, so the answer is to stop offering the addresses instead.
    #
    # Remove this line when the tunnel gains real IPv6; until then an AAAA
    # answer can only cost time.
    printf 'filter-AAAA\n'
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

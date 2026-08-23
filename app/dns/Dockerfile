# Split DNS for VPN clients: answers the panel's own domain with the
# server's address inside the tunnel, forwards everything else.
#
# Why this exists: the panel's public address is also the tunnel endpoint,
# and a client cannot send a packet to the endpoint through the tunnel
# itself. On iOS that packet is simply dropped, so the panel became
# unreachable from a device connected to its own VPN. Answering the same
# hostname with the in-tunnel address keeps one URL working both ways.
ARG ALPINE_VERSION
FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache dnsmasq

# dnsmasq drops privileges to this user itself; the container starts as
# root only to bind port 53.
COPY app/dns/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]

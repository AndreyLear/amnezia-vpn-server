// Package mailer delivers the operator's notification mail
// (amnezia-vpn-server-hxgr, second step of dfs2).
//
// It is used by cmd/awgmail, a host service run by a systemd timer once a
// minute next to the watchdog. Not the panel: the panel does not reach the
// outside world on purpose. Not curl: the password would end up in the
// command line and in ps.
//
// Three parts:
//
//   - Compose builds the RFC 5322 message. No header value can carry a
//     line break — addresses with one are refused, the subject is folded
//     to one line and encoded — so nothing can smuggle in another header.
//   - Sender talks SMTP to the operator's relay. Encryption is chosen by
//     the port, not by a switch: 465 is TLS from the first byte, anything
//     else must offer STARTTLS. A server that does not is refused before
//     the password is sent — the password never crosses the wire in clear.
//   - State is the outbox. It holds at most one pending message per key:
//     a newer message for the same key replaces the older one, so what goes
//     out is the current state of things, not yesterday's. A stale letter
//     about a trouble that has already ended is worse than silence. A failed
//     send is retried after 1, 5 and 15 minutes, then the refusal is
//     remembered for the panel to show.
//
// The password never appears in an error, a log line or the state file:
// every error that could have seen it goes through redact.
package mailer

// Package notify decides what to write to the operator about, and when
// (amnezia-vpn-server-0d2n, third step of dfs2). It sends nothing itself:
// Evaluate turns one observation of the server into letters, and
// cmd/awgmail puts them into the outbox (internal/mailer).
//
// The list is short on purpose — a long one gets switched off:
//
//   - the tunnel service is not working for 5 minutes;
//   - every client dropped at once and stayed away for 5 minutes;
//   - the watchdog restarted a service;
//   - an update failed (rolled back, or left the server needing a hand);
//   - an update was installed;
//   - a new version is available.
//
// Every trouble gets a letter when it ends, too: without one the operator
// cannot tell whether it is over.
//
// One letter per change of state, never one per tick. The rules remember
// what they last told (State.*.Told), so a tunnel that is still down does
// not produce a letter every minute, and a letter that could not be sent
// while the group was muted still goes out once the mute ends, if the
// state it describes is still true.
//
// Flapping is its own diagnosis. More than three changes within an hour in
// one group give one letter «… мигает» and an hour of silence for that
// group: the flapping matters more than each separate drop.
//
// On the first run (State.Initialized false) the rules only take a
// baseline: a restart, an update outcome or a release that happened before
// mail was switched on is not news to write about.
package notify

import (
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

const (
	// DownThreshold is how long a trouble must last before a letter: a
	// short blip is not worth waking anyone.
	DownThreshold = 5 * time.Minute
	// OnlineSilence is the rx silence after which a client counts as gone —
	// the same sign the panel uses (status.SpeedOfflineSilence, tyic).
	OnlineSilence = status.SpeedOfflineSilence
	// CollapseTolerance is «within one tick»: awg samples every 5 seconds
	// and stamps whole seconds, so two peers cut at the same instant can
	// land in adjacent samples a little more than 5 seconds apart.
	CollapseTolerance = 7 * time.Second
	// FlapWindow and FlapLimit: more than FlapLimit changes in FlapWindow is
	// flapping; the group is then muted for FlapWindow.
	FlapWindow = time.Hour
	FlapLimit  = 3
)

// Letter is one message to queue under a key; a newer letter with the same
// key replaces an unsent older one in the outbox.
type Letter struct {
	Key     string
	Message mailer.Message
}

// Inputs is one observation of the server.
type Inputs struct {
	Now time.Time
	// Server is how the letter names the server: the client domain, or the
	// IP when there is none.
	Server string
	// Zone is the time zone the letters speak in; nil means UTC.
	Zone *time.Location

	// TunnelUp: status.json is fresh and the interface exists.
	TunnelUp bool
	// Services is the watchdog's snapshot; nil when there is none.
	Services *status.Services
	// Update is the update agent's last state; nil when there is none.
	Update *status.UpdateState
	// Installed and Latest versions; Latest is "" when unknown.
	Installed, Latest string
	// Clients is rx activity over the last minutes; nil when the speed log
	// could not be read, in which case the client rule keeps its state.
	Clients *status.RxActivity
}

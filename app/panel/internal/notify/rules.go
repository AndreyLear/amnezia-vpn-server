package notify

import (
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Groups muted together when they flap. A dropped tunnel and every client
// dropping at once are the same news from the operator's side.
const (
	groupTunnel = "tunnel"
	groupDNS    = "dns"
)

// Outbox keys. One pending letter per key: a newer one replaces it.
const (
	keyTunnel  = "tunnel"
	keyClients = "clients"
	keyUpdate  = "update"
	keyRelease = "release"
)

func keyRestart(service string) string { return "restart-" + service }
func keyFlap(group string) string      { return "flap-" + group }

func groupOf(service string) string {
	if service == "dns" {
		return groupDNS
	}
	return groupTunnel
}

// clientsLogStale: a speed log without a sample this recent says nothing
// about the clients — the tunnel rule speaks for that case.
const clientsLogStale = 30 * time.Second

type eval struct {
	in      Inputs
	st      *State
	now     time.Time
	letters []Letter
	// tunnelBackNow: this run wrote that the tunnel is back, which already
	// covers a restart of awg that brought it back.
	tunnelBackNow bool
}

// Evaluate updates st from one observation and returns the letters to
// queue, in the order they should be read.
func Evaluate(in Inputs, st *State) []Letter {
	if in.Zone == nil {
		in.Zone = time.UTC
	}
	if st.Restarts == nil {
		st.Restarts = map[string]Restart{}
	}
	if st.Flaps == nil {
		st.Flaps = map[string]Flap{}
	}
	e := &eval{in: in, st: st, now: in.Now.UTC()}
	first := !st.Initialized

	e.observeTunnel()
	e.observeClients()
	e.observeRestarts(first)
	e.checkFlaps()

	e.tellTrouble(&st.Tunnel, groupTunnel, keyTunnel, e.tunnelDown, e.tunnelUp)
	e.tellTrouble(&st.Clients, groupTunnel, keyClients, e.clientsDown, e.clientsUp)
	e.tellRestarts()
	e.tellUpdate(first)
	e.tellRelease(first)

	st.Initialized = true
	return e.letters
}

func (e *eval) emit(key string, msg mailer.Message) {
	e.letters = append(e.letters, Letter{Key: key, Message: msg})
}

func (e *eval) change(group string) {
	f := e.st.Flaps[group]
	f.Changes = append(f.Changes, e.now)
	e.st.Flaps[group] = f
}

func (e *eval) muted(group string) bool {
	f := e.st.Flaps[group]
	return f.MutedUntil != nil && e.now.Before(*f.MutedUntil)
}

func (e *eval) observeTunnel() {
	t := &e.st.Tunnel
	switch {
	case !e.in.TunnelUp && t.Since == nil:
		at := e.now
		t.Since = &at
		e.change(groupTunnel)
	case e.in.TunnelUp && t.Since != nil:
		t.Since = nil
		at := e.now
		e.st.TunnelBackAt = &at
		e.change(groupTunnel)
	}
}

// observeClients applies the one narrow client rule: two or more clients
// were on the line and all of them fell silent within one tick. Two devices
// do not fall asleep within seconds of each other; a cut drops everyone at
// once. With a single client the rule never fires — «nobody uses it» and
// «nobody can reach it» look the same from the server, and a guess is worse
// than silence.
func (e *eval) observeClients() {
	c := &e.st.Clients
	a := e.in.Clients
	if a == nil {
		return
	}
	// While the tunnel itself is down the clients are gone for that reason,
	// and the tunnel letter says so.
	if e.st.Tunnel.Since != nil || a.LastSample.IsZero() || e.now.Sub(a.LastSample) > clientsLogStale {
		return
	}
	var latest time.Time
	for _, at := range a.LastMove {
		if e.now.Sub(at) <= OnlineSilence {
			if c.Since != nil {
				c.Since = nil
				e.change(groupTunnel)
			}
			return
		}
		if at.After(latest) {
			latest = at
		}
	}
	if latest.IsZero() {
		return
	}
	online, together := 0, true
	for _, at := range a.LastMove {
		if latest.Sub(at) <= OnlineSilence {
			online++
			if latest.Sub(at) > CollapseTolerance {
				together = false
			}
		}
	}
	if online < 2 || !together {
		return
	}
	if back := e.st.TunnelBackAt; back != nil && !latest.After(*back) {
		return
	}
	if c.Since == nil {
		e.change(groupTunnel)
	}
	c.Since = &latest
}

func (e *eval) observeRestarts(first bool) {
	if e.in.Services == nil {
		return
	}
	for _, svc := range e.in.Services.Services {
		if svc.RestartedAtUTC == "" {
			continue
		}
		r := e.st.Restarts[svc.Name]
		if svc.RestartedAtUTC == r.Seen {
			continue
		}
		r.Seen = svc.RestartedAtUTC
		at, err := time.Parse(time.RFC3339, svc.RestartedAtUTC)
		if !first && err == nil {
			r.Pending = &at
			r.Reason = svc.RestartReason
			// A restart of awg is part of a tunnel drop already counted by
			// observeTunnel; counting it again would call one episode flapping.
			if groupOf(svc.Name) == groupDNS {
				e.change(groupDNS)
			}
		}
		e.st.Restarts[svc.Name] = r
	}
}

func (e *eval) checkFlaps() {
	for group, f := range e.st.Flaps {
		kept := f.Changes[:0]
		for _, at := range f.Changes {
			if e.now.Sub(at) < FlapWindow {
				kept = append(kept, at)
			}
		}
		f.Changes = kept
		if f.MutedUntil != nil && !e.now.Before(*f.MutedUntil) {
			f.MutedUntil = nil
		}
		if f.MutedUntil == nil && len(f.Changes) > FlapLimit {
			e.emit(keyFlap(group), e.flapping(group, len(f.Changes)))
			until := e.now.Add(FlapWindow)
			f.MutedUntil = &until
			f.Changes = nil
		}
		e.st.Flaps[group] = f
	}
}

// tellTrouble writes when a trouble has lasted DownThreshold, and again
// when it ends — but only about an occurrence it has written about. While
// the group is muted nothing is written and Told stays as it was, so the
// letter that is still true goes out once the mute ends.
func (e *eval) tellTrouble(t *Trouble, group, key string,
	down func(since time.Time) mailer.Message, up func(since time.Time) mailer.Message) {
	if e.muted(group) {
		return
	}
	switch {
	case t.Since != nil && t.Told != toldDown && e.now.Sub(*t.Since) >= DownThreshold:
		since := *t.Since
		e.emit(key, down(since))
		t.Told = toldDown
		t.ToldSince = &since
	case t.Since == nil && t.Told == toldDown:
		since := e.now
		if t.ToldSince != nil {
			since = *t.ToldSince
		}
		e.emit(key, up(since))
		t.Told = toldNothing
		t.ToldSince = nil
		if key == keyTunnel {
			e.tunnelBackNow = true
		}
	}
}

func (e *eval) tellRestarts() {
	for name, r := range e.st.Restarts {
		svc, known := e.in.Services.Find(name)
		healthy := known && svc.Healthy()
		group := groupOf(name)
		switch {
		case r.Pending != nil && e.muted(group):
			// The flapping letter covers these restarts.
			r.Pending = nil
		// The watchdog's snapshot written right after a restart still carries
		// the verdict that caused it, so a healthy service here is one checked
		// after the restart.
		case r.Pending != nil && healthy:
			if !(name == "awg" && (e.tunnelBackNow || e.st.Tunnel.Told == toldDown)) {
				e.emit(keyRestart(name), e.restarted(name, *r.Pending, r.Reason))
			}
			r.Pending = nil
			r.ToldBroken = false
		case r.Pending != nil && e.now.Sub(*r.Pending) >= DownThreshold:
			// For awg the tunnel letter is the one that says it is still down.
			if name != "awg" {
				e.emit(keyRestart(name), e.stillBroken(name, *r.Pending, r.Reason))
				r.ToldBroken = true
			}
			r.Pending = nil
		case r.Pending == nil && r.ToldBroken && healthy && !e.muted(group):
			e.emit(keyRestart(name), e.serviceBack(name))
			r.ToldBroken = false
		}
		e.st.Restarts[name] = r
	}
}

func (e *eval) tellUpdate(first bool) {
	u := e.in.Update
	if u == nil {
		return
	}
	switch u.State {
	case "ok", "rolled-back", "failed":
	default:
		return
	}
	stamp := u.State + "@" + u.AtUTC
	if stamp == e.st.UpdateSeen {
		return
	}
	e.st.UpdateSeen = stamp
	if first {
		return
	}
	e.emit(keyUpdate, e.updateOutcome(u))
}

func (e *eval) tellRelease(first bool) {
	latest := e.in.Latest
	if latest == "" || !status.IsNewer(latest, e.in.Installed) || latest == e.st.Release {
		return
	}
	e.st.Release = latest
	if first {
		return
	}
	e.emit(keyRelease, e.releaseAvailable(latest))
}

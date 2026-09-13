package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/notify"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// tunnelStaleAfter mirrors the watchdog's STATUS_MAX_AGE: awg rewrites
// status.json every 5 seconds, so two minutes without it is a tunnel that
// is not working, whatever the container says.
const tunnelStaleAfter = 120 * time.Second

// clientsWindow is how far back the speed log is read: the threshold, the
// online silence, and room for a collapse that is still being watched.
const clientsWindow = 20 * time.Minute

// observe gathers one observation from the files the host services and the
// awg container already write. Nothing is asked of the panel: the point of
// this service is to speak even when the panel does not.
//
// Each source is optional. A file that is missing or unreadable leaves its
// part of the observation empty, and the rule that depends on it keeps its
// state instead of guessing.
func observe(root string, now time.Time, cfg *mailconf.File) notify.Inputs {
	dir := filepath.Join(root, "status")
	in := notify.Inputs{Now: now, Server: cfg.Server, Zone: time.Local}

	if st, err := status.ReadStatus(filepath.Join(dir, "status.json")); err == nil {
		in.TunnelUp = st.Interface != nil && now.Sub(st.GeneratedAt) <= tunnelStaleAfter
	}
	in.Services, _ = status.ReadServices(filepath.Join(dir, "services.json"))
	in.Update, _ = status.ReadUpdateState(filepath.Join(dir, "update-state.json"))
	in.Installed = imageVersion(filepath.Join(root, "versions.lock"))
	if releases, err := status.ReadReleases(filepath.Join(dir, "update-latest.json")); err == nil {
		in.Latest, _ = status.NotesSince(releases, in.Installed)
	}
	if act, err := status.ReadRxActivity(filepath.Join(dir, "speed.log"), now.Add(-clientsWindow), now); err == nil {
		in.Clients = act
	}
	return in
}

// imageVersion reads IMAGE_VERSION from versions.lock — the version the
// stack runs, the same value compose hands the panel.
func imageVersion(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "IMAGE_VERSION="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

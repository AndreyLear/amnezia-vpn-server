// Command awgstatus is the M5 status producer: it runs
//
//	awg show <iface> dump
//
// and atomically writes status/status.json from the result
// (M5_AUDIT.md §13, §16). It is executed by the awg entrypoint after
// every successful UAPI health check (every CHECK_INTERVAL) and is the
// only writer of status.json.
//
// Behavior:
//
//   - `awg show` exit != 0 (or a missing awg binary) → writes an
//     has_interface=false snapshot and exits 0 (the runtime is the
//     authority; status reflects it, the container stays up);
//   - dump parse failure → generic error on stderr, exit 1, previous
//     status.json left untouched (the entrypoint keeps the last good
//     snapshot);
//   - success → deterministic JSON (peers sorted by public_key), mode
//     0600, atomic write (unique temp + fsync + rename + dir fsync).
//
// The dump is never printed: key material (private_key, preshared_key)
// is discarded by the parser before it could reach the model, and no
// error path echoes dump content.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

const usageText = "usage: awgstatus <interface> <status-file>\n"

// awgDump executes `awg show <iface> dump`. The raw output is returned
// only to the parser; it is never printed or logged by awgstatus.
func awgDump(iface string) ([]byte, error) {
	return exec.Command("awg", "show", iface, "dump").Output()
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(1)
	}
	iface, outPath := os.Args[1], os.Args[2]
	if err := status.Generate(iface, outPath, time.Now, awgDump); err != nil {
		fmt.Fprintf(os.Stderr, "awgstatus: %v\n", err)
		os.Exit(1)
	}
}

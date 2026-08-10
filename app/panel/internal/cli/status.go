package cli

// `panel status` — M5 consumer command: prints the runtime AWG status
// (status/status.json) without SQLite reconciliation (that is M6).
// status.json is a runtime-only file produced by the awg container; the
// panel reads it strictly read-only and never writes it
// (M5_AUDIT.md §6 I, §10).
//
// Exit codes keep the M4 conventions (0 success, 1 runtime error,
// 2 usage). A missing file is not an error: the command answers "na"
// (M5_AUDIT.md §6 E). The payload is validated against the strict v1
// schema (unknown fields are rejected), so a hand-tampered file cannot
// leak secret-looking content through the command or an error message.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

const opStatus = "status"

// cmdStatus prints the contents of status.json. On success the canonical
// strict-parsed JSON is printed to stdout (the model is the only thing
// ever printed — no secret field can exist in it). A missing file maps
// to "na" with exit 0; malformed/tampered files exit 1 with a generic
// diagnostic that never echoes the file contents.
func (a *app) cmdStatus(args []string) int {
	if len(args) != 0 {
		return a.usageError(opStatus, "unexpected arguments")
	}
	st, err := status.ReadStatus(status.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return a.ok(opStatus, "na")
		}
		return a.fatal(opStatus, err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return a.fatal(opStatus, fmt.Errorf("serialize status: %w", err))
	}
	fmt.Fprintln(a.stdout, string(data))
	return 0
}

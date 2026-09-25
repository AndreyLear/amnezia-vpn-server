package cli

import (
	"strings"
	"testing"
)

// restore печатает safety-backup как путь отката, значит и принимать его
// должен: раньше откат требовал переименовать файл руками
// (amnezia-vpn-server-76mp.25).
func TestRestoreAcceptsSafetyBackup(t *testing.T) {
	c, _, name, _ := seedRestoreState(t)
	out := c.mustRun("restore", name)
	var safety string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "panel restore: safety backup: "); ok {
			safety = rest[strings.LastIndex(rest, "/")+1:]
		}
	}
	if !strings.HasPrefix(safety, "safety-backup-") {
		t.Fatalf("нет safety-backup в выводе:\n%s", out)
	}
	c.mustRun("init") // перезапуск применяет восстановление

	code, _, errb := c.run("restore", safety)
	if code != 0 {
		t.Fatalf("restore %s: exit %d, stderr %q", safety, code, errb)
	}
}

func TestValidRestoreName(t *testing.T) {
	cases := map[string]bool{
		"backup-2026-09-25.tar.zst":                    true,
		"safety-backup-2026-09-25-10-30-00.tar.zst":    true,
		"safety-backup-2026-09-25-25-30-00.tar.zst":    false,
		"safety-backup-2026-09-25.tar.zst":             false,
		"safety-backup-2026-09-25-10-30-00.tar":        false,
		"../safety-backup-2026-09-25-10-30-00.tar.zst": false,
	}
	for name, want := range cases {
		if got := validRestoreName(name); got != want {
			t.Errorf("validRestoreName(%q) = %v, ждали %v", name, got, want)
		}
	}
}

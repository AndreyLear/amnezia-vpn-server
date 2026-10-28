package xray

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Настройки, которые API не применяет (stats/policy, ключи, путь, порт),
// доходят до Xray только перезапуском, а у панели нет доступа к Docker.
// Она оставляет просьбу — файл рядом с сокетом API, в каталоге, который
// смонтирован и у неё, и на хосте; перезапуск делает единица systemd на
// хосте (amnezia-vpn-server-14y0.11). Раньше панель только писала в журнал
// «restart xray to apply them», и после 2.12.1 Xray на боевом так и работал
// со старым конфигом без счётчиков трафика.
func TestRegenerateAsksTheHostToRestartXray(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AMNEZIA_XRAY_API_ADDR", "unix:"+filepath.Join(dir, "api.sock"))
	t.Setenv("AMNEZIA_XRAY_CONFIG_PATH", filepath.Join(dir, "cfg", "config.json"))
	request := filepath.Join(dir, RestartRequestName)
	handle := configuredDB(t)
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }

	addClient(t, handle, "andrey", awgPub)
	Regenerate(handle, filepath.Join(dir, "awg0.conf"), logf)
	if _, err := os.Stat(request); !os.IsNotExist(err) {
		t.Fatalf("первый конфиг Xray прочтёт при старте, просьба не нужна: %v", err)
	}

	// Только пользователи — это API, без перезапуска.
	addClient(t, handle, "mama", awgPub2)
	Regenerate(handle, filepath.Join(dir, "awg0.conf"), logf)
	if _, err := os.Stat(request); !os.IsNotExist(err) {
		t.Fatalf("смена пользователей не должна перезапускать Xray: %v", err)
	}

	s := fixtureSettings()
	s.Path = "/other"
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	logged = nil
	Regenerate(handle, filepath.Join(dir, "awg0.conf"), logf)
	fi, err := os.Stat(request)
	if err != nil {
		t.Fatalf("смена пути XHTTP не оставила просьбы о перезапуске: %v (журнал: %q)", err, logged)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("просьба о перезапуске с правами %v, нужно 0600", fi.Mode().Perm())
	}
	if !strings.Contains(strings.Join(logged, "\n"), "asked the host to restart xray") {
		t.Fatalf("в журнале нет строки о просьбе: %q", logged)
	}
}

// Адрес API не на сокете — каталога, общего с хостом, нет; просить некуда,
// остаётся строка в журнале.
func TestRestartRequestPathNeedsTheSocketDirectory(t *testing.T) {
	if got := RestartRequestPath("unix:/run/xray/api.sock"); got != "/run/xray/"+RestartRequestName {
		t.Fatalf("RestartRequestPath(unix) = %q", got)
	}
	if got := RestartRequestPath("127.0.0.1:10085"); got != "" {
		t.Fatalf("RestartRequestPath(tcp) = %q, want empty", got)
	}
}

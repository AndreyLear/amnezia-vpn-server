package xray

import (
	"context"
	"database/sql"
	"time"
)

// syncTimeout ограничивает всю синхронизацию с запущенным Xray.
const syncTimeout = 10 * time.Second

// Regenerate — то, что вызывают панель и CLI после каждого изменения
// клиентов или настроек, сразу за awg0.conf: конфиг Xray рядом с
// awgConfPath (или в AMNEZIA_XRAY_CONFIG_PATH), разница — через API
// (amnezia-vpn-server-14y0.2).
//
// Ошибок не возвращает. Изменение уже записано в базу и применено к AWG;
// Xray — второй способ подключения, и его сбой не должен ни отменять
// изменение, ни отвечать на него ошибкой. Всё, что пошло не так, уходит в
// logf: файл не записан — применится при следующем изменении; API
// недоступен — файл записан и применится при перезапуске Xray.
func Regenerate(handle *sql.DB, awgConfPath string, logf func(format string, args ...any)) {
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()
	addr := APIAddr()
	res, err := Sync(ctx, handle, ConfigPath(awgConfPath), Options{APIAddr: addr, LogDir: LogDir()}, NewAPI(addr))
	switch {
	case err != nil:
		logf("xray: config not written: %v", err)
		return
	case res.APIErr != nil:
		logf("xray: config written, the running xray was not updated (it will pick the change up on restart): %v", res.APIErr)
	}
	if res.RestartNeeded {
		logf("xray: server settings changed: restart xray to apply them")
	}
}

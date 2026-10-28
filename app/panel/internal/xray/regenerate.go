package xray

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
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
	if err := ClearStaleSocket(addr); err != nil {
		logf("%v", err)
	}
	res, err := Sync(ctx, handle, ConfigPath(awgConfPath), Options{APIAddr: addr}, NewAPI(addr))
	switch {
	case err != nil:
		logf("xray: config not written: %v", err)
		return
	case res.APIErr != nil:
		logf("xray: config written, the running xray was not updated (it will pick the change up on restart): %v", res.APIErr)
	}
	if res.RestartNeeded {
		if err := RequestRestart(addr); err != nil {
			logf("xray: server settings changed: restart xray to apply them (%v)", err)
			return
		}
		logf("xray: server settings changed: asked the host to restart xray")
	}
}

// Просьба о перезапуске Xray (amnezia-vpn-server-14y0.11).
//
// Настройки сервера (stats/policy, ключи Reality, путь, порт) через API не
// применить — только перезапуском, а Docker панели недоступен и доступен
// не будет: сокет Docker — это root на хосте. Панель оставляет файл
// RestartRequestName рядом с сокетом API: этот каталог (run/xray)
// смонтирован у неё на запись и виден хосту, а там его ждёт единица
// amnezia-vpn-xray-restart.path (install.sh), которая перезапускает один
// сервис xray и удаляет файл. Всё, что эта дорожка даёт захватившему
// панель, — перезапустить Xray; так же, как с запросом обновления.
const RestartRequestName = "restart-request"

// RestartRequestPath — где лежит просьба: в каталоге сокета API. Для
// TCP-адреса API общего с хостом каталога нет — пустая строка.
func RestartRequestPath(addr string) string {
	path, ok := strings.CutPrefix(addr, unixPrefix)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(path), RestartRequestName)
}

// RequestRestart оставляет просьбу о перезапуске Xray. Содержимое — время
// просьбы, для человека; хосту важен сам файл.
func RequestRestart(addr string) error {
	path := RestartRequestPath(addr)
	if path == "" {
		return fmt.Errorf("the API address %s is not a socket shared with the host", addr)
	}
	return awgconf.WriteAtomic(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
}

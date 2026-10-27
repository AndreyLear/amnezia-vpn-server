package xray

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Трафик VLESS по клиентам (amnezia-vpn-server-14y0.8): StatsService Xray
// через тот же сокет API, что и HandlerService. Конфиг включает счётчики
// пользователей (stats и policy.levels.0, см. config.go), панель раз в
// несколько секунд читает их и копит в базе (db.RecordVLESSCounters):
// Xray держит счётчики в памяти и теряет их при перезапуске.
//
// Сообщения — из app/stats/command/command.proto Xray, кодируются и
// разбираются вручную по той же причине, что и HandlerService (см.
// api.go):
//
//	QueryStatsRequest  { string pattern = 1; bool reset = 2; }
//	QueryStatsResponse { repeated Stat stat = 1; }
//	Stat               { string name = 1; int64 value = 2; }
//	SysStatsRequest    {}
//	SysStatsResponse   { …; uint32 Uptime = 10; }
//
// reset всегда false: сброс при чтении терял бы трафик между чтением и
// записью в базу, если запись не удалась, и мешал бы любому, кто смотрит
// счётчики руками (`xray api statsquery`).

const (
	queryStatsPath = "/xray.app.stats.command.StatsService/QueryStats"
	sysStatsPath   = "/xray.app.stats.command.StatsService/GetSysStats"
	// userStatsPattern — Xray отбирает счётчики подстрокой имени; у
	// пользовательских имя «user>>>email>>>traffic>>>uplink|downlink».
	userStatsPattern = "user>>>"
)

func queryStatsRequest(pattern string) []byte {
	return appendBytesField(nil, 1, []byte(pattern))
}

// UserCounters — счётчики одного пользователя Xray: Up — от клиента
// (uplink), Down — к клиенту (downlink).
type UserCounters struct {
	Up, Down uint64
}

// UserTraffic — счётчики всех пользователей Xray по email.
func (a *API) UserTraffic(ctx context.Context) (map[string]UserCounters, error) {
	resp, err := a.invoke(ctx, queryStatsPath, queryStatsRequest(userStatsPattern))
	if err != nil {
		return nil, err
	}
	out := map[string]UserCounters{}
	err = eachField(resp, func(field int, _ uint64, data []byte) error {
		if field != 1 {
			return nil
		}
		var (
			name  string
			value uint64
		)
		if err := eachField(data, func(f int, v uint64, d []byte) error {
			switch f {
			case 1:
				name = string(d)
			case 2:
				value = v
			}
			return nil
		}); err != nil {
			return err
		}
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			return nil
		}
		c := out[parts[1]]
		switch parts[3] {
		case "uplink":
			c.Up = value
		case "downlink":
			c.Down = value
		default:
			return nil
		}
		out[parts[1]] = c
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("xray api: query stats: %w", err)
	}
	return out, nil
}

// Uptime — сколько работает Xray. По нему панель узнаёт перезапуск, после
// которого счётчики начались заново.
func (a *API) Uptime(ctx context.Context) (time.Duration, error) {
	resp, err := a.invoke(ctx, sysStatsPath, nil)
	if err != nil {
		return 0, err
	}
	var (
		uptime uint64
		found  bool
	)
	if err := eachField(resp, func(field int, v uint64, _ []byte) error {
		if field == 10 {
			uptime, found = v, true
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("xray api: sys stats: %w", err)
	}
	if !found {
		// proto3 не пишет нулевое значение: Xray, запущенный меньше
		// секунды назад.
		return 0, nil
	}
	return time.Duration(uptime) * time.Second, nil
}

var errMalformed = errors.New("malformed protobuf")

// eachField обходит поля сообщения protobuf: для varint — v, для
// строк, байтов и вложенных сообщений — data. Поля фиксированной длины
// пропускаются.
func eachField(b []byte, fn func(field int, v uint64, data []byte) error) error {
	for len(b) > 0 {
		key, n := readVarint(b)
		if n == 0 {
			return errMalformed
		}
		b = b[n:]
		field, wire := int(key>>3), key&7
		var (
			v    uint64
			data []byte
		)
		switch wire {
		case 0:
			if v, n = readVarint(b); n == 0 {
				return errMalformed
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return errMalformed
			}
			b = b[8:]
			continue
		case 2:
			l, n := readVarint(b)
			if n == 0 || uint64(len(b)-n) < l {
				return errMalformed
			}
			data = b[n : n+int(l)]
			b = b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return errMalformed
			}
			b = b[4:]
			continue
		default:
			return errMalformed
		}
		if err := fn(field, v, data); err != nil {
			return err
		}
	}
	return nil
}

// readVarint — значение и длина varint; длина 0 — varint оборван.
func readVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// StatsReader — то, что отдаёт счётчики Xray (API в работе, подделка в
// тестах).
type StatsReader interface {
	UserTraffic(ctx context.Context) (map[string]UserCounters, error)
	Uptime(ctx context.Context) (time.Duration, error)
}

// ReadTraffic — один опрос Xray: время его запуска и счётчики клиентов
// панели по номеру. Служебные учётки (ruentry) отбрасываются: трафик через
// российский вход приходит одним таким пользователем, и разложить его по
// клиентам на этом сервере нельзя.
func ReadTraffic(ctx context.Context, api StatsReader, now time.Time) (time.Time, map[int64]db.VLESSCounters, error) {
	// Аптайм — до счётчиков: перезапуск между двумя вызовами даст время
	// запуска раньше настоящего, и опрос засчитается как прирост, а не как
	// начало заново, — меньшее из зол, чем наоборот.
	uptime, err := api.Uptime(ctx)
	if err != nil {
		return time.Time{}, nil, err
	}
	traffic, err := api.UserTraffic(ctx)
	if err != nil {
		return time.Time{}, nil, err
	}
	out := map[int64]db.VLESSCounters{}
	for email, c := range traffic {
		id, ok := clientIDFromEmail(email)
		if !ok {
			continue
		}
		out[id] = db.VLESSCounters{Up: c.Up, Down: c.Down}
	}
	return now.Add(-uptime), out, nil
}

// clientIDFromEmail — обратное к userEmail.
func clientIDFromEmail(email string) (int64, bool) {
	digits, ok := strings.CutPrefix(email, "c")
	if !ok || digits == "" || digits[0] < '1' || digits[0] > '9' {
		return 0, false
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

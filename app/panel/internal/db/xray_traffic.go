package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Трафик VLESS по клиентам (amnezia-vpn-server-14y0.8).
//
// Xray считает байты каждого пользователя (user>>>c<id>>>>traffic>>>…), но
// держит счётчики в памяти: перезапуск Xray — а он нужен при каждой смене
// ключей и при каждом обновлении — обнуляет их. Поэтому панель копит сама:
// в строке клиента лежит накопленное (up_bytes/down_bytes) и последнее
// виденное значение счётчика Xray вместе со временем запуска Xray, к
// которому оно относится. Прирост — разница с последним значением, а после
// перезапуска — всё значение целиком.
//
// Перезапуск узнаётся по времени запуска (Xray отдаёт свой аптайм), а не
// только по «счётчик стал меньше»: панель опрашивает раз в несколько
// секунд и сама перезапускается при обновлении, и к первому опросу после
// перезапуска счётчик активного клиента легко перерастает прежний — тогда
// сравнение засчитало бы только излишек.
//
// Строка ключуется номером клиента, как и email c<id> в Xray. Номер
// удалённого клиента может достаться новому (id без AUTOINCREMENT), а
// счётчик c<id> переживает в Xray удаление пользователя. Поэтому удаление
// обнуляет накопленное, но оставляет последнее значение счётчика, а опрос
// для номера, которого нет среди клиентов, только двигает это значение:
// новый клиент с тем же номером начинает с нуля и с чужим счётчиком как с
// точкой отсчёта.
//
// SchemaVersion не поднимается: таблица аддитивна, как почта и сессии;
// архив прежней версии получает её пустой через Migrate, а прежняя версия
// лишнюю таблицу не читает.
const vlessTrafficTable = `CREATE TABLE IF NOT EXISTS vless_traffic (
		client_id INTEGER PRIMARY KEY,
		up_bytes INTEGER NOT NULL DEFAULT 0,
		down_bytes INTEGER NOT NULL DEFAULT 0,
		counter_up INTEGER NOT NULL DEFAULT 0,
		counter_down INTEGER NOT NULL DEFAULT 0,
		counter_since_unix INTEGER NOT NULL DEFAULT 0,
		last_active_utc TEXT
	);`

// xrayRestartSlack — насколько может «плавать» время запуска Xray от опроса
// к опросу: аптайм приходит в целых секундах, ответ идёт не мгновенно. Два
// перезапуска в пределах этих секунд друг от друга различать незачем.
const xrayRestartSlack = 5 * time.Second

// VLESSCounters — счётчики Xray одного пользователя как есть: Up — от
// клиента (uplink), Down — к клиенту (downlink).
type VLESSCounters struct {
	Up, Down uint64
}

// VLESSTraffic — накопленный трафик клиента по VLESS напрямую и время
// последнего опроса, на котором он сдвинулся. LastActive nil — движения не
// видели ни разу.
type VLESSTraffic struct {
	Up, Down   uint64
	LastActive *time.Time
}

// RecordVLESSCounters переносит в базу один опрос Xray: started — время
// запуска Xray, counters — по номеру клиента, now — время опроса. Всё одной
// транзакцией: опрос либо учтён целиком, либо не учтён вовсе.
func RecordVLESSCounters(handle *sql.DB, started time.Time, counters map[int64]VLESSCounters, now time.Time) error {
	if len(counters) == 0 {
		return nil
	}
	tx, err := handle.Begin()
	if err != nil {
		return fmt.Errorf("db: begin vless traffic: %w", err)
	}
	defer tx.Rollback()
	nowText := now.UTC().Format(time.RFC3339Nano)
	for id, cur := range counters {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM clients WHERE id = ?)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("db: vless traffic: find client %d: %w", id, err)
		}
		var (
			up, down, lastUp, lastDown, since int64
			found                             = true
		)
		err := tx.QueryRow(
			`SELECT up_bytes, down_bytes, counter_up, counter_down, counter_since_unix
			   FROM vless_traffic WHERE client_id = ?`, id,
		).Scan(&up, &down, &lastUp, &lastDown, &since)
		switch {
		case err == sql.ErrNoRows:
			found = false
		case err != nil:
			return fmt.Errorf("db: vless traffic: read %d: %w", id, err)
		}
		restarted := !found || absDuration(started.Sub(time.Unix(since, 0))) > xrayRestartSlack
		dUp := counterDelta(uint64(lastUp), cur.Up, restarted)
		dDown := counterDelta(uint64(lastDown), cur.Down, restarted)
		sinceNext := since
		if restarted {
			sinceNext = started.Unix()
		}
		if !exists {
			// Клиента нет — только точка отсчёта для того, кому достанется
			// этот номер.
			if _, err := tx.Exec(
				`INSERT INTO vless_traffic (client_id, counter_up, counter_down, counter_since_unix)
				 VALUES (?, ?, ?, ?)
				 ON CONFLICT (client_id) DO UPDATE SET up_bytes = 0, down_bytes = 0,
				   counter_up = excluded.counter_up, counter_down = excluded.counter_down,
				   counter_since_unix = excluded.counter_since_unix, last_active_utc = NULL`,
				id, int64(cur.Up), int64(cur.Down), sinceNext,
			); err != nil {
				return fmt.Errorf("db: vless traffic: baseline %d: %w", id, err)
			}
			continue
		}
		var active any
		if dUp > 0 || dDown > 0 {
			active = nowText
		}
		if _, err := tx.Exec(
			`INSERT INTO vless_traffic (client_id, up_bytes, down_bytes, counter_up, counter_down, counter_since_unix, last_active_utc)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (client_id) DO UPDATE SET
			   up_bytes = vless_traffic.up_bytes + ?, down_bytes = vless_traffic.down_bytes + ?,
			   counter_up = excluded.counter_up, counter_down = excluded.counter_down,
			   counter_since_unix = excluded.counter_since_unix,
			   last_active_utc = COALESCE(excluded.last_active_utc, vless_traffic.last_active_utc)`,
			id, int64(dUp), int64(dDown), int64(cur.Up), int64(cur.Down), sinceNext, active,
			int64(dUp), int64(dDown),
		); err != nil {
			return fmt.Errorf("db: vless traffic: record %d: %w", id, err)
		}
		// Тот же прирост — в историю по часам (amnezia-vpn-server-gzmb):
		// Up — от клиента (rx), Down — к нему (tx).
		if err := addTrafficHour(tx, id, trafficProtoVLESS, now, dUp, dDown); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit vless traffic: %w", err)
	}
	return nil
}

// counterDelta — прирост счётчика с прошлого опроса. После перезапуска
// Xray, как и когда счётчик стал меньше прежнего (тоже перезапуск), всё
// текущее значение набежало заново.
func counterDelta(last, cur uint64, restarted bool) uint64 {
	if restarted || cur < last {
		return cur
	}
	return cur - last
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// VLESSTrafficAll — накопленный трафик VLESS по номеру клиента. Клиента,
// которого Xray ещё ни разу не показывал, в ответе нет.
func VLESSTrafficAll(handle Querier) (map[int64]VLESSTraffic, error) {
	rows, err := handle.Query(
		`SELECT t.client_id, t.up_bytes, t.down_bytes, t.last_active_utc
		   FROM vless_traffic t JOIN clients c ON c.id = t.client_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query vless traffic: %w", err)
	}
	defer rows.Close()
	out := map[int64]VLESSTraffic{}
	for rows.Next() {
		var (
			id, up, down int64
			active       sql.NullString
		)
		if err := rows.Scan(&id, &up, &down, &active); err != nil {
			return nil, fmt.Errorf("db: scan vless traffic: %w", err)
		}
		tr := VLESSTraffic{Up: uint64(up), Down: uint64(down)}
		if active.Valid {
			if at, err := time.Parse(time.RFC3339Nano, active.String); err == nil {
				at = at.UTC()
				tr.LastActive = &at
			}
		}
		out[id] = tr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate vless traffic: %w", err)
	}
	return out, nil
}

// resetVLESSTraffic обнуляет накопленное удаляемого клиента, оставляя
// точку отсчёта счётчика Xray (см. комментарий к таблице).
func resetVLESSTraffic(handle execer, id int64) error {
	if _, err := handle.Exec(
		`UPDATE vless_traffic SET up_bytes = 0, down_bytes = 0, last_active_utc = NULL WHERE client_id = ?`, id,
	); err != nil {
		return fmt.Errorf("db: reset vless traffic %d: %w", id, err)
	}
	return nil
}

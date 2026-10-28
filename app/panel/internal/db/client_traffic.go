package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Трафик клиента по часам (amnezia-vpn-server-gzmb).
//
// «Скачал/Отдал» в карточке — накопленное с какого-то момента: у AWG — с
// запуска интерфейса, у VLESS — с появления клиента. Сколько из этого
// пришлось на вчера, по ним не сказать, а журналы скорости, где это видно,
// живут две недели. Здесь — прирост тех же счётчиков, сложенный по часам
// UTC и по протоколу. Час, а не день: день — понятие местное, и браузер
// складывает часы в дни своего пояса сам; сервер его пояса не знает.
//
// Ключ начинается с часа: старое удаляется диапазоном по ключу, без
// отдельного индекса, а выборка одного клиента за месяц — это тот же
// диапазон, в котором строк не больше «клиенты × часы × 2». Строк, где
// трафика не было, нет вовсе.
//
// SchemaVersion не поднимается: таблицы аддитивны, как vless_traffic.
// Архив прежней версии получает их пустыми через Migrate, а прежняя
// версия лишних таблиц не читает. В архив резервной копии история идёт
// целиком вместе с базой.
//
// Внешнего ключа на clients нет, как и у прочих таблиц: PRAGMA
// foreign_keys в панели не включена. Историю удалённого клиента удаляет
// DeleteClient — номер достаётся следующему клиенту, и чужие дни на его
// графике были бы хуже пустоты.
const clientTrafficTable = `CREATE TABLE IF NOT EXISTS client_traffic_hourly (
		hour_unix INTEGER NOT NULL,
		client_id INTEGER NOT NULL,
		protocol TEXT NOT NULL CHECK (protocol IN ('awg', 'vless')),
		rx_bytes INTEGER NOT NULL DEFAULT 0,
		tx_bytes INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (hour_unix, client_id, protocol)
	) WITHOUT ROWID;`

// awgTrafficTable — последнее виденное значение счётчиков пира AWG.
//
// Прирост — разница с ним. Держать его в памяти нельзя: панель
// перезапускается при каждом обновлении, а awg тем временем продолжает
// считать, и трафик, набежавший, пока панели не было, пропал бы. Ключ
// пира записан рядом: другой ключ — другой пир, и его счётчик к прежнему
// отношения не имеет.
const awgTrafficTable = `CREATE TABLE IF NOT EXISTS awg_traffic (
		client_id INTEGER PRIMARY KEY,
		public_key TEXT NOT NULL,
		counter_rx INTEGER NOT NULL DEFAULT 0,
		counter_tx INTEGER NOT NULL DEFAULT 0
	);`

// ClientTrafficRetention — сколько хранится история по часам. Год: на
// графике тридцать дней, а «как было в прошлом месяце» и «что было
// летом» хочется посмотреть в базе и потом. 10 клиентов за год — около
// 175 тысяч строк в худшем случае (оба протокола каждый час).
const ClientTrafficRetention = 365 * 24 * time.Hour

const (
	trafficProtoAWG   = "awg"
	trafficProtoVLESS = "vless"
)

// AWGCounters — счётчики пира из status.json как есть: Rx — от клиента,
// Tx — к клиенту.
type AWGCounters struct {
	Rx, Tx uint64
}

// TrafficHour — прирост за один час UTC. Rx — от клиента (он отдал), Tx
// — к клиенту (он скачал), как в status.json и в журналах скорости.
type TrafficHour struct {
	Hour             time.Time
	AWGRx, AWGTx     uint64
	VLESSRx, VLESSTx uint64
}

// RecordAWGCounters переносит в историю один снимок status.json: peers —
// счётчики по публичному ключу пира, now — время опроса, к часу которого
// относится прирост. Одной транзакцией с ней — удаление истории старше
// ClientTrafficRetention: опрос идёт всегда, пока работает панель, и
// отдельной уборки не нужно, как не нужно её журналу панели.
//
// Сброс счётчика узнаётся по «стало меньше прежнего»: тогда прирост —
// всё новое значение. Времени запуска интерфейса, как у Xray, status.json
// не несёт, поэтому перезапуск awg, после которого счётчик к опросу успел
// перерасти прежний, засчитает только излишек. Опрос раз в десять секунд,
// а прежнее значение — всё накопленное с прошлого запуска, так что
// перерасти его за это время клиент может только в самом начале.
//
// Первое знакомство с пиром — только точка отсчёта: в счётчике может
// лежать неделя трафика до обновления, и класть её в один час нельзя.
func RecordAWGCounters(handle *sql.DB, peers map[string]AWGCounters, now time.Time) error {
	tx, err := handle.Begin()
	if err != nil {
		return fmt.Errorf("db: begin awg traffic: %w", err)
	}
	defer tx.Rollback()
	if len(peers) > 0 {
		if err := recordAWGPeers(tx, peers, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`DELETE FROM client_traffic_hourly WHERE hour_unix < ?`,
		now.UTC().Truncate(time.Hour).Add(-ClientTrafficRetention).Unix(),
	); err != nil {
		return fmt.Errorf("db: prune traffic history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit awg traffic: %w", err)
	}
	return nil
}

func recordAWGPeers(tx *sql.Tx, peers map[string]AWGCounters, now time.Time) error {
	rows, err := tx.Query(
		`SELECT c.id, c.public_key, t.public_key, t.counter_rx, t.counter_tx
		   FROM clients c LEFT JOIN awg_traffic t ON t.client_id = c.id`,
	)
	if err != nil {
		return fmt.Errorf("db: awg traffic: read clients: %w", err)
	}
	type update struct {
		id      int64
		key     string
		cur     AWGCounters
		dRx     uint64
		dTx     uint64
		counted bool
	}
	var updates []update
	for rows.Next() {
		var (
			id             int64
			key            string
			lastKey        sql.NullString
			lastRx, lastTx sql.NullInt64
		)
		if err := rows.Scan(&id, &key, &lastKey, &lastRx, &lastTx); err != nil {
			rows.Close()
			return fmt.Errorf("db: awg traffic: scan: %w", err)
		}
		cur, ok := peers[key]
		if !ok {
			// Пира нет в снимке (клиент на паузе, awg остановлен) —
			// точка отсчёта остаётся прежней.
			continue
		}
		u := update{id: id, key: key, cur: cur}
		switch {
		case !lastKey.Valid:
			// Первое знакомство — только точка отсчёта.
		case lastKey.String != key:
			// Другой пир: его счётчик начинался с нуля.
			u.dRx, u.dTx, u.counted = cur.Rx, cur.Tx, true
		default:
			u.dRx = counterDelta(uint64(lastRx.Int64), cur.Rx, false)
			u.dTx = counterDelta(uint64(lastTx.Int64), cur.Tx, false)
			u.counted = true
		}
		updates = append(updates, u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("db: awg traffic: iterate: %w", err)
	}
	rows.Close()
	for _, u := range updates {
		if _, err := tx.Exec(
			`INSERT INTO awg_traffic (client_id, public_key, counter_rx, counter_tx) VALUES (?, ?, ?, ?)
			 ON CONFLICT (client_id) DO UPDATE SET public_key = excluded.public_key,
			   counter_rx = excluded.counter_rx, counter_tx = excluded.counter_tx`,
			u.id, u.key, int64(u.cur.Rx), int64(u.cur.Tx),
		); err != nil {
			return fmt.Errorf("db: awg traffic: baseline %d: %w", u.id, err)
		}
		if u.counted {
			if err := addTrafficHour(tx, u.id, trafficProtoAWG, now, u.dRx, u.dTx); err != nil {
				return err
			}
		}
	}
	return nil
}

// addTrafficHour прибавляет прирост к корзине часа now. Нулевой прирост
// строки не заводит: час без трафика — это отсутствие строки.
func addTrafficHour(handle execer, id int64, protocol string, now time.Time, rx, tx uint64) error {
	if rx == 0 && tx == 0 {
		return nil
	}
	if _, err := handle.Exec(
		`INSERT INTO client_traffic_hourly (hour_unix, client_id, protocol, rx_bytes, tx_bytes)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (hour_unix, client_id, protocol) DO UPDATE SET
		   rx_bytes = client_traffic_hourly.rx_bytes + excluded.rx_bytes,
		   tx_bytes = client_traffic_hourly.tx_bytes + excluded.tx_bytes`,
		now.UTC().Truncate(time.Hour).Unix(), id, protocol, int64(rx), int64(tx),
	); err != nil {
		return fmt.Errorf("db: traffic hour %d: %w", id, err)
	}
	return nil
}

// ClientTrafficHours — часовые корзины клиента с началом в [from, to),
// по возрастанию часа; оба протокола одного часа — одна корзина.
func ClientTrafficHours(handle Querier, id int64, from, to time.Time) ([]TrafficHour, error) {
	rows, err := handle.Query(
		`SELECT hour_unix, protocol, rx_bytes, tx_bytes FROM client_traffic_hourly
		  WHERE hour_unix >= ? AND hour_unix < ? AND client_id = ?
		  ORDER BY hour_unix`,
		from.UTC().Unix(), to.UTC().Unix(), id,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query traffic history: %w", err)
	}
	defer rows.Close()
	var out []TrafficHour
	for rows.Next() {
		var (
			hour, rx, tx int64
			protocol     string
		)
		if err := rows.Scan(&hour, &protocol, &rx, &tx); err != nil {
			return nil, fmt.Errorf("db: scan traffic history: %w", err)
		}
		at := time.Unix(hour, 0).UTC()
		if len(out) == 0 || !out[len(out)-1].Hour.Equal(at) {
			out = append(out, TrafficHour{Hour: at})
		}
		last := &out[len(out)-1]
		switch protocol {
		case trafficProtoAWG:
			last.AWGRx, last.AWGTx = uint64(rx), uint64(tx)
		case trafficProtoVLESS:
			last.VLESSRx, last.VLESSTx = uint64(rx), uint64(tx)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate traffic history: %w", err)
	}
	return out, nil
}

// deleteClientTraffic удаляет историю и точку отсчёта AWG удаляемого
// клиента (см. комментарий к таблице).
func deleteClientTraffic(handle execer, id int64) error {
	if _, err := handle.Exec(`DELETE FROM client_traffic_hourly WHERE client_id = ?`, id); err != nil {
		return fmt.Errorf("db: delete traffic history %d: %w", id, err)
	}
	if _, err := handle.Exec(`DELETE FROM awg_traffic WHERE client_id = ?`, id); err != nil {
		return fmt.Errorf("db: delete awg traffic %d: %w", id, err)
	}
	return nil
}

package xray

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Applier — то, что умеет менять пользователей запущенного Xray (API в
// работе, подделка в тестах).
type Applier interface {
	AddUser(ctx context.Context, tag, email, uuid string) error
	RemoveUser(ctx context.Context, tag, email string) error
}

// Result — что сделала синхронизация.
type Result struct {
	// Written — файл конфига записан (Xray на сервере настроен).
	Written bool
	// Added и Removed — email пользователей, добавленных и снятых через
	// API.
	Added, Removed []string
	// RestartNeeded — изменилось не только множество пользователей
	// (ключи, путь, маскировка): через API это не применить, новый конфиг
	// вступит в силу при перезапуске Xray.
	RestartNeeded bool
	// APIErr — API ответил ошибкой или недоступен. Файл при этом записан и
	// применится при перезапуске Xray, поэтому это не ошибка изменения,
	// а повод для строчки в журнале.
	APIErr error
}

// Sync пересобирает конфиг Xray из базы, атомарно пишет его в path и
// переносит разницу в пользователях в запущенный Xray через api (nil —
// только файл, как в panel-init, когда Xray ещё не запущен).
//
// Нет настроек Xray в базе — файл не пишется вовсе, и это не ошибка: стек
// поднимается и без Xray (amnezia-vpn-server-14y0.2).
//
// Разница считается между прежним файлом и новым: файл — это то, с чем
// Xray запущен, плюс всё, что уже применено через API. Чтение старого,
// запись нового и вызовы API идут под тем же видом блокировки, что и у
// awg0.conf (файл path+".lock"): иначе CLI и панель, меняя клиентов
// одновременно, применили бы свои разницы вперемешку.
func Sync(ctx context.Context, handle *sql.DB, path string, opt Options, api Applier) (Result, error) {
	s, err := db.XraySettingsRow(handle)
	if errors.Is(err, db.ErrXrayNotConfigured) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Result{}, fmt.Errorf("xray: create config directory: %w", err)
	}
	unlock, err := lock(path)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	// Настройки и клиенты — одним снимком, как у awg0.conf
	// (amnezia-vpn-server-76mp.18).
	tx, err := handle.Begin()
	if err != nil {
		return Result{}, fmt.Errorf("xray: begin config read: %w", err)
	}
	defer tx.Rollback()
	if s, err = db.XraySettingsRow(tx); err != nil {
		return Result{}, err
	}
	clients, err := db.VLESSClientsForConfig(tx)
	if err != nil {
		return Result{}, err
	}
	service, err := db.XrayServiceUsers(tx)
	if err != nil {
		return Result{}, err
	}
	next := build(s, clients, service, opt)
	data, err := marshal(next)
	if err != nil {
		return Result{}, err
	}

	prev, havePrev := readPrevious(path)
	if err := awgconf.WriteAtomic(path, data); err != nil {
		return Result{}, err
	}
	res := Result{Written: true}
	// Без прежнего файла Xray с этим конфигом ещё не запускался: ему
	// нечего досылать, он прочтёт файл при старте.
	if !havePrev || api == nil {
		return res, nil
	}
	if !sameExceptUsers(prev, next) {
		res.RestartNeeded = true
	}
	pending := readPending(path)
	var left []string
	res.Added, res.Removed, left, res.APIErr = applyUsers(ctx, api, usersOf(prev), usersOf(next), pending)
	if err := writePending(path, left); err != nil && res.APIErr == nil {
		res.APIErr = err
	}
	return res, nil
}

// Недосланное через API запоминается в path+".pending". Без этого сбой
// одного вызова терялся бы: следующая синхронизация считает разницу от уже
// записанного файла и о неприменённом не знает. Отключённый клиент, которого
// не удалось снять, сохранял бы доступ до перезапуска Xray — а отключение
// обязано действовать.
func pendingPath(path string) string { return path + ".pending" }

func readPending(path string) []string {
	data, err := os.ReadFile(pendingPath(path))
	if err != nil {
		return nil
	}
	var emails []string
	if json.Unmarshal(data, &emails) != nil {
		return nil
	}
	return emails
}

func writePending(path string, emails []string) error {
	if len(emails) == 0 {
		if err := os.Remove(pendingPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("xray: clear pending: %w", err)
		}
		return nil
	}
	data, err := json.Marshal(emails)
	if err != nil {
		return fmt.Errorf("xray: marshal pending: %w", err)
	}
	return awgconf.WriteAtomic(pendingPath(path), data)
}

func lock(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("xray: open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("xray: lock %s: %w", f.Name(), err)
	}
	return func() { f.Close() }, nil
}

// readPrevious читает прежний конфиг. Нечитаемый или чужой файл — как
// отсутствующий: досылать по нему нечего.
func readPrevious(path string) (config, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, false
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil || len(c.Inbounds) == 0 {
		return config{}, false
	}
	return c, true
}

func usersOf(c config) map[string]string {
	out := map[string]string{}
	for _, in := range c.Inbounds {
		if in.Tag != InboundTag {
			continue
		}
		for _, u := range in.Settings.Clients {
			out[u.Email] = u.ID
		}
	}
	return out
}

func sameExceptUsers(a, b config) bool {
	strip := func(c config) []byte {
		in := append([]inbound(nil), c.Inbounds...)
		for i := range in {
			in[i].Settings.Clients = nil
		}
		c.Inbounds = in
		data, _ := marshal(c)
		return data
	}
	return bytes.Equal(strip(a), strip(b))
}

// applyUsers переносит разницу в запущенный Xray: пользователей, которые
// изменились между prev и next, и всех из pending — недосланных прошлыми
// синхронизациями. Сначала снимаются ушедшие (отключённый должен потерять
// доступ, даже если добавление следом не удастся), потом добавляются
// новые; сменивший UUID или недосланный — снять и добавить заново. «Уже
// есть» и «нет такого» — не ошибки: Xray уже в нужном состоянии, поэтому
// лишняя операция безвредна. На первой настоящей ошибке синхронизация
// останавливается, а необработанное возвращается в left.
func applyUsers(ctx context.Context, api Applier, prev, next map[string]string, pending []string) (added, removed, left []string, err error) {
	ops := map[string]bool{}
	for email, id := range prev {
		if nid, ok := next[email]; !ok || nid != id {
			ops[email] = true
		}
	}
	for email, id := range next {
		if pid, ok := prev[email]; !ok || pid != id {
			ops[email] = true
		}
	}
	retry := map[string]bool{}
	for _, email := range pending {
		ops[email] = true
		retry[email] = true
	}
	var toRemove, toAdd []string
	for email := range ops {
		if _, ok := next[email]; ok {
			toAdd = append(toAdd, email)
		} else {
			toRemove = append(toRemove, email)
		}
	}
	sort.Strings(toRemove)
	sort.Strings(toAdd)
	order := append(append([]string(nil), toRemove...), toAdd...)
	for i, email := range order {
		uuid, keep := next[email]
		if _, was := prev[email]; !keep || was || retry[email] {
			if err := api.RemoveUser(ctx, InboundTag, email); err != nil && !isNotFound(err) {
				return added, removed, order[i:], fmt.Errorf("remove %s: %w", email, err)
			}
			if !keep {
				removed = append(removed, email)
				continue
			}
		}
		if err := api.AddUser(ctx, InboundTag, email, uuid); err != nil && !isAlreadyExists(err) {
			return added, removed, order[i:], fmt.Errorf("add %s: %w", email, err)
		}
		added = append(added, email)
	}
	return added, removed, nil, nil
}

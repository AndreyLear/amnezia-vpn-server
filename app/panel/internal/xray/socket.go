package xray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// Брошенный сокет API (amnezia-vpn-server-14y0.2).
//
// Xray не удаляет чужой файл сокета перед bind: после SIGKILL, OOM или
// пропажи питания файл остаётся, следующий запуск падает с «address
// already in use», и Docker перезапускает Xray по кругу — VLESS лежит,
// пока кто-нибудь не удалит файл руками. Убирает его панель, у которой
// каталог сокета смонтирован на запись.
//
// Отличить брошенный сокет от живого помогает сам Xray: пока он слушает
// сокет, он держит flock на файле <сокет>.lock. Взяли эту блокировку —
// значит, никто не слушает, и файл можно удалить; не взяли — Xray жив.
// Запускающийся Xray, наткнувшись на нашу блокировку, просто подождёт её.

// ClearStaleSocket удаляет сокет API, если его никто не слушает. addr —
// как в AMNEZIA_XRAY_API_ADDR; для TCP-адреса ничего не делает.
func ClearStaleSocket(addr string) error {
	path, ok := strings.CutPrefix(addr, unixPrefix)
	if !ok {
		return nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("xray: open socket lock: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil // Xray слушает сокет
		}
		return fmt.Errorf("xray: lock socket: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("xray: remove stale socket: %w", err)
	}
	return nil
}

// WatchSocket раз в every убирает брошенный сокет, пока ctx не отменён:
// упавший Xray поднимется сам, не дожидаясь, пока в панели что-то
// изменят.
func WatchSocket(ctx context.Context, addr string, every time.Duration, logf func(format string, args ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := ClearStaleSocket(addr); err != nil {
			logf("%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

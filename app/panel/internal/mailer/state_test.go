package mailer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)

// Отправка не удалась — повторы через 1, 5 и 15 минут, потом отказ
// запоминается и письмо снимается с очереди (amnezia-vpn-server-hxgr).
func TestDeliverRetriesThenGivesUp(t *testing.T) {
	st := &State{}
	st.Put("tunnel", Message{Subject: "Туннель не работает 5 минут"}, t0)
	calls := 0
	failing := func(context.Context, Message) error { calls++; return errors.New("421 try later") }

	at := t0
	wantNext := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
	for i, d := range wantNext {
		out := st.Deliver(context.Background(), at, failing)
		if len(out) != 1 || out[0].Sent || out[0].GaveUp || out[0].Attempt != i+1 {
			t.Fatalf("попытка %d: %+v", i+1, out)
		}
		if got := st.Pending[0].NextAt; !got.Equal(at.Add(d)) {
			t.Fatalf("попытка %d: следующая в %v, ждали через %v", i+1, got, d)
		}
		// До срока ничего не отправляется.
		if out := st.Deliver(context.Background(), at.Add(d-time.Second), failing); len(out) != 0 {
			t.Fatalf("отправка раньше срока: %+v", out)
		}
		at = at.Add(d)
	}
	out := st.Deliver(context.Background(), at, failing)
	if len(out) != 1 || !out[0].GaveUp {
		t.Fatalf("после трёх повторов не сдались: %+v", out)
	}
	if calls != 4 {
		t.Errorf("попыток %d, ждали 4: первая и три повтора", calls)
	}
	if len(st.Pending) != 0 {
		t.Errorf("очередь не пуста: %+v", st.Pending)
	}
	if st.LastFailure == nil || st.LastFailure.Error != "421 try later" || st.LastFailure.Key != "tunnel" {
		t.Fatalf("отказ не запомнен: %+v", st.LastFailure)
	}

	// Первое же удачное письмо снимает отметку об отказе.
	st.Put("tunnel", Message{Subject: "Туннель снова работает"}, at)
	out = st.Deliver(context.Background(), at, func(context.Context, Message) error { return nil })
	if len(out) != 1 || !out[0].Sent {
		t.Fatalf("не отправлено: %+v", out)
	}
	if st.LastFailure != nil || st.LastSuccessAt == nil || !st.LastSuccessAt.Equal(at) {
		t.Fatalf("после успеха: failure=%+v success=%v", st.LastFailure, st.LastSuccessAt)
	}
}

// Очередь не копится: новое письмо по тому же поводу заменяет ожидающее,
// и уходит актуальное состояние, а не устаревшее.
func TestPutReplacesPendingOfSameKey(t *testing.T) {
	st := &State{}
	st.Put("tunnel", Message{Subject: "Туннель не работает 5 минут"}, t0)
	st.Put("update", Message{Subject: "Доступна новая версия"}, t0)
	st.Deliver(context.Background(), t0, func(context.Context, Message) error { return errors.New("down") })

	st.Put("tunnel", Message{Subject: "Туннель снова работает"}, t0.Add(10*time.Second))
	if len(st.Pending) != 2 {
		t.Fatalf("в очереди %d, ждали 2", len(st.Pending))
	}
	var sent []string
	st.Deliver(context.Background(), t0.Add(10*time.Second), func(_ context.Context, m Message) error {
		sent = append(sent, m.Subject)
		return nil
	})
	// Замена сбрасывает счётчик и срок: актуальное уходит сразу, а письмо
	// об обновлении ждёт своей минуты.
	if len(sent) != 1 || sent[0] != "Туннель снова работает" {
		t.Fatalf("отправлено %q", sent)
	}
	if len(st.Pending) != 1 || st.Pending[0].Key != "update" {
		t.Fatalf("осталось %+v", st.Pending)
	}
}

func TestStateSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail-state.json")
	st, err := LoadState(path)
	if err != nil || len(st.Pending) != 0 {
		t.Fatalf("нет файла: %+v, %v", st, err)
	}
	st.Put("tunnel", Message{Subject: "s", Body: "b"}, t0)
	st.Deliver(context.Background(), t0, func(context.Context, Message) error { return errors.New("down") })
	if err := st.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("права %o", info.Mode().Perm())
	}
	back, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(back.Pending) != 1 || back.Pending[0].Attempts != 1 || !back.Pending[0].NextAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("прочитано %+v", back.Pending)
	}

	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = LoadState(path)
	if err == nil || st == nil || len(st.Pending) != 0 {
		t.Fatalf("испорченный файл: %+v, %v — ждали пустую очередь и ошибку", st, err)
	}
}

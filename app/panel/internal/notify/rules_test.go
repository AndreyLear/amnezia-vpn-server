package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

var start = time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)

// world is a server observed once a minute, the way the timer runs awgmail.
type world struct {
	t     *testing.T
	st    *State
	in    Inputs
	sent  []Letter
	clock time.Time
	dir   string
}

func newWorld(t *testing.T) *world {
	w := &world{t: t, st: &State{}, clock: start, dir: t.TempDir()}
	w.in = Inputs{
		Server:    "vpn.example.org",
		TunnelUp:  true,
		Installed: "2.10.26",
		Latest:    "2.10.26",
		Services: &status.Services{Services: []status.Service{
			{Name: "dns", State: "ok"}, {Name: "awg", State: "ok"},
		}},
	}
	w.clientsActive("a", "b")
	return w
}

// clientsActive: these peers moved rx just now; others keep their last move.
func (w *world) clientsActive(keys ...string) {
	if w.in.Clients == nil {
		w.in.Clients = &status.RxActivity{LastMove: map[string]time.Time{}}
	}
	for _, k := range keys {
		w.in.Clients.LastMove[k] = w.clock
	}
	w.in.Clients.LastSample = w.clock
}

// step advances by d and runs the rules; the log keeps sampling.
func (w *world) step(d time.Duration) []Letter {
	w.t.Helper()
	w.clock = w.clock.Add(d)
	w.in.Now = w.clock
	w.in.Services.CheckedAtUTC = w.clock.Format(time.RFC3339)
	if w.in.Clients != nil {
		w.in.Clients.LastSample = w.clock
	}
	got := Evaluate(w.in, w.st)
	w.sent = append(w.sent, got...)
	// The state survives every run through its file form.
	back, err := roundTrip(w.st, w.dir)
	if err != nil {
		w.t.Fatalf("state round trip: %v", err)
	}
	w.st = back
	return got
}

// minutes runs n one-minute steps, calling each before every step.
func (w *world) minutes(n int, each func()) []Letter {
	w.t.Helper()
	var all []Letter
	for i := 0; i < n; i++ {
		if each != nil {
			each()
		}
		all = append(all, w.step(time.Minute)...)
	}
	return all
}

func roundTrip(st *State, _ string) (*State, error) {
	var back State
	if err := json.Unmarshal(st.Marshal(), &back); err != nil {
		return nil, err
	}
	return &back, nil
}

// Состояние переживает запись в файл и чтение.
func TestStateFile(t *testing.T) {
	w := newWorld(t)
	w.in.TunnelUp = false
	w.minutes(7, nil)
	path := filepath.Join(t.TempDir(), "notify-state.json")
	if err := w.st.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadState(path)
	if err != nil || string(back.Marshal()) != string(w.st.Marshal()) {
		t.Fatalf("прочитано другое: %v\n%s\n%s", err, back.Marshal(), w.st.Marshal())
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := LoadState(path); err == nil || st.Initialized {
		t.Fatalf("испорченный файл: %+v, %v — ждали первый прогон и ошибку", st, err)
	}
}

func keys(letters []Letter) []string {
	var out []string
	for _, l := range letters {
		out = append(out, l.Key)
	}
	return out
}

func wantKeys(t *testing.T, got []Letter, want ...string) {
	t.Helper()
	if strings.Join(keys(got), ",") != strings.Join(want, ",") {
		t.Fatalf("письма %v, ждали %v", keys(got), want)
	}
}

// Исправный сервер не пишет вовсе — ни в первый прогон, ни за сутки
// (amnezia-vpn-server-0d2n, приёмка dfs2).
func TestHealthyServerSendsNothing(t *testing.T) {
	w := newWorld(t)
	wantKeys(t, w.minutes(24*60, func() { w.clientsActive("a", "b") }))
}

// Первый прогон — только точка отсчёта: старый перезапуск, давний итог
// обновления и уже известный выпуск новостью не считаются.
func TestFirstRunTakesBaseline(t *testing.T) {
	w := newWorld(t)
	w.in.Services.Services[0].RestartedAtUTC = start.Add(-time.Hour).Format(time.RFC3339)
	w.in.Update = &status.UpdateState{State: "ok", From: "2.10.25", To: "2.10.26", AtUTC: start.Add(-2 * time.Hour).Format(time.RFC3339)}
	w.in.Latest = "2.10.27"
	wantKeys(t, w.minutes(10, func() { w.clientsActive("a", "b") }))
}

// Туннель: письмо ровно через 5 минут, одно, без повторов каждую минуту;
// письмо о возврате — одно.
func TestTunnelDownAndBack(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.TunnelUp = false
	wantKeys(t, w.minutes(5, nil)) // отказ замечен на 1-й минуте, 4 минуты — рано
	got := w.step(time.Minute)
	wantKeys(t, got, keyTunnel)
	if !strings.Contains(got[0].Message.Subject, "5 минут") {
		t.Errorf("тема %q", got[0].Message.Subject)
	}
	wantKeys(t, w.minutes(30, nil))
	w.in.TunnelUp = true
	wantKeys(t, w.step(time.Minute), keyTunnel)
	wantKeys(t, w.minutes(30, func() { w.clientsActive("a", "b") }))
}

// Короткий провал короче порога не стоит письма — ни о беде, ни о возврате.
func TestTunnelBlipIsSilent(t *testing.T) {
	w := newWorld(t)
	w.in.TunnelUp = false
	w.minutes(3, nil)
	w.in.TunnelUp = true
	wantKeys(t, w.minutes(10, func() { w.clientsActive("a", "b") }))
	if len(w.sent) != 0 {
		t.Fatalf("письма за провал короче порога: %v", keys(w.sent))
	}
}

// Двое на связи пропали одновременно и не вернулись за 5 минут — письмо;
// первый вернувшийся — письмо о возврате.
func TestClientsCollapse(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.Clients.LastMove["a"] = w.clock
	w.in.Clients.LastMove["b"] = w.clock.Add(-5 * time.Second)
	// Молчание: 1 минута до «не на связи» и ещё 4 — до порога от момента обрыва.
	wantKeys(t, w.minutes(4, nil))
	got := w.step(time.Minute)
	wantKeys(t, got, keyClients)
	// Только факт, без догадок о причине (dfs2 п.11, amnezia-vpn-server-daqa).
	for _, guess := range []string{"Так бывает", "блокировка", "хостер"} {
		if strings.Contains(got[0].Message.Body, guess) {
			t.Errorf("в письме догадка о причине: %q", got[0].Message.Body)
		}
	}
	wantKeys(t, w.minutes(10, nil))
	w.step(0)
	w.clientsActive("b")
	wantKeys(t, w.step(time.Second), keyClients)
}

// Двое простаивающих на связи: rx движется только от keepalive раз в
// 25 секунд, последние движения до обрыва — за 2 и за 20 секунд. Это тот же
// обрыв, что у активных, и письмо о нём приходит (amnezia-vpn-server-76mp.15).
func TestClientsCollapseWhileIdle(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	cut := w.clock
	w.in.Clients.LastMove["a"] = cut.Add(-2 * time.Second)
	w.in.Clients.LastMove["b"] = cut.Add(-20 * time.Second)
	w.in.Clients.PrevMove = map[string]time.Time{
		"a": cut.Add(-27 * time.Second),
		"b": cut.Add(-45 * time.Second),
	}
	wantKeys(t, w.minutes(4, nil))
	got := w.step(time.Minute)
	wantKeys(t, got, keyClients)
	if !strings.Contains(got[0].Message.Body, "В 03:00 UTC") {
		t.Errorf("момент обрыва — последнее движение: %q", got[0].Message.Body)
	}
}

func TestClientsRuleIsNarrow(t *testing.T) {
	cases := map[string]func(w *world){
		"засыпали по очереди": func(w *world) {
			w.in.Clients.LastMove["a"] = w.clock
			w.in.Clients.LastMove["b"] = w.clock.Add(-20 * time.Second)
		},
		// Простаивающие клиенты шлют только keepalive раз в 25 секунд; b
		// замолчал за 40 секунд до a — дольше, чем окно его keepalive, так
		// что это не один обрыв (amnezia-vpn-server-76mp.15).
		"простаивающие засыпали по очереди": func(w *world) {
			w.in.Clients.LastMove["a"] = w.clock
			w.in.Clients.LastMove["b"] = w.clock.Add(-40 * time.Second)
			w.in.Clients.PrevMove = map[string]time.Time{
				"a": w.clock.Add(-25 * time.Second),
				"b": w.clock.Add(-65 * time.Second),
			}
		},
		// Ритм, реже keepalive, не расширяет окно сверх него.
		"редкий трафик не шире keepalive": func(w *world) {
			w.in.Clients.LastMove["a"] = w.clock
			w.in.Clients.LastMove["b"] = w.clock.Add(-40 * time.Second)
			w.in.Clients.PrevMove = map[string]time.Time{
				"a": w.clock.Add(-5 * time.Second),
				"b": w.clock.Add(-10 * time.Minute),
			}
		},
		"клиент один": func(w *world) {
			delete(w.in.Clients.LastMove, "b")
			w.in.Clients.LastMove["a"] = w.clock
		},
		"журнал скорости не пишется": func(w *world) {
			w.in.Clients.LastMove["a"] = w.clock
			w.in.Clients.LastMove["b"] = w.clock
			w.in.Clients = &status.RxActivity{LastMove: w.in.Clients.LastMove, LastSample: w.clock}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.step(time.Minute)
			setup(w)
			frozen := w.in.Clients.LastSample
			var got []Letter
			for i := 0; i < 20; i++ {
				w.clock = w.clock.Add(time.Minute)
				w.in.Now = w.clock
				if name != "журнал скорости не пишется" {
					w.in.Clients.LastSample = w.clock
				} else {
					w.in.Clients.LastSample = frozen
				}
				got = append(got, Evaluate(w.in, w.st)...)
			}
			wantKeys(t, got)
		})
	}
}

// Когда лёг сам туннель, клиенты пропали по этой причине: письмо одно,
// о туннеле.
func TestTunnelDownSuppressesClientsLetter(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.TunnelUp = false
	w.in.Clients.LastMove["a"] = w.clock
	w.in.Clients.LastMove["b"] = w.clock
	got := w.minutes(20, nil)
	wantKeys(t, got, keyTunnel)
}

// Сторож перезапустил резолвер, и тот заработал — одно письмо.
func TestWatchdogRestartRecovered(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	dns := &w.in.Services.Services[0]
	dns.State = "fail"
	dns.RestartedAtUTC = w.clock.Add(30 * time.Second).Format(time.RFC3339)
	dns.RestartReason = "не отвечает на 10.8.0.1"
	// Снимок сторожа сразу после перезапуска ещё говорит «fail».
	wantKeys(t, w.step(time.Minute))
	dns.State = "ok"
	got := w.step(time.Minute)
	wantKeys(t, got, keyRestart("dns"))
	if !strings.Contains(got[0].Message.Body, "не отвечает на 10.8.0.1") {
		t.Errorf("в письме нет причины: %q", got[0].Message.Body)
	}
	wantKeys(t, w.minutes(30, func() { w.clientsActive("a", "b") }))
}

// Перезапуск не помог — письмо через 5 минут; заработал позже — ещё одно.
func TestWatchdogRestartDidNotHelp(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	dns := &w.in.Services.Services[0]
	dns.State = "fail"
	dns.RestartedAtUTC = w.clock.Add(10 * time.Second).Format(time.RFC3339)
	active := func() { w.clientsActive("a", "b") }
	got := w.minutes(6, active)
	wantKeys(t, got, keyRestart("dns"))
	wantKeys(t, w.minutes(10, active))
	dns.State = "ok"
	active()
	wantKeys(t, w.step(time.Minute), keyRestart("dns"))
	wantKeys(t, w.minutes(10, active))
}

// Перезапуск туннеля, который вернул связь до порга, — одно письмо о
// перезапуске, без писем о туннеле.
func TestAwgRestartBeforeThreshold(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.TunnelUp = false
	w.minutes(2, nil)
	awg := &w.in.Services.Services[1]
	awg.State = "fail"
	awg.RestartedAtUTC = w.clock.Add(5 * time.Second).Format(time.RFC3339)
	w.step(time.Minute)
	w.in.TunnelUp = true
	awg.State = "ok"
	got := w.minutes(10, func() { w.clientsActive("a", "b") })
	wantKeys(t, got, keyRestart("awg"))
}

// Перезапуск туннеля не помог — пишет правило туннеля, а не сторожа:
// одна беда, одно письмо, и одно — о возврате без отдельного о перезапуске.
func TestAwgRestartThatDidNotHelp(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.TunnelUp = false
	awg := &w.in.Services.Services[1]
	awg.State = "fail"
	w.minutes(2, nil)
	awg.RestartedAtUTC = w.clock.Add(5 * time.Second).Format(time.RFC3339)
	wantKeys(t, w.minutes(15, nil), keyTunnel)
	w.in.TunnelUp = true
	awg.State = "ok"
	wantKeys(t, w.minutes(10, func() { w.clientsActive("a", "b") }), keyTunnel)
}

// Туннель лежал дольше порога, сторож перезапустил его, и он заработал —
// письмо о возврате туннеля, отдельного о перезапуске нет.
func TestAwgRestartAfterTunnelLetter(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.TunnelUp = false
	awg := &w.in.Services.Services[1]
	awg.State = "fail"
	w.minutes(4, nil)
	awg.RestartedAtUTC = w.clock.Add(5 * time.Second).Format(time.RFC3339)
	wantKeys(t, w.minutes(2, nil), keyTunnel)
	w.in.TunnelUp = true
	awg.State = "ok"
	wantKeys(t, w.minutes(10, func() { w.clientsActive("a", "b") }), keyTunnel)
}

func TestUpdateOutcomes(t *testing.T) {
	for _, state := range []string{"ok", "rolled-back", "failed"} {
		t.Run(state, func(t *testing.T) {
			w := newWorld(t)
			w.step(time.Minute)
			w.in.Update = &status.UpdateState{State: "running", From: "2.10.26", To: "2.10.27", AtUTC: w.clock.Format(time.RFC3339)}
			wantKeys(t, w.step(time.Minute))
			w.in.Update = &status.UpdateState{State: state, From: "2.10.26", To: "2.10.27", AtUTC: w.clock.Format(time.RFC3339)}
			got := w.step(time.Minute)
			wantKeys(t, got, keyUpdate)
			if !strings.Contains(got[0].Message.Body, "2.10.27") {
				t.Errorf("в письме нет версии: %q", got[0].Message.Body)
			}
			after := w.minutes(10, func() { w.clientsActive("a", "b") })
			if state == "failed" {
				// Сервер при этом исправен — через пять минут письмо о
				// возврате к норме (amnezia-vpn-server-crar).
				wantKeys(t, after, keyUpdate)
				return
			}
			wantKeys(t, after)
		})
	}
}

// Обновление не удалось и не откатилось — после письма об этом приходит
// письмо о возврате к норме, когда сервер исправен 5 минут подряд
// (dfs2 «ОБЯЗАТЕЛЬНО о возврате к норме», amnezia-vpn-server-crar).
func TestUpdateFailedThenRecovered(t *testing.T) {
	w := newWorld(t)
	active := func() { w.clientsActive("a", "b") }
	var updateLetters []string
	collect := func(ls []Letter) {
		for _, l := range ls {
			if l.Key == keyUpdate {
				updateLetters = append(updateLetters, l.Message.Subject)
			}
		}
	}
	w.step(time.Minute)
	w.in.TunnelUp = false
	w.in.Update = &status.UpdateState{State: "failed", From: "2.10.26", To: "2.10.27", AtUTC: w.clock.Format(time.RFC3339)}
	collect(w.step(time.Minute))
	if len(updateLetters) != 1 {
		t.Fatalf("нет письма о неудаче: %v", updateLetters)
	}
	updateLetters = nil
	w.minutes(3, nil) // туннель ещё лежит
	w.in.TunnelUp = true
	collect(w.minutes(4, active)) // исправен, но меньше пяти минут
	if len(updateLetters) != 0 {
		t.Fatalf("письмо о возврате раньше срока: %v", updateLetters)
	}
	// Сбой службы сбрасывает отсчёт.
	w.in.Services.Services[0].State = "fail"
	collect(w.minutes(2, active))
	w.in.Services.Services[0].State = "ok"
	collect(w.minutes(4, active))
	if len(updateLetters) != 0 {
		t.Fatalf("отсчёт не сброшен сбоем службы: %v", updateLetters)
	}
	collect(w.minutes(3, active))
	if len(updateLetters) != 1 || updateLetters[0] != "Сервер снова работает" {
		t.Fatalf("письма об обновлении %v, ждали одно «Сервер снова работает»", updateLetters)
	}
	collect(w.minutes(30, active))
	if len(updateLetters) != 1 {
		t.Fatalf("письмо о возврате повторилось: %v", updateLetters)
	}
}

// Удачное обновление после неудачного — одно письмо «Обновление
// установлено», без отдельного «Сервер снова работает».
func TestUpdateFailedThenInstalled(t *testing.T) {
	w := newWorld(t)
	active := func() { w.clientsActive("a", "b") }
	w.step(time.Minute)
	w.in.Update = &status.UpdateState{State: "failed", From: "2.10.26", To: "2.10.27", AtUTC: w.clock.Format(time.RFC3339)}
	w.in.TunnelUp = false
	w.step(time.Minute)
	w.in.TunnelUp = true
	w.in.Update = &status.UpdateState{State: "ok", From: "2.10.26", To: "2.10.27", AtUTC: w.clock.Add(time.Second).Format(time.RFC3339)}
	var subjects []string
	for _, l := range w.minutes(20, active) {
		if l.Key == keyUpdate {
			subjects = append(subjects, l.Message.Subject)
		}
	}
	if len(subjects) != 1 || subjects[0] != "Обновление установлено" {
		t.Fatalf("письма об обновлении %v", subjects)
	}
}

func TestReleaseAvailableOnce(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.Latest = "2.10.27"
	got := w.step(time.Minute)
	wantKeys(t, got, keyRelease)
	if strings.Contains(got[0].Message.Subject, "2.10.27") {
		t.Errorf("версия в теме: %q", got[0].Message.Subject)
	}
	wantKeys(t, w.minutes(60, func() { w.clientsActive("a", "b") }))
	w.in.Installed, w.in.Latest = "2.10.27", "2.10.27"
	wantKeys(t, w.minutes(5, func() { w.clientsActive("a", "b") }))
}

// Мигание: больше трёх смен за час — одно письмо «мигает» и час тишины.
// Беда, пережившая тишину, пишется после неё.
func TestFlappingMutesForAnHour(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	for i := 0; i < 2; i++ { // два коротких пропадания = четыре смены
		w.in.TunnelUp = false
		w.minutes(2, nil)
		w.in.TunnelUp = true
		w.minutes(2, func() { w.clientsActive("a", "b") })
	}
	wantKeys(t, w.sent, keyFlap(groupTunnel))
	w.sent = nil

	// Долгий провал внутри часа тишины — молчим.
	w.in.TunnelUp = false
	wantKeys(t, w.minutes(40, nil))
	// Тишина кончилась, туннель всё ещё лежит — пишем.
	got := w.minutes(20, nil)
	wantKeys(t, got, keyTunnel)
}

func TestLettersNameServerWithoutLinks(t *testing.T) {
	w := newWorld(t)
	w.step(time.Minute)
	w.in.TunnelUp = false
	w.in.Latest = "2.10.27"
	w.in.Update = &status.UpdateState{State: "failed", From: "2.10.26", To: "2.10.27", AtUTC: w.clock.Format(time.RFC3339)}
	got := w.minutes(7, nil)
	w.in.TunnelUp = true
	got = append(got, w.step(time.Minute)...)
	if len(got) < 4 {
		t.Fatalf("писем %v", keys(got))
	}
	for _, l := range got {
		if !strings.Contains(l.Message.Body, "Сервер: vpn.example.org") {
			t.Errorf("%s: нет адреса сервера в теле", l.Key)
		}
		if strings.Contains(l.Message.Subject, "vpn.example.org") {
			t.Errorf("%s: адрес сервера в теме", l.Key)
		}
		if strings.Contains(l.Message.Body, "http") || strings.Contains(l.Message.Body, "://") {
			t.Errorf("%s: ссылка в письме", l.Key)
		}
		if strings.TrimSpace(l.Message.Subject) == "" || strings.TrimSpace(l.Message.Body) == "" {
			t.Errorf("%s: пустое письмо", l.Key)
		}
	}
}

// flapThenTold проводит туннель через серию обрывов по сценарию
// amnezia-vpn-server-76mp.14: падение 03:02, возврат 03:03, падение 03:04,
// письмо «не работает» в 03:09; возврат в 03:30 — четвёртая смена за час,
// письмо «мигает» и тишина до 04:30.
func flapThenTold(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	active := func() { w.clientsActive("a", "b") }
	w.step(time.Minute)
	w.in.TunnelUp = false
	w.step(time.Minute)
	w.in.TunnelUp = true
	w.minutes(1, active)
	w.in.TunnelUp = false
	wantKeys(t, w.minutes(6, nil), keyTunnel)
	wantKeys(t, w.minutes(20, nil))
	w.in.TunnelUp = true
	wantKeys(t, w.step(time.Minute), keyFlap(groupTunnel))
	if w.clock != start.Add(30*time.Minute) {
		t.Fatalf("сценарий сбился: %s", w.clock)
	}
	return w
}

// Письмо о возврате, отложенное тишиной, называет настоящий момент возврата
// и настоящую длительность, а не момент конца тишины
// (amnezia-vpn-server-76mp.14).
func TestTunnelBackAfterMuteNamesRealReturn(t *testing.T) {
	w := flapThenTold(t)
	active := func() { w.clientsActive("a", "b") }
	wantKeys(t, w.minutes(59, active))
	active()
	got := w.step(time.Minute)
	wantKeys(t, got, keyTunnel)
	body := got[0].Message.Body
	for _, want := range []string{"снова работает с 03:30 UTC", "Не работал 26 минут, с 03:04 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("в письме нет %q:\n%s", want, body)
		}
	}
	wantKeys(t, w.minutes(30, active))
}

// Долгий обрыв, начавшийся и кончившийся внутри тишины, не пропадает:
// о нём говорит письмо, которое уходит по её окончании
// (amnezia-vpn-server-76mp.14).
func TestTunnelDropInsideMuteIsToldAfterIt(t *testing.T) {
	w := flapThenTold(t)
	active := func() { w.clientsActive("a", "b") }
	w.minutes(10, active) // 03:40
	w.in.TunnelUp = false
	w.minutes(15, nil) // 03:55
	w.in.TunnelUp = true
	wantKeys(t, w.minutes(34, active)) // до 04:29 — тишина
	active()
	got := w.step(time.Minute)
	wantKeys(t, got, keyTunnel)
	body := got[0].Message.Body
	for _, want := range []string{"снова работает с 03:30 UTC", "15 минут", "с 03:41 UTC до 03:56 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("в письме нет %q:\n%s", want, body)
		}
	}
	wantKeys(t, w.minutes(30, active))
}

// Тишина от коротких провалов, внутри неё — долгий обрыв, о котором ещё не
// писали: по окончании тишины — одно письмо о нём (amnezia-vpn-server-76mp.14).
func TestMissedDropWithoutToldEpisode(t *testing.T) {
	w := newWorld(t)
	active := func() { w.clientsActive("a", "b") }
	w.step(time.Minute)
	for i := 0; i < 2; i++ {
		w.in.TunnelUp = false
		w.minutes(2, nil)
		w.in.TunnelUp = true
		w.minutes(2, active)
	}
	wantKeys(t, w.sent, keyFlap(groupTunnel)) // тишина до 04:08
	w.minutes(5, active)
	w.in.TunnelUp = false
	w.minutes(10, nil) // 03:15–03:25
	w.in.TunnelUp = true
	wantKeys(t, w.minutes(43, active)) // до 04:07 — тишина
	active()
	got := w.step(time.Minute)
	wantKeys(t, got, keyTunnel)
	if !strings.Contains(got[0].Message.Body, "10 минут") || !strings.Contains(got[0].Message.Body, "с 03:15 UTC до 03:25 UTC") {
		t.Errorf("в письме нет обрыва:\n%s", got[0].Message.Body)
	}
	if strings.Contains(got[0].Message.Subject, "снова работает") {
		t.Errorf("тема %q: о возврате писать нечего, о беде не писали", got[0].Message.Subject)
	}
	wantKeys(t, w.minutes(60, active))
}

// Абзац о нескольких обрывах внутри тишины называет их число и самый долгий.
func TestMissedNoteSeveral(t *testing.T) {
	e := &eval{in: Inputs{Zone: time.UTC}, now: start.Add(2 * time.Hour)}
	m := Missed{Count: 3, Since: start.Add(41 * time.Minute), Until: start.Add(56 * time.Minute)}
	want := "Пока писем не было, туннель 3 раза не работал дольше 5 минут. Дольше всего — 15 минут, с 03:41 UTC до 03:56 UTC."
	if got := e.tunnelMissedNote(m); got != want {
		t.Errorf("туннель:\n%s\nждали\n%s", got, want)
	}
	want = "Пока писем не было, все клиенты 3 раза разом пропадали со связи дольше 5 минут. Дольше всего — 15 минут, с 03:41 UTC до 03:56 UTC."
	if got := e.clientsMissedNote(m); got != want {
		t.Errorf("клиенты:\n%s\nждали\n%s", got, want)
	}
}

// Туннель вернулся внутри тишины и снова лёг: по её окончании — письмо о
// возврате того обрыва, о котором писали, затем — о новом
// (amnezia-vpn-server-76mp.14).
func TestTunnelDownAgainWhenMuteEnds(t *testing.T) {
	w := flapThenTold(t)
	active := func() { w.clientsActive("a", "b") }
	w.minutes(50, active) // 04:20
	w.in.TunnelUp = false
	wantKeys(t, w.minutes(9, nil)) // 04:21–04:29
	got := w.step(time.Minute)
	wantKeys(t, got, keyTunnel)
	if !strings.Contains(got[0].Message.Body, "снова работает с 03:30 UTC") {
		t.Errorf("письмо о возврате:\n%s", got[0].Message.Body)
	}
	got = w.step(time.Minute)
	wantKeys(t, got, keyTunnel)
	if !strings.Contains(got[0].Message.Body, "не работает с 04:21 UTC") {
		t.Errorf("письмо о новом обрыве:\n%s", got[0].Message.Body)
	}
}

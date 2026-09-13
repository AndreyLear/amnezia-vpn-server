package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Letter texts. Decided with the owner (dfs2, 12.09.2026):
//
//   - the subject names the trouble only, never the server — the server is
//     in the body;
//   - the version appears only in letters about updates, where it is the
//     content;
//   - no links to the panel: https://IP:8443 with a self-signed certificate
//     is flagged as dangerous by mail clients;
//   - a letter about a failure may end with one short action line.

const checkServices = "Проверьте состояние служб"

var monthsShort = [...]string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

// clock names a moment the way the panel does: the time, and the date when
// it is not today.
func (e *eval) clock(t time.Time) string {
	local, now := t.In(e.in.Zone), e.now.In(e.in.Zone)
	hm := local.Format("15:04 MST")
	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
		return hm
	}
	return fmt.Sprintf("%d %s %s", local.Day(), monthsShort[local.Month()-1], hm)
}

// span says how long something lasted, in whole minutes, at least one.
func span(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 1 {
		m = 1
	}
	if m < 60 {
		return fmt.Sprintf("%d %s", m, plural(m, "минуту", "минуты", "минут"))
	}
	h, rest := m/60, m%60
	out := fmt.Sprintf("%d %s", h, plural(h, "час", "часа", "часов"))
	if rest > 0 {
		out += fmt.Sprintf(" %d %s", rest, plural(rest, "минуту", "минуты", "минут"))
	}
	return out
}

func plural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// body joins paragraphs and appends the server line.
func (e *eval) body(paragraphs ...string) string {
	var parts []string
	for _, p := range paragraphs {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if e.in.Server != "" {
		parts = append(parts, "Сервер: "+e.in.Server)
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func serviceName(service string) string {
	switch service {
	case "dns":
		return "DNS-сервер"
	case "awg":
		return "туннель"
	}
	return service
}

func capitalize(s string) string {
	for i := range s {
		if i > 0 {
			return strings.ToUpper(s[:i]) + s[i:]
		}
	}
	return strings.ToUpper(s)
}

func (e *eval) tunnelDown(since time.Time) mailer.Message {
	return mailer.Message{
		Subject: "Туннель не работает " + span(DownThreshold),
		Body: e.body(
			"Туннель не работает с "+e.clock(since)+". Клиенты без связи.",
			checkServices,
		),
	}
}

func (e *eval) tunnelUp(since time.Time) mailer.Message {
	return mailer.Message{
		Subject: "Туннель снова работает",
		Body: e.body(
			"Туннель снова работает с " + e.clock(e.now) + ". Не работал " + span(e.now.Sub(since)) + ", с " + e.clock(since) + ".",
		),
	}
}

func (e *eval) clientsDown(since time.Time) mailer.Message {
	return mailer.Message{
		Subject: "Все клиенты разом пропали со связи",
		Body: e.body(
			"В "+e.clock(since)+" все клиенты одновременно пропали со связи. За "+span(DownThreshold)+" никто не вернулся. Сам туннель при этом работает.",
			"Так бывает, когда до сервера перестают доходить пакеты: блокировка, сбой у хостера или на пути до него.",
		),
	}
}

func (e *eval) clientsUp(since time.Time) mailer.Message {
	return mailer.Message{
		Subject: "Клиенты снова на связи",
		Body: e.body(
			"Трафик от клиентов снова идёт с " + e.clock(e.now) + ". Его не было " + span(e.now.Sub(since)) + ", с " + e.clock(since) + ".",
		),
	}
}

func (e *eval) restarted(service string, at time.Time, reason string) mailer.Message {
	name := serviceName(service)
	why := ""
	if reason != "" {
		why = " Причина: " + reason + "."
	}
	return mailer.Message{
		Subject: "Сторож перезапустил " + name,
		Body: e.body(
			capitalize(name) + " перестал работать. Сторож перезапустил его в " + e.clock(at) + "." + why + " После перезапуска " + name + " работает.",
		),
	}
}

func (e *eval) stillBroken(service string, at time.Time, reason string) mailer.Message {
	name := serviceName(service)
	why := ""
	if reason != "" {
		why = " Причина: " + reason + "."
	}
	return mailer.Message{
		Subject: capitalize(name) + " не работает после перезапуска",
		Body: e.body(
			"Сторож перезапустил "+name+" в "+e.clock(at)+", но это не помогло."+why,
			checkServices,
		),
	}
}

func (e *eval) serviceBack(service string) mailer.Message {
	name := serviceName(service)
	return mailer.Message{
		Subject: capitalize(name) + " снова работает",
		Body:    e.body(capitalize(name) + " снова работает с " + e.clock(e.now) + "."),
	}
}

func (e *eval) updateOutcome(u *status.UpdateState) mailer.Message {
	switch u.State {
	case "ok":
		return mailer.Message{
			Subject: "Обновление установлено",
			Body:    e.body("Сервер обновлён до версии " + u.To + "."),
		}
	case "rolled-back":
		return mailer.Message{
			Subject: "Обновление не удалось",
			Body: e.body(
				"Обновление до версии " + u.To + " не удалось. Сервер вернулся к версии " + u.From + " и работает.",
			),
		}
	}
	return mailer.Message{
		Subject: "Обновление не удалось, сервер не в порядке",
		Body: e.body(
			"Обновление до версии "+u.To+" не удалось, и вернуть версию "+u.From+" тоже не вышло.",
			checkServices,
		),
	}
}

func (e *eval) releaseAvailable(latest string) mailer.Message {
	return mailer.Message{
		Subject: "Доступна новая версия",
		Body:    e.body("Вышла версия " + latest + ". Сейчас установлена " + e.in.Installed + "."),
	}
}

func (e *eval) flapping(group string, changes int) mailer.Message {
	// changes counts state transitions: one drop of the tunnel is two of
	// them (down, then up), so the episode count is (changes+1)/2. For the
	// resolver each change is already one restart.
	subject := "Туннель мигает"
	n := (changes + 1) / 2
	clause := fmt.Sprintf("туннель пропадал %d %s", n, plural(n, "раз", "раза", "раз"))
	if group == groupDNS {
		subject = "DNS-сервер мигает"
		n = changes
		clause = fmt.Sprintf("сторож перезапускал DNS-сервер %d %s", n, plural(n, "раз", "раза", "раз"))
	}
	return mailer.Message{
		Subject: subject,
		Body: e.body(
			"За последний час "+clause+". Следующий час писем об этом не будет.",
			checkServices,
		),
	}
}

// TestLetter is the test letter sent from the panel's notification settings
// (amnezia-vpn-server-8fg2). It confirms the channel works and says what
// will arrive through it.
func TestLetter(server string) mailer.Message {
	e := &eval{in: Inputs{Server: server, Zone: time.UTC}}
	return mailer.Message{
		Subject: "Пробное письмо",
		Body: e.body(
			"Уведомления настроены. Сюда будут приходить письма о сбоях и об обновлениях.",
		),
	}
}

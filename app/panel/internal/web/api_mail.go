// Настройки почты для уведомлений (amnezia-vpn-server-8fg2, dfs2).
//
// Панель хранит настройки и выводит из них data/mail.conf, но сама наружу не
// ходит: пробное письмо отправляет хостовая служба awgmail по просьбе,
// оставленной рядом с mail.conf, и кладёт итог в status/. Панель лишь
// сравнивает, на какую просьбу пришёл ответ.
//
// Пароль от почтового ящика не возвращается никогда — ни после сохранения,
// ни в каком-либо другом ответе. Пустой пароль при сохранении значит
// «оставить прежний»: иначе каждое исправление адреса требовало бы вводить
// пароль заново.
package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
)

const (
	auditMailSave = "mail.save"
	auditMailTest = "mail.test"
)

// Состояния пробного письма.
const (
	mailTestNone    = "none"
	mailTestPending = "pending"
	mailTestOK      = "ok"
	mailTestFailed  = "failed"
)

type mailTestJSON struct {
	State          string `json:"state"`
	Error          string `json:"error,omitempty"`
	RequestedAtUTC string `json:"requested_at_utc,omitempty"`
	AtUTC          string `json:"at_utc,omitempty"`
}

type mailJSON struct {
	OK          bool   `json:"ok"`
	Configured  bool   `json:"configured"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Recipient   string `json:"recipient"`
	PasswordSet bool   `json:"password_set"`
	// Verified — после последнего сохранения хотя бы одно письмо дошло до
	// почтового сервера. До тех пор настройка «не проверена»: канал, который
	// есть только на бумаге, хуже честного «не настроено».
	Verified bool         `json:"verified"`
	Test     mailTestJSON `json:"test"`
	// Channel — дошли ли бы письма сейчас, по последнему известному исходу
	// (amnezia-vpn-server-pz2r): "off" — почта не настроена, и это не
	// ошибка; "password_missing" — после восстановления нет пароля;
	// "unverified" — после сохранения ещё ничего не отправлялось; "ok" —
	// последнее письмо дошло; "failing" — последнее письмо не ушло.
	Channel string `json:"channel"`
	// LastSuccessAtUTC — когда служба в последний раз отправила письмо
	// (не пробное).
	LastSuccessAtUTC string `json:"last_success_at_utc,omitempty"`
	// LastFailure — письмо, от которого служба отказалась после всех
	// повторов. Пусто, если с тех пор письмо дошло.
	LastFailure *mailFailureJSON `json:"last_failure,omitempty"`
}

type mailFailureJSON struct {
	Subject string `json:"subject"`
	Error   string `json:"error"`
	AtUTC   string `json:"at_utc"`
}

const (
	mailChannelOff             = "off"
	mailChannelPasswordMissing = "password_missing"
	mailChannelUnverified      = "unverified"
	mailChannelOK              = "ok"
	mailChannelFailing         = "failing"
)

func (s *Server) mailTestRequestPath() string {
	return mailconf.TestRequestPath(s.cfg.MailConfPath)
}

func (s *Server) mailView() (mailJSON, error) {
	out := mailJSON{OK: true, Port: db.MailDefaultPort, Test: mailTestJSON{State: mailTestNone}, Channel: mailChannelOff}
	settings, err := db.LoadMailSettings(s.db())
	if errors.Is(err, db.ErrMailNotConfigured) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Configured = true
	out.Host, out.Port, out.Username, out.Recipient = settings.Host, settings.Port, settings.Username, settings.Recipient
	out.PasswordSet = !settings.PasswordMissing()

	req, _ := mailconf.ReadTestRequest(s.mailTestRequestPath())
	res, _ := mailconf.ReadTestResult(filepath.Join(s.statusDir(), mailconf.TestResultName))
	if req != nil {
		out.Test.RequestedAtUTC = req.AtUTC.Format(time.RFC3339)
		switch {
		case res == nil || res.ID != req.ID:
			out.Test.State = mailTestPending
		case res.OK:
			out.Test.State = mailTestOK
			out.Test.AtUTC = res.AtUTC.Format(time.RFC3339)
		default:
			out.Test.State = mailTestFailed
			out.Test.Error = res.Error
			out.Test.AtUTC = res.AtUTC.Format(time.RFC3339)
		}
	}

	st, _ := mailer.LoadState(filepath.Join(s.statusDir(), "mail-state.json"))
	if st != nil && st.LastSuccessAt != nil {
		out.LastSuccessAtUTC = st.LastSuccessAt.UTC().Format(time.RFC3339)
	}
	if st != nil && st.LastFailure != nil {
		out.LastFailure = &mailFailureJSON{
			Subject: st.LastFailure.Subject,
			Error:   st.LastFailure.Error,
			AtUTC:   st.LastFailure.At.UTC().Format(time.RFC3339),
		}
	}

	if !out.PasswordSet {
		out.Channel = mailChannelPasswordMissing
		return out, nil
	}

	// Исходы, случившиеся после последнего сохранения: прежние говорят о
	// прежних настройках. Решает самый поздний из них.
	saved := settings.UpdatedAt
	type outcome struct {
		at time.Time
		ok bool
	}
	var outcomes []outcome
	if req != nil && res != nil && res.ID == req.ID && !req.AtUTC.Before(saved) {
		outcomes = append(outcomes, outcome{res.AtUTC, res.OK})
	}
	if st != nil && st.LastSuccessAt != nil && !st.LastSuccessAt.Before(saved) {
		outcomes = append(outcomes, outcome{*st.LastSuccessAt, true})
	}
	if st != nil && st.LastFailure != nil && !st.LastFailure.At.Before(saved) {
		outcomes = append(outcomes, outcome{st.LastFailure.At, false})
	}
	out.Channel = mailChannelUnverified
	var latest time.Time
	for _, o := range outcomes {
		if o.ok {
			out.Verified = true
		}
		if !o.at.Before(latest) {
			latest = o.at
			if o.ok {
				out.Channel = mailChannelOK
			} else {
				out.Channel = mailChannelFailing
			}
		}
	}
	return out, nil
}

func (s *Server) apiMail(w http.ResponseWriter, r *http.Request) {
	view, err := s.mailView()
	if err != nil {
		internalFailure(w, r, s, "api mail", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type mailSaveRequest struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Recipient string `json:"recipient"`
}

// mailFieldError — отказ, привязанный к полю: окно подсвечивает поле и
// показывает текст под ним. Значение поля в ответ не попадает.
func mailFieldError(w http.ResponseWriter, field, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "field": field, "message": message})
}

func (s *Server) apiMailSave(w http.ResponseWriter, r *http.Request) {
	var req mailSaveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	next := db.MailSettings{
		Host:      strings.TrimSpace(req.Host),
		Port:      req.Port,
		Username:  strings.TrimSpace(req.Username),
		Password:  req.Password,
		Recipient: strings.TrimSpace(req.Recipient),
	}
	if next.Port == 0 {
		next.Port = db.MailDefaultPort
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if next.Password == "" {
		if prev, err := db.LoadMailSettings(s.db()); err == nil {
			next.Password = prev.Password
		}
	}
	switch {
	case next.Host == "" || strings.ContainsAny(next.Host, "\r\n /@"):
		mailFieldError(w, "host", "Укажите адрес почтового сервера")
		return
	case next.Port < 1 || next.Port > 65535:
		mailFieldError(w, "port", "Порт — число от 1 до 65535")
		return
	case next.Username == "" || strings.ContainsAny(next.Username, "\r\n"):
		mailFieldError(w, "username", "Укажите логин")
		return
	case next.Password == "":
		mailFieldError(w, "password", "Введите пароль")
		return
	case strings.ContainsAny(next.Password, "\r\n"):
		mailFieldError(w, "password", "Пароль не может содержать перевод строки")
		return
	case !plausibleAddress(next.Recipient):
		mailFieldError(w, "recipient", "Укажите адрес почты, например name@example.com")
		return
	}

	if err := db.SaveMailSettings(s.db(), next, time.Now()); err != nil {
		internalFailure(w, r, s, "api mail save", err)
		return
	}
	if err := db.RenderMailConf(s.db(), s.cfg.MailConfPath); err != nil {
		internalFailure(w, r, s, "api mail render", err)
		return
	}
	s.audit(r, auditMailSave, next.Recipient, "")
	// Пробное письмо уходит при каждом сохранении: не дошло — настройки всё
	// равно сохранены, а окно покажет отказ.
	if _, err := mailconf.WriteTestRequest(s.mailTestRequestPath(), time.Now()); err != nil {
		s.cfg.Logger.Printf("api mail test request: %v", err)
	}
	s.writeMailView(w, r)
}

func (s *Server) apiMailTest(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	settings, err := db.LoadMailSettings(s.db())
	if errors.Is(err, db.ErrMailNotConfigured) || (err == nil && settings.PasswordMissing()) {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "message": "Сначала сохраните настройки почты"})
		return
	}
	if err != nil {
		internalFailure(w, r, s, "api mail test", err)
		return
	}
	if _, err := mailconf.WriteTestRequest(s.mailTestRequestPath(), time.Now()); err != nil {
		internalFailure(w, r, s, "api mail test request", err)
		return
	}
	s.audit(r, auditMailTest, settings.Recipient, "")
	s.writeMailView(w, r)
}

func (s *Server) writeMailView(w http.ResponseWriter, r *http.Request) {
	view, err := s.mailView()
	if err != nil {
		internalFailure(w, r, s, "api mail view", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// plausibleAddress — адрес, похожий на почтовый: одна @, непустые части,
// точка в домене, без пробелов и переводов строки. Полную проверку делает
// почтовый сервер, и пробное письмо её покажет.
func plausibleAddress(addr string) bool {
	if strings.ContainsAny(addr, " \t\r\n<>,;") {
		return false
	}
	local, domain, ok := strings.Cut(addr, "@")
	return ok && local != "" && !strings.Contains(domain, "@") &&
		strings.Contains(strings.Trim(domain, "."), ".")
}

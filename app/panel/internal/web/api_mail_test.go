package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
)

const mailSecret = "mailbox-password-never-in-response"

func mailBody(password string) map[string]any {
	return map[string]any{
		"host": "smtp.example.org", "port": 587, "username": "vpn@example.org",
		"password": password, "recipient": "owner@example.org",
	}
}

func decodeMail(t *testing.T, rec *httptest.ResponseRecorder) mailJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out mailJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	return out
}

func (f *fixture) answerTest(ok bool, errText string) {
	f.t.Helper()
	req, err := mailconf.ReadTestRequest(f.server.mailTestRequestPath())
	if err != nil || req == nil {
		f.t.Fatalf("нет просьбы о пробном письме: %v", err)
	}
	res := &mailconf.TestResult{ID: req.ID, OK: ok, Error: errText, AtUTC: time.Now().UTC()}
	if err := mailconf.WriteTestResult(filepath.Join(f.server.statusDir(), mailconf.TestResultName), res); err != nil {
		f.t.Fatal(err)
	}
}

// Почта не настроена — это не ошибка, а «не настроено»
// (amnezia-vpn-server-8fg2, pz2r).
func TestAPIMailNotConfigured(t *testing.T) {
	f := newFixture(t)
	got := decodeMail(t, f.get("/api/mail"))
	if got.Configured || got.PasswordSet || got.Verified || got.Test.State != mailTestNone || got.Port != 587 {
		t.Fatalf("не настроено: %+v", got)
	}
}

// Сохранение: настройки доезжают до хоста (mail.conf), просьба о пробном
// письме оставлена, пароль не возвращается ни в каком ответе.
func TestAPIMailSave(t *testing.T) {
	f := newFixture(t)
	rec := f.apiCSRF(http.MethodPut, "/api/mail", mailBody(mailSecret))
	if strings.Contains(rec.Body.String(), mailSecret) {
		t.Fatal("пароль в ответе на сохранение")
	}
	got := decodeMail(t, rec)
	if !got.Configured || !got.PasswordSet || got.Test.State != mailTestPending || got.Verified {
		t.Fatalf("после сохранения %+v", got)
	}
	conf, err := mailconf.Load(f.server.cfg.MailConfPath)
	if err != nil || conf.Password != mailSecret || conf.Recipient != "owner@example.org" {
		t.Fatalf("mail.conf: %+v, %v", conf, err)
	}
	if raw := f.get("/api/mail").Body.String(); strings.Contains(raw, mailSecret) {
		t.Fatal("пароль в GET /api/mail")
	}
	entries, err := db.AuditTail(f.h, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action == auditMailSave {
			found = true
			if strings.Contains(e.Subject+e.Detail, mailSecret) {
				t.Fatal("пароль в журнале")
			}
		}
	}
	if !found {
		t.Error("сохранение не попало в журнал")
	}
}

// Пустой пароль при повторном сохранении — «оставить прежний».
func TestAPIMailSaveKeepsPassword(t *testing.T) {
	f := newFixture(t)
	decodeMail(t, f.apiCSRF(http.MethodPut, "/api/mail", mailBody(mailSecret)))
	body := mailBody("")
	body["recipient"] = "other@example.org"
	got := decodeMail(t, f.apiCSRF(http.MethodPut, "/api/mail", body))
	if !got.PasswordSet || got.Recipient != "other@example.org" {
		t.Fatalf("%+v", got)
	}
	if conf, _ := mailconf.Load(f.server.cfg.MailConfPath); conf == nil || conf.Password != mailSecret {
		t.Fatal("пароль потерян при сохранении без пароля")
	}
}

func TestAPIMailSaveValidation(t *testing.T) {
	cases := map[string]struct {
		mutate func(map[string]any)
		field  string
	}{
		"без пароля в первый раз": {func(b map[string]any) { b["password"] = "" }, "password"},
		"пустой хост":             {func(b map[string]any) { b["host"] = " " }, "host"},
		"перевод строки в хосте":  {func(b map[string]any) { b["host"] = "smtp.example.org\r\nX" }, "host"},
		"порт вне диапазона":      {func(b map[string]any) { b["port"] = 70000 }, "port"},
		"пустой логин":            {func(b map[string]any) { b["username"] = "" }, "username"},
		"адрес без @":             {func(b map[string]any) { b["recipient"] = "owner.example.org" }, "recipient"},
		"адрес с заголовком":      {func(b map[string]any) { b["recipient"] = "a@b.org\r\nBcc: c@d.org" }, "recipient"},
		"пароль с переводом":      {func(b map[string]any) { b["password"] = "a\nb" + mailSecret }, "password"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			body := mailBody(mailSecret)
			tc.mutate(body)
			rec := f.apiCSRF(http.MethodPut, "/api/mail", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, body=%s", rec.Code, rec.Body.String())
			}
			var out map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if out["field"] != tc.field {
				t.Errorf("field = %v, ждали %s", out["field"], tc.field)
			}
			if strings.Contains(rec.Body.String(), mailSecret) {
				t.Error("ответ повторяет пароль")
			}
			if _, err := os.Stat(f.server.cfg.MailConfPath); err == nil {
				t.Error("отвергнутые настройки дошли до mail.conf")
			}
		})
	}
}

// Пробное письмо: ответ хоста сопоставляется с просьбой по id; удача
// делает настройку проверенной, неудача показывает ответ сервера, новое
// сохранение снова делает её непроверенной.
func TestAPIMailTestOutcome(t *testing.T) {
	f := newFixture(t)
	decodeMail(t, f.apiCSRF(http.MethodPut, "/api/mail", mailBody(mailSecret)))

	f.answerTest(false, "mailer: login as vpn@example.org: 535 5.7.8 Authentication failed")
	got := decodeMail(t, f.get("/api/mail"))
	if got.Test.State != mailTestFailed || !strings.Contains(got.Test.Error, "535") || got.Verified {
		t.Fatalf("неудача: %+v", got)
	}

	decodeMail(t, f.apiCSRF(http.MethodPost, "/api/mail/test", nil))
	got = decodeMail(t, f.get("/api/mail"))
	if got.Test.State != mailTestPending {
		t.Fatalf("новая просьба, а ответ старый засчитан: %+v", got.Test)
	}
	f.answerTest(true, "")
	got = decodeMail(t, f.get("/api/mail"))
	if got.Test.State != mailTestOK || !got.Verified {
		t.Fatalf("удача: %+v", got)
	}

	// Новое сохранение — новая проверка.
	time.Sleep(10 * time.Millisecond)
	got = decodeMail(t, f.apiCSRF(http.MethodPut, "/api/mail", mailBody("")))
	if got.Verified || got.Test.State != mailTestPending {
		t.Fatalf("после пересохранения %+v", got)
	}
}

// Удачный ответ на просьбу, оставленную раньше последнего сохранения, не
// подтверждает новые настройки: письмо ушло по прежним.
func TestAPIMailOldTestDoesNotVerifyNewSettings(t *testing.T) {
	f := newFixture(t)
	decodeMail(t, f.apiCSRF(http.MethodPut, "/api/mail", mailBody(mailSecret)))
	req := &mailconf.TestRequest{ID: "old", AtUTC: time.Now().Add(-time.Hour).UTC()}
	data, _ := json.Marshal(req)
	if err := os.WriteFile(f.server.mailTestRequestPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.answerTest(true, "")
	if got := decodeMail(t, f.get("/api/mail")); got.Verified {
		t.Fatalf("старое пробное письмо подтвердило новые настройки: %+v", got)
	}
}

// Настройку подтверждает и обычное письмо, ушедшее после сохранения.
func TestAPIMailVerifiedByDeliveredLetter(t *testing.T) {
	f := newFixture(t)
	decodeMail(t, f.apiCSRF(http.MethodPut, "/api/mail", mailBody(mailSecret)))
	st := &mailer.State{}
	at := time.Now().Add(time.Minute).UTC()
	st.LastSuccessAt = &at
	if err := st.Save(filepath.Join(f.server.statusDir(), "mail-state.json")); err != nil {
		t.Fatal(err)
	}
	if got := decodeMail(t, f.get("/api/mail")); !got.Verified {
		t.Fatalf("%+v", got)
	}
}

// Пробное письмо без сохранённых настроек или без пароля (после
// восстановления) не запрашивается.
func TestAPIMailTestNeedsSettings(t *testing.T) {
	f := newFixture(t)
	if rec := f.apiCSRF(http.MethodPost, "/api/mail/test", nil); rec.Code != http.StatusConflict {
		t.Fatalf("не настроено: code = %d", rec.Code)
	}
	if err := db.SaveMailSettings(f.h, db.MailSettings{Host: "smtp.example.org", Port: 587, Username: "u@example.org", Recipient: "o@example.org"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := decodeMail(t, f.get("/api/mail"))
	if !got.Configured || got.PasswordSet {
		t.Fatalf("после восстановления %+v", got)
	}
	if rec := f.apiCSRF(http.MethodPost, "/api/mail/test", nil); rec.Code != http.StatusConflict {
		t.Fatalf("без пароля: code = %d", rec.Code)
	}
	if _, err := os.Stat(f.server.mailTestRequestPath()); err == nil {
		t.Fatal("просьба оставлена без пароля")
	}
}

func TestAPIMailRequiresCSRF(t *testing.T) {
	f := newFixture(t)
	req := apiJSON(t, http.MethodPut, "/api/mail", mailBody(mailSecret))
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("без CSRF: code = %d", rec.Code)
	}
}

func TestPlausibleAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"owner@example.org": true, "a.b+c@mail.example.co": true,
		"owner@localhost": false, "owner.example.org": false, "@example.org": false,
		"a@b@example.org": false, "a b@example.org": false, "a@example.org.": true,
	} {
		if got := plausibleAddress(addr); got != want {
			t.Errorf("plausibleAddress(%q) = %v", addr, got)
		}
	}
}

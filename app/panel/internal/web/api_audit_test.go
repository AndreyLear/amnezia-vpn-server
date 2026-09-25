package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Журнал панели (amnezia-vpn-server-gqep): при одном администраторе это
// защита от «я такого не делал».
func TestAuditRecordsWhatWasDoneAndByWhom(t *testing.T) {
	f := newFixture(t)

	rec := f.postBody("/api/clients", `{"name":"alice"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("создание клиента: код %d: %s", rec.Code, rec.Body.String())
	}

	got := decodeAPI(t, f.get("/api/audit"))
	entries, _ := got["entries"].([]any)
	if len(entries) == 0 {
		t.Fatalf("журнал пуст: %v", got)
	}
	first, _ := entries[0].(map[string]any)
	if first["action"] != "client.add" || first["subject"] != "alice" {
		t.Fatalf("запись = %v", first)
	}
	if first["actor"] == "" {
		t.Fatalf("запись без автора: %v", first)
	}
	if first["at_utc"] == "" {
		t.Fatalf("запись без времени: %v", first)
	}
}

// Секретам в журнале не место: он должен пережить кражу базы, не добавив
// вору ничего сверх того, что он в ней и так нашёл.
func TestAuditNeverStoresSecrets(t *testing.T) {
	f := newFixture(t)
	f.postBody("/api/clients", `{"name":"alice"}`)

	rec := f.get("/api/audit")
	body := rec.Body.String()
	// Ключи клиента лежат в базе рядом — и не должны попасть в журнал.
	var priv, pub, psk string
	if err := f.h.QueryRow(
		`SELECT private_key, public_key, COALESCE(preshared_key, '') FROM clients LIMIT 1`,
	).Scan(&priv, &pub, &psk); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{priv, pub, psk} {
		if secret == "" {
			continue
		}
		if strings.Contains(body, secret) {
			t.Fatalf("в журнал попал секрет клиента")
		}
	}
}

// Отказ журнала не должен превращаться в отказ панели: он существует ради
// разбирательств, а не вместо работы.
func TestAuditFailureDoesNotBreakTheAction(t *testing.T) {
	f := newFixture(t)
	if _, err := f.h.Exec(`DROP TABLE audit`); err != nil {
		t.Fatal(err)
	}
	rec := f.postBody("/api/clients", `{"name":"bob"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("клиент не создан из-за журнала: код %d: %s", rec.Code, rec.Body.String())
	}
}

// Восстановление из копии — самое разрушительное действие панели, и до сих
// пор оно было единственным незаписанным (amnezia-vpn-server-y5y2).
//
// Запись обязана попасть в ВОССТАНОВЛЕННУЮ базу: журнал лежит в той же
// базе, и запись, сделанная до подмены, исчезла бы вместе со старой — то
// есть восстановление стёрло бы след самого себя.
func TestRestoreIsRecordedInTheRestoredDatabase(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	// Копия снимается с нынешней базы, в журнале которой уже что-то есть.
	f.addClient("alice")
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}

	rec := postRestoreUpload(t, f, restoreFields(f), map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("восстановление: код %d, ожидался 303", rec.Code)
	}

	// Читаем из базы на диске, а не через панель: восстановление гасит
	// сессию (пользователи могли приехать из копии другими), и это верно.
	// Проверять надо не доступ, а что запись легла в НОВУЮ базу.
	handle, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	entries, err := db.AuditTail(handle, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("журнал восстановленной базы пуст")
	}
	if entries[0].Action != "backup.restore" {
		t.Fatalf("восстановление не записано, первая запись = %+v", entries[0])
	}
	if entries[0].Actor == "" {
		t.Fatalf("запись без автора: %+v", entries[0])
	}
	if !strings.Contains(entries[0].Detail, "клиентов применено") {
		t.Fatalf("не сказано, сколько применено: %+v", entries[0])
	}
}

// lastAudit — последняя запись журнала с этим действием.
func lastAudit(t *testing.T, f *fixture, action string) (actor string, ok bool) {
	t.Helper()
	err := f.h.QueryRow(
		`SELECT actor FROM audit WHERE action = ? ORDER BY id DESC LIMIT 1`, action,
	).Scan(&actor)
	return actor, err == nil
}

// Неудачный вход пишет в журнал введённое имя. Без предела длины его можно
// было раздуть до десятков килобайт на запись и вытеснить настоящие записи,
// заодно сделав бэкап больше, чем панель согласна восстановить
// (amnezia-vpn-server-76mp.11).
func TestAuditFailedLoginNameIsBounded(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("я", 30000)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiLoginFrom(t, "203.0.113.90:1", long, "wrong-password"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	actor, ok := lastAudit(t, f, auditLoginFailed)
	if !ok {
		t.Fatal("login.failed not recorded")
	}
	if n := utf8.RuneCountInString(actor); n > auditActorMaxRunes+1 {
		t.Fatalf("actor length = %d runes, want <= %d", n, auditActorMaxRunes+1)
	}
	if !utf8.ValidString(actor) {
		t.Fatal("truncated actor must stay valid UTF-8")
	}
	if !strings.HasPrefix(actor, strings.Repeat("я", auditActorMaxRunes)) {
		t.Fatalf("actor must keep the beginning of the name: %q", actor)
	}
}

// Вход через HTML-форму POST /login пишется в журнал так же, как через
// /api/login (amnezia-vpn-server-76mp.28).
func TestAuditFormLoginIsRecorded(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	bad := postLogin(t, f, loginRequest(t, "203.0.113.91:1",
		url.Values{"username": {"alice"}, "password": {"wrong-password"}}))
	if bad.Code != http.StatusOK {
		t.Fatalf("wrong password: code = %d, want 200", bad.Code)
	}
	if actor, ok := lastAudit(t, f, auditLoginFailed); !ok || actor != "alice" {
		t.Fatalf("login.failed = %q (recorded %v), want alice", actor, ok)
	}

	good := postLogin(t, f, loginRequest(t, "203.0.113.91:1",
		url.Values{"username": {"alice"}, "password": {testPassword}}))
	if good.Code != http.StatusSeeOther {
		t.Fatalf("correct password: code = %d, want 303", good.Code)
	}
	if actor, ok := lastAudit(t, f, auditLogin); !ok || actor != "alice" {
		t.Fatalf("login = %q (recorded %v), want alice", actor, ok)
	}
	var detail string
	if err := f.h.QueryRow(`SELECT group_concat(detail, '') FROM audit`).Scan(&detail); err == nil &&
		strings.Contains(detail, "wrong-password") {
		t.Fatal("the typed password must never reach the journal")
	}
}

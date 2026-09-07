package web

import (
	"net/http"
	"strings"
	"testing"
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

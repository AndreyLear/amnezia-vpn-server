// M6.3 mutation tests (M6_AUDIT.md §9): every POST answers 303 with a
// fixed flash, the database reflects the mutation, config/awg0.conf is
// deterministically regenerated (byte-equal to awgconf.Generate), and
// errors answer 303 flag messages except real internals which answer
// generic 500. Includes the concurrency smoke check from §9.
package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// post sends a form-encoded POST to path with the fixture session's
// real CSRF token (M7.6) and returns the recorder.
func (f *fixture) post(path string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form = cloneValues(form)
	form.Set(auth.CSRFFieldName, f.csrf)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

// cloneValues copies a url.Values map so tests can reuse their fixture
// form definitions across posts.
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

// redirectLocation returns the Location of a 303 response.
func (f *fixture) redirectLocation(rec *httptest.ResponseRecorder) string {
	f.t.Helper()
	if rec.Code != http.StatusSeeOther {
		f.t.Fatalf("code = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func (f *fixture) flashOf(rec *httptest.ResponseRecorder) string {
	f.t.Helper()
	loc := f.redirectLocation(rec)
	u, err := url.Parse(loc)
	if err != nil {
		f.t.Fatalf("bad Location %q: %v", loc, err)
	}
	if u.Path != "/" {
		f.t.Fatalf("Location path = %q, want /", u.Path)
	}
	return u.Query().Get("msg")
}

// configMatches asserts the regenerated awg0.conf equals a fresh
// awgconf.Generate rendering of the same database.
func (f *fixture) configMatches() {
	f.t.Helper()
	got, err := os.ReadFile(f.confPath)
	if err != nil {
		f.t.Fatalf("read awg0.conf: %v", err)
	}
	want := filepath.Join(f.t.TempDir(), "expected.conf")
	if err := awgconf.Generate(f.h, want); err != nil {
		f.t.Fatalf("expected generate: %v", err)
	}
	exp, err := os.ReadFile(want)
	if err != nil {
		f.t.Fatalf("read expected: %v", err)
	}
	if string(got) != string(exp) {
		f.t.Errorf("awg0.conf differs from awgconf.Generate")
	}
}

func TestMutationAddClient(t *testing.T) {
	f := newFixture(t)
	rec := f.post("/clients/new", url.Values{"name": {"carol"}})
	if got := f.flashOf(rec); got != flashAdded {
		t.Fatalf("flash = %q, want %q", got, flashAdded)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 1 || clients[0].Name != "carol" || !clients[0].Enabled {
		t.Fatalf("client row wrong: %+v", clients)
	}
	f.configMatches()
}

func TestMutationAddTrimsNameWithoutExpiry(t *testing.T) {
	f := newFixture(t)
	rec := f.post("/clients/new", url.Values{"name": {"  dave  "}})
	if got := f.flashOf(rec); got != flashAdded {
		t.Fatalf("flash = %q", got)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1", len(clients))
	}
	if clients[0].Name != "dave" {
		t.Errorf("name = %q, want trimmed dave", clients[0].Name)
	}
	if clients[0].ExpiresAt != "" {
		t.Errorf("ExpiresAt = %q, want empty (web add does not set expiry)", clients[0].ExpiresAt)
	}
}

func TestMutationAddIgnoresExpiresAt(t *testing.T) {
	f := newFixture(t)
	expiry := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	rec := f.post("/clients/new", url.Values{"name": {"dave"}, "expires_at": {expiry}})
	if got := f.flashOf(rec); got != flashAdded {
		t.Fatalf("flash = %q, want %q", got, flashAdded)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1", len(clients))
	}
	if clients[0].ExpiresAt != "" {
		t.Errorf("ExpiresAt = %q, want empty when expires_at is posted on /clients/new", clients[0].ExpiresAt)
	}
}

func TestMutationAddFailures(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name      string
		fields    url.Values
		wantFlash string
	}{
		{"empty name", url.Values{"name": {""}}, flashInvalidName},
		{"whitespace name", url.Values{"name": {"   "}}, flashInvalidName},
		{"long name", url.Values{"name": {strings.Repeat("x", 65)}}, flashInvalidName},
		{"garbage expires_at ignored", url.Values{"name": {"eve"}, "expires_at": {"tomorrow"}}, flashAdded},
	}
	for _, tc := range cases {
		rec := f.post("/clients/new", tc.fields)
		if got := f.flashOf(rec); got != tc.wantFlash {
			t.Errorf("%s: flash = %q, want %q", tc.name, got, tc.wantFlash)
		}
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 1 || clients[0].Name != "eve" {
		t.Fatalf("only the ignored-expires_at add must insert: %+v", clients)
	}
}

func TestMutationAddDuplicateNameRejected(t *testing.T) {
	f := newFixture(t)
	if got := f.flashOf(f.post("/clients/new", url.Values{"name": {"twin"}})); got != flashAdded {
		t.Fatalf("first add: flash = %q", got)
	}
	rec := f.post("/clients/new", url.Values{"name": {"twin"}})
	if got := f.flashOf(rec); got != flashNameTaken {
		t.Fatalf("duplicate add: flash = %q, want %q", got, flashNameTaken)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("duplicate add created a row: %d clients", len(clients))
	}
}

func TestMutationEnableDisable(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("frank")
	if !c.Enabled {
		t.Fatal("fixture client must start enabled")
	}
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/disable", c.ID), nil)); got != flashDisabled {
		t.Fatalf("disable flash = %q", got)
	}
	row, err := db.ClientByID(f.h, c.ID)
	if err != nil || row.Enabled {
		t.Fatalf("client still enabled: %+v err=%v", row, err)
	}
	f.configMatches()
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/enable", c.ID), nil)); got != flashEnabled {
		t.Fatalf("enable flash = %q", got)
	}
	row, err = db.ClientByID(f.h, c.ID)
	if err != nil || !row.Enabled {
		t.Fatalf("client not re-enabled: %+v err=%v", row, err)
	}
	f.configMatches()
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/enable", c.ID), nil)); got != flashEnabled {
		t.Fatalf("idempotent enable flash = %q", got)
	}
}

func TestMutationDelete(t *testing.T) {
	f := newFixture(t)
	f.addClient("grace")
	f.addClient("heidi")
	clients, _ := db.ClientsAll(f.h)
	if len(clients) != 2 {
		t.Fatalf("setup: %d clients", len(clients))
	}
	// delete the second one: the first address must not be reused
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/delete", clients[1].ID), nil)); got != flashDeleted {
		t.Fatalf("delete flash = %q", got)
	}
	f.configMatches()
	remaining, err := db.ClientsAll(f.h)
	if err != nil || len(remaining) != 1 || remaining[0].ID != clients[0].ID {
		t.Fatalf("after delete: %+v err=%v", remaining, err)
	}
}

func TestMutationDeleteUnknownID(t *testing.T) {
	f := newFixture(t)
	if got := f.flashOf(f.post("/clients/new", url.Values{"name": {"mallory"}})); got != flashAdded {
		t.Fatalf("setup add: flash = %q", got)
	}
	if got := f.flashOf(f.post("/clients/999/delete", nil)); got != flashNotFound {
		t.Fatalf("flash = %q, want %q (explicit error, not silent no-op)", got, flashNotFound)
	}
	f.configMatches()
}

func TestMutationRename(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("ivan")
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/rename", c.ID), url.Values{"name": {"ivan-the-terrible"}})); got != flashRenamed {
		t.Fatalf("rename flash = %q", got)
	}
	row, err := db.ClientByID(f.h, c.ID)
	if err != nil || row.Name != "ivan-the-terrible" {
		t.Fatalf("name wrong: %+v err=%v", row, err)
	}
	f.configMatches()
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/rename", c.ID), url.Values{"name": {""}})); got != flashInvalidName {
		t.Fatalf("empty rename flash = %q, want %q", got, flashInvalidName)
	}
}

func TestMutationExpiry(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("judy")
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/expiry", c.ID), url.Values{"expires_at": {future}})); got != flashExpirySet {
		t.Fatalf("expiry flash = %q", got)
	}
	row, err := db.ClientByID(f.h, c.ID)
	if err != nil || row.ExpiresAt == "" {
		t.Fatalf("expiry not set: %+v err=%v", row, err)
	}
	f.configMatches()
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/expiry", c.ID), url.Values{"expires_at": {"none"}})); got != flashExpirySet {
		t.Fatalf("clear flash = %q", got)
	}
	row, _ = db.ClientByID(f.h, c.ID)
	if row.ExpiresAt != "" {
		t.Errorf("expiry not cleared: %q", row.ExpiresAt)
	}
	// T-120 round 2 §8: an empty value also means "no deadline".
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/expiry", c.ID), url.Values{"expires_at": {future}})); got != flashExpirySet {
		t.Fatalf("re-set flash = %q", got)
	}
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/expiry", c.ID), url.Values{"expires_at": {""}})); got != flashExpirySet {
		t.Fatalf("empty expiry flash = %q, want %q", got, flashExpirySet)
	}
	row, _ = db.ClientByID(f.h, c.ID)
	if row.ExpiresAt != "" {
		t.Errorf("empty expiry must clear: %q", row.ExpiresAt)
	}
	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/expiry", c.ID), url.Values{"expires_at": {"not-a-date"}})); got != flashInvalidExpiry {
		t.Fatalf("bad expiry flash = %q, want %q", got, flashInvalidExpiry)
	}
}

// TestMutationExpiryDatetimeLocal (T-120 round 2 §8): the browser's
// datetime-local shape (YYYY-MM-DDTHH:MM) is accepted and stored as
// the canonical UTC RFC3339 timestamp.
func TestMutationExpiryDatetimeLocal(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("kate")
	rec := f.post(fmt.Sprintf("/clients/%d/expiry", c.ID), url.Values{"expires_at": {"2026-09-01T18:30"}})
	if got := f.flashOf(rec); got != flashExpirySet {
		t.Fatalf("flash = %q, want %q", got, flashExpirySet)
	}
	row, err := db.ClientByID(f.h, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-09-01T18:30:00Z"; row.ExpiresAt != want {
		t.Fatalf("ExpiresAt = %q, want canonical UTC %q", row.ExpiresAt, want)
	}
}

func TestMutationInvalidIDAnswerInvalidFlash(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/clients/abc/delete",
		"/clients/0/delete",
		"/clients/-3/delete",
		"/clients/1.5/delete",
	} {
		if got := f.flashOf(f.post(path, nil)); got != flashInvalidID {
			t.Errorf("%s: flash = %q, want %q", path, got, flashInvalidID)
		}
	}
}

// TestMutationWrongMethod404 asserts a wrong method on a mutation
// route falls through to the generic 404 page (all methods not wired
// land on the universal fallback, so no method/response is detected by
// the mux) without echoing the path.
func TestMutationWrongMethod404(t *testing.T) {
	f := newFixture(t)
	f.addClient("nina")
	for _, req := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/clients/1/delete"},
		{http.MethodGet, "/clients/new"},
		{http.MethodPut, "/clients/1/enable"},
	} {
		rec := httptest.NewRecorder()
		f.serve(rec, httptest.NewRequest(req.method, req.path, nil))
		if req.method == http.MethodGet {
			assertSPA(t, rec)
			continue
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: code = %d, want 404", req.method, req.path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), req.path) {
			t.Errorf("%s %s: 404 body echoes the path", req.method, req.path)
		}
	}
}

func TestMutationOversizedBody413(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("x", MaxBodyBytes+1)
	rec := f.post("/clients/new", url.Values{"name": {big}})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized add: code = %d, want 413", rec.Code)
	}
	clients, _ := db.ClientsAll(f.h)
	if len(clients) != 0 {
		t.Errorf("413 request must not insert: %d clients", len(clients))
	}
}

func TestMutationFlashNoSecretsAndEscaped(t *testing.T) {
	f := newFixture(t)
	_, priv, psk := f.addClient("mallory")
	if got := f.flashOf(f.post("/clients/999/expiry", url.Values{"expires_at": {"x"}})); got != flashInvalidExpiry {
		t.Fatalf("flash = %q", got)
	}
	body := f.get("/?msg=" + url.QueryEscape("<script>alert(1)</script>")).Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, priv) || strings.Contains(body, psk) {
		t.Fatalf("flash/keys leaked")
	}
}

// TestMutationConcurrency is the M6_AUDIT.md §9 concurrency smoke: N
// parallel adds allocate unique addresses, awg0.conf stays valid
// (byte-equal to a fresh generate) and concurrent page loads are 200.
func TestMutationConcurrency(t *testing.T) {
	f := newFixture(t)
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := f.post("/clients/new", url.Values{"name": {fmt.Sprintf("bulk-%02d", i)}})
			if rec.Code != http.StatusSeeOther {
				errs <- fmt.Sprintf("add %d: code %d", i, rec.Code)
			}
			if g := f.get("/"); g.Code != http.StatusOK {
				errs <- fmt.Sprintf("get during add %d: code %d", i, g.Code)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != n {
		t.Fatalf("clients = %d, want %d", len(clients), n)
	}
	seen := map[string]bool{}
	for _, c := range clients {
		if seen[c.Address] {
			t.Fatalf("duplicate address %s", c.Address)
		}
		seen[c.Address] = true
	}
	f.configMatches()
}

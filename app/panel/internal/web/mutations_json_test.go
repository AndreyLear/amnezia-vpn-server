// T-120 round 2 §12: the fetch/JSON mutation channel. app.js sends
// mutations with X-Requested-With + Accept: application/json; the
// server answers JSON ({"ok","message","html","count"}) instead of the
// 303 PRG redirect, and the HTML fragment carries the fresh card
// rendered by html/template (autoscaped). Plain form POSTs keep the
// 303 flow — covered by the existing mutation tests.
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// postJSON posts a form-encoded mutation with the fetch headers and
// returns the recorder.
func (f *fixture) postJSON(path string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form = cloneValues(form)
	form.Set(auth.CSRFFieldName, f.csrf)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "fetch")
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

// decodeMutationJSON decodes the fetch-channel answer and asserts the
// shape (status 200, application/json).
func decodeMutationJSON(t *testing.T, rec *httptest.ResponseRecorder) mutationResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 JSON; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var resp mutationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode json: %v; body: %s", err, rec.Body.String())
	}
	return resp
}

func TestMutationJSONAddClient(t *testing.T) {
	f := newFixture(t)
	rec := f.postJSON("/clients/new", url.Values{"name": {"carol"}})
	resp := decodeMutationJSON(t, rec)
	if !resp.OK || resp.Message != flashAdded {
		t.Fatalf("resp = %+v, want ok + %q", resp, flashAdded)
	}
	if !strings.Contains(resp.HTML, `class="card"`) || !strings.Contains(resp.HTML, "carol") {
		t.Fatalf("html fragment missing the new card: %q", resp.HTML)
	}
	if resp.Count == nil || *resp.Count != 1 {
		t.Fatalf("count = %v, want 1", resp.Count)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil || len(clients) != 1 || clients[0].Name != "carol" {
		t.Fatalf("db after JSON add: %+v err=%v", clients, err)
	}
	f.configMatches()
}

func TestMutationJSONAddFragmentEscaped(t *testing.T) {
	f := newFixture(t)
	rec := f.postJSON("/clients/new", url.Values{"name": {"<script>alert(1)</script>"}})
	resp := decodeMutationJSON(t, rec)
	if !resp.OK {
		t.Fatalf("resp = %+v, want ok", resp)
	}
	if strings.Contains(resp.HTML, "<script>alert") {
		t.Fatalf("fragment not escaped: %q", resp.HTML)
	}
	if !strings.Contains(resp.HTML, "&lt;script&gt;") {
		t.Fatalf("escaped form missing: %q", resp.HTML)
	}
}

func TestMutationJSONToggleReturnsCard(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("frank")
	rec := f.postJSON(fmt.Sprintf("/clients/%d/disable", c.ID), nil)
	resp := decodeMutationJSON(t, rec)
	if !resp.OK || resp.Message != flashDisabled {
		t.Fatalf("disable resp = %+v", resp)
	}
	if !strings.Contains(resp.HTML, "отключён") || !strings.Contains(resp.HTML, "Включить") {
		t.Fatalf("fragment must reflect the disabled state: %q", resp.HTML)
	}
	rec = f.postJSON(fmt.Sprintf("/clients/%d/enable", c.ID), nil)
	resp = decodeMutationJSON(t, rec)
	if !resp.OK || resp.Message != flashEnabled {
		t.Fatalf("enable resp = %+v", resp)
	}
	if !strings.Contains(resp.HTML, "включён") || !strings.Contains(resp.HTML, "Отключить") {
		t.Fatalf("fragment must reflect the enabled state: %q", resp.HTML)
	}
}

func TestMutationJSONRenameReturnsCard(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("ivan")
	rec := f.postJSON(fmt.Sprintf("/clients/%d/rename", c.ID), url.Values{"name": {"иван-новый"}})
	resp := decodeMutationJSON(t, rec)
	if !resp.OK || resp.Message != flashRenamed {
		t.Fatalf("resp = %+v", resp)
	}
	if !strings.Contains(resp.HTML, "иван-новый") {
		t.Fatalf("fragment missing the new name: %q", resp.HTML)
	}
	row, err := db.ClientByID(f.h, c.ID)
	if err != nil || row.Name != "иван-новый" {
		t.Fatalf("db after JSON rename: %+v err=%v", row, err)
	}
}

func TestMutationJSONDelete(t *testing.T) {
	f := newFixture(t)
	f.addClient("grace")
	c, _, _ := f.addClient("heidi")
	rec := f.postJSON(fmt.Sprintf("/clients/%d/delete", c.ID), nil)
	resp := decodeMutationJSON(t, rec)
	if !resp.OK || resp.Message != flashDeleted {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.HTML != "" {
		t.Fatalf("delete must not carry a fragment, got %q", resp.HTML)
	}
	if resp.Count == nil || *resp.Count != 1 {
		t.Fatalf("count = %v, want 1 remaining", resp.Count)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil || len(clients) != 1 || clients[0].ID == c.ID {
		t.Fatalf("db after JSON delete: %+v err=%v", clients, err)
	}
	f.configMatches()
}

func TestMutationJSONDeleteLastClientKeepsZeroCount(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("only")
	rec := f.postJSON(fmt.Sprintf("/clients/%d/delete", c.ID), nil)
	resp := decodeMutationJSON(t, rec)
	if !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Count == nil || *resp.Count != 0 {
		t.Fatalf("count = %v, want 0 (explicit zero, not omitted)", resp.Count)
	}
}

func TestMutationJSONErrors(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name      string
		path      string
		form      url.Values
		wantFlash string
	}{
		{"bad name", "/clients/new", url.Values{"name": {""}}, flashInvalidName},
		{"bad id", "/clients/abc/delete", nil, flashInvalidID},
		{"unknown id", "/clients/999/delete", nil, flashNotFound},
	}
	for _, tc := range cases {
		rec := f.postJSON(tc.path, tc.form)
		resp := decodeMutationJSON(t, rec)
		if resp.OK || resp.Message != tc.wantFlash {
			t.Errorf("%s: resp = %+v, want ok=false + %q", tc.name, resp, tc.wantFlash)
		}
	}
}

// TestMutationPlainFormPOSTStill303: without the fetch headers the
// mutation answers the classic 303 PRG (the no-JS fallback app.js
// upgrades from).
func TestMutationPlainFormPOSTStill303(t *testing.T) {
	f := newFixture(t)
	rec := f.post("/clients/new", url.Values{"name": {"prg-client"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("plain POST: code = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/?msg=") {
		t.Fatalf("Location = %q, want /?msg=...", loc)
	}
}

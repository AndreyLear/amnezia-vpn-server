// M7.7 process-level session hardening (internal/cli): the panel
// process owns the in-memory session store, so a restart must discard
// every issued session (restart → re-login); the server logs must
// never carry the password, the session cookie, the CSRF token or the
// stored hash; and the SQLite auth table stores only the Argon2id PHC
// hash — the plaintext password never touches the database file.
package cli

import (
	"bytes"
	"encoding/json"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/web"
)

const (
	lifeAdmin     = "life-admin"
	lifePassword  = "life-password-for-m77"
)

// jarClient returns an HTTP client with a cookie jar (a real browser:
// Set-Cookie is stored, redirects are followed).
func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// sessionIDFromJar extracts the panel session cookie from the jar.
func sessionIDFromJar(t *testing.T, cl *http.Client, base *url.URL) string {
	t.Helper()
	for _, c := range cl.Jar.Cookies(base) {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("cookie jar holds no session cookie")
	return ""
}

func extractCSRF(t *testing.T, cl *http.Client, base *url.URL) string {
	t.Helper()
	resp, err := cl.Get(base.String() + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer resp.Body.Close()
	var me map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /api/me: %v", err)
	}
	got, _ := me["csrf"].(string)
	if len(got) != 43 {
		t.Fatalf("csrf value %q is not 43 chars", got)
	}
	return got
}

// loginOverHTTP performs the real login round trip and returns the
// followed-to dashboard body plus the session cookie value.
func loginOverHTTP(t *testing.T, cl *http.Client, base *url.URL) (dashBody, sid string) {
	t.Helper()
	resp, err := cl.PostForm(base.String()+"/login",
		url.Values{"username": {lifeAdmin}, "password": {lifePassword}})
	if err != nil {
		t.Fatalf("login POST: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("login body: %v", err)
	}
	// The client follows the 303 / to the dashboard; a failure would
	// land on the login form with the generic error. The dashboard does
	// not expose the username in its navigation chrome.
	if !strings.Contains(string(body), `id="root"`) {
		t.Fatalf("login did not reach the SPA: %q", body)
	}
	return string(body), sessionIDFromJar(t, cl, base)
}

// startServe boots one panel process (fresh in-memory session store,
// like a container start) and returns its stop function.
func startServe(t *testing.T, addr string, errb io.Writer) (stop func(), done chan int) {
	t.Helper()
	a := &app{stdout: io.Discard, stderr: errb}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan int, 1)
	go func() { done <- a.serveHTTP(ctx, web.Config{Addr: addr}) }()
	waitForServe(t, addr, 5*time.Second)
	return cancel, done
}

func TestServeRestartInvalidatesSessions(t *testing.T) {
	newCtx(t)
	if code, _, errb := runInput(strings.NewReader(lifePassword+"\n"),
		"auth", "add-user", lifeAdmin, "--password-stdin"); code != 0 {
		t.Fatalf("auth add-user: exit %d, stderr %q", code, errb)
	}
	addr := freePort(t)
	base := &url.URL{Scheme: "http", Host: addr}

	// First panel process: login works, the dashboard renders.
	stop1, done1 := startServe(t, addr, io.Discard)
	cl1 := jarClient(t)
	_, sid1 := loginOverHTTP(t, cl1, base)
	stop1()
	if rc := <-done1; rc != 0 {
		t.Fatalf("first serve exited %d", rc)
	}

	// Restart: a new process, a new in-memory store. The old cookie
	// must authenticate nothing anymore.
	stop2, done2 := startServe(t, addr, io.Discard)
	defer func() {
		stop2()
		<-done2
	}()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodGet, base.String()+"/api/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid1})
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatalf("GET /api/me with pre-restart cookie: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-restart cookie after restart: %d; want 401", resp.StatusCode)
	}

	// A fresh login after the restart works again.
	cl2 := jarClient(t)
	if _, sid2 := loginOverHTTP(t, cl2, base); sid2 == sid1 {
		t.Fatal("re-login must issue a fresh SID")
	}
}

func TestCLIChangePasswordInvalidatesLiveServeSessions(t *testing.T) {
	newCtx(t)
	if code, _, errb := runInput(strings.NewReader(lifePassword+"\n"),
		"auth", "add-user", lifeAdmin, "--password-stdin"); code != 0 {
		t.Fatalf("auth add-user: exit %d, stderr %q", code, errb)
	}
	addr := freePort(t)
	base := &url.URL{Scheme: "http", Host: addr}
	stop, done := startServe(t, addr, io.Discard)
	defer func() {
		stop()
		<-done
	}()

	cl := jarClient(t)
	_, sid := loginOverHTTP(t, cl, base)
	const newPassword = "life-password-after-cli"
	code, out, errb := runInput(strings.NewReader(lifePassword+"\n"+newPassword+"\n"),
		"auth", "change-password", lifeAdmin, "--old-password-stdin", "--new-password-stdin")
	if code != 0 {
		t.Fatalf("change-password: exit %d, stdout %q, stderr %q", code, out, errb)
	}

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodGet, base.String()+"/api/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatalf("GET / after CLI password change: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stolen session after CLI change-password: %d; want 401", resp.StatusCode)
	}

	cl2 := jarClient(t)
	resp, err = cl2.PostForm(base.String()+"/login",
		url.Values{"username": {lifeAdmin}, "password": {newPassword}})
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("re-login body: %v", err)
	}
	if !strings.Contains(string(body), `id="root"`) {
		t.Fatalf("re-login with new password did not reach the SPA: %q", body)
	}
}

func TestCLISetPasswordInvalidatesLiveServeSessions(t *testing.T) {
	newCtx(t)
	if code, _, errb := runInput(strings.NewReader(lifePassword+"\n"),
		"auth", "add-user", lifeAdmin, "--password-stdin"); code != 0 {
		t.Fatalf("auth add-user: exit %d, stderr %q", code, errb)
	}
	addr := freePort(t)
	base := &url.URL{Scheme: "http", Host: addr}
	stop, done := startServe(t, addr, io.Discard)
	defer func() {
		stop()
		<-done
	}()

	cl := jarClient(t)
	_, sid := loginOverHTTP(t, cl, base)
	const newPassword = "life-password-after-set"
	code, out, errb := runInput(strings.NewReader(newPassword+"\n"),
		"auth", "set-password", lifeAdmin, "--password-stdin")
	if code != 0 {
		t.Fatalf("set-password: exit %d, stdout %q, stderr %q", code, out, errb)
	}

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodGet, base.String()+"/api/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatalf("GET / after CLI set-password: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stolen session after CLI set-password: %d; want 401", resp.StatusCode)
	}

	cl2 := jarClient(t)
	resp, err = cl2.PostForm(base.String()+"/login",
		url.Values{"username": {lifeAdmin}, "password": {newPassword}})
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("re-login body: %v", err)
	}
	if !strings.Contains(string(body), `id="root"`) {
		t.Fatalf("re-login with new password did not reach the SPA: %q", body)
	}
}

func TestServeLifecycleLogsSecretFree(t *testing.T) {
	newCtx(t)
	if code, _, errb := runInput(strings.NewReader(lifePassword+"\n"),
		"auth", "add-user", lifeAdmin, "--password-stdin"); code != 0 {
		t.Fatalf("auth add-user: exit %d, stderr %q", code, errb)
	}
	addr := freePort(t)
	base := &url.URL{Scheme: "http", Host: addr}
	serverLogs := &bytes.Buffer{}
	stop, done := startServe(t, addr, serverLogs)

	// The whole M7 lifecycle over real HTTP.
	cl := jarClient(t)
	_, sid := loginOverHTTP(t, cl, base)
	csrf := extractCSRF(t, cl, base)
	if resp, err := cl.PostForm(base.String()+"/clients/new",
		url.Values{"name": {"life-client"}, auth.CSRFFieldName: {csrf}}); err != nil {
		t.Fatalf("add client: %v", err)
	} else {
		resp.Body.Close()
	}
	if resp, err := cl.PostForm(base.String()+"/logout",
		url.Values{auth.CSRFFieldName: {csrf}}); err != nil {
		t.Fatalf("logout: %v", err)
	} else {
		resp.Body.Close()
	}

	stop()
	if rc := <-done; rc != 0 {
		t.Fatalf("serve exited %d", rc)
	}

	// M7.5/M7.6 contract: the password, the SID, the CSRF token and
	// the stored hash must never appear in the server logs.
	u := authUser(t, lifeAdmin)
	logs := serverLogs.String()
	for _, secret := range []string{lifePassword, sid, csrf, u.PasswordHash} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("server logs leak %q: %s", secret, logs)
		}
	}
	if logs == "" {
		t.Fatal("no server logs captured — the scan is vacuous")
	}
}

func TestServeDBStoresOnlyArgon2idHash(t *testing.T) {
	c := newCtx(t)
	code, out, errb := runInput(strings.NewReader(lifePassword+"\n"),
		"auth", "add-user", lifeAdmin, "--password-stdin")
	if code != 0 {
		t.Fatalf("auth add-user: exit %d, stderr %q", code, errb)
	}
	assertNoSecrets(t, out, errb, lifePassword)

	// The auth row carries a real Argon2id PHC hash, not the password.
	u := authUser(t, lifeAdmin)
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("stored hash is not an Argon2id PHC string: %q", u.PasswordHash)
	}
	if strings.Contains(u.PasswordHash, lifePassword) {
		t.Fatal("stored hash embeds the plaintext password")
	}
	if !auth.VerifyPassword(lifePassword, u.PasswordHash) {
		t.Fatal("stored hash must verify the original password")
	}

	// The raw database file must contain the hash (the check is not
	// vacuous) and never the plaintext password.
	raw, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	if !strings.Contains(string(raw), u.PasswordHash) {
		t.Fatal("database file does not contain the stored hash")
	}
	if strings.Contains(string(raw), lifePassword) {
		t.Fatal("plaintext password found in the database file")
	}
}

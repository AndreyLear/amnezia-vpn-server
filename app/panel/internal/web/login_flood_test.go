package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Параллельные входы не должны обходить лимит попыток и не должны
// запускать Argon2 (64 МиБ на проверку) без предела
// (amnezia-vpn-server-76mp.1).

func apiLoginFrom(t *testing.T, remoteAddr, username, password string) *http.Request {
	t.Helper()
	req := apiJSON(t, http.MethodPost, "/api/login", map[string]string{
		"username": username,
		"password": password,
	})
	req.RemoteAddr = remoteAddr
	return req
}

func TestLoginParallelSameIPReachesVerifyAtMostLimit(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	var calls atomic.Int32
	f.server.verifyPassword = func(string, string) bool {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return false
	}

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			f.server.ServeHTTP(rec, apiLoginFrom(t, "203.0.113.10:12345", "alice", "wrong-password"))
		}()
	}
	wg.Wait()
	if got := calls.Load(); got > loginFailLimit {
		t.Fatalf("VerifyPassword calls = %d from one IP, want <= %d", got, loginFailLimit)
	}
}

func TestLoginConcurrentVerifyCapped(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	release := make(chan struct{})
	var inflight, maxInflight atomic.Int32
	f.server.verifyPassword = func(string, string) bool {
		cur := inflight.Add(1)
		for {
			old := maxInflight.Load()
			if cur <= old || maxInflight.CompareAndSwap(old, cur) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		return false
	}

	const n = 20
	var wg sync.WaitGroup
	var finished atomic.Int32
	codes := make([]int, n)
	recs := make([]*httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			// Разные адреса: лимит попыток на адрес здесь не мешает.
			f.server.ServeHTTP(rec, apiLoginFrom(t, fmt.Sprintf("198.51.100.%d:1000", i+1), "alice", "wrong-password"))
			codes[i] = rec.Code
			recs[i] = rec
			finished.Add(1)
		}(i)
	}
	// Отказы должны прийти быстро, не дожидаясь занятых проверок.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && finished.Load() < n-loginVerifySlots {
		time.Sleep(5 * time.Millisecond)
	}
	fast := finished.Load()
	close(release)
	wg.Wait()

	if got := maxInflight.Load(); got > loginVerifySlots {
		t.Fatalf("concurrent VerifyPassword = %d, want <= %d", got, loginVerifySlots)
	}
	if fast < n-loginVerifySlots {
		t.Fatalf("only %d of %d requests answered while checks were busy: queue must not wait", fast, n)
	}
	busy := 0
	for i, code := range codes {
		switch code {
		case http.StatusUnauthorized:
		case http.StatusTooManyRequests:
			busy++
			got := decodeAPI(t, recs[i])
			if got["ok"] != false || got["message"] != loginLimitMessage {
				t.Fatalf("busy answer = %v", got)
			}
			if recs[i].Header().Get("Retry-After") == "" {
				t.Fatal("busy answer must set Retry-After")
			}
		default:
			t.Fatalf("request %d: code = %d", i, code)
		}
	}
	if busy < n-loginVerifySlots {
		t.Fatalf("busy answers = %d, want >= %d", busy, n-loginVerifySlots)
	}
}

func TestLoginBusyAnswerDoesNotCountAsAttempt(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	// Все слоты заняты: вход с адреса отклоняется, но не должен съедать
	// попытки этого адреса.
	for i := 0; i < loginVerifySlots; i++ {
		f.server.loginVerify <- struct{}{}
	}
	ip := "203.0.113.50:1"
	for i := 0; i < loginFailLimit+2; i++ {
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, apiLoginFrom(t, ip, "alice", "wrong-password"))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("busy: code = %d, want 429", rec.Code)
		}
	}
	for i := 0; i < loginVerifySlots; i++ {
		<-f.server.loginVerify
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiLoginFrom(t, ip, "alice", testPassword))
	if rec.Code != http.StatusOK {
		t.Fatalf("after busy period: code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestLoginRateLimitGroupsIPv6By64(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	f.server.verifyPassword = func(string, string) bool { return false }
	wrong := url.Values{"username": {"alice"}, "password": {"wrong-password"}}

	for i := 1; i <= loginFailLimit; i++ {
		addr := fmt.Sprintf("[2001:db8:1:2::%x]:443", i)
		rec := postLogin(t, f, loginRequest(t, addr, wrong))
		if rec.Code != http.StatusOK {
			t.Fatalf("fail %d: code = %d, want 200", i, rec.Code)
		}
	}
	rec := postLogin(t, f, loginRequest(t, "[2001:db8:1:2:ffff:ffff:ffff:ffff]:443", wrong))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64: code = %d, want 429", rec.Code)
	}
	other := postLogin(t, f, loginRequest(t, "[2001:db8:1:3::1]:443", wrong))
	if other.Code != http.StatusOK {
		t.Fatalf("other /64: code = %d, want 200", other.Code)
	}
}

func TestLoginLimitKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"::ffff:203.0.113.7":   "203.0.113.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2::":       "2001:db8:1:2::/64",
		"not-an-ip":            "not-an-ip",
	}
	for in, want := range cases {
		if got := loginLimitKey(in); got != want {
			t.Errorf("loginLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoginLimiterSweepsStaleEntries(t *testing.T) {
	l := newLoginLimiter()
	t0 := time.Now()
	for i := 0; i < 100; i++ {
		l.reserve(fmt.Sprintf("192.0.2.%d", i), t0)
	}
	l.reserve("198.51.100.1", t0.Add(loginFailWindow+time.Second))
	l.mu.Lock()
	n := len(l.byIP)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("byIP holds %d entries after window, want 1 (stale ones swept)", n)
	}
}

// Ответ лимитера на /api/login — JSON, как у остальных ответов входа:
// иначе SPA не может показать, что вход ограничен
// (amnezia-vpn-server-76mp.8).
func TestAPILoginRateLimitAnswersJSON(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	f.server.verifyPassword = func(string, string) bool { return false }
	ip := "203.0.113.77:1"
	for i := 1; i <= loginFailLimit; i++ {
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, apiLoginFrom(t, ip, "alice", "wrong-password"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("fail %d: code = %d, want 401", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiLoginFrom(t, ip, "alice", "wrong-password"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("code = %d, want 429", rec.Code)
	}
	if _, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil {
		t.Fatalf("Retry-After = %q, want seconds", rec.Header().Get("Retry-After"))
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false || got["message"] != loginLimitMessage {
		t.Fatalf("body = %v, want ok=false message=%q", got, loginLimitMessage)
	}
	if strings.Contains(rec.Body.String(), "wrong-password") {
		t.Fatal("429 must not echo the password")
	}
}

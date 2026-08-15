package web

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Login brute-force limits (T-105). In-memory; a panel restart clears
// the map, same as sessions.
const (
	loginFailLimit    = 5
	loginFailWindow   = 15 * time.Minute
	loginLimitMessage = "Слишком много попыток входа. Подождите и попробуйте снова."
)

type loginBucket struct {
	count       int
	windowStart time.Time
}

type loginLimiter struct {
	mu   sync.Mutex
	byIP map[string]loginBucket
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{byIP: make(map[string]loginBucket)}
}

// clientIP is the rate-limit key. X-Real-IP is trusted only when it is
// a single valid IP (install.sh nginx). X-Forwarded-For is ignored so
// a client cannot spoof a chain. Otherwise the host of RemoteAddr.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); net.ParseIP(ip) != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (l *loginLimiter) retryAfter(ip string, now time.Time) (int, bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.byIP[ip]
	if !ok {
		return 0, false
	}
	if now.Sub(b.windowStart) >= loginFailWindow {
		delete(l.byIP, ip)
		return 0, false
	}
	if b.count < loginFailLimit {
		return 0, false
	}
	rem := b.windowStart.Add(loginFailWindow).Sub(now)
	sec := int(rem / time.Second)
	if rem%time.Second != 0 {
		sec++
	}
	if sec < 1 {
		sec = 1
	}
	return sec, true
}

func (l *loginLimiter) fail(ip string, now time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.byIP[ip]
	if !ok || now.Sub(b.windowStart) >= loginFailWindow {
		l.byIP[ip] = loginBucket{count: 1, windowStart: now}
		return
	}
	b.count++
	l.byIP[ip] = b
}

func (l *loginLimiter) clear(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byIP, ip)
}

func (s *Server) rejectLimitedLogin(w http.ResponseWriter, r *http.Request) bool {
	sec, limited := s.loginLimit.retryAfter(clientIP(r), time.Now())
	if !limited {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(sec))
	http.Error(w, loginLimitMessage, http.StatusTooManyRequests)
	return true
}

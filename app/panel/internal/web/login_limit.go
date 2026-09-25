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
//
// An attempt is RESERVED in the limiter before the Argon2 check, not
// counted after it (amnezia-vpn-server-76mp.1): with check-then-count,
// a hundred parallel requests all passed the check before the first
// failure landed, each ran Argon2 with 64 MiB, and the container died
// of OOM — restart wiped the limiter and the flood went on. A reserved
// attempt stays counted when the password is wrong and is dropped only
// when nothing was checked (busy slots, database error) or on success
// (clear).
const (
	loginFailLimit    = 5
	loginFailWindow   = 15 * time.Minute
	loginLimitMessage = "Слишком много попыток входа. Подождите и попробуйте снова."
	// loginVerifySlots caps concurrent Argon2 checks across all addresses:
	// the reservation stops one address, a botnet has many
	// (amnezia-vpn-server-76mp.1). Two slots keep peak memory at 128 MiB
	// and still let two people log in at the same moment.
	loginVerifySlots = 2
	// loginBusyRetryAfter is the Retry-After for a login turned away
	// because every slot was busy. A check takes ~100 ms, so a second is
	// plenty; the request is answered at once instead of queueing, so a
	// flood cannot pile up waiting goroutines behind the slots.
	loginBusyRetryAfter = 1
	// loginSweepEvery bounds how often reserve walks byIP for expired
	// buckets: often enough that the map tracks only the last window's
	// addresses, rarely enough that a flood does not pay O(n) per request.
	loginSweepEvery = time.Minute
)

type loginBucket struct {
	count       int
	windowStart time.Time
}

type loginLimiter struct {
	mu        sync.Mutex
	byIP      map[string]loginBucket
	lastSweep time.Time
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

// loginLimitKey groups IPv6 addresses by /64: one subscriber usually
// gets a whole /64, and counting per address gave them 2^64 fresh
// budgets of five attempts (amnezia-vpn-server-76mp.1). IPv4 (including
// IPv4-mapped IPv6) stays per address; anything unparsable is its own
// key.
func loginLimitKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	prefix := &net.IPNet{IP: parsed.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}
	return prefix.String()
}

func requestLoginKey(r *http.Request) string {
	return loginLimitKey(clientIP(r))
}

// retryAfterLocked answers whether key is out of attempts and for how
// many seconds. Callers hold l.mu.
func (l *loginLimiter) retryAfterLocked(key string, now time.Time) (int, bool) {
	b, ok := l.byIP[key]
	if !ok {
		return 0, false
	}
	if now.Sub(b.windowStart) >= loginFailWindow {
		delete(l.byIP, key)
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

// retryAfter is the cheap pre-check before the request body is read.
func (l *loginLimiter) retryAfter(key string, now time.Time) (int, bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.retryAfterLocked(key, now)
}

// reserve atomically checks the limit and takes one attempt. It returns
// (seconds, false) when key is out of attempts; the attempt is then not
// taken.
func (l *loginLimiter) reserve(key string, now time.Time) (int, bool) {
	if l == nil {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	if sec, limited := l.retryAfterLocked(key, now); limited {
		return sec, false
	}
	b, ok := l.byIP[key]
	if !ok {
		b = loginBucket{windowStart: now}
	}
	b.count++
	l.byIP[key] = b
	return 0, true
}

// release gives back an attempt reserved for a check that never ran.
func (l *loginLimiter) release(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.byIP[key]
	if !ok {
		return
	}
	b.count--
	if b.count <= 0 {
		delete(l.byIP, key)
		return
	}
	l.byIP[key] = b
}

// sweepLocked drops buckets whose window has passed. Without it byIP
// only lost an entry when the same key came back, so every address
// that ever failed stayed in memory forever (amnezia-vpn-server-76mp.1).
func (l *loginLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < loginSweepEvery {
		return
	}
	l.lastSweep = now
	for key, b := range l.byIP {
		if now.Sub(b.windowStart) >= loginFailWindow {
			delete(l.byIP, key)
		}
	}
}

func (l *loginLimiter) clear(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byIP, key)
}

// tryAcquireLoginSlot takes an Argon2 slot without waiting.
func (s *Server) tryAcquireLoginSlot() bool {
	select {
	case s.loginVerify <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseLoginSlot() { <-s.loginVerify }

func (s *Server) rejectLimitedLogin(w http.ResponseWriter, r *http.Request) bool {
	sec, limited := s.loginLimit.retryAfter(requestLoginKey(r), time.Now())
	if !limited {
		return false
	}
	writeLoginLimited(w, sec)
	return true
}

func writeLoginLimited(w http.ResponseWriter, sec int) {
	w.Header().Set("Retry-After", strconv.Itoa(sec))
	http.Error(w, loginLimitMessage, http.StatusTooManyRequests)
}

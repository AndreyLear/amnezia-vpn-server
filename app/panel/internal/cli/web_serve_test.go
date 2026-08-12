package cli

// M6.1 serve tests (M6_AUDIT.md §9): the serve command starts a real
// HTTP server, answers GET /, shuts down gracefully on SIGTERM/SIGINT
// with exit 0, and fails with exit 1 on fatal startup errors.
// Real signals are delivered with syscall.Kill to the test process,
// but only after the signal handler is installed by the test itself.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/web"
)

// sampleKeyMaterial is a realistic-looking X25519 value used to assert
// that it never appears in HTTP responses.
const sampleKeyMaterial = "4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="

// freePort returns a localhost port that was free at reservation time.
// The listener is closed right away; tests tolerate the tiny race.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
}

// waitForServe polls the server until an HTTP response arrives or the
// deadline passes. Since M7.4 the panel is auth-closed by default, so
// an unauthenticated GET / answers 303 /login: any response counts as
// "the server is up".
func waitForServe(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET http://%s/ answered nothing within %v", addr, timeout)
}

// serveFixture wires the test database through the environment and
// returns an app plus the captured stderr buffer.
func serveFixture(t *testing.T, c *ctx) (*app, *bytes.Buffer) {
	t.Helper()
	errb := &bytes.Buffer{}
	a := &app{stdout: io.Discard, stderr: errb}
	t.Setenv("AMNEZIA_DB_PATH", c.dbPath)
	t.Setenv("AMNEZIA_CONFIG_PATH", c.cfgPath)
	return a, errb
}

// checkExit asserts the serve result within the deadline and reports
// the captured stderr on failure.
func checkExit(t *testing.T, done chan int, errb *bytes.Buffer, want int) {
	t.Helper()
	select {
	case rc := <-done:
		if rc != want {
			t.Fatalf("serve: exit %d, want %d; stderr: %s", rc, want, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit within 10s")
	}
}

func TestServeStartsAndAnswersGET(t *testing.T) {
	c := newCtx(t)
	a, errb := serveFixture(t, c)
	addr := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- a.serveHTTP(ctx, web.Config{Addr: addr}) }()

	waitForServe(t, addr, 5*time.Second)
	cancel()
	checkExit(t, done, errb, 0)

	if !strings.Contains(errb.String(), "listening on") {
		t.Errorf("stderr missing startup log: %q", errb.String())
	}
}

func TestServeGracefulSIGTERM(t *testing.T) {
	c := newCtx(t)
	a, errb := serveFixture(t, c)
	addr := freePort(t)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	done := make(chan int, 1)
	go func() { done <- a.serveHTTP(ctx, web.Config{Addr: addr}) }()

	waitForServe(t, addr, 5*time.Second)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}
	checkExit(t, done, errb, 0)
}

func TestServeGracefulSIGINT(t *testing.T) {
	c := newCtx(t)
	a, errb := serveFixture(t, c)
	addr := freePort(t)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan int, 1)
	go func() { done <- a.serveHTTP(ctx, web.Config{Addr: addr}) }()

	waitForServe(t, addr, 5*time.Second)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("kill self: %v", err)
	}
	checkExit(t, done, errb, 0)
}

func TestServeFatalBadDBPath(t *testing.T) {
	c := newCtx(t)
	a, errb := serveFixture(t, c)
	// pointing the DB at an existing directory fails the open.
	t.Setenv("AMNEZIA_DB_PATH", c.dir)

	rc := a.serveHTTP(context.Background(), web.Config{Addr: freePort(t)})
	if rc != 1 {
		t.Fatalf("serve with bad DB path: exit %d, want 1; stderr: %s", rc, errb.String())
	}
	if !strings.Contains(errb.String(), "panel serve:") {
		t.Errorf("stderr missing diagnostic: %q", errb.String())
	}
}

func TestServeFatalBadAddr(t *testing.T) {
	c := newCtx(t)
	a, errb := serveFixture(t, c)

	// port 99999 fails inside net.Listen → fatal exit 1.
	rc := a.serveHTTP(context.Background(), web.Config{Addr: "127.0.0.1:99999"})
	if rc != 1 {
		t.Fatalf("serve with bad addr: exit %d, want 1; stderr: %s", rc, errb.String())
	}
	if !strings.Contains(errb.String(), "panel serve:") {
		t.Errorf("stderr missing diagnostic: %q", errb.String())
	}
}

func TestCmdServeUsageErrors(t *testing.T) {
	c := newCtx(t)
	for _, tc := range [][]string{
		{"serve", "--addr"},
		{"serve", "--bogus", "x"},
		{"serve", "extra-positional"},
	} {
		code, _, errb := c.run(tc...)
		if code != 2 {
			t.Errorf("panel %v: exit %d, want 2; stderr: %s", tc, code, errb)
		}
	}
}

func TestServeHTTPSecretFreeAnd404(t *testing.T) {
	c := newCtx(t)
	a, _ := serveFixture(t, c)
	addr := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- a.serveHTTP(ctx, web.Config{Addr: addr}) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("serve did not stop after test")
		}
	}()

	waitForServe(t, addr, 5*time.Second)

	// M7.4: an unauthenticated GET / is challenged with 303 /login, and
	// the challenge must not echo query key material anywhere. The
	// client must not follow redirects: the 303 is the assertion target.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noFollow.Get("http://" + addr + "/?msg=" + sampleKeyMaterial)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("GET / unauthenticated: status %d, want 303 /login", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Errorf("GET / unauthenticated: Location %q, want /login", loc)
	}
	if strings.Contains(string(body), sampleKeyMaterial) {
		t.Errorf("GET / echoed query key material: %s", body)
	}

	resp, err = http.Get("http://" + addr + "/does-not-exist")
	if err != nil {
		t.Fatalf("GET unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown route: status %d, want 404", resp.StatusCode)
	}
}

// TestCmdServeAddrFlag drives the full cmdServe entry point (not just
// serveHTTP): the --addr flag is wired through to the listener. Run
// executes in a goroutine; SIGTERM triggers the graceful exit 0.
func TestCmdServeAddrFlag(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	addr := freePort(t)

	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	done := make(chan int, 1)
	go func() { done <- Run([]string{"serve", "--addr", addr}, out, errb) }()

	waitForServe(t, addr, 5*time.Second)

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}
	checkExit(t, done, errb, 0)
	if !strings.Contains(errb.String(), "listening on "+addr) {
		t.Errorf("stderr missing startup log for %s: %q", addr, errb.String())
	}
}

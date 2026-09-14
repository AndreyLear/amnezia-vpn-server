package mailer

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeHost = "smtp.test"

// testCert returns a server certificate for fakeHost and a pool that
// trusts it.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: fakeHost},
		DNSNames:              []string{fakeHost},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// fakeSMTP is a minimal relay: enough of RFC 5321 to observe what the
// sender puts on the wire, and in particular what it sends before TLS.
type fakeSMTP struct {
	implicitTLS bool
	startTLS    bool
	authMethods string // advertised AUTH methods, "" for none
	user, pass  string
	echoLogin   bool // a careless server quoting the credentials in its refusal
	rejectRcpt  bool // the recipient is refused with 550

	tlsConfig *tls.Config
	pool      *x509.CertPool
	ln        net.Listener

	mu        sync.Mutex
	clearText strings.Builder // client bytes received before TLS
	logins    []string        // "user:pass" as received
	messages  []string        // DATA payloads
	accepted  bool            // at least one login succeeded
}

func newFakeSMTP(t *testing.T, configure func(*fakeSMTP)) *fakeSMTP {
	t.Helper()
	cert, pool := testCert(t)
	f := &fakeSMTP{
		startTLS:    true,
		authMethods: "PLAIN LOGIN",
		user:        "vpn@example.org",
		pass:        "correct horse battery staple",
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}},
		pool:        pool,
	}
	if configure != nil {
		configure(f)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

// sender returns a Sender that reaches this server whatever address it
// asks for and trusts its certificate.
func (f *fakeSMTP) sender() *Sender {
	return &Sender{
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, f.ln.Addr().String())
		},
		TLSConfig: &tls.Config{RootCAs: f.pool, MinVersion: tls.VersionTLS12},
		Timeout:   5 * time.Second,
	}
}

func (f *fakeSMTP) serve(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	var conn net.Conn = raw
	secure := false
	if f.implicitTLS {
		conn = tls.Server(raw, f.tlsConfig)
		secure = true
	}
	r := bufio.NewReader(conn)
	reply := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
	readLine := func() (string, bool) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		if !secure {
			f.mu.Lock()
			f.clearText.WriteString(line)
			f.mu.Unlock()
		}
		return strings.TrimRight(line, "\r\n"), true
	}
	login := func(user, pass string) {
		f.mu.Lock()
		f.logins = append(f.logins, user+":"+pass)
		ok := user == f.user && pass == f.pass
		if ok {
			f.accepted = true
		}
		f.mu.Unlock()
		if ok {
			reply("235 2.7.0 Authentication successful")
		} else if f.echoLogin {
			reply("535 5.7.8 Authentication failed for " + user + " with " + pass)
		} else {
			reply("535 5.7.8 Authentication credentials invalid")
		}
	}
	decode := func(s string) string {
		b, _ := base64.StdEncoding.DecodeString(s)
		return string(b)
	}

	reply("220 " + fakeHost + " ESMTP")
	for {
		line, ok := readLine()
		if !ok {
			return
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			reply("250-" + fakeHost)
			if !secure && f.startTLS {
				reply("250-STARTTLS")
			}
			if f.authMethods != "" {
				reply("250-AUTH " + f.authMethods)
			}
			reply("250 8BITMIME")
		case upper == "STARTTLS":
			reply("220 2.0.0 Ready to start TLS")
			conn = tls.Server(raw, f.tlsConfig)
			r = bufio.NewReader(conn)
			secure = true
		case strings.HasPrefix(upper, "AUTH PLAIN "):
			parts := strings.Split(decode(strings.TrimSpace(line[len("AUTH PLAIN "):])), "\x00")
			if len(parts) != 3 {
				reply("501 malformed")
				continue
			}
			login(parts[1], parts[2])
		case upper == "AUTH LOGIN":
			reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, ok := readLine()
			if !ok {
				return
			}
			reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, ok := readLine()
			if !ok {
				return
			}
			login(decode(u), decode(p))
		case strings.HasPrefix(upper, "RCPT TO:") && f.rejectRcpt:
			reply("550 5.1.1 Mailbox unavailable")
		case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
			reply("250 2.1.0 OK")
		case upper == "DATA":
			reply("354 End data with <CR><LF>.<CR><LF>")
			var msg strings.Builder
			for {
				l, ok := readLine()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				msg.WriteString(l)
				msg.WriteString("\r\n")
			}
			f.mu.Lock()
			f.messages = append(f.messages, msg.String())
			f.mu.Unlock()
			reply("250 2.0.0 queued")
		case upper == "QUIT":
			reply("221 2.0.0 Bye")
			return
		default:
			reply("502 5.5.2 command not recognized")
		}
	}
}

func (f *fakeSMTP) snapshot() (clear string, logins, messages []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clearText.String(), append([]string(nil), f.logins...), append([]string(nil), f.messages...)
}

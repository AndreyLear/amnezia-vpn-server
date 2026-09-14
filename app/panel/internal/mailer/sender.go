package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
)

// ImplicitTLSPort is the only port spoken TLS from the first byte.
const ImplicitTLSPort = 465

// DefaultTimeout bounds one whole delivery: dial, TLS, dialogue, data.
// The timer fires every minute; a hung relay must not pile runs up.
const DefaultTimeout = 45 * time.Second

// ErrNoTLS reports a server on a non-465 port that does not offer
// STARTTLS. The password is not sent to it.
var ErrNoTLS = errors.New("mailer: server does not offer STARTTLS, refusing to send the password in clear")

// ErrNoAuth reports a server that offers no login method we speak.
var ErrNoAuth = errors.New("mailer: server offers neither AUTH PLAIN nor AUTH LOGIN")

// Sender delivers one message over SMTP. The zero value is ready to use.
type Sender struct {
	// Dial opens the TCP connection; nil uses net.Dialer. Tests point it
	// at a local fake server.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLSConfig is cloned for every connection, ServerName set to the
	// relay host; nil verifies against the system roots.
	TLSConfig *tls.Config
	// Timeout bounds the whole delivery; zero means DefaultTimeout.
	Timeout time.Duration
	// Now stamps the Date header; nil means time.Now.
	Now func() time.Time
}

// Send delivers msg from cfg.Username to cfg.Recipient. Errors never
// contain the password.
func (s *Sender) Send(ctx context.Context, cfg *mailconf.File, msg Message) error {
	err := s.send(ctx, cfg, msg)
	if err == nil {
		return nil
	}
	return &SendError{Summary: explain(err), err: redact(err, cfg)}
}

func (s *Sender) send(ctx context.Context, cfg *mailconf.File, msg Message) error {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	data, err := Compose(cfg.Username, cfg.Recipient, msg, now())
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dial := s.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mailer: connect %s: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	// The deadline covers blocking reads; cancellation of ctx itself
	// (a signal to the service) closes the connection.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.TLSConfig != nil {
		tlsConfig = s.TLSConfig.Clone()
	}
	tlsConfig.ServerName = cfg.Host

	if cfg.Port == ImplicitTLSPort {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("mailer: TLS with %s: %w", addr, err)
		}
		conn = tlsConn
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("mailer: greeting from %s: %w", addr, err)
	}
	defer c.Close()
	if err := c.Hello(helloName(cfg.Username)); err != nil {
		return fmt.Errorf("mailer: EHLO: %w", err)
	}
	if cfg.Port != ImplicitTLSPort {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrNoTLS
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("mailer: STARTTLS with %s: %w", addr, err)
		}
	}

	auth, err := pickAuth(c, cfg)
	if err != nil {
		return err
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("mailer: login as %s: %w", cfg.Username, err)
	}
	if err := c.Mail(cfg.Username); err != nil {
		return fmt.Errorf("mailer: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(cfg.Recipient); err != nil {
		return fmt.Errorf("mailer: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return fmt.Errorf("mailer: write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: message not accepted: %w", err)
	}
	// The message is accepted once DATA is closed; a failed QUIT does not
	// make it undelivered.
	_ = c.Quit()
	return nil
}

// helloName is what we introduce ourselves as. The login's domain is the
// honest answer available without asking the host: the server has no
// reverse DNS name of its own to offer.
func helloName(username string) string {
	if at := strings.LastIndex(username, "@"); at >= 0 && at < len(username)-1 {
		return username[at+1:]
	}
	return "localhost"
}

func pickAuth(c *smtp.Client, cfg *mailconf.File) (smtp.Auth, error) {
	ok, methods := c.Extension("AUTH")
	if !ok {
		return nil, ErrNoAuth
	}
	fields := strings.Fields(strings.ToUpper(methods))
	has := func(name string) bool {
		for _, f := range fields {
			if f == name {
				return true
			}
		}
		return false
	}
	switch {
	case has("PLAIN"):
		return smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host), nil
	case has("LOGIN"):
		return &loginAuth{username: cfg.Username, password: cfg.Password}, nil
	}
	return nil, ErrNoAuth
}

// loginAuth is AUTH LOGIN, which some relays offer instead of PLAIN.
// It runs only after TLS is up: Send never reaches authentication on a
// connection that is not encrypted.
type loginAuth struct{ username, password string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, ErrNoTLS
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.Contains(prompt, "username"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "password"):
		return []byte(a.password), nil
	}
	return nil, errors.New("mailer: unexpected AUTH LOGIN prompt")
}

// redact removes the password — as is, and in the base64 forms AUTH PLAIN
// and AUTH LOGIN put on the wire — from an error's text. Server replies
// do not normally echo credentials, but an error is shown in the panel
// and written to the journal, so this is not left to the server's manners.
func redact(err error, cfg *mailconf.File) error {
	if err == nil || cfg == nil || cfg.Password == "" {
		return err
	}
	text := err.Error()
	secrets := []string{
		cfg.Password,
		base64.StdEncoding.EncodeToString([]byte(cfg.Password)),
		base64.StdEncoding.EncodeToString([]byte("\x00" + cfg.Username + "\x00" + cfg.Password)),
	}
	clean := text
	for _, s := range secrets {
		clean = strings.ReplaceAll(clean, s, "***")
	}
	if clean == text {
		return err
	}
	// The wrapped chain still holds the original text, so it is not kept.
	return &redactedError{text: clean, sentinel: sentinelOf(err)}
}

type redactedError struct {
	text     string
	sentinel error
}

func (e *redactedError) Error() string { return e.text }
func (e *redactedError) Unwrap() error { return e.sentinel }

func sentinelOf(err error) error {
	for _, s := range []error{ErrNoTLS, ErrNoAuth, ErrBadAddress} {
		if errors.Is(err, s) {
			return s
		}
	}
	return nil
}

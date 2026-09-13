package mailer

import (
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
)

func confFor(f *fakeSMTP, port int) *mailconf.File {
	return &mailconf.File{
		Host: fakeHost, Port: port, Username: f.user, Password: f.pass, Recipient: "owner@example.org",
	}
}

var testMessage = Message{Subject: "Туннель не работает 5 минут", Body: "Сервер 203.0.113.10\nПроверьте состояние служб"}

// assertNoPassword fails when the password, raw or in the base64 forms
// AUTH puts on the wire, appears in s.
func assertNoPassword(t *testing.T, where, s string, cfg *mailconf.File) {
	t.Helper()
	for _, secret := range []string{
		cfg.Password,
		base64.StdEncoding.EncodeToString([]byte(cfg.Password)),
		base64.StdEncoding.EncodeToString([]byte("\x00" + cfg.Username + "\x00" + cfg.Password)),
	} {
		if strings.Contains(s, secret) {
			t.Fatalf("пароль виден в %s", where)
		}
	}
}

// Порт 587: письмо уходит только после STARTTLS, до шифрования по проводу
// не проходит ни пароль, ни сама команда входа (amnezia-vpn-server-hxgr).
func TestSendStartTLS(t *testing.T) {
	for _, method := range []string{"PLAIN", "LOGIN"} {
		t.Run(method, func(t *testing.T) {
			f := newFakeSMTP(t, func(f *fakeSMTP) { f.authMethods = method })
			cfg := confFor(f, 587)
			if err := f.sender().Send(context.Background(), cfg, testMessage); err != nil {
				t.Fatalf("Send: %v", err)
			}
			clear, logins, messages := f.snapshot()
			if !strings.Contains(clear, "STARTTLS") {
				t.Fatal("STARTTLS не было")
			}
			if strings.Contains(strings.ToUpper(clear), "AUTH") {
				t.Fatalf("вход до шифрования:\n%s", clear)
			}
			assertNoPassword(t, "открытой части разговора", clear, cfg)
			if len(logins) != 1 || logins[0] != cfg.Username+":"+cfg.Password {
				t.Fatalf("входы %q", logins)
			}
			if len(messages) != 1 {
				t.Fatalf("писем %d, ждали одно", len(messages))
			}
			assertDelivered(t, messages[0], cfg)
		})
	}
}

// Порт 465: TLS с первого байта, открытой части разговора нет вовсе.
func TestSendImplicitTLS(t *testing.T) {
	f := newFakeSMTP(t, func(f *fakeSMTP) { f.implicitTLS = true; f.startTLS = false })
	cfg := confFor(f, ImplicitTLSPort)
	if err := f.sender().Send(context.Background(), cfg, testMessage); err != nil {
		t.Fatalf("Send: %v", err)
	}
	clear, _, messages := f.snapshot()
	if clear != "" {
		t.Fatalf("на 465 были байты до TLS:\n%s", clear)
	}
	if len(messages) != 1 {
		t.Fatalf("писем %d", len(messages))
	}
	assertDelivered(t, messages[0], cfg)
}

// Сервер без STARTTLS не получает пароль: отказ раньше входа.
func TestSendRefusesServerWithoutTLS(t *testing.T) {
	f := newFakeSMTP(t, func(f *fakeSMTP) { f.startTLS = false })
	cfg := confFor(f, 587)
	err := f.sender().Send(context.Background(), cfg, testMessage)
	if !errors.Is(err, ErrNoTLS) {
		t.Fatalf("err = %v, ждали ErrNoTLS", err)
	}
	clear, logins, messages := f.snapshot()
	if len(logins) != 0 || strings.Contains(strings.ToUpper(clear), "AUTH") {
		t.Fatalf("вход без шифрования: %q\n%s", logins, clear)
	}
	assertNoPassword(t, "разговоре", clear, cfg)
	if len(messages) != 0 {
		t.Fatal("письмо ушло без шифрования")
	}
}

// Сертификат, которому нет доверия, — не повод отправить пароль.
func TestSendRefusesUntrustedCertificate(t *testing.T) {
	f := newFakeSMTP(t, nil)
	cfg := confFor(f, 587)
	s := f.sender()
	s.TLSConfig = nil // системные корни: самоподписанный сертификат не пройдёт
	if err := s.Send(context.Background(), cfg, testMessage); err == nil {
		t.Fatal("письмо ушло через недоверенный сертификат")
	}
	if _, logins, _ := f.snapshot(); len(logins) != 0 {
		t.Fatalf("пароль отправлен через недоверенный сертификат: %d входов", len(logins))
	}
}

// Отказ сервера в логине виден словами, но без пароля.
func TestSendWrongPasswordErrorHasNoPassword(t *testing.T) {
	f := newFakeSMTP(t, nil)
	cfg := confFor(f, 587)
	cfg.Password = "wrong-password-must-not-show"
	err := f.sender().Send(context.Background(), cfg, testMessage)
	if err == nil {
		t.Fatal("вход с неверным паролем прошёл")
	}
	if !strings.Contains(err.Error(), "535") {
		t.Errorf("в ошибке нет ответа сервера: %v", err)
	}
	assertNoPassword(t, "тексте ошибки", err.Error(), cfg)
}

// Сервер, который цитирует пароль в отказе, не выносит его в ошибку.
func TestSendRedactsServerEchoingPassword(t *testing.T) {
	f := newFakeSMTP(t, func(f *fakeSMTP) { f.echoLogin = true; f.authMethods = "LOGIN" })
	cfg := confFor(f, 587)
	cfg.Password = "wrong-password-echoed-back"
	err := f.sender().Send(context.Background(), cfg, testMessage)
	if err == nil {
		t.Fatal("вход прошёл")
	}
	assertNoPassword(t, "тексте ошибки", err.Error(), cfg)
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("пароль не заменён, а просто не дошёл: %v", err)
	}
}

// AUTH LOGIN сам отказывается работать без шифрования — на случай, если
// когда-нибудь до него дойдут в обход проверки STARTTLS.
func TestLoginAuthRequiresTLS(t *testing.T) {
	a := &loginAuth{username: "u", password: "p"}
	if _, _, err := a.Start(&smtp.ServerInfo{Name: fakeHost, TLS: false}); !errors.Is(err, ErrNoTLS) {
		t.Fatalf("без TLS: err = %v", err)
	}
	if proto, _, err := a.Start(&smtp.ServerInfo{Name: fakeHost, TLS: true}); err != nil || proto != "LOGIN" {
		t.Fatalf("с TLS: %q, %v", proto, err)
	}
}

func TestSendNoAuthMethod(t *testing.T) {
	f := newFakeSMTP(t, func(f *fakeSMTP) { f.authMethods = "CRAM-MD5" })
	if err := f.sender().Send(context.Background(), confFor(f, 587), testMessage); !errors.Is(err, ErrNoAuth) {
		t.Fatalf("err = %v, ждали ErrNoAuth", err)
	}
}

// Даже если ошибка по какой-то причине несёт пароль, наружу он не выходит.
func TestRedact(t *testing.T) {
	cfg := &mailconf.File{Username: "u@example.org", Password: "s3cret-pass"}
	plain := base64.StdEncoding.EncodeToString([]byte("\x00u@example.org\x00s3cret-pass"))
	err := redact(errors.Join(ErrNoTLS, errors.New("server said s3cret-pass and "+plain)), cfg)
	assertNoPassword(t, "очищенной ошибке", err.Error(), cfg)
	if !errors.Is(err, ErrNoTLS) {
		t.Error("после очистки ошибка потеряла свой вид")
	}
	if redact(nil, cfg) != nil {
		t.Error("redact(nil) != nil")
	}
}

func assertDelivered(t *testing.T, raw string, cfg *mailconf.File) {
	t.Helper()
	head, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("нет разделителя заголовков:\n%s", raw)
	}
	for _, want := range []string{"From: " + cfg.Username, "To: " + cfg.Recipient} {
		if !strings.Contains(head, want) {
			t.Errorf("нет заголовка %q:\n%s", want, head)
		}
	}
	subject := headerValue(head, "Subject")
	if decoded, err := new(mime.WordDecoder).DecodeHeader(subject); err != nil || decoded != testMessage.Subject {
		t.Errorf("тема %q (%v), ждали %q", decoded, err, testMessage.Subject)
	}
	text, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil {
		t.Fatalf("тело не base64: %v", err)
	}
	if want := strings.ReplaceAll(testMessage.Body, "\n", "\r\n"); string(text) != want {
		t.Errorf("тело %q, ждали %q", text, want)
	}
}

func headerValue(head, name string) string {
	for _, line := range strings.Split(head, "\r\n") {
		if v, ok := strings.CutPrefix(line, name+": "); ok {
			return v
		}
	}
	return ""
}

// Перевод строки в теме не может добавить заголовок.
func TestComposeHeaderInjection(t *testing.T) {
	raw, err := Compose("a@example.org", "b@example.org",
		Message{Subject: "Alert\r\nBcc: attacker@example.org", Body: "x"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Тема только из ASCII не кодируется, поэтому перевод строки в ней
	// дошёл бы до заголовков как есть.
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("тема добавила заголовок:\n%s", head)
		}
	}
	if _, err := Compose("a@example.org\r\nBcc: x@y", "b@example.org", Message{}, time.Now()); !errors.Is(err, ErrBadAddress) {
		t.Fatalf("адрес с переводом строки принят: %v", err)
	}
	long, _ := Compose("a@example.org", "b@example.org", Message{Subject: "s", Body: strings.Repeat("я", 500)}, time.Now())
	for _, line := range strings.Split(string(long), "\r\n") {
		if len(line) > 78 {
			t.Fatalf("строка длиннее 78 байт: %d", len(line))
		}
	}
}

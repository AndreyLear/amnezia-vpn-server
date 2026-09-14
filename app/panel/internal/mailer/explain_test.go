package mailer

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Отказ объясняется словами, а ответ сервера остаётся как есть
// (amnezia-vpn-server-idd6).
func TestSendErrorSummary(t *testing.T) {
	dialErr := func(err error) func(*Sender) {
		return func(s *Sender) {
			s.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, err }
		}
	}
	cases := []struct {
		name      string
		configure func(*fakeSMTP)
		sender    func(*Sender)
		port      int
		password  string
		want      string
	}{
		{name: "сервер не найден", sender: dialErr(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "smtp.invalid", IsNotFound: true}}), want: summaryNoHost},
		{name: "порт закрыт", sender: dialErr(&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), want: summaryRefused},
		{name: "не отвечает", sender: dialErr(context.DeadlineExceeded), want: summaryNoAnswer},
		{name: "нет шифрования", configure: func(f *fakeSMTP) { f.startTLS = false }, want: summaryNoTLS},
		{name: "нет способа входа", configure: func(f *fakeSMTP) { f.authMethods = "CRAM-MD5" }, want: summaryNoAuthMethod},
		{name: "неверный пароль", password: "wrong-password", want: summaryLogin},
		// Сервер повторил пароль в отказе: очистка теряет тип ошибки, поэтому
		// объяснять нужно до неё.
		{name: "неверный пароль, сервер его повторил", configure: func(f *fakeSMTP) { f.echoLogin = true; f.authMethods = "LOGIN" }, password: "wrong-password-echoed", want: summaryLogin},
		{name: "получатель отвергнут", configure: func(f *fakeSMTP) { f.rejectRcpt = true }, want: summaryRejected},
		{name: "недоверенный сертификат", sender: func(s *Sender) { s.TLSConfig = nil }, want: summaryCertificate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSMTP(t, tc.configure)
			s := f.sender()
			s.Timeout = 3 * time.Second
			if tc.sender != nil {
				tc.sender(s)
			}
			port := 587
			if tc.port != 0 {
				port = tc.port
			}
			cfg := confFor(f, port)
			if tc.password != "" {
				cfg.Password = tc.password
			}
			err := s.Send(context.Background(), cfg, testMessage)
			if err == nil {
				t.Fatal("письмо ушло")
			}
			if got := Summary(err); got != tc.want {
				t.Fatalf("объяснение %q, ждали %q; ошибка: %v", got, tc.want, err)
			}
			assertNoPassword(t, "тексте ошибки", err.Error(), cfg)
			if strings.TrimSpace(err.Error()) == "" {
				t.Error("ответ сервера потерян")
			}
		})
	}
	if Summary(errors.New("что-то чужое")) != summaryUnknown {
		t.Error("чужая ошибка без общего объяснения")
	}
}

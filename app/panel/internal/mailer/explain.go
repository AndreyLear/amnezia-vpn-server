package mailer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/textproto"
	"os"
	"syscall"
)

// A send failure is shown to the operator twice over (amnezia-vpn-server-idd6):
// a short explanation of what it means, in words, and under it the server's
// own reply as is. The reply alone — «mailer: connect smtp.invalid:587: dial
// tcp: lookup … no such host» — is an implementation detail; it does not say
// what to fix. The explanation does, and the reply stays for whoever needs to
// tell the mail provider what exactly happened.

// SendError is a failed delivery: Summary explains it, the error text is the
// sender's own, already free of the password.
type SendError struct {
	Summary string
	err     error
}

func (e *SendError) Error() string { return e.err.Error() }
func (e *SendError) Unwrap() error { return e.err }

// Summary returns the explanation of err, or a generic one for an error that
// did not come from Sender.
func Summary(err error) string {
	var se *SendError
	if errors.As(err, &se) && se.Summary != "" {
		return se.Summary
	}
	return summaryUnknown
}

const (
	summaryUnknown      = "Письмо не отправлено"
	summaryNoHost       = "Почтовый сервер не найден: проверьте его адрес"
	summaryNoAnswer     = "Почтовый сервер не отвечает: проверьте адрес и порт"
	summaryRefused      = "Почтовый сервер не принимает подключения на этом порту"
	summaryCertificate  = "Сертификат почтового сервера не прошёл проверку"
	summaryNoTLS        = "Почтовый сервер не поддерживает шифрование на этом порту"
	summaryNoAuthMethod = "Почтовый сервер не предлагает подходящего способа входа"
	summaryLogin        = "Почтовый сервер не принял логин или пароль"
	summaryRejected     = "Почтовый сервер отказался принять письмо"
)

// explain classifies the unredacted error. It runs before the text is
// redacted, because redaction keeps only the text.
func explain(err error) string {
	var (
		dnsErr   *net.DNSError
		tpErr    *textproto.Error
		certErr  *tls.CertificateVerificationError
		authErr  x509.UnknownAuthorityError
		hostErr  x509.HostnameError
		invalErr x509.CertificateInvalidError
		netErr   net.Error
	)
	switch {
	case errors.Is(err, ErrNoTLS):
		return summaryNoTLS
	case errors.Is(err, ErrNoAuth):
		return summaryNoAuthMethod
	case errors.As(err, &dnsErr):
		return summaryNoHost
	case errors.As(err, &certErr), errors.As(err, &authErr), errors.As(err, &hostErr), errors.As(err, &invalErr):
		return summaryCertificate
	case errors.Is(err, syscall.ECONNREFUSED):
		return summaryRefused
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return summaryNoAnswer
	case errors.As(err, &tpErr):
		switch {
		case tpErr.Code == 534 || tpErr.Code == 535 || tpErr.Code == 530:
			return summaryLogin
		case tpErr.Code >= 500:
			return summaryRejected
		}
	}
	return summaryUnknown
}

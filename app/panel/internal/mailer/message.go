package mailer

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"strings"
	"time"
)

// Message is one letter: what is wrong, and the details.
type Message struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// ErrBadAddress reports an address that cannot be put into a header.
var ErrBadAddress = errors.New("mailer: address contains a line break or is empty")

// Compose renders the message as sent over DATA: CRLF line endings,
// UTF-8 subject in encoded-word form, body in base64 so any text survives
// any relay.
func Compose(from, to string, msg Message, now time.Time) ([]byte, error) {
	for _, addr := range []string{from, to} {
		if strings.TrimSpace(addr) == "" || strings.ContainsAny(addr, "\r\n") {
			return nil, ErrBadAddress
		}
	}
	subject := strings.Join(strings.Fields(msg.Subject), " ")

	var b strings.Builder
	header := func(name, value string) {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\r\n")
	}
	header("From", from)
	header("To", to)
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", messageID(from))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")

	body := strings.ReplaceAll(msg.Body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	if encoded != "" {
		b.WriteString(encoded)
		b.WriteString("\r\n")
	}
	return []byte(b.String()), nil
}

// messageID gives every letter a unique id under the sender's domain;
// some relays reject or down-rank mail without one.
func messageID(from string) string {
	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		domain = from[at+1:]
	}
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return "<" + hex.EncodeToString(raw[:]) + "@" + domain + ">"
}

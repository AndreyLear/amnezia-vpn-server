package xray

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Клиент API Xray: HandlerService.AlterInbound с AddUserOperation и
// RemoveUserOperation — добавить или снять пользователя, не трогая
// остальных подключённых (решение владельца Q36,
// amnezia-vpn-server-14y0.2).
//
// Модуль github.com/xtls/xray-core сюда не тянется сознательно: ради
// четырёх маленьких сообщений он принёс бы в панель quic-go, gvisor,
// wireguard-go, gRPC и protobuf — десятки модулей, которые пришлось бы
// закреплять и обновлять. gRPC для одного унарного вызова — это HTTP/2 без
// TLS (в net/http он есть с Go 1.24), пять байт рамки и protobuf из пяти
// полей, закодированный вручную ниже. Поля сверены с .proto Xray:
//
//	app/proxyman/command/command.proto
//	  AddUserOperation    { xray.common.protocol.User user = 1; }
//	  RemoveUserOperation { string email = 1; }
//	  AlterInboundRequest { string tag = 1; xray.common.serial.TypedMessage operation = 2; }
//	common/protocol/user.proto   User         { uint32 level = 1; string email = 2; TypedMessage account = 3; }
//	common/serial/typed_message.proto TypedMessage { string type = 1; bytes value = 2; }
//	proxy/vless/account.proto    Account      { string id = 1; string flow = 2; string encryption = 3; … }
//
// и байт в байт с тем, что даёт proto.Marshal на этих типах (тест
// TestAlterInboundEncodingMatchesProtobuf).

const (
	alterInboundPath       = "/xray.app.proxyman.command.HandlerService/AlterInbound"
	typeAddUserOperation   = "xray.app.proxyman.command.AddUserOperation"
	typeRemoveUserOperaton = "xray.app.proxyman.command.RemoveUserOperation"
	typeVLESSAccount       = "xray.proxy.vless.Account"
)

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// appendBytesField — поле с типом проводки 2 (строка, байты, вложенное
// сообщение). Пустые поля, как и в proto3, не пишутся.
func appendBytesField(b []byte, field int, data []byte) []byte {
	if len(data) == 0 {
		return b
	}
	b = appendVarint(b, uint64(field)<<3|2)
	b = appendVarint(b, uint64(len(data)))
	return append(b, data...)
}

func typedMessage(typ string, value []byte) []byte {
	var b []byte
	b = appendBytesField(b, 1, []byte(typ))
	return appendBytesField(b, 2, value)
}

func alterInboundRequest(tag string, operation []byte) []byte {
	var b []byte
	b = appendBytesField(b, 1, []byte(tag))
	return appendBytesField(b, 2, operation)
}

// addUserRequest — AlterInboundRequest{tag, AddUserOperation{User{email,
// Account{id}}}}. Flow и encryption пусты: у входа XHTTP их нет, а
// encryption в пользователях входа Xray запрещает вовсе.
func addUserRequest(tag, email, uuid string) []byte {
	var account []byte
	account = appendBytesField(account, 1, []byte(uuid))
	var user []byte
	user = appendBytesField(user, 2, []byte(email))
	user = appendBytesField(user, 3, typedMessage(typeVLESSAccount, account))
	var op []byte
	op = appendBytesField(op, 1, user)
	return alterInboundRequest(tag, typedMessage(typeAddUserOperation, op))
}

func removeUserRequest(tag, email string) []byte {
	var op []byte
	op = appendBytesField(op, 1, []byte(email))
	return alterInboundRequest(tag, typedMessage(typeRemoveUserOperaton, op))
}

// API — клиент HandlerService по адресу host:port.
type API struct {
	addr   string
	client *http.Client
}

// apiTimeout ограничивает каждый вызов: панель ждёт ответа под мьютексом
// изменений, и зависший Xray не должен вешать её саму.
const apiTimeout = 3 * time.Second

// NewAPI — клиент API Xray. Соединение HTTP/2 без TLS с первого байта
// (prior knowledge), как говорит gRPC-сервер Xray.
func NewAPI(addr string) *API {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	return &API{
		addr: addr,
		client: &http.Client{
			Transport: &http.Transport{Protocols: &protocols, DisableKeepAlives: true},
			Timeout:   apiTimeout,
		},
	}
}

// GRPCError — ответ сервера с ненулевым grpc-status.
type GRPCError struct {
	Code    string
	Message string
}

func (e *GRPCError) Error() string {
	return "xray api: grpc status " + e.Code + ": " + e.Message
}

// AddUser добавляет пользователя во вход tag.
func (a *API) AddUser(ctx context.Context, tag, email, uuid string) error {
	return a.call(ctx, addUserRequest(tag, email, uuid))
}

// RemoveUser снимает пользователя с входа tag.
func (a *API) RemoveUser(ctx context.Context, tag, email string) error {
	return a.call(ctx, removeUserRequest(tag, email))
}

func (a *API) call(ctx context.Context, msg []byte) error {
	frame := make([]byte, 5, 5+len(msg))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
	frame = append(frame, msg...)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+a.addr+alterInboundPath, bytes.NewReader(frame))
	if err != nil {
		return fmt.Errorf("xray api: %w", err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("xray api: %w", err)
	}
	defer resp.Body.Close()
	// Трейлеры доступны только после того, как тело прочитано до конца.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)); err != nil {
		return fmt.Errorf("xray api: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xray api: http status %d", resp.StatusCode)
	}
	// Ошибка без тела приходит одними заголовками (trailers-only).
	status, msgText := resp.Trailer.Get("Grpc-Status"), resp.Trailer.Get("Grpc-Message")
	if status == "" {
		status, msgText = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	if status == "" {
		return errors.New("xray api: response without grpc-status")
	}
	if status != "0" {
		if m, err := url.PathUnescape(msgText); err == nil {
			msgText = m
		}
		return &GRPCError{Code: status, Message: msgText}
	}
	return nil
}

// Тексты Xray (proxy/vless/validator.go): пользователь уже есть / его нет.
// Для синхронизации это не ошибки — нужное состояние уже достигнуто.
func isAlreadyExists(err error) bool {
	var g *GRPCError
	return errors.As(err, &g) && strings.Contains(g.Message, "already exists")
}

func isNotFound(err error) bool {
	var g *GRPCError
	return errors.As(err, &g) && strings.Contains(g.Message, "not found")
}

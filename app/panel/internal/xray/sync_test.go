package xray

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

var update = flag.Bool("update", false, "rewrite testdata/config.golden.json")

var testOpts = Options{APIAddr: DefaultAPIAddr}

// Конфиг Xray закреплён целиком: вход VLESS+XHTTP+Reality на петле за
// nginx stream (PROXY protocol), пользователи c<id>, API на петле, журналы
// в каталоге логов, запрет частных сетей. Любое изменение — осознанное
// (amnezia-vpn-server-14y0.2).
func TestRenderGolden(t *testing.T) {
	got, err := Render(fixtureSettings(), []db.VLESSClient{
		{ID: 1, Name: "andrey", UUID: "3f2504e0-4f89-41d3-9a0c-0305e82c3301"},
		{ID: 10, Name: "Мама телефон", UUID: "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d"},
	}, []db.VLESSServiceUser{{Email: "ruentry", UUID: "5a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}}, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "config.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("конфиг разошёлся с testdata/config.golden.json:\n%s", got)
	}
	if strings.Contains(string(got), "Мама") {
		t.Fatal("имя клиента попало в конфиг Xray")
	}
}

func TestRenderNoClientsIsEmptyList(t *testing.T) {
	got, err := Render(fixtureSettings(), nil, nil, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"clients": []`) {
		t.Fatalf("без клиентов список должен быть пустым, а не null:\n%s", got)
	}
}

type call struct{ op, email, uuid string }

type fakeAPI struct {
	mu    sync.Mutex
	calls []call
	fail  error
	// failOn — email, на котором вызов падает (пусто — на любом, если
	// fail задан).
	failOn string
	users  map[string]string
}

func (f *fakeAPI) AddUser(_ context.Context, tag, email, uuid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tag != InboundTag {
		return fmt.Errorf("tag %q", tag)
	}
	f.calls = append(f.calls, call{"add", email, uuid})
	if f.fail != nil && (f.failOn == "" || f.failOn == email) {
		return f.fail
	}
	if f.users != nil {
		if _, ok := f.users[email]; ok {
			return &GRPCError{Code: "2", Message: "User " + email + " already exists."}
		}
		f.users[email] = uuid
	}
	return nil
}

func (f *fakeAPI) RemoveUser(_ context.Context, tag, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{"remove", email, ""})
	if f.fail != nil && (f.failOn == "" || f.failOn == email) {
		return f.fail
	}
	if f.users != nil {
		if _, ok := f.users[email]; !ok {
			return &GRPCError{Code: "2", Message: "User " + email + " not found."}
		}
		delete(f.users, email)
	}
	return nil
}

func (f *fakeAPI) take() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func configuredDB(t *testing.T) *sql.DB {
	t.Helper()
	handle := newDB(t)
	if err := db.SaveXraySettings(handle, *fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	return handle
}

func fileUsers(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return usersOf(c)
}

// Нет ключей Reality в базе — нет и файла, и панель не падает: стек
// обязан подниматься без Xray.
func TestSyncWithoutSettingsWritesNothing(t *testing.T) {
	handle := newDB(t)
	addClient(t, handle, "andrey", awgPub)
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	api := &fakeAPI{}
	res, err := Sync(context.Background(), handle, path, testOpts, api)
	if err != nil {
		t.Fatalf("Sync без настроек Xray: %v", err)
	}
	if res.Written {
		t.Fatal("Written без настроек")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("каталог конфига создан без настроек: %v", err)
	}
	if len(api.take()) != 0 {
		t.Fatal("API вызван без настроек")
	}
}

func TestSyncFirstWriteLeavesAPIAlone(t *testing.T) {
	handle := configuredDB(t)
	a := addClient(t, handle, "andrey", awgPub)
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	api := &fakeAPI{}
	res, err := Sync(context.Background(), handle, path, testOpts, api)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Written || len(api.take()) != 0 {
		t.Fatalf("первая запись: %+v", res)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("права конфига %v: в нём приватный ключ Reality", fi.Mode().Perm())
	}
	if got := fileUsers(t, path); !reflect.DeepEqual(got, map[string]string{userEmail(a.ID): a.VLESSUUID}) {
		t.Fatalf("пользователи в файле: %v", got)
	}
}

// Добавление, отключение, удаление и смена UUID доходят до Xray через API
// по одному пользователю, остальных не трогая.
func TestSyncAppliesUserChangesThroughAPI(t *testing.T) {
	handle := configuredDB(t)
	a := addClient(t, handle, "andrey", awgPub)
	b := addClient(t, handle, "mama", awgPub2)
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	api := &fakeAPI{}
	ctx := context.Background()
	if _, err := Sync(ctx, handle, path, testOpts, api); err != nil {
		t.Fatal(err)
	}

	c := addClient(t, handle, "papa", awgPub3)
	res, err := Sync(ctx, handle, path, testOpts, api)
	if err != nil {
		t.Fatal(err)
	}
	if got := api.take(); !reflect.DeepEqual(got, []call{{"add", userEmail(c.ID), c.VLESSUUID}}) {
		t.Fatalf("новый клиент: вызовы %v", got)
	}
	if res.RestartNeeded || res.APIErr != nil {
		t.Fatalf("новый клиент: %+v", res)
	}

	if err := db.SetClientEnabled(handle, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, handle, path, testOpts, api); err != nil {
		t.Fatal(err)
	}
	if got := api.take(); !reflect.DeepEqual(got, []call{{"remove", userEmail(b.ID), ""}}) {
		t.Fatalf("отключение: вызовы %v", got)
	}

	if err := db.DeleteClient(handle, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, handle, path, testOpts, api); err != nil {
		t.Fatal(err)
	}
	if got := api.take(); !reflect.DeepEqual(got, []call{{"remove", userEmail(c.ID), ""}}) {
		t.Fatalf("удаление: вызовы %v", got)
	}

	const newUUID = "11111111-2222-4333-8444-555555555555"
	if err := db.SetClientVLESSUUID(handle, a.ID, newUUID); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, handle, path, testOpts, api); err != nil {
		t.Fatal(err)
	}
	if got := api.take(); !reflect.DeepEqual(got, []call{{"remove", userEmail(a.ID), ""}, {"add", userEmail(a.ID), newUUID}}) {
		t.Fatalf("смена UUID: вызовы %v", got)
	}

	// Ничего не изменилось — ни одного вызова.
	if _, err := Sync(ctx, handle, path, testOpts, api); err != nil {
		t.Fatal(err)
	}
	if got := api.take(); len(got) != 0 {
		t.Fatalf("без изменений: вызовы %v", got)
	}
}

// API недоступен: изменение в панели не падает, файл записан (применится
// при перезапуске Xray), а недосланное досылается следующей
// синхронизацией — отключённый не сохраняет доступ из-за одного сбоя.
func TestSyncAPIFailureIsRetried(t *testing.T) {
	handle := configuredDB(t)
	addClient(t, handle, "andrey", awgPub)
	b := addClient(t, handle, "mama", awgPub2)
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	ctx := context.Background()
	if _, err := Sync(ctx, handle, path, testOpts, &fakeAPI{}); err != nil {
		t.Fatal(err)
	}

	if err := db.SetClientEnabled(handle, b.ID, false); err != nil {
		t.Fatal(err)
	}
	down := &fakeAPI{fail: errors.New("connection refused")}
	res, err := Sync(ctx, handle, path, testOpts, down)
	if err != nil {
		t.Fatalf("сбой API сорвал изменение: %v", err)
	}
	if res.APIErr == nil || !res.Written {
		t.Fatalf("сбой API не отражён: %+v", res)
	}
	if _, ok := fileUsers(t, path)[userEmail(b.ID)]; ok {
		t.Fatal("файл не обновлён при недоступном API")
	}

	c := addClient(t, handle, "papa", awgPub3)
	up := &fakeAPI{}
	res, err = Sync(ctx, handle, path, testOpts, up)
	if err != nil || res.APIErr != nil {
		t.Fatalf("повтор: %v %+v", err, res)
	}
	got := up.take()
	want := []call{{"remove", userEmail(b.ID), ""}, {"add", userEmail(c.ID), c.VLESSUUID}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("повтор: вызовы %v, ожидались %v", got, want)
	}
	if _, err := os.Stat(path + ".pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("после успешного повтора недосланное не очищено: %v", err)
	}
}

// Повтор безвреден, если Xray тем временем перезапустили и он уже в нужном
// состоянии: «уже есть» и «нет такого» — не ошибки.
func TestSyncRetryIsIdempotent(t *testing.T) {
	handle := configuredDB(t)
	a := addClient(t, handle, "andrey", awgPub)
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	ctx := context.Background()
	if _, err := Sync(ctx, handle, path, testOpts, &fakeAPI{}); err != nil {
		t.Fatal(err)
	}
	b := addClient(t, handle, "mama", awgPub2)
	if res, _ := Sync(ctx, handle, path, testOpts, &fakeAPI{fail: errors.New("down")}); res.APIErr == nil {
		t.Fatal("ожидался сбой API")
	}
	// Xray перезапустился и прочёл файл: оба пользователя уже на месте.
	restarted := &fakeAPI{users: map[string]string{userEmail(a.ID): a.VLESSUUID, userEmail(b.ID): b.VLESSUUID}}
	res, err := Sync(ctx, handle, path, testOpts, restarted)
	if err != nil || res.APIErr != nil {
		t.Fatalf("повтор после перезапуска: %v %+v", err, res)
	}
	if restarted.users[userEmail(b.ID)] != b.VLESSUUID {
		t.Fatal("пользователь потерян при повторе")
	}
}

// Ключи, путь и маскировку через API не поменять: Sync говорит, что нужен
// перезапуск Xray.
func TestSyncReportsRestartForServerSettings(t *testing.T) {
	handle := configuredDB(t)
	addClient(t, handle, "andrey", awgPub)
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	ctx := context.Background()
	if _, err := Sync(ctx, handle, path, testOpts, &fakeAPI{}); err != nil {
		t.Fatal(err)
	}
	s := fixtureSettings()
	s.Path = "/other"
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{}
	res, err := Sync(ctx, handle, path, testOpts, api)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RestartNeeded {
		t.Fatal("смена пути XHTTP не потребовала перезапуска")
	}
	if len(api.take()) != 0 {
		t.Fatal("API дёрнут без изменений в пользователях")
	}
}

func TestConfigPathFollowsAWGConfig(t *testing.T) {
	t.Setenv("AMNEZIA_XRAY_CONFIG_PATH", "")
	if got := ConfigPath("/tmp/p/awg0.conf"); got != "/tmp/p/xray/config.json" {
		t.Fatalf("ConfigPath = %q", got)
	}
	t.Setenv("AMNEZIA_XRAY_CONFIG_PATH", "/elsewhere/x.json")
	if got := ConfigPath("/tmp/p/awg0.conf"); got != "/elsewhere/x.json" {
		t.Fatalf("ConfigPath с AMNEZIA_XRAY_CONFIG_PATH = %q", got)
	}
}

// Байты запросов совпадают с proto.Marshal на типах xray-core
// (app/proxyman/command, common/protocol, common/serial, proxy/vless);
// эталон снят с xray-core v1.260327.0.
func TestAlterInboundEncodingMatchesProtobuf(t *testing.T) {
	const (
		wantAdd = "0a0b766c6573732d786874747012780a2a787261792e6170702e70726f78796d616e2e636f6d6d616e642e" +
			"416464557365724f7065726174696f6e124a0a48120263311a420a18787261792e70726f78792e766c6573732e" +
			"4163636f756e7412260a2433663235303465302d346638392d343164332d396130632d303330356538326333333031"
		wantRemove = "0a0b766c6573732d786874747012350a2d787261792e6170702e70726f78796d616e2e636f6d6d616e642e" +
			"52656d6f7665557365724f7065726174696f6e12040a026331"
	)
	if got := hex.EncodeToString(addUserRequest(InboundTag, "c1", "3f2504e0-4f89-41d3-9a0c-0305e82c3301")); got != wantAdd {
		t.Fatalf("AddUser:\n got %s\nwant %s", got, wantAdd)
	}
	if got := hex.EncodeToString(removeUserRequest(InboundTag, "c1")); got != wantRemove {
		t.Fatalf("RemoveUser:\n got %s\nwant %s", got, wantRemove)
	}
}

// fakeGRPC — сервер HTTP/2 без TLS, отвечающий как HandlerService Xray.
func fakeGRPC(t *testing.T, handler func(w http.ResponseWriter, body []byte)) string {
	t.Helper()
	return fakeGRPCAt(t, alterInboundPath, func(w http.ResponseWriter, _ *http.Request, body []byte) {
		handler(w, body)
	})
}

// fakeGRPCAt — сервер HTTP/2 без TLS, отвечающий как gRPC-сервер Xray на
// методе path (пусто — на любом).
func fakeGRPCAt(t *testing.T, path string, handler func(w http.ResponseWriter, r *http.Request, body []byte)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Protocols: &protocols,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 || r.Method != http.MethodPost || (path != "" && r.URL.Path != path) ||
				r.Header.Get("Content-Type") != "application/grpc" {
				t.Errorf("запрос %s %s %s %q", r.Proto, r.Method, r.URL.Path, r.Header.Get("Content-Type"))
			}
			frame, _ := io.ReadAll(r.Body)
			if len(frame) < 5 || frame[0] != 0 || int(binary.BigEndian.Uint32(frame[1:5])) != len(frame)-5 {
				t.Errorf("рамка gRPC: % x", frame)
				return
			}
			handler(w, r, frame[5:])
		}),
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func TestAPICallsHandlerService(t *testing.T) {
	var got [][]byte
	addr := fakeGRPC(t, func(w http.ResponseWriter, body []byte) {
		got = append(got, body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{0, 0, 0, 0, 0}) // пустой AlterInboundResponse
		w.Header().Set("Grpc-Status", "0")
	})
	api := NewAPI(addr)
	ctx := context.Background()
	if err := api.AddUser(ctx, InboundTag, "c1", "3f2504e0-4f89-41d3-9a0c-0305e82c3301"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if err := api.RemoveUser(ctx, InboundTag, "c1"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
	if len(got) != 2 || string(got[0]) != string(addUserRequest(InboundTag, "c1", "3f2504e0-4f89-41d3-9a0c-0305e82c3301")) ||
		string(got[1]) != string(removeUserRequest(InboundTag, "c1")) {
		t.Fatalf("сервер получил %x", got)
	}
}

func TestAPIErrors(t *testing.T) {
	addr := fakeGRPC(t, func(w http.ResponseWriter, body []byte) {
		// trailers-only: ошибка приходит одними заголовками.
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "2")
		w.Header().Set("Grpc-Message", "User%20c1%20already%20exists.")
		w.WriteHeader(http.StatusOK)
	})
	err := NewAPI(addr).AddUser(context.Background(), InboundTag, "c1", "3f2504e0-4f89-41d3-9a0c-0305e82c3301")
	if !isAlreadyExists(err) {
		t.Fatalf("ошибка «уже есть» не распознана: %v", err)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	if err := NewAPI(dead).RemoveUser(context.Background(), InboundTag, "c1"); err == nil || isNotFound(err) {
		t.Fatalf("недоступный API: %v", err)
	}
}

// Служебная учётка входа в конфиге всегда и переживает любые изменения
// клиентов: снять её значило бы отрезать всех, кто ходит через вход.
func TestSyncKeepsServiceUsers(t *testing.T) {
	handle := newDB(t)
	a := addClient(t, handle, "andrey", awgPub)
	const ruentry = "5a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	if _, _, err := db.ImportVLESS(handle, db.VLESSImport{
		Settings:     *fixtureSettings(),
		ServiceUsers: []db.VLESSServiceUser{{Email: "ruentry", UUID: ruentry}},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "xray", "config.json")
	ctx := context.Background()
	if _, err := Sync(ctx, handle, path, testOpts, &fakeAPI{}); err != nil {
		t.Fatal(err)
	}
	if fileUsers(t, path)["ruentry"] != ruentry {
		t.Fatal("служебной учётки нет в конфиге")
	}
	if err := db.SetClientEnabled(handle, a.ID, false); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{}
	if _, err := Sync(ctx, handle, path, testOpts, api); err != nil {
		t.Fatal(err)
	}
	if got := api.take(); !reflect.DeepEqual(got, []call{{"remove", userEmail(a.ID), ""}}) {
		t.Fatalf("вызовы %v: снимать надо только клиента", got)
	}
	if fileUsers(t, path)["ruentry"] != ruentry {
		t.Fatal("служебная учётка пропала вместе с клиентом")
	}
}

// Журнала доступа нет, ошибки — в stderr (в json-file Docker с его
// ротацией): иначе на диске копилась бы история сайтов каждого клиента без
// предела (amnezia-vpn-server-14y0.3).
func TestRenderKeepsNoAccessLog(t *testing.T) {
	data, err := Render(fixtureSettings(), nil, nil, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Log.Access != "none" || c.Log.Error != "" {
		t.Fatalf("журналы: %+v", c.Log)
	}
}

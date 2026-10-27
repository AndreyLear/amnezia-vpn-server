package xray

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// shortDir — каталог для сокета: путь Unix-сокета ограничен ~104 байтами,
// а t.TempDir() на macOS длиннее.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "xr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// fakeGRPCUnix — fakeGRPC на Unix-сокете.
func fakeGRPCUnix(t *testing.T, sock string, handler func(w http.ResponseWriter, body []byte)) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Protocols: &protocols,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 || r.URL.Path != alterInboundPath {
				t.Errorf("запрос %s %s", r.Proto, r.URL.Path)
			}
			buf := make([]byte, 1<<12)
			n, _ := r.Body.Read(buf)
			handler(w, buf[5:n])
		}),
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

// API ходит в Xray через Unix-сокет из общего каталога: Xray в сети хоста,
// панель в своей сети Docker, и петля у них разная (решение оркестратора
// по amnezia-vpn-server-14y0.2).
func TestAPIOverUnixSocket(t *testing.T) {
	sock := filepath.Join(shortDir(t), "api.sock")
	var got [][]byte
	fakeGRPCUnix(t, sock, func(w http.ResponseWriter, body []byte) {
		got = append(got, append([]byte(nil), body...))
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{0, 0, 0, 0, 0})
		w.Header().Set("Grpc-Status", "0")
	})
	api := NewAPI("unix:" + sock)
	ctx := context.Background()
	if err := api.AddUser(ctx, InboundTag, "c1", "3f2504e0-4f89-41d3-9a0c-0305e82c3301"); err != nil {
		t.Fatalf("AddUser по сокету: %v", err)
	}
	if err := api.RemoveUser(ctx, InboundTag, "c1"); err != nil {
		t.Fatalf("RemoveUser по сокету: %v", err)
	}
	if len(got) != 2 || string(got[1]) != string(removeUserRequest(InboundTag, "c1")) {
		t.Fatalf("сервер получил %x", got)
	}
	if err := NewAPI("unix:"+sock+".missing").RemoveUser(ctx, InboundTag, "c1"); err == nil {
		t.Fatal("несуществующий сокет: ошибки нет")
	}
}

// API-вход в конфиге — dokodemo-door на сокете с правами только для root
// и маршрутом в outbound api раньше остальных правил.
func TestRenderAPIInboundOnSocket(t *testing.T) {
	data, err := Render(fixtureSettings(), nil, nil, Options{APIAddr: "unix:/run/xray/api.sock"})
	if err != nil {
		t.Fatal(err)
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.API.Listen != "" || c.API.Tag != "api" {
		t.Fatalf("api: %+v — на TCP слушать нельзя", c.API)
	}
	if len(c.Inbounds) != 2 {
		t.Fatalf("входов %d", len(c.Inbounds))
	}
	in := c.Inbounds[1]
	if in.Tag != "api" || in.Protocol != "dokodemo-door" || in.Listen != "/run/xray/api.sock,0600" ||
		in.Settings.Network != "unix" || in.Settings.Port == 0 || in.Port != 0 {
		t.Fatalf("API-вход: %+v", in)
	}
	if r := c.Routing.Rules[0]; len(r.InboundTag) != 1 || r.InboundTag[0] != "api" || r.OutboundTag != "api" {
		t.Fatalf("первое правило маршрутизации: %+v", r)
	}
	if strings.Contains(string(data), "0666") || strings.Contains(string(data), "0777") {
		t.Fatal("сокет API открыт всем")
	}
}

// Брошенный после аварии сокет убирается, живой — нет.
func TestClearStaleSocket(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "api.sock")
	addr := "unix:" + sock

	if err := ClearStaleSocket(addr); err != nil {
		t.Fatalf("без сокета: %v", err)
	}

	// Живой Xray: слушает и держит flock на .lock.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	lock, err := os.Create(sock + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := ClearStaleSocket(addr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatal("удалён сокет живого Xray")
	}

	// Xray убит: блокировка снята, файл сокета остался.
	ln.Close()
	lock.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatal("сокет должен был остаться после аварии")
	}
	if err := ClearStaleSocket(addr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("брошенный сокет не убран: %v", err)
	}
	if ln, err := net.Listen("unix", sock); err != nil {
		t.Fatalf("после уборки слушать снова нельзя: %v", err)
	} else {
		ln.Close()
	}
}

// Живая проверка против настоящего Xray: AMNEZIA_TEST_XRAY_BIN — путь к
// бинарнику xray. Конфиг тот, что пишет панель, без правила geoip:private
// (geoip.dat в тестовом окружении нет); API — по Unix-сокету.
func TestLiveXrayAPI(t *testing.T) {
	bin := os.Getenv("AMNEZIA_TEST_XRAY_BIN")
	if bin == "" {
		t.Skip("AMNEZIA_TEST_XRAY_BIN не задан")
	}
	dir := shortDir(t)
	sock := filepath.Join(dir, "api.sock")
	opt := Options{APIAddr: "unix:" + sock}
	clients := []db.VLESSClient{{ID: 1, UUID: "3f2504e0-4f89-41d3-9a0c-0305e82c3301"}}
	c := build(fixtureSettings(), clients, nil, opt)
	c.Inbounds[0].Port = 14432
	c.Routing.Rules = c.Routing.Rules[:1]
	data, err := marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", cfg)
	out := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; ; i++ {
		if fi, err := os.Stat(sock); err == nil {
			if fi.Mode().Perm() != 0o600 {
				t.Fatalf("права сокета %v", fi.Mode().Perm())
			}
			break
		}
		if i > 50 {
			t.Fatalf("xray не открыл сокет API:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	api := NewAPI("unix:" + sock)
	ctx := context.Background()
	if err := api.AddUser(ctx, InboundTag, "c2", "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d"); err != nil {
		t.Fatalf("AddUser: %v\n%s", err, out)
	}
	if err := api.AddUser(ctx, InboundTag, "c2", "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d"); !isAlreadyExists(err) {
		t.Fatalf("повторный AddUser: %v", err)
	}
	// Статистика включена конфигом и читается тем же сокетом
	// (amnezia-vpn-server-14y0.8).
	if _, err := api.UserTraffic(ctx); err != nil {
		t.Fatalf("UserTraffic: %v\n%s", err, out)
	}
	if _, err := api.Uptime(ctx); err != nil {
		t.Fatalf("Uptime: %v\n%s", err, out)
	}
	if err := api.RemoveUser(ctx, InboundTag, "c1"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
	if err := api.RemoveUser(ctx, InboundTag, "c1"); !isNotFound(err) {
		t.Fatalf("повторный RemoveUser: %v", err)
	}

	// Аварийная остановка оставляет сокет; без уборки Xray не поднимется.
	cmd.Process.Kill()
	cmd.Wait()
	if _, err := os.Stat(sock); err != nil {
		t.Skipf("после SIGKILL сокета нет — уборка не нужна этой версии xray: %v", err)
	}
	again := exec.Command(bin, "run", "-c", cfg)
	if b, err := again.CombinedOutput(); err == nil {
		t.Logf("xray поднялся на брошенном сокете:\n%s", b)
	}
	if err := ClearStaleSocket("unix:" + sock); err != nil {
		t.Fatal(err)
	}
	third := exec.Command(bin, "run", "-c", cfg)
	third.Stdout, third.Stderr = out, out
	if err := third.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { third.Process.Kill(); third.Wait() })
	for i := 0; ; i++ {
		if err := api.AddUser(ctx, InboundTag, "c3", "11111111-2222-4333-8444-555555555555"); err == nil {
			break
		} else if i > 50 {
			t.Fatalf("после уборки сокета xray не поднялся: %v\n%s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

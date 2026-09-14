package awgconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Добивка S4 входит в каждый транспортный пакет: без неё в расчёте потолок
// 1440 на пути 1500 резал пакеты на сервере (amnezia-vpn-server-bctr).
func TestTunnelMTUsSubtractS4(t *testing.T) {
	cases := []struct {
		name                string
		env                 string
		client, padding     uint16
		wantDevice, wantDef uint16
	}{
		{"путь 1500, S4 = 29", "1440", 1360, 29, 1411, 1360},
		{"путь 1500 без S4 — как раньше", "1440", 1360, 0, 1440, 1360},
		{"путь 1476, S4 = 29", "1416", 1360, 29, 1387, 1360},
		{"узкий путь: осторожное значение тоже зажимается", "1360", 1360, 29, 1331, 1331},
		{"совсем узкий путь не опускается ниже 1280", "1290", 1280, 29, 1280, 1280},
		{"потолок не измеряли — S4 не вычитается", "", 1360, 29, 1360, 1360},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(deviceMTUEnv, tc.env)
			device, def := TunnelMTUs(tc.client, tc.padding)
			if device != tc.wantDevice || def != tc.wantDef {
				t.Fatalf("TunnelMTUs(%d, %d) при %s=%q = (%d, %d), ждали (%d, %d)",
					tc.client, tc.padding, deviceMTUEnv, tc.env, device, def, tc.wantDevice, tc.wantDef)
			}
			// Полный пакет наружу не больше того, что тянет путь сервера.
			if tc.env != "" && tc.padding > 0 && device > MinMTU {
				measured := uint16(0)
				for _, ch := range tc.env {
					measured = measured*10 + uint16(ch-'0')
				}
				if int(device)+EncapsulationOverhead+int(tc.padding) > int(measured)+EncapsulationOverhead {
					t.Fatalf("пакет %d + %d + S4 %d выходит за путь", device, EncapsulationOverhead, tc.padding)
				}
			}
		})
	}
}

// awg0.conf на сервере с S4 = 29 и путём 1500: интерфейс 1411, клиент со
// своими 1420 получает маршрут 1411, а панель не даёт задать больше 1411.
func TestGenerateAndClientMTUMaxWithS4(t *testing.T) {
	t.Setenv(deviceMTUEnv, "1440")
	handle, dir := newTestDB(t)
	seedServer(t, handle, `{"jc":3,"jmin":1,"jmax":5,"s1":1,"s2":2,"s3":3,"s4":29}`, "1.1.1.1")
	seedClient(t, handle, 1, testKey(4), true, "10.8.0.2/32")
	if err := db.UpdateClientMTU(handle, 1, 1420); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "awg0.conf")
	if err := Generate(handle, target); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "MTU = 1411\n") {
		t.Fatalf("интерфейс не зажат добивкой S4:\n%s", got)
	}
	if !strings.Contains(got, "# amnezia-route-mtu = 1411\n") {
		t.Fatalf("маршрут клиента с 1420 не зажат до 1411:\n%s", got)
	}
	max, err := ClientMTUMax(handle)
	if err != nil || max != 1411 {
		t.Fatalf("ClientMTUMax = %d, %v; ждали 1411", max, err)
	}

	t.Setenv(deviceMTUEnv, "")
	if max, err := ClientMTUMax(handle); err != nil || max != 0 {
		t.Fatalf("без измеренного потолка ClientMTUMax = %d, %v; ждали 0 (неизвестно)", max, err)
	}
}

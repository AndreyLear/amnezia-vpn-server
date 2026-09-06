package cli

import (
	"strings"
	"testing"
)

// Установщик спрашивает у бинарника, что тот умеет, ПРЕЖДЕ чем менять
// что-либо на хосте (amnezia-vpn-server-v4xj). Значит список обязан быть
// правдой: каждый объявленный флаг команда должна принимать, иначе проверка
// пропустит образ, на котором установка развалится позже и посреди работы.
func TestCapabilitiesTokensAreAccepted(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)

	values := map[string]string{
		"address6":    "fd00:dead:beef::1/64",
		"listen-port": "51821",
		"mtu":         "1340",
		"dns":         testDNS,
	}

	out := c.mustRun("capabilities")
	tokens := strings.Fields(out)
	if len(tokens) == 0 {
		t.Fatal("capabilities: пустой список — установщику нечего проверять")
	}

	for _, token := range tokens {
		command, flag, ok := strings.Cut(token, ":")
		if !ok {
			t.Fatalf("capabilities: токен %q не в виде <команда>:<флаг>", token)
		}
		value, ok := values[flag]
		if !ok {
			t.Fatalf("capabilities: у флага %q нет значения для проверки — тест надо дополнить", flag)
		}
		var args []string
		switch command {
		case "server-update":
			args = []string{"server", "update", "--" + flag, value}
		case "server-init":
			// init на уже созданном сервере откажет по существу, но флаг
			// обязан быть разобран: нас интересует именно это.
			args = []string{"server", "init", testServerCIDR, "51820", "--" + flag, value}
		default:
			t.Fatalf("capabilities: неизвестная команда в токене %q", token)
		}
		_, _, errb := c.run(args...)
		if strings.Contains(errb, "unknown flag") {
			t.Errorf("%s объявлен в capabilities, но %v отвечает: %s", token, args, strings.TrimSpace(errb))
		}
	}
}

func TestCapabilitiesRejectsArguments(t *testing.T) {
	c := newCtx(t)
	code, _, errb := c.run("capabilities", "лишнее")
	if code != 2 {
		t.Fatalf("capabilities с аргументом: код %d, ожидался 2", code)
	}
	if !strings.Contains(errb, "takes no arguments") {
		t.Fatalf("capabilities с аргументом: не объяснил отказ: %s", errb)
	}
}

// Флаг, который установщик передаёт в server update, обязан быть в списке:
// иначе следующая правка установщика повторит историю с --address6.
func TestCapabilitiesCoversAddress6(t *testing.T) {
	c := newCtx(t)
	out := c.mustRun("capabilities")
	if !strings.Contains(out, "server-update:address6") {
		t.Fatalf("capabilities не объявляет server-update:address6:\n%s", out)
	}
}

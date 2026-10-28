import { describe, expect, it } from "vitest";

import { vlessRouteHint, vlessRouteLabel } from "@/components/VlessSection";

// Подписи маршрутов (решение владельца 28.10.2026): узел один — просто
// «Через вход», узлов несколько — с именем узла, не с приставкой ссылки.
describe("vlessRouteLabel", () => {
  const direct = { entry: "", prefix: "", link: "vless://direct" };
  const entry = { entry: "entry", prefix: "VLESS ", link: "vless://entry" };
  const second = { entry: "kz", prefix: "VLESS ", link: "vless://kz" };

  it("names the direct route", () => {
    expect(vlessRouteLabel(direct, [direct, entry])).toBe("Напрямую");
  });

  it("omits the node name when there is one entry node", () => {
    expect(vlessRouteLabel(entry, [direct, entry])).toBe("Через вход");
  });

  it("names the node when there are several", () => {
    expect(vlessRouteLabel(entry, [direct, entry, second])).toBe("Через вход entry");
    expect(vlessRouteLabel(second, [direct, entry, second])).toBe("Через вход kz");
  });
});

describe("vlessRouteHint", () => {
  it("describes the entry route without countries or workarounds", () => {
    const hint = vlessRouteHint({ entry: "entry", prefix: "VLESS ", link: "" });
    expect(hint).toBe(
      "Подходит, когда напрямую подключиться не получается. Сайты из списка прямого выхода открываются без основного сервера.",
    );
    expect(hint).not.toMatch(/росси|блок|обход|мимо VPN/i);
  });
});

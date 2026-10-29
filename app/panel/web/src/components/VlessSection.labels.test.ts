import { describe, expect, it } from "vitest";

import { vlessRouteHint, vlessRouteLabel } from "@/components/VlessSection";

// Подписи маршрутов (решения владельца 28.10.2026, amnezia-vpn-server-14y0.17):
// узел один — просто «Каскад», узлов несколько — с именем узла, не с
// приставкой ссылки.
describe("vlessRouteLabel", () => {
  const direct = { entry: "", prefix: "", link: "vless://direct" };
  const entry = { entry: "entry", prefix: "VLESS ", link: "vless://entry" };
  const second = { entry: "kz", prefix: "VLESS ", link: "vless://kz" };

  it("names the direct route", () => {
    expect(vlessRouteLabel(direct, [direct, entry])).toBe("Напрямую");
  });

  it("omits the node name when there is one entry node", () => {
    expect(vlessRouteLabel(entry, [direct, entry])).toBe("Каскад");
  });

  it("names the node when there are several", () => {
    expect(vlessRouteLabel(entry, [direct, entry, second])).toBe("Каскад entry");
    expect(vlessRouteLabel(second, [direct, entry, second])).toBe("Каскад kz");
  });

  // Имя каскада от владельца (amnezia-vpn-server-q1do.4) — само по себе.
  it("uses the owner's cascade name as is", () => {
    const named = { ...entry, title: "Москва" };
    expect(vlessRouteLabel(named, [direct, named])).toBe("Москва");
    expect(vlessRouteLabel(named, [direct, named, second])).toBe("Москва");
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

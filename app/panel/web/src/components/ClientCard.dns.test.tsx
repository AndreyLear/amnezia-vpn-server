import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ClientCard } from "@/components/ClientCard";
import type { Client } from "@/lib/api";

// Отметка «имена разрешаются мимо туннеля» (amnezia-vpn-server-g0vd).
// Так выглядит подключение с роутера: устройства дома берут DNS у роутера, до
// нас запрос не доходит, и подмена ответа провайдером остаётся невидимой.
const base: Client = {
  id: 1,
  name: "router",
  description: "",
  address: "10.8.0.4/32",
  enabled: true,
  online: true,
  last_handshake_utc: "2026-09-07T00:00:00Z",
  rx_bytes: 0,
  tx_bytes: 0,
  mtu: 0,
  dns_bypass: false,
};

const originalMatchMedia = window.matchMedia;

function wideScreen() {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}

describe("отметка об обходе резолвера", () => {
  it("клиент, спрашивающий у нас, отметки не получает", () => {
    wideScreen();
    render(<ClientCard client={base} />);

    expect(screen.queryByLabelText("Нет запросов к нашему резолверу")).toBeNull();
    window.matchMedia = originalMatchMedia;
  });

  it("клиент, спрашивающий мимо, получает отметку", () => {
    wideScreen();
    render(<ClientCard client={{ ...base, dns_bypass: true }} />);

    expect(
      screen.getByLabelText("Нет запросов к нашему резолверу"),
    ).toBeInTheDocument();
    window.matchMedia = originalMatchMedia;
  });
});

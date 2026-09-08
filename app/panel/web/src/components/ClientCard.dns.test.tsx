import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ClientCard } from "@/components/ClientCard";
import type { Client } from "@/lib/api";

// Раньше карточка вешала жёлтый щит на клиента, чей DNS шёл мимо туннеля
// (amnezia-vpn-server-g0vd). Владелец за один вечер трижды положил себе
// домашнюю сеть, пытаясь заставить этот щит погаснуть, — постоянно горящий
// значок пугал, хотя ничего не было сломано: так выглядит подключение с
// роутера. Щит убран без замены (amnezia-vpn-server-m2cq): в списке клиентов
// об этом больше не сказано ни словом, ни цветом, ни иконкой — сам факт
// остаётся честным и дешёвым в dns_seen/status.json/API, но список клиентов
// его не показывает.
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

describe("щит обхода резолвера убран без замены", () => {
  it("клиент, спрашивающий у нас, значка не получает", () => {
    wideScreen();
    render(<ClientCard client={base} />);

    expect(screen.queryByLabelText("Нет запросов к нашему резолверу")).toBeNull();
    expect(document.querySelector("svg.lucide-shield-alert")).toBeNull();
    window.matchMedia = originalMatchMedia;
  });

  it("клиент, спрашивающий мимо, тоже значка не получает", () => {
    wideScreen();
    render(<ClientCard client={{ ...base, dns_bypass: true }} />);

    expect(screen.queryByLabelText("Нет запросов к нашему резолверу")).toBeNull();
    expect(document.querySelector("svg.lucide-shield-alert")).toBeNull();
    expect(screen.queryByText(/резолвер/i)).toBeNull();
    window.matchMedia = originalMatchMedia;
  });
});

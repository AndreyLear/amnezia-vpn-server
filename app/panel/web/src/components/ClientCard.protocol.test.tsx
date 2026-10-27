import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ClientCard } from "@/components/ClientCard";
import type { Client } from "@/lib/api";

const base: Client = {
  id: 1,
  name: "Alice",
  description: "",
  address: "10.8.0.2/32",
  address6: "",
  enabled: true,
  online: false,
  last_handshake_utc: "2026-10-27T10:00:00Z",
  rx_bytes: 1024,
  tx_bytes: 2048,
  mtu: 0,
  rate_limit: 0,
  dns_bypass: false,
  vless_up_bytes: 0,
  vless_down_bytes: 0,
  vless_last_active_utc: null,
  vless_online: false,
};

// amnezia-vpn-server-14y0.8: строка клиента показывает активность и трафик
// протокола, по которому клиент был на связи последним, и называет его.
describe("ClientCard: последний протокол", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("AWG: рукопожатие и трафик AWG с пометкой AWG", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-27T10:05:00Z"));
    render(<ClientCard client={base} />);

    expect(screen.getByLabelText("Протокол: AWG")).toHaveTextContent("AWG");
    expect(screen.queryByLabelText("Протокол: VLESS")).toBeNull();
    expect(screen.getByText("5 мин")).toBeInTheDocument();
    expect(screen.getByText("2,0 Кб")).toBeInTheDocument();
    expect(screen.getByText("1,0 Кб")).toBeInTheDocument();
  });

  it("VLESS свежее: активность и трафик VLESS с пометкой VLESS", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-27T12:01:00Z"));
    render(
      <ClientCard
        client={{
          ...base,
          vless_last_active_utc: "2026-10-27T12:00:00Z",
          vless_online: true,
          vless_down_bytes: 5 * 1024 ** 2,
          vless_up_bytes: 3 * 1024,
        }}
      />,
    );

    expect(screen.getByLabelText("Протокол: VLESS")).toHaveTextContent("VLESS");
    expect(screen.getByText("1 мин")).toBeInTheDocument();
    expect(screen.getByText("5,0 Мб")).toBeInTheDocument();
    expect(screen.getByText("3,0 Кб")).toBeInTheDocument();
    // Трафик AWG не показан.
    expect(screen.queryByText("2,0 Кб")).toBeNull();
    const icon = screen.getByText("1 мин").parentElement?.querySelector("svg");
    expect(icon).toHaveClass("text-emerald-500");
  });

  it("подсказки называют протокол", async () => {
    const user = userEvent.setup();
    render(
      <ClientCard client={{ ...base, last_handshake_utc: null, vless_last_active_utc: "2026-10-27T12:00:00Z" }} />,
    );

    await user.hover(screen.getByLabelText("Протокол: VLESS"));
    expect((await screen.findAllByText("Последний раз на связи по VLESS")).length).toBeGreaterThan(0);
  });
});

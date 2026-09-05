import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ClientCard } from "@/components/ClientCard";
import type { Client } from "@/lib/api";

// Меню действий возвращает фокус на кнопку при закрытии. Для клавиатуры это
// единственно верно, для мыши — кольцо фокуса, которое горит на кнопке уже
// после того, как меню закрыли (amnezia-vpn-server-c7iz).
const client: Client = {
  id: 1,
  name: "Alice",
  description: "",
  address: "10.8.0.2/32",
  enabled: true,
  online: true,
  last_handshake_utc: "2026-09-06T00:00:00Z",
  rx_bytes: 0,
  tx_bytes: 0,
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

afterEach(() => {
  window.matchMedia = originalMatchMedia;
});

describe("фокус кнопки действий", () => {
  it("мышью открыли и закрыли — кнопка фокус не удерживает", async () => {
    wideScreen();
    const user = userEvent.setup();
    render(<ClientCard client={client} />);

    const trigger = screen.getByRole("button", { name: "Действия для Alice" });
    await user.click(trigger);
    expect(await screen.findByRole("menuitem", { name: "Сведения" })).toBeInTheDocument();

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(screen.queryByRole("menuitem", { name: "Сведения" })).toBeNull();
    });
    expect(document.activeElement).not.toBe(trigger);
  });

  it("с клавиатуры открыли и закрыли — фокус возвращается на кнопку", async () => {
    wideScreen();
    const user = userEvent.setup();
    render(<ClientCard client={client} />);

    const trigger = screen.getByRole("button", { name: "Действия для Alice" });
    trigger.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("menuitem", { name: "Сведения" })).toBeInTheDocument();

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(screen.queryByRole("menuitem", { name: "Сведения" })).toBeNull();
    });
    // Иначе человек, работающий с клавиатуры, теряет место, откуда пришёл.
    expect(document.activeElement).toBe(trigger);
  });
});

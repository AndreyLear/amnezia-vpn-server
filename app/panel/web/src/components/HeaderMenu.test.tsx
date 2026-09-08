import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { HeaderMenu } from "@/components/HeaderMenu";

// Меню в шапке (amnezia-vpn-server-8bt5): пункты добавляются списком, окно
// «О версиях» не оставляет пустых мест, а кольцо фокуса ведёт себя как у
// меню действий клиента (amnezia-vpn-server-c7iz).
const versions = {
  product: "2.9.0",
  latest: "2.9.0",
  amneziawg_go: "0.2.19",
  amneziawg_tools: "3.1.20260812",
  protocol: "AmneziaWG 2.0",
  schema: "8",
  tunnel_ipv6: true,
  tunnel_dns: true,
  watchdog: true,
  fail2ban: false,
  update_check: null,
  os: "ubuntu 24.04 (noble)",
  docker: "27.3.1",
};

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/versions")) {
        return new Response(JSON.stringify(versions), {
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({ ok: true }), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("меню в шапке", () => {
  it("открывается мышью и показывает свои пункты", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.click(screen.getByRole("button", { name: "Ещё" }));
    expect(await screen.findByText("О версиях")).toBeInTheDocument();
    expect(screen.getByText("Состояние служб")).toBeInTheDocument();
    expect(screen.getByText("Проверить обновления")).toBeInTheDocument();
  });

  // Пункты живут списком: новый добавляется строкой и вёрстку шапки не
  // трогает. Проверяем это тем, что все они и есть один список.
  it("рисует пункты одним списком, а не вёрсткой", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.click(screen.getByRole("button", { name: "Ещё" }));
    const items = await screen.findAllByRole("menuitem");
    expect(items.map((item) => item.textContent)).toEqual([
      "О версиях",
      "Состояние служб",
      "Журнал",
      "Проверить обновления",
    ]);
  });

  // Названия пунктов ломались на две строки: меню наследовало ширину кнопки,
  // а кнопка — квадратная иконка (amnezia-vpn-server-n8w3). В jsdom нет
  // раскладки, поэтому проверяем то, чем перенос запрещён.
  it("не переносит названия пунктов на две строки", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.click(screen.getByRole("button", { name: "Ещё" }));
    const menu = await screen.findByRole("menu");
    expect(menu).toHaveClass("min-w-max");
    // Ширина по кнопке — это и есть причина переносов.
    expect(menu.className).not.toContain("--radix-dropdown-menu-trigger-width");
    for (const item of screen.getAllByRole("menuitem")) {
      expect(item).toHaveClass("whitespace-nowrap");
    }
  });

  it("открывается с клавиатуры", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.tab();
    expect(screen.getByRole("button", { name: "Ещё" })).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(await screen.findByText("О версиях")).toBeInTheDocument();
  });

  // Значок — напоминание о невзятом выпуске, и он на кнопке меню, а не на
  // полосе: полосу закрывают крестиком, а напоминание должно остаться.
  it("показывает значок только когда есть что взять", () => {
    const { rerender } = render(<HeaderMenu />);
    expect(screen.queryByTestId("update-badge")).not.toBeInTheDocument();

    rerender(<HeaderMenu pendingUpdate />);
    expect(screen.getByTestId("update-badge")).toBeInTheDocument();
  });
});

describe("окно «О версиях»", () => {
  it("показывает всё, что определяет поведение сервера", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.click(screen.getByRole("button", { name: "Ещё" }));
    await user.click(await screen.findByText("О версиях"));

    expect(await screen.findByText("AmneziaWG 2.0")).toBeInTheDocument();
    expect(screen.getByText("0.2.19")).toBeInTheDocument();
    expect(screen.getByText("3.1.20260812")).toBeInTheDocument();
    expect(screen.getByText("ubuntu 24.04 (noble)")).toBeInTheDocument();
    expect(screen.getByText("27.3.1")).toBeInTheDocument();
  });

  // Пустое место читается как «ничего нет». Панель на развёртывании старше
  // этой возможности не знает — и должна сказать именно это.
  it("неизвестное называет неизвестным, а не оставляет пустым", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.click(screen.getByRole("button", { name: "Ещё" }));
    await user.click(await screen.findByText("О версиях"));

    // update_check пришёл null — это «неизвестно», а не «выключено».
    await waitFor(() => expect(screen.getByText("неизвестно")).toBeInTheDocument());
    // А выключенное показывается выключенным: fail2ban пришёл false.
    expect(screen.getByText("выключен")).toBeInTheDocument();
  });

  it("говорит, последняя ли версия", async () => {
    const user = userEvent.setup();
    render(<HeaderMenu />);

    await user.click(screen.getByRole("button", { name: "Ещё" }));
    await user.click(await screen.findByText("О версиях"));

    // amnezia-vpn-server-4yo4: «свежая» переименовано в «последняя» —
    // владелец принимал первое за отдельную, незнакомую версию.
    expect(await screen.findByText("2.9.0 — последняя")).toBeInTheDocument();
  });
});

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
  enabled: true,
  online: true,
  last_handshake_utc: "2026-08-16T00:00:00Z",
  rx_bytes: Math.round(0.1 * 1024 ** 3),
  tx_bytes: 0,
};

const originalMatchMedia = window.matchMedia;

function stubMinWidthSm(matches: boolean) {
  window.matchMedia = vi.fn((query: string) => ({
    matches: query === "(min-width: 640px)" ? matches : false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }));
}

describe("ClientCard", () => {
  afterEach(() => {
    vi.useRealTimers();
    window.matchMedia = originalMatchMedia;
  });

  it("shows a horizontal row with name, handshake, rx and tx and no online badge", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("1 мин")).toBeInTheDocument();
    expect(screen.queryByText("16.08.2026, 00:00:00")).not.toBeInTheDocument();
    expect(screen.queryByText("2026-08-16 00:00:00 UTC")).not.toBeInTheDocument();
    expect(screen.getByText("0,1 Гб")).toBeInTheDocument();
    expect(screen.getByText("0 Б")).toBeInTheDocument();
    expect(screen.queryByText("онлайн")).not.toBeInTheDocument();
    expect(screen.queryByText("офлайн")).not.toBeInTheDocument();

    const age = screen.getByText("1 мин");
    const icon = age.parentElement?.querySelector("svg");
    expect(icon).toHaveClass("text-emerald-500");

    const card = screen.getByText("Alice").closest("[data-slot=card]");
    expect(card).toHaveClass("flex-row");
    expect(card).toHaveClass("py-2");
    expect(card).toHaveClass("sm:py-1");
    expect(card).not.toHaveClass("h-full", "min-h-36");

    const content = screen.getByText("Alice").closest("[data-slot=card-content]");
    expect(content).toHaveClass("px-2");
    expect(content).toHaveClass("sm:px-4");
  });

  it("keeps the handshake icon muted when the client is offline", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={{ ...base, online: false }} />);

    expect(screen.queryByText("онлайн")).not.toBeInTheDocument();
    expect(screen.queryByText("офлайн")).not.toBeInTheDocument();

    const age = screen.getByText("1 мин");
    const icon = age.parentElement?.querySelector("svg");
    expect(icon).not.toHaveClass("text-emerald-500");
  });

  it("does not color rx or tx icons emerald when the client is online", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const rxIcon = screen.getByText("0,1 Гб").parentElement?.querySelector("svg");
    const txIcon = screen.getByText("0 Б").parentElement?.querySelector("svg");
    expect(rxIcon).not.toHaveClass("text-emerald-500");
    expect(txIcon).not.toHaveClass("text-emerald-500");
  });

  it("puts a one-shot hover sweep on the card frame", () => {
    render(<ClientCard client={base} />);

    const card = screen.getByText("Alice").closest("[data-slot=card]");
    expect(card).toHaveClass("client-card-sweep");
    expect(card).toHaveClass("relative");
  });

  it("brightens the card ring on hover", () => {
    render(<ClientCard client={base} />);

    const card = screen.getByText("Alice").closest("[data-slot=card]");
    expect(card).toHaveClass("hover:ring-foreground/25");
  });

  it("packs metrics and places the menu opposite the name on max-sm", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const name = screen.getByText("Alice");
    const content = name.closest("[data-slot=card-content]");
    expect(content).toHaveClass(
      "max-sm:grid",
      "max-sm:grid-cols-[minmax(0,1fr)_auto]",
      "gap-x-3",
      "gap-y-1",
    );
    expect(content).not.toHaveClass("gap-x-2");
    expect(content).not.toHaveClass("max-sm:flex-wrap");
    expect(name.parentElement).not.toHaveClass("max-sm:basis-full");

    const handshake = screen.getByText("1 мин");
    const menu = screen.getByRole("button", { name: "Действия для Alice" });
    expect(content).toContainElement(menu);

    let metrics: HTMLElement | null = handshake.parentElement;
    while (metrics && !metrics.classList.contains("sm:contents")) {
      metrics = metrics.parentElement;
    }
    expect(metrics).toHaveClass("flex", "shrink-0", "items-center", "gap-3", "max-sm:col-span-2", "sm:contents");
    expect(metrics).not.toHaveClass("gap-2");
    expect(metrics).not.toHaveClass("max-sm:justify-between", "max-sm:w-full");
    expect(metrics).not.toContainElement(menu);

    let menuCell: HTMLElement | null = menu;
    while (
      menuCell &&
      !(
        menuCell.classList.contains("max-sm:col-start-2") &&
        menuCell.classList.contains("max-sm:row-start-1")
      )
    ) {
      if (menuCell === content) {
        menuCell = null;
        break;
      }
      menuCell = menuCell.parentElement;
    }
    expect(menuCell).toHaveClass("max-sm:col-start-2", "max-sm:row-start-1");

    const icon = menu.querySelector("svg");
    expect(icon).toHaveClass("lucide-ellipsis-vertical");
    expect(icon).not.toHaveClass("lucide-ellipsis");
  });

  it("uses sm:contents on the metrics wrapper so handshake rx tx are grid cells", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const handshake = screen.getByText("1 мин");
    let wrapper: HTMLElement | null = handshake.parentElement;
    while (wrapper && !wrapper.classList.contains("sm:contents")) {
      wrapper = wrapper.parentElement;
    }
    expect(wrapper).toHaveClass("sm:contents");
  });

  it("opens info from the name without nesting the menu in that control", async () => {
    const user = userEvent.setup();
    const onInfo = vi.fn();
    render(<ClientCard client={base} onInfo={onInfo} />);

    const menu = screen.getByRole("button", { name: "Действия для Alice" });
    const nameButton = screen.getByRole("button", { name: "Alice" });
    expect(nameButton).not.toContainElement(menu);

    await user.click(nameButton);
    expect(onInfo).toHaveBeenCalledTimes(1);
  });

  it("shows Пауза and Включить when the client is disabled", async () => {
    const user = userEvent.setup();
    render(<ClientCard client={{ ...base, enabled: false }} />);

    const name = screen.getByText("Alice");
    const pause = screen.getByText("Пауза");
    expect(pause).toHaveClass("text-muted-foreground");
    expect(name.compareDocumentPosition(pause) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    const card = name.closest("[data-slot=card]");
    expect(card).toHaveClass("opacity-60");

    await user.click(screen.getByRole("button", { name: "Действия для Alice" }));
    expect(screen.getByRole("menuitem", { name: "Включить" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Отключить" })).not.toBeInTheDocument();
  });

  it("keeps 0.4rem gap between handshake icon and value, not 0.25rem", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const handshakeTrigger = screen.getByText("1 мин").parentElement;
    expect(handshakeTrigger).toHaveClass("gap-[0.4rem]");
    expect(handshakeTrigger).not.toHaveClass("gap-[0.25rem]");
  });

  it("uses 0.25rem gap between rx and tx icons and values, not 0.4rem", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const rxTrigger = screen.getByText("0,1 Гб").parentElement;
    const txTrigger = screen.getByText("0 Б").parentElement;
    for (const trigger of [rxTrigger, txTrigger]) {
      expect(trigger).toHaveClass("gap-[0.25rem]");
      expect(trigger).not.toHaveClass("gap-[0.4rem]");
    }
  });

  it("spaces metric groups with gap-3 on the sm:contents wrapper, not gap-2", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const handshake = screen.getByText("1 мин");
    let wrapper: HTMLElement | null = handshake.parentElement;
    while (wrapper && !wrapper.classList.contains("sm:contents")) {
      wrapper = wrapper.parentElement;
    }
    expect(wrapper).toHaveClass("gap-3", "sm:contents");
    expect(wrapper).not.toHaveClass("gap-2");
  });

  it("shows Последний handshake on handshake metric hover", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);
    const handshakeAge = screen.getByText("1 мин");
    vi.useRealTimers();
    const user = userEvent.setup({ pointerEventsCheck: 0 });

    await user.hover(handshakeAge);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Последний handshake");
  });

  it("shows Входящий трафик on rx metric hover", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<ClientCard client={base} />);

    await user.hover(screen.getByText("0,1 Гб"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Входящий трафик");
  });

  it("shows Исходящий трафик on tx metric hover", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<ClientCard client={base} />);

    await user.hover(screen.getByText("0 Б"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Исходящий трафик");
  });

  it("opens client info from the overflow button below sm, not a dropdown", async () => {
    stubMinWidthSm(false);
    const user = userEvent.setup();
    const onInfo = vi.fn();
    render(<ClientCard client={base} onInfo={onInfo} />);

    const menu = screen.getByRole("button", { name: "Действия для Alice" });
    expect(menu).not.toHaveAttribute("data-slot", "dropdown-menu-trigger");

    await user.click(menu);
    expect(onInfo).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menuitem")).not.toBeInTheDocument();
  });

  it("opens the actions dropdown from the overflow button on sm and up", async () => {
    stubMinWidthSm(true);
    const user = userEvent.setup();
    render(<ClientCard client={base} />);

    const menu = screen.getByRole("button", { name: "Действия для Alice" });
    expect(menu).toHaveAttribute("data-slot", "dropdown-menu-trigger");

    await user.click(menu);
    expect(screen.getByRole("menuitem", { name: "Сведения" })).toBeInTheDocument();
  });
});

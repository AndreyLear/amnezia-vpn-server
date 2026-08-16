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

describe("ClientCard", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows a horizontal row with name, handshake, rx and tx and no online badge", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("1 мин")).toBeInTheDocument();
    expect(screen.queryByText("2026-08-16 00:00:00 UTC")).not.toBeInTheDocument();
    expect(screen.getByText("0,1 Гб")).toBeInTheDocument();
    expect(screen.getByText("0 Б")).toBeInTheDocument();
    expect(screen.queryByText("онлайн")).not.toBeInTheDocument();
    expect(screen.queryByText("офлайн")).not.toBeInTheDocument();

    const card = screen.getByText("Alice").closest("[data-slot=card]");
    expect(card).toHaveClass("flex-row");
    expect(card).toHaveClass("py-2");
    expect(card).not.toHaveClass("h-full", "min-h-36");
  });

  it("wraps the name onto a full-width row on max-sm", () => {
    render(<ClientCard client={base} />);

    const name = screen.getByText("Alice");
    const content = name.closest("[data-slot=card-content]");
    expect(content).toHaveClass("max-sm:flex-wrap");
    expect(name.parentElement).toHaveClass("max-sm:basis-full");
  });

  it("uses sm:contents on the metrics wrapper so handshake rx tx and menu are grid cells", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const handshake = screen.getByText("1 мин");
    const menu = screen.getByRole("button", { name: "Действия для Alice" });
    let wrapper: HTMLElement | null = handshake;
    while (wrapper && !wrapper.contains(menu)) {
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

  it("uses 0.4rem gap between metric icons and values, not gap-1", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-16T00:01:00Z"));
    render(<ClientCard client={base} />);

    const handshakeTrigger = screen.getByText("1 мин").parentElement;
    const rxTrigger = screen.getByText("0,1 Гб").parentElement;
    const txTrigger = screen.getByText("0 Б").parentElement;

    for (const trigger of [handshakeTrigger, rxTrigger, txTrigger]) {
      expect(trigger).toHaveClass("gap-[0.4rem]");
      expect(trigger).not.toHaveClass("gap-1");
    }
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
});

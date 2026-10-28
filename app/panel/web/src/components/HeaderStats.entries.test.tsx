import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { HeaderStats } from "@/components/HeaderStats";
import type { EntryHost, HostSnapshot } from "@/lib/api";
import { formatBytes } from "@/lib/format";

// Строки «Каскад» под строкой основного сервера (amnezia-vpn-server-q1do.1):
// те же три числа в том же формате, слово окрашено как AWG и VLESS.

const DASH = "—";

const host: HostSnapshot = {
  cpu_percent: 2,
  ram_percent: 21,
  disk_percent: 26,
  ram_used_bytes: 1024 ** 3,
  ram_total_bytes: 4 * 1024 ** 3,
  disk_used_bytes: 6 * 1024 ** 3,
  disk_total_bytes: 25 * 1024 ** 3,
  iface: "awg0",
  iface_state: "up",
  vless_state: "up",
};

const entry: EntryHost = {
  name: "entry",
  state: "up",
  reporting: true,
  seen: true,
  cpu_percent: 5.4,
  ram_percent: 30,
  disk_percent: 12,
  ram_used_bytes: 600 * 1024 ** 2,
  ram_total_bytes: 2 * 1024 ** 3,
  disk_used_bytes: 2.1 * 1024 ** 3,
  disk_total_bytes: 18 * 1024 ** 3,
  rx_bytes: 1.2 * 1024 ** 3,
  tx_bytes: 300 * 1024 ** 2,
  interval_seconds: 60,
};

async function tooltipOf(label: string): Promise<HTMLElement> {
  const user = userEvent.setup({ pointerEventsCheck: 0 });
  await user.hover(screen.getByLabelText(label));
  return screen.findByRole("tooltip");
}

describe("HeaderStats — каскад", () => {
  it("без узлов строки каскада нет", () => {
    render(<HeaderStats host={{ ...host, entries: [] }} />);
    expect(screen.queryByText(/Каскад/)).not.toBeInTheDocument();
    render(<HeaderStats host={host} />);
    expect(screen.queryByText(/Каскад/)).not.toBeInTheDocument();
  });

  it("один узел — строка «Каскад» с тремя числами в формате основной", () => {
    render(<HeaderStats host={{ ...host, entries: [entry] }} />);
    const word = screen.getByLabelText("Состояние каскада");
    expect(word).toHaveTextContent(/^Каскад$/);
    expect(word).toHaveClass("text-xs", "text-muted-foreground");
    expect(screen.getByLabelText("CPU каскада")).toHaveTextContent("5%");
    expect(screen.getByLabelText("RAM каскада")).toHaveTextContent("30%");
    expect(screen.getByLabelText("Диск каскада")).toHaveTextContent(formatBytes(entry.disk_used_bytes!));
    // Строка каскада — под строкой основного сервера, отдельной строкой.
    const mainRow = screen.getByLabelText("CPU").closest("[class*='tabular-nums']") as HTMLElement;
    const entryRow = screen.getByLabelText("CPU каскада").closest("[class*='tabular-nums']") as HTMLElement;
    expect(entryRow).not.toBe(mainRow);
    expect(entryRow).toContainElement(word);
    expect(mainRow.compareDocumentPosition(entryRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByLabelText("CPU")).toHaveTextContent("2%");
  });

  it("несколько узлов — по строке «Каскад <имя>» на каждый", () => {
    render(
      <HeaderStats
        host={{ ...host, entries: [{ ...entry, name: "de-1" }, { ...entry, name: "entry", cpu_percent: 80 }] }}
      />,
    );
    expect(screen.getByLabelText("Состояние каскада de-1")).toHaveTextContent(/^Каскад de-1$/);
    expect(screen.getByLabelText("Состояние каскада entry")).toHaveTextContent(/^Каскад entry$/);
    expect(screen.getByLabelText("CPU каскада entry")).toHaveTextContent("80%");
    expect(screen.getByLabelText("CPU каскада de-1")).toHaveTextContent("5%");
  });

  it("узел молчит — слово красное, вместо чисел прочерки", () => {
    render(
      <HeaderStats
        host={{
          ...host,
          entries: [{ name: "entry", state: "down", reporting: false, seen: true }],
        }}
      />,
    );
    expect(screen.getByLabelText("Состояние каскада")).toHaveClass("text-red-500");
    expect(screen.getByLabelText("CPU каскада")).toHaveTextContent(DASH);
    expect(screen.getByLabelText("RAM каскада")).toHaveTextContent(DASH);
    expect(screen.getByLabelText("Диск каскада")).toHaveTextContent(DASH);
  });

  it("подсказка работающего узла — трафик за интервал", async () => {
    render(<HeaderStats host={{ ...host, entries: [entry] }} />);
    const tip = await tooltipOf("Состояние каскада");
    expect(tip).toHaveTextContent("Каскад работает");
    expect(tip).toHaveTextContent(
      `За минуту принято ${formatBytes(entry.rx_bytes!)}, отправлено ${formatBytes(entry.tx_bytes!)}`,
    );
    expect(tip.textContent).not.toMatch(/\.$/);
    expect(tip).toHaveAttribute("data-side", "right");
  });

  it("интервал дольше минуты — так и сказано", async () => {
    render(<HeaderStats host={{ ...host, entries: [{ ...entry, interval_seconds: 180 }] }} />);
    expect(await tooltipOf("Состояние каскада")).toHaveTextContent("За 3 мин принято");
  });

  it("без трафика — только «Каскад работает»", async () => {
    render(<HeaderStats host={{ ...host, entries: [{ ...entry, rx_bytes: null, tx_bytes: null }] }} />);
    const tip = await tooltipOf("Состояние каскада");
    expect(tip.textContent).toBe("Каскад работает");
  });

  it.each([
    [
      "старый узел",
      { state: "up", reporting: false, seen: true },
      "Каскад на связи, но показатели не приходят — на нём старая версия. Обновите: запустите на узле install.sh --role entry",
      "text-muted-foreground",
    ],
    [
      "молчит",
      { state: "down", reporting: false, seen: true },
      "Каскад не выходил на связь больше 5 минут. Проверьте, что сервер включён и видит панель",
      "text-red-500",
    ],
    [
      "ни разу",
      { state: "down", reporting: false, seen: false },
      "Каскад ещё ни разу не выходил на связь. Проверьте адрес панели и секрет узла на нём",
      "text-red-500",
    ],
  ] as const)("подсказка: %s", async (_name, state, copy, color) => {
    render(<HeaderStats host={{ ...host, entries: [{ name: "entry", ...state }] }} />);
    expect(screen.getByLabelText("Состояние каскада")).toHaveClass(color);
    const tip = await tooltipOf("Состояние каскада");
    expect(tip.textContent).toBe(copy);
  });

  it("подсказки чисел каскада — как у основного сервера, но про каскад", async () => {
    render(<HeaderStats host={{ ...host, entries: [entry] }} />);
    const tip = await tooltipOf("CPU каскада");
    expect(tip.textContent).toBe("Загрузка CPU каскада: 5%");
  });

  it("подсказка диска каскада называет узел, когда их несколько", async () => {
    render(<HeaderStats host={{ ...host, entries: [entry, { ...entry, name: "de-1" }] }} />);
    const tip = await tooltipOf("Диск каскада de-1");
    expect(tip.textContent).toBe(
      `Загрузка диска каскада de-1: 12% (${formatBytes(entry.disk_used_bytes!)} / ${formatBytes(entry.disk_total_bytes!)})`,
    );
  });
});

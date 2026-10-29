import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { HeaderStats } from "@/components/HeaderStats";
import type { EntryHost, HostSnapshot } from "@/lib/api";

// Имена серверов в шапке (amnezia-vpn-server-q1do.4): без имени — как
// было, с именем — имя владельца; клик по строке открывает окно сервера.

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
  cpu_percent: 5,
  ram_percent: 30,
  disk_percent: 12,
  ram_used_bytes: 600 * 1024 ** 2,
  ram_total_bytes: 2 * 1024 ** 3,
  disk_used_bytes: 2 * 1024 ** 3,
  disk_total_bytes: 18 * 1024 ** 3,
};

async function tooltipOf(label: string): Promise<HTMLElement> {
  const user = userEvent.setup({ pointerEventsCheck: 0 });
  await user.hover(screen.getByLabelText(label));
  return screen.findByRole("tooltip");
}

describe("HeaderStats — имена", () => {
  it("без имени — строка основного без подписи, кнопка «Сервер»", () => {
    render(<HeaderStats host={{ ...host, server_name: "", entries: [entry] }} />);
    const button = screen.getByRole("button", { name: "Сервер" });
    expect(button).toContainElement(screen.getByLabelText("CPU"));
    expect(screen.getByRole("button", { name: "Каскад" })).toContainElement(screen.getByLabelText("CPU каскада"));
    expect(screen.getByLabelText("Состояние каскада")).toHaveTextContent(/^Каскад$/);
  });

  it("имя основного — словом перед числами, в подсказках и метках", async () => {
    render(<HeaderStats host={{ ...host, server_name: "Дача 2" }} />);
    const button = screen.getByRole("button", { name: "Сервер «Дача 2»" });
    const name = within(button).getByText("Дача 2");
    expect(name).toHaveAttribute("translate", "no");
    expect(name).toHaveClass("text-foreground", "truncate");
    const cpu = screen.getByLabelText("CPU сервера «Дача 2»");
    expect(cpu).toHaveTextContent("2%");
    expect(name.compareDocumentPosition(cpu) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(await tooltipOf("CPU сервера «Дача 2»")).toHaveTextContent("Загрузка CPU сервера «Дача 2»: 2%");
  });

  it("имя каскада — вместо «Каскад entry», в подсказках — в кавычках", async () => {
    render(
      <HeaderStats
        host={{ ...host, entries: [{ ...entry, title: "Москва" }, { ...entry, name: "de-1" }] }}
      />,
    );
    const word = screen.getByLabelText("Состояние каскада «Москва»");
    expect(word).toHaveTextContent(/^Москва$/);
    expect(screen.getByRole("button", { name: "Каскад «Москва»" })).toContainElement(word);
    expect(screen.getByLabelText("Состояние каскада de-1")).toHaveTextContent(/^Каскад de-1$/);
    expect(screen.getByLabelText("CPU каскада «Москва»")).toHaveTextContent("5%");
    expect(await tooltipOf("Состояние каскада «Москва»")).toHaveTextContent("Каскад «Москва» работает");
  });

  it("подсказка молчащего каскада с именем", async () => {
    render(
      <HeaderStats
        host={{ ...host, entries: [{ name: "entry", title: "Москва", state: "down", reporting: false, seen: true }] }}
      />,
    );
    expect(await tooltipOf("Состояние каскада «Москва»")).toHaveTextContent(
      "Каскад «Москва» не выходил на связь больше 5 минут. Проверьте, что сервер включён и видит панель",
    );
  });

  it("имя — текст, не разметка", () => {
    render(<HeaderStats host={{ ...host, server_name: "<i>x</i>", entries: [{ ...entry, title: "<b>y</b>" }] }} />);
    expect(screen.getByText("<i>x</i>")).toBeInTheDocument();
    expect(screen.getByText("<b>y</b>")).toBeInTheDocument();
    expect(document.querySelector("i, b")).toBeNull();
  });

  it("клик по строке основного — окно «Сервер», по строке каскада — окно «Каскад»", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<HeaderStats host={{ ...host, server_name: "Дача", entries: [{ ...entry, title: "Москва" }] }} />);
    await user.click(screen.getByRole("button", { name: "Сервер «Дача»" }));
    const server = await screen.findByRole("dialog", { name: "Сервер" });
    expect(within(server).getByText("Дача")).toBeInTheDocument();
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "Каскад «Москва»" }));
    const cascade = await screen.findByRole("dialog", { name: "Каскад" });
    expect(within(cascade).getByText("Москва")).toBeInTheDocument();
    expect(within(cascade).getByText("entry")).toBeInTheDocument();
  });
});

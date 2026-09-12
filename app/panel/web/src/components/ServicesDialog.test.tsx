import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ServicesDialog } from "@/components/ServicesDialog";
import type { ServicesInfo } from "@/lib/api";

// Окно «Состояние служб» (amnezia-vpn-server-eq82): отказ, который чинится
// сам, невидим, пока о нём негде прочитать.
let answer: ServicesInfo;

function reply(info: Partial<ServicesInfo>) {
  answer = { checked_at_utc: "", watchdog: true, services: [], ...info };
}

beforeEach(() => {
  reply({});
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify(answer), {
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("состояние служб", () => {
  it("показывает работающие службы", async () => {
    reply({
      checked_at_utc: new Date().toISOString(),
      services: [
        { name: "dns", state: "ok", reason: "", fails: 0, restarted_at_utc: "", restart_reason: "" },
        { name: "awg", state: "ok", reason: "", fails: 0, restarted_at_utc: "", restart_reason: "" },
      ],
    });
    render(<ServicesDialog open onOpenChange={() => {}} />);

    expect(await screen.findByText("Резолвер в туннеле")).toBeInTheDocument();
    expect(screen.getByText("Туннель")).toBeInTheDocument();
    expect(screen.getAllByText("Работает")).toHaveLength(2);
  });

  it("называет причину, когда служба не отвечает", async () => {
    reply({
      services: [
        {
          name: "dns",
          state: "fail",
          reason: "не отвечает на 10.8.0.1",
          fails: 1,
          restarted_at_utc: "",
          restart_reason: "",
        },
      ],
    });
    render(<ServicesDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText("не отвечает на 10.8.0.1")).toBeInTheDocument();
  });

  // Через минуту после починки всё исправно. Без следа владелец так и не
  // узнает, что сервер сам себя чинил, — а это и есть то, ради чего окно.
  it("оставляет след перезапуска после починки", async () => {
    reply({
      services: [
        {
          name: "dns",
          state: "ok",
          reason: "",
          fails: 0,
          restarted_at_utc: "2026-09-07T11:40:00Z",
          restart_reason: "не отвечает на 10.8.0.1",
        },
      ],
    });
    render(<ServicesDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/Перезапускался/)).toBeInTheDocument();
    expect(screen.getByText(/не отвечает на 10.8.0.1/)).toBeInTheDocument();
  });

  // Сервер без сторожа не сломан — за ним просто никто не следит.
  it("отличает «сторожа нет» от поломки", async () => {
    reply({ watchdog: false });
    render(<ServicesDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/Сторож не установлен/)).toBeInTheDocument();
  });

  // Первая минута после установки: проверять ещё не успели.
  it("отличает «ещё не проверяли» от поломки", async () => {
    reply({ watchdog: true, services: [] });
    render(<ServicesDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/ещё не проверял/)).toBeInTheDocument();
  });

  // Раньше строка стояла последней и читалась как примечание к последней
  // службе в списке, хотя относится ко всему снимку (amnezia-vpn-server-4yo4).
  it("ставит время проверки сразу под заголовком, а не последней строкой", async () => {
    reply({
      checked_at_utc: new Date().toISOString(),
      services: [
        { name: "dns", state: "ok", reason: "", fails: 0, restarted_at_utc: "", restart_reason: "" },
        { name: "awg", state: "ok", reason: "", fails: 0, restarted_at_utc: "", restart_reason: "" },
      ],
    });
    render(<ServicesDialog open onOpenChange={() => {}} />);

    const checked = await screen.findByText(/Проверено/);
    const lastService = screen.getByText("Туннель");
    // «Проверено …» должно предшествовать службам в разметке — иначе оно
    // либо читается как хвост последней службы, либо стоит после неё в DOM.
    expect(
      checked.compareDocumentPosition(lastService) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

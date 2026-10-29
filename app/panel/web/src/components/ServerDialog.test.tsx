import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ServerDialog } from "@/components/ServerDialog";
import { Toaster } from "@/components/ui/sonner";

// Окно сервера по клику на строку шапки (amnezia-vpn-server-q1do.4): имя,
// «Изменить» и то же окно правки, что у клиента.

let answer: { ok: boolean; message?: string } = { ok: true };
const sent: { url: string; method: string; body: unknown }[] = [];

beforeEach(() => {
  sent.length = 0;
  answer = { ok: true };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method && init.method !== "GET") {
        sent.push({ url: String(input), method: init.method, body: JSON.parse(String(init.body)) });
      }
      return new Response(JSON.stringify(answer), { headers: { "Content-Type": "application/json" } });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function openEdit() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Изменить имя" }));
  const edit = await screen.findByRole("dialog", { name: "Имя" });
  return { user, edit };
}

describe("окно сервера", () => {
  it("основной без имени — «Не задано», имя сохраняется и попадает в шапку", async () => {
    const onSaved = vi.fn();
    render(
      <>
        <ServerDialog target={{ kind: "server", name: "" }} onOpenChange={() => {}} onSaved={onSaved} />
        <Toaster />
      </>,
    );
    const dialog = screen.getByRole("dialog", { name: "Сервер" });
    expect(within(dialog).getByText("Имя")).toBeInTheDocument();
    expect(within(dialog).getByText("Не задано")).toHaveClass("text-muted-foreground");
    expect(within(dialog).queryByText("Узел")).not.toBeInTheDocument();

    const { user, edit } = await openEdit();
    const input = within(edit).getByLabelText("Имя");
    expect(input).toHaveValue("");
    expect(input).toHaveAttribute("maxLength", "32");
    expect(within(edit).getByText("До 32 символов. Пустое — без имени")).toBeInTheDocument();
    await user.type(input, "Москва");
    await user.click(within(edit).getByRole("button", { name: "Сохранить имя" }));

    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toEqual({ url: "/api/server/name", method: "PUT", body: { name: "Москва" } });
    expect(await screen.findByText("Имя сохранено")).toBeInTheDocument();
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Имя" })).not.toBeInTheDocument());
  });

  it("каскад — узел, подсказка с подписью по умолчанию, пустое имя сбрасывает", async () => {
    render(
      <>
        <ServerDialog
          target={{ kind: "entry", entry: { name: "entry", title: "Москва" }, count: 2 }}
          onOpenChange={() => {}}
          onSaved={() => {}}
        />
        <Toaster />
      </>,
    );
    const dialog = screen.getByRole("dialog", { name: "Каскад" });
    expect(within(dialog).getByText("Москва")).toBeInTheDocument();
    expect(within(dialog).getByText("Узел")).toBeInTheDocument();
    expect(within(dialog).getByText("entry")).toBeInTheDocument();

    const { user, edit } = await openEdit();
    expect(within(edit).getByText("До 32 символов. Пустое — «Каскад entry»")).toBeInTheDocument();
    const input = within(edit).getByLabelText("Имя");
    expect(input).toHaveValue("Москва");
    await user.clear(input);
    await user.click(within(edit).getByRole("button", { name: "Сохранить имя" }));

    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toEqual({ url: "/api/entries/entry/title", method: "PUT", body: { title: "" } });
    expect(await screen.findByText("Имя сброшено")).toBeInTheDocument();
  });

  it("один каскад — подсказка «Каскад»", async () => {
    render(
      <ServerDialog target={{ kind: "entry", entry: { name: "entry" }, count: 1 }} onOpenChange={() => {}} onSaved={() => {}} />,
    );
    const { edit } = await openEdit();
    expect(within(edit).getByText("До 32 символов. Пустое — «Каскад»")).toBeInTheDocument();
  });

  it("отказ сервера — его текст, окно правки остаётся", async () => {
    answer = { ok: false, message: "Такое имя уже у другого каскада" };
    const onSaved = vi.fn();
    render(
      <>
        <ServerDialog target={{ kind: "entry", entry: { name: "de-1" }, count: 2 }} onOpenChange={() => {}} onSaved={onSaved} />
        <Toaster />
      </>,
    );
    const { user, edit } = await openEdit();
    await user.type(within(edit).getByLabelText("Имя"), "москва");
    await user.click(within(edit).getByRole("button", { name: "Сохранить имя" }));
    expect(await screen.findByText("Такое имя уже у другого каскада")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Имя" })).toBeInTheDocument();
    expect(onSaved).not.toHaveBeenCalled();
  });

  it("то же имя — без запроса", async () => {
    render(
      <ServerDialog target={{ kind: "server", name: "Москва" }} onOpenChange={() => {}} onSaved={() => {}} />,
    );
    const { user, edit } = await openEdit();
    await user.click(within(edit).getByRole("button", { name: "Сохранить имя" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Имя" })).not.toBeInTheDocument());
    expect(sent).toHaveLength(0);
  });

  it("имя выводится текстом, а не разметкой", () => {
    render(
      <ServerDialog target={{ kind: "server", name: "<b>Дом</b>" }} onOpenChange={() => {}} onSaved={() => {}} />,
    );
    const dialog = screen.getByRole("dialog", { name: "Сервер" });
    expect(within(dialog).getByText("<b>Дом</b>")).toHaveAttribute("translate", "no");
    expect(dialog.querySelector("b")).toBeNull();
  });
});

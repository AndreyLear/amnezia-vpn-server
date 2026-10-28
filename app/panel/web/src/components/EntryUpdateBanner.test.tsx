import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { EntryUpdateBanner, entryUpdateReason } from "@/components/EntryUpdateBanner";
import { Toaster } from "@/components/ui/sonner";
import type { EntryHost, EntryUpdate } from "@/lib/api";

// Обновление каскада (amnezia-vpn-server-q1do.2): полоса рядом с полосой о
// выпуске сервера, кнопка «Обновить каскад» и окно с ходом и итогом.
function update(overrides: Partial<EntryUpdate> = {}): EntryUpdate {
  return {
    supported: true,
    state: "idle",
    target: "",
    reason: "",
    at_utc: "",
    release: "3.3.0",
    panel: "3.4.0",
    available: true,
    requested: false,
    ...overrides,
  };
}

function entry(u: EntryUpdate | undefined, name = "entry"): EntryHost {
  return { name, state: "up", reporting: true, seen: true, update: u };
}

let answer: { ok: boolean; message?: string } = { ok: true };
const posted: string[] = [];

beforeEach(() => {
  posted.length = 0;
  answer = { ok: true };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") posted.push(String(input));
      return new Response(JSON.stringify(answer), { headers: { "Content-Type": "application/json" } });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("полоса обновления каскада", () => {
  it("каскад отстаёт от панели — версии и кнопка «Обновить каскад»", () => {
    render(<EntryUpdateBanner entries={[entry(update())]} onChanged={() => {}} />);
    expect(screen.getByText("Каскад entry на версии 3.3.0, панель — на 3.4.0")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Обновить каскад" })).toBeInTheDocument();
  });

  it("каскад на версии панели — полосы нет", () => {
    render(<EntryUpdateBanner entries={[entry(update({ available: false, release: "3.4.0" }))]} onChanged={() => {}} />);
    expect(screen.queryByTestId("entry-update-entry")).not.toBeInTheDocument();
  });

  it("старая панель без update — полосы нет", () => {
    render(<EntryUpdateBanner entries={[entry(undefined)]} onChanged={() => {}} />);
    expect(screen.queryByTestId("entry-update-entry")).not.toBeInTheDocument();
  });

  it("старый каскад, который сам не обновляется, — подсказка без кнопки", () => {
    render(<EntryUpdateBanner entries={[entry(update({ supported: false, state: "" }))]} onChanged={() => {}} />);
    expect(
      screen.getByText(
        "Каскад entry на версии 3.3.0, панель — на 3.4.0. Обновите его один раз вручную по ssh — дальше он будет обновляться сам",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Обновить каскад" })).not.toBeInTheDocument();
  });

  it("запрос ждёт каскада — без кнопки", () => {
    render(<EntryUpdateBanner entries={[entry(update({ requested: true }))]} onChanged={() => {}} />);
    expect(screen.getByText("Каскад entry начнёт обновление в течение минуты")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Обновить каскад" })).not.toBeInTheDocument();
  });

  it("идёт обновление — без кнопки", () => {
    render(
      <EntryUpdateBanner entries={[entry(update({ state: "updating", target: "3.4.0" }))]} onChanged={() => {}} />,
    );
    expect(screen.getByTestId("entry-update-entry")).toHaveTextContent("Каскад entry обновляется до 3.4.0");
    expect(screen.queryByRole("button", { name: "Обновить каскад" })).not.toBeInTheDocument();
  });

  it("неудача — понятная причина и кнопка повтора", () => {
    render(
      <EntryUpdateBanner
        entries={[entry(update({ state: "failed", reason: "install", target: "3.4.0" }))]}
        onChanged={() => {}}
      />,
    );
    expect(
      screen.getByText("Каскад entry не обновился: установка не удалась, каскад вернулся на прежнюю версию"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Обновить каскад" })).toBeInTheDocument();
  });

  it("причина, которой панель не знает, — без текста каскада", () => {
    expect(entryUpdateReason("Позвоните нам")).toBe("причина неизвестна");
    expect(entryUpdateReason("checksum")).toBe("контрольная сумма выпуска не совпала");
  });
});

describe("окно обновления каскада", () => {
  it("подтверждение, запрос, ход и удачный итог", async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn();
    const { rerender } = render(<EntryUpdateBanner entries={[entry(update())]} onChanged={onChanged} />);
    await user.click(screen.getByRole("button", { name: "Обновить каскад" }));
    expect(screen.getByRole("heading", { name: "Обновить каскад entry" })).toBeInTheDocument();
    expect(screen.getByText(/Каскад скачает выпуск 3\.4\.0 с GitHub/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Обновить" }));
    await waitFor(() => expect(posted).toEqual(["/api/entries/entry/update"]));
    expect(onChanged).toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Обновляем каскад entry…" })).toBeInTheDocument();

    // Опрос принёс: запрос ждёт, потом идёт, потом каскад догнал панель.
    rerender(<EntryUpdateBanner entries={[entry(update({ requested: true }))]} onChanged={onChanged} />);
    expect(screen.getByRole("heading", { name: "Обновляем каскад entry…" })).toBeInTheDocument();
    rerender(<EntryUpdateBanner entries={[entry(update({ state: "updating", target: "3.4.0" }))]} onChanged={onChanged} />);
    rerender(
      <EntryUpdateBanner
        entries={[entry(update({ state: "idle", release: "3.4.0", available: false }))]}
        onChanged={onChanged}
      />,
    );
    expect(screen.getByRole("heading", { name: "Каскад обновлён" })).toBeInTheDocument();
    expect(screen.getByText("Каскад entry обновлён до версии 3.4.0")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Понятно" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("прежняя неудача не выдаётся за итог нового запроса; новая — показывается", async () => {
    const user = userEvent.setup();
    const failed = update({ state: "failed", reason: "install", target: "3.4.0" });
    const { rerender } = render(<EntryUpdateBanner entries={[entry(failed)]} onChanged={() => {}} />);
    await user.click(screen.getByRole("button", { name: "Обновить каскад" }));
    await user.click(screen.getByRole("button", { name: "Обновить" }));
    // Опрос ещё не пришёл: состояние прежнее — окно ждёт, а не пугает.
    await waitFor(() => expect(screen.getByRole("heading", { name: "Обновляем каскад entry…" })).toBeInTheDocument());

    rerender(<EntryUpdateBanner entries={[entry(update({ state: "updating", target: "3.4.0" }))]} onChanged={() => {}} />);
    rerender(
      <EntryUpdateBanner
        entries={[entry(update({ state: "failed", reason: "rollback", target: "3.4.0" }))]}
        onChanged={() => {}}
      />,
    );
    expect(screen.getByRole("heading", { name: "Каскад entry не обновился" })).toBeInTheDocument();
    expect(
      screen.getByText("Установка не удалась, прежняя версия не вернулась — нужно зайти на каскад по ssh"),
    ).toBeInTheDocument();
  });

  it("отказ сервера — тостом, окно остаётся предложением", async () => {
    const user = userEvent.setup();
    answer = { ok: false, message: "Обновление каскада уже идёт" };
    render(
      <>
        <Toaster />
        <EntryUpdateBanner entries={[entry(update())]} onChanged={() => {}} />
      </>,
    );
    await user.click(screen.getByRole("button", { name: "Обновить каскад" }));
    await user.click(screen.getByRole("button", { name: "Обновить" }));
    expect(await screen.findByText("Обновление каскада уже идёт")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Обновить каскад entry" })).toBeInTheDocument();
  });
});

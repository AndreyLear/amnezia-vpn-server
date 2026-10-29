import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AboutDialog } from "@/components/AboutDialog";
import type { Versions } from "@/lib/api";

// Окно «О версиях» (amnezia-vpn-server-8bt5, -4yo4): всё, что определяет
// поведение сервера, в одном месте, без выдумывания неизвестных значений.
// Схема базы больше не входит в тип Versions на клиенте, но бэкенд её всё
// равно отдаёт (номер доступен через CLI) — тест воспроизводит именно это.
let answer: Partial<Versions> & { schema?: string };

function reply(versions: Partial<Versions> & { schema?: string }) {
  answer = versions;
}

// Значение строки «О версиях» по её подписи (пара dt/dd).
function rowValue(label: string): string | null | undefined {
  return screen.getByText(label, { selector: "dt" }).nextElementSibling?.textContent;
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

describe("о версиях", () => {
  // Приписка сначала была «свежая», потом «последняя», и оба раза владелец
  // читал её как вторую, незнакомую версию. Когда новой версии нет, сказать
  // нечего — номер стоит один (amnezia-vpn-server-cavu).
  it("показывает установленную версию без приписок, когда новее ничего нет", async () => {
    reply({ product: "2.10.12", latest: "2.10.12" });
    render(<AboutDialog open onOpenChange={() => {}} />);

    expect(await screen.findByText("2.10.12")).toBeInTheDocument();
    expect(screen.queryByText(/последняя|свежая/)).not.toBeInTheDocument();
  });

  it("показывает вышедшую версию отдельно от установленной", async () => {
    reply({ product: "2.10.11", latest: "2.10.12" });
    render(<AboutDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText("2.10.11 — вышла 2.10.12")).toBeInTheDocument();
  });

  // Номер схемы SQLite ничего не говорит оператору, а сбой миграции панель
  // показывает отдельно — строка убрана целиком.
  it("не показывает схему базы", async () => {
    reply({ product: "2.10.12", schema: "8" });
    render(<AboutDialog open onOpenChange={() => {}} />);

    await screen.findByText("Версия");
    expect(screen.queryByText("Схема базы")).not.toBeInTheDocument();
    expect(screen.queryByText("8")).not.toBeInTheDocument();
  });

  // Туннель на модуле ядра — показываем модуль, а не amneziawg-go из образа,
  // который не используется (amnezia-vpn-server-y7bp).
  it("показывает модуль ядра, когда туннель работает на нём", async () => {
    reply({ product: "2.10.37", amneziawg_go: "3.1.20260828", tunnel_driver: "kernel", amneziawg_module: "3.1.20260812" });
    render(<AboutDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText("Модуль ядра")).toBeInTheDocument();
    expect(screen.getByText("3.1.20260812")).toBeInTheDocument();
    expect(screen.queryByText("amneziawg-go")).not.toBeInTheDocument();
  });

  // После AmneziaWG — Xray: версия ядра, включён ли VLESS и когда каждый
  // узел-вход последний раз забирал конфиг (amnezia-vpn-server-14y0.16).
  // «Нет связи» решает сервер по своим часам (online), а не браузер.
  it("показывает Xray, VLESS и время синка каждого входа", async () => {
    const synced = new Date(Date.now() - 90_000).toISOString();
    reply({
      product: "3.0.1",
      xray: "26.10.21",
      vless_state: "up",
      entries: [
        { name: "entry", last_sync_utc: synced, online: true },
        { name: "ru", last_sync_utc: "2026-10-01T00:00:00Z", online: false },
        { name: "fi", last_sync_utc: null, online: false },
      ],
    });
    render(<AboutDialog open onOpenChange={() => {}} />);

    expect((await screen.findAllByText("Xray"))[0]).toBeInTheDocument();
    expect(screen.getByText("26.10.21")).toBeInTheDocument();
    expect(rowValue("VLESS")).toBe("Включён");
    expect(rowValue("Каскад entry")).toBe("1 мин назад");
    expect(rowValue("Каскад ru")).toBe("Нет связи");
    expect(rowValue("Каскад fi")).toBe("Нет связи");
  });

  // Под строкой узла — его версия выпуска, Xray и время без перезагрузки из
  // отчёта узла (amnezia-vpn-server-q1do.1). Неизвестное — «Неизвестно»:
  // старый узел отчётов не шлёт.
  it("показывает версию, Xray и аптайм каждого входа под его строкой", async () => {
    const synced = new Date(Date.now() - 90_000).toISOString();
    reply({
      product: "3.1.0",
      xray: "26.10.21",
      vless_state: "up",
      entries: [
        { name: "entry", last_sync_utc: synced, online: true, xray: "26.11.2", release: "3.2.0", uptime_seconds: 3 * 86400 + 5 },
        { name: "old", last_sync_utc: synced, online: true },
      ],
    });
    render(<AboutDialog open onOpenChange={() => {}} />);
    await screen.findByText("3.1.0");

    const values = (label: string) =>
      screen.getAllByText(label, { selector: "dt" }).map((dt) => dt.nextElementSibling?.textContent);
    // Порядок: строка основного сервера, потом узлы по порядку.
    expect(values("Версия")).toEqual(["3.1.0", "3.2.0", "Неизвестно"]);
    expect(values("Xray")).toEqual(["26.10.21", "26.11.2", "Неизвестно"]);
    expect(values("Без перезагрузки")).toEqual(["3 дн", "Неизвестно"]);

    // Строки узла идут сразу под его строкой и отличаются отступом.
    const entryRow = screen.getByText("Каскад entry", { selector: "dt" }).parentElement as HTMLElement;
    const sub = entryRow.nextElementSibling as HTMLElement;
    expect(sub.querySelector("dt")).toHaveTextContent("Версия");
    expect(sub.querySelector("dt")).toHaveClass("ps-4");
  });

  // Самообновление каскада (amnezia-vpn-server-q1do.2): строка под узлом —
  // только когда есть что сказать; при обычном состоянии её нет.
  it("показывает обновление каскада, только когда оно идёт, запрошено или не удалось", async () => {
    const base = {
      supported: true, target: "3.4.0", reason: "", at_utc: "", release: "3.3.0", panel: "3.4.0",
      available: true, requested: false,
    };
    reply({
      product: "3.4.0",
      xray: "26.10.21",
      vless_state: "up",
      entries: [
        { name: "a", last_sync_utc: null, online: false, update: { ...base, state: "updating" } },
        { name: "b", last_sync_utc: null, online: false, update: { ...base, state: "idle", requested: true } },
        { name: "c", last_sync_utc: null, online: false, update: { ...base, state: "failed", reason: "install" } },
        { name: "d", last_sync_utc: null, online: false, update: { ...base, state: "idle", available: false } },
      ],
    });
    render(<AboutDialog open onOpenChange={() => {}} />);
    await screen.findByText("Каскад d");
    const values = screen
      .getAllByText("Обновление", { selector: "dt" })
      .map((dt) => dt.nextElementSibling?.textContent);
    expect(values).toEqual(["Обновляется до 3.4.0", "Запрошено", "Не удалось"]);
  });

  // Сторож каскада (amnezia-vpn-server-q1do.5): последний перезапуск Xray —
  // строкой под узлом, только если он был. Причины в строке нет: длинная
  // сломала бы список.
  it("показывает последний перезапуск Xray сторожем каскада, только если он был", async () => {
    const restarted = new Date(Date.now() - 2 * 3600_000 - 60_000).toISOString();
    reply({
      product: "3.5.0",
      xray: "26.10.21",
      vless_state: "up",
      entries: [
        { name: "a", last_sync_utc: null, online: false, watchdog: { restarted_at_utc: restarted, reason: "api" } },
        { name: "b", last_sync_utc: null, online: false, watchdog: null },
        { name: "c", last_sync_utc: null, online: false },
      ],
    });
    render(<AboutDialog open onOpenChange={() => {}} />);
    await screen.findByText("Каскад c");
    const rows = screen.getAllByText("Сторож Xray", { selector: "dt" });
    expect(rows.map((dt) => dt.nextElementSibling?.textContent)).toEqual(["Перезапуск 2 ч назад"]);
    expect(rows[0]).toHaveClass("ps-4");
  });

  // Опрос Xray не ответил — VLESS всё равно включён: строка говорит о
  // настройке, а не о том, ответил ли Xray в последние пять секунд.
  it("считает VLESS включённым при любом состоянии, кроме none", async () => {
    for (const state of ["down", "na"]) {
      reply({ product: "3.0.1", xray: "26.10.21", vless_state: state, entries: [] });
      const { unmount } = render(<AboutDialog open onOpenChange={() => {}} />);
      await screen.findByText("Xray");
      expect(rowValue("VLESS")).toBe("Включён");
      unmount();
    }
  });

  // Входы есть, а VLESS выключен — строки остаются, и выключенное названо
  // выключенным.
  it("не прячет Xray, когда VLESS выключен, но входы есть", async () => {
    reply({ product: "3.0.1", xray: "26.10.21", vless_state: "none", entries: [{ name: "entry", last_sync_utc: null, online: false }] });
    render(<AboutDialog open onOpenChange={() => {}} />);
    expect((await screen.findAllByText("Xray"))[0]).toBeInTheDocument();
    expect(rowValue("VLESS")).toBe("Выключен");
    expect(rowValue("Каскад entry")).toBe("Нет связи");
  });

  // Установка только с AWG выглядит как раньше.
  it("прячет Xray, когда VLESS выключен и входов нет", async () => {
    reply({ product: "3.0.1", xray: "26.10.21", vless_state: "none", entries: [] });
    render(<AboutDialog open onOpenChange={() => {}} />);
    await screen.findByText("3.0.1");
    expect(screen.queryByText("Xray")).not.toBeInTheDocument();
    expect(screen.queryByText("VLESS")).not.toBeInTheDocument();
    expect(screen.queryByText(/^Каскад/)).not.toBeInTheDocument();
  });

  it("показывает amneziawg-go, когда туннель на нём или неизвестно на чём", async () => {
    for (const driver of ["userspace", undefined]) {
      reply({ product: "2.10.37", amneziawg_go: "3.1.20260828", tunnel_driver: driver });
      const { unmount } = render(<AboutDialog open onOpenChange={() => {}} />);
      expect(await screen.findByText("amneziawg-go")).toBeInTheDocument();
      expect(screen.getByText("3.1.20260828")).toBeInTheDocument();
      expect(screen.queryByText("Модуль ядра")).not.toBeInTheDocument();
      unmount();
    }
  });
});

// Имена серверов (amnezia-vpn-server-q1do.4): имя основного — строкой
// «Имя» вверху, только когда задано; каскад с именем — «Каскад «Москва»».
describe("о версиях — имена", () => {
  it("без имени строки «Имя» нет", async () => {
    reply({ product: "3.3.0", server_name: "" });
    render(<AboutDialog open onOpenChange={() => {}} />);
    await screen.findByText("3.3.0");
    expect(screen.queryByText("Имя", { selector: "dt" })).not.toBeInTheDocument();
  });

  it("имя основного — первой строкой, текстом", async () => {
    reply({ product: "3.3.0", server_name: "<b>Дача</b>" });
    render(<AboutDialog open onOpenChange={() => {}} />);
    const label = await screen.findByText("Имя", { selector: "dt" });
    expect(rowValue("Имя")).toBe("<b>Дача</b>");
    expect(label.closest("dl")?.firstElementChild).toContainElement(label);
    expect(document.querySelector("dd b")).toBeNull();
  });

  it("каскад с именем — «Каскад «Москва»», без имени — как раньше", async () => {
    reply({
      vless_state: "up",
      entries: [
        { name: "entry", title: "Москва", last_sync_utc: null, online: false },
        { name: "de-1", last_sync_utc: null, online: false },
      ],
    });
    render(<AboutDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText("Каскад de-1", { selector: "dt" })).toBeInTheDocument();
    const named = screen.getByText((_, el) => el?.tagName === "DT" && el.textContent === "Каскад «Москва»");
    expect(named.nextElementSibling?.textContent).toBe("Нет связи");
  });
});

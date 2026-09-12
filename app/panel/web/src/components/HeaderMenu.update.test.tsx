import { act, fireEvent, render, screen } from "@testing-library/react";
import { toast } from "sonner";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { HeaderMenu } from "@/components/HeaderMenu";
import { setCsrf, type UpdateInfo } from "@/lib/api";

// «Проверить обновления» глазами человека (amnezia-vpn-server-n8w3).
//
// Панель наружу не ходит: нажатие кладёт файл-просьбу в её том, а GitHub
// спрашивает хостовая служба и пишет ответ в update-check.json. Значит
// «проверка закончилась» — это не ответ на POST, а НОВОЕ checked_at_utc в
// /api/update. Отсюда и устройство здешней подделки хоста: ответ на POST
// приходит сразу, а свежая отметка — только через несколько опросов.
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

// Те же числа, что в компоненте, но записанные здесь заново: проверка не
// должна проходить только потому, что взяла шаг опроса у того, кого проверяет.
const POLL_MS = 1000;
const LIMIT_MS = 45000;

const PAST_CHECK = "2026-09-08T10:00:00Z";
const FRESH_CHECK = "2026-09-09T12:00:00Z";

// Прошлая проверка в файле — неудачная. Так видно, что панель дождалась новой,
// а не пересказала лежавшую: пересказ прошлой звучал бы иначе.
const base: UpdateInfo = {
  installed: "2.9.0",
  latest: "",
  available: false,
  notes: "",
  checked_at_utc: PAST_CHECK,
  check_result: "failed",
  check_reason: "unreachable",
  state: "",
  state_from: "",
  state_to: "",
  state_step: "",
  state_message: "",
  state_at_utc: "",
  dismissed: "",
  outcome_seen: "",
};

/** Что хост запишет в update-check.json и после какого по счёту опроса. */
type HostAnswer = { after: number; info: Partial<UpdateInfo> };

let current: UpdateInfo;
let answer: HostAnswer | null;
let polls: number;
let posts: number;

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  // Поддельные часы ставятся ДО отрисовки: включённые позже, они не управляют
  // уже заведённым ожиданием, и проверка прошла бы при любой реализации.
  vi.useFakeTimers();
  vi.setSystemTime(new Date(FRESH_CHECK));
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  // Без токена запрос ждёт его полсекунды на настоящих часах — здесь часы
  // поддельные, и ожидание было бы вечным.
  setCsrf("csrf-token");
  current = { ...base };
  answer = null;
  polls = 0;
  posts = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/update/check") {
        posts += 1;
        return json({ ok: true });
      }
      if (url === "/api/update" && (init?.method ?? "GET") === "GET") {
        polls += 1;
        // Первый опрос — это снимок «до»: он задан ещё до просьбы.
        if (answer && posts > 0 && polls > answer.after) {
          current = { ...current, ...answer.info };
          answer = null;
        }
        return json(current);
      }
      return json({ ok: true });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

/**
 * Меню открывается и пункт нажимается через fireEvent, а не user-event: тот
 * оборачивает каждое действие в ожидание, которое на поддельных часах некому
 * сдвинуть, и проверка зависает целиком. Поддельные часы здесь важнее удобства
 * — без них ожидание проверки идёт настоящие 45 секунд.
 */
function pressCheck() {
  fireEvent.pointerDown(screen.getByRole("button", { name: "Ещё" }), {
    button: 0,
    ctrlKey: false,
    pointerType: "mouse",
  });
  const item = screen.getByRole("menuitem", { name: /Проверить обновления/ });
  fireEvent.click(item);
  return item;
}

async function waitMs(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("проверка обновлений из меню", () => {
  it("дожидается свежей отметки и называет вышедшую версию", async () => {
    answer = {
      after: 2,
      info: {
        checked_at_utc: FRESH_CHECK,
        check_result: "ok",
        check_reason: "",
        latest: "2.11.0",
        available: true,
      },
    };
    const onChecked = vi.fn();
    render(<HeaderMenu onChecked={onChecked} />);
    pressCheck();

    await waitMs(POLL_MS * 3);
    expect(toast.success).toHaveBeenCalledWith("Вышла версия 2.11.0");
    // Полосе с предложением обновиться пора перечитать своё: иначе о выпуске
    // скажет только всплывающее сообщение, а оно уходит.
    expect(onChecked).toHaveBeenCalled();
  });

  // Тот самый молчаливый исход: выпуск не новее установленного не менял на
  // экране ничего, и нажавший не знал, сработала ли кнопка.
  it("говорит словами, что установлена последняя версия", async () => {
    answer = {
      after: 2,
      info: {
        checked_at_utc: FRESH_CHECK,
        check_result: "ok",
        check_reason: "",
        latest: "",
        available: false,
      },
    };
    render(<HeaderMenu />);
    pressCheck();

    await waitMs(POLL_MS * 3);
    // Заголовок и текст раздельно: одна слипшаяся строка читалась хуже
    // (amnezia-vpn-server-3tm4).
    expect(toast.success).toHaveBeenCalledWith("Обновлений нет", {
      description: "Установлена последняя версия",
    });
    expect(toast.error).not.toHaveBeenCalled();
  });

  // Неудачу нельзя сваливать в «всё хорошо»: причину хост называет, и разные
  // причины советуют человеку разное.
  it("называет причину, по которой проверить не удалось", async () => {
    answer = {
      after: 1,
      info: { checked_at_utc: FRESH_CHECK, check_result: "failed", check_reason: "no-release" },
    };
    render(<HeaderMenu />);
    pressCheck();

    await waitMs(POLL_MS * 2);
    expect(toast.error).toHaveBeenCalledWith("GitHub ответил, но выпуска в ответе нет");
    expect(toast.success).not.toHaveBeenCalledWith("Обновлений нет: установлена последняя версия");
  });

  it("пока ответа нет, держит пункт занятым и не даёт нажать снова", async () => {
    // Хост молчит: отметка в файле остаётся прошлой, ответа ещё нет.
    render(<HeaderMenu />);
    const item = pressCheck();

    await waitMs(POLL_MS * 3);
    expect(item).toHaveAttribute("aria-busy", "true");
    // Вертушки рядом с подписью нет намеренно: она раздвигала меню на два
    // десятка пикселей ровно в тот момент, когда по нему целятся. Что работа
    // идёт, говорит сама подпись (amnezia-vpn-server-x65u).
    expect(screen.queryByTestId("check-spinner")).toBeNull();
    // И второе нажатие занятого пункта не просит проверку заново.
    fireEvent.click(item);
    await waitMs(0);
    expect(posts).toBe(1);
  });

  it("по потолку ожидания честно говорит, что ответа нет", async () => {
    render(<HeaderMenu />);
    const item = pressCheck();

    await waitMs(LIMIT_MS + POLL_MS);
    expect(toast.error).toHaveBeenCalledWith("Проверка не ответила. Попробуйте позже");
    // Прошлую проверку за ответ не выдаём: она в файле лежит и выглядит как
    // настоящая.
    expect(toast.error).not.toHaveBeenCalledWith("Не удалось связаться с GitHub");
    // И вертушка гаснет: молчание — это конец ожидания, а не его продолжение.
    expect(item).not.toHaveAttribute("aria-busy", "true");
    expect(screen.queryByTestId("check-spinner")).not.toBeInTheDocument();
  });
});

// Пункт меню говорит, что делает и что нашёл (amnezia-vpn-server-3tm4,
// -nzb3). Раньше он молчал в обоих случаях: во время проверки выглядел
// нажатым, а бадж висел на кнопке меню и ни к чему не относился.
describe("пункт проверки называет своё состояние", () => {
  function openMenu() {
    fireEvent.pointerDown(screen.getByRole("button", { name: "Ещё" }), {
      button: 0,
      ctrlKey: false,
      pointerType: "mouse",
    });
  }

  it("во время проверки называется «Проверяю», а не «Проверить обновления»", async () => {
    answer = { after: 3, info: { checked_at_utc: FRESH_CHECK, check_result: "ok", latest: "" } };
    render(<HeaderMenu />);
    pressCheck();

    // Один опрос: проверка ещё идёт.
    await waitMs(POLL_MS);
    expect(screen.getByRole("menuitem", { name: /Проверяю/ })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /Проверить обновления/ })).toBeNull();

    await waitMs(POLL_MS * 4);
  });

  it("ширина пункта не гуляет между состояниями", async () => {
    answer = { after: 3, info: { checked_at_utc: FRESH_CHECK, check_result: "ok", latest: "" } };
    render(<HeaderMenu />);
    pressCheck();
    await waitMs(POLL_MS);

    // Ширину держат сами варианты, отрисованные друг поверх друга: ячейка
    // всегда шириной с самый широкий из них. Здесь проверяется только
    // устройство — настоящую ширину в jsdom не измерить, вёрстки нет вовсе,
    // и первый заход именно на этом и обжёгся: тест был зелёный, а меню у
    // владельца прыгало. Измерение живьём — в e2e (amnezia-vpn-server-x65u).
    const cell = document.querySelector(".header-menu-check");
    expect(cell).not.toBeNull();
    const variants = [...cell!.children];
    expect(variants).toHaveLength(3);

    // Виден ровно один, остальные лежат под ним и скрыты от чтения с экрана.
    const shown = variants.filter((v) => !v.className.includes("invisible"));
    expect(shown).toHaveLength(1);
    expect(shown[0].textContent).toContain("Проверяю");
    for (const v of variants) {
      if (v === shown[0]) continue;
      expect(v).toHaveAttribute("aria-hidden", "true");
    }

    // В доступное имя пункта попадает только текущая подпись.
    expect(screen.getByRole("menuitem", { name: "Проверяю" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Проверить обновления" })).toBeNull();

    await waitMs(POLL_MS * 4);
  });

  it("без доступного обновления пункт зовёт проверить, и бадж не виден", () => {
    render(<HeaderMenu />);
    openMenu();
    expect(screen.getByRole("menuitem", { name: "Проверить обновления" })).toBeInTheDocument();
    // Бадж есть в разметке — он лежит в скрытом варианте подписи, который
    // держит ширину, — но помечен скрытым и не читается.
    const badge = screen.getByTestId("update-item-badge");
    expect(badge.closest("[aria-hidden=true]")).not.toBeNull();
    expect(badge.closest(".invisible")).not.toBeNull();
  });
});

// Пункт, который называется «Доступна новая версия», обязан её показать, а
// не идти спрашивать GitHub заново про то, что уже известно
// (amnezia-vpn-server-919e).
describe("пункт с доступной версией показывает её, а не проверяет", () => {
  it("зовёт показать обновление и не шлёт запрос на проверку", async () => {
    const onShowUpdate = vi.fn();
    render(<HeaderMenu pendingUpdate onShowUpdate={onShowUpdate} />);

    fireEvent.pointerDown(screen.getByRole("button", { name: "Ещё" }), {
      button: 0,
      ctrlKey: false,
      pointerType: "mouse",
    });
    fireEvent.click(screen.getByRole("menuitem", { name: "Доступна новая версия" }));
    await waitMs(0);

    expect(onShowUpdate).toHaveBeenCalledTimes(1);
    expect(posts).toBe(0);
  });

  it("без доступного обновления по-прежнему просит проверить", async () => {
    const onShowUpdate = vi.fn();
    render(<HeaderMenu onShowUpdate={onShowUpdate} />);
    pressCheck();
    await waitMs(0);

    expect(onShowUpdate).not.toHaveBeenCalled();
    expect(posts).toBe(1);
  });
});

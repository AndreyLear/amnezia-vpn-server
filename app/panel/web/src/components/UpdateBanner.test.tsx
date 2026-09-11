import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { UpdateBanner } from "@/components/UpdateBanner";
import { UpdateDialog } from "@/components/UpdateDialog";
import type { UpdateInfo } from "@/lib/api";

// Полоса о новом выпуске (amnezia-vpn-server-tjoq).
function info(overrides: Partial<UpdateInfo> = {}): UpdateInfo {
  return {
    installed: "2.8.2",
    latest: "2.9.0",
    available: true,
    notes: "- первое\n- второе",
    checked_at_utc: "2026-09-07T08:00:00Z",
    check_result: "ok",
    check_reason: "",
    state: "",
    state_from: "",
    state_to: "",
    state_step: "",
    state_message: "",
    state_at_utc: "",
    dismissed: "",
    outcome_seen: "",
    ...overrides,
  };
}

const posted: string[] = [];

beforeEach(() => {
  posted.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") posted.push(url);
      return new Response(JSON.stringify({ ok: true }), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("полоса о новом выпуске", () => {
  it("появляется, когда есть что взять", () => {
    render(<UpdateBanner info={info()} onChanged={() => {}} />);
    expect(screen.getByText("Вышла версия 2.9.0")).toBeInTheDocument();
  });

  it("не появляется, когда брать нечего", () => {
    render(<UpdateBanner info={info({ available: false })} onChanged={() => {}} />);
    expect(screen.queryByText(/Вышла версия/)).not.toBeInTheDocument();
  });

  // Закрытие хранится на сервере, поэтому здесь оно приходит в ответе, а не
  // достаётся из браузера: другой браузер того же владельца увидит то же.
  it("не появляется, когда её уже закрыли для этой версии", () => {
    render(<UpdateBanner info={info({ dismissed: "2.9.0" })} onChanged={() => {}} />);
    expect(screen.queryByText(/Вышла версия/)).not.toBeInTheDocument();
  });

  // Закрыли для 2.9.0 — 2.10.0 обязана вернуться сама.
  it("возвращается со следующим выпуском", () => {
    render(<UpdateBanner info={info({ latest: "2.10.0", dismissed: "2.9.0" })} onChanged={() => {}} />);
    expect(screen.getByText("Вышла версия 2.10.0")).toBeInTheDocument();
  });

  it("крестик сообщает серверу, какую версию убрали", async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn();
    render(<UpdateBanner info={info()} onChanged={onChanged} />);

    await user.click(screen.getByRole("button", { name: "Скрыть до следующего выпуска" }));
    await waitFor(() => expect(posted).toContain("/api/update/dismiss"));
    expect(onChanged).toHaveBeenCalled();
  });

  it("подробности показывают, что изменилось, и предупреждают о перерыве", async () => {
    const user = userEvent.setup();
    render(<UpdateBanner info={info()} onChanged={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Показать подробности" }));
    expect(await screen.findByText("первое")).toBeInTheDocument();
    expect(screen.getByText(/клиенты остаются без связи/)).toBeInTheDocument();
  });

  it("кнопка «Обновить» просит сервер обновиться", async () => {
    const user = userEvent.setup();
    render(<UpdateBanner info={info()} onChanged={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Показать подробности" }));
    await user.click(await screen.findByRole("button", { name: "Обновить" }));
    await waitFor(() => expect(posted).toContain("/api/update/start"));
  });

  // Регрессия на сам гейт, который раньше решал, рендерить ли UpdateDialog
  // вообще: `if (!offer) return outcome;` возвращался из UpdateBanner ДО
  // <UpdateDialog>, поэтому как только available становился false — а
  // именно это происходит ровно в момент успешного обновления, когда
  // installed становится равен latest, — открытое окно хода обновления
  // пропадало из документа целиком, а не просто теряло предложение
  // обновиться. Владелец видел это как «модалка закрывается посреди
  // обновления» (amnezia-vpn-server-mrjh).
  it("окно хода обновления остаётся в документе, когда available гаснет во время показа", async () => {
    const user = userEvent.setup();
    const base = { installed: "2.9.0", latest: "2.10.0", available: true, state: "running" };
    const { rerender } = render(<UpdateBanner info={info(base)} onChanged={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Показать подробности" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    // Обновление только что удалось: installed сравнялся с latest, брать
    // больше нечего — available стал false. Это НЕ команда закрыть окно.
    rerender(
      <UpdateBanner
        info={info({ ...base, available: false, installed: "2.10.0" })}
        onChanged={() => {}}
      />,
    );

    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});

// Критерий 5 задачи: «Итог виден, даже если браузер был закрыт всё
// обновление». Обновление перезапускает саму панель, поэтому итог обязан
// найтись сам, а не ждать, пока человек куда-то нажмёт.
describe("итог обновления", () => {
  it("показывается сам после удачного обновления", async () => {
    render(
      <UpdateBanner
        info={info({
          installed: "2.9.0",
          latest: "2.9.0",
          available: false,
          state: "ok",
          state_to: "2.9.0",
          state_message: "обновление до 2.9.0 завершено",
          state_at_utc: "2026-09-08T10:00:00Z",
        })}
        onChanged={() => {}}
      />,
    );
    expect(await screen.findByText("Обновление завершено")).toBeInTheDocument();
    expect(screen.getByText("обновление до 2.9.0 завершено")).toBeInTheDocument();
  });

  it("показывается сам и когда обновление не удалось", async () => {
    render(
      <UpdateBanner
        info={info({
          state: "rolled-back",
          state_message: "не удалось; сервер работает на 2.8.2",
          state_at_utc: "2026-09-08T10:00:00Z",
        })}
        onChanged={() => {}}
      />,
    );
    expect(await screen.findByText("Обновиться не удалось")).toBeInTheDocument();
  });

  // Показали один раз — больше не показываем: сервер помнит, какой именно
  // итог человек уже видел.
  it("не возвращается после того, как его закрыли", () => {
    render(
      <UpdateBanner
        info={info({
          available: false,
          state: "ok",
          state_at_utc: "2026-09-08T10:00:00Z",
          outcome_seen: "2026-09-08T10:00:00Z",
        })}
        onChanged={() => {}}
      />,
    );
    expect(screen.queryByText("Обновление завершено")).not.toBeInTheDocument();
  });

  it("закрытие сообщает серверу, какой итог показали", async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn();
    render(
      <UpdateBanner
        info={info({ available: false, state: "ok", state_at_utc: "2026-09-08T10:00:00Z" })}
        onChanged={onChanged}
      />,
    );
    await user.click(await screen.findByRole("button", { name: "Понятно" }));
    await waitFor(() => expect(posted).toContain("/api/update/dismiss"));
    expect(onChanged).toHaveBeenCalled();
  });

  // Агент умеет отказать, и панель обязана это состояние знать.
  it("объясняет отказ, а не молчит", async () => {
    render(
      <UpdateBanner
        info={info({
          state: "refused",
          state_message: "версия 2.7.0 не новее установленной 2.9.0",
          state_at_utc: "2026-09-08T10:00:00Z",
        })}
        onChanged={() => {}}
      />,
    );
    expect(await screen.findByText(/не новее установленной/)).toBeInTheDocument();
  });

  // Окно хода обновления, пока открыто, само доводит наблюдаемое им
  // обновление до итога; отдельный попап с тем же итогом в этот момент
  // молчит — иначе один и тот же итог показался бы дважды в двух окнах
  // разом (amnezia-vpn-server-mrjh).
  it("пока окно хода обновления открыто, отдельный попап с итогом не дублируется", async () => {
    const user = userEvent.setup();
    const base = {
      installed: "2.9.0",
      latest: "2.10.0",
      available: true,
      state: "running",
    };
    const { rerender } = render(<UpdateBanner info={info(base)} onChanged={() => {}} />);

    // Открыли окно, пока обновление ещё идёт — итога ещё нет, попапу
    // конфликтовать не с чем.
    await user.click(screen.getByRole("button", { name: "Показать подробности" }));
    expect(screen.getByRole("progressbar")).toBeInTheDocument();

    // Опрос принёс итог тем же info — так это выглядит в реальном приложении:
    // useUpdateInfo меняет один и тот же проп, а не подменяет компонент.
    rerender(
      <UpdateBanner
        info={info({
          ...base,
          state: "ok",
          state_message: "обновление до 2.10.0 завершено",
          state_at_utc: "2026-09-08T10:00:00Z",
        })}
        onChanged={() => {}}
      />,
    );

    // Окно хода обновления само показывает итог, кнопка «Понятно» —
    // ровно одна, а не две (отдельный попап промолчал).
    expect(screen.getAllByRole("button", { name: "Понятно" })).toHaveLength(1);
    expect(screen.getAllByText("обновление до 2.10.0 завершено")).toHaveLength(1);
  });
});

describe("ход обновления", () => {
  // Окно проверяется напрямую: щёлкать по полосе, чтобы добраться до
  // прогресса, значило бы проверять заодно и открытие окна, которое проверено
  // выше, и мешать поддельным часам работать.
  function openDialog(
    overrides: Partial<UpdateInfo>,
    extra: {
      restarting?: boolean;
      timedOut?: boolean;
      onOpenChange?: (open: boolean) => void;
      onAcknowledge?: () => void;
    } = {},
  ) {
    render(
      <UpdateDialog
        info={info(overrides)}
        open
        onOpenChange={extra.onOpenChange ?? (() => {})}
        onStarted={() => {}}
        restarting={extra.restarting}
        timedOut={extra.timedOut}
        onAcknowledge={extra.onAcknowledge}
      />,
    );
  }

  // Заголовок — то, что человек сказал бы сам, а не отчёт о системе
  // (amnezia-vpn-server-7edq).
  it("говорит «Обновляем…», а не «Обновление идёт»", () => {
    openDialog({ state: "running", state_step: "установка", state_message: "ставлю выпуск 2.11.0" });
    expect(screen.getByRole("heading", { name: "Обновляем…" })).toBeInTheDocument();
  });

  // Хост пишет и машинное имя шага, и человеческое пояснение; показывалось
  // только первое, и выходило «Шаг: запрос» — не значащее ничего
  // (amnezia-vpn-server-7edq).
  it("под полосой стоит пояснение, а не машинное слово", () => {
    openDialog({ state: "running", state_step: "запрос", state_message: "проверяю выпуск" });
    expect(screen.getByText("проверяю выпуск")).toBeInTheDocument();
    expect(screen.queryByText(/Шаг:/)).not.toBeInTheDocument();
    expect(screen.queryByText("запрос")).not.toBeInTheDocument();
  });

  // Без пояснения машинное слово не всплывает обратно: «запрос» в одиночку
  // человеку не помогает (amnezia-vpn-server-7edq).
  it("без пояснения говорит общее, а не машинное слово", () => {
    openDialog({ state: "running", state_step: "запрос", state_message: "" });
    expect(screen.getByText("Идёт обновление")).toBeInTheDocument();
    expect(screen.queryByText(/запрос/)).not.toBeInTheDocument();
  });

  // Полоса прогресса не привязана ко времени: панель в середине
  // перезапускается, и живой ход отдавать некому. Дойти до ста она может
  // ровно в тот момент, когда обновление затянулось, — а это единственный
  // момент, когда на неё смотрят.
  it("никогда не доходит до ста, пока итога нет", async () => {
    vi.useFakeTimers();
    try {
      openDialog({ state: "running", state_step: "установка" });
      const bar = screen.getByRole("progressbar");
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
      });
      expect(Number(bar.getAttribute("aria-valuenow"))).toBeLessThan(100);
      // И не стоит на месте: за час она обязана уйти далеко от нуля.
      expect(Number(bar.getAttribute("aria-valuenow"))).toBeGreaterThan(50);
    } finally {
      vi.useRealTimers();
    }
  });

  it("во время обновления кнопки «Обновить» нет", () => {
    openDialog({ state: "running" });
    expect(screen.getByRole("progressbar")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Обновить" })).not.toBeInTheDocument();
  });

  it("говорит, что окно можно закрыть", () => {
    openDialog({ state: "running" });
    expect(screen.getByText(/Окно можно закрыть/)).toBeInTheDocument();
  });

  // state_at_utc пустой — это "итога никогда не было" (свежая установка), а
  // не "мой итог только что пришёл". Спутать их значило бы показать
  // случайный чужой итог тому, кто просто открыл окно посмотреть изменения
  // (amnezia-vpn-server-tjoq, -mrjh).
  it("без state_at_utc снова показывает изменения, а не итог", () => {
    openDialog({ state: "ok", state_message: "обновление до 2.9.0 завершено" });
    expect(screen.queryByText("обновление до 2.9.0 завершено")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Обновить" })).toBeInTheDocument();
  });

  // Критерий задачи amnezia-vpn-server-mrjh: упавший по сети запрос во
  // время перезапуска панели — не признак неудачи. Окно остаётся открытым
  // и говорящим, а не гаснет.
  it("запрос упал по сети — окно не закрывается и говорит, что панель перезапускается", () => {
    const onOpenChange = vi.fn();
    openDialog({ state: "running" }, { restarting: true, onOpenChange });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText(/панель перезапускается/i)).toBeInTheDocument();
    // Полоса прогресса остаётся на месте — это не отдельный отказ, а тот же ход.
    expect(screen.getByRole("progressbar")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  // Панель снова ответила успешным итогом — то же самое окно показывает
  // его сразу, без перезагрузки страницы (amnezia-vpn-server-mrjh).
  it("сервер вернулся с успешным итогом — тот же диалог показывает итог", () => {
    const onAcknowledge = vi.fn();
    openDialog(
      {
        state: "ok",
        state_message: "обновление до 2.10.0 завершено",
        state_at_utc: "2026-09-11T10:00:00Z",
      },
      { onAcknowledge },
    );
    expect(screen.getByText("Обновление завершено")).toBeInTheDocument();
    expect(screen.getByText("обновление до 2.10.0 завершено")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  // Сервер вернулся с неудачей — показана неудача, а не тишина
  // (amnezia-vpn-server-mrjh).
  it("сервер вернулся с неудачей — показана неудача", () => {
    openDialog({
      state: "rolled-back",
      state_message: "не удалось; сервер работает на 2.9.0",
      state_at_utc: "2026-09-11T10:00:00Z",
    });
    expect(screen.getByText("Обновиться не удалось")).toBeInTheDocument();
    expect(screen.getByText("не удалось; сервер работает на 2.9.0")).toBeInTheDocument();
  });

  // Потолок ожидания исчерпан — честное признание вместо вечной полосы, и
  // предложение обновить страницу вместо того, чтобы ждать молча
  // (amnezia-vpn-server-mrjh).
  it("потолок ожидания исчерпан — сказано, что связь не вернулась, предложено обновить страницу", () => {
    openDialog({ state: "running" }, { restarting: true, timedOut: true });
    expect(screen.getByText(/не отвечает/i)).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Обновить страницу" })).toBeInTheDocument();
  });
});

// Описание выпуска (amnezia-vpn-server-u1fv). Тело выпуска приходит с GitHub
// как есть, а там пункты набраны переносами по ширине — и отрисовка
// «физическая строка = абзац» разваливала один пункт на четыре.
describe("описание выпуска", () => {
  function openDialog(notes: string) {
    render(
      <UpdateDialog info={info({ notes })} open onOpenChange={() => {}} onStarted={() => {}} />,
    );
  }

  // Тело выпуска 2.10.13 дословно, как его отдаёт GitHub: строка про меню
  // обрывается на «пункт меню», продолжение уходит на следующие три строки с
  // отступом в два пробела. Выдуманный однострочный пункт эту беду не ловит.
  const release21013 = [
    "- Кнопка «Проверить обновления» теперь отвечает: пока проверка идёт, пункт меню",
    "  занят, а по её окончании панель говорит словами, вышла ли новая версия, нет",
    "  ли обновлений или проверить не удалось. Раньше при отсутствии новой версии не",
    "  происходило ничего.",
    "- Журнал стал шире, и подробность изменения стоит отдельной строкой — записи",
    "  больше не ломаются посреди фразы. Время проверки в окне состояния служб",
    "  переехало под заголовок.",
    "- Отстающие часы телефона или ноутбука показывали отрицательный возраст:",
    "  «Проверено -34940 сек назад». Исправлено.",
  ].join("\n");

  it("собирает пункт из переносов в один элемент списка", () => {
    openDialog(release21013);

    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    // Пункт целиком: и начало, и конец фразы, которая шла четвёртой строкой.
    expect(items[0].textContent).toBe(
      "Кнопка «Проверить обновления» теперь отвечает: пока проверка идёт, пункт меню " +
        "занят, а по её окончании панель говорит словами, вышла ли новая версия, нет " +
        "ли обновлений или проверить не удалось. Раньше при отсутствии новой версии " +
        "не происходило ничего.",
    );
    expect(items[2].textContent).toContain("«Проверено -34940 сек назад». Исправлено.");
  });

  // Владелец попросил буллиты, а не набор абзацев: пункты обязаны быть
  // списком с маркерами.
  it("рисует пункты списком с маркерами", () => {
    openDialog(release21013);

    const list = screen.getByRole("list");
    expect(list.tagName).toBe("UL");
    expect(list.className).toContain("list-disc");
  });

  // Не всё в теле выпуска — пункт. Вступление и приписка набираются без
  // дефиса, и превращать их в буллит нельзя.
  it("оставляет абзац без дефиса абзацем", () => {
    openDialog("Выпуск чинит окно обновления.\n\n- пункт списка\n\nПодробности в RELEASES.md.");

    const intro = screen.getByText("Выпуск чинит окно обновления.");
    expect(intro.tagName).toBe("P");
    expect(intro.closest("li")).toBeNull();
    const tail = screen.getByText("Подробности в RELEASES.md.");
    expect(tail.tagName).toBe("P");
    expect(tail.closest("li")).toBeNull();
    // И списком остаётся ровно то, что набрано дефисами.
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(1);
    expect(items[0].textContent).toBe("пункт списка");
  });

  // Стандартные sm:max-w-sm ломали собранный пункт через слово: строка в
  // ~55 знаков на текст такой длины не рассчитана.
  it("открывается шире стандартного диалога", () => {
    openDialog(release21013);
    expect(screen.getByRole("dialog").className).toContain("sm:max-w-lg");
  });
});

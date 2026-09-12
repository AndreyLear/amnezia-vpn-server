import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SpeedChart, formatBits, speedScale } from "@/components/SpeedChart";
import type { SpeedSeries } from "@/lib/api";

const fetchSpeed = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  fetchSpeed,
}));

function series(over: Partial<SpeedSeries> = {}): SpeedSeries {
  return {
    window: "10min",
    from_utc: "2026-09-08T12:50:00Z",
    to_utc: "2026-09-08T13:00:00Z",
    down_min_bps: [],
    down_max_bps: [],
    up_min_bps: [],
    up_max_bps: [],
    online: [],
    ...over,
  };
}

/**
 * Заливки: у приёма до максимума («поднималось») и до минимума
 * («держалось»), у отдачи — до максимума.
 */
/** Подложки «связи не было» (amnezia-vpn-server-tyic). */
function offline(): SVGRectElement[] {
  const svg = document.querySelector("svg");
  if (!svg) return [];
  return Array.from(svg.querySelectorAll('rect[data-series="offline"]'));
}

function areas(series: "down-max" | "up-max"): SVGPathElement[] {
  const svg = document.querySelector("svg");
  if (!svg) return [];
  return Array.from(svg.querySelectorAll(`path[data-series="${series}"]`));
}

/** Любые заливки приёма — чтобы просто дождаться отрисовки. */
function bands(): SVGPathElement[] {
  return areas("down-max");
}

/** Метка времени так, как её печатает браузер, — с секундами. */
function clock(utc: string): string {
  return new Date(utc).toLocaleTimeString("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** Все координаты y из фигуры — чтобы проверять высоту полосы. */
function ys(el: Element): number[] {
  const d = el.getAttribute("d") ?? "";
  return [...d.matchAll(/[\s,ML]([\d.]+)(?=[\sZ]|$)/g)].map((m) => Number(m[1]));
}

beforeEach(() => {
  fetchSpeed.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("график скорости", () => {
  // Самое важное: столбец рисуется от минимума до максимума. Среднее
  // спрятало бы секундный провал, ради которого график и заводится.
  // Заливка одна, до максимума. Их было две — плотная до минимума и
  // светлая до максимума; светлая читалась как посторонняя тень и путала
  // (amnezia-vpn-server-lzqx). Минимум не потерян: он в подсказке.
  it("рисует одну заливку до максимума, а не две", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000],
        down_max_bps: [100_000_000],
        up_min_bps: [0],
        up_max_bps: [0],
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(areas("down-max")).toHaveLength(1));
    expect(document.querySelectorAll('path[data-series="down-min"]')).toHaveLength(0);
    // Верх заливки — максимум столбца, а не среднее и не минимум.
    const top = Math.min(...ys(areas("down-max")[0]));
    const y = (bps: number) => 120 - Math.min(bps / 100_000_000, 1) * 120;
    expect(top).toBeCloseTo(y(100_000_000), 0);
    // И доходит до основания: снизу сплошь, а не дырки.
    expect(Math.max(...ys(areas("down-max")[0]))).toBe(120);
  });

  // Разрыв — это отсутствие столбика, а не столбик нулевой высоты. Ноль
  // означает «клиент ничего не получал» и является диагнозом.
  it("не рисует разрыв нулём", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000, null, 2_000_000],
        down_max_bps: [1_000_000, null, 2_000_000],
        up_min_bps: [0, null, 0],
        up_max_bps: [0, null, 0],
      }),
    );
    render(<SpeedChart clientId={1} />);

    // Разрыв рвёт заливку надвое, а не рисуется полосой нулевой высоты.
    await waitFor(() => expect(areas("down-max")).toHaveLength(2));
  });

  it("говорит, когда замеров нет вовсе", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [null, null],
        down_max_bps: [null, null],
        up_min_bps: [null, null],
        up_max_bps: [null, null],
      }),
    );
    render(<SpeedChart clientId={1} />);

    expect(await screen.findByText(/замеров нет/)).toBeInTheDocument();
  });

  it("говорит, когда историю не удалось прочитать", async () => {
    fetchSpeed.mockRejectedValue(new Error("нет связи"));
    render(<SpeedChart clientId={1} />);

    expect(await screen.findByText(/прочитать не удалось/)).toBeInTheDocument();
  });

  // Час заменён десятью минутами (amnezia-vpn-server-teos): в часе на
  // столбец приходилось по четыре замера, и та самая мелочь, ради которой
  // график открывают, усреднялась. Десять минут при такте в пять секунд —
  // 120 замеров, и на 120 столбцах свёртки нет вовсе.
  it("просит короткое окно ровно на 120 столбцов — по замеру на столбец", async () => {
    fetchSpeed.mockResolvedValue(series({ down_min_bps: [1], down_max_bps: [1], up_min_bps: [0], up_max_bps: [0] }));
    render(<SpeedChart clientId={7} />);

    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "10min", 120));
  });

  // Десять минут — «тормозит прямо сейчас», сутки — «найди вчерашний вечер».
  it("переключает окно", async () => {
    const user = userEvent.setup();
    fetchSpeed.mockResolvedValue(series({ down_min_bps: [1], down_max_bps: [1], up_min_bps: [0], up_max_bps: [0] }));
    render(<SpeedChart clientId={7} />);

    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "10min", expect.any(Number)));
    await user.click(screen.getByRole("button", { name: "Сутки" }));
    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "day", expect.any(Number)));
    await user.click(screen.getByRole("button", { name: "10 минут" }));
    await waitFor(() =>
      expect(fetchSpeed.mock.calls.at(-1)?.[1]).toBe("10min"),
    );
  });
});

describe("обновление по таймеру", () => {
  it("в коротком окне подтягивает новые замеры", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fetchSpeed.mockResolvedValue(series({ down_min_bps: [1], down_max_bps: [1], up_min_bps: [0], up_max_bps: [0] }));
    render(<SpeedChart clientId={1} />);

    await vi.waitFor(() => expect(fetchSpeed).toHaveBeenCalledTimes(1));
    await vi.advanceTimersByTimeAsync(11_000);
    expect(fetchSpeed.mock.calls.length).toBeGreaterThan(1);
    // И подтягивает именно короткое окно, а не что-то ещё.
    expect(fetchSpeed.mock.calls.at(-1)?.[1]).toBe("10min");
  });

  // В сутках один новый замер из 17 280 не меняет ни пикселя, а запрос раз
  // в пять секунд — работа впустую на каждой открытой вкладке.
  it("в сутках по таймеру не ходит", async () => {
    // Поддельные таймеры ставятся ДО отрисовки: включённые позже, они не
    // управляют интервалом, который уже создан, и проверка проходила бы
    // при любой реализации.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    fetchSpeed.mockResolvedValue(series({ down_min_bps: [1], down_max_bps: [1], up_min_bps: [0], up_max_bps: [0] }));
    render(<SpeedChart clientId={1} />);
    await vi.waitFor(() => expect(fetchSpeed).toHaveBeenCalledTimes(1));

    await user.click(screen.getByRole("button", { name: "Сутки" }));
    await vi.waitFor(() => expect(fetchSpeed).toHaveBeenCalledTimes(2));

    await vi.advanceTimersByTimeAsync(30_000);
    expect(fetchSpeed).toHaveBeenCalledTimes(2);
  });
});

describe("шкала", () => {
  // Владелец отверг 95-й процентиль: срезанный пик читается как поломка
  // графика (amnezia-vpn-server-dbmm). Шкала обязана вмещать самый высокий
  // столбец целиком.
  it("вмещает самый высокий столбец", () => {
    const quiet = Array.from({ length: 100 }, () => 10_000_000);
    const withSpike = [...quiet.slice(1), 200_000_000];
    expect(
      speedScale(
        series({ down_max_bps: withSpike, down_min_bps: withSpike, up_max_bps: [], up_min_bps: [] }),
      ),
    ).toBeGreaterThanOrEqual(200_000_000);
  });

  it("считает и отдачу тоже", () => {
    const up = [5_000_000, 90_000_000];
    expect(
      speedScale(series({ down_max_bps: [1_000], down_min_bps: [1_000], up_max_bps: up, up_min_bps: up })),
    ).toBeGreaterThanOrEqual(90_000_000);
  });

  // Ось с подписью «41.7» читается хуже, чем с «50».
  it("округляет верх до круглого числа", () => {
    const v = [41_700_000];
    expect(
      speedScale(series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] })),
    ).toBe(50_000_000);
  });

  it("ничего не режет: все столбцы ниже верха шкалы", async () => {
    const quiet = Array.from({ length: 20 }, () => 10_000_000);
    const withSpike = [...quiet.slice(1), 200_000_000];
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: withSpike, down_min_bps: withSpike, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    // Верх поля — y = 0; ни одна точка не должна оказаться выше него.
    expect(Math.min(...ys(bands()[0]))).toBeGreaterThanOrEqual(0);
  });
});

describe("легенда", () => {
  // Владелец: строка «разрывы — время, за которое замеров нет» под
  // графиком — лишняя, убрать её совсем. Разрывы по-прежнему видны в самой
  // заливке и в подсказке при наведении — эта проверка стережёт только то,
  // что поясняющий абзац больше не появляется, даже когда пропуски есть.
  it("не пишет отдельной строкой про разрывы, даже когда они есть", async () => {
    const v: (number | null)[] = [20_000_000, null, 20_000_000];
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(areas("down-max")).toHaveLength(2));
    expect(document.querySelector("[data-slot='speed-gaps']")).toBeNull();
    expect(document.body.textContent).not.toMatch(/разрывы/);
  });

  // Владелец: «нужно хотя бы цвет добавить», синий и оранжевый. Ряды
  // обязаны различаться сразу, а не при разглядывании.
  it("разводит ряды цветом", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: v, up_min_bps: v }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(document.querySelector("svg path.fill-sky-500\\/80")).not.toBeNull());
    expect(document.querySelector("svg path[data-series='up-max']")).toHaveClass(
      "fill-orange-500/60",
    );
  });

  // Слова называют действие, а не приём отрисовки: «заливка» и «линия»
  // объясняли, как нарисовано, а не что это значит (amnezia-vpn-server-udas).
  // Слова ровно те, что на наброске владельца (amnezia-vpn-server-jyhb).
  it("называет ряды словами, а не приёмом отрисовки", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: v, up_min_bps: v }),
    );
    render(<SpeedChart clientId={1} />);

    expect(await screen.findByText("Скачал")).toBeInTheDocument();
    expect(screen.getByText("Отдал")).toBeInTheDocument();
    expect(screen.queryByText(/заливка|линия — от/)).toBeNull();
  });

  // Набросок владельца: «23:40 ↓ скачал ↑ отдал 00:40». Легенда своей
  // строкой съедала высоту и без того длинной карточки
  // (amnezia-vpn-server-jyhb).
  it("держит легенду и обе метки времени в одной строке", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({
        down_max_bps: v,
        down_min_bps: v,
        up_max_bps: [],
        up_min_bps: [],
        from_utc: "2026-09-08T12:00:00Z",
        to_utc: "2026-09-08T13:00:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    const row = document.querySelector("[data-slot='speed-legend']");
    // Обе метки времени лежат в той же строке, что и легенда, а не своей.
    expect(screen.getByText(clock("2026-09-08T12:00:00Z")).parentElement).toBe(row);
    expect(screen.getByText(clock("2026-09-08T13:00:00Z")).parentElement).toBe(row);
    expect(screen.getByText("Скачал").closest("[data-slot='speed-legend']")).toBe(row);
    // Время по краям, легенда между ними.
    const parts = Array.from(row!.children).map((el) => el.textContent ?? "");
    expect(parts).toHaveLength(3);
    expect(parts[0]).toBe(clock("2026-09-08T12:00:00Z"));
    expect(parts[2]).toBe(clock("2026-09-08T13:00:00Z"));
    expect(parts[1]).toMatch(/Скачал.*Отдал/);
  });

  // Шкала следует за данными, и верх оси называет почти то же число, что и
  // пик: отдельная строка ради него не нужна (amnezia-vpn-server-jyhb).
  it("не поминает пик словами", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    expect(document.body.textContent).not.toMatch(/пик/i);
    // Кроме подписи у svg: незрячему читателю она заменяет обе убранные
    // строки, поэтому пик обязан остаться в ней.
    expect(document.querySelector("svg")?.getAttribute("aria-label")).toMatch(
      /пик 20\.0 Мбит\/с/,
    );
  });

  // Кружок держался на одном цвете и сам ничего не называл; стрелка
  // показывает направление (amnezia-vpn-server-jyhb).
  it("метит ряды стрелками, и цвет у них разный", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: v, up_min_bps: v }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    const down = document.querySelector("[data-slot='speed-legend'] [data-slot='legend-down']");
    const up = document.querySelector("[data-slot='speed-legend'] [data-slot='legend-up']");
    expect(down?.textContent?.trim()).toBe("↓");
    expect(up?.textContent?.trim()).toBe("↑");
    // Цвета те же, что у заливок в поле: иначе легенда объясняет не тот
    // график.
    expect(down).toHaveClass("text-sky-500");
    expect(up).toHaveClass("text-orange-500");
  });
});

describe("отдача", () => {
  // Отзыв владельца: «появляется куча линий, и они начинают сливаться.
  // Каша получается». Отдача была ломаной поверх заливки приёма: проволока
  // резала фигуру, и в местах пересечения не читался ни один ряд
  // (amnezia-vpn-server-6kj9).
  it("рисуется заливкой от основания, а не ломаной", async () => {
    const down = Array.from({ length: 10 }, () => 20_000_000);
    const up = Array.from({ length: 10 }, () => 8_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: down, down_min_bps: down, up_max_bps: up, up_min_bps: up }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(areas("up-max")).toHaveLength(1));
    // Ломаных в поле не осталось вовсе: два ряда — две фигуры.
    expect(document.querySelector("svg polyline")).toBeNull();
    // Фигура доходит до основания и замкнута, как у приёма.
    const shape = areas("up-max")[0];
    expect(Math.max(...ys(shape))).toBe(120);
    expect(shape.getAttribute("d")).toMatch(/Z$/);
  });

  // Перекрытие обязано читаться смешением цвета: под верхней заливкой
  // видна нижняя. Непрозрачная отдача просто закрыла бы приём.
  it("лежит поверх приёма и просвечивает", async () => {
    const down = Array.from({ length: 10 }, () => 20_000_000);
    const up = Array.from({ length: 10 }, () => 8_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: down, down_min_bps: down, up_max_bps: up, up_min_bps: up }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(areas("up-max")).toHaveLength(1));
    const up_ = areas("up-max")[0];
    const downDense = areas("down-max")[0];
    // Поверх — значит позже в порядке отрисовки.
    expect(
      downDense.compareDocumentPosition(up_) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    // И полупрозрачная: доля меньше сотни, иначе приём под ней пропадёт.
    const alpha = /fill-orange-500\/(\d+)/.exec(up_.getAttribute("class") ?? "");
    expect(alpha).not.toBeNull();
    expect(Number(alpha![1])).toBeGreaterThan(0);
    expect(Number(alpha![1])).toBeLessThan(100);
  });
});

describe("поле графика", () => {
  // Решение владельца: у поля не должно быть скруглённых углов.
  it("рисуется без скругления", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    expect(document.querySelector("svg")?.getAttribute("class")).not.toMatch(/rounded/);
  });

  it("и без скругления, когда рисовать нечего", async () => {
    fetchSpeed.mockRejectedValue(new Error("нет связи"));
    render(<SpeedChart clientId={1} />);

    const empty = await screen.findByText(/прочитать не удалось/);
    expect(empty.getAttribute("class")).not.toMatch(/rounded/);
  });

  // Легенда упиралась в кнопку «Удалить» под карточкой: между ними нужен
  // воздух (amnezia-vpn-server-6kj9).
  it("оставляет отступ под легендой", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    expect(document.querySelector("[data-slot='speed-legend']")).toHaveClass("pb-2");
  });
});

describe("чтение значения в точке", () => {
  // Владелец: «пока не понятно как смотреть пики и провалы». По трём числам
  // на оси провал в 200 Кбит/с не прочитать (amnezia-vpn-server-cor5).
  function hover(x: number) {
    const svg = document.querySelector("svg")!;
    svg.getBoundingClientRect = () => ({ left: 0, width: 100, top: 0, height: 120 }) as DOMRect;
    const plot = svg.parentElement!;
    plot.getBoundingClientRect = () => ({ left: 0, width: 100, top: 0, height: 120 }) as DOMRect;
    fireEvent.pointerMove(svg, { clientX: x, clientY: 60 });
  }

  // Подложка помечает отрезок, но какой именно столбец под указателем —
  // говорит подсказка. Нули там же, где «связи не было», без слов
  // неразличимы (amnezia-vpn-server-tyic).
  it("говорит про обрыв словами, а «неизвестно» не выдаёт за обрыв", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [0, 0],
        down_max_bps: [0, 0],
        up_min_bps: [0, 0],
        up_max_bps: [0, 0],
        online: [false, null],
      }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands().length).toBeGreaterThan(0));

    hover(25);
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain("Связи не было"),
    );

    hover(75);
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).not.toContain("Связи не было"),
    );
  });

  it("показывает время и оба ряда", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [2_000_000, 2_000_000],
        down_max_bps: [7_000_000, 7_000_000],
        up_min_bps: [500_000, 500_000],
        up_max_bps: [500_000, 500_000],
        from_utc: "2026-09-08T12:00:00Z",
        to_utc: "2026-09-08T13:00:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands()).toHaveLength(1));

    hover(10);
    const box = await screen.findByRole("status");
    // Разброс, а не одно число: он и есть провал внутри столбца.
    expect(box.textContent).toMatch(/2\.0\s*-\s*7\.0 Мбит\/с/);
    expect(box.textContent).toMatch(/500 Кбит\/с/);
  });

  // Владелец: секунды нужны и в подсказке, тем же порядком, что и по краям
  // графика — иначе подсказка называет минуту, а не тот замер под курсором.
  it("называет время подсказки с секундами", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [2_000_000, 2_000_000],
        down_max_bps: [7_000_000, 7_000_000],
        up_min_bps: [500_000, 500_000],
        up_max_bps: [500_000, 500_000],
        from_utc: "2026-09-08T12:00:00Z",
        to_utc: "2026-09-08T13:00:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands()).toHaveLength(1));

    hover(10);
    const box = await screen.findByRole("status");
    const time = box.querySelector(".text-muted-foreground")?.textContent ?? "";
    expect(time).toMatch(/^\d{2}:\d{2}:\d{2}$/);
  });

  // Разрыв обязан читаться и здесь, а не выглядеть нулём.
  it("на разрыве говорит, что замеров нет", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [null, 2_000_000],
        down_max_bps: [null, 2_000_000],
        up_min_bps: [null, 0],
        up_max_bps: [null, 0],
      }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands()).toHaveLength(1));

    hover(10);
    const box = await screen.findByRole("status");
    expect(box.textContent).toMatch(/Замеров нет/);
    expect(box.textContent).not.toMatch(/0 бит/);
  });

  it("ставит черту там же, где указатель", async () => {
    const v = Array.from({ length: 10 }, () => 5_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands()).toHaveLength(1));

    hover(50);
    await waitFor(() => {
      const guide = Array.from(document.querySelectorAll("svg line")).find((el) =>
        (el.getAttribute("class") ?? "").includes("text-foreground/50"),
      );
      expect(guide).toBeDefined();
      // Черта стоит в том же столбце, что и указатель: половина ширины.
      expect(Number(guide!.getAttribute("x1"))).toBeCloseTo(5.5, 0);
    });
  });

  // На касании pointerleave не приходит, поэтому подсказка снимается и по
  // pointerup.
  // Наверху подсказка закрывала бы ровно то, на что человек смотрит.
  it("уходит вниз, когда в столбце высокая скорость", async () => {
    const hi = Array.from({ length: 10 }, () => 100_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: hi, down_min_bps: hi, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands()).toHaveLength(1));

    hover(10);
    const box = await screen.findByRole("status");
    expect(box.style.bottom).toBe("0px");
    expect(box.style.top).toBe("");
  });

  it("снимается по отпусканию касания", async () => {
    const v = Array.from({ length: 10 }, () => 5_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(bands()).toHaveLength(1));

    hover(30);
    await screen.findByRole("status");
    fireEvent.pointerUp(document.querySelector("svg")!);
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });
});

describe("оси", () => {
  // В сутках время без даты обманывает: начало и конец окна показывают один
  // и тот же час, и подписи выглядят одинаковыми.
  it("в сутках подписывают дату, а не только час", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({
        window: "day",
        down_max_bps: v,
        down_min_bps: v,
        up_max_bps: [],
        up_min_bps: [],
        from_utc: "2026-09-07T13:00:00Z",
        to_utc: "2026-09-08T13:00:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);

    // Дата подписывается так же, как везде в панели: «7 сен», а не «07.09»
    // (amnezia-vpn-server-kfmf).
    expect(await screen.findByText(/7 сен/)).toBeInTheDocument();
    expect(screen.getByText(/8 сен/)).toBeInTheDocument();
  });

  // Отзыв владельца: «нет мин макс значений». Числа обязаны быть на самом
  // графике, а не только в подписи под ним.
  it("подписывают шкалу и время", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({
        down_max_bps: v,
        down_min_bps: v,
        up_max_bps: [],
        up_min_bps: [],
        from_utc: "2026-09-08T12:00:00Z",
        to_utc: "2026-09-08T13:00:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);

    // Единицы стоят в заголовке, а на оси только числа: полная подпись в
    // колонке не помещается и ломается на две строки посреди слова.
    expect(await screen.findByText("Скорость, Мбит/с")).toBeInTheDocument();
    expect(screen.getByText("20.0")).toBeInTheDocument();
    expect(screen.getByText("10.0")).toBeInTheDocument();
    expect(screen.getByText("0")).toBeInTheDocument();
    // Начало и конец окна — с секундами, а не только часом и минутой.
    expect(screen.getByText(clock("2026-09-08T12:00:00Z"))).toBeInTheDocument();
  });

  it("рисует линии сетки", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() =>
      expect(document.querySelectorAll("svg line.text-border")).toHaveLength(3),
    );
  });

  // Владелец: по краям графика видны только часы и минуты, а нужны секунды.
  it("подписывает секунды по краям, а не только часы и минуты", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({
        down_max_bps: v,
        down_min_bps: v,
        up_max_bps: [],
        up_min_bps: [],
        from_utc: "2026-09-08T12:00:07Z",
        to_utc: "2026-09-08T13:00:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    const row = document.querySelector("[data-slot='speed-legend']");
    const parts = Array.from(row!.children).map((el) => el.textContent ?? "");
    expect(parts[0]).toMatch(/^\d{2}:\d{2}:\d{2}$/);
    expect(parts[2]).toMatch(/^\d{2}:\d{2}:\d{2}$/);
  });

  // В сутках секунды нужны там же, где дата: формат один на оба края, чтобы
  // подписи не разъезжались по ширине.
  it("в сутках тоже подписывает секунды, и обе метки одной длины", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({
        window: "day",
        down_max_bps: v,
        down_min_bps: v,
        up_max_bps: [],
        up_min_bps: [],
        from_utc: "2026-09-07T13:00:07Z",
        to_utc: "2026-09-08T09:05:00Z",
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    const row = document.querySelector("[data-slot='speed-legend']");
    const parts = Array.from(row!.children).map((el) => el.textContent ?? "");
    expect(parts[0]).toMatch(/^\d{1,2} [а-я]{3} \d{2}:\d{2}:\d{2}$/);
    expect(parts[2]).toMatch(/^\d{1,2} [а-я]{3} \d{2}:\d{2}:\d{2}$/);
    // Раньше здесь стояло равенство длин: обе метки были «07.09 13:00:07», и
    // ширина не гуляла. С «7 сен» день не дополняется нулём, поэтому окно с 9
    // на 10 число даёт метки разной длины на один знак. Это принято: метки
    // прижаты к противоположным краям (justify-between), так что двигается
    // только середина легенды, и то на символ — а второй формат даты ради
    // этого заводить нельзя (amnezia-vpn-server-kfmf).
    expect(parts[0].endsWith(":07")).toBe(true);
    expect(parts[2].endsWith(":00")).toBe(true);
  });
});

describe("подпись скорости", () => {
  it("читается человеком", () => {
    expect(formatBits(10_000_000)).toBe("10.0 Мбит/с");
    expect(formatBits(250_000)).toBe("250 Кбит/с");
    expect(formatBits(300)).toBe("300 бит/с");
  });
});

// Нули в трафике и «сервер не слышал клиента» — разные вещи, и без
// подложки человек видел одни и те же нули и читал их как обрыв
// (amnezia-vpn-server-tyic).
describe("промежутки без связи", () => {
  it("закрашивает подряд идущие столбцы одной подложкой", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000, 0, 0, 0, 1_000_000],
        down_max_bps: [1_000_000, 0, 0, 0, 1_000_000],
        up_min_bps: [0, 0, 0, 0, 0],
        up_max_bps: [0, 0, 0, 0, 0],
        online: [true, false, false, false, true],
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(offline()).toHaveLength(1));
    const rect = offline()[0];
    expect(rect.getAttribute("x")).toBe("1");
    expect(rect.getAttribute("width")).toBe("3");
    // Во всю высоту: помечается отрезок времени, а не значение.
    expect(rect.getAttribute("height")).toBe("120");
  });

  it("не закрашивает «неизвестно»", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000, 0, 1_000_000],
        down_max_bps: [1_000_000, 0, 1_000_000],
        up_min_bps: [0, 0, 0],
        up_max_bps: [0, 0, 0],
        online: [true, null, true],
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(areas("down-max").length).toBeGreaterThan(0));
    expect(offline()).toHaveLength(0);
  });

  it("объясняет подложку в легенде, и только когда она есть", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000, 0],
        down_max_bps: [1_000_000, 0],
        up_min_bps: [0, 0],
        up_max_bps: [0, 0],
        online: [true, false],
      }),
    );
    const { unmount } = render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(screen.getByText("Связи не было")).toBeInTheDocument());
    unmount();

    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000, 1_000_000],
        down_max_bps: [1_000_000, 1_000_000],
        up_min_bps: [0, 0],
        up_max_bps: [0, 0],
        online: [true, true],
      }),
    );
    render(<SpeedChart clientId={1} />);
    await waitFor(() => expect(areas("down-max").length).toBeGreaterThan(0));
    expect(screen.queryByText("Связи не было")).not.toBeInTheDocument();
  });

  // Сервер прежней версии поля не присылает: страница не должна падать.
  it("переживает ответ без поля", async () => {
    const s = series({
      down_min_bps: [1_000_000],
      down_max_bps: [1_000_000],
      up_min_bps: [0],
      up_max_bps: [0],
    });
    delete (s as { online?: unknown }).online;
    fetchSpeed.mockResolvedValue(s);
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(areas("down-max")).toHaveLength(1));
    expect(offline()).toHaveLength(0);
  });
});

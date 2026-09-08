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
    window: "hour",
    from_utc: "2026-09-08T12:00:00Z",
    to_utc: "2026-09-08T13:00:00Z",
    down_min_bps: [],
    down_max_bps: [],
    up_min_bps: [],
    up_max_bps: [],
    ...over,
  };
}

/** Куски заливки приёма: по одному на каждый непрерывный отрезок. */
function bands(): SVGPathElement[] {
  const svg = document.querySelector("svg");
  if (!svg) return [];
  return Array.from(svg.querySelectorAll("path"));
}

/** Подпись под графиком целиком: она собрана из нескольких узлов. */
function legendText(): string {
  return document.querySelector("p.text-xs")?.textContent ?? "";
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
  it("рисует столбец от минимума до максимума, а не одну точку", async () => {
    fetchSpeed.mockResolvedValue(
      series({
        down_min_bps: [1_000_000],
        down_max_bps: [100_000_000],
        up_min_bps: [0],
        up_max_bps: [0],
      }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(bands()).toHaveLength(1));
    const heights = ys(bands()[0]);
    // Полоса имеет высоту: верх — максимум, низ — минимум. Одна координата
    // означала бы среднее, а среднее прячет провал.
    expect(Math.max(...heights)).toBeGreaterThan(Math.min(...heights));
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
    await waitFor(() => expect(bands()).toHaveLength(2));
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

  // Час — «жалуется прямо сейчас», сутки — «найди вчерашний вечер».
  it("переключает окно", async () => {
    const user = userEvent.setup();
    fetchSpeed.mockResolvedValue(series({ down_min_bps: [1], down_max_bps: [1], up_min_bps: [0], up_max_bps: [0] }));
    render(<SpeedChart clientId={7} />);

    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "hour", expect.any(Number)));
    await user.click(screen.getByRole("button", { name: "сутки" }));
    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "day", expect.any(Number)));
  });
});

describe("обновление по таймеру", () => {
  it("в часе подтягивает новые замеры", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fetchSpeed.mockResolvedValue(series({ down_min_bps: [1], down_max_bps: [1], up_min_bps: [0], up_max_bps: [0] }));
    render(<SpeedChart clientId={1} />);

    await vi.waitFor(() => expect(fetchSpeed).toHaveBeenCalledTimes(1));
    await vi.advanceTimersByTimeAsync(11_000);
    expect(fetchSpeed.mock.calls.length).toBeGreaterThan(1);
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

    await user.click(screen.getByRole("button", { name: "сутки" }));
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
  // Про пропуски написано всегда, и читатель ищет в графике то, чего в нём
  // не было (amnezia-vpn-server-dbmm).
  it("молчит о разрывах, когда их нет", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(legendText()).toMatch(/пик/));
    expect(legendText()).not.toMatch(/разрывы/);
  });

  it("называет их, когда они есть", async () => {
    const v: (number | null)[] = [20_000_000, null, 20_000_000];
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(legendText()).toMatch(/разрывы/));
  });

  // Владелец: «нужно хотя бы цвет добавить», синий и оранжевый. Ряды
  // обязаны различаться сразу, а не при разглядывании.
  it("разводит ряды цветом", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: v, up_min_bps: v }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(document.querySelector("svg path.fill-sky-500\\/70")).not.toBeNull());
    expect(document.querySelector("svg polyline.text-orange-500")).not.toBeNull();
  });

  // Кружок вместо слов «заливка» и «линия»: те объясняли приём отрисовки,
  // а не называли вещи (amnezia-vpn-server-udas).
  it("называет ряды словами при кружках, а не приёмом отрисовки", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: v, up_min_bps: v }),
    );
    render(<SpeedChart clientId={1} />);

    expect(await screen.findByText("скачивание")).toBeInTheDocument();
    expect(screen.getByText("отдача")).toBeInTheDocument();
    expect(screen.queryByText(/заливка|линия — от/)).toBeNull();
    expect(document.querySelector("span.bg-sky-500")).not.toBeNull();
    expect(document.querySelector("span.bg-orange-500")).not.toBeNull();
  });

  // Пик вынесен отдельно, а не втиснут в строку легенды.
  it("держит пик отдельно от легенды", async () => {
    const v = Array.from({ length: 10 }, () => 20_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: v, down_min_bps: v, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await waitFor(() => expect(legendText()).toMatch(/пик 20\.0 Мбит\/с/));
    const bullets = screen.getByText("скачивание").closest("div");
    expect(bullets?.textContent).not.toMatch(/пик/);
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
    expect(box.textContent).toMatch(/замеров нет/);
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

    expect(await screen.findByText(/07\.09/)).toBeInTheDocument();
    expect(screen.getByText(/08\.09/)).toBeInTheDocument();
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
    // Начало и конец окна.
    const start = new Date("2026-09-08T12:00:00Z").toLocaleTimeString("ru-RU", {
      hour: "2-digit",
      minute: "2-digit",
    });
    expect(screen.getByText(start)).toBeInTheDocument();
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
});

describe("подпись скорости", () => {
  it("читается человеком", () => {
    expect(formatBits(10_000_000)).toBe("10.0 Мбит/с");
    expect(formatBits(250_000)).toBe("250 Кбит/с");
    expect(formatBits(300)).toBe("300 бит/с");
  });
});

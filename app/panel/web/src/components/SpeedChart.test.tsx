import { render, screen, waitFor } from "@testing-library/react";
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
  // Отзыв владельца: один всплеск прижимал весь час к полу, и провалы
  // пропадали — то самое, от чего мы отказались, отвергнув усреднение
  // (amnezia-vpn-server-0ypv).
  it("одиночный всплеск её не задирает", () => {
    const quiet = Array.from({ length: 100 }, () => 10_000_000);
    const withSpike = [...quiet.slice(1), 200_000_000];
    const base = speedScale(
      series({ down_max_bps: quiet, down_min_bps: quiet, up_max_bps: [], up_min_bps: [] }),
    );
    const spiked = speedScale(
      series({ down_max_bps: withSpike, down_min_bps: withSpike, up_max_bps: [], up_min_bps: [] }),
    );
    expect(spiked).toBeLessThan(base * 1.25);
  });

  // А устойчивая высокая нагрузка — задирает: она и есть 95-й процентиль.
  it("устойчивая нагрузка её поднимает", () => {
    const busy = Array.from({ length: 100 }, () => 200_000_000);
    expect(
      speedScale(series({ down_max_bps: busy, down_min_bps: busy, up_max_bps: [], up_min_bps: [] })),
    ).toBeGreaterThan(100_000_000);
  });

  it("говорит, что пик выше шкалы, когда его обрезало", async () => {
    const quiet = Array.from({ length: 100 }, () => 10_000_000);
    const withSpike = [...quiet.slice(1), 200_000_000];
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: withSpike, down_min_bps: withSpike, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    expect(await screen.findByText(/выше шкалы/)).toBeInTheDocument();
  });

  it("не поминает шкалу, когда обрезать нечего", async () => {
    const quiet = Array.from({ length: 100 }, () => 10_000_000);
    fetchSpeed.mockResolvedValue(
      series({ down_max_bps: quiet, down_min_bps: quiet, up_max_bps: [], up_min_bps: [] }),
    );
    render(<SpeedChart clientId={1} />);

    await screen.findByText(/Пик/);
    expect(screen.queryByText(/выше шкалы/)).toBeNull();
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

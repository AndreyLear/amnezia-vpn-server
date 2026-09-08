import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SpeedChart, formatBits } from "@/components/SpeedChart";
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

/** Столбики графика в порядке отрисовки. */
function bars(prefix: "d" | "u"): SVGLineElement[] {
  const svg = document.querySelector("svg");
  if (!svg) return [];
  return Array.from(svg.querySelectorAll("line")).filter((el) =>
    (el.getAttribute("class") ?? "").includes(prefix === "d" ? "text-primary" : "text-muted"),
  );
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

    await waitFor(() => expect(bars("d")).toHaveLength(1));
    const bar = bars("d")[0];
    const y1 = Number(bar.getAttribute("y1"));
    const y2 = Number(bar.getAttribute("y2"));
    expect(y1).not.toBe(y2);
    // Пик наверху, провал внизу: чем меньше скорость, тем больше y.
    expect(y1).toBeGreaterThan(y2);
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

    await waitFor(() => expect(bars("d")).toHaveLength(2));
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

describe("подпись скорости", () => {
  it("читается человеком", () => {
    expect(formatBits(10_000_000)).toBe("10.0 Мбит/с");
    expect(formatBits(250_000)).toBe("250 Кбит/с");
    expect(formatBits(300)).toBe("300 бит/с");
  });
});

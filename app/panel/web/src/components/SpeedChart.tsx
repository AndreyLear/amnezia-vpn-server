import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { fetchSpeed, type SpeedSeries } from "@/lib/api";

/**
 * График скорости клиента (amnezia-vpn-server-tmjw, -0ypv).
 *
 * Своим svg, без библиотеки: одна область без легенды и зума — библиотека
 * весила бы больше, чем весь график, а прореживание делает сервер.
 *
 * Приём рисуется ПОЛОСОЙ между минимумом и максимумом столбца, а не
 * средним и не отдельными штрихами. Среднее спрятало бы секундный провал —
 * ровно то, ради чего график заводился; отдельные штрихи при трёхстах
 * столбцах превращались в щетину, по которой не видно ни формы, ни
 * значений. Тонкая полоса читается как «скорость держалась», широкая — как
 * «прыгала».
 */

type Range = "hour" | "day";

/**
 * Ширина в столбцах. В часе это 720 замеров на 180 столбцов — по четыре на
 * столбец: полоса остаётся честной, а щетины уже нет.
 */
const COLUMNS = 180;
const HEIGHT = 120;
/** Такт записи истории; в часе он и есть шаг обновления. */
const REFRESH_MS = 5000;

export function SpeedChart({ clientId }: { clientId: number }) {
  const [range, setRange] = useState<Range>("hour");
  // Единицы стоят в заголовке, а не у засечек: «15.8 Мбит/с» в колонке оси
  // не помещается и ломается на две строки посреди слова.
  const [unit, setUnit] = useState("");
  const [series, setSeries] = useState<SpeedSeries | null>(null);
  const [failed, setFailed] = useState(false);
  const [loading, setLoading] = useState(true);
  const alive = useRef(true);

  const load = useCallback(
    async (r: Range) => {
      try {
        const data = await fetchSpeed(clientId, r, COLUMNS);
        if (!alive.current) return;
        if (!data || !Array.isArray(data.down_max_bps)) {
          setFailed(true);
          return;
        }
        setSeries(data);
        setUnit(speedUnit(speedScale(data)));
        setFailed(false);
      } catch {
        if (alive.current) setFailed(true);
      } finally {
        if (alive.current) setLoading(false);
      }
    },
    [clientId],
  );

  useEffect(() => {
    alive.current = true;
    setLoading(true);
    void load(range);
    // Обновление только в часе. В сутках один новый замер из 17 280 не
    // меняет ни пикселя, и запрос раз в пять секунд был бы работой впустую
    // на каждой открытой вкладке.
    if (range !== "hour") return () => void (alive.current = false);
    const timer = setInterval(() => void load(range), REFRESH_MS);
    return () => {
      alive.current = false;
      clearInterval(timer);
    };
  }, [load, range]);

  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <dt className="text-muted-foreground">Скорость{unit ? `, ${unit}` : ""}</dt>
        <div className="flex gap-1">
          <RangeButton current={range} value="hour" onSelect={setRange}>
            час
          </RangeButton>
          <RangeButton current={range} value="day" onSelect={setRange}>
            сутки
          </RangeButton>
        </div>
      </div>
      <SpeedPlot series={series} loading={loading} failed={failed} />
    </div>
  );
}

function RangeButton({
  current,
  value,
  onSelect,
  children,
}: {
  current: Range;
  value: Range;
  onSelect: (r: Range) => void;
  children: React.ReactNode;
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant={current === value ? "secondary" : "ghost"}
      aria-pressed={current === value}
      onClick={() => onSelect(value)}
    >
      {children}
    </Button>
  );
}

function SpeedPlot({
  series,
  loading,
  failed,
}: {
  series: SpeedSeries | null;
  loading: boolean;
  failed: boolean;
}) {
  if (failed) return <Empty>историю прочитать не удалось</Empty>;
  if (loading || !series) return <Empty>загружаю</Empty>;

  const peak = Math.max(
    0,
    ...series.down_max_bps.map((v) => v ?? 0),
    ...series.up_max_bps.map((v) => v ?? 0),
  );
  if (peak === 0 && series.down_max_bps.every((v) => v === null)) {
    return <Empty>за это время замеров нет</Empty>;
  }
  const scale = speedScale(series);
  const n = series.down_max_bps.length;
  const hasGaps = series.down_max_bps.some((v) => v === null);
  const clipId = `speed-clip-${n}`;
  const y = (bps: number) => HEIGHT - Math.min(bps / scale, 1) * HEIGHT;

  return (
    <div className="grid gap-1">
      <div className="flex gap-1">
        {/* Ось слева, а не внутри svg: там preserveAspectRatio растянул бы
            текст вместе с картинкой. */}
        {/* Только числа: единицы названы в заголовке, потому что полная
            подпись в колонке оси не помещается и ломается на две строки. */}
        <div className="flex h-[120px] w-9 shrink-0 flex-col justify-between text-right text-[10px] leading-none text-muted-foreground tabular-nums">
          <span>{formatBitsBare(scale, scale)}</span>
          <span>{formatBitsBare(scale / 2, scale)}</span>
          <span>0</span>
        </div>
        <svg
          role="img"
          aria-label={`Скорость: шкала до ${formatBits(scale)}, пик ${formatBits(peak)}`}
          viewBox={`0 0 ${n} ${HEIGHT}`}
          preserveAspectRatio="none"
          className="h-[120px] min-w-0 flex-1 overflow-hidden rounded-md bg-muted/40"
        >
          {/* Рисовать только внутри поля: обрезка идёт по значению, но
              фигура без этого вылезала за рамку. */}
          <clipPath id={clipId}>
            <rect x={0} y={0} width={n} height={HEIGHT} />
          </clipPath>
          {[0, 0.5, 1].map((f) => (
            <line
              key={f}
              x1={0}
              x2={n}
              y1={HEIGHT * f}
              y2={HEIGHT * f}
              stroke="currentColor"
              strokeWidth={1}
              className="text-border"
              vectorEffect="non-scaling-stroke"
            />
          ))}
          {/* Приём — заливка. Разрывы рвут заливку, а не рисуются нулём:
              ноль означал бы «клиент ничего не получал», а это диагноз. */}
          {bandRuns(series.down_min_bps, series.down_max_bps).map((run, i) => (
            <path
              key={`d${i}`}
              d={bandPath(run, series.down_min_bps, series.down_max_bps, y)}
              clipPath={`url(#${clipId})`}
              className="fill-sky-500/70"
            />
          ))}
          {/* Отметка обрезанного столбца. Без неё число пика висело в
              воздухе: подпись говорила «40 Мбит/с», а верх шкалы был 7, и
              в поле зрения этому числу не соответствовало ничего. */}
          {series.down_max_bps.map((v, i) =>
            v !== null && v > scale ? (
              <line
                key={`c${i}`}
                x1={i + 0.5}
                x2={i + 0.5}
                y1={0}
                y2={4}
                stroke="currentColor"
                strokeWidth={2}
                className="text-sky-300"
                vectorEffect="non-scaling-stroke"
              />
            ) : null,
          )}
          {/* Отдача — тонкий контур поверх: палитра панели одноцветная, и
              различать ряды приходится не оттенком, а тем, что один залит, а
              другой обведён. */}
          {bandRuns(series.up_min_bps, series.up_max_bps).map((run, i) => (
            <polyline
              key={`u${i}`}
              points={run
                .map((c) => `${c + 0.5},${y(series.up_max_bps[c] ?? 0)}`)
                .join(" ")}
              fill="none"
              stroke="currentColor"
              strokeWidth={1.5}
              clipPath={`url(#${clipId})`}
              className="text-amber-400"
              vectorEffect="non-scaling-stroke"
            />
          ))}
        </svg>
      </div>
      <div className="flex justify-between pl-10 text-[10px] leading-none text-muted-foreground tabular-nums">
        <span>{formatClock(series.from_utc, series)}</span>
        <span>{formatClock(series.to_utc, series)}</span>
      </div>
      <p className="text-xs text-muted-foreground">
        <span className="text-sky-500">заливка</span> — к клиенту,{" "}
        <span className="text-amber-500">линия</span> — от него
        {peak > scale
          ? ` · пик ${formatBits(peak)}, отмечен засечками сверху`
          : ` · пик ${formatBits(peak)}`}
        {/* Про пропуски — только когда они есть: иначе читатель ищет в
            графике то, чего в нём не было. */}
        {hasGaps ? " · разрывы — время, за которое замеров нет" : ""}
      </p>
    </div>
  );
}

/**
 * Шкала строится по 95-му процентилю, а не по максимуму. Одной секунды
 * скачивания хватало, чтобы прижать весь остальной час к полу и спрятать
 * провалы — то есть сделать ровно то, от чего мы отказались, отвергнув
 * усреднение (amnezia-vpn-server-0ypv). Устойчивая нагрузка шкалу
 * поднимает: она и есть 95-й процентиль.
 */
export function speedScale(series: SpeedSeries): number {
  const values = [...series.down_max_bps, ...series.up_max_bps]
    .filter((v): v is number => v !== null && v > 0)
    .sort((a, b) => a - b);
  if (values.length === 0) return 1_000_000;
  const at = values[Math.min(values.length - 1, Math.floor(values.length * 0.95))];
  return Math.max(at, 1_000);
}

/** Непрерывные куски без разрывов: каждый рисуется отдельной фигурой. */
function bandRuns(mins: (number | null)[], maxs: (number | null)[]): number[][] {
  const runs: number[][] = [];
  let run: number[] = [];
  for (let i = 0; i < maxs.length; i++) {
    if (mins[i] === null || maxs[i] === null) {
      if (run.length) runs.push(run);
      run = [];
      continue;
    }
    run.push(i);
  }
  if (run.length) runs.push(run);
  return runs;
}

/** Полоса: по верхам вперёд, по низам назад. */
function bandPath(
  run: number[],
  mins: (number | null)[],
  maxs: (number | null)[],
  y: (bps: number) => number,
): string {
  const top = run.map((c) => `${c + 0.5},${y(maxs[c] ?? 0)}`);
  const bottom = [...run].reverse().map((c) => `${c + 0.5},${y(mins[c] ?? 0)}`);
  return `M${top.join(" L")} L${bottom.join(" L")} Z`;
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-[120px] items-center justify-center rounded-md bg-muted/40 text-xs text-muted-foreground">
      {children}
    </div>
  );
}

/** Человек читает канал в мегабитах; в килобитах — только совсем тихий. */
export function formatBits(bps: number): string {
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(1)} Мбит/с`;
  if (bps >= 1_000) return `${Math.round(bps / 1_000)} Кбит/с`;
  return `${Math.round(bps)} бит/с`;
}

/**
 * В сутках время без даты обманывает: начало и конец окна показывают один и
 * тот же час, и подписи выглядят одинаковыми.
 */
function formatClock(utc: string, series: SpeedSeries): string {
  const d = new Date(utc);
  if (Number.isNaN(d.getTime())) return "";
  const clock = d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
  if (series.window !== "day") return clock;
  const date = d.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" });
  return `${date} ${clock}`;
}

/** Единицы шкалы: их называет заголовок, а не каждая засечка. */
export function speedUnit(scale: number): string {
  if (scale >= 1_000_000) return "Мбит/с";
  if (scale >= 1_000) return "Кбит/с";
  return "бит/с";
}

/** Число без единиц: они уже названы в заголовке. */
function formatBitsBare(bps: number, scale: number): string {
  if (scale >= 1_000_000) return (bps / 1_000_000).toFixed(1);
  if (scale >= 1_000) return String(Math.round(bps / 1_000));
  return String(Math.round(bps));
}

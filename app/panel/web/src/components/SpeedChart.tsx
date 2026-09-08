import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { fetchSpeed, type SpeedSeries } from "@/lib/api";

/**
 * График скорости клиента (amnezia-vpn-server-tmjw, -0ypv).
 *
 * Своим svg, без библиотеки: одна область без легенды и зума — библиотека
 * весила бы больше, чем весь график, а прореживание делает сервер.
 *
 * Приём рисуется ДВУМЯ ЗАЛИВКАМИ от нуля: плотная доходит до минимума
 * столбца — «столько было всё время», светлая до максимума — «до столько
 * поднималось». Ни среднего, ни отдельных штрихов: среднее спрятало бы
 * секундный провал, ради которого график заводился, а штрихи при трёхстах
 * столбцах превращались в щетину без формы и значений.
 *
 * Отдача — тоже заливка от нуля, полупрозрачная, поверх приёма: ломаная
 * поверх фигуры превращала оба ряда в кашу (amnezia-vpn-server-6kj9).
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
  // Столбец под указателем. Без него по графику читались только три числа
  // на оси, а «сколько было в провале» не читалось никак
  // (amnezia-vpn-server-cor5).
  const [at, setAt] = useState<number | null>(null);
  const plot = useRef<HTMLDivElement>(null);
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
        <div ref={plot} className="relative min-w-0 flex-1">
        {at !== null && series !== null ? (
          <Readout series={series} at={at} n={series.down_max_bps.length} scale={scale} />
        ) : null}
        <svg
          role="img"
          aria-label={`Скорость: шкала до ${formatBits(scale)}, пик ${formatBits(peak)}`}
          viewBox={`0 0 ${n} ${HEIGHT}`}
          preserveAspectRatio="none"
          className="h-[120px] w-full touch-none overflow-hidden bg-black/10"
          onPointerMove={(e) => {
            const box = plot.current?.getBoundingClientRect();
            if (!box || box.width === 0) return;
            const i = Math.floor(((e.clientX - box.left) / box.width) * n);
            setAt(Math.min(n - 1, Math.max(0, i)));
          }}
          onPointerLeave={() => setAt(null)}
          onPointerUp={() => setAt(null)}
          onPointerCancel={() => setAt(null)}
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
          {/* Приём — две заливки от нуля, одна поверх другой. Раньше это
              была полоса от минимума до максимума, и там, где максимум
              скакал, между всплесками зияло поле: читалось как дырки в
              данных, хотя данные были сплошные. Теперь плотная заливка
              доходит до минимума — «столько было всё время», а светлая до
              максимума — «до столько поднималось». Разрывы рвут обе, а не
              рисуются нулём: ноль означал бы «клиент ничего не получал», а
              это диагноз. */}
          {bandRuns(series.down_min_bps, series.down_max_bps).map((run, i) => (
            <path
              key={`dmax${i}`}
              data-series="down-max"
              d={areaPath(run, series.down_max_bps, y)}
              clipPath={`url(#${clipId})`}
              className="fill-sky-500/35"
            />
          ))}
          {bandRuns(series.down_min_bps, series.down_max_bps).map((run, i) => (
            <path
              key={`dmin${i}`}
              data-series="down-min"
              d={areaPath(run, series.down_min_bps, y)}
              clipPath={`url(#${clipId})`}
              className="fill-sky-500/80"
            />
          ))}
          {/* Отдача — такая же заливка от основания, только полупрозрачная и
              поверх приёма. Раньше здесь была ломаная: проволока резала
              фигуру приёма, и там, где ряды пересекались, не читался ни
              один — глазу приходилось разбирать, где край заливки, а где
              линия (amnezia-vpn-server-6kj9). Две фигуры перекрываются
              цветом, и перекрытие читается смешением: сквозь оранжевую
              видно синюю. Рисуется только максимум: нижняя граница дала бы
              четвёртый полупрозрачный слой, и смешение, ради которого всё
              и затевалось, перестало бы читаться. Доля 60% выбрана по
              снимку: меньше — и сама отдача над пустым полем перестаёт
              читаться оранжевой, больше — и приём под ней пропадает. */}
          {bandRuns(series.up_min_bps, series.up_max_bps).map((run, i) => (
            <path
              key={`umax${i}`}
              data-series="up-max"
              d={areaPath(run, series.up_max_bps, y)}
              clipPath={`url(#${clipId})`}
              className="fill-orange-500/60"
            />
          ))}
          {/* Черта под указателем: без неё непонятно, к какому месту
              относится подсказка (amnezia-vpn-server-cor5). Она рисуется
              последней, иначе полупрозрачная отдача ложилась бы поверх и
              черта тускнела. */}
          {at !== null ? (
            <line
              x1={at + 0.5}
              x2={at + 0.5}
              y1={0}
              y2={HEIGHT}
              stroke="currentColor"
              strokeWidth={1}
              className="text-foreground/50"
              vectorEffect="non-scaling-stroke"
            />
          ) : null}
        </svg>
        </div>
        {/* Ось справа: слева она отодвигала само поле, а поле важнее чисел.
            Числа без единиц — единицы названы в заголовке, полная подпись в
            колонке не помещается и ломается на две строки. Ось живёт в
            HTML, а не в svg: там preserveAspectRatio растянул бы текст
            вместе с картинкой. */}
        <div className="flex h-[120px] w-9 shrink-0 flex-col justify-between text-left text-[10px] leading-none text-muted-foreground tabular-nums">
          <span>{formatBitsBare(scale, scale)}</span>
          <span>{formatBitsBare(scale / 2, scale)}</span>
          <span>0</span>
        </div>
      </div>
      {/* Время и легенда — одной строкой: время по краям, легенда между
          ними (amnezia-vpn-server-jyhb). Пика здесь больше нет вовсе: шкала
          следует за данными, и верх оси называет почти то же число, так что
          отдельная строка ради него только съедала высоту и без того
          длинной карточки. Незрячему пик по-прежнему называет подпись у
          svg — ему она заменяет обе убранные строки.

          Стрелка вместо кружка: кружок ничего не называл сам и держался
          только на цвете, а стрелка показывает направление. Слова же
          называют действие, а не приём отрисовки: «заливка» и «линия»
          заставляли сперва разобрать, что залито, а что обведено, и только
          потом — что это значит (amnezia-vpn-server-udas). */}
      <div
        data-slot="speed-legend"
        className="flex items-center justify-between gap-4 pt-1 pb-2 pr-10 text-xs text-muted-foreground"
      >
        <span className="tabular-nums">{formatClock(series.from_utc, series)}</span>
        <span className="flex items-center gap-4">
          <span className="flex items-center gap-1.5">
            {/* Цвет тот же, что у заливки приёма: легенда обязана совпадать
                с полем, иначе она объясняет не тот график. */}
            <span data-slot="legend-down" className="text-sky-500" aria-hidden>
              ↓
            </span>
            скачал
          </span>
          <span className="flex items-center gap-1.5">
            <span data-slot="legend-up" className="text-orange-500" aria-hidden>
              ↑
            </span>
            отдал
          </span>
        </span>
        <span className="tabular-nums">{formatClock(series.to_utc, series)}</span>
      </div>
      {/* Про разрывы — только когда они есть: иначе читатель ищет в графике
          то, чего в нём не было. */}
      {hasGaps ? (
        <p data-slot="speed-gaps" className="text-xs text-muted-foreground">
          разрывы — время, за которое замеров нет
        </p>
      ) : null}
    </div>
  );
}

/**
 * Шкала следует за данными: она вмещает самый высокий столбец, и ничто не
 * режется о верхний край.
 *
 * Была попытка строить её по 95-му процентилю, чтобы одиночный всплеск не
 * прижимал остальной час к полу. Владелец эту попытку отверг: срезанный пик
 * читается как поломка графика, а не как решение
 * (amnezia-vpn-server-0ypv, -dbmm). Мелочь внизу теперь читается не
 * масштабом, а подсказкой при наведении (amnezia-vpn-server-cor5).
 *
 * Верх округляется вверх до круглого числа: ось с подписью «41.7» читается
 * хуже, чем с «50», а запас сверху не даёт пику упереться в рамку.
 */
export function speedScale(series: SpeedSeries): number {
  const peak = Math.max(
    0,
    ...series.down_max_bps.map((v) => v ?? 0),
    ...series.up_max_bps.map((v) => v ?? 0),
  );
  if (peak <= 0) return 1_000_000;
  return roundUpNicely(peak);
}

/** Ближайшее сверху круглое: 1, 2, 2.5 или 5 на порядок величины. */
function roundUpNicely(value: number): number {
  const power = 10 ** Math.floor(Math.log10(value));
  for (const step of [1, 2, 2.5, 5, 10]) {
    if (value <= step * power) return step * power;
  }
  return 10 * power;
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

/**
 * Заливка от основания до значения: вверх, по верхам вперёд, вниз к
 * основанию и назад по нему.
 *
 * Раньше рисовалась полоса от минимума до максимума. Там, где максимум
 * скакал, между всплесками зияло поле, и это читалось как дырки в данных,
 * хотя данные были сплошные.
 */
function areaPath(
  run: number[],
  values: (number | null)[],
  y: (bps: number) => number,
): string {
  if (run.length === 0) return "";
  const first = run[0] + 0.5;
  const last = run[run.length - 1] + 0.5;
  const top = run.map((c) => `${c + 0.5},${y(values[c] ?? 0)}`);
  return `M${first},${HEIGHT} L${top.join(" L")} L${last},${HEIGHT} Z`;
}

/**
 * Что было в этом столбце. Столбец — это несколько замеров, поэтому
 * показывается разброс, а не одно число: «2.1-7.6» и есть тот самый
 * провал, ради которого график открывают.
 */
function Readout({
  series,
  at,
  n,
  scale,
}: {
  series: SpeedSeries;
  at: number;
  n: number;
  scale: number;
}) {
  const from = new Date(series.from_utc).getTime();
  const to = new Date(series.to_utc).getTime();
  const moment = new Date(from + ((to - from) * (at + 0.5)) / n);
  const time = Number.isNaN(moment.getTime())
    ? ""
    : moment.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
  const gap = series.down_max_bps[at] === null;
  // Подсказка держится у своего края: у правого края поля она иначе
  // вылезала бы за карточку.
  const right = at > n / 2;
  // И уходит вниз, когда в этом столбце высокая скорость: наверху она
  // закрывала бы ровно то, на что человек смотрит.
  const tall = (series.down_max_bps[at] ?? 0) > scale / 2;

  return (
    <div
      role="status"
      className="pointer-events-none absolute z-10 min-w-max rounded-md border border-border bg-popover px-2 py-1 text-[11px] leading-tight text-popover-foreground shadow-sm"
      style={{
        ...(right ? { right: 0 } : { left: 0 }),
        ...(tall ? { bottom: 0 } : { top: 0 }),
      }}
    >
      <div className="tabular-nums text-muted-foreground">{time}</div>
      {gap ? (
        // Разрыв обязан читаться и здесь: ноль означал бы «клиент ничего не
        // получал», а это диагноз.
        <div>замеров нет</div>
      ) : (
        <>
          <div className="text-sky-500 tabular-nums">
            ↓ {formatRange(series.down_min_bps[at], series.down_max_bps[at])}
          </div>
          <div className="text-orange-500 tabular-nums">
            ↑ {formatRange(series.up_min_bps[at], series.up_max_bps[at])}
          </div>
        </>
      )}
    </div>
  );
}

/** Разброс внутри столбца; при совпадении краёв — одно число. */
function formatRange(lo: number | null, hi: number | null): string {
  if (lo === null || hi === null) return "—";
  if (lo === hi) return formatBits(hi);
  return `${formatBitsBare(lo, hi)}\u2009-\u2009${formatBits(hi)}`;
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-[120px] items-center justify-center bg-black/10 text-xs text-muted-foreground">
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

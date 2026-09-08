import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { fetchSpeed, type SpeedSeries } from "@/lib/api";

/**
 * График скорости клиента (amnezia-vpn-server-tmjw).
 *
 * Своим svg, без библиотеки: одна область без легенды, зума и
 * переключателей осей — библиотека весила бы больше, чем весь график, а
 * прореживание всё равно делает сервер.
 *
 * Столбик рисуется от минимума до максимума столбца, и среднего здесь нет
 * вовсе. Усреднение прячет ровно то, ради чего график заводился: провал
 * длиной в секунду среди двадцати пяти замеров превращается в незаметную
 * рябь.
 */

type Range = "hour" | "day";

/** Ширина в столбцах. Больше пикселей просить незачем: столбец уже пиксель. */
const COLUMNS = 360;
const HEIGHT = 120;
/** Такт записи истории; в часе он и есть шаг обновления. */
const REFRESH_MS = 5000;

export function SpeedChart({ clientId }: { clientId: number }) {
  const [range, setRange] = useState<Range>("hour");
  const [series, setSeries] = useState<SpeedSeries | null>(null);
  const [failed, setFailed] = useState(false);
  // Первая загрузка и обновление по таймеру — разные вещи для человека:
  // «загружаю» показывается один раз, дальше линия просто дорисовывается.
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
        <dt className="text-muted-foreground">Скорость</dt>
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
  if (failed) {
    return <Empty>историю прочитать не удалось</Empty>;
  }
  if (loading || !series) {
    return <Empty>загружаю</Empty>;
  }
  const n = series.down_max_bps.length;
  const peak = Math.max(
    ...series.down_max_bps.map((v) => v ?? 0),
    ...series.up_max_bps.map((v) => v ?? 0),
    1,
  );
  const filled = series.down_max_bps.filter((v) => v !== null).length;
  if (filled === 0) {
    // Пусто и молчание — разные вещи, но человеку в обоих случаях нужно
    // одно: понять, что смотреть не на что, и почему.
    return <Empty>за это время замеров нет</Empty>;
  }

  const width = n;
  const y = (bps: number) => HEIGHT - (bps / peak) * HEIGHT;

  return (
    <div className="grid gap-1">
      <svg
        role="img"
        aria-label={`Скорость: пик ${formatBits(peak)}`}
        viewBox={`0 0 ${width} ${HEIGHT}`}
        preserveAspectRatio="none"
        className="h-[120px] w-full rounded-md bg-muted/40"
      >
        {series.down_max_bps.map((maxV, i) => {
          const minV = series.down_min_bps[i];
          // Разрыв не рисуется вовсе. Ноль означал бы «клиент ничего не
          // получал» — диагноз, которого никто не ставил.
          if (maxV === null || minV === null) return null;
          return (
            <line
              key={`d${i}`}
              x1={i + 0.5}
              x2={i + 0.5}
              y1={y(minV)}
              y2={y(maxV)}
              stroke="currentColor"
              strokeWidth={1}
              className="text-primary"
              vectorEffect="non-scaling-stroke"
            />
          );
        })}
        {series.up_max_bps.map((maxV, i) => {
          const minV = series.up_min_bps[i];
          if (maxV === null || minV === null) return null;
          return (
            <line
              key={`u${i}`}
              x1={i + 0.5}
              x2={i + 0.5}
              y1={y(minV)}
              y2={y(maxV)}
              stroke="currentColor"
              strokeWidth={1}
              className="text-muted-foreground/70"
              vectorEffect="non-scaling-stroke"
            />
          );
        })}
      </svg>
      <p className="text-xs text-muted-foreground">
        Пик {formatBits(peak)} · ↓ к клиенту, ↑ от него · пропуски — время, за
        которое замеров нет
      </p>
    </div>
  );
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

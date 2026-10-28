import { useEffect, useRef, useState } from "react";

import { fetchTraffic } from "@/lib/api";
import { foldDays, periodStart, TRAFFIC_DAYS, type DayTraffic } from "@/lib/dailyTraffic";
import { formatBytes, formatLocalDateShort } from "@/lib/format";
import type { Protocol } from "@/lib/protocol";

/**
 * Трафик клиента по дням за тридцать дней (amnezia-vpn-server-gzmb).
 *
 * Стоит во вкладке протокола под графиком скорости и, как он, показывает
 * только свой протокол. Сначала это была отдельная карточка под вкладками
 * с обоими протоколами сразу; владелец перенёс график во вкладки: рядом с
 * графиком скорости того же протокола он читается вместе с ним, а два
 * протокола на одном поле требовали второй легенды. Сервер отдаёт оба
 * протокола одним ответом, нужный отбирается здесь.
 *
 * Своим svg, как график скорости (SpeedChart): те же поле, сетка, ось
 * справа без единиц и единицы в заголовке, та же подсказка. Цвет говорит о
 * направлении так же, как там: синий — скачал, оранжевый — отдал. На день —
 * два столбика рядом, а не один сложенный: отдача обычно в десятки раз
 * меньше приёма и поверх него терялась бы.
 */

const HEIGHT = 120;
/** Доля ширины дня под один столбик; остальное — зазоры. */
const BAR = 0.34;

/** День одного протокола: столько скачал и столько отдал. */
type Day = { date: Date; down: number; up: number };

type Load =
  | { state: "loading" }
  | { state: "failed" }
  | { state: "ready"; days: Day[]; now: number };

function ofProtocol(days: DayTraffic[], protocol: Protocol): Day[] {
  return days.map((d) =>
    protocol === "vless"
      ? { date: d.date, down: d.vlessDown, up: d.vlessUp }
      : { date: d.date, down: d.awgDown, up: d.awgUp },
  );
}

export function DailyTrafficChart({
  clientId,
  protocol,
}: {
  clientId: number;
  protocol: Protocol;
}) {
  const [load, setLoad] = useState<Load>({ state: "loading" });

  useEffect(() => {
    // Флаг свой у каждого запуска: ответ по прежнему клиенту, пришедший
    // последним, не должен рисоваться у нового (как в SpeedChart,
    // amnezia-vpn-server-76mp.16).
    let active = true;
    const now = new Date();
    setLoad({ state: "loading" });
    // Без опроса: за время, пока окно открыто, день меняется на килобайты,
    // а запрос раз в несколько секунд был бы работой впустую.
    fetchTraffic(clientId, periodStart(now))
      .then((data) => {
        if (!active) return;
        if (!data || !Array.isArray(data.hours)) {
          setLoad({ state: "failed" });
          return;
        }
        setLoad({
          state: "ready",
          days: ofProtocol(foldDays(data.hours, now), protocol),
          now: now.getTime(),
        });
      })
      .catch(() => {
        if (active) setLoad({ state: "failed" });
      });
    return () => {
      active = false;
    };
  }, [clientId, protocol]);

  const scale = load.state === "ready" ? byteScale(load.days) : null;
  return (
    <div className="grid gap-2">
      <dt className="text-muted-foreground">Трафик по дням{scale ? `, ${scale.unit}` : ""}</dt>
      {load.state === "failed" ? (
        <Empty>Историю прочитать не удалось</Empty>
      ) : load.state === "loading" ? (
        <Empty>Загружаю</Empty>
      ) : scale === null ? (
        <Empty>
          <span>За {TRAFFIC_DAYS} дней трафика нет</span>
          {/* Пустой график у только что обновлённой панели — норма:
              прошлое не восстанавливается. Без этой строки он читался бы
              как поломка. */}
          <span className="text-muted-foreground/70">История копится с обновления панели</span>
        </Empty>
      ) : (
        <TrafficPlot days={load.days} now={load.now} scale={scale} />
      )}
    </div>
  );
}

type Scale = { top: number; unit: string; step: number };

/**
 * Шкала по самому высокому столбику, округлённая вверх до круглого в своих
 * единицах (как у скорости): «5» на оси читается лучше «3,1». Единица —
 * та, в которой верх шкалы не меньше единицы; null — трафика не было.
 */
function byteScale(days: Day[]): Scale | null {
  const peak = Math.max(0, ...days.map((d) => Math.max(d.down, d.up)));
  if (peak <= 0) return null;
  const units = ["Б", "Кб", "Мб", "Гб", "Тб"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(peak) / Math.log(1024)));
  const step = 1024 ** i;
  return { top: roundUpNicely(peak / step) * step, unit: units[i], step };
}

/** Ближайшее сверху круглое: 1, 2, 2.5 или 5 на порядок величины. */
function roundUpNicely(value: number): number {
  const power = 10 ** Math.floor(Math.log10(value));
  for (const step of [1, 2, 2.5, 5, 10]) {
    if (value <= step * power) return step * power;
  }
  return 10 * power;
}

function TrafficPlot({ days, now, scale }: { days: Day[]; now: number; scale: Scale }) {
  // День под указателем или пальцем.
  const [at, setAt] = useState<number | null>(null);
  const plot = useRef<HTMLDivElement>(null);
  const n = days.length;
  const h = (bytes: number) => (Math.min(bytes, scale.top) / scale.top) * HEIGHT;

  // Касание подсказку не убирает — палец уже поднят, а прочитать её надо.
  // Убирает касание мимо графика.
  useEffect(() => {
    if (at === null) return;
    const away = (e: PointerEvent) => {
      if (!plot.current?.contains(e.target as Node)) setAt(null);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [at]);

  // «Больше всего» — день с наибольшим трафиком в обе стороны вместе:
  // то, сколько клиент потратил за день, а не высота одного столбика.
  const busiest = days.reduce((a, b) => (b.down + b.up > a.down + a.up ? b : a));
  const pick = (clientX: number) => {
    const box = plot.current?.getBoundingClientRect();
    if (!box || box.width === 0) return;
    const i = Math.floor(((clientX - box.left) / box.width) * n);
    setAt(Math.min(n - 1, Math.max(0, i)));
  };

  const bar = (i: number, x: number, bytes: number, dir: "down" | "up") =>
    bytes > 0 ? (
      <rect
        key={`${dir}${i}`}
        data-series={dir}
        data-day={i}
        x={x}
        width={BAR}
        y={HEIGHT - h(bytes)}
        height={h(bytes)}
        className={dir === "down" ? "fill-sky-500" : "fill-orange-500"}
      />
    ) : null;

  return (
    <div className="grid gap-1">
      <div className="flex gap-1">
        <div ref={plot} className="relative min-w-0 flex-1">
          {at !== null ? (
            <Readout
              day={days[at]}
              at={at}
              n={n}
              now={now}
              tall={Math.max(days[at].down, days[at].up) > scale.top / 2}
            />
          ) : null}
          <svg
            role="img"
            aria-label={`Трафик по дням за ${TRAFFIC_DAYS} дней: больше всего ${formatBytes(
              busiest.down + busiest.up,
            )}, ${formatLocalDateShort(busiest.date, now)}`}
            viewBox={`0 0 ${n} ${HEIGHT}`}
            preserveAspectRatio="none"
            className="h-[120px] w-full touch-none overflow-hidden bg-black/10"
            onPointerDown={(e) => pick(e.clientX)}
            onPointerMove={(e) => pick(e.clientX)}
            onPointerLeave={(e) => {
              if (e.pointerType !== "touch") setAt(null);
            }}
            onPointerCancel={() => setAt(null)}
          >
            {/* Подсветка дня под указателем — под столбиками, во всю
                высоту: черта, как у скорости, легла бы ровно между двумя
                столбиками дня и ни к одному не относилась бы. */}
            {at !== null ? (
              <rect x={at} width={1} y={0} height={HEIGHT} className="fill-foreground/10" />
            ) : null}
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
            {days.map((d, i) => (
              <g key={i}>
                {bar(i, i + 0.5 - BAR - 0.02, d.down, "down")}
                {bar(i, i + 0.5 + 0.02, d.up, "up")}
              </g>
            ))}
          </svg>
        </div>
        {/* Ось справа и без единиц — как у графика скорости. */}
        <div className="flex h-[120px] w-9 shrink-0 flex-col justify-between text-left text-[10px] leading-none text-muted-foreground tabular-nums">
          <span>{axisNumber(scale.top / scale.step)}</span>
          <span>{axisNumber(scale.top / scale.step / 2)}</span>
          <span>0</span>
        </div>
      </div>
      {/* Даты и легенда одной строкой, как у скорости: первый день слева,
          «Сегодня» справа — неполный последний столбик так объясняет себя
          сам. */}
      <div
        data-slot="traffic-legend"
        className="flex items-center justify-between gap-4 pt-1 pb-2 pr-10 text-xs text-muted-foreground"
      >
        <span className="tabular-nums">{formatLocalDateShort(days[0].date, now)}</span>
        <span className="flex items-center gap-4">
          <span className="flex items-center gap-1.5">
            <span className="text-sky-500" aria-hidden>
              ↓
            </span>
            Скачал
          </span>
          <span className="flex items-center gap-1.5">
            <span className="text-orange-500" aria-hidden>
              ↑
            </span>
            Отдал
          </span>
        </span>
        <span>Сегодня</span>
      </div>
    </div>
  );
}

/** Цифры дня под указателем. */
function Readout({
  day,
  at,
  n,
  now,
  tall,
}: {
  day: Day;
  at: number;
  n: number;
  now: number;
  tall: boolean;
}) {
  const right = at > n / 2;
  return (
    <div
      role="status"
      className="pointer-events-none absolute z-10 min-w-max rounded-md border border-border bg-popover px-2 py-1 text-[11px] leading-tight text-popover-foreground shadow-sm"
      style={{
        ...(right ? { right: 0 } : { left: 0 }),
        ...(tall ? { bottom: 0 } : { top: 0 }),
      }}
    >
      <div className="tabular-nums text-muted-foreground">{formatLocalDateShort(day.date, now)}</div>
      {day.down + day.up === 0 ? (
        <div>Трафика не было</div>
      ) : (
        <div className="tabular-nums">
          <span className="text-sky-500">↓ {formatBytes(day.down)}</span>
          {" · "}
          <span className="text-orange-500">↑ {formatBytes(day.up)}</span>
        </div>
      )}
    </div>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-[120px] flex-col items-center justify-center gap-1 bg-black/10 text-center text-xs text-muted-foreground">
      {children}
    </div>
  );
}

/** Число оси: без единиц, до двух знаков, с запятой, как formatBytes. */
function axisNumber(v: number): string {
  return String(Number(v.toFixed(2))).replace(".", ",");
}

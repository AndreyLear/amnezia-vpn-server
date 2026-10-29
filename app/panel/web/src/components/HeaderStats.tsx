import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";

import { ServerDialog, type ServerTarget } from "@/components/ServerDialog";
import { UserText } from "@/components/UserText";
import type { EntryHost, HostSnapshot, IfaceState, VLESSState } from "@/lib/api";
import { formatBytes } from "@/lib/format";
import { entryLabel, serverButtonLabel } from "@/lib/serverNames";
import { cn } from "@/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

const DASH = "\u2014";

function roundPercent(value: number): number {
  return Math.round(value);
}

function loadColor(percent: number | null | undefined): string {
  if (percent == null) return "text-muted-foreground";
  if (percent < 70) return "text-muted-foreground";
  if (percent < 90) return "text-amber-500";
  return "text-red-500";
}

function prefersReducedMotion(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

function RollingValue({
  value,
  className,
}: {
  value: string;
  className?: string;
}) {
  const previous = useRef(value);
  const [outgoing, setOutgoing] = useState<string | null>(null);
  const outgoingNode = useRef<HTMLSpanElement | null>(null);
  const rolling =
    !prefersReducedMotion() &&
    (value !== previous.current || outgoing != null);

  useLayoutEffect(() => {
    if (value === previous.current) return;
    const from = previous.current;
    previous.current = value;
    if (prefersReducedMotion()) {
      setOutgoing(null);
      return;
    }
    setOutgoing(from);
  }, [value]);

  useEffect(() => {
    const node = outgoingNode.current;
    if (!node || outgoing == null) return;
    const clear = () => setOutgoing(null);
    node.addEventListener("animationend", clear);
    return () => node.removeEventListener("animationend", clear);
  }, [outgoing]);

  return (
    <span className="relative inline-grid overflow-hidden">
      {outgoing != null ? (
        <span
          ref={outgoingNode}
          aria-hidden
          className={cn(
            "col-start-1 row-start-1 animate-out slide-out-to-bottom duration-200 ease-out motion-reduce:hidden",
            className,
          )}
          onAnimationEnd={() => setOutgoing(null)}
        >
          {outgoing}
        </span>
      ) : null}
      <span
        key={value}
        className={cn(
          "col-start-1 row-start-1",
          rolling &&
            "animate-in slide-in-from-top duration-200 ease-out motion-reduce:animate-none",
          className,
        )}
      >
        {value}
      </span>
    </span>
  );
}

function Metric({
  ariaLabel,
  value,
  colorPercent,
  tooltip,
}: {
  ariaLabel: string;
  value: string;
  colorPercent: number | null | undefined;
  tooltip: string | null;
}) {
  const colored = value !== DASH;
  const colorClass = colored ? loadColor(colorPercent) : "text-muted-foreground";
  const inner = (
    <span aria-label={ariaLabel} className={colorClass}>
      <RollingValue value={value} className={colorClass} />
    </span>
  );
  if (!tooltip) return inner;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{inner}</TooltipTrigger>
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  );
}

/**
 * Три числа шапки — общие для основного сервера и каскада
 * (amnezia-vpn-server-q1do.1): узел присылает те же поля в том же виде.
 */
type Load = Pick<
  Partial<HostSnapshot>,
  | "cpu_percent"
  | "ram_percent"
  | "disk_percent"
  | "ram_used_bytes"
  | "ram_total_bytes"
  | "disk_used_bytes"
  | "disk_total_bytes"
>;

function cpuValue(host: Load | null): string {
  if (host?.cpu_percent == null) return DASH;
  return `${roundPercent(host.cpu_percent)}%`;
}

function ramValue(host: Load | null): string {
  if (host?.ram_percent == null) return DASH;
  return `${roundPercent(host.ram_percent)}%`;
}

function diskValue(host: Load | null): string {
  if (host?.disk_used_bytes == null) return DASH;
  return formatBytes(host.disk_used_bytes);
}

// subject — чья нагрузка: пусто у основного сервера, « каскада» или
// « каскада <имя>» у узла, чтобы подсказку под строкой каскада не приняли
// за подсказку про этот сервер. С именем владельца (amnezia-vpn-server-q1do.4)
// — « сервера «Москва»» и « каскада «Москва»».
function cpuTooltip(host: Load | null, subject = ""): string | null {
  if (host?.cpu_percent == null) return null;
  return `Загрузка CPU${subject}: ${roundPercent(host.cpu_percent)}%`;
}

function ramTooltip(host: Load | null, subject = ""): string | null {
  if (
    host?.ram_percent == null ||
    host.ram_used_bytes == null ||
    host.ram_total_bytes == null
  ) {
    return null;
  }
  return `Загрузка RAM${subject}: ${roundPercent(host.ram_percent)}% (${formatBytes(host.ram_used_bytes)} / ${formatBytes(host.ram_total_bytes)})`;
}

function diskTooltip(host: Load | null, subject = ""): string | null {
  if (
    host?.disk_percent == null ||
    host.disk_used_bytes == null ||
    host.disk_total_bytes == null
  ) {
    return null;
  }
  return `Загрузка диска${subject}: ${roundPercent(host.disk_percent)}% (${formatBytes(host.disk_used_bytes)} / ${formatBytes(host.disk_total_bytes)})`;
}

/** «2% / 21% / 6,4 Гб» — без подписей, подписи в aria-label и подсказках. */
function LoadValues({
  load,
  labels,
  subject,
}: {
  load: Load | null;
  labels: { cpu: string; ram: string; disk: string };
  subject?: string;
}) {
  return (
    <>
      <Metric
        ariaLabel={labels.cpu}
        value={cpuValue(load)}
        colorPercent={load?.cpu_percent}
        tooltip={cpuTooltip(load, subject)}
      />
      <span aria-hidden className="text-muted-foreground">
        /
      </span>
      <Metric
        ariaLabel={labels.ram}
        value={ramValue(load)}
        colorPercent={load?.ram_percent}
        tooltip={ramTooltip(load, subject)}
      />
      <span aria-hidden className="text-muted-foreground">
        /
      </span>
      <Metric
        ariaLabel={labels.disk}
        value={diskValue(load)}
        colorPercent={load?.disk_percent}
        tooltip={diskTooltip(load, subject)}
      />
    </>
  );
}

function ifaceName(host: HostSnapshot | null): string {
  const name = host?.iface?.trim();
  return name || "awg0";
}

function ifaceVisualState(host: HostSnapshot | null): IfaceState | "loading" {
  if (host == null) return "loading";
  return host.iface_state ?? "na";
}

function ifaceTooltip(state: IfaceState | "loading"): string {
  switch (state) {
    case "up":
      return "Интерфейс поднят";
    case "down":
      return "Интерфейс недоступен";
    case "error":
      return "Ошибка чтения статуса";
    default:
      return "Нет данных статуса";
  }
}

// Рядом с VLESS слово «интерфейс» ничего не говорит, а имя протокола в
// подсказке сразу показывает, к какому слову она относится
// (amnezia-vpn-server-45ds).
function awgTooltip(state: IfaceState | "loading"): string {
  switch (state) {
    case "up":
      return "AWG работает";
    case "down":
      return "AWG недоступен";
    case "error":
      return "Ошибка чтения статуса AWG";
    default:
      return "Нет данных статуса AWG";
  }
}

function vlessTooltip(state: Exclude<VLESSState, "none">): string {
  switch (state) {
    case "up":
      return "VLESS работает";
    case "down":
      return "VLESS не отвечает";
    default:
      return "Нет данных VLESS";
  }
}

const ifaceColorClass: Record<IfaceState, string> = {
  up: "text-muted-foreground",
  error: "text-amber-500",
  down: "text-red-500",
  na: "text-muted-foreground",
};

/**
 * Слово в первой строке шапки, окрашенное состоянием: спокойный серый, пока
 * всё работает, жёлтый и красный — когда нет, мерцание — пока данных нет.
 */
function StatusWord({
  ariaLabel,
  text,
  state,
  tooltip,
  className,
}: {
  ariaLabel: string;
  text: ReactNode;
  state: IfaceState | "loading";
  tooltip: ReactNode;
  className?: string;
}) {
  const shimmer =
    (state === "loading" || state === "na") && !prefersReducedMotion();
  const colorClass = shimmer
    ? "header-iface-shimmer"
    : ifaceColorClass[state === "loading" ? "na" : state];
  const inner = (
    <span aria-label={ariaLabel} className={cn("w-fit text-xs", colorClass, className)}>
      {text}
    </span>
  );
  return (
    <Tooltip>
      <TooltipTrigger asChild>{inner}</TooltipTrigger>
      <TooltipContent side="right">{tooltip}</TooltipContent>
    </Tooltip>
  );
}

/**
 * Первая строка шапки. Без VLESS — имя интерфейса, как было. С VLESS
 * (amnezia-vpn-server-45ds) — два протокола рядом, «AWG  VLESS», каждый
 * своим цветом: «awg0 VLESS» смешивал бы имя интерфейса с именем протокола.
 * VLESS «работает», когда Xray ответил на последний опрос панели.
 */
function HeaderIface({ host }: { host: HostSnapshot | null }) {
  const state = ifaceVisualState(host);
  const vless = host?.vless_state ?? "none";
  if (vless === "none") {
    return (
      <StatusWord
        ariaLabel="Интерфейс"
        text={ifaceName(host)}
        state={state}
        tooltip={ifaceTooltip(state)}
      />
    );
  }
  return (
    <span className="flex items-center gap-2">
      <StatusWord
        ariaLabel="Состояние AWG"
        text="AWG"
        state={state}
        tooltip={awgTooltip(state)}
      />
      <StatusWord
        ariaLabel="Состояние VLESS"
        text="VLESS"
        state={vless}
        tooltip={vlessTooltip(vless)}
      />
    </span>
  );
}

// Подсказка слова «Каскад» (amnezia-vpn-server-q1do.1). «5 минут» — порог
// entryOfflineAfter на сервере: состояние считает он.
// С именем владельца — «Каскад «Москва»» (amnezia-vpn-server-q1do.4), без
// него — как было.
function entryTooltip(entry: EntryHost): ReactNode {
  const title = entry.title?.trim();
  const word = title ? `Каскад «${title}»` : "Каскад";
  if (entry.state !== "up") {
    return entry.seen
      ? `${word} не выходил на связь больше 5 минут. Проверьте, что сервер включён и видит панель`
      : `${word} ещё ни разу не выходил на связь. Проверьте адрес панели и секрет узла на нём`;
  }
  if (!entry.reporting) {
    return `${word} на связи, но показатели не приходят — на нём старая версия. Обновите: запустите на узле install.sh --role entry`;
  }
  if (entry.rx_bytes == null || entry.tx_bytes == null) return `${word} работает`;
  // Трафик всего основного интерфейса узла, а не только клиентов, — отсюда
  // «за минуту», а не «клиенты передали». Узел ходит раз в минуту; пропуски
  // растягивают интервал, и он назван как есть.
  const minutes = Math.round((entry.interval_seconds ?? 60) / 60);
  const span = minutes <= 1 ? "За минуту" : `За ${minutes} мин`;
  // Одним столбцом: содержимое подсказки — flex-ряд, и две строки без
  // обёртки вставали рядом колонками.
  return (
    <span className="flex flex-col">
      <span>{`${word} работает`}</span>
      <span>
        {`${span} принято ${formatBytes(entry.rx_bytes)}, отправлено ${formatBytes(entry.tx_bytes)}`}
      </span>
    </span>
  );
}

/**
 * Строка узла-входа под строкой основного сервера: слово «Каскад» (с
 * именем, когда узлов несколько), окрашенное как AWG и VLESS, и те же три
 * числа. Узел молчит дольше 5 минут — слово красное, вместо чисел
 * прочерки: сервер не отдаёт числа несвежего отчёта.
 */
// Строка сервера в шапке — кнопка: клик открывает окно сервера с его
// именем (amnezia-vpn-server-q1do.4). Отступ съедается отрицательным
// полем, чтобы подсветка при наведении не сдвигала текст.
const rowButtonClass =
  "-mx-1 cursor-pointer rounded-md px-1 text-start outline-none transition-colors hover:bg-accent/60 focus-visible:ring-[3px] focus-visible:ring-ring/50";

function EntryRow({
  entry,
  count,
  onOpen,
}: {
  entry: EntryHost;
  count: number;
  onOpen: () => void;
}) {
  const title = entry.title?.trim();
  const suffix = title ? ` «${title}»` : count > 1 ? ` ${entry.name}` : "";
  const label = entryLabel(entry, count);
  const reporting = entry.state === "up" && entry.reporting;
  // На телефоне «Каскад de-1» с числами в ширину рядом с кнопками не
  // влезает: слово и числа не рвутся внутри себя, а числа целиком уходят
  // под слово. Длинное имя владельца обрезается многоточием.
  return (
    <button
      type="button"
      aria-label={title ? `Каскад «${title}»` : label}
      className={cn(rowButtonClass, "flex flex-wrap items-center gap-x-1 gap-y-0.5 tabular-nums")}
      onClick={onOpen}
    >
      <StatusWord
        ariaLabel={`Состояние каскада${suffix}`}
        text={title ? <UserText>{title}</UserText> : label}
        state={entry.state === "up" ? "up" : "down"}
        tooltip={entryTooltip(entry)}
        className={cn("me-1 whitespace-nowrap", title && "max-w-40 truncate")}
      />
      <span className="flex items-center gap-1 whitespace-nowrap">
        <LoadValues
          load={reporting ? entry : null}
          labels={{
            cpu: `CPU каскада${suffix}`,
            ram: `RAM каскада${suffix}`,
            disk: `Диск каскада${suffix}`,
          }}
          subject={` каскада${suffix}`}
        />
      </span>
    </button>
  );
}

type Selected = { kind: "server" } | { kind: "entry"; name: string } | null;

export function HeaderStats({
  host,
  className,
  onRenamed,
}: {
  host: HostSnapshot | null;
  className?: string;
  /** Имя сервера или каскада сохранено: пора перечитать снимок шапки. */
  onRenamed?: () => void | Promise<void>;
}) {
  const entries = host?.entries ?? [];
  const serverName = host?.server_name?.trim() ?? "";
  const serverSubject = serverName ? ` сервера «${serverName}»` : "";
  const [selected, setSelected] = useState<Selected>(null);
  // Окно держит живые данные из опроса, а не снимок на момент клика.
  const selectedEntry =
    selected?.kind === "entry" ? entries.find((e) => e.name === selected.name) : undefined;
  const target: ServerTarget | null =
    selected?.kind === "server"
      ? { kind: "server", name: serverName }
      : selectedEntry
        ? { kind: "entry", entry: selectedEntry, count: entries.length }
        : null;
  return (
    <TooltipProvider>
      <div
        className={cn(
          "flex min-w-0 flex-col gap-1 text-xs text-muted-foreground",
          className,
        )}
      >
        {/* Строка основного сервера: состояние протоколов и нагрузка. Имя
            владельца — словом перед числами, как «Каскад» в строке каскада,
            но цветом текста: «Дача 2 2%» иначе читалось бы одним числом
            (amnezia-vpn-server-q1do.4). Без имени — как было. */}
        <button
          type="button"
          aria-label={serverButtonLabel(serverName)}
          className={cn(rowButtonClass, "flex min-w-0 flex-col gap-1 text-xs")}
          onClick={() => setSelected({ kind: "server" })}
        >
          <HeaderIface host={host} />
          <span className="flex min-w-0 items-center gap-1 tabular-nums">
            {serverName ? (
              <UserText className="me-1 min-w-0 max-w-40 truncate text-foreground">{serverName}</UserText>
            ) : null}
            <LoadValues
              load={host}
              labels={{
                cpu: `CPU${serverSubject}`,
                ram: `RAM${serverSubject}`,
                disk: `Диск${serverSubject}`,
              }}
              subject={serverSubject}
            />
          </span>
        </button>
        {entries.map((entry) => (
          <EntryRow
            key={entry.name}
            entry={entry}
            count={entries.length}
            onOpen={() => setSelected({ kind: "entry", name: entry.name })}
          />
        ))}
      </div>
      <ServerDialog
        target={target}
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
        onSaved={onRenamed}
      />
    </TooltipProvider>
  );
}

import { useEffect, useLayoutEffect, useRef, useState } from "react";

import type { HostSnapshot, IfaceState } from "@/lib/api";
import { formatBytes } from "@/lib/format";
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

function cpuValue(host: HostSnapshot | null): string {
  if (host?.cpu_percent == null) return DASH;
  return `${roundPercent(host.cpu_percent)}%`;
}

function ramValue(host: HostSnapshot | null): string {
  if (host?.ram_percent == null) return DASH;
  return `${roundPercent(host.ram_percent)}%`;
}

function diskValue(host: HostSnapshot | null): string {
  if (host?.disk_used_bytes == null) return DASH;
  return formatBytes(host.disk_used_bytes);
}

function cpuTooltip(host: HostSnapshot | null): string | null {
  if (host?.cpu_percent == null) return null;
  return `Загрузка CPU: ${roundPercent(host.cpu_percent)}%`;
}

function ramTooltip(host: HostSnapshot | null): string | null {
  if (
    host?.ram_percent == null ||
    host.ram_used_bytes == null ||
    host.ram_total_bytes == null
  ) {
    return null;
  }
  return `Загрузка RAM: ${roundPercent(host.ram_percent)}% (${formatBytes(host.ram_used_bytes)} / ${formatBytes(host.ram_total_bytes)})`;
}

function diskTooltip(host: HostSnapshot | null): string | null {
  if (
    host?.disk_percent == null ||
    host.disk_used_bytes == null ||
    host.disk_total_bytes == null
  ) {
    return null;
  }
  return `Загрузка диска: ${roundPercent(host.disk_percent)}% (${formatBytes(host.disk_used_bytes)} / ${formatBytes(host.disk_total_bytes)})`;
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

const ifaceColorClass: Record<IfaceState, string> = {
  up: "text-muted-foreground",
  error: "text-amber-500",
  down: "text-red-500",
  na: "text-muted-foreground",
};

function HeaderIface({ host }: { host: HostSnapshot | null }) {
  const state = ifaceVisualState(host);
  const shimmer =
    (state === "loading" || state === "na") && !prefersReducedMotion();
  const colorClass = shimmer
    ? "header-iface-shimmer"
    : ifaceColorClass[state === "loading" ? "na" : state];
  const inner = (
    <span aria-label="Интерфейс" className={cn("text-xs", colorClass)}>
      {ifaceName(host)}
    </span>
  );
  return (
    <Tooltip>
      <TooltipTrigger asChild>{inner}</TooltipTrigger>
      <TooltipContent>{ifaceTooltip(state)}</TooltipContent>
    </Tooltip>
  );
}

export function HeaderStats({
  host,
  className,
}: {
  host: HostSnapshot | null;
  className?: string;
}) {
  return (
    <TooltipProvider>
      <div
        className={cn(
          "flex min-w-0 flex-col gap-1 text-xs text-muted-foreground",
          className,
        )}
      >
        <HeaderIface host={host} />
        <div className="flex items-center gap-1 tabular-nums">
          <Metric
            ariaLabel="CPU"
            value={cpuValue(host)}
            colorPercent={host?.cpu_percent}
            tooltip={cpuTooltip(host)}
          />
          <span aria-hidden className="text-muted-foreground">
            /
          </span>
          <Metric
            ariaLabel="RAM"
            value={ramValue(host)}
            colorPercent={host?.ram_percent}
            tooltip={ramTooltip(host)}
          />
          <span aria-hidden className="text-muted-foreground">
            /
          </span>
          <Metric
            ariaLabel="Диск"
            value={diskValue(host)}
            colorPercent={host?.disk_percent}
            tooltip={diskTooltip(host)}
          />
        </div>
      </div>
    </TooltipProvider>
  );
}

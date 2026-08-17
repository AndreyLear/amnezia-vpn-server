import type { HostSnapshot } from "@/lib/api";
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
  if (percent < 70) return "text-emerald-500";
  if (percent < 90) return "text-amber-500";
  return "text-red-500";
}

function Metric({
  label,
  value,
  colorPercent,
  tooltip,
}: {
  label: string;
  value: string;
  colorPercent: number | null | undefined;
  tooltip: string | null;
}) {
  const colored = value !== DASH;
  const inner = (
    <span className="text-muted-foreground">
      {label}{" "}
      <span className={colored ? loadColor(colorPercent) : "text-muted-foreground"}>
        {value}
      </span>
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
  return `Загрузка CPU ${roundPercent(host.cpu_percent)}%`;
}

function ramTooltip(host: HostSnapshot | null): string | null {
  if (
    host?.ram_percent == null ||
    host.ram_used_bytes == null ||
    host.ram_total_bytes == null
  ) {
    return null;
  }
  return `Загрузка RAM ${roundPercent(host.ram_percent)}% (${formatBytes(host.ram_used_bytes)} / ${formatBytes(host.ram_total_bytes)})`;
}

function diskTooltip(host: HostSnapshot | null): string | null {
  if (
    host?.disk_percent == null ||
    host.disk_used_bytes == null ||
    host.disk_total_bytes == null
  ) {
    return null;
  }
  return `Загрузка диска ${roundPercent(host.disk_percent)}% (${formatBytes(host.disk_used_bytes)} / ${formatBytes(host.disk_total_bytes)})`;
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
          "shrink-0 items-center gap-3 text-sm text-muted-foreground tabular-nums",
          className,
        )}
      >
        <Metric
          label="cpu"
          value={cpuValue(host)}
          colorPercent={host?.cpu_percent}
          tooltip={cpuTooltip(host)}
        />
        <Metric
          label="ram"
          value={ramValue(host)}
          colorPercent={host?.ram_percent}
          tooltip={ramTooltip(host)}
        />
        <Metric
          label="disk"
          value={diskValue(host)}
          colorPercent={host?.disk_percent}
          tooltip={diskTooltip(host)}
        />
      </div>
    </TooltipProvider>
  );
}

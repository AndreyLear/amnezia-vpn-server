import type { HostSnapshot } from "@/lib/api";
import { cn } from "@/lib/utils";

const DASH = "\u2014";

function formatPercent(value: number | null | undefined): string {
  if (value == null) return DASH;
  return String(Math.round(value));
}

export function HeaderStats({
  host,
  className,
}: {
  host: HostSnapshot | null;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "shrink-0 items-center gap-3 text-sm text-muted-foreground tabular-nums",
        className,
      )}
    >
      <span>CPU {formatPercent(host?.cpu_percent)}</span>
      <span>RAM {formatPercent(host?.ram_percent)}</span>
      <span>Диск {formatPercent(host?.disk_percent)}</span>
    </div>
  );
}

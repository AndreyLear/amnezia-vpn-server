const units = ["Б", "Кб", "Мб", "Гб", "Тб"] as const;

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} Б`;
  let value = n;
  let i = 0;
  while (i < units.length - 1 && Number((value / 1024).toFixed(1)) >= 0.1) {
    value /= 1024;
    i += 1;
  }
  return `${value.toFixed(1).replace(".", ",")} ${units[i]}`;
}

export function formatHandshake(iso: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  const ss = String(d.getUTCSeconds()).padStart(2, "0");
  return `${y}-${m}-${day} ${hh}:${mm}:${ss} UTC`;
}

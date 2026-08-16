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
  return `${day}.${m}.${y}, ${hh}:${mm}:${ss} UTC`;
}

export function formatHandshakeAge(iso: string | null, now = Date.now()): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const sec = Math.floor((now - t) / 1000);
  if (sec < 60) return `${sec} сек`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} мин`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} ч`;
  return `${Math.floor(hr / 24)} дн`;
}

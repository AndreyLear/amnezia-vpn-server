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

// Three-letter Russian month abbreviations, genitive case, no trailing dot
// ("11 сен", "5 мая") — the form the owner asked for everywhere a date is
// shown (amnezia-vpn-server-kfmf). Hardcoded rather than delegated to
// `Intl.DateTimeFormat`/`toLocaleDateString(..., { month: "short" })":
// in this project's Node and in real browsers that locale short form is
// inconsistent — mixed lengths and a trailing dot ("янв.", "март", "июнь",
// "сент.") — so it cannot be trusted to match the requested format.
const MONTHS_SHORT_RU = [
  "янв",
  "фев",
  "мар",
  "апр",
  "мая",
  "июн",
  "июл",
  "авг",
  "сен",
  "окт",
  "ноя",
  "дек",
] as const;

/**
 * "11 сен" for a date within the current (UTC) year, "11 сен 2025"
 * otherwise — the owner only wants the year written out when it actually
 * disambiguates (amnezia-vpn-server-kfmf). The day is never zero-padded
 * ("5 янв", not "05 янв"), matching how the day is normally written in
 * Russian dates. `d` and `now` are both read in UTC, preserving the
 * pre-existing behavior of this module: the panel shows the server's
 * timestamp as-is, not converted to the viewer's device timezone.
 */
export function formatDateShort(d: Date, now = Date.now()): string {
  const day = d.getUTCDate();
  const month = MONTHS_SHORT_RU[d.getUTCMonth()];
  const year = d.getUTCFullYear();
  const currentYear = new Date(now).getUTCFullYear();
  return year === currentYear ? `${day} ${month}` : `${day} ${month} ${year}`;
}

/**
 * Single formatter for every "date" or "date and time" display in the panel
 * (journal entries, service restarts, client handshakes) — the owner asked
 * that a new screen never grow a fourth date format of its own
 * (amnezia-vpn-server-kfmf). `now` defaults to the real clock and is only
 * overridden in tests, to decide whether the year needs to be shown.
 *
 * Seconds are kept: this task is about the date part of the format, not the
 * time part, and journal/service-restart readers rely on second-level
 * precision to tell apart events that landed in the same minute.
 */
export function formatHandshake(iso: string | null, now = Date.now()): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  const ss = String(d.getUTCSeconds()).padStart(2, "0");
  return `${formatDateShort(d, now)}, ${hh}:${mm}:${ss}`;
}

export function formatHandshakeAge(iso: string | null, now = Date.now()): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  // Время приходит с сервера, а `now` берётся с устройства, где открыта
  // панель. Отстающие часы телефона или ноутбука делали разность
  // отрицательной, и подпись читалась как «Проверено -34940 сек назад»
  // (amnezia-vpn-server-qrgv).
  //
  // Отсчёт зажимается в ноль, а не подменяется словом вроде «только что»:
  // здесь возвращается ДЛИТЕЛЬНОСТЬ, и вызывающий вправе дописать к ней
  // «назад». Наречие на этом месте дало бы «Проверено только что назад».
  const sec = Math.max(0, Math.floor((now - t) / 1000));
  if (sec < 60) return `${sec} сек`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} мин`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} ч`;
  return `${Math.floor(hr / 24)} дн`;
}

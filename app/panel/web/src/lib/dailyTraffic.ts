/**
 * Трафик клиента по дням (amnezia-vpn-server-gzmb).
 *
 * Сервер хранит и отдаёт часы UTC, а день — понятие местное: у человека
 * в UTC+7 час 20:00 UTC — уже завтра. Поэтому в дни часы складывает
 * браузер, в своём поясе. Час попадает в день, на который приходится его
 * начало; в поясах со сдвигом на полчаса граница дня проходит посреди
 * часа, и такой час целиком уходит в день своего начала — точнее часовых
 * корзин не сказать.
 *
 * Дни строятся шагом по календарю (`new Date(y, m, d - i)`), а не вычитанием
 * суток в миллисекундах: в день перехода на летнее или зимнее время 23 или
 * 25 часов, и вычитание съехало бы на час.
 */

/** Одна часовая корзина ответа /api/clients/{id}/traffic. */
export type TrafficHour = {
  hour_utc: string;
  awg_down: number;
  awg_up: number;
  vless_down: number;
  vless_up: number;
};

export type DayTraffic = {
  /** Местная полночь дня. */
  date: Date;
  awgDown: number;
  awgUp: number;
  vlessDown: number;
  vlessUp: number;
};

/** Сколько дней на графике. */
export const TRAFFIC_DAYS = 30;

/** Местная полночь первого дня периода — с неё и запрашиваются часы. */
export function periodStart(now: Date, days = TRAFFIC_DAYS): Date {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate() - (days - 1));
}

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

/** Дни периода от старого к сегодняшнему; дни без трафика — нулями. */
export function foldDays(hours: TrafficHour[], now: Date, days = TRAFFIC_DAYS): DayTraffic[] {
  const out: DayTraffic[] = [];
  const byKey = new Map<string, DayTraffic>();
  for (let i = days - 1; i >= 0; i--) {
    const date = new Date(now.getFullYear(), now.getMonth(), now.getDate() - i);
    const day = { date, awgDown: 0, awgUp: 0, vlessDown: 0, vlessUp: 0 };
    out.push(day);
    byKey.set(dayKey(date), day);
  }
  for (const h of hours) {
    const at = new Date(h.hour_utc);
    if (Number.isNaN(at.getTime())) continue;
    const day = byKey.get(dayKey(at));
    if (!day) continue;
    day.awgDown += h.awg_down;
    day.awgUp += h.awg_up;
    day.vlessDown += h.vless_down;
    day.vlessUp += h.vless_up;
  }
  return out;
}

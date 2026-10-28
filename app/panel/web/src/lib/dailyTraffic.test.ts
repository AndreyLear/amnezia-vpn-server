import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { foldDays, periodStart, type TrafficHour } from "@/lib/dailyTraffic";
import { formatLocalDateShort } from "@/lib/format";

/**
 * Дни считаются в поясе браузера (amnezia-vpn-server-gzmb): сервер отдаёт
 * часы UTC, и час 20:00 UTC у человека в UTC+7 — это уже завтра.
 * Node перечитывает process.env.TZ при присваивании, поэтому пояс задаётся
 * прямо в тесте.
 */
const savedTZ = process.env.TZ;

function hour(utc: string, h: Partial<TrafficHour> = {}): TrafficHour {
  return { hour_utc: utc, awg_down: 0, awg_up: 0, vless_down: 0, vless_up: 0, ...h };
}

describe("foldDays", () => {
  afterEach(() => {
    process.env.TZ = savedTZ;
  });

  describe("UTC+7", () => {
    beforeEach(() => {
      process.env.TZ = "Asia/Novosibirsk";
    });

    it("кладёт час в местный день, а не в день UTC", () => {
      // 28 окт 10:00 местного = 03:00 UTC.
      const now = new Date("2026-10-28T03:00:00Z");
      const days = foldDays(
        [
          // 27 окт 16:00 UTC = 27 окт 23:00 местного — ещё 27-е.
          hour("2026-10-27T16:00:00Z", { awg_down: 100 }),
          // 27 окт 17:00 UTC = 28 окт 00:00 местного — уже 28-е.
          hour("2026-10-27T17:00:00Z", { awg_down: 5, vless_up: 7 }),
          hour("2026-10-28T02:00:00Z", { awg_up: 3, vless_down: 11 }),
        ],
        now,
      );
      expect(days).toHaveLength(30);
      const today = days[29];
      const yesterday = days[28];
      expect(formatLocalDateShort(today.date, now.getTime())).toBe("28 окт");
      expect(today).toMatchObject({ awgDown: 5, awgUp: 3, vlessDown: 11, vlessUp: 7 });
      expect(formatLocalDateShort(yesterday.date, now.getTime())).toBe("27 окт");
      expect(yesterday).toMatchObject({ awgDown: 100, awgUp: 0, vlessDown: 0, vlessUp: 0 });
      expect(formatLocalDateShort(days[0].date, now.getTime())).toBe("29 сен");
    });

    it("начинает период с местной полуночи первого дня", () => {
      const now = new Date("2026-10-28T03:00:00Z");
      // 29 сен 00:00 местного = 28 сен 17:00 UTC.
      expect(periodStart(now).toISOString()).toBe("2026-09-28T17:00:00.000Z");
    });

    it("отбрасывает часы вне тридцати дней", () => {
      const now = new Date("2026-10-28T03:00:00Z");
      const days = foldDays([hour("2026-09-28T16:00:00Z", { awg_down: 9 })], now);
      expect(days.every((d) => d.awgDown === 0)).toBe(true);
    });
  });

  describe("переход на зимнее время", () => {
    beforeEach(() => {
      process.env.TZ = "America/New_York";
    });

    it("в сутках перехода 25 часов, и все они — один день", () => {
      // 1 ноя 2026 в Нью-Йорке часы переводят назад: сутки с 04:00 UTC
      // 1 ноя до 05:00 UTC 2 ноя.
      const now = new Date("2026-11-02T15:00:00Z");
      const hours: TrafficHour[] = [];
      for (let i = 0; i < 25; i++) {
        const at = new Date(Date.UTC(2026, 10, 1, 4 + i));
        hours.push(hour(at.toISOString().replace(".000", ""), { awg_down: 1 }));
      }
      const days = foldDays(hours, now);
      expect(formatLocalDateShort(days[28].date, now.getTime())).toBe("1 ноя");
      expect(days[28].awgDown).toBe(25);
      expect(days[27].awgDown).toBe(0);
      expect(days[29].awgDown).toBe(0);
    });
  });
});

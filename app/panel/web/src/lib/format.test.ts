import { describe, expect, it } from "vitest";

import { formatHandshake, formatHandshakeAge } from "@/lib/format";

const now = Date.parse("2026-08-16T12:00:00Z");

describe("formatHandshake", () => {
  // Table-driven cases for the amnezia-vpn-server-kfmf date format: day and
  // a three-letter, dot-free Russian month abbreviation, with the year
  // shown only when the entry is not from `now`'s (UTC) year.
  it.each([
    ["current year, mid-month", "2026-08-16T08:17:23Z", now, "16 авг, 08:17:23"],
    ["current year, single-digit day stays unpadded", "2026-01-05T08:17:23Z", now, "5 янв, 08:17:23"],
    ["previous year gets the year suffix", "2025-08-16T08:17:23Z", now, "16 авг 2025, 08:17:23"],
    [
      "year boundary: an event a second before New Year, relative to `now` right at New Year, still needs the year",
      "2025-12-31T23:59:59Z",
      Date.parse("2026-01-01T00:00:00Z"),
      "31 дек 2025, 23:59:59",
    ],
    [
      "year boundary: an event right at New Year, relative to the same `now`, does not",
      "2026-01-01T00:00:00Z",
      Date.parse("2026-01-01T00:00:00Z"),
      "1 янв, 00:00:00",
    ],
    ["midnight keeps two-digit zero time, not omitted", "2026-08-16T00:00:00Z", now, "16 авг, 00:00:00"],
  ] as const)("%s", (_label, iso, at, expected) => {
    expect(formatHandshake(iso, at)).toBe(expected);
  });

  it("returns an em dash for null or invalid timestamps", () => {
    expect(formatHandshake(null, now)).toBe("—");
    expect(formatHandshake("not-a-date", now)).toBe("—");
  });
});

describe("formatHandshakeAge", () => {
  // Время приходит с сервера, а `now` — с устройства. Отстающие часы
  // телефона делали разность отрицательной, и подпись читалась как
  // «Проверено -34940 сек назад» (amnezia-vpn-server-qrgv).
  it("не показывает отрицательный возраст при отстающих часах устройства", () => {
    const now = Date.UTC(2026, 8, 9, 3, 0, 0);
    const fromTheFuture = new Date(now + 10 * 60 * 60 * 1000).toISOString();
    const got = formatHandshakeAge(fromTheFuture, now);
    expect(got).not.toMatch(/-/);
    // И остаётся длительностью: вызывающий дописывает к ней «назад», а
    // наречие вроде «только что» дало бы «Проверено только что назад».
    expect(got).toBe("0 сек");
  });

  it("returns an em dash for null or invalid timestamps", () => {
    expect(formatHandshakeAge(null, now)).toBe("—");
    expect(formatHandshakeAge("not-a-date", now)).toBe("—");
  });

  it("uses seconds when elapsed is under a minute", () => {
    expect(formatHandshakeAge("2026-08-16T11:59:01Z", now)).toBe("59 сек");
    expect(formatHandshakeAge("2026-08-16T12:00:00Z", now)).toBe("0 сек");
  });

  it("uses minutes when elapsed is under an hour", () => {
    expect(formatHandshakeAge("2026-08-16T11:00:01Z", now)).toBe("59 мин");
    expect(formatHandshakeAge("2026-08-16T11:59:00Z", now)).toBe("1 мин");
  });

  it("uses hours when elapsed is under a day", () => {
    expect(formatHandshakeAge("2026-08-15T12:00:01Z", now)).toBe("23 ч");
    expect(formatHandshakeAge("2026-08-16T11:00:00Z", now)).toBe("1 ч");
  });

  it("uses days otherwise, flooring to the largest unit", () => {
    expect(formatHandshakeAge("2026-08-15T12:00:00Z", now)).toBe("1 дн");
    expect(formatHandshakeAge("2026-08-12T12:00:00Z", now)).toBe("4 дн");
  });
});

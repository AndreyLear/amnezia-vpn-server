import { describe, expect, it } from "vitest";

import { formatHandshake, formatHandshakeAge } from "@/lib/format";

const now = Date.parse("2026-08-16T12:00:00Z");

describe("formatHandshake", () => {
  it("writes the same UTC instant as Russian date and 24-hour time", () => {
    expect(formatHandshake("2026-08-16T08:17:23Z")).toBe("16.08.2026, 08:17:23");
    expect(formatHandshake("2026-08-16T00:00:00Z")).toBe("16.08.2026, 00:00:00");
  });

  it("returns an em dash for null or invalid timestamps", () => {
    expect(formatHandshake(null)).toBe("—");
    expect(formatHandshake("not-a-date")).toBe("—");
  });
});

describe("formatHandshakeAge", () => {
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

import { describe, expect, it } from "vitest";

import { formatBytes } from "@/lib/format";
import { nextDemoHost } from "@/lib/demoHost";
import type { HostSnapshot } from "@/lib/api";

function assertFilled(snap: HostSnapshot) {
  expect(snap.cpu_percent).not.toBeNull();
  expect(snap.ram_percent).not.toBeNull();
  expect(snap.disk_percent).not.toBeNull();
  expect(snap.ram_used_bytes).not.toBeNull();
  expect(snap.ram_total_bytes).not.toBeNull();
  expect(snap.disk_used_bytes).not.toBeNull();
  expect(snap.disk_total_bytes).not.toBeNull();
}

describe("nextDemoHost", () => {
  it("returns non-null percents and byte fields", () => {
    const snap = nextDemoHost(null, () => 0);
    assertFilled(snap);
  });

  it("keeps cpu and ram percents in 0–99 and changes rounded display vs prev", () => {
    const prev = nextDemoHost(null, () => 0);
    const next = nextDemoHost(prev, () => 0);
    for (const snap of [prev, next]) {
      expect(snap.cpu_percent).toBeGreaterThanOrEqual(0);
      expect(snap.cpu_percent).toBeLessThan(100);
      expect(snap.ram_percent).toBeGreaterThanOrEqual(0);
      expect(snap.ram_percent).toBeLessThan(100);
    }
    expect(Math.round(next.cpu_percent!)).not.toBe(Math.round(prev.cpu_percent!));
    expect(Math.round(next.ram_percent!)).not.toBe(Math.round(prev.ram_percent!));
  });

  it("changes cpu, ram, and formatBytes(disk_used_bytes) on successive calls", () => {
    const rng = () => 0;
    const a = nextDemoHost(null, rng);
    const b = nextDemoHost(a, rng);
    expect(a.cpu_percent).not.toBe(b.cpu_percent);
    expect(a.ram_percent).not.toBe(b.ram_percent);
    expect(formatBytes(a.disk_used_bytes!)).not.toBe(formatBytes(b.disk_used_bytes!));
  });
});

import type { HostSnapshot } from "@/lib/api";

export type Rng = () => number;

/** Representative used-disk sizes so formatBytes crosses unit buckets. */
const DISK_USED_SAMPLES = [
  800,
  48 * 1024,
  12 * 1024 ** 2,
  7 * 1024 ** 3,
  2 * 1024 ** 4,
] as const;

function pickInt(rng: Rng, maxExclusive: number): number {
  const n = Math.floor(rng() * maxExclusive);
  if (n >= maxExclusive) return maxExclusive - 1;
  if (n < 0) return 0;
  return n;
}

function nextPercent(rng: Rng, prev: number | null | undefined): number {
  let n = pickInt(rng, 100);
  if (prev != null && Math.round(n) === Math.round(prev)) {
    n = (n + 1) % 100;
  }
  return n;
}

function diskSampleIndex(bytes: number): number {
  const exact = DISK_USED_SAMPLES.indexOf(bytes as (typeof DISK_USED_SAMPLES)[number]);
  if (exact >= 0) return exact;
  for (let i = DISK_USED_SAMPLES.length - 1; i >= 0; i -= 1) {
    if (bytes >= DISK_USED_SAMPLES[i]) return i;
  }
  return 0;
}

export function nextDemoHost(prev: HostSnapshot | null, rng: Rng = Math.random): HostSnapshot {
  const cpu_percent = nextPercent(rng, prev?.cpu_percent);
  const ram_percent = nextPercent(rng, prev?.ram_percent);

  let diskIdx = pickInt(rng, DISK_USED_SAMPLES.length);
  if (prev?.disk_used_bytes != null && diskIdx === diskSampleIndex(prev.disk_used_bytes)) {
    diskIdx = (diskIdx + 1) % DISK_USED_SAMPLES.length;
  }
  const disk_used_bytes = DISK_USED_SAMPLES[diskIdx];
  const disk_total_bytes = DISK_USED_SAMPLES[DISK_USED_SAMPLES.length - 1];
  const disk_percent = Math.min(99, Math.round((disk_used_bytes / disk_total_bytes) * 100));

  const ram_total_bytes = 8 * 1024 ** 3;
  const ram_used_bytes = Math.round((ram_percent / 100) * ram_total_bytes);

  return {
    cpu_percent,
    ram_percent,
    disk_percent,
    ram_used_bytes,
    ram_total_bytes,
    disk_used_bytes,
    disk_total_bytes,
    iface: "awg0",
    iface_state: "up",
  };
}

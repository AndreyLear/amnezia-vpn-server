import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "index.css"), "utf8");

function rule(selector: string): string {
  const match = css.match(new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{[^}]*\\}`));
  expect(match, `missing CSS rule ${selector}`).toBeTruthy();
  return match![0];
}

describe("index.css client-card-sweep", () => {
  it("defines a one-shot light sweep on :hover, not an infinite loop", () => {
    expect(css).toContain("@keyframes client-card-light-sweep");

    const hover = rule(".client-card-sweep:hover::after");
    expect(hover).toMatch(/animation:/);
    expect(hover).toMatch(/ease-out 1| 1 /);
    expect(hover).not.toContain("infinite");

    const rest = rule(".client-card-sweep::after");
    expect(rest).toMatch(/opacity:\s*0/);
  });

  it("runs the hover sweep in 1.4s, not 0.75s, as a one-shot", () => {
    const hover = rule(".client-card-sweep:hover::after");
    expect(hover).toContain("1.4s");
    expect(hover).not.toContain("0.75s");
    expect(hover).toMatch(/ease-out 1/);
    expect(hover).not.toContain("infinite");
  });

  it("disables the sweep when the user prefers reduced motion", () => {
    const reduced = css.match(
      /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.client-card-sweep:hover::after\s*\{[^}]*\}/,
    );
    expect(reduced, "missing reduced-motion rule for .client-card-sweep:hover::after").toBeTruthy();
    expect(reduced![0]).toMatch(/animation:\s*none/);
  });
});

// The "Обновляем…" title's dots light up one after another
// (amnezia-vpn-server-d27j) — pinned the same way as the other decorative
// animations above: an infinite keyframe, per-dot stagger, and a
// reduced-motion override that turns the animation off and leaves the dots
// visible rather than stuck at opacity 0.
describe("index.css update-running-ellipsis", () => {
  it("fades each dot in and out on an infinite loop", () => {
    expect(css).toContain("@keyframes update-running-ellipsis-dot");

    const dot = rule(".update-running-ellipsis span");
    expect(dot).toMatch(/animation:/);
    expect(dot).toContain("infinite");
  });

  it("staggers the three dots so they light up in turn, not together", () => {
    const first = rule(".update-running-ellipsis span:nth-child(1)");
    const second = rule(".update-running-ellipsis span:nth-child(2)");
    const third = rule(".update-running-ellipsis span:nth-child(3)");
    expect(first).toMatch(/animation-delay:\s*0s/);
    expect(second).toMatch(/animation-delay:\s*0\.2s/);
    expect(third).toMatch(/animation-delay:\s*0\.4s/);
    // Three distinct delays, not the same value copy-pasted three times.
    const delays = new Set(
      [first, second, third].map((r) => /animation-delay:\s*([\d.]+s)/.exec(r)?.[1]),
    );
    expect(delays.size).toBe(3);
  });

  it("disables the animation and keeps the dots visible under reduced motion", () => {
    const reduced = css.match(
      /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.update-running-ellipsis span\s*\{[^}]*\}/,
    );
    expect(reduced, "missing reduced-motion rule for .update-running-ellipsis span").toBeTruthy();
    expect(reduced![0]).toMatch(/animation:\s*none/);
    expect(reduced![0]).toMatch(/opacity:\s*1/);
  });
});

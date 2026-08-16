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

  it("disables the sweep when the user prefers reduced motion", () => {
    const reduced = css.match(
      /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.client-card-sweep:hover::after\s*\{[^}]*\}/,
    );
    expect(reduced, "missing reduced-motion rule for .client-card-sweep:hover::after").toBeTruthy();
    expect(reduced![0]).toMatch(/animation:\s*none/);
  });
});

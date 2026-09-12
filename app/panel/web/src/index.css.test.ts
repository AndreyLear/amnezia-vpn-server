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

// Подпись пункта проверки обновлений меняется на ходу, и меню не должно
// дёргаться под курсором (amnezia-vpn-server-3tm4).
describe("подпись пункта проверки обновлений", () => {
  it("резервирует ширину псевдоэлементом, а не копией текста в DOM", () => {
    expect(css).toMatch(
      /\.header-menu-check::before\s*\{[\s\S]*?content:\s*var\(--header-menu-check-reserve[\s\S]*?\}/,
    );
  });

  it("прячет резерв от глаза и от указателя", () => {
    const rule = css.match(/\.header-menu-check::before\s*\{([\s\S]*?)\}/)?.[1] ?? "";
    expect(rule).toContain("visibility: hidden");
    expect(rule).toContain("pointer-events: none");
  });

  it("набегает многоточием, а не стоит на месте", () => {
    expect(css).toMatch(/\.checking-ellipsis > span\s*\{[\s\S]*?animation:\s*checking-ellipsis/);
    expect(css).toMatch(/@keyframes checking-ellipsis\s*\{/);
  });

  it("выключает многоточие, когда человек просил меньше движения", () => {
    expect(css).toMatch(
      /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.checking-ellipsis > span\s*\{[^}]*animation:\s*none/,
    );
  });
});

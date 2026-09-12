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

// Одна анимация многоточия на всю панель: и «Проверяю…» в меню, и
// «Обновляем…» в окне обновления просят одного и того же
// (amnezia-vpn-server-3tm4, -d27j).
describe("набегающее многоточие", () => {
  it("точки набегают по одной, а не стоят на месте", () => {
    expect(css).toMatch(/\.animated-ellipsis > span\s*\{[\s\S]*?animation:\s*animated-ellipsis/);
    expect(css).toMatch(/@keyframes animated-ellipsis\s*\{/);
  });

  it("точки идут со сдвигом, иначе мигают все разом", () => {
    expect(css).toMatch(/\.animated-ellipsis > span:nth-child\(2\)\s*\{[^}]*animation-delay/);
    expect(css).toMatch(/\.animated-ellipsis > span:nth-child\(3\)\s*\{[^}]*animation-delay/);
  });

  it("выключается, когда человек просил меньше движения", () => {
    expect(css).toMatch(
      /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.animated-ellipsis > span\s*\{[^}]*animation:\s*none/,
    );
  });
});

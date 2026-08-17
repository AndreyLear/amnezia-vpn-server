import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AmbientBackground } from "@/components/AmbientBackground";

const indexCss = readFileSync(
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../index.css"),
  "utf8",
);

function cssBlock(source: string, selector: string): string {
  const needle = `${selector} {`;
  const start = source.indexOf(needle);
  expect(start).toBeGreaterThan(-1);
  const open = source.indexOf("{", start);
  const close = source.indexOf("}", open);
  return source.slice(open, close + 1);
}

describe("AmbientBackground", () => {
  it("renders ambient-bg without a center modifier by default", () => {
    const { container } = render(<AmbientBackground />);

    const bg = container.querySelector(".ambient-bg");
    expect(bg).toBeInTheDocument();
    expect(bg).not.toHaveClass("ambient-bg--center");
  });

  it("adds ambient-bg--center when center is set", () => {
    const { container } = render(<AmbientBackground center />);

    const bg = container.querySelector(".ambient-bg");
    expect(bg).toBeInTheDocument();
    expect(bg).toHaveClass("ambient-bg--center");
  });

  it("softens the primary oval via mix tokens (list/login 16%, empty-state 8%)", () => {
    const defaultBlock = cssBlock(indexCss, ".ambient-bg");
    const centerBlock = cssBlock(indexCss, ".ambient-bg--center");

    expect(defaultBlock).toContain("--ambient-primary-mix: 16%");
    expect(defaultBlock).toContain("50% -20%");
    expect(defaultBlock).toContain(
      "color-mix(in oklch, var(--primary) var(--ambient-primary-mix), transparent)",
    );
    expect(centerBlock).toContain("8%");
    expect(centerBlock).toContain("--ambient-primary-pos: 50% 50%");
  });
});

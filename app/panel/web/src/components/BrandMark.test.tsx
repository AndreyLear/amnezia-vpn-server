import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { BrandMark } from "@/components/BrandMark";

describe("BrandMark", () => {
  it("renders an AWG Panel mark with theme-aware fills", () => {
    render(<BrandMark />);

    const mark = screen.getByRole("img", { name: "AWG Panel" });
    expect(mark.tagName.toLowerCase()).toBe("svg");
    expect(mark).toHaveAttribute("viewBox", "0 0 32 32");

    const tile = mark.querySelector("rect");
    expect(tile).toHaveAttribute("width", "32");
    expect(tile).toHaveAttribute("height", "32");
    expect(tile).toHaveAttribute("rx", "8");
    expect(tile).toHaveClass("fill-foreground");

    const letter = mark.querySelector("path");
    expect(letter).toHaveClass("fill-background");
    expect(letter).toHaveAttribute("fill-rule", "evenodd");
    expect(letter).toHaveAttribute(
      "d",
      "M16 6.5 25.5 25.5h-3.4l-2.05-4.7h-8.1L9.9 25.5H6.5L16 6.5Zm0 5.7-2.85 6.6h5.7L16 12.2Z",
    );
  });
});

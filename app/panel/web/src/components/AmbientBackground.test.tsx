import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AmbientBackground } from "@/components/AmbientBackground";

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
});

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Label } from "@/components/ui/label";

describe("Label", () => {
  it("uses regular weight so field captions stay lighter than headings", () => {
    render(<Label htmlFor="x">Новый пароль</Label>);

    const label = screen.getByText("Новый пароль");
    expect(label.className).toContain("font-normal");
    expect(label.className).not.toContain("font-medium");
  });
});

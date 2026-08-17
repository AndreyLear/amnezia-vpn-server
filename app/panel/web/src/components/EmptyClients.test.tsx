import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { EmptyClients } from "@/components/EmptyClients";

describe("EmptyClients", () => {
  it("shows caption, decorative cat, and add button", async () => {
    const user = userEvent.setup();
    const onAdd = vi.fn();
    render(<EmptyClients onAdd={onAdd} />);

    const caption = screen.getByText("Пока нет клиентов");
    expect(caption).toHaveClass("text-muted-foreground");
    const cat = caption.parentElement?.querySelector("svg");
    expect(cat).not.toBeNull();
    expect(cat).toHaveAttribute("aria-hidden", "true");

    await user.click(screen.getByRole("button", { name: "Добавить клиента" }));
    expect(onAdd).toHaveBeenCalledTimes(1);
  });

  it("pauses blink and tail motion when the user prefers reduced motion", () => {
    render(<EmptyClients onAdd={() => {}} />);
    const css = [...document.querySelectorAll("style")]
      .map((el) => el.textContent ?? "")
      .join("\n");
    expect(css).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)/);
    expect(css).toMatch(/animation:\s*none/);
  });
});

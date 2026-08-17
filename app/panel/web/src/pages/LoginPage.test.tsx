import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import LoginPage from "@/pages/LoginPage";

describe("LoginPage", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("uses ambient background and required username/password", () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 401 })),
    );

    const { container } = render(<LoginPage />);

    expect(container.querySelector(".ambient-bg")).toBeInTheDocument();
    expect(screen.getByLabelText("Имя пользователя")).toBeRequired();
    expect(screen.getByLabelText("Пароль")).toBeRequired();
    expect(screen.getByRole("button", { name: "Войти" })).toHaveAttribute(
      "data-size",
      "lg",
    );
    const form = container.querySelector("form");
    expect(form).toHaveClass("gap-6");
    expect(form).not.toHaveClass("gap-3");
    const fieldStack = form?.querySelector(":scope > div");
    expect(fieldStack).toHaveClass("grid", "gap-4");
    expect(screen.getByRole("button", { name: "Войти" })).not.toHaveClass("mt-4");
    expect(screen.queryByLabelText("Код")).not.toBeInTheDocument();
  });
});

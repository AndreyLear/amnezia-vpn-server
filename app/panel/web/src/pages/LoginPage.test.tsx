import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

    const bg = container.querySelector(".ambient-bg");
    expect(bg).toBeInTheDocument();
    expect(bg).not.toHaveClass("ambient-bg--center");
    expect(screen.getByLabelText("Имя пользователя")).toBeRequired();
    expect(screen.getByLabelText("Пароль")).toBeRequired();
    const submit = screen.getByRole("button", { name: "Войти" });
    expect(submit).toHaveAttribute("data-size", "lg");
    expect(submit).toHaveClass("h-12", "w-full");
    const form = container.querySelector("form");
    expect(form).toHaveClass("gap-6");
    expect(form).not.toHaveClass("gap-3");
    const logo = screen.getByRole("img", { name: "AWG Panel" });
    expect(logo.tagName.toLowerCase()).toBe("svg");
    expect(logo).not.toHaveAttribute("src");
    expect(logo).toHaveClass("size-12");
    expect(form?.firstElementChild).toBe(logo);
    expect(container.querySelector("img[src='/favicon.svg']")).toBeNull();
    const fieldStack = form?.querySelector(":scope > div");
    expect(fieldStack).toHaveClass("grid", "gap-4");
    expect(screen.getByRole("button", { name: "Войти" })).not.toHaveClass("mt-4");
    expect(screen.queryByLabelText("Код")).not.toBeInTheDocument();
  });

  // Лимит попыток: ответ 429 text/plain ронял разбор, кнопка оживала без
  // объяснения (amnezia-vpn-server-76mp.8).
  async function submitWith(login: () => Response | Promise<Response>) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input) === "/api/login") return login();
        return new Response(null, { status: 401 });
      }),
    );
    render(<LoginPage />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Имя пользователя"), "admin");
    await user.type(screen.getByLabelText("Пароль"), "wrong");
    await user.click(screen.getByRole("button", { name: "Войти" }));
  }

  it("shows the server's limit message on JSON 429", async () => {
    await submitWith(
      () =>
        new Response(JSON.stringify({ ok: false, message: "Подождите 5 минут" }), {
          status: 429,
          headers: { "Content-Type": "application/json", "Retry-After": "300" },
        }),
    );
    expect(await screen.findByText("Подождите 5 минут")).toBeInTheDocument();
  });

  it("explains the limit with Retry-After when 429 is plain text", async () => {
    await submitWith(
      () =>
        new Response("Too many\n", {
          status: 429,
          headers: { "Content-Type": "text/plain", "Retry-After": "290" },
        }),
    );
    expect(
      await screen.findByText("Слишком много попыток входа. Подождите 5 мин и попробуйте снова"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Войти" })).toBeEnabled();
  });

  it("explains the limit without Retry-After", async () => {
    await submitWith(() => new Response("Too many", { status: 429 }));
    expect(
      await screen.findByText("Слишком много попыток входа. Подождите немного и попробуйте снова"),
    ).toBeInTheDocument();
  });

  it("shows a general error when the answer is not JSON", async () => {
    await submitWith(() => new Response("boom", { status: 500 }));
    expect(
      await screen.findByText("Не удалось войти. Сервер ответил с ошибкой"),
    ).toBeInTheDocument();
  });

  it("says the panel is unreachable when the request fails", async () => {
    await submitWith(() => Promise.reject(new TypeError("Failed to fetch")));
    expect(
      await screen.findByText("Панель не отвечает. Проверьте подключение и попробуйте снова"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Войти" })).toBeEnabled();
  });
});


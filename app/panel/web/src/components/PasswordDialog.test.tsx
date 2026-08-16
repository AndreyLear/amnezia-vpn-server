import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { PasswordDialog } from "@/components/PasswordDialog";
import { setCsrf } from "@/lib/api";

function renderDialog(
  props: Partial<{
    totpEnabled: boolean;
    onOpenChange: (open: boolean) => void;
    onTotpChange: (enabled: boolean) => void;
  }> = {},
) {
  return render(
    <PasswordDialog
      open
      totpEnabled={props.totpEnabled ?? false}
      onOpenChange={props.onOpenChange ?? (() => {})}
      onTotpChange={props.onTotpChange ?? (() => {})}
    />,
  );
}

describe("PasswordDialog", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("titles the dialog Аккаунт and has no password heading or switch", () => {
    renderDialog();

    expect(screen.getByRole("heading", { name: "Аккаунт" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Изменить пароль" })).not.toBeInTheDocument();
    expect(screen.queryByText("Изменить пароль")).not.toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Пароль" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Двухфакторная аутентификация" })).toBeInTheDocument();
    expect(screen.getByText("выкл")).toBeInTheDocument();
    expect(
      screen.getByText("QR появится после подтверждения паролем"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("QR появится после подтверждения паролем."),
    ).not.toBeInTheDocument();
    const input = screen.getByLabelText(/для включения 2FA/);
    const hint = screen.getByText("QR появится после подтверждения паролем");
    expect(input.compareDocumentPosition(hint) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const heading = screen.getByRole("heading", { name: "Двухфакторная аутентификация" });
    expect(heading.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(hint.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeFalsy();
    expect(screen.getByRole("button", { name: "Включить" })).toBeInTheDocument();
  });

  it("closes without API call when password fields are empty", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDialog({ onOpenChange });

    await user.type(screen.getByLabelText(/для включения 2FA/), "2fa-only");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("posts confirm_password equal to the new password", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/api/account/password")) {
        return new Response(JSON.stringify({ ok: true, message: "Пароль изменён." }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (path.includes("/api/me")) {
        return new Response(
          JSON.stringify({ username: "admin", csrf: "csrf", totp: { enabled: false } }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDialog();

    await user.type(screen.getByLabelText("Текущий пароль"), "old-pass");
    await user.type(screen.getByLabelText("Новый пароль"), "new-pass");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });

    const passwordCall = fetchMock.mock.calls.find(([input]) =>
      String(input).includes("/api/account/password"),
    );
    expect(passwordCall).toBeDefined();
    const body = JSON.parse(String(passwordCall?.[1]?.body));
    expect(body).toMatchObject({
      old_password: "old-pass",
      new_password: "new-pass",
      confirm_password: "new-pass",
    });
  });

  it("does not call enroll when saving the password", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/api/account/totp/enroll")) {
        throw new Error("enroll must not be called");
      }
      if (path.includes("/api/account/password")) {
        return new Response(JSON.stringify({ ok: true, message: "Пароль изменён." }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (path.includes("/api/me")) {
        return new Response(
          JSON.stringify({ username: "admin", csrf: "csrf", totp: { enabled: false } }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDialog();

    await user.type(screen.getByLabelText("Текущий пароль"), "old-pass");
    await user.type(screen.getByLabelText("Новый пароль"), "new-pass");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    await vi.waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).includes("/api/account/password")),
      ).toBe(true);
    });
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).includes("/api/account/totp/enroll")),
    ).toBe(false);
  });

  it("does not enroll when Включить is clicked without the 2FA password", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDialog();

    await user.click(screen.getByRole("button", { name: "Включить" }));

    expect(
      screen.getByText(
        "Чтобы включить 2FA, подтвердите текущим паролем. Это не смена пароля аккаунта.",
      ),
    ).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.queryByAltText("QR-код 2FA")).not.toBeInTheDocument();
  });

  it("shows a same-origin QR URL and the TOTP secret after Включить", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path.includes("/api/account/totp/enroll")) {
          expect(JSON.parse(String(init?.body))).toMatchObject({ password: "secret-pass" });
          return new Response(
            JSON.stringify({
              ok: true,
              qr: "/account/totp/qr",
              secret: "JBSWY3DPEHPK3PXP",
              otpauth: "otpauth://totp/test",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        throw new Error(path);
      }),
    );

    renderDialog();

    await user.type(screen.getByLabelText(/для включения 2FA/), "secret-pass");
    await user.click(screen.getByRole("button", { name: "Включить" }));

    const img = await screen.findByAltText("QR-код 2FA");
    expect(img).toHaveAttribute("src", "/account/totp/qr");
    expect(img.getAttribute("src")?.startsWith("data:")).toBe(false);
    expect(screen.getByText("JBSWY3DPEHPK3PXP")).toBeInTheDocument();
    expect(screen.getByLabelText("Код подтверждения")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Подтвердить 2FA" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Включить" })).not.toBeInTheDocument();
  });

  it("shows Код 2FA when enabled and validates disable without 2FA fields", async () => {
    const user = userEvent.setup();
    const onTotpChange = vi.fn();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDialog({ totpEnabled: true, onTotpChange });

    expect(screen.getByLabelText("Код 2FA")).toBeInTheDocument();
    expect(screen.getByText("вкл")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Выключить" })).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Выключить" }));

    expect(
      screen.getByText("Чтобы выключить 2FA, введите текущий пароль и код из приложения."),
    ).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onTotpChange).not.toHaveBeenCalled();
  });

  it("shows friendly error when disabling 2FA with wrong credentials", async () => {
    const user = userEvent.setup();
    const onTotpChange = vi.fn();
    setCsrf("csrf");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/account/totp/disable")) {
          return new Response(
            JSON.stringify({ ok: false, message: "Операция не выполнена." }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        throw new Error(path);
      }),
    );

    renderDialog({ totpEnabled: true, onTotpChange });

    await user.type(screen.getByLabelText(/для отключения 2FA/), "wrong-pass");
    await user.type(screen.getByLabelText("Код из приложения"), "000000");
    await user.click(screen.getByRole("button", { name: "Выключить" }));

    expect(await screen.findByText("Неверный пароль или код.")).toBeInTheDocument();
    expect(screen.queryByText("Операция не выполнена.")).not.toBeInTheDocument();
    expect(onTotpChange).not.toHaveBeenCalled();
  });

  it("closes without API call when only 2FA code is filled", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDialog({ totpEnabled: true, onOpenChange });

    await user.type(screen.getByLabelText("Код 2FA"), "123456");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

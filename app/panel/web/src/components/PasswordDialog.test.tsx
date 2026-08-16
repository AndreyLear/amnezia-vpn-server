import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { PasswordDialog } from "@/components/PasswordDialog";
import { setCsrf } from "@/lib/api";

describe("PasswordDialog", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("asks for old and new password and has no passwordless control", async () => {
    const user = userEvent.setup();
    render(
      <PasswordDialog
        open
        totpEnabled={false}
        onOpenChange={() => {}}
        onTotpChange={() => {}}
      />,
    );

    expect(screen.getByLabelText("Текущий пароль")).toBeInTheDocument();
    expect(screen.getByLabelText("Новый пароль")).toBeInTheDocument();
    expect(screen.getByLabelText("Двухфакторная аутентификация")).toBeInTheDocument();
    expect(screen.queryByText(/passwordless/i)).not.toBeInTheDocument();
    expect(screen.queryByText("Только код")).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Текущий пароль"), "old");
    await user.type(screen.getByLabelText("Новый пароль"), "new");
  });

  it("requires a TOTP code when 2FA is enabled", () => {
    render(
      <PasswordDialog
        open
        totpEnabled
        onOpenChange={() => {}}
        onTotpChange={() => {}}
      />,
    );
    expect(screen.getByLabelText("Код 2FA")).toBeInTheDocument();
  });

  it("closes without API call when password fields are empty", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PasswordDialog
        open
        totpEnabled={false}
        onOpenChange={onOpenChange}
        onTotpChange={() => {}}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.queryByText("Операция не выполнена.")).not.toBeInTheDocument();
  });

  it("closes without API call when only 2FA code is filled", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PasswordDialog
        open
        totpEnabled
        onOpenChange={onOpenChange}
        onTotpChange={() => {}}
      />,
    );

    await user.type(screen.getByLabelText("Код 2FA"), "123456");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.queryByText("Операция не выполнена.")).not.toBeInTheDocument();
  });

  it("shows validation error when disabling 2FA without password and code", async () => {
    const user = userEvent.setup();
    const onTotpChange = vi.fn();
    const fetchMock = vi.fn(async () => {
      throw new Error("fetch should not be called");
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PasswordDialog
        open
        totpEnabled
        onOpenChange={() => {}}
        onTotpChange={onTotpChange}
      />,
    );

    expect(screen.getByRole("switch")).toBeChecked();
    await user.click(screen.getByRole("switch"));

    expect(
      screen.getByText("Чтобы выключить 2FA, введите текущий пароль и код из приложения."),
    ).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onTotpChange).not.toHaveBeenCalled();
    expect(screen.getByRole("switch")).toBeChecked();
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

    render(
      <PasswordDialog
        open
        totpEnabled
        onOpenChange={() => {}}
        onTotpChange={onTotpChange}
      />,
    );

    await user.type(screen.getByLabelText("Текущий пароль"), "wrong-pass");
    await user.type(screen.getByLabelText("Код 2FA"), "000000");
    await user.click(screen.getByRole("switch"));

    expect(await screen.findByText("Неверный пароль или код.")).toBeInTheDocument();
    expect(screen.queryByText("Операция не выполнена.")).not.toBeInTheDocument();
    expect(onTotpChange).not.toHaveBeenCalled();
    expect(screen.getByRole("switch")).toBeChecked();
  });

  it("shows a same-origin QR URL and the TOTP secret", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/account/totp/enroll")) {
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

    render(
      <PasswordDialog
        open
        totpEnabled={false}
        onOpenChange={() => {}}
        onTotpChange={() => {}}
      />,
    );

    await user.type(screen.getByLabelText("Пароль для 2FA"), "secret-pass");
    await user.click(screen.getByLabelText("Двухфакторная аутентификация"));

    const img = await screen.findByAltText("QR-код 2FA");
    expect(img).toHaveAttribute("src", "/account/totp/qr");
    expect(img.getAttribute("src")?.startsWith("data:")).toBe(false);
    expect(screen.getByText("JBSWY3DPEHPK3PXP")).toBeInTheDocument();
  });
});

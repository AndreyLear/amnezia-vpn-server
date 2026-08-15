import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { PasswordDialog } from "@/components/PasswordDialog";

describe("PasswordDialog", () => {
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
});

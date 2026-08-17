import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { QrDialog } from "@/components/QrDialog";

describe("QrDialog", () => {
  it("shows a scan hint without a trailing period", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    expect(
      screen.getByText("Отсканируйте код в приложении AmneziaVPN"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Отсканируйте код в приложении AmneziaVPN."),
    ).toBeNull();
  });

  it("puts title and hint in a header with 8px gap", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const title = screen.getByRole("heading", { name: "QR-код: Alice" });
    const hint = screen.getByText("Отсканируйте код в приложении AmneziaVPN");
    const header = title.parentElement;

    expect(header).toHaveAttribute("data-slot", "dialog-header");
    expect(header).toHaveClass("gap-2");
    expect(header).toContainElement(hint);
  });
});

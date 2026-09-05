import { act, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { QrDialog } from "@/components/QrDialog";

describe("QrDialog", () => {
  // Подпись называла одно приложение, тогда как конфигурацию AWG понимают
  // несколько клиентов: человеку с другим клиентом она говорила неправду
  // (amnezia-vpn-server-d86w).
  it("не называет одно приложение и не ставит точку в конце", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const hint = screen.getByText(/Отсканируйте код в приложении/);
    expect(hint).toBeInTheDocument();
    expect(hint.textContent).not.toMatch(/AmneziaVPN/);
    expect(hint.textContent?.trim().endsWith(".")).toBe(false);
  });

  it("ведёт к списку клиентов, а не к одному приложению", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const link = screen.getByRole("link", { name: "список клиентов" });
    expect(link).toHaveAttribute("href", "https://docs.amnezia.org/documentation/amnezia-wg");
    expect(link).toHaveAttribute("target", "_blank");
    // Вкладка, открытая ссылкой, не должна получать доступ к окну панели.
    expect(link.getAttribute("rel")).toContain("noopener");
    expect(link.getAttribute("rel")).toContain("noreferrer");
  });

  it("puts title and hint in a header with 8px gap", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const title = screen.getByRole("heading", { name: "QR-код: Alice" });
    const hint = screen.getByText(/Отсканируйте код в приложении/);
    const header = title.parentElement;

    expect(header).toHaveAttribute("data-slot", "dialog-header");
    expect(header).toHaveClass("gap-2");
    expect(header).toContainElement(hint);
  });

  // A fixed 256 px box was what the dialog shipped with, and 256 px over
  // the 89 modules of a client config is under 3 px of pitch — too little
  // for a camera photographing a screen (T-ky6l).
  it("shows the symbol at the full dialog width, square and unconstrained", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const qr = screen.getByRole("img", { name: "QR-код клиента Alice" });

    expect(qr).toHaveClass("w-full");
    expect(qr).toHaveClass("aspect-square");
    expect(qr.className).not.toMatch(/\b(size|w|max-w)-\d/);
    expect(qr).not.toHaveAttribute("width");
    expect(qr).not.toHaveAttribute("height");
  });

  it("does not autofocus the close button when opened", async () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const title = screen.getByRole("heading", { name: "QR-код: Alice" });
    const close = screen.getByRole("button", { name: /close/i });

    await act(async () => {
      await new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      });
    });

    expect(document.activeElement).not.toBe(close);
    expect(title).toBeVisible();
  });
});

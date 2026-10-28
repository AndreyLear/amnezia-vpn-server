import { act, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { QrDialog } from "@/components/QrDialog";

describe("QrDialog", () => {
  // amnezia-vpn-server-14y0.17: владелец попросил убрать пояснения из окна
  // целиком. Прежние подсказки называли приложения (d86w, d077a64), а
  // названия приложений устаревают; в шапке остаётся только заголовок.
  it("без пояснений: в шапке только заголовок", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const title = screen.getByRole("heading", { name: "QR-код: Alice" });
    const header = title.parentElement;
    expect(header).toHaveAttribute("data-slot", "dialog-header");
    expect(header?.children).toHaveLength(1);
    expect(screen.queryByText(/Отсканируйте/)).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
  });

  // A fixed 256 px box was what the dialog shipped with, and 256 px over
  // the 89 modules of a client config is under 3 px of pitch — too little
  // for a camera photographing a screen (T-ky6l).
  it("shows the symbol at the full dialog width, square and unconstrained", () => {
    render(
      <QrDialog clientId={1} clientName="Alice" onOpenChange={() => {}} />,
    );

    const qr = screen.getByRole("img", { name: "QR-код AmneziaWG клиента Alice" });

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

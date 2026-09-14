import { render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { toast } from "sonner";

import { Toaster } from "@/components/ui/sonner";

function stubMatchMedia(matches: boolean) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: query.includes("max-width") ? matches : false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

async function toasterRegion() {
  return waitFor(() => {
    const el = document.querySelector("[data-sonner-toaster]");
    expect(el).toBeTruthy();
    return el as HTMLElement;
  });
}

// Все тосты сверху справа на любой ширине (amnezia-vpn-server-1f31).
describe("Toaster position", () => {
  afterEach(() => {
    toast.dismiss();
  });

  it.each([true, false])("places toasts top-right (narrow viewport: %s)", async (narrow) => {
    stubMatchMedia(narrow);
    render(<Toaster />);
    toast("saved");

    const region = await toasterRegion();
    expect(region).toHaveAttribute("data-y-position", "top");
    expect(region).toHaveAttribute("data-x-position", "right");
  });
});

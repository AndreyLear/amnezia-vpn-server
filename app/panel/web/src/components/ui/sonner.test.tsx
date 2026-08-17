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

describe("Toaster position", () => {
  afterEach(() => {
    toast.dismiss();
  });

  it("places toasts at the top on max-sm viewports", async () => {
    stubMatchMedia(true);
    render(<Toaster />);
    toast("saved");

    expect(await toasterRegion()).toHaveAttribute("data-y-position", "top");
  });

  it("keeps the default Sonner y-position on sm+ viewports", async () => {
    stubMatchMedia(false);
    render(<Toaster />);
    toast("saved");

    expect(await toasterRegion()).not.toHaveAttribute("data-y-position", "top");
  });
});

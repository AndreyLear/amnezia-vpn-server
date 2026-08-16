import { afterEach, describe, expect, it } from "vitest";

import { applyTheme, getTheme } from "@/lib/theme";

describe("theme", () => {
  afterEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("defaults to dark", () => {
    expect(getTheme()).toBe("dark");
  });

  it("stores light|dark and toggles html.dark", () => {
    applyTheme("dark");
    expect(localStorage.getItem("amnezia-theme")).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);

    applyTheme("light");
    expect(localStorage.getItem("amnezia-theme")).toBe("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
  });

  it("maps unknown and system storage values to dark", () => {
    localStorage.setItem("amnezia-theme", "system");
    expect(getTheme()).toBe("dark");

    localStorage.setItem("amnezia-theme", "unknown");
    expect(getTheme()).toBe("dark");
  });
});

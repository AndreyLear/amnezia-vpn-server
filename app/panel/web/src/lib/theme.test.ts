import { afterEach, describe, expect, it } from "vitest";

import { applyTheme, getTheme } from "@/lib/theme";

describe("theme", () => {
  afterEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("defaults to system", () => {
    expect(getTheme()).toBe("system");
  });

  it("stores light|dark|system and toggles html.dark", () => {
    applyTheme("dark");
    expect(localStorage.getItem("amnezia-theme")).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);

    applyTheme("light");
    expect(localStorage.getItem("amnezia-theme")).toBe("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);

    applyTheme("system");
    expect(localStorage.getItem("amnezia-theme")).toBe("system");
  });
});

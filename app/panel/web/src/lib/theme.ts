const STORAGE_KEY = "amnezia-theme";

export type Theme = "light" | "dark";

export function getTheme(): Theme {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    if (value === "light" || value === "dark") {
      return value;
    }
  } catch {
    // ignore
  }
  return "dark";
}

export function applyTheme(theme: Theme) {
  try {
    localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // ignore
  }
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export function initTheme() {
  applyTheme(getTheme());
}

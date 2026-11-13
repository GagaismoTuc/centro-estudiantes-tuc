// Light/dark/system override (Modern Web Guidance `dark-mode-toggle`): "system" stores
// nothing and keeps `<meta name="color-scheme" content="light dark">`; an explicit choice
// is stored literally. public/theme.js applies the stored value before first paint.

export type Theme = "light" | "dark" | "system";

export const THEME_KEY = "color-scheme";

export function storedTheme(): Theme {
  try {
    const value = localStorage.getItem(THEME_KEY);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  let meta = document.querySelector<HTMLMetaElement>('meta[name="color-scheme"]');
  if (!meta) {
    meta = document.createElement("meta");
    meta.name = "color-scheme";
    document.head.append(meta);
  }
  meta.content = theme === "system" ? "light dark" : theme;
}

export function setTheme(theme: Theme): void {
  try {
    if (theme === "system") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, theme);
  } catch {
    // Storage can be blocked; the theme still applies for this page.
  }
  applyTheme(theme);
}

/** The scheme the OS asks for right now. */
export function systemScheme(): "light" | "dark" {
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

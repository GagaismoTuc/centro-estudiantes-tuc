// Light/dark/system override (Modern Web Guidance `dark-mode-toggle`): "system" stores
// nothing and keeps `<meta name="color-scheme" content="light dark">`; an explicit choice
// is stored literally. public/theme.js applies the stored value before first paint.

export type Theme = "light" | "dark" | "system";

/**
 * localStorage key of the override. public/theme.js is a classic script served as-is
 * (it must run before the bundle), so it cannot import this constant and repeats the
 * literal; App.test.tsx fails if the two ever differ.
 */
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

// One store for every ThemeSwitch on the page (sidebar and drawer never disagree).
const listeners = new Set<() => void>();
let current: Theme | null = null;

function notify(): void {
  for (const listener of listeners) listener();
}

function onStorage(event: StorageEvent): void {
  // Another tab changed the override.
  if (event.key !== THEME_KEY && event.key !== null) return;
  current = storedTheme();
  applyTheme(current);
  notify();
}

export function subscribeTheme(listener: () => void): () => void {
  if (listeners.size === 0) {
    window.addEventListener("storage", onStorage);
    applyTheme(currentTheme()); // same as public/theme.js; also creates a missing meta tag
  }
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      window.removeEventListener("storage", onStorage);
      current = null; // re-read storage on the next mount
    }
  };
}

export function currentTheme(): Theme {
  current ??= storedTheme();
  return current;
}

export function setTheme(theme: Theme): void {
  current = theme;
  notify();
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

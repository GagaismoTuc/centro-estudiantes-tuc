import { useEffect, useId, useState } from "react";
import { applyTheme, setTheme, storedTheme, systemScheme, THEME_KEY, type Theme } from "../lib/theme.ts";

const options: { value: Theme; label: string }[] = [
  { value: "light", label: "Claro" },
  { value: "dark", label: "Oscuro" },
  { value: "system", label: "Sistema" },
];

/** Three-state Claro/Oscuro/Sistema switch as a radio group (2–5 exclusive options). */
export function ThemeSwitch() {
  const [theme, setThemeState] = useState<Theme>(storedTheme);
  const [system, setSystem] = useState(systemScheme);
  // Unique per instance: the sidebar and the mobile drawer may both render a switch.
  const name = useId();

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  useEffect(() => {
    // Another tab changed the override, or the OS switched schemes.
    const onStorage = (event: StorageEvent) => {
      if (event.key === THEME_KEY || event.key === null) setThemeState(storedTheme());
    };
    window.addEventListener("storage", onStorage);
    const media = typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)") : null;
    const onMedia = () => setSystem(systemScheme());
    media?.addEventListener("change", onMedia);
    return () => {
      window.removeEventListener("storage", onStorage);
      media?.removeEventListener("change", onMedia);
    };
  }, []);

  function choose(next: Theme) {
    setTheme(next);
    setThemeState(next);
  }

  return (
    <fieldset className="segmented">
      <legend className="visually-hidden">Tema</legend>
      {options.map((option) => (
        <label key={option.value} className="segmented__option">
          <input
            type="radio"
            name={name}
            value={option.value}
            checked={theme === option.value}
            onChange={() => choose(option.value)}
          />
          <span>
            {option.label}
            {option.value === "system" && (
              <span className="visually-hidden"> (ahora {system === "dark" ? "oscuro" : "claro"})</span>
            )}
          </span>
        </label>
      ))}
    </fieldset>
  );
}

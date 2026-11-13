import { useEffect, useId, useState, useSyncExternalStore } from "react";
import { currentTheme, setTheme, subscribeTheme, systemScheme, type Theme } from "../lib/theme.ts";

const options: { value: Theme; label: string }[] = [
  { value: "light", label: "Claro" },
  { value: "dark", label: "Oscuro" },
  { value: "system", label: "Sistema" },
];

/** Three-state Claro/Oscuro/Sistema switch as a radio group (2–5 exclusive options). */
export function ThemeSwitch() {
  // Shared store: every instance (sidebar, drawer) shows the same choice.
  const theme = useSyncExternalStore(subscribeTheme, currentTheme);
  const [system, setSystem] = useState(systemScheme);
  // Unique per instance: the sidebar and the mobile drawer may both render a switch.
  const name = useId();

  useEffect(() => {
    // The OS switched schemes: only the "Sistema" hint changes.
    const media = typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)") : null;
    const onMedia = () => setSystem(systemScheme());
    media?.addEventListener("change", onMedia);
    return () => media?.removeEventListener("change", onMedia);
  }, []);

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
            onChange={() => setTheme(option.value)}
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

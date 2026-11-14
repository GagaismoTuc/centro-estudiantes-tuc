import { useMemo } from "react";
import { encodeQr } from "../lib/qr/encode.ts";

const QUIET_ZONE = 4; // modules of light margin that scanners need around the symbol

/** Draws text as a QR code in SVG: dark modules on a white field, also in dark mode, so phones can read it. */
export function QrCode({ text, label }: { text: string; label: string }) {
  const { size, path } = useMemo(() => {
    const { modules } = encodeQr(text);
    let d = "";
    modules.forEach((row, y) =>
      row.forEach((dark, x) => {
        if (dark) d += `M${x + QUIET_ZONE} ${y + QUIET_ZONE}h1v1h-1z`;
      }),
    );
    return { size: modules.length + QUIET_ZONE * 2, path: d };
  }, [text]);

  return (
    <svg className="qr" role="img" aria-label={label} viewBox={`0 0 ${size} ${size}`} shapeRendering="crispEdges">
      <rect width={size} height={size} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}

import { useMemo } from "react";
import { encodeQr } from "../lib/qr/encode.ts";

const QUIET_ZONE = 4; // modules of light margin that scanners need around the symbol

/** Draws text as a QR code in SVG: dark modules on a white field, also in dark mode, so phones can read it. */
export function QrCode({ text, label }: { text: string; label: string }) {
  const qr = useMemo(() => {
    try {
      const { modules } = encodeQr(text);
      let d = "";
      modules.forEach((row, y) =>
        row.forEach((dark, x) => {
          if (dark) d += `M${x + QUIET_ZONE} ${y + QUIET_ZONE}h1v1h-1z`;
        }),
      );
      return { size: modules.length + QUIET_ZONE * 2, path: d };
    } catch {
      return null;
    }
  }, [text]);

  if (!qr) return null;

  return (
    <svg className="qr" role="img" aria-label={label} viewBox={`0 0 ${qr.size} ${qr.size}`} shapeRendering="crispEdges">
      <rect width={qr.size} height={qr.size} fill="#fff" />
      <path d={qr.path} fill="#000" />
    </svg>
  );
}

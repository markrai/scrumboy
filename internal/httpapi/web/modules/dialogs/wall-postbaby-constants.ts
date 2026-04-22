// Postbaby-sourced constants for the Scrumbaby wall.
//
// Values are lifted directly from `postbaby-private/js/strings.js` and
// `postbaby-private/js/script.js` to preserve the exact feel of a Postbaby
// note layer. Do not tune by guess - match the reference.

// Desktop rainbow palette from strings.js (rainbowColors).
// Single-click on a note cycles through this list in order.
// Index 0 is the default color for newly created notes. Scrumbaby uses
// powder blue (#B0E0E6) instead of Postbaby's white so a fresh note reads
// as "Scrumbaby" on the dark wall surface.
export const RAINBOW_COLORS: readonly string[] = [
  "#B0E0E6",
  "#DC143C",
  "#FF7F00",
  "#FFBF00",
  "#AAFF00",
  "#89CFF0",
  "#CBC3E3",
  "#CF9FFF",
];

// Matches doubleTapThreshold in script.js.
export const DOUBLE_TAP_MS = 300;

// Matches dragThreshold in script.js (pixels of movement before drag starts).
export const DRAG_THRESHOLD_PX = 10;

// Per-note transient coalescing window (locked to the investigation plan
// §5.2 target of ~100ms between transient emits per note).
export const TRANSIENT_COALESCE_MS = 100;

// Default canvas size for a freshly created note (Postbaby-ish dimensions,
// clamped to the Scrumboy backend width/height limits elsewhere).
export const DEFAULT_NOTE_WIDTH = 200;
export const DEFAULT_NOTE_HEIGHT = 120;

export function normalizeHex(hex: string | null | undefined): string {
  if (!hex) return "";
  const s = String(hex).trim();
  if (!s) return "";
  return s.startsWith("#") ? s.toUpperCase() : ("#" + s).toUpperCase();
}

// Find the index of the provided color in RAINBOW_COLORS (case-insensitive).
// Returns 0 on miss so single-click still cycles predictably for notes whose
// stored color is not in the palette (the same spirit as Postbaby's
// `parseInt(div.dataset.colorIndex)` defaulting to 0 / NaN).
export function colorIndexFromHex(hex: string | null | undefined): number {
  const target = normalizeHex(hex);
  if (!target) return 0;
  for (let i = 0; i < RAINBOW_COLORS.length; i++) {
    if (RAINBOW_COLORS[i].toUpperCase() === target) return i;
  }
  return 0;
}

// Return the next palette color after `hex`. If the current color is not in
// the palette, we start at index 0 (matches Postbaby's wrap-around logic in
// script.js around line 1702 / 1819).
export function nextColor(hex: string | null | undefined): { color: string; index: number } {
  const idx = colorIndexFromHex(hex);
  const next = (idx + 1) % RAINBOW_COLORS.length;
  return { color: RAINBOW_COLORS[next], index: next };
}

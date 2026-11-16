// Pure colour math for tag pills (no DOM access, safe to import anywhere).

type Rgb = readonly [number, number, number];

function hexToRgb(hex: string): Rgb {
  return [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as unknown as Rgb;
}

function rgbToHex(rgb: Rgb): string {
  return "#" + rgb.map((v) => Math.round(v).toString(16).padStart(2, "0")).join("");
}

function mixRgb(a: Rgb, b: Rgb, shareOfA: number): Rgb {
  return [0, 1, 2].map((i) => a[i] * shareOfA + b[i] * (1 - shareOfA)) as unknown as Rgb;
}

function luminance(rgb: Rgb): number {
  const [r, g, b] = rgb.map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrastRatio(a: Rgb, b: Rgb): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

// Card surface and body text of each theme (see --card-bg / --panel and --text in styles.css).
const TAG_THEME_LIGHT = { surface: hexToRgb("#f5f3ef"), text: hexToRgb("#111827") } as const;
const TAG_THEME_DARK = { surface: hexToRgb("#111827"), text: hexToRgb("#e5e7eb") } as const;
const TAG_TINT_SHARE = 0.125; // pills use `background: <color>20` (0x20 / 255)
const TAG_MIN_CONTRAST = 4.5; // WCAG AA for normal text

function readableOn(color: Rgb, theme: { surface: Rgb; text: Rgb }): string {
  const pillBg = mixRgb(color, theme.surface, TAG_TINT_SHARE);
  // Keep as much of the tag colour as still reads: step toward the theme's text colour.
  for (let share = 1; share > 0; share -= 0.05) {
    const hex = rgbToHex(mixRgb(color, theme.text, share));
    // Test the rounded hex that will actually be rendered.
    if (contrastRatio(hexToRgb(hex), pillBg) >= TAG_MIN_CONTRAST) return hex;
  }
  return rgbToHex(theme.text);
}

/**
 * Text colour for a tag pill drawn in the tag's own colour. Returns a `light-dark()` value that
 * keeps the tag hue but darkens (light theme) or lightens (dark theme) it until it reaches AA
 * contrast on the pill's tinted background. Input must already be a sanitized `#rrggbb`.
 */
export function readableTagTextColor(safeHex: string): string {
  const color = hexToRgb(safeHex);
  return `light-dark(${readableOn(color, TAG_THEME_LIGHT)}, ${readableOn(color, TAG_THEME_DARK)})`;
}

type Rgb = readonly [number, number, number];

type TintContext = {
  readonly surface: Rgb;
  readonly tintShare: number;
};

type ThemeContexts = {
  readonly text: Rgb;
  readonly tints: readonly TintContext[];
};

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
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrastRatio(a: Rgb, b: Rgb): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

const STRONG_TINT_SHARE = 0x20 / 0xff;
const SUBTLE_TINT_SHARE = 0x0d / 0xff;
const MIN_CONTRAST = 4.5;

const LIGHT_THEME: ThemeContexts = {
  text: hexToRgb("#111827"),
  tints: [
    { surface: hexToRgb("#f7f7f4"), tintShare: STRONG_TINT_SHARE },
    { surface: hexToRgb("#f7f7f4"), tintShare: SUBTLE_TINT_SHARE },
    { surface: hexToRgb("#f5f3ef"), tintShare: STRONG_TINT_SHARE },
    { surface: hexToRgb("#f0ede8"), tintShare: STRONG_TINT_SHARE },
  ],
};

const DARK_THEME: ThemeContexts = {
  text: hexToRgb("#e5e7eb"),
  tints: [
    { surface: hexToRgb("#0b0f17"), tintShare: STRONG_TINT_SHARE },
    { surface: hexToRgb("#0b0f17"), tintShare: SUBTLE_TINT_SHARE },
    { surface: hexToRgb("#111827"), tintShare: STRONG_TINT_SHARE },
  ],
};

function readableOn(color: Rgb, theme: ThemeContexts): string {
  const pillBackgrounds = theme.tints.map(({ surface, tintShare }) => mixRgb(color, surface, tintShare));
  for (let colorPercent = 100; colorPercent >= 0; colorPercent -= 5) {
    const hex = rgbToHex(mixRgb(color, theme.text, colorPercent / 100));
    const candidate = hexToRgb(hex);
    if (pillBackgrounds.every((background) => contrastRatio(candidate, background) >= MIN_CONTRAST)) return hex;
  }
  return rgbToHex(theme.text);
}

/**
 * Returns theme-specific text colors with normal-text AA contrast on Scrumboy's supported
 * strong and subtle tinted pill backgrounds. Input must already be a sanitized `#rrggbb`.
 */
export function readableTagTextColor(safeHex: string): string {
  const color = hexToRgb(safeHex);
  return `light-dark(${readableOn(color, LIGHT_THEME)}, ${readableOn(color, DARK_THEME)})`;
}

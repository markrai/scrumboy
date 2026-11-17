import { describe, expect, it } from "vitest";
import { readableTagTextColor } from "./tag-text-color.js";

type Rgb = readonly [number, number, number];
type Theme = "light" | "dark";
type Context = { readonly name: string; readonly theme: Theme; readonly surface: string; readonly alpha: number };

const rgb = (hex: string): Rgb => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as Rgb;
const composite = (foreground: Rgb, background: Rgb, alpha: number): Rgb => (
  [0, 1, 2].map((i) => foreground[i] * alpha + background[i] * (1 - alpha)) as unknown as Rgb
);
const luminance = (color: Rgb): number => {
  const [r, g, b] = color.map((channel) => {
    const encoded = channel / 255;
    return encoded <= 0.04045 ? encoded / 12.92 : ((encoded + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (left: Rgb, right: Rgb): number => {
  const l1 = luminance(left);
  const l2 = luminance(right);
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
};

const contexts: readonly Context[] = [
  { name: "light background strong", theme: "light", surface: "#f7f7f4", alpha: 0x20 / 0xff },
  { name: "light background subtle", theme: "light", surface: "#f7f7f4", alpha: 0x0d / 0xff },
  { name: "light card", theme: "light", surface: "#f5f3ef", alpha: 0x20 / 0xff },
  { name: "light card hover", theme: "light", surface: "#f0ede8", alpha: 0x20 / 0xff },
  { name: "dark background strong", theme: "dark", surface: "#0b0f17", alpha: 0x20 / 0xff },
  { name: "dark background subtle", theme: "dark", surface: "#0b0f17", alpha: 0x0d / 0xff },
  { name: "dark panel", theme: "dark", surface: "#111827", alpha: 0x20 / 0xff },
];

function parse(value: string): { light: string; dark: string } {
  const m = /^light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\)$/.exec(value);
  expect(m, value).not.toBeNull();
  return { light: m![1], dark: m![2] };
}

describe("readableTagTextColor", () => {
  it("darkens pure yellow in the light theme and keeps it in the dark theme", () => {
    const { light, dark } = parse(readableTagTextColor("#ffff00"));
    expect(light).not.toBe("#ffff00");
    expect(dark).toBe("#ffff00");
  });

  it("lightens pure black in the dark theme and keeps it in the light theme", () => {
    const { light, dark } = parse(readableTagTextColor("#000000"));
    expect(light).toBe("#000000");
    expect(dark).not.toBe("#000000");
  });

  it("keeps a light blue unchanged in the dark theme", () => {
    expect(parse(readableTagTextColor("#60a5fa")).dark).toBe("#60a5fa");
  });

  it.each(["#ffff00", "#ffffff", "#000000", "#ff0000", "#cccccc", "#60a5fa", "#9ca3af", "#ef4444", "#64748b"])(
    "reaches normal-text AA for %s in every supported context",
    (hex) => {
      const result = parse(readableTagTextColor(hex));
      for (const context of contexts) {
        const background = composite(rgb(hex), rgb(context.surface), context.alpha);
        expect(contrast(rgb(result[context.theme]), background), `${hex} ${context.name}`).toBeGreaterThanOrEqual(4.5);
      }
    },
  );

  it("reaches normal-text AA across a representative colour cube", () => {
    const steps = [0, 51, 102, 153, 204, 255];
    for (const r of steps) {
      for (const g of steps) {
        for (const b of steps) {
          const hex = "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("");
          const result = parse(readableTagTextColor(hex));
          for (const context of contexts) {
            const background = composite(rgb(hex), rgb(context.surface), context.alpha);
            expect(contrast(rgb(result[context.theme]), background), `${hex} ${context.name}`).toBeGreaterThanOrEqual(4.5);
          }
        }
      }
    }
  });

  it("preserves already-readable colors and returns deterministic rounded hex values", () => {
    expect(parse(readableTagTextColor("#111827")).light).toBe("#111827");
    expect(parse(readableTagTextColor("#e5e7eb")).dark).toBe("#e5e7eb");
    const first = readableTagTextColor("#ff0000");
    expect(readableTagTextColor("#ff0000")).toBe(first);
    expect(first).toMatch(/^light-dark\(#[0-9a-f]{6}, #[0-9a-f]{6}\)$/);
  });
});

import { describe, expect, it } from "vitest";
import { contrastRatio, readableTagTextColor } from "./tag-text-color.js";

type Rgb = [number, number, number];

const rgb = (hex: string): Rgb => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as Rgb;
const mix = (a: Rgb, b: Rgb, shareOfA: number): Rgb => [0, 1, 2].map((i) => a[i] * shareOfA + b[i] * (1 - shareOfA)) as Rgb;

const LIGHT_SURFACE = rgb("#f5f3ef");
const DARK_SURFACE = rgb("#111827");

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

  it("reaches WCAG AA on the pill background across the colour cube, in both themes", () => {
    const steps = [0, 51, 102, 153, 204, 255];
    for (const r of steps) {
      for (const g of steps) {
        for (const b of steps) {
          const hex = "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("");
          const { light, dark } = parse(readableTagTextColor(hex));
          const base = rgb(hex);
          expect(contrastRatio(rgb(light), mix(base, LIGHT_SURFACE, 0.125)), `${hex} light`).toBeGreaterThanOrEqual(4.5);
          expect(contrastRatio(rgb(dark), mix(base, DARK_SURFACE, 0.125)), `${hex} dark`).toBeGreaterThanOrEqual(4.5);
        }
      }
    }
  });
});

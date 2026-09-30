// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  bindSettingsTabsFit,
  SETTINGS_TABS_MIN_SCALE,
  settingsTabsScaleForWidths,
  fitSettingsTabsNav,
} from "./settings-tabs-fit.js";

afterEach(() => {
  bindSettingsTabsFit(null);
  vi.restoreAllMocks();
});

describe("settings tabs fit", () => {
  it("keeps full size when the labels already fit", () => {
    expect(settingsTabsScaleForWidths(720, 600)).toBe(1);
  });

  it("scales the row down so full labels still fit in one row", () => {
    expect(settingsTabsScaleForWidths(480, 720)).toBeCloseTo(480 / 720);
  });

  it("does not scale below the minimum", () => {
    expect(settingsTabsScaleForWidths(50, 800)).toBe(SETTINGS_TABS_MIN_SCALE);
  });

  it("resets scale on mobile and skips shrink-to-fit", () => {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches: query !== "(min-width: 621px)",
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
        onchange: null,
      }),
    });

    const nav = document.createElement("div");
    nav.className = "settings-tabs-nav";
    Object.defineProperty(nav, "clientWidth", { configurable: true, value: 320 });
    Object.defineProperty(nav, "scrollWidth", { configurable: true, value: 900 });
    document.body.appendChild(nav);

    expect(fitSettingsTabsNav(nav)).toBe(1);
    expect(nav.style.getPropertyValue("--settings-tabs-scale")).toBe("1");
    nav.remove();
  });

  it("applies shrink-to-fit on desktop when labels overflow", () => {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches: query === "(min-width: 621px)",
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
        onchange: null,
      }),
    });

    const nav = document.createElement("div");
    nav.className = "settings-tabs-nav";
    Object.defineProperty(nav, "clientWidth", { configurable: true, value: 480 });
    Object.defineProperty(nav, "scrollWidth", { configurable: true, value: 720 });
    document.body.appendChild(nav);

    expect(fitSettingsTabsNav(nav)).toBeCloseTo(480 / 720);
    expect(nav.style.getPropertyValue("--settings-tabs-scale")).toBe(String(480 / 720));
    nav.remove();
  });
});

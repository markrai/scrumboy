const SCALE_PROPERTY = "--settings-tabs-scale";
export const SETTINGS_TABS_MIN_SCALE = 0.6;
const FIT_EPSILON = 1;
const DESKTOP_MIN_WIDTH_MQ = "(min-width: 621px)";

let settingsTabsResizeObserver: ResizeObserver | null = null;

function applyScale(nav: HTMLElement, scale: number): void {
  nav.style.setProperty(SCALE_PROPERTY, String(scale));
}

export function settingsTabsScaleForWidths(available: number, needed: number): number {
  if (available <= 0) return 1;
  if (needed <= available + FIT_EPSILON) return 1;
  return Math.max(SETTINGS_TABS_MIN_SCALE, available / needed);
}

function isDesktopSettingsTabsLayout(): boolean {
  return typeof window !== "undefined" && window.matchMedia(DESKTOP_MIN_WIDTH_MQ).matches;
}

export function fitSettingsTabsNav(nav: HTMLElement | null): number {
  if (!nav) return 1;
  if (!isDesktopSettingsTabsLayout()) {
    applyScale(nav, 1);
    return 1;
  }
  applyScale(nav, 1);
  const scale = settingsTabsScaleForWidths(nav.clientWidth, nav.scrollWidth);
  applyScale(nav, scale);
  return scale;
}

export function bindSettingsTabsFit(nav: HTMLElement | null): void {
  settingsTabsResizeObserver?.disconnect();
  settingsTabsResizeObserver = null;
  if (!nav) return;
  fitSettingsTabsNav(nav);
  const target = nav.parentElement;
  if (!target || typeof ResizeObserver === "undefined") return;
  settingsTabsResizeObserver = new ResizeObserver(() => {
    fitSettingsTabsNav(nav);
  });
  settingsTabsResizeObserver.observe(target);
}

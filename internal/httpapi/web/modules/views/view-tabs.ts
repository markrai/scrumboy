const SCALE_PROPERTY = '--view-tabs-scale';
export const VIEW_TABS_MIN_SCALE = 0.7;
const FIT_EPSILON = 1;

let viewTabsResizeObserver: ResizeObserver | null = null;

function applyScale(row: HTMLElement, scale: number): void {
  row.style.setProperty(SCALE_PROPERTY, String(scale));
}

export function viewTabsScaleForWidths(available: number, needed: number): number {
  if (available <= 0) return 1;
  if (needed <= available + FIT_EPSILON) return 1;
  return Math.max(VIEW_TABS_MIN_SCALE, available / needed);
}

export function fitViewTabsRow(row: HTMLElement | null): number {
  if (!row) return 1;
  applyScale(row, 1);
  const scale = viewTabsScaleForWidths(row.clientWidth, row.scrollWidth);
  applyScale(row, scale);
  return scale;
}

export function bindViewTabsFit(row: HTMLElement | null): void {
  viewTabsResizeObserver?.disconnect();
  viewTabsResizeObserver = null;
  if (!row) return;
  fitViewTabsRow(row);
  const target = row.parentElement;
  if (!target || typeof ResizeObserver === 'undefined') return;
  viewTabsResizeObserver = new ResizeObserver(() => {
    fitViewTabsRow(row);
  });
  viewTabsResizeObserver.observe(target);
}

/**
 * Generic open/close/position behavior for the search filter popover. Pure DOM
 * helpers with no listeners and no knowledge of which board owns the panel:
 * each board binds its own (scoped) event handling and calls these.
 */

export function closeFilterPopover(panel: HTMLElement, toggle: HTMLElement): void {
  panel.hidden = true;
  toggle.setAttribute('aria-expanded', 'false');
}

// The panel is `position: fixed` and positioned here (rather than via CSS
// `top/right` relative to `.search-input-wrapper`) because `.topbar` sets
// `overflow-y: hidden` for its mobile chip-wrapping layout; an absolutely
// positioned descendant would be clipped to the topbar's own box.
export function positionFilterPopover(panel: HTMLElement, toggle: HTMLElement): void {
  const toggleRect = toggle.getBoundingClientRect();
  const margin = 8;
  panel.style.top = `${toggleRect.bottom + 6}px`;
  // Measured after the panel is made visible so panel.offsetWidth is accurate.
  const panelWidth = panel.offsetWidth || 200;
  let left = toggleRect.right - panelWidth;
  left = Math.max(margin, Math.min(left, window.innerWidth - panelWidth - margin));
  panel.style.left = `${left}px`;
}

export function openFilterPopover(panel: HTMLElement, toggle: HTMLElement): void {
  panel.hidden = false;
  toggle.setAttribute('aria-expanded', 'true');
  positionFilterPopover(panel, toggle);
}

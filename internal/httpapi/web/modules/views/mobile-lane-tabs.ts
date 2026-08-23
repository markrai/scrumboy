import { escapeHTML, sanitizeHexColor } from "../utils.js";
import { AGENDA_COLUMN_KEY, agendaMobileTabInnerHtml } from "./board-agenda.js";

/** One workflow lane as used for mobile tab strip rendering. */
export type MobileLaneColumn = { key: string; title: string; color?: string; count?: number };

/**
 * Inline `style` attribute fragments for mobile tab buttons and drop zones (matches board template).
 * Empty when the lane has no valid workflow hex color (built-in CSS fallbacks apply).
 */
export function mobileLaneTabStyleAttrForHtml(c: { color?: string }): { tab: string; drop: string } {
  const safe = sanitizeHexColor(c.color);
  if (!safe) return { tab: "", drop: "" };
  const col = escapeHTML(safe);
  return {
    tab: ` style="--lane-color:${col};--lane-shadow:${col}d9;background:${col};color:#ffffff;"`,
    drop: ` style="--lane-color:${col};--lane-shadow:${col}d9;background:${col};"`,
  };
}

/** Apply workflow lane colors to live mobile tab / drop-zone nodes (in-place refresh). */
export function applyMobileLaneTabStyles(el: HTMLElement, c: { color?: string }, role: "tab" | "drop"): void {
  const safe = sanitizeHexColor(c.color);
  if (!safe) {
    el.removeAttribute("style");
    return;
  }
  if (role === "tab") {
    el.style.cssText = `--lane-color:${safe};--lane-shadow:${safe}d9;background:${safe};color:#ffffff;`;
  } else {
    el.style.cssText = `--lane-color:${safe};--lane-shadow:${safe}d9;background:${safe};`;
  }
}

export type BuildMobileTabsInnerHtmlOpts = {
  activeTabKey: string | null | undefined;
  /** Full visible label for the tab, e.g. "Doing 3". */
  laneLabel: (key: string) => string;
  /** Extra tabs (e.g. Agenda) with no Sortable drop zone. */
  extraTabs?: MobileLaneColumn[];
};

/**
 * HTML for `#mobileTabs` inner content: tab buttons plus `#mobileTabDropZones` (drop overlays).
 * Keys are escaped in attributes; `id="tab_drop_*"` uses raw keys (same as legacy template).
 */
export function buildMobileTabsInnerHtml(boardCols: MobileLaneColumn[], opts: BuildMobileTabsInnerHtmlOpts): string {
  const extra = opts.extraTabs ?? [];
  const tabs = [...extra, ...boardCols]
    .map((c) => {
      const { tab } = mobileLaneTabStyleAttrForHtml(c);
      const active = opts.activeTabKey === c.key ? "mobile-tab--active" : "";
      const dk = escapeHTML(c.key);
      const label = opts.laneLabel(c.key);
      const isAgenda = c.key === AGENDA_COLUMN_KEY;
      const inner = isAgenda ? agendaMobileTabInnerHtml(c.count ?? 0) : escapeHTML(label);
      const aria = isAgenda ? ` aria-label="${escapeHTML(label)}"` : "";
      return `<button class="mobile-tab ${active}" data-tab="${dk}"${tab}${aria}><span class="mobile-tab__text">${inner}</span></button>`;
    })
    .join("");
  const drops = boardCols
    .map((c) => {
      const { drop } = mobileLaneTabStyleAttrForHtml(c);
      const dk = escapeHTML(c.key);
      return `<div id="tab_drop_${c.key}" class="mobile-tab-drop" data-status="${dk}"${drop}></div>`;
    })
    .join("");
  return `${tabs}<div id="mobileTabDropZones">${drops}</div>`;
}

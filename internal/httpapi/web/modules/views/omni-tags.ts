/**
 * Shared Omni tag primitives for the member board and the public read-only
 * board: suggestion matching, Browse ranking scaffolding, pill and row markup,
 * and element-based candidate-rail behavior.
 *
 * Everything here is parameterized by the caller (tag limit, ordering, data
 * attributes, element IDs, elements), so the member and public boards keep
 * separate DOM hooks and separate controllers. Must not import member state,
 * member controllers, or anything that performs requests.
 */

import { escapeHTML, sanitizeHexColor } from '../utils.js';

/** At or below this width the Omni row shows mobile pills instead of the desktop rails. */
export const OMNI_MOBILE_MAX_WIDTH_PX = 767;

export type OmniTagLike = { readonly name: string };
export type OmniPillTag = { readonly name: string; readonly color?: string | null };

/** Case-insensitive name order with a case-sensitive tie-break, so ordering is deterministic. */
export function compareOmniTagNames(left: string, right: string): number {
  const a = left.toLowerCase();
  const b = right.toLowerCase();
  if (a !== b) return a < b ? -1 : 1;
  return left < right ? -1 : left > right ? 1 : 0;
}

/**
 * Suggestions for a typed query: exact match, then prefix, then substring,
 * each group in name order. Already applied tags are excluded, and nothing is
 * offered once `limit` tags are applied. Callers pass only selectable tags.
 */
export function matchOmniTagCandidates<T extends OmniTagLike>(
  query: string,
  tags: readonly T[],
  appliedTags: readonly string[],
  limit: number,
): T[] {
  const needle = query.trim().toLowerCase();
  if (!needle || appliedTags.length >= limit) return [];
  const applied = new Set(appliedTags.map((tag) => tag.toLocaleLowerCase()));
  return tags
    .filter((tag) => !applied.has(tag.name.toLocaleLowerCase()))
    .map((tag) => {
      const name = tag.name.toLowerCase();
      const rank = name === needle ? 0 : name.startsWith(needle) ? 1 : name.includes(needle) ? 2 : 3;
      return { tag, rank };
    })
    .filter((entry) => entry.rank < 3)
    .sort((a, b) => (a.rank !== b.rank ? a.rank - b.rank : compareOmniTagNames(a.tag.name, b.tag.name)))
    .map((entry) => entry.tag);
}

/**
 * Empty-search Browse candidates: selectable tags not yet pinned, ordered by
 * the caller's comparator, never mutating the input. Nothing once `limit` tags
 * are pinned.
 */
export function rankOmniBrowseCandidates<T extends OmniTagLike>(
  tags: readonly T[],
  pinnedTags: readonly string[],
  limit: number,
  compare: (left: T, right: T) => number,
): T[] {
  if (pinnedTags.length >= limit) return [];
  const pinned = new Set(pinnedTags.map((tag) => tag.toLocaleLowerCase()));
  return tags.filter((tag) => !pinned.has(tag.name.toLocaleLowerCase())).sort(compare);
}

/**
 * Pill text color: `accent` uses the tag color for text (member board);
 * `theme` keeps the theme foreground for contrast and uses the color only on
 * border and tint (public board).
 */
export type OmniPillTone = 'accent' | 'theme';

function pillStyle(color: string | null | undefined, subtle: boolean, tone: OmniPillTone): string {
  const safe = sanitizeHexColor(color ?? undefined);
  if (!safe) return '';
  const c = escapeHTML(safe);
  const text = tone === 'accent' ? c : 'var(--text)';
  return subtle
    ? ` style="border-color:${c}66;background:${c}0d;color:${text}"`
    : ` style="border-color:${c};background:${c}20;color:${text}"`;
}

export function buildOmniAppliedPillHtml(
  tag: OmniPillTag,
  opts: { clearAttr: string; clearLabel: string; tone: OmniPillTone },
): string {
  return `<span class="omni-tag-pill omni-tag-pill--applied"${pillStyle(tag.color, false, opts.tone)}>
        <span>${escapeHTML(tag.name)}</span>
        <button type="button" class="omni-tag-pill__clear" ${escapeHTML(opts.clearAttr)}="${escapeHTML(tag.name)}" aria-label="${escapeHTML(opts.clearLabel)}">×</button>
      </span>`;
}

export function buildOmniCandidatePillHtml(
  tag: OmniPillTag,
  opts: { attr: string; browse: boolean; tone: OmniPillTone },
): string {
  const kind = opts.browse ? 'omni-tag-pill--browse' : 'omni-tag-pill--suggestion';
  return `<button type="button" class="omni-tag-pill ${kind}" ${escapeHTML(opts.attr)}="${escapeHTML(tag.name)}"${pillStyle(tag.color, opts.browse, opts.tone)}>${escapeHTML(tag.name)}</button>`;
}

export type OmniRowContent = {
  readonly pinnedHtml: string;
  readonly candidatesHtml: string;
  readonly mobileHtml: string;
  readonly hasPins: boolean;
};

/**
 * Composes the three Omni regions. Desktop: pinned pills plus Browse (empty
 * search) or suggestions (typed search). Mobile: pinned pills plus typed
 * suggestions only, never empty-search Browse candidates.
 */
export function buildOmniRowContent(args: {
  applied: readonly OmniPillTag[];
  candidates: readonly OmniPillTag[];
  browse: boolean;
  attrs: { tag: string; clear: string };
  tone: OmniPillTone;
  clearLabel: (name: string) => string;
}): OmniRowContent {
  const pinnedHtml = args.applied
    .map((tag) => buildOmniAppliedPillHtml(tag, { clearAttr: args.attrs.clear, clearLabel: args.clearLabel(tag.name), tone: args.tone }))
    .join('');
  const candidatesHtml = args.candidates
    .map((tag) => buildOmniCandidatePillHtml(tag, { attr: args.attrs.tag, browse: args.browse, tone: args.tone }))
    .join('');
  return {
    pinnedHtml,
    candidatesHtml,
    mobileHtml: pinnedHtml + (args.browse ? '' : candidatesHtml),
    hasPins: args.applied.length > 0,
  };
}

export type OmniRowIds = {
  readonly pinned: string;
  readonly region: string;
  readonly previous: string;
  readonly viewport: string;
  readonly next: string;
  readonly mobile: string;
};

/** The Omni bar: pinned rail, candidate region with chevrons, and the mobile pill rail. */
export function buildOmniBarHtml(args: { ids: OmniRowIds; previousLabel: string; nextLabel: string; leadingHtml?: string }): string {
  const { ids } = args;
  const previousLabel = escapeHTML(args.previousLabel);
  const nextLabel = escapeHTML(args.nextLabel);
  return `<div class="omni-bar">
      ${args.leadingHtml ?? ''}
      <div class="omni-pinned-tags" id="${escapeHTML(ids.pinned)}"></div>
      <div class="omni-candidate-region" id="${escapeHTML(ids.region)}">
        <button type="button" class="omni-candidate-chevron omni-candidate-chevron--prev" id="${escapeHTML(ids.previous)}" aria-label="${previousLabel}" disabled aria-hidden="true">‹</button>
        <div class="omni-candidate-viewport" id="${escapeHTML(ids.viewport)}"></div>
        <button type="button" class="omni-candidate-chevron omni-candidate-chevron--next" id="${escapeHTML(ids.next)}" aria-label="${nextLabel}" disabled aria-hidden="true">›</button>
      </div>
      <div class="omni-mobile-tag-pills" id="${escapeHTML(ids.mobile)}"></div>
    </div>`;
}

// ---- Element-based rail behavior ----

export type OmniRowElements = {
  readonly pinned: HTMLElement;
  readonly region: HTMLElement;
  readonly viewport: HTMLElement;
  readonly previous: HTMLButtonElement | null;
  readonly next: HTMLButtonElement | null;
  readonly mobile: HTMLElement;
};

/** Looks up the row's regions inside `root` by the caller's IDs. */
export function findOmniRowElements(root: ParentNode, ids: OmniRowIds): OmniRowElements | null {
  const byId = (id: string) => root.querySelector<HTMLElement>(`[id="${id}"]`);
  const pinned = byId(ids.pinned);
  const region = byId(ids.region);
  const viewport = byId(ids.viewport);
  const mobile = byId(ids.mobile);
  if (!pinned || !region || !viewport || !mobile) return null;
  return {
    pinned,
    region,
    viewport,
    mobile,
    previous: byId(ids.previous) as HTMLButtonElement | null,
    next: byId(ids.next) as HTMLButtonElement | null,
  };
}

export function isOmniDesktopRail(): boolean {
  return !window.matchMedia(`(max-width: ${OMNI_MOBILE_MAX_WIDTH_PX}px)`).matches;
}

function setChevronInert(button: HTMLButtonElement | null, inert: boolean): void {
  if (!button) return;
  button.disabled = inert;
  button.setAttribute('aria-hidden', inert ? 'true' : 'false');
}

/** Enables a chevron only when there is more to scroll that way, and fades the overflowing edges. */
export function updateOmniChevrons(viewport: HTMLElement, previous: HTMLButtonElement | null, next: HTMLButtonElement | null): void {
  const overflows = viewport.scrollWidth > viewport.clientWidth + 1;
  const atStart = !overflows || viewport.scrollLeft <= 1;
  const atEnd = !overflows || viewport.scrollLeft + viewport.clientWidth >= viewport.scrollWidth - 1;
  setChevronInert(previous, atStart);
  setChevronInert(next, atEnd);
  viewport.classList.toggle('omni-candidate-viewport--fade-start', !atStart);
  viewport.classList.toggle('omni-candidate-viewport--fade-end', !atEnd);
}

export function pageOmniCandidates(viewport: HTMLElement, direction: -1 | 1): void {
  const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
  viewport.scrollBy({ left: direction * viewport.clientWidth, behavior: reduceMotion ? 'auto' : 'smooth' });
}

/** Desktop only: scrolls the pinned rail so the most recently pinned tag is visible. */
export function revealLastPinnedOmniPill(pinned: HTMLElement): void {
  if (!isOmniDesktopRail()) return;
  const overflow = pinned.scrollWidth - pinned.clientWidth;
  if (overflow <= 1) return;
  pinned.scrollLeft = overflow;
}

/** Desktop only: keeps a keyboard-focused pinned pill in view. */
export function revealFocusedPinnedOmniPill(pinned: HTMLElement, target: EventTarget | null): void {
  if (!(target instanceof Element) || !isOmniDesktopRail() || !pinned.contains(target)) return;
  const pill = target.closest('.omni-tag-pill--applied');
  if (pill instanceof HTMLElement) pill.scrollIntoView({ inline: 'nearest', block: 'nearest' });
}

const renderedHtml = new WeakMap<HTMLElement, string>();

function dataAttributes(el: Element): [string, string][] {
  return el.getAttributeNames().filter((name) => name.startsWith('data-')).map((name) => [name, el.getAttribute(name) ?? '']);
}

/**
 * Replaces a region's markup. With `preserveUnchanged`, identical markup is
 * left in place (keeping scroll and focus) and a focused control is re-focused
 * in the new markup when an element with the same data attributes exists.
 * Returns whether the region changed.
 */
function replaceRegion(el: HTMLElement, html: string, preserveUnchanged: boolean): boolean {
  if (preserveUnchanged && renderedHtml.get(el) === html) return false;
  const active = preserveUnchanged && document.activeElement instanceof Element && el.contains(document.activeElement)
    ? dataAttributes(document.activeElement)
    : null;
  el.innerHTML = html;
  renderedHtml.set(el, html);
  if (active && active.length > 0) {
    const match = Array.from(el.querySelectorAll<HTMLElement>('*'))
      .find((candidate) => active.every(([name, value]) => candidate.getAttribute(name) === value));
    match?.focus();
  }
  return true;
}

/**
 * Writes the Omni regions and syncs the rail state. By default every region is
 * rewritten and scroll positions reset (member board behavior). With
 * `preserveUnchanged`, unchanged regions keep their DOM, scroll, and focus.
 */
export function renderOmniRow(els: OmniRowElements, content: OmniRowContent, opts: { preserveUnchanged?: boolean } = {}): void {
  const preserve = opts.preserveUnchanged ?? false;
  const pinnedChanged = replaceRegion(els.pinned, content.pinnedHtml, preserve);
  const candidatesChanged = replaceRegion(els.viewport, content.candidatesHtml, preserve);
  if (replaceRegion(els.mobile, content.mobileHtml, preserve)) els.mobile.scrollLeft = 0;
  els.region.classList.toggle('omni-candidate-region--after-pins', content.hasPins);
  if (candidatesChanged) els.viewport.scrollLeft = 0;
  updateOmniChevrons(els.viewport, els.previous, els.next);
  if (pinnedChanged) revealLastPinnedOmniPill(els.pinned);
}

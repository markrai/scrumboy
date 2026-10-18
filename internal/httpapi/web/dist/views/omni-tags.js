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
import { compareTagNames, tagIdentityKey, tagLookupKey } from '../tag-names.js';
/** At or below this width the Omni row shows mobile pills instead of the desktop rails. */
export const OMNI_MOBILE_MAX_WIDTH_PX = 767;
/** Case-insensitive name order with a case-sensitive tie-break, so ordering is deterministic. */
export function compareOmniTagNames(left, right) {
    return compareTagNames(left, right);
}
/**
 * Suggestions for a typed query: exact match, then prefix, then substring,
 * each group in name order. Already applied tags are excluded, and nothing is
 * offered once `limit` tags are applied. Callers pass only selectable tags.
 */
export function matchOmniTagCandidates(query, tags, appliedTags, limit) {
    const needle = tagLookupKey(query.trim());
    if (!needle || appliedTags.length >= limit)
        return [];
    const applied = new Set(appliedTags.map(tagIdentityKey));
    return tags
        .filter((tag) => !applied.has(tagIdentityKey(tag.name)))
        .map((tag) => {
        const name = tagLookupKey(tag.name);
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
export function rankOmniBrowseCandidates(tags, pinnedTags, limit, compare) {
    if (pinnedTags.length >= limit)
        return [];
    const pinned = new Set(pinnedTags.map(tagIdentityKey));
    return tags.filter((tag) => !pinned.has(tagIdentityKey(tag.name))).sort(compare);
}
function pillStyle(color, subtle, tone) {
    const safe = sanitizeHexColor(color ?? undefined);
    if (!safe)
        return '';
    const c = escapeHTML(safe);
    const text = tone === 'accent' ? c : 'var(--text)';
    return subtle
        ? ` style="border-color:${c}66;background:${c}0d;color:${text}"`
        : ` style="border-color:${c};background:${c}20;color:${text}"`;
}
export function buildOmniAppliedPillHtml(tag, opts) {
    const clearAttr = tag.clearAttr ?? opts.clearAttr;
    const clearLabel = tag.clearLabel ?? opts.clearLabel;
    const value = tag.value ?? tag.name;
    const prefix = tag.prefix
        ? `<span class="omni-tag-pill__prefix">${escapeHTML(tag.prefix)}:</span>`
        : '';
    return `<span class="omni-tag-pill omni-tag-pill--applied"${pillStyle(tag.color, false, opts.tone)}>
        <span>${prefix}${escapeHTML(tag.name)}</span>
        <button type="button" class="omni-tag-pill__clear" ${escapeHTML(clearAttr)}="${escapeHTML(value)}" aria-label="${escapeHTML(clearLabel)}">×</button>
      </span>`;
}
export function buildOmniCandidatePillHtml(tag, opts) {
    const kind = opts.browse ? 'omni-tag-pill--browse' : 'omni-tag-pill--suggestion';
    const attr = tag.attr ?? opts.attr;
    const value = tag.value ?? tag.name;
    const prefix = tag.prefix
        ? `<span class="omni-tag-pill__prefix">${escapeHTML(tag.prefix)}:</span>`
        : '';
    return `<button type="button" class="omni-tag-pill ${kind}" ${escapeHTML(attr)}="${escapeHTML(value)}"${pillStyle(tag.color, opts.browse, opts.tone)}>${prefix}${escapeHTML(tag.name)}</button>`;
}
/**
 * Composes the three Omni regions. Desktop: pinned pills plus Browse (empty
 * search) or suggestions (typed search). Mobile: pinned pills plus typed
 * suggestions only, never empty-search Browse candidates.
 */
export function buildOmniRowContent(args) {
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
/** The Omni bar: pinned rail, candidate region with chevrons, and the mobile pill rail. */
export function buildOmniBarHtml(args) {
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
/** Looks up the row's regions inside `root` by the caller's IDs. */
export function findOmniRowElements(root, ids) {
    const byId = (id) => root.querySelector(`[id="${id}"]`);
    const pinned = byId(ids.pinned);
    const region = byId(ids.region);
    const viewport = byId(ids.viewport);
    const mobile = byId(ids.mobile);
    if (!pinned || !region || !viewport || !mobile)
        return null;
    return {
        pinned,
        region,
        viewport,
        mobile,
        previous: byId(ids.previous),
        next: byId(ids.next),
    };
}
export function isOmniDesktopRail() {
    return !window.matchMedia(`(max-width: ${OMNI_MOBILE_MAX_WIDTH_PX}px)`).matches;
}
function setChevronInert(button, inert) {
    if (!button)
        return;
    button.disabled = inert;
    button.setAttribute('aria-hidden', inert ? 'true' : 'false');
}
/** Enables a chevron only when there is more to scroll that way, and fades the overflowing edges. */
export function updateOmniChevrons(viewport, previous, next) {
    const overflows = viewport.scrollWidth > viewport.clientWidth + 1;
    const atStart = !overflows || viewport.scrollLeft <= 1;
    const atEnd = !overflows || viewport.scrollLeft + viewport.clientWidth >= viewport.scrollWidth - 1;
    setChevronInert(previous, atStart);
    setChevronInert(next, atEnd);
    viewport.classList.toggle('omni-candidate-viewport--fade-start', !atStart);
    viewport.classList.toggle('omni-candidate-viewport--fade-end', !atEnd);
}
export function pageOmniCandidates(viewport, direction) {
    const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
    viewport.scrollBy({ left: direction * viewport.clientWidth, behavior: reduceMotion ? 'auto' : 'smooth' });
}
/** Desktop only: scrolls the pinned rail so the most recently pinned tag is visible. */
export function revealLastPinnedOmniPill(pinned) {
    if (!isOmniDesktopRail())
        return;
    const overflow = pinned.scrollWidth - pinned.clientWidth;
    if (overflow <= 1)
        return;
    pinned.scrollLeft = overflow;
}
/** Desktop only: keeps a keyboard-focused pinned pill in view. */
export function revealFocusedPinnedOmniPill(pinned, target) {
    if (!(target instanceof Element) || !isOmniDesktopRail() || !pinned.contains(target))
        return;
    const pill = target.closest('.omni-tag-pill--applied');
    if (pill instanceof HTMLElement)
        pill.scrollIntoView({ inline: 'nearest', block: 'nearest' });
}
const renderedHtml = new WeakMap();
function dataAttributes(el) {
    return el.getAttributeNames().filter((name) => name.startsWith('data-')).map((name) => [name, el.getAttribute(name) ?? '']);
}
/**
 * Replaces a region's markup. With `preserveUnchanged`, identical markup is
 * left in place (keeping scroll and focus) and a focused control is re-focused
 * in the new markup when an element with the same data attributes exists.
 * Returns whether the region changed.
 */
function replaceRegion(el, html, preserveUnchanged) {
    if (preserveUnchanged && renderedHtml.get(el) === html)
        return false;
    const active = preserveUnchanged && document.activeElement instanceof Element && el.contains(document.activeElement)
        ? dataAttributes(document.activeElement)
        : null;
    el.innerHTML = html;
    renderedHtml.set(el, html);
    if (active && active.length > 0) {
        const match = Array.from(el.querySelectorAll('*'))
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
export function renderOmniRow(els, content, opts = {}) {
    const preserve = opts.preserveUnchanged ?? false;
    const pinnedChanged = replaceRegion(els.pinned, content.pinnedHtml, preserve);
    const candidatesChanged = replaceRegion(els.viewport, content.candidatesHtml, preserve);
    if (replaceRegion(els.mobile, content.mobileHtml, preserve))
        els.mobile.scrollLeft = 0;
    els.region.classList.toggle('omni-candidate-region--after-pins', content.hasPins);
    if (candidatesChanged)
        els.viewport.scrollLeft = 0;
    updateOmniChevrons(els.viewport, els.previous, els.next);
    if (pinnedChanged)
        revealLastPinnedOmniPill(els.pinned);
}

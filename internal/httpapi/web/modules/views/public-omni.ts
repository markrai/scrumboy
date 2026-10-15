/**
 * Public board Omni tag controller. Binds only to one public Omni row (looked
 * up inside its root by public IDs, matching only data-public-omni-* hooks),
 * and every listener and observer it creates is released when `signal` aborts.
 * Query changes go through the callbacks; this module performs no requests and
 * never touches the member board's Omni IDs, attributes, or controllers.
 */

import type { PublicTagSummary } from '../public-board-api.js';
import {
  findOmniRowElements,
  pageOmniCandidates,
  renderOmniRow,
  revealFocusedPinnedOmniPill,
  revealLastPinnedOmniPill,
  updateOmniChevrons,
} from './omni-tags.js';
import { buildPublicOmniContent, PUBLIC_OMNI_CLEAR_ATTR, PUBLIC_OMNI_IDS, PUBLIC_OMNI_TAG_ATTR } from './public-board-rendering.js';

export type PublicOmniState = {
  readonly tags: readonly PublicTagSummary[];
  readonly selected: readonly string[];
  /** The live search input value, which may run ahead of the debounced query. */
  readonly input: string;
  /** Label of an active sprint-number deep link, pinned so it can be cleared. */
  readonly sprintLabel?: string | null;
};

export type PublicOmniController = {
  /** Re-renders changed regions only, keeping scroll and focus where content is unchanged. */
  render(): void;
};

export function bindPublicOmni(opts: {
  root: HTMLElement;
  signal: AbortSignal;
  getState: () => PublicOmniState;
  onAdd: (tag: string) => void;
  onRemove: (tag: string) => void;
}): PublicOmniController | null {
  const els = findOmniRowElements(opts.root, PUBLIC_OMNI_IDS);
  if (!els) return null;
  const { signal } = opts;
  const syncRails = () => {
    updateOmniChevrons(els.viewport, els.previous, els.next);
    revealLastPinnedOmniPill(els.pinned);
  };

  opts.root.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    const clear = target.closest(`[${PUBLIC_OMNI_CLEAR_ATTR}]`);
    if (clear) {
      opts.onRemove(clear.getAttribute(PUBLIC_OMNI_CLEAR_ATTR) ?? '');
      return;
    }
    const tag = target.closest(`[${PUBLIC_OMNI_TAG_ATTR}]`);
    if (tag) {
      opts.onAdd(tag.getAttribute(PUBLIC_OMNI_TAG_ATTR) ?? '');
      return;
    }
    if (els.previous?.contains(target)) pageOmniCandidates(els.viewport, -1);
    else if (els.next?.contains(target)) pageOmniCandidates(els.viewport, 1);
  }, { signal });
  els.viewport.addEventListener('scroll', () => updateOmniChevrons(els.viewport, els.previous, els.next), { signal, passive: true });
  els.pinned.addEventListener('focusin', (event) => revealFocusedPinnedOmniPill(els.pinned, event.target), { signal });
  window.addEventListener('resize', syncRails, { signal });
  if (typeof ResizeObserver !== 'undefined') {
    const observer = new ResizeObserver(syncRails);
    observer.observe(els.viewport);
    observer.observe(els.pinned);
    signal.addEventListener('abort', () => observer.disconnect(), { once: true });
  }

  const render = () => {
    if (signal.aborted) return;
    renderOmniRow(els, buildPublicOmniContent(opts.getState()), { preserveUnchanged: true });
  };
  render();
  return { render };
}

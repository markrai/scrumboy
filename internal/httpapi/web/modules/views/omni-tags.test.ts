// @vitest-environment happy-dom
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  buildOmniBarHtml,
  buildOmniRowContent,
  compareOmniTagNames,
  findOmniRowElements,
  matchOmniTagCandidates,
  rankOmniBrowseCandidates,
  renderOmniRow,
  updateOmniChevrons,
  type OmniRowIds,
} from './omni-tags.js';

const IDS: OmniRowIds = { pinned: 'p', region: 'r', previous: 'prev', viewport: 'v', next: 'next', mobile: 'm' };
const hostile = '"><img src=x onerror=alert(1)>';

function mountRow(): ReturnType<typeof findOmniRowElements> & object {
  document.body.innerHTML = `<div class="filters--omni">${buildOmniBarHtml({ ids: IDS, previousLabel: 'Prev', nextLabel: 'Next' })}</div>`;
  return findOmniRowElements(document, IDS)!;
}

describe('shared Omni matching and ranking', () => {
  const tags = [{ name: 'xandroid' }, { name: 'Android' }, { name: 'android-ui' }, { name: 'ANDROID-tv' }, { name: 'ios' }];

  it('ranks exact, prefix, then substring with deterministic name tie-breaks', () => {
    expect(matchOmniTagCandidates('android', tags, [], 20).map((t) => t.name)).toEqual(['Android', 'ANDROID-tv', 'android-ui', 'xandroid']);
    expect(matchOmniTagCandidates('  ', tags, [], 20)).toEqual([]);
    expect(compareOmniTagNames('a', 'A')).toBeGreaterThan(0);
    expect(compareOmniTagNames('Beta', 'alpha')).toBeGreaterThan(0);
  });

  it('excludes applied tags case-insensitively', () => {
    expect(matchOmniTagCandidates('android', tags, ['ANDROID', 'Android-UI'], 20).map((t) => t.name)).toEqual(['ANDROID-tv', 'xandroid']);
  });

  it('enforces the caller tag limit for both the member (20) and public (10) boards', () => {
    const applied = (n: number) => Array.from({ length: n }, (_, i) => `tag-${i}`);
    for (const limit of [20, 10]) {
      expect(matchOmniTagCandidates('ios', tags, applied(limit), limit)).toEqual([]);
      expect(matchOmniTagCandidates('ios', tags, applied(limit - 1), limit).map((t) => t.name)).toEqual(['ios']);
      expect(rankOmniBrowseCandidates(tags, applied(limit), limit, (a, b) => compareOmniTagNames(a.name, b.name))).toEqual([]);
      expect(rankOmniBrowseCandidates(tags, applied(limit - 1), limit, (a, b) => compareOmniTagNames(a.name, b.name))).toHaveLength(tags.length);
    }
  });

  it('Browse uses the caller comparator without mutating input', () => {
    const counted = [{ name: 'b', n: 1 }, { name: 'a', n: 3 }, { name: 'c', n: 3 }];
    const order = counted.map((t) => t.name);
    expect(rankOmniBrowseCandidates(counted, ['C'], 10, (x, y) => y.n - x.n || compareOmniTagNames(x.name, y.name)).map((t) => t.name)).toEqual(['a', 'b']);
    expect(counted.map((t) => t.name)).toEqual(order);
  });
});

describe('shared Omni markup', () => {
  it('builds pills with caller attributes, escaping, and accent or theme text', () => {
    const accent = buildOmniRowContent({
      applied: [{ name: hostile, color: '#123456' }],
      candidates: [{ name: 'ok', color: 'red;}' }],
      browse: false,
      attrs: { tag: 'data-x-tag', clear: 'data-x-clear' },
      tone: 'accent',
      clearLabel: (name) => `Remove ${name}`,
    });
    const root = document.createElement('div');
    root.innerHTML = accent.pinnedHtml + accent.candidatesHtml;
    expect(root.querySelector('img')).toBeNull();
    expect(root.querySelector('[data-x-clear]')?.getAttribute('data-x-clear')).toBe(hostile);
    expect(root.querySelector('[data-x-clear]')?.getAttribute('aria-label')).toBe(`Remove ${hostile}`);
    expect(root.querySelector('.omni-tag-pill--applied')?.getAttribute('style')).toBe('border-color:#123456;background:#12345620;color:#123456');
    expect(root.querySelector('[data-x-tag="ok"]')?.hasAttribute('style')).toBe(false);

    const theme = buildOmniRowContent({
      applied: [],
      candidates: [{ name: 'ok', color: '#123456' }],
      browse: true,
      attrs: { tag: 'data-y-tag', clear: 'data-y-clear' },
      tone: 'theme',
      clearLabel: () => '',
    });
    expect(theme.candidatesHtml).toContain('style="border-color:#12345666;background:#1234560d;color:var(--text)"');
    expect(theme.candidatesHtml).toContain('omni-tag-pill--browse');
    expect(theme.mobileHtml).toBe('');
    expect(theme.hasPins).toBe(false);
  });

  it('supports mixed pill types with safe values, labels, and compact prefixes', () => {
    const mixed = buildOmniRowContent({
      applied: [{
        name: 'October Sprint',
        value: '12',
        prefix: 'Sprint',
        clearAttr: 'data-clear-sprint',
        clearLabel: 'All sprints',
      }],
      candidates: [{
        name: hostile,
        value: '13',
        prefix: 'Sprint',
        attr: 'data-sprint',
      }],
      browse: false,
      attrs: { tag: 'data-tag', clear: 'data-clear-tag' },
      tone: 'accent',
      clearLabel: () => 'Clear tag',
    });
    const root = document.createElement('div');
    root.innerHTML = mixed.pinnedHtml + mixed.candidatesHtml;

    expect(root.querySelector('[data-clear-sprint]')?.getAttribute('data-clear-sprint')).toBe('12');
    expect(root.querySelector('[data-clear-sprint]')?.getAttribute('aria-label')).toBe('All sprints');
    expect(root.querySelector('[data-sprint]')?.getAttribute('data-sprint')).toBe('13');
    expect(root.querySelectorAll('.omni-tag-pill__prefix')).toHaveLength(2);
    expect(root.querySelector('img')).toBeNull();
  });

  it('builds the bar with the caller ids and inert chevrons', () => {
    const els = mountRow();
    expect(els.previous?.disabled).toBe(true);
    expect(els.next?.getAttribute('aria-hidden')).toBe('true');
    expect(document.querySelector('[id^="omni"]')).toBeNull();
  });
});

describe('shared Omni rail rendering', () => {
  const content = (pins: string[], candidates: string[]) => buildOmniRowContent({
    applied: pins.map((name) => ({ name })),
    candidates: candidates.map((name) => ({ name })),
    browse: false,
    attrs: { tag: 'data-x-tag', clear: 'data-x-clear' },
    tone: 'theme',
    clearLabel: () => 'x',
  });

  it('by default rewrites every region and resets scroll (member behavior)', () => {
    const els = mountRow();
    renderOmniRow(els, content(['a'], ['b']));
    const first = els.viewport.firstElementChild;
    els.viewport.scrollLeft = 40;
    renderOmniRow(els, content(['a'], ['b']));
    expect(els.viewport.firstElementChild).not.toBe(first);
    expect(els.viewport.scrollLeft).toBe(0);
    expect(els.region.classList.contains('omni-candidate-region--after-pins')).toBe(true);
  });

  it('with preserveUnchanged keeps unchanged regions, their scroll, and focus', () => {
    const els = mountRow();
    renderOmniRow(els, content(['a'], ['b', 'c']), { preserveUnchanged: true });
    const candidate = els.viewport.querySelector<HTMLButtonElement>('[data-x-tag="c"]')!;
    candidate.focus();
    Object.defineProperty(els.viewport, 'scrollWidth', { configurable: true, value: 400 });
    Object.defineProperty(els.viewport, 'clientWidth', { configurable: true, value: 100 });
    els.viewport.scrollLeft = 50;
    renderOmniRow(els, content(['a'], ['b', 'c']), { preserveUnchanged: true });
    expect(els.viewport.querySelector('[data-x-tag="c"]')).toBe(candidate);
    expect(els.viewport.scrollLeft).toBe(50);
    expect(document.activeElement).toBe(candidate);

    // Changed content: replaced and scroll reset, but a still-present focused control is re-focused.
    renderOmniRow(els, content(['a'], ['b', 'c', 'd']), { preserveUnchanged: true });
    expect(els.viewport.querySelector('[data-x-tag="c"]')).not.toBe(candidate);
    expect(document.activeElement).toBe(els.viewport.querySelector('[data-x-tag="c"]'));
    expect(els.viewport.scrollLeft).toBe(0);
  });

  it('enables chevrons only toward overflow and fades the overflowing edges', () => {
    const els = mountRow();
    Object.defineProperty(els.viewport, 'scrollWidth', { configurable: true, value: 300 });
    Object.defineProperty(els.viewport, 'clientWidth', { configurable: true, value: 100 });
    els.viewport.scrollLeft = 0;
    updateOmniChevrons(els.viewport, els.previous, els.next);
    expect(els.previous?.disabled).toBe(true);
    expect(els.next?.disabled).toBe(false);
    expect(els.viewport.classList.contains('omni-candidate-viewport--fade-end')).toBe(true);
    els.viewport.scrollLeft = 200;
    updateOmniChevrons(els.viewport, els.previous, els.next);
    expect(els.previous?.disabled).toBe(false);
    expect(els.next?.disabled).toBe(true);
    expect(els.viewport.classList.contains('omni-candidate-viewport--fade-start')).toBe(true);
  });
});

describe('Omni module boundaries', () => {
  function importsOf(file: string): string[] {
    const source = readFileSync(resolve(process.cwd(), 'modules/views', file), 'utf8');
    return Array.from(source.matchAll(/from\s+'([^']+)'/g)).map((m) => m[1]);
  }

  it('the shared module stays dependency-light', () => {
    expect(importsOf('omni-tags.ts')).toEqual(['../utils.js', '../tag-names.js']);
  });

  it('public modules import no member board controllers, URL state, or authenticated API plumbing', () => {
    const member = ['./board-filters.js', './board-rendering.js', './board.js', '../state/board-filter-url.js', '../api.js'];
    for (const file of ['public-omni.ts', 'public-board.ts', 'public-board-rendering.ts']) {
      for (const forbidden of member) expect(importsOf(file), `${file} -> ${forbidden}`).not.toContain(forbidden);
    }
    // The public Omni modules also stay off app state entirely.
    for (const file of ['public-omni.ts', 'public-board-rendering.ts']) {
      for (const forbidden of ['../state/selectors.js', '../state/mutations.js']) expect(importsOf(file), `${file} -> ${forbidden}`).not.toContain(forbidden);
    }
  });
});

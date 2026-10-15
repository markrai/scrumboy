// @vitest-environment happy-dom
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../i18n/index.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../i18n/index.js')>()),
  t: (key: string) => key,
}));

import { normalizePublicBoardSnapshot, EMPTY_PUBLIC_QUERY } from '../public-board-api.js';
import {
  buildPublicBoardHtml,
  buildPublicOmniContent,
  buildPublicUnavailableHtml,
  comparePublicBrowseTags,
  type PublicBoardViewModel,
  type PublicLaneState,
} from './public-board-rendering.js';

const hostile = '"><img src=x onerror=alert(1)><script>bad()</script>';

function view(overrides: Partial<PublicBoardViewModel> = {}): PublicBoardViewModel {
  const snapshot = normalizePublicBoardSnapshot({
    access: { kind: 'public', readOnly: true },
    project: { slug: 'ignite', name: hostile, dominantColor: '#123456', estimationMode: 'MODIFIED_FIBONACCI', sprintsEnabled: true },
    workflow: [{ key: 'backlog', name: hostile, color: 'javascript:alert(1)', isDone: false, position: 0 }],
    priorities: [{ key: 'p"x', name: hostile, color: '#ff0000', position: 0 }],
    tags: [{ name: hostile, color: 'url(evil)', activeCount: 1 }],
    columns: { backlog: [{ localId: 5, title: hostile, body: hostile, columnKey: 'backlog', estimationPoints: 8, priorityKey: 'p"x', tags: [{ name: hostile, color: '#00ff00' }] }] },
    columnsMeta: { backlog: { hasMore: true, nextCursor: 'c1', totalCount: 9 } },
  }, 'ignite');
  const lanes: Record<string, PublicLaneState> = { backlog: { ...snapshot.columnsMeta.backlog, loading: false, error: false } };
  return {
    snapshot,
    columns: snapshot.columns,
    lanes,
    query: EMPTY_PUBLIC_QUERY,
    sprints: [{ number: 2, name: hostile, state: 'ACTIVE' }],
    activeMobileTab: 'backlog',
    isMobile: false,
    user: null,
    canSignIn: false,
    ...overrides,
  };
}

function render(model: PublicBoardViewModel): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = buildPublicBoardHtml(model);
  return root;
}

describe('public board rendering', () => {
  it('escapes every user-controlled value and drops unsafe colors', () => {
    const root = render(view());
    expect(root.querySelector('img[src="x"], script')).toBeNull();
    expect(root.innerHTML).not.toContain('javascript:');
    expect(root.innerHTML).not.toContain('url(evil)');
    expect(root.querySelector('.card__title')?.textContent).toBe(hostile);
    expect(root.querySelector('[data-public-local-id="5"]')).not.toBeNull();
  });

  it('keeps tag accents on border and tint but uses the theme foreground for tag text', () => {
    const root = render(view());
    const tagStyle = root.querySelector('.card .tag')!.getAttribute('style') ?? '';
    expect(tagStyle).toContain('border-color: #00ff00');
    expect(tagStyle).toContain('background: #00ff0020');
    expect(tagStyle).toContain('color: var(--text)');
    expect(tagStyle).not.toMatch(/(^|[^-])color: #00ff00/);
    // Priority badges keep accent-colored text (shared member style string).
    const priority = root.querySelector<HTMLElement>('.card__priority')!;
    expect(priority.getAttribute('style')).toBe('border-color:#ff0000; background:#ff000020; color:#ff0000;');
    expect(priority.style.color).toBe('#ff0000');
  });

  it('renders a stable Omni shell with public-only ids and no static chips or All chip', () => {
    const root = render(view());
    const row = root.querySelector('.container > [data-public-omni].filters--omni')!;
    expect(row).not.toBeNull();
    for (const id of ['publicOmniPinnedTags', 'publicOmniCandidateRegion', 'publicOmniCandidatePrev', 'publicOmniCandidateViewport', 'publicOmniCandidateNext', 'publicOmniMobileTagPills']) {
      expect(row.querySelector(`#${id}`), id).not.toBeNull();
    }
    expect(root.querySelector('[id^="omni"], [data-omni-tag], [data-omni-clear-tag], .chip, [data-public-tag], #publicTagChips, .public-board-filters')).toBeNull();
  });

  it('escapes hostile tag names, drops unsafe colors, and keeps theme text for contrast', () => {
    const tags = [
      { name: hostile, color: 'url(evil)', activeCount: 3 },
      { name: 'blue', color: '#3b82f6', activeCount: 2 },
    ];
    const root = document.createElement('div');
    const content = buildPublicOmniContent({ tags, selected: ['blue'], input: '' });
    root.innerHTML = content.pinnedHtml + content.candidatesHtml;
    expect(root.querySelector('img, script')).toBeNull();
    const browse = root.querySelector<HTMLButtonElement>('[data-public-omni-tag]')!;
    expect(browse.getAttribute('data-public-omni-tag')).toBe(hostile);
    expect(browse.textContent).toBe(hostile);
    expect(browse.classList.contains('omni-tag-pill--browse')).toBe(true);
    expect(browse.hasAttribute('style')).toBe(false);
    expect(content.candidatesHtml).not.toContain('url(evil)');
    const pinned = root.querySelector('.omni-tag-pill--applied')!;
    const style = pinned.getAttribute('style') ?? '';
    expect(style).toContain('border-color:#3b82f6');
    expect(style).toContain('background:#3b82f620');
    expect(style).toContain('color:var(--text)');
    expect(style).not.toMatch(/(^|[^-])color:#3b82f6/);
    expect(pinned.querySelector('[data-public-omni-clear-tag="blue"]')?.getAttribute('aria-label')).toBe('board.filters.clearTag');
    // Typed suggestions use the strong tint, still with theme text.
    const typed = buildPublicOmniContent({ tags, selected: [], input: 'blu' });
    expect(typed.candidatesHtml).toContain('omni-tag-pill--suggestion');
    expect(typed.candidatesHtml).toContain('color:var(--text)');
  });

  it('orders public Browse by active count, then case-insensitive name, without recency', () => {
    const tags = [
      { name: 'zeta', color: null, activeCount: 2 },
      { name: 'Alpha', color: null, activeCount: 2 },
      { name: 'beta', color: null, activeCount: 5 },
      { name: 'idle', color: null, activeCount: 0 },
      { name: 'alpha2', color: null, activeCount: 1 },
    ];
    const root = document.createElement('div');
    root.innerHTML = buildPublicOmniContent({ tags, selected: [], input: '' }).candidatesHtml;
    expect(Array.from(root.querySelectorAll('[data-public-omni-tag]')).map((el) => el.textContent)).toEqual(['beta', 'Alpha', 'zeta', 'alpha2']);
    expect([...tags].sort(comparePublicBrowseTags).map((tag) => tag.name)).toEqual(['beta', 'Alpha', 'zeta', 'alpha2', 'idle']);
    // Typed: exact, prefix, substring; selected tags excluded.
    root.innerHTML = buildPublicOmniContent({ tags, selected: ['alpha2'], input: 'ALPHA' }).candidatesHtml;
    expect(Array.from(root.querySelectorAll('[data-public-omni-tag]')).map((el) => el.textContent)).toEqual(['Alpha']);
  });

  it('keeps removed URL tags removable and stops offering tags at the ten-tag limit', () => {
    const tags = Array.from({ length: 12 }, (_, i) => ({ name: `t${i}`, color: null, activeCount: 1 }));
    const gone = buildPublicOmniContent({ tags, selected: ['Vanished'], input: '' });
    expect(gone.pinnedHtml).toContain('data-public-omni-clear-tag="Vanished"');
    expect(gone.hasPins).toBe(true);
    const full = buildPublicOmniContent({ tags, selected: tags.slice(0, 10).map((tag) => tag.name), input: '' });
    expect(full.candidatesHtml).toBe('');
    expect(buildPublicOmniContent({ tags, selected: tags.slice(0, 10).map((tag) => tag.name), input: 't1' }).candidatesHtml).toBe('');
    expect(buildPublicOmniContent({ tags, selected: tags.slice(0, 9).map((tag) => tag.name), input: '' }).candidatesHtml).toContain('t10');
  });

  it('mobile pills show pinned tags plus typed suggestions, never empty-search Browse', () => {
    const tags = [{ name: 'api', color: null, activeCount: 1 }, { name: 'apex', color: null, activeCount: 1 }];
    const browse = buildPublicOmniContent({ tags, selected: ['api'], input: '' });
    expect(browse.mobileHtml).toBe(browse.pinnedHtml);
    expect(browse.candidatesHtml).toContain('apex');
    const typed = buildPublicOmniContent({ tags, selected: ['api'], input: 'ap' });
    expect(typed.mobileHtml).toBe(typed.pinnedHtml + typed.candidatesHtml);
    expect(typed.mobileHtml).toContain('data-public-omni-tag="apex"');
  });

  it('renders cards as keyboard-focusable read-only buttons with no member or drag hooks', () => {
    const root = render(view());
    const card = root.querySelector<HTMLButtonElement>('[data-public-local-id="5"]')!;
    expect(card.tagName).toBe('BUTTON');
    expect(card.type).toBe('button');
    expect(card.hasAttribute('draggable')).toBe(false);
    for (const attr of ['data-todo-id', 'data-todo-local-id', 'data-assignee-user-id', 'id']) {
      expect(card.hasAttribute(attr), attr).toBe(false);
    }
    expect(root.querySelector('.card__drag-handle, .todo-avatar, [data-status], #mobileTabDropZones')).toBeNull();
    // Pagination indicator plus load more via the opaque cursor path.
    expect(root.querySelector('[data-public-load-more="backlog"]')).not.toBeNull();
    expect(root.querySelector('.col__count')?.textContent).toBe('9');
  });

  it('shows the view-only indicator with an accessible label', () => {
    const root = render(view());
    const badge = root.querySelector('.public-board-badge')!;
    expect(badge.getAttribute('role')).toBe('note');
    expect(badge.getAttribute('aria-label')).toBe('publicBoard.badgeAria');
    expect(badge.textContent).toContain('publicBoard.badge');
  });

  it('topbar shows the project name first with no Scrumboy logo, keeping search, sign-in, badge, and filters', () => {
    const root = render(view({ canSignIn: true }));
    const topbar = root.querySelector('.topbar')!;
    expect(topbar.querySelector('img, .brand-text, #publicBrandLink')).toBeNull();
    expect(topbar.innerHTML).not.toContain('scrumboytext.png');
    const name = topbar.firstElementChild!;
    expect(name.classList.contains('public-board__project-name')).toBe(true);
    expect(name.textContent).toBe(hostile);
    expect(topbar.querySelectorAll('.brand')).toHaveLength(1);
    expect(topbar.querySelector('.public-board-badge')).not.toBeNull();
    expect(topbar.querySelector('#publicSearchInput')).not.toBeNull();
    expect(topbar.querySelector('#publicSignInBtn')).not.toBeNull();
    // Mobile keeps the priority popover inside the search control.
    const mobile = render(view({ isMobile: true }));
    const wrapper = mobile.querySelector('.topbar .search-input-wrapper')!;
    expect(wrapper.querySelector('#publicSearchInput')).not.toBeNull();
    expect(wrapper.querySelector<HTMLButtonElement>('#publicFilterToggle')?.hidden).toBe(false);
    expect(wrapper.querySelector<HTMLElement>('#publicFilterPanel')?.hidden).toBe(true);
    expect(mobile.querySelector('.public-board-badge')).not.toBeNull();
  });

  it('offers only supported filters: text, tags, named priorities; never a Sprint section', () => {
    const root = render(view());
    expect(root.querySelector('#publicSearchInput')).not.toBeNull();
    expect(root.querySelector('[data-public-omni]')).not.toBeNull();
    const priorityOptions = Array.from(root.querySelectorAll('[data-public-priority-option]'));
    expect(priorityOptions.map((o) => o.getAttribute('data-public-priority-option'))).toEqual(['', 'p"x']);
    expect(priorityOptions[1].textContent).toBe(hostile);
    expect(priorityOptions[1].getAttribute('style')).toContain('border-color: #ff0000');
    const panel = root.querySelector('#publicFilterPanel')!;
    expect(panel.querySelectorAll('.search-filter-panel__section')).toHaveLength(1);
    expect(panel.textContent).not.toContain('board.filters.sprint');
    // No member-only values, controls, or the member panel's hooks.
    expect(root.innerHTML).not.toContain('scheduled');
    expect(root.innerHTML).not.toContain('**none**');
    expect(root.querySelector([
      '[data-sort-option]', '[data-assignee-option]', '[data-sprint-option]', '[data-priority-option]',
      '[data-public-sprint-option]', '[data-public-sprint-section]',
      '#assigneeFilter', '#searchFilterToggle', '#searchFilterPanel', '#searchInput', 'select',
      '#publicSprintFilter', '#publicPriorityFilter', '.public-board-filter',
    ].join(', '))).toBeNull();
  });

  it('never renders a Sprint section for zero, one, or many sprints, signed in or not', () => {
    const many = Array.from({ length: 25 }, (_, i) => ({ number: i + 1, name: `Sprint ${i + 1}`, state: 'PLANNED' }));
    const user = { id: 7, name: 'Visitor', email: 'v@example.com' } as PublicBoardViewModel['user'];
    const panels = [[], many.slice(0, 1), many].flatMap((sprints) => [
      render(view({ sprints, user: null, canSignIn: true })),
      render(view({ sprints, user, canSignIn: false })),
    ]).map((root) => root.querySelector('#publicFilterPanel')!.innerHTML);
    for (const html of panels) {
      expect(html).not.toContain('board.filters.sprint');
      expect(html).not.toContain('sprint-option');
      expect(html).toBe(panels[0]);
    }
  });

  it('marks the selected priority as active and pulses the toggle only for priority', () => {
    const idle = render(view());
    expect(idle.querySelector('#publicFilterToggle')?.classList.contains('search-filter-toggle--active')).toBe(false);
    expect(idle.querySelector('[data-public-priority-option=""]')?.getAttribute('aria-pressed')).toBe('true');

    // A sprint deep link is shown as a pinned pill, not as a popover filter.
    const sprintOnly = render(view({ query: { ...EMPTY_PUBLIC_QUERY, sprintNumber: 2 } }));
    expect(sprintOnly.querySelector('#publicFilterToggle')?.classList.contains('search-filter-toggle--active')).toBe(false);

    const root = render(view({ query: { ...EMPTY_PUBLIC_QUERY, sprintNumber: 2, priority: 'p"x' } }));
    const toggle = root.querySelector('#publicFilterToggle')!;
    expect(toggle.classList.contains('search-filter-toggle--active')).toBe(true);
    expect(toggle.getAttribute('aria-controls')).toBe('publicFilterPanel');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    const priority = Array.from(root.querySelectorAll('[data-public-priority-option]')).find((o) => o.getAttribute('data-public-priority-option') === 'p"x')!;
    expect(priority.classList.contains('is-active')).toBe(true);
    expect(priority.getAttribute('aria-pressed')).toBe('true');
    expect(root.querySelector('[data-public-priority-option=""]')?.getAttribute('aria-pressed')).toBe('false');
  });

  it('hides the toggle when the board has no priority tiers, whatever the sprints', () => {
    const base = view();
    const noTiers = { ...base.snapshot, priorities: [] };
    for (const sprintsEnabled of [true, false]) {
      const none = render({ ...base, snapshot: { ...noTiers, project: { ...noTiers.project, sprintsEnabled } } });
      expect(none.querySelector('[data-public-priority-option], .search-filter-panel__section')).toBeNull();
      expect(none.querySelector<HTMLButtonElement>('#publicFilterToggle')?.hidden).toBe(true);
      expect(none.querySelector('#publicSearchInput')).not.toBeNull();
    }
  });

  it('pins a removable, escaped pill for a sprint-number deep link ahead of tag pills', () => {
    const tags = [{ name: 'blue', color: '#3b82f6', activeCount: 1 }];
    const without = buildPublicOmniContent({ tags, selected: [], input: '' });
    expect(without.pinnedHtml).not.toContain('data-public-clear-sprint');
    expect(without.hasPins).toBe(false);

    const content = buildPublicOmniContent({ tags, selected: ['blue'], input: '', sprintLabel: hostile });
    expect(content.hasPins).toBe(true);
    const root = document.createElement('div');
    root.innerHTML = content.pinnedHtml;
    expect(root.querySelector('img, script')).toBeNull();
    const pills = root.querySelectorAll('.omni-tag-pill--applied');
    expect(pills).toHaveLength(2);
    expect(pills[0].textContent).toContain(`board.filters.sprint: ${hostile}`);
    const clear = pills[0].querySelector('[data-public-clear-sprint]')!;
    expect(clear.getAttribute('aria-label')).toBe('board.filters.allSprints');
    expect(clear.hasAttribute('data-public-omni-clear-tag')).toBe(false);
    expect(pills[1].querySelector('[data-public-omni-clear-tag="blue"]')).not.toBeNull();
    // Mobile shows the same pinned sprint pill.
    expect(content.mobileHtml.startsWith(content.pinnedHtml)).toBe(true);
  });

  it('renders loading, error, exhausted, and empty states', () => {
    const base = view();
    const loading = render({ ...base, lanes: { backlog: { ...base.lanes.backlog, loading: true } } });
    expect(loading.querySelector('[data-public-load-more-state="loading"]')?.getAttribute('role')).toBe('status');
    const failed = render({ ...base, lanes: { backlog: { ...base.lanes.backlog, error: true } } });
    expect(failed.querySelector('[data-public-load-more="backlog"]')?.textContent).toBe('board.loadMoreFailed');
    const exhausted = render({ ...base, lanes: { backlog: { hasMore: false, nextCursor: null, totalCount: 1, loading: false, error: false } } });
    expect(exhausted.querySelector('[data-public-load-more]')).toBeNull();
    const empty = render({
      ...base,
      columns: { backlog: [] },
      lanes: { backlog: { hasMore: false, nextCursor: null, totalCount: 0, loading: false, error: false } },
      query: { ...EMPTY_PUBLIC_QUERY, search: 'none' },
    });
    expect(empty.querySelector('.public-board__no-results')?.getAttribute('role')).toBe('status');
    expect(empty.querySelector('.public-board__empty-lane')).not.toBeNull();
  });

  it('keeps mobile lane tabs as real buttons with accessible state', () => {
    const root = render(view({ isMobile: true }));
    const tab = root.querySelector<HTMLButtonElement>('[data-public-tab="backlog"]')!;
    expect(tab.type).toBe('button');
    expect(tab.getAttribute('aria-selected')).toBe('true');
    expect(root.querySelector('.col--mobile-active')).not.toBeNull();
    expect(root.querySelector('button.public-board__load-more-mobile')).not.toBeNull();
  });

  it('renders a generic unavailable state with no board content', () => {
    const root = document.createElement('div');
    root.innerHTML = buildPublicUnavailableHtml();
    expect(root.textContent).toContain('publicBoard.unavailable.title');
    expect(root.querySelector('[data-public-local-id], [data-public-board]')).toBeNull();
  });

  it('mobile hides only the badge, keeping search and its filter popover, on the public board page', () => {
    const css = readFileSync(resolve(process.cwd(), 'styles.css'), 'utf8');
    const section = css.slice(css.indexOf('PUBLIC READ-ONLY BOARD'));
    const media = section.slice(section.indexOf('@media (max-width: 620px)'));
    expect(media).toMatch(/\.page--public-board \.public-board-badge\s*\{\s*display: none;/);
    // Tags, search, the filter popover, and lane tabs are never hidden on the public board.
    expect(media).not.toMatch(/(chips|search-input|search-filter|mobile-tab)[^{}]*\{[^}]*display:\s*none/);
    // The removed dropdown and static tag-row styles are gone.
    expect(css).not.toMatch(/public-board-filter|public-board-sr-only/);
    // Every public mobile rule is scoped to the public page; nothing can reach
    // the member board's topbar or filters (.filters, .brand, #sprintFilter...).
    const selectors = Array.from(media.matchAll(/([^{}]+)\{[^{}]*\}/g)).map((m) => m[1].replace(/\/\*[\s\S]*?\*\//g, '').trim());
    expect(selectors.length).toBeGreaterThan(0);
    for (const group of selectors) {
      for (const sel of group.split(',')) expect(sel.trim()).toMatch(/^\.page--public-board /);
    }
    // Public-only classes are emitted only by the public renderer.
    const memberSource = readFileSync(resolve(process.cwd(), 'modules/views/board-rendering.ts'), 'utf8')
      + readFileSync(resolve(process.cwd(), 'modules/views/board.ts'), 'utf8');
    for (const cls of ['page--public-board', 'publicFilterToggle', 'publicFilterPanel', 'data-public-sprint-option', 'data-public-priority-option', 'publicOmni', 'data-public-omni', 'public-board-badge', 'public-board__project-name']) {
      expect(memberSource).not.toContain(cls);
    }
  });

  it('hidden search controls stay hidden despite their display rules, with no duplicate native clear', () => {
    const css = readFileSync(resolve(process.cwd(), 'styles.css'), 'utf8');
    // .search-filter-toggle and .search-clear set display:flex, which beats the UA [hidden] rule.
    expect(css).toMatch(/\.search-filter-toggle\[hidden\],[^{]*\.search-clear\[hidden\]\s*\{\s*display: none;/);
    expect(css).toMatch(/\.page--public-board #publicSearchInput::-webkit-search-cancel-button\s*\{[^}]*display: none;/);
  });

  it('public styles use theme variables only, so light and dark themes both apply', () => {
    // Vitest runs from internal/httpapi/web (scripts/run-vitest.mjs).
    const css = readFileSync(resolve(process.cwd(), 'styles.css'), 'utf8');
    const section = css.slice(css.indexOf('PUBLIC READ-ONLY BOARD'));
    expect(section.length).toBeGreaterThan(100);
    expect(section).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(section).toMatch(/var\(--muted\)/);
    expect(css).toContain('#publicTodoDialog .todo-markdown-preview');
  });
});

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
  buildPublicUnavailableHtml,
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

  it('offers only supported filters: text, tags, sprint number, priority', () => {
    const root = render(view());
    expect(root.querySelector('#publicSearchInput')).not.toBeNull();
    expect(root.querySelector('[data-public-tag=""]')).not.toBeNull();
    expect((root.querySelector('#publicSprintFilter option[value="2"]') as HTMLOptionElement).textContent).toBe(hostile);
    expect(root.querySelector('#publicPriorityFilter')).not.toBeNull();
    expect(root.querySelector('[data-sort-option], [data-assignee-option], #assigneeFilter')).toBeNull();
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

// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';

const selectorState: {
  board: Board | null;
  slug: string | null;
  tag: string;
  search: string;
  tagColors: Record<string, string>;
} = {
  board: null,
  slug: null,
  tag: '',
  search: '',
  tagColors: {},
};

vi.mock('../state/selectors.js', () => ({
  getAssigneeFromUrl: () => new URL(window.location.href).searchParams.get('assignee'),
  getSortFromUrl: () => new URL(window.location.href).searchParams.get('sort'),
  getPriorityFromUrl: () => new URL(window.location.href).searchParams.get('priority'),
  getBoard: () => selectorState.board,
  getSearch: () => selectorState.search,
  getSlug: () => selectorState.slug,
  getSprintIdFromUrl: () => new URL(window.location.href).searchParams.get('sprintId'),
  getTag: () => selectorState.tag,
  getTagColors: () => selectorState.tagColors,
  getUser: () => null,
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  sanitizeHexColor: (color?: string) => {
    if (!color || typeof color !== 'string') return null;
    return /^#[0-9a-fA-F]{6}$/.test(color.trim()) ? color.trim() : null;
  },
  isAnonymousBoard: (board: Board | null) => !!(board?.project && board.project.expiresAt != null && board.project.creatorUserId == null),
  isTemporaryBoard: (board: Board | null) => !!board?.project?.expiresAt,
  renderAvatarContent: () => '',
  renderUserAvatar: () => '',
}));

function makeBoard(): Board {
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 1,
    },
    tags: [
      { name: 'bug', count: 2, color: '#ff0000' },
      { name: 'feature', count: 1, color: '#00ff00' },
    ],
    columns: {
      backlog: [],
      not_started: [],
      doing: [],
      testing: [],
      done: [],
    },
  };
}

function renderFilterShell(): void {
  document.body.innerHTML = `
    <div class="filters">
      <div id="tagChips"></div>
      <div id="chipsNav">
        <button type="button" class="chips-nav__prev">Prev</button>
        <button type="button" class="chips-nav__next">Next</button>
      </div>
    </div>
    <div class="search-input-wrapper">
      <input id="searchInput" type="text" />
    </div>
    <div id="omniTagPills"></div>
  `;
}

function setDesktopMatchMedia(): void {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

async function loadBoardFiltersModules() {
  const boardFilters = await import('./board-filters.js');
  const events = await import('../events.ts');
  return { boardFilters, events };
}

async function setupBoardFiltersState(url: string, opts?: {
  tag?: string;
  search?: string;
  board?: Board;
  layout?: 'omni' | 'legacy';
}) {
  vi.resetModules();
  localStorage.setItem('scrumboy.boardFilterLayout', opts?.layout ?? 'legacy');
  window.history.replaceState({}, '', url);
  renderFilterShell();
  setDesktopMatchMedia();

  const { boardFilters, events } = await loadBoardFiltersModules();
  const board = opts?.board ?? makeBoard();
  selectorState.board = board;
  selectorState.slug = 'alpha';
  selectorState.tag = opts?.tag ?? '';
  selectorState.search = opts?.search ?? '';
  selectorState.tagColors = { bug: '#ff0000', feature: '#00ff00' };

  return { boardFilters, events, board };
}

describe('board-filters', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.resetModules();
    document.body.innerHTML = '';
    window.history.replaceState({}, '', '/');
    selectorState.board = null;
    selectorState.slug = null;
    selectorState.tag = '';
    selectorState.search = '';
    selectorState.tagColors = {};
    localStorage.clear();
  });

  it('computes stable chips html when board filter inputs do not change', async () => {
    const { boardFilters, board } = await setupBoardFiltersState('/alpha');

    const first = boardFilters.computeBoardChipsRender(board, '', null);
    const second = boardFilters.computeBoardChipsRender(board, '', null);

    expect(first.chipsHTML).toContain('data-tag="bug"');
    expect(first.chipsUnchanged).toBe(false);
    expect(second.chipsHTML).toBe(first.chipsHTML);
    expect(second.chipsUnchanged).toBe(true);
  });

  it('does not render cached sprint chips or selection for a disabled board', async () => {
    const board = makeBoard();
    board.project.sprintsEnabled = false;
    const { boardFilters } = await setupBoardFiltersState('/alpha?sprintId=7', { board });
    boardFilters.setSprintChipDataForSlug('alpha', {
      sprints: [{ id: 12, number: 7, name: 'Sprint 7', state: 'ACTIVE' }],
    });

    const rendered = boardFilters.computeBoardChipsRender(board, '', '7');

    expect(rendered.chipsHTML).not.toContain('data-sprint-id');
    expect(rendered.chipsHTML).not.toContain('Sprint 7');
  });

  it('non-additive tag chip click clears sprint filter and reloads with the selected tag', async () => {
    const { boardFilters, board } = await setupBoardFiltersState(
      '/alpha?search=query&sprintId=7',
      { search: 'query' },
    );
    const reloadBoard = vi.fn().mockResolvedValue(undefined);

    boardFilters.setSprintChipDataForSlug('alpha', {
      sprints: [{ id: 12, number: 7, name: 'Sprint 7', state: 'PLANNED' }],
    });
    const rendered = boardFilters.computeBoardChipsRender(board, '', '7');
    const tagChips = document.getElementById('tagChips');
    if (!tagChips) throw new Error('missing tagChips test node');
    tagChips.innerHTML = rendered.chipsHTML;

    boardFilters.bindBoardFilterUi({
      reloadBoard,
      showError: vi.fn(),
    });

    const bugChip = tagChips.querySelector('[data-tag="bug"]');
    if (!(bugChip instanceof HTMLElement)) throw new Error('missing bug chip');
    bugChip.dispatchEvent(new MouseEvent('click', { bubbles: true }));

    const params = new URL(window.location.href).searchParams;
    expect(params.get('tag')).toBe('bug');
    expect(params.get('sprintId')).toBeNull();
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(reloadBoard).toHaveBeenCalledWith('alpha', 'bug', 'query', null, null, null, null);
  });

  it('additive tag chip click preserves the existing sprint filter', async () => {
    const { boardFilters, board } = await setupBoardFiltersState(
      '/alpha?search=query&sprintId=7',
      { search: 'query' },
    );
    const reloadBoard = vi.fn().mockResolvedValue(undefined);

    boardFilters.setSprintChipDataForSlug('alpha', {
      sprints: [{ id: 12, number: 7, name: 'Sprint 7', state: 'PLANNED' }],
    });
    const rendered = boardFilters.computeBoardChipsRender(board, '', '7');
    const tagChips = document.getElementById('tagChips');
    if (!tagChips) throw new Error('missing tagChips test node');
    tagChips.innerHTML = rendered.chipsHTML;

    boardFilters.bindBoardFilterUi({
      reloadBoard,
      showError: vi.fn(),
    });

    const featureChip = tagChips.querySelector('[data-tag="feature"]');
    if (!(featureChip instanceof HTMLElement)) throw new Error('missing feature chip');
    featureChip.dispatchEvent(new MouseEvent('click', { bubbles: true, ctrlKey: true }));

    const params = new URL(window.location.href).searchParams;
    expect(params.get('tag')).toBe('feature');
    expect(params.get('sprintId')).toBe('7');
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(reloadBoard).toHaveBeenCalledWith('alpha', 'feature', 'query', '7', null, null, null);
  });

  it('non-additive sprint chip click clears the tag filter and reloads with the sprint number', async () => {
    const { boardFilters, board } = await setupBoardFiltersState(
      '/alpha?tag=bug&search=query',
      { tag: 'bug', search: 'query' },
    );
    const reloadBoard = vi.fn().mockResolvedValue(undefined);

    boardFilters.setSprintChipDataForSlug('alpha', {
      sprints: [{ id: 12, number: 3, name: 'Sprint 3', state: 'PLANNED' }],
    });
    const rendered = boardFilters.computeBoardChipsRender(board, 'bug', null);
    const tagChips = document.getElementById('tagChips');
    if (!tagChips) throw new Error('missing tagChips test node');
    tagChips.innerHTML = rendered.chipsHTML;

    boardFilters.bindBoardFilterUi({
      reloadBoard,
      showError: vi.fn(),
    });

    const sprintChip = tagChips.querySelector('[data-sprint-id="3"]');
    if (!(sprintChip instanceof HTMLElement)) throw new Error('missing sprint chip');
    sprintChip.dispatchEvent(new MouseEvent('click', { bubbles: true }));

    const params = new URL(window.location.href).searchParams;
    expect(params.get('tag')).toBeNull();
    expect(params.get('sprintId')).toBe('3');
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(reloadBoard).toHaveBeenCalledWith('alpha', '', 'query', '3', null, null, null);
  });

  it('search input debounces reloads, trims the search value, and clear removes the filter', async () => {
    const { boardFilters } = await setupBoardFiltersState(
      '/alpha?tag=bug&sprintId=7',
      { tag: 'bug', search: '' },
    );
    const reloadBoard = vi.fn().mockResolvedValue(undefined);

    boardFilters.bindBoardFilterUi({
      reloadBoard,
      showError: vi.fn(),
    });

    const searchInput = document.getElementById('searchInput') as HTMLInputElement | null;
    if (!searchInput) throw new Error('missing searchInput');

    searchInput.value = '  login  ';
    searchInput.dispatchEvent(new Event('input', { bubbles: true }));

    expect(document.getElementById('searchClear')).not.toBeNull();
    expect(reloadBoard).not.toHaveBeenCalled();

    vi.advanceTimersByTime(299);
    expect(reloadBoard).not.toHaveBeenCalled();

    vi.advanceTimersByTime(1);
    expect(new URL(window.location.href).searchParams.get('search')).toBe('login');
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(reloadBoard).toHaveBeenLastCalledWith('alpha', 'bug', 'login', '7', null, null, null);

    const clearBtn = document.getElementById('searchClear');
    if (!(clearBtn instanceof HTMLElement)) throw new Error('missing search clear button');
    clearBtn.click();

    expect(searchInput.value).toBe('');
    expect(new URL(window.location.href).searchParams.get('search')).toBeNull();
    expect(reloadBoard).toHaveBeenCalledTimes(2);
    expect(reloadBoard).toHaveBeenLastCalledWith('alpha', 'bug', null, '7', null, null, null);
  });

  it('search clear cancels a debounce that has not fired and preserves tag and sprint', async () => {
    const { boardFilters } = await setupBoardFiltersState('/alpha?tag=bug&sprintId=7', {
      tag: 'bug',
      layout: 'omni',
    });
    const reloadBoard = vi.fn().mockResolvedValue(undefined);
    boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });
    const input = document.getElementById('searchInput') as HTMLInputElement;
    input.value = 'stale';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    (document.getElementById('searchClear') as HTMLButtonElement).click();

    vi.advanceTimersByTime(300);
    expect(new URL(window.location.href).searchParams.get('search')).toBeNull();
    expect(new URL(window.location.href).searchParams.get('tag')).toBe('bug');
    expect(new URL(window.location.href).searchParams.get('sprintId')).toBe('7');
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(reloadBoard).toHaveBeenCalledWith('alpha', 'bug', null, '7', null, null, null);
  });

  it('ranks Omni suggestions exact, prefix, then substring case-insensitively and omits inactive/applied tags', async () => {
    const { boardFilters } = await setupBoardFiltersState('/alpha?tag=android-ui', {
      tag: 'android-ui',
      layout: 'omni',
    });
    const tags = [
      { name: 'xandroid', count: 1 },
      { name: 'Android', count: 2 },
      { name: 'android-ui', count: 1 },
      { name: 'andr-legacy', count: 0 },
    ];

    expect(boardFilters.matchOmniTags('ANDROID', tags, 'android-ui').map((tag: { name: string }) => tag.name))
      .toEqual(['Android', 'xandroid']);
    expect(boardFilters.matchOmniTags('', tags, '')).toEqual([]);
  });

  it('Omni suggestion consumes search, preserves other filters, and cancels the pending debounce', async () => {
    const board = makeBoard();
    board.tags = [
      { name: 'android', count: 2, color: '#00ff00' },
      { name: 'android-ui', count: 1 },
      { name: 'ancient', count: 0 },
    ];
    const { boardFilters } = await setupBoardFiltersState(
      '/alpha?sprintId=7&assignee=me&sort=newest&priority=high',
      { board, layout: 'omni' },
    );
    const reloadBoard = vi.fn().mockResolvedValue(undefined);
    boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

    const input = document.getElementById('searchInput') as HTMLInputElement;
    input.value = 'andr';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(Array.from(document.querySelectorAll('[data-omni-tag]')).map((el) => el.textContent))
      .toEqual(['android', 'android-ui']);

    (document.querySelector('[data-omni-tag="android"]') as HTMLButtonElement).click();
    expect(input.value).toBe('');
    const params = new URL(window.location.href).searchParams;
    expect(params.get('search')).toBeNull();
    expect(params.get('tag')).toBe('android');
    expect(params.get('sprintId')).toBe('7');
    expect(params.get('assignee')).toBe('me');
    expect(params.get('sort')).toBe('newest');
    expect(params.get('priority')).toBe('high');
    expect(reloadBoard).toHaveBeenCalledWith('alpha', 'android', null, '7', 'me', 'newest', 'high');

    vi.advanceTimersByTime(300);
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(new URL(window.location.href).searchParams.get('search')).toBeNull();

    input.value = 'crash';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    vi.advanceTimersByTime(300);
    expect(new URL(window.location.href).searchParams.get('tag')).toBe('android');
    expect(new URL(window.location.href).searchParams.get('search')).toBe('crash');
    expect(reloadBoard).toHaveBeenLastCalledWith('alpha', 'android', 'crash', '7', 'me', 'newest', 'high');
  });

  it('shows and clears a selected zero-active tag without clearing text search', async () => {
    const board = makeBoard();
    board.tags = [{ name: 'ancient', count: 0 }];
    const { boardFilters } = await setupBoardFiltersState('/alpha?tag=ancient&search=crash&sprintId=7', {
      tag: 'ancient',
      search: 'crash',
      board,
      layout: 'omni',
    });
    const input = document.getElementById('searchInput') as HTMLInputElement;
    input.value = 'crash';
    const reloadBoard = vi.fn().mockResolvedValue(undefined);
    boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

    expect(document.querySelector('.omni-tag-pill--applied')?.textContent).toContain('ancient');
    expect(document.querySelector('[data-omni-tag="ancient"]')).toBeNull();
    (document.querySelector('[data-omni-clear-tag]') as HTMLButtonElement).click();

    const params = new URL(window.location.href).searchParams;
    expect(params.get('tag')).toBeNull();
    expect(params.get('search')).toBe('crash');
    expect(params.get('sprintId')).toBe('7');
    expect(input.value).toBe('crash');
    expect(reloadBoard).toHaveBeenCalledWith('alpha', '', 'crash', '7', null, null, null);
  });

  it('keeps a pending typed search scheduled when clearing the applied tag', async () => {
    const board = makeBoard();
    board.tags = [
      { name: 'android', count: 2 },
      { name: 'crash-reporting', count: 1 },
    ];
    const { boardFilters } = await setupBoardFiltersState('/alpha?tag=android', {
      tag: 'android',
      board,
      layout: 'omni',
    });
    const reloadBoard = vi.fn().mockResolvedValue(undefined);
    boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

    const input = document.getElementById('searchInput') as HTMLInputElement;
    input.value = 'crash';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    (document.querySelector('[data-omni-clear-tag]') as HTMLButtonElement).click();

    expect(input.value).toBe('crash');
    expect(new URL(window.location.href).searchParams.get('tag')).toBeNull();
    expect(new URL(window.location.href).searchParams.get('search')).toBeNull();
    expect(reloadBoard).toHaveBeenCalledTimes(1);
    expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, null);

    vi.advanceTimersByTime(299);
    expect(reloadBoard).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(1);
    const params = new URL(window.location.href).searchParams;
    expect(params.get('tag')).toBeNull();
    expect(params.get('search')).toBe('crash');
    expect(input.value).toBe('crash');
    expect(reloadBoard).toHaveBeenCalledTimes(2);
    expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', 'crash', null, null, null, null);
  });

  it.each(['Enter', ' '])('activates an Omni suggestion with the %s key', async (key) => {
    const board = makeBoard();
    board.tags = [{ name: 'android', count: 2 }];
    const { boardFilters } = await setupBoardFiltersState('/alpha?search=andr&sprintId=3', {
      search: 'andr',
      board,
      layout: 'omni',
    });
    const input = document.getElementById('searchInput') as HTMLInputElement;
    input.value = 'andr';
    const reloadBoard = vi.fn().mockResolvedValue(undefined);
    boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

    const suggestion = document.querySelector('[data-omni-tag="android"]') as HTMLButtonElement;
    suggestion.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));

    const params = new URL(window.location.href).searchParams;
    expect(params.get('tag')).toBe('android');
    expect(params.get('search')).toBeNull();
    expect(params.get('sprintId')).toBe('3');
    expect(input.value).toBe('');
    expect(reloadBoard).toHaveBeenCalledTimes(1);
  });

  it('updates sprint chip state via sprint-updated events without a full board reload', async () => {
    const { boardFilters, events, board } = await setupBoardFiltersState('/alpha?sprintId=3');
    const reloadBoard = vi.fn().mockResolvedValue(undefined);

    boardFilters.setSprintChipDataForSlug('alpha', {
      sprints: [{ id: 12, number: 3, name: 'Sprint 3', state: 'PLANNED' }],
    });
    const rendered = boardFilters.computeBoardChipsRender(board, '', '3');
    const tagChips = document.getElementById('tagChips');
    if (!tagChips) throw new Error('missing tagChips test node');
    tagChips.innerHTML = rendered.chipsHTML;

    boardFilters.bindBoardFilterUi({
      reloadBoard,
      showError: vi.fn(),
    });
    boardFilters.ensureSprintSubscription();

    expect(tagChips.innerHTML).not.toContain('chip--active-sprint');

    events.emit('sprint-updated', { sprintId: 12, state: 'ACTIVE' });

    expect(tagChips.innerHTML).toContain('chip--active-sprint');
    expect(reloadBoard).not.toHaveBeenCalled();
  });
});

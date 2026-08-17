// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board, PriorityTier } from '../types.js';
import { NO_PRIORITY_FILTER_VALUE } from '../types.js';
import { BOARD_TODO_SORT_STORAGE_KEY, getBoardTodoSortPreference } from '../core/board-sort-preferences.js';
import { buildFilterPanelHtml, isBoardFilterActive } from './board-rendering.js';
import type { BoardMember } from '../state/state.js';
import enCatalog from '../i18n/locales/en.json';

const selectorState: {
  board: Board | null;
  slug: string | null;
  tag: string;
  search: string;
  tagColors: Record<string, string>;
  user: { id: number; name: string; email: string } | null;
} = {
  board: null,
  slug: null,
  tag: '',
  search: '',
  tagColors: {},
  user: { id: 1, name: 'Me', email: 'me@example.com' },
};

const toastMock = vi.fn();

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
  getUser: () => selectorState.user,
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
  showToast: (...args: unknown[]) => toastMock(...args),
}));

function makeMembers(): BoardMember[] {
  return [
    { userId: 1, name: 'Me', email: 'me@example.com', role: 'contributor' },
    { userId: 5, name: 'Alice Example', email: 'alice@example.com', role: 'maintainer' },
    { userId: 9, name: '', email: 'noname@example.com', role: 'contributor' },
  ];
}

function makeTiers(): PriorityTier[] {
  return [
    { key: 'high', name: 'High', color: '#ff0000', position: 0 },
    { key: 'low', name: 'Low', color: '#00ff00', position: 1 },
    { key: 'none', name: 'None tier', color: '#0000ff', position: 2 },
  ];
}

function renderFilterShell(assignee: string | null, sort: string | null, user: any = { id: 1, name: 'Me', email: 'me@example.com' }, priority: string | null = null): void {
  document.body.innerHTML = `
    <div class="search-input-wrapper">
      <input id="searchInput" type="text" />
      ${buildFilterPanelHtml(assignee, sort, makeMembers(), user, priority, makeTiers())}
    </div>
  `;
}

async function loadModules() {
  const boardFilters = await import('./board-filters.js');
  return { boardFilters };
}

async function setupState(url: string, opts?: { tag?: string; search?: string; assignee?: string | null; sort?: string | null; priority?: string | null; user?: any }) {
  toastMock.mockClear();
  window.history.replaceState({}, '', url);
  renderFilterShell(opts?.assignee ?? null, opts?.sort ?? null, opts?.user, opts?.priority ?? null);

  const { boardFilters } = await loadModules();
  selectorState.board = null;
  selectorState.slug = 'alpha';
  selectorState.tag = opts?.tag ?? '';
  selectorState.search = opts?.search ?? '';

  return { boardFilters };
}

describe('board filter panel (assignee + sort)', () => {
  beforeEach(async () => {
    localStorage.clear();
    selectorState.user = { id: 1, name: 'Me', email: 'me@example.com' };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({
      locale: 'en',
      loadLocale: vi.fn(async () => enCatalog),
    });
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    document.body.innerHTML = '';
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    selectorState.board = null;
    selectorState.slug = null;
    selectorState.tag = '';
    selectorState.search = '';
    selectorState.user = { id: 1, name: 'Me', email: 'me@example.com' };
  });

  describe('buildFilterPanelHtml', () => {
    it('renders the toggle and both assignee/sort option sections', () => {
      const html = buildFilterPanelHtml(null, null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });

      expect(html).toContain('id="searchFilterToggle"');
      expect(html).toContain('id="searchFilterPanel"');
      expect(html).toContain('data-assignee-option=""');
      expect(html).toContain('data-assignee-option="unassigned"');
      expect(html).toContain('data-assignee-option="me"');
      expect(html).not.toContain('data-assignee-option="1"');
      expect(html).toContain('data-assignee-option="5"');
      expect(html).toContain('>Alice Example<');
      // Falls back to email when name is blank.
      expect(html).toContain('>noname@example.com<');
      expect(html).toContain('data-sort-option=""');
      expect(html).toContain('data-sort-option="newest"');
      expect(html).toContain('data-sort-option="oldest"');
    });

    it('omits the "Assigned to me" option when there is no logged-in user (anonymous/temp boards)', () => {
      const html = buildFilterPanelHtml(null, null, makeMembers(), null);
      expect(html).not.toContain('data-assignee-option="me"');
      expect(html).toContain('data-assignee-option="1"');
      expect(html).toContain('data-assignee-option="5"');
      expect(html).toContain('data-assignee-option="9"');
    });

    it('omits the logged-in user\'s named member option while keeping Assigned to me and other members', () => {
      const html = buildFilterPanelHtml(null, null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(html).toContain('data-assignee-option="me"');
      expect(html).not.toContain('data-assignee-option="1"');
      expect(html).toContain('data-assignee-option="5"');
      expect(html).toContain('data-assignee-option="9"');
    });

    it('marks Assigned to me as active for a legacy numeric current-user assignee value', () => {
      const html = buildFilterPanelHtml('1', null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(html).toContain('class="search-filter-option is-active" data-assignee-option="me"');
      expect(html).not.toContain('data-assignee-option="1"');
    });

    it('marks the option matching the current assignee/sort value as active', () => {
      const html = buildFilterPanelHtml('unassigned', 'newest', makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(html).toContain('class="search-filter-option is-active" data-assignee-option="unassigned"');
      expect(html).toContain('class="search-filter-option is-active" data-sort-option="newest"');
    });

    it('marks the toggle as active whenever assignee or sort is non-default', () => {
      expect(isBoardFilterActive(null, null, null)).toBe(false);
      expect(isBoardFilterActive('unassigned', null, null)).toBe(true);
      expect(isBoardFilterActive(null, 'newest', null)).toBe(true);
      expect(isBoardFilterActive(null, null, 'high')).toBe(true);

      const htmlInactive = buildFilterPanelHtml(null, null, makeMembers(), null);
      expect(htmlInactive).not.toContain('search-filter-toggle--active');

      const htmlActive = buildFilterPanelHtml(null, 'oldest', makeMembers(), null);
      expect(htmlActive).toContain('search-filter-toggle--active');
    });

    it('renders the priority section from a tiers fixture, with "All priorities" and "No priority" options', () => {
      const html = buildFilterPanelHtml(null, null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' }, null, makeTiers());

      expect(html).toContain('data-priority-option=""');
      expect(html).toContain(`data-priority-option="${NO_PRIORITY_FILTER_VALUE}"`);
      expect(html).toContain('data-priority-option="none"');
      expect(html).toContain('data-priority-option="high"');
      expect(html).toContain('data-priority-option="low"');
      expect(html.match(/data-priority-option="none"/g)).toHaveLength(1);
      expect(html.match(/data-priority-option="\*\*none\*\*"/g)).toHaveLength(1);
      expect(html).toContain('>High<');
      expect(html).toContain('>Low<');
      expect(html).toContain('>None tier<');
      expect(html).toContain('border-color: #ff0000');
    });

    it('distinguishes active real-none-tier and no-priority options', () => {
      document.body.innerHTML = buildFilterPanelHtml(null, null, makeMembers(), null, 'none', makeTiers());
      expect(Array.from(document.querySelectorAll('[data-priority-option].is-active')).map((el) => el.getAttribute('data-priority-option'))).toEqual(['none']);

      document.body.innerHTML = buildFilterPanelHtml(null, null, makeMembers(), null, NO_PRIORITY_FILTER_VALUE, makeTiers());
      expect(Array.from(document.querySelectorAll('[data-priority-option].is-active')).map((el) => el.getAttribute('data-priority-option'))).toEqual([NO_PRIORITY_FILTER_VALUE]);
    });

    it('marks the toggle as active when a priority filter is applied', () => {
      const htmlInactive = buildFilterPanelHtml(null, null, makeMembers(), null, null, makeTiers());
      expect(htmlInactive).not.toContain('search-filter-toggle--active');

      const htmlActive = buildFilterPanelHtml(null, null, makeMembers(), null, NO_PRIORITY_FILTER_VALUE, makeTiers());
      expect(htmlActive).toContain('search-filter-toggle--active');
    });
  });

  describe('popover open/close', () => {
    it('opens on toggle click and closes on a subsequent toggle click', async () => {
      const { boardFilters } = await setupState('/alpha');
      boardFilters.bindBoardFilterUi({ reloadBoard: vi.fn().mockResolvedValue(undefined), showError: vi.fn() });

      const toggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      const panel = document.getElementById('searchFilterPanel') as HTMLElement;
      expect(panel.hidden).toBe(true);

      toggle.click();
      expect(panel.hidden).toBe(false);
      expect(toggle.getAttribute('aria-expanded')).toBe('true');

      toggle.click();
      expect(panel.hidden).toBe(true);
      expect(toggle.getAttribute('aria-expanded')).toBe('false');
    });

    it('closes on outside click', async () => {
      const { boardFilters } = await setupState('/alpha');
      boardFilters.bindBoardFilterUi({ reloadBoard: vi.fn().mockResolvedValue(undefined), showError: vi.fn() });

      const toggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      const panel = document.getElementById('searchFilterPanel') as HTMLElement;
      toggle.click();
      expect(panel.hidden).toBe(false);

      document.body.click();
      expect(panel.hidden).toBe(true);
    });

    it('closes on Escape', async () => {
      const { boardFilters } = await setupState('/alpha');
      boardFilters.bindBoardFilterUi({ reloadBoard: vi.fn().mockResolvedValue(undefined), showError: vi.fn() });

      const toggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      const panel = document.getElementById('searchFilterPanel') as HTMLElement;
      toggle.click();
      expect(panel.hidden).toBe(false);

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
      expect(panel.hidden).toBe(true);
    });

    it('keeps one delegated listener set across full panel replacements', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      const oldToggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      const oldPanel = document.getElementById('searchFilterPanel') as HTMLElement;
      oldToggle.click();
      expect(oldPanel.hidden).toBe(false);

      renderFilterShell(null, null);
      boardFilters.resetBoardFilterUiState();
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      const currentToggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      const currentPanel = document.getElementById('searchFilterPanel') as HTMLElement;
      const oldFocus = vi.spyOn(oldToggle, 'focus');
      const currentFocus = vi.spyOn(currentToggle, 'focus');
      const oldRect = vi.spyOn(oldToggle, 'getBoundingClientRect');
      const currentRect = vi.spyOn(currentToggle, 'getBoundingClientRect');

      currentToggle.click();
      oldRect.mockClear();
      currentRect.mockClear();
      window.dispatchEvent(new Event('resize'));
      expect(oldRect).not.toHaveBeenCalled();
      expect(currentRect).toHaveBeenCalledTimes(1);

      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
      expect(oldFocus).not.toHaveBeenCalled();
      expect(currentFocus).toHaveBeenCalledTimes(1);
      expect(currentPanel.hidden).toBe(true);

      (currentPanel.querySelector('[data-sort-option="newest"]') as HTMLButtonElement).click();
      expect(reloadBoard).toHaveBeenCalledTimes(1);
      expect(reloadBoard).toHaveBeenCalledWith('alpha', '', null, null, null, 'newest', null);
    });
  });

  describe('URL param round-trip and reload wiring', () => {
    it('picking an assignee option sets the URL param and reloads with all 6 positional args', async () => {
      const { boardFilters } = await setupState('/alpha?tag=bug&search=query&sprintId=7', { tag: 'bug', search: 'query' });
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      const option = document.querySelector('[data-assignee-option="unassigned"]') as HTMLButtonElement;
      option.click();

      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('unassigned');
      expect(reloadBoard).toHaveBeenCalledTimes(1);
      expect(reloadBoard).toHaveBeenCalledWith('alpha', 'bug', 'query', '7', 'unassigned', null, null);
      expect(toastMock).toHaveBeenCalledWith(expect.stringContaining('Unassigned'));
    });

    it('picking "me" sets assignee=me; picking "All assignees" clears the param without a toast', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-assignee-option="me"]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('me');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, 'me', null, null);
      expect(toastMock).toHaveBeenCalledTimes(1);

      toastMock.mockClear();
      (document.querySelector('[data-assignee-option=""]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('assignee')).toBeNull();
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, null);
      // Clearing back to the neutral option is not itself "filtering" — no toast.
      expect(toastMock).not.toHaveBeenCalled();
    });

    it('picking a board member sets assignee to their numeric user id', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-assignee-option="5"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('5');
      expect(reloadBoard).toHaveBeenCalledWith('alpha', '', null, null, '5', null, null);
    });

    it('picking a sort option sets the sort param, reloads, and toasts "Sorted: ..."', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-sort-option="newest"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('sort')).toBe('newest');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, 'newest', null);
      expect(toastMock).toHaveBeenCalledWith(expect.stringContaining('Newest first'));

      toastMock.mockClear();
      (document.querySelector('[data-sort-option=""]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('sort')).toBeNull();
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, null);
      expect(toastMock).not.toHaveBeenCalled();
    });

    it('persists newest, oldest, and default board sort preferences when signed in', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-sort-option="newest"]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('sort')).toBe('newest');
      expect(getBoardTodoSortPreference()).toBe('newest');
      expect(localStorage.getItem(BOARD_TODO_SORT_STORAGE_KEY)).toBe('newest');
      expect(fetch).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ key: 'boardTodoSort', value: 'newest' }),
      }));

      (document.querySelector('[data-sort-option="oldest"]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('sort')).toBe('oldest');
      expect(getBoardTodoSortPreference()).toBe('oldest');
      expect(fetch).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ key: 'boardTodoSort', value: 'oldest' }),
      }));

      toastMock.mockClear();
      (document.querySelector('[data-sort-option=""]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('sort')).toBeNull();
      expect(getBoardTodoSortPreference()).toBe('default');
      expect(fetch).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ key: 'boardTodoSort', value: 'default' }),
      }));
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, null);
      expect(toastMock).not.toHaveBeenCalled();
    });

    it('does not PUT a board sort preference when there is no signed-in user', async () => {
      selectorState.user = null;
      const { boardFilters } = await setupState('/alpha', { user: null });
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });
      const fetchMock = fetch as unknown as ReturnType<typeof vi.fn>;
      fetchMock.mockClear();

      (document.querySelector('[data-sort-option="newest"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('sort')).toBe('newest');
      expect(reloadBoard).toHaveBeenCalledWith('alpha', '', null, null, null, 'newest', null);
      expect(fetchMock).not.toHaveBeenCalled();
      expect(localStorage.getItem(BOARD_TODO_SORT_STORAGE_KEY)).toBeNull();
      expect(getBoardTodoSortPreference()).toBe('default');
    });

    it('toggles the --active pulse class on the toggle button as filters are applied/cleared', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      const toggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      expect(toggle.classList.contains('search-filter-toggle--active')).toBe(false);

      (document.querySelector('[data-sort-option="oldest"]') as HTMLButtonElement).click();
      expect(toggle.classList.contains('search-filter-toggle--active')).toBe(true);

      (document.querySelector('[data-sort-option=""]') as HTMLButtonElement).click();
      expect(toggle.classList.contains('search-filter-toggle--active')).toBe(false);
    });

    it('picking a priority tier sets the priority param, reloads, and toasts "Filtering: ..."', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-priority-option="high"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('priority')).toBe('high');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, 'high');
      expect(toastMock).toHaveBeenCalledWith(expect.stringContaining('High'));

      toastMock.mockClear();
      (document.querySelector('[data-priority-option=""]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('priority')).toBeNull();
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, null);
      expect(toastMock).not.toHaveBeenCalled();
    });

    it('picking "No priority" uses the collision-proof sentinel', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector(`[data-priority-option="${NO_PRIORITY_FILTER_VALUE}"]`) as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('priority')).toBe(NO_PRIORITY_FILTER_VALUE);
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, NO_PRIORITY_FILTER_VALUE);
    });

    it('picking a real tier whose key is none keeps the literal tier key', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-priority-option="none"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('priority')).toBe('none');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null, 'none');
    });

    it('toggles the --active pulse class on the toggle button as the priority filter is applied/cleared', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      const toggle = document.getElementById('searchFilterToggle') as HTMLButtonElement;
      expect(toggle.classList.contains('search-filter-toggle--active')).toBe(false);

      (document.querySelector('[data-priority-option="high"]') as HTMLButtonElement).click();
      expect(toggle.classList.contains('search-filter-toggle--active')).toBe(true);

      (document.querySelector('[data-priority-option=""]') as HTMLButtonElement).click();
      expect(toggle.classList.contains('search-filter-toggle--active')).toBe(false);
    });
  });
});

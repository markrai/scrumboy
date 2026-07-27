// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import { buildFilterPanelHtml, isBoardFilterActive } from './board-rendering.js';
import type { BoardMember } from '../state/state.js';
import enCatalog from '../i18n/locales/en.json';

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

const toastMock = vi.fn();

vi.mock('../state/selectors.js', () => ({
  getAssigneeFromUrl: () => new URL(window.location.href).searchParams.get('assignee'),
  getSortFromUrl: () => new URL(window.location.href).searchParams.get('sort'),
  getBoard: () => selectorState.board,
  getSearch: () => selectorState.search,
  getSlug: () => selectorState.slug,
  getSprintIdFromUrl: () => new URL(window.location.href).searchParams.get('sprintId'),
  getTag: () => selectorState.tag,
  getTagColors: () => selectorState.tagColors,
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
    { userId: 5, name: 'Alice Example', email: 'alice@example.com', role: 'maintainer' },
    { userId: 9, name: '', email: 'noname@example.com', role: 'contributor' },
  ];
}

function renderFilterShell(assignee: string | null, sort: string | null, user: any = { id: 1, name: 'Me', email: 'me@example.com' }): void {
  document.body.innerHTML = `
    <div class="search-input-wrapper">
      <input id="searchInput" type="text" />
      ${buildFilterPanelHtml(assignee, sort, makeMembers(), user)}
    </div>
  `;
}

async function loadModules() {
  const boardFilters = await import('./board-filters.js');
  return { boardFilters };
}

async function setupState(url: string, opts?: { tag?: string; search?: string; assignee?: string | null; sort?: string | null; user?: any }) {
  toastMock.mockClear();
  window.history.replaceState({}, '', url);
  renderFilterShell(opts?.assignee ?? null, opts?.sort ?? null, opts?.user);

  const { boardFilters } = await loadModules();
  selectorState.board = null;
  selectorState.slug = 'alpha';
  selectorState.tag = opts?.tag ?? '';
  selectorState.search = opts?.search ?? '';

  return { boardFilters };
}

describe('board filter panel (assignee + sort)', () => {
  beforeEach(async () => {
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
    document.body.innerHTML = '';
    window.history.replaceState({}, '', '/');
    selectorState.board = null;
    selectorState.slug = null;
    selectorState.tag = '';
    selectorState.search = '';
  });

  describe('buildFilterPanelHtml', () => {
    it('renders the toggle and both assignee/sort option sections', () => {
      const html = buildFilterPanelHtml(null, null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });

      expect(html).toContain('id="searchFilterToggle"');
      expect(html).toContain('id="searchFilterPanel"');
      expect(html).toContain('data-assignee-option=""');
      expect(html).toContain('data-assignee-option="unassigned"');
      expect(html).toContain('data-assignee-option="me"');
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
    });

    it('marks the option matching the current assignee/sort value as active', () => {
      const html = buildFilterPanelHtml('unassigned', 'newest', makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(html).toContain('class="search-filter-option is-active" data-assignee-option="unassigned"');
      expect(html).toContain('class="search-filter-option is-active" data-sort-option="newest"');
    });

    it('marks the toggle as active whenever assignee or sort is non-default', () => {
      expect(isBoardFilterActive(null, null)).toBe(false);
      expect(isBoardFilterActive('unassigned', null)).toBe(true);
      expect(isBoardFilterActive(null, 'newest')).toBe(true);

      const htmlInactive = buildFilterPanelHtml(null, null, makeMembers(), null);
      expect(htmlInactive).not.toContain('search-filter-toggle--active');

      const htmlActive = buildFilterPanelHtml(null, 'oldest', makeMembers(), null);
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
      expect(reloadBoard).toHaveBeenCalledWith('alpha', '', null, null, null, 'newest');
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
      expect(reloadBoard).toHaveBeenCalledWith('alpha', 'bug', 'query', '7', 'unassigned', null);
      expect(toastMock).toHaveBeenCalledWith(expect.stringContaining('Unassigned'));
    });

    it('picking "me" sets assignee=me; picking "All assignees" clears the param without a toast', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-assignee-option="me"]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('me');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, 'me', null);
      expect(toastMock).toHaveBeenCalledTimes(1);

      toastMock.mockClear();
      (document.querySelector('[data-assignee-option=""]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('assignee')).toBeNull();
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null);
      // Clearing back to the neutral option is not itself "filtering" — no toast.
      expect(toastMock).not.toHaveBeenCalled();
    });

    it('picking a board member sets assignee to their numeric user id', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-assignee-option="5"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('5');
      expect(reloadBoard).toHaveBeenCalledWith('alpha', '', null, null, '5', null);
    });

    it('picking a sort option sets the sort param, reloads, and toasts "Sorted: ..."', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);
      boardFilters.bindBoardFilterUi({ reloadBoard, showError: vi.fn() });

      (document.querySelector('[data-sort-option="newest"]') as HTMLButtonElement).click();

      expect(new URL(window.location.href).searchParams.get('sort')).toBe('newest');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, 'newest');
      expect(toastMock).toHaveBeenCalledWith(expect.stringContaining('Newest first'));

      toastMock.mockClear();
      (document.querySelector('[data-sort-option=""]') as HTMLButtonElement).click();
      expect(new URL(window.location.href).searchParams.get('sort')).toBeNull();
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null, null);
      expect(toastMock).not.toHaveBeenCalled();
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
  });
});

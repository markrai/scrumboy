// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import { buildAssigneeSelectHtml } from './board-rendering.js';
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

vi.mock('../state/selectors.js', () => ({
  getAssigneeFromUrl: () => new URL(window.location.href).searchParams.get('assignee'),
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
}));

function makeMembers(): BoardMember[] {
  return [
    { userId: 5, name: 'Alice Example', email: 'alice@example.com', role: 'maintainer' },
    { userId: 9, name: '', email: 'noname@example.com', role: 'contributor' },
  ];
}

function renderFilterShell(): void {
  document.body.innerHTML = `
    <div class="search-input-wrapper">
      <input id="searchInput" type="text" />
    </div>
    <div id="assigneeHost"></div>
  `;
}

async function loadModules() {
  const boardFilters = await import('./board-filters.js');
  return { boardFilters };
}

async function setupState(url: string, opts?: { tag?: string; search?: string }) {
  vi.resetModules();
  window.history.replaceState({}, '', url);
  renderFilterShell();

  const { boardFilters } = await loadModules();
  selectorState.board = null;
  selectorState.slug = 'alpha';
  selectorState.tag = opts?.tag ?? '';
  selectorState.search = opts?.search ?? '';

  return { boardFilters };
}

describe('board assignee filter', () => {
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

  describe('buildAssigneeSelectHtml', () => {
    it('renders All/Unassigned first, then Assigned to me when a user is present, then board members', () => {
      const html = buildAssigneeSelectHtml(null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });

      expect(html).toContain('id="assigneeSelect"');
      expect(html).toContain('<option value="" selected');
      expect(html).toContain('<option value="unassigned"');
      expect(html).toContain('<option value="me"');
      expect(html).toContain('<option value="5">Alice Example</option>');
      // Falls back to email when name is blank.
      expect(html).toContain('<option value="9">noname@example.com</option>');

      const allIdx = html.indexOf('value=""');
      const unassignedIdx = html.indexOf('value="unassigned"');
      const meIdx = html.indexOf('value="me"');
      const memberIdx = html.indexOf('value="5"');
      expect(allIdx).toBeLessThan(unassignedIdx);
      expect(unassignedIdx).toBeLessThan(meIdx);
      expect(meIdx).toBeLessThan(memberIdx);
    });

    it('omits the "Assigned to me" option when there is no logged-in user (anonymous/temp boards)', () => {
      const html = buildAssigneeSelectHtml(null, makeMembers(), null);
      expect(html).not.toContain('value="me"');
    });

    it('marks the option matching the current assignee value as selected', () => {
      const htmlUnassigned = buildAssigneeSelectHtml('unassigned', makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(htmlUnassigned).toContain('<option value="unassigned" selected');

      const htmlMe = buildAssigneeSelectHtml('me', makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(htmlMe).toContain('<option value="me" selected');

      const htmlMember = buildAssigneeSelectHtml('5', makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });
      expect(htmlMember).toContain('<option value="5" selected>Alice Example</option>');
    });
  });

  describe('URL param round-trip and reload wiring', () => {
    it('reads assignee from the URL and includes it (with tag/search/sprintId) when reloading on change', async () => {
      const { boardFilters } = await setupState('/alpha?tag=bug&search=query&sprintId=7', { tag: 'bug', search: 'query' });
      const reloadBoard = vi.fn().mockResolvedValue(undefined);

      const host = document.getElementById('assigneeHost');
      if (!host) throw new Error('missing assigneeHost test node');
      host.innerHTML = buildAssigneeSelectHtml(null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });

      boardFilters.bindBoardFilterUi({
        reloadBoard,
        showError: vi.fn(),
      });

      const select = document.getElementById('assigneeSelect') as HTMLSelectElement | null;
      if (!select) throw new Error('missing assigneeSelect');

      select.value = 'unassigned';
      select.dispatchEvent(new Event('change', { bubbles: true }));

      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('unassigned');
      expect(reloadBoard).toHaveBeenCalledTimes(1);
      expect(reloadBoard).toHaveBeenCalledWith('alpha', 'bug', 'query', '7', 'unassigned');
    });

    it('selecting "me" sets assignee=me, and selecting All assignees clears the param', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);

      const host = document.getElementById('assigneeHost');
      if (!host) throw new Error('missing assigneeHost test node');
      host.innerHTML = buildAssigneeSelectHtml(null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });

      boardFilters.bindBoardFilterUi({
        reloadBoard,
        showError: vi.fn(),
      });

      const select = document.getElementById('assigneeSelect') as HTMLSelectElement | null;
      if (!select) throw new Error('missing assigneeSelect');

      select.value = 'me';
      select.dispatchEvent(new Event('change', { bubbles: true }));
      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('me');
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, 'me');

      select.value = '';
      select.dispatchEvent(new Event('change', { bubbles: true }));
      expect(new URL(window.location.href).searchParams.get('assignee')).toBeNull();
      expect(reloadBoard).toHaveBeenLastCalledWith('alpha', '', null, null, null);
    });

    it('selecting a board member sets assignee to their numeric user id', async () => {
      const { boardFilters } = await setupState('/alpha');
      const reloadBoard = vi.fn().mockResolvedValue(undefined);

      const host = document.getElementById('assigneeHost');
      if (!host) throw new Error('missing assigneeHost test node');
      host.innerHTML = buildAssigneeSelectHtml(null, makeMembers(), { id: 1, name: 'Me', email: 'me@example.com' });

      boardFilters.bindBoardFilterUi({
        reloadBoard,
        showError: vi.fn(),
      });

      const select = document.getElementById('assigneeSelect') as HTMLSelectElement | null;
      if (!select) throw new Error('missing assigneeSelect');

      select.value = '5';
      select.dispatchEvent(new Event('change', { bubbles: true }));

      expect(new URL(window.location.href).searchParams.get('assignee')).toBe('5');
      expect(reloadBoard).toHaveBeenCalledWith('alpha', '', null, null, '5');
    });
  });
});

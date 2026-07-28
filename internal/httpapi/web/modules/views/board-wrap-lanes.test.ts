// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import {
  setWrapLanesPreference,
  WRAP_LANES_STORAGE_KEY,
} from '../core/wrap-lanes-preferences.js';

const apiFetchMock = vi.hoisted(() => vi.fn());
const initDnDMock = vi.hoisted(() => vi.fn());
const setDnDColumnsMock = vi.hoisted(() => vi.fn());
const domElements = vi.hoisted(() => ({
  app: document.createElement('div'),
  settingsDialog: document.createElement('dialog'),
}));

vi.mock('../dom/elements.js', () => ({
  app: domElements.app,
  settingsDialog: domElements.settingsDialog,
}));

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../members-cache.js', () => ({
  fetchProjectMembers: vi.fn(async () => []),
  invalidateMembersCache: vi.fn(),
}));

vi.mock('../router.js', () => ({
  navigate: vi.fn(),
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  showToast: vi.fn(),
  renderAvatarContent: vi.fn(() => ''),
  renderUserAvatar: vi.fn(() => ''),
  processImageFile: vi.fn(),
  confirmDelete: vi.fn(),
  showConfirmDialog: vi.fn(),
  showPromptDialog: vi.fn(),
  sanitizeHexColor: (color?: string, fallback?: string) =>
    typeof color === 'string' && /^#[0-9a-fA-F]{6}$/.test(color) ? color : (fallback ?? null),
  isAnonymousBoard: (board: Board | null) => !!(board?.project?.expiresAt && board.project.creatorUserId == null),
  isTemporaryBoard: (board: Board | null) => !!board?.project?.expiresAt,
}));

vi.mock('../field-tooltips.js', () => ({
  FIELD_TOOLTIPS: {},
  titleAttr: () => '',
  fieldLabelHTML: (label: string) => `<div class="field__label">${label}</div>`,
}));

vi.mock('../dialogs/todo.js', () => ({
  openTodoDialog: vi.fn(),
}));

vi.mock('../dialogs/settings.js', () => ({
  renderSettingsModal: vi.fn(),
}));

vi.mock('../features/drag-drop.js', () => ({
  initDnD: initDnDMock,
  setDnDColumns: setDnDColumnsMock,
  columnsSpec: () => [
    { key: 'backlog', title: 'Backlog' },
    { key: 'done', title: 'Done' },
  ],
  dragInProgress: false,
  dragJustEnded: false,
}));

vi.mock('../features/context-menu-button.js', () => ({
  setContextMenuStatus: vi.fn(),
  setContextMenuRole: vi.fn(),
}));

vi.mock('../events.js', () => ({
  on: vi.fn(),
  off: vi.fn(),
}));

vi.mock('../realtime/guard.js', () => ({
  recordLocalMutation: vi.fn(),
}));

vi.mock('../orchestration/board-refresh.js', () => ({
  registerBoardRefresher: vi.fn(),
  registerSprintsRefresher: vi.fn(),
  invalidateBoard: vi.fn(),
  getBoardLimitPerLaneFloor: () => 20,
  resetBoardLimitPerLaneFloor: vi.fn(),
  setBoardLimitPerLaneFloor: vi.fn(),
}));

vi.mock('../sprints.js', () => ({
  normalizeSprints: vi.fn(() => []),
}));

vi.mock('./mobile-lane-tabs.js', () => ({
  applyMobileLaneTabStyles: vi.fn(),
  buildMobileTabsInnerHtml: vi.fn(() => ''),
  mobileLaneTabStyleAttrForHtml: vi.fn(() => ({ tab: '', drop: '' })),
}));

vi.mock('./board-realtime.js', () => ({
  attachBoardInteractionListeners: vi.fn(),
  clearPendingRealtimeRefresh: vi.fn(),
  connectBoardEvents: vi.fn(),
  debugLog: vi.fn(),
  disconnectBoardEvents: vi.fn(),
  markBoardLoadSucceeded: vi.fn(),
  runWhileTodoDialogOpening: vi.fn(async (fn: () => unknown) => fn()),
  setInitialBoardLoadInFlight: vi.fn(),
}));

vi.mock('./board-command-capabilities.js', () => ({
  canShowVoiceCommands: vi.fn(() => false),
}));

vi.mock('../core/voiceflow-preferences.js', () => ({
  getVoiceFlowEnabledPreference: vi.fn(() => false),
}));

vi.mock('../dialogs/bulk-edit.js', () => ({
  initBulkEditDialog: vi.fn(),
  openBulkEditDialog: vi.fn(),
}));

const enCatalog = {
  'board.actions.changeProjectImage': 'Change project image',
  'board.actions.clearSearch': 'Clear search',
  'board.actions.deleteProject': 'Delete project',
  'board.actions.manageMembers': 'Members',
  'board.actions.newTodo': 'New Todo',
  'board.actions.openWall': 'Open wall',
  'board.actions.renameProject': 'Rename',
  'board.actions.settings': 'Settings',
  'board.backToProjects': '\u2190 Projects',
  'board.filters.all': 'All',
  'board.filters.allAssignees': 'All assignees',
  'board.filters.assignee': 'Assignee',
  'board.filters.assignedToMe': 'Assigned to me',
  'board.filters.defaultOrder': 'Default order',
  'board.filters.filteringOn': 'Filtering: {value}',
  'board.filters.label': 'Tags:',
  'board.filters.newestFirst': 'Newest first',
  'board.filters.next': 'Next tags',
  'board.filters.oldestFirst': 'Oldest first',
  'board.filters.openFilters': 'Filters',
  'board.filters.previous': 'Previous tags',
  'board.filters.scheduled': 'Scheduled',
  'board.filters.sort': 'Sort',
  'board.filters.sortedBy': 'Sorted: {value}',
  'board.filters.unassigned': 'Unassigned',
  'board.filters.unscheduled': 'Unscheduled',
  'board.loadMore': 'Load more',
  'board.noResults': 'No todos found matching "{search}"',
  'board.search.placeholder.desktop': 'Search todos...',
  'board.search.placeholder.mobile': 'Search',
  'board.todo.dragCard': 'Drag card',
};

function boardWithLaneCount(count: number): Board {
  const columnOrder = Array.from({ length: count }, (_, index) => ({
    key: `lane${index + 1}`,
    name: `Lane ${index + 1}`,
    color: '#9ca3af',
    isDone: index === count - 1,
  }));
  const columns: Board['columns'] = {};
  for (const column of columnOrder) {
    columns[column.key as keyof Board['columns']] = [];
  }
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 1,
    },
    tags: [],
    columnOrder,
    columns,
  };
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

async function renderPrefetchedBoard(
  mod: typeof import('./board.js'),
  board: Board,
): Promise<void> {
  await mod.renderBoard('alpha', '', '', null, null, null, null, null, {
    prefetchedBoard: board,
  });
  await flushPromises();
}

function boardEl(): Element | null {
  return document.querySelector('.board');
}

describe('board wrap lanes rendering', () => {
  beforeEach(async () => {
    document.body.innerHTML = '';
    domElements.app.id = 'app';
    domElements.app.innerHTML = '';
    document.body.appendChild(domElements.app);
    localStorage.clear();
    window.history.replaceState({}, '', '/alpha');
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue(null);
    initDnDMock.mockReset();
    setDnDColumnsMock.mockReset();
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      value: 1600,
    });
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
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
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({
      locale: 'en',
      loadLocale: vi.fn(async () => enCatalog),
    });
    const mutations = await import('../state/mutations.js');
    mutations.setAuthStatusAvailable(true);
    mutations.setUser({ id: 1, name: 'Alex', email: 'alex@example.com' } as any);
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    const mutations = await import('../state/mutations.js');
    mutations.setUser(null);
    mutations.setAuthStatusAvailable(false);
    document.body.innerHTML = '';
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('adds board--wrapped on full render when preference is on and lane count exceeds five', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const mod = await import('./board.js');

    await renderPrefetchedBoard(mod, boardWithLaneCount(6));

    expect(boardEl()?.classList.contains('board--wrapped')).toBe(true);
    expect((boardEl() as HTMLElement).style.getPropertyValue('--board-wrap-cols')).toBe('3');
  });

  it('does not add board--wrapped on full render when lane count is five or fewer', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const mod = await import('./board.js');

    await renderPrefetchedBoard(mod, boardWithLaneCount(5));

    expect(boardEl()?.classList.contains('board--wrapped')).toBe(false);
  });

  it('adds board--wrapped during incremental render when workflow grows beyond five lanes', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const mod = await import('./board.js');

    await renderPrefetchedBoard(mod, boardWithLaneCount(5));
    expect(boardEl()?.classList.contains('board--wrapped')).toBe(false);

    await renderPrefetchedBoard(mod, boardWithLaneCount(8));
    expect(boardEl()?.classList.contains('board--wrapped')).toBe(true);
    expect((boardEl() as HTMLElement).style.getPropertyValue('--board-wrap-cols')).toBe('4');
  });

  it('removes board--wrapped during incremental render when workflow shrinks to five lanes', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const mod = await import('./board.js');

    await renderPrefetchedBoard(mod, boardWithLaneCount(6));
    expect(boardEl()?.classList.contains('board--wrapped')).toBe(true);

    await renderPrefetchedBoard(mod, boardWithLaneCount(5));
    expect(boardEl()?.classList.contains('board--wrapped')).toBe(false);
    expect((boardEl() as HTMLElement).style.getPropertyValue('--board-wrap-cols')).toBe('');
  });

  it('uses half-width rows for ten lanes (5+5)', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const mod = await import('./board.js');

    await renderPrefetchedBoard(mod, boardWithLaneCount(10));

    expect(boardEl()?.classList.contains('board--wrapped')).toBe(true);
    expect((boardEl() as HTMLElement).style.getPropertyValue('--board-wrap-cols')).toBe('5');
  });
});

describe('board wrap lanes hydration interaction', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
  });

  it('does not inherit a stale local enabled value after missing server preference hydration', async () => {
    const prefs = await import('../core/wrap-lanes-preferences.js');
    localStorage.setItem(WRAP_LANES_STORAGE_KEY, 'true');

    await prefs.loadWrapLanesPreferenceFromServer(async () => ({ value: '' }));

    expect(prefs.getWrapLanesPreference()).toBe(false);
  });
});

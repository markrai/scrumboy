// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { apiFetchMock, navigateMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn(), navigateMock: vi.fn() }));

vi.mock('../api.js', () => ({ apiFetch: apiFetchMock }));
vi.mock('../router.js', () => ({ navigate: navigateMock }));
vi.mock('../state/mutations.js', () => ({ setProjectsTab: vi.fn() }));
// Public mode: board route, but no member board object and an anonymous or nonmember viewer.
vi.mock('../state/selectors.js', () => ({
  getAuthStatusAvailable: () => true,
  getBoard: () => null,
  getProjectsTab: () => 'projects',
  getRoute: () => 'boardBySlug',
  getUser: () => null,
}));
vi.mock('../i18n/index.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../i18n/index.js')>()),
  t: (key: string) => key,
}));

import { normalizePublicBoardSnapshot, EMPTY_PUBLIC_QUERY } from '../public-board-api.js';
import { buildPublicBoardHtml } from '../views/public-board-rendering.js';

function renderPublicBoardDom(): void {
  const snapshot = normalizePublicBoardSnapshot({
    access: { kind: 'public', readOnly: true },
    project: { slug: 'ignite', name: 'Ignite', dominantColor: '', estimationMode: 'MODIFIED_FIBONACCI', sprintsEnabled: false },
    workflow: [{ key: 'backlog', name: 'Backlog', color: '#9ca3af', isDone: false, position: 0 }],
    priorities: [],
    tags: [],
    columns: { backlog: [{ localId: 1, title: 'One', body: '', columnKey: 'backlog', tags: [] }] },
    columnsMeta: { backlog: { hasMore: false, nextCursor: null, totalCount: 1 } },
  }, 'ignite');
  const app = document.createElement('div');
  app.id = 'app';
  app.innerHTML = buildPublicBoardHtml({
    snapshot,
    columns: snapshot.columns,
    lanes: { backlog: { ...snapshot.columnsMeta.backlog, loading: false, error: false } },
    query: EMPTY_PUBLIC_QUERY,
    sprints: [],
    activeMobileTab: 'backlog',
    isMobile: false,
    user: null,
  });
  document.body.replaceChildren(app);
  const settings = document.createElement('dialog');
  settings.id = 'settingsDialog';
  const todo = document.createElement('dialog');
  todo.id = 'todoDialog';
  document.body.append(settings, todo);
}

function press(key: string, code: string): void {
  document.dispatchEvent(new KeyboardEvent('keydown', { key, code, bubbles: true, cancelable: true }));
}

describe('global keyboard shortcuts on a public board', () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
    apiFetchMock.mockReset();
    navigateMock.mockReset();
    renderPublicBoardDom();
  });

  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('no mutation or private-surface shortcut can act: new todo, wall, escape-back', async () => {
    const clickSpy = vi.spyOn(HTMLButtonElement.prototype, 'click');
    const keybindings = await import('./keybindings.js');
    keybindings.initKeybindings({ openSettings: vi.fn() });

    press('n', 'KeyN');
    press('w', 'KeyW');
    press('Escape', 'Escape');
    for (const action of ['newTodo', 'openWall', 'boardEscapeBack'] as const) {
      keybindings.executeAction(action);
    }

    expect(clickSpy).not.toHaveBeenCalled();
    expect(navigateMock).not.toHaveBeenCalled();
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(document.querySelector('#newTodoBtn, #wallBtn')).toBeNull();
  });
});

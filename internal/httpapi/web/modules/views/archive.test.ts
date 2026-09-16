// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const stylesSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'styles.css'), 'utf8');

const h = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  listArchivedTodos: vi.fn(),
  restoreTodos: vi.fn(),
  openTodoDialog: vi.fn(),
  requestTodoDialogClose: vi.fn(),
  navigate: vi.fn(),
  showToast: vi.fn(),
  role: 'viewer' as string | null,
  temporary: false,
  state: {
    slug: null as string | null,
    board: null as any,
    projectId: 1,
    members: [] as any[],
    editingTodo: null as any,
  },
}));

vi.mock('../api.js', () => ({
  apiFetch: h.apiFetch,
  listArchivedTodos: h.listArchivedTodos,
  restoreTodos: h.restoreTodos,
}));
vi.mock('../dialogs/todo.js', () => ({
  openTodoDialog: h.openTodoDialog,
  requestTodoDialogClose: h.requestTodoDialogClose,
}));
vi.mock('../events.js', () => ({ on: vi.fn(), off: vi.fn() }));
vi.mock('../core/sse-client.js', () => ({
  SseConnectionManager: class { open() {} stop() {} },
}));
vi.mock('../realtime/guard.js', () => ({ recordLocalMutation: vi.fn(), setBulkUpdating: vi.fn() }));
vi.mock('../state/selectors.js', () => ({
  getAuthStatusAvailable: () => true,
  getBoard: () => h.state.board,
  getBoardMembers: () => h.state.members,
  getEditingTodo: () => h.state.editingTodo,
  getProjectId: () => h.state.projectId,
  getSlug: () => h.state.slug,
  getUser: () => ({ id: 7, name: 'Ada' }),
}));
vi.mock('../state/mutations.js', () => ({
  setBoard: (board: any) => { h.state.board = board; },
  setBoardMembers: (members: any[]) => { h.state.members = members; },
  setOpenTodoSegment: vi.fn(),
}));
vi.mock('../dom/elements.js', () => ({
  get app() { return document.getElementById('app'); },
}));
vi.mock('../utils.js', () => ({
  escapeHTML: (value: unknown) => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;'),
  isTemporaryBoard: () => h.temporary,
  renderAvatarContent: () => '',
  renderUserAvatar: () => '<button id="userAvatarBtn"></button>',
  sanitizeHexColor: () => null,
  showToast: h.showToast,
}));
vi.mock('../router.js', () => ({ navigate: h.navigate }));
vi.mock('../i18n/index.js', () => ({
  apiErrorMessage: () => 'error',
  formatDate: (value: string) => value.slice(0, 10),
  t: (key: string, params?: Record<string, unknown>) => {
    const values: Record<string, string> = {
      'archive.title': 'Archive',
      'archive.loading': 'Loading archive…',
      'archive.empty': 'No archived stories.',
      'archive.selectAll': 'Select all loaded',
      'archive.restoreSelected': 'Restore selected',
      'board.loadMore': 'Load more',
      'common.retry': 'Retry',
    };
    let out = values[key] || key;
    for (const [name, value] of Object.entries(params || {})) out = out.replace(`{${name}}`, String(value));
    return out;
  },
}));
vi.mock('./board-load-bootstrap.js', () => ({
  bootstrapLoadedBoardView: async (args: any) => {
    h.state.slug = args.slug;
    h.state.members = [{ userId: 7, name: 'Ada', role: h.role }];
    args.setResolvedRole(h.role);
    args.renderLoadedBoard({ projectId: 1, backHref: '/', minimalTopbar: false });
    return true;
  },
}));

const board = {
  project: { id: 1, slug: 'alpha', name: 'Alpha', dominantColor: '#000000', creatorUserId: 2 },
  tags: [],
  columnOrder: [
    { key: 'doing', name: 'Doing', isDone: false },
    { key: 'done', name: 'Done', isDone: true },
  ],
  columns: { doing: [], done: [] },
};

function todo(id: number, localId: number, title: string) {
  return {
    id,
    localId,
    title,
    status: 'done',
    columnKey: 'done',
    archivedAt: `2026-09-${String(localId).padStart(2, '0')}T12:00:00Z`,
    tags: [],
  };
}

describe('archive view', () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = '<div id="app"></div>';
    window.history.replaceState({}, '', '/alpha/archive');
    h.apiFetch.mockReset().mockResolvedValue(board);
    h.listArchivedTodos.mockReset();
    h.restoreTodos.mockReset();
    h.openTodoDialog.mockReset();
    h.navigate.mockReset();
    h.showToast.mockReset();
    h.role = 'viewer';
    h.temporary = false;
    h.state.slug = null;
    h.state.board = null;
    h.state.members = [];
    h.state.editingTodo = null;
  });

  it('lets a viewer browse archived stories without mutation controls', async () => {
    h.listArchivedTodos.mockResolvedValue({ todos: [todo(1, 12, 'Shipped story')], nextCursor: null, hasMore: false });
    const { renderArchive } = await import('./archive.js');
    await renderArchive('alpha');

    expect(document.querySelector('[data-archive-open="12"]')?.textContent).toContain('Shipped story');
    expect(document.querySelector('[data-archive-open="12"]')?.textContent).toContain('archive.done');
    expect(document.querySelector('[data-archive-select]')).toBeNull();
    expect(document.getElementById('archiveSelectAllBtn')?.hidden).toBe(true);
  });

  it('returns to the board on Escape when no dialog is open', async () => {
    h.listArchivedTodos.mockResolvedValue({ todos: [], nextCursor: null, hasMore: false });
    const { renderArchive, stopArchiveEvents } = await import('./archive.js');
    await renderArchive('alpha');

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    expect(h.navigate).toHaveBeenCalledWith('/alpha');

    h.navigate.mockClear();
    const dialog = document.createElement('dialog');
    dialog.id = 'todoDialog';
    document.body.appendChild(dialog);
    dialog.setAttribute('open', '');
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    expect(h.navigate).not.toHaveBeenCalled();

    stopArchiveEvents();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    expect(h.navigate).not.toHaveBeenCalled();
  });

  it('keeps the archive project image inside a fixed-size desktop wrapper', async () => {
    h.apiFetch.mockResolvedValue({
      ...board,
      project: { ...board.project, image: '/uploads/alpha.png' },
    });
    h.listArchivedTodos.mockResolvedValue({ todos: [], nextCursor: null, hasMore: false });
    const { renderArchive } = await import('./archive.js');

    await renderArchive('alpha');

    expect(document.querySelector('.archive-project-image > img.project-image-topbar')).not.toBeNull();
    expect(stylesSource).toMatch(
      /\.archive-project-image\s*\{[^}]*width:\s*var\(--s-32\)[^}]*height:\s*var\(--s-32\)[^}]*flex:\s*0\s+0\s+var\(--s-32\)[^}]*overflow:\s*hidden/,
    );
  });

  it('appends cursor pages without duplicating a repeated boundary row', async () => {
    h.listArchivedTodos
      .mockResolvedValueOnce({ todos: [todo(1, 12, 'First')], nextCursor: '100:1', hasMore: true })
      .mockResolvedValueOnce({ todos: [todo(1, 12, 'First'), todo(2, 11, 'Second')], nextCursor: null, hasMore: false });
    const { renderArchive } = await import('./archive.js');
    await renderArchive('alpha');
    (document.getElementById('archiveLoadMoreBtn') as HTMLButtonElement).click();

    await vi.waitFor(() => expect(document.querySelectorAll('[data-archive-local-id]')).toHaveLength(2));
    expect(h.listArchivedTodos).toHaveBeenNthCalledWith(2, 'alpha', { limit: 50, afterCursor: '100:1' });
    expect((document.getElementById('archiveLoadMoreBtn') as HTMLButtonElement).hidden).toBe(true);
  });

  it('keeps a failed continuation cursor visible and retries it without dropping loaded rows', async () => {
    h.listArchivedTodos
      .mockResolvedValueOnce({ todos: [todo(1, 12, 'First')], nextCursor: '100:1', hasMore: true })
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValueOnce({ todos: [todo(2, 11, 'Second')], nextCursor: null, hasMore: false });
    const { renderArchive } = await import('./archive.js');
    await renderArchive('alpha');
    const loadMore = document.getElementById('archiveLoadMoreBtn') as HTMLButtonElement;

    loadMore.click();

    await vi.waitFor(() => expect(h.showToast).toHaveBeenCalledTimes(1));
    expect(document.querySelectorAll('[data-archive-local-id]')).toHaveLength(1);
    expect(loadMore.hidden).toBe(false);
    expect(loadMore.disabled).toBe(false);
    expect(loadMore.textContent).toBe('Retry');
    expect(h.listArchivedTodos).toHaveBeenNthCalledWith(2, 'alpha', { limit: 50, afterCursor: '100:1' });

    loadMore.click();

    await vi.waitFor(() => expect(document.querySelectorAll('[data-archive-local-id]')).toHaveLength(2));
    expect(h.listArchivedTodos).toHaveBeenNthCalledWith(3, 'alpha', { limit: 50, afterCursor: '100:1' });
    expect(loadMore.hidden).toBe(true);
  });

  it('cannot replace a same-slug board after archive teardown invalidates an in-flight render', async () => {
    let resolveBoard!: (value: typeof board) => void;
    h.apiFetch.mockImplementationOnce(() => new Promise<typeof board>((resolve) => { resolveBoard = resolve; }));
    const { renderArchive, stopArchiveEvents } = await import('./archive.js');
    const pendingRender = renderArchive('alpha');
    await vi.waitFor(() => expect(h.apiFetch).toHaveBeenCalledWith('/api/board/alpha?limitPerLane=1'));

    stopArchiveEvents();
    h.state.slug = 'alpha';
    document.getElementById('app')!.innerHTML = '<main id="liveBoard">Current board</main>';
    resolveBoard(board);
    await pendingRender;

    expect(document.getElementById('liveBoard')?.textContent).toBe('Current board');
    expect(h.listArchivedTodos).not.toHaveBeenCalled();
  });

  it('restores selected stories with one atomic batch request and refetches the list', async () => {
    h.role = 'maintainer';
    h.listArchivedTodos
      .mockResolvedValueOnce({ todos: [todo(1, 12, 'One'), todo(2, 14, 'Two')], nextCursor: null, hasMore: false })
      .mockResolvedValueOnce({ todos: [], nextCursor: null, hasMore: false });
    h.restoreTodos.mockResolvedValue({ transitionedCount: 2 });
    const { renderArchive } = await import('./archive.js');
    await renderArchive('alpha');

    document.querySelectorAll<HTMLInputElement>('[data-archive-select]').forEach((checkbox) => {
      checkbox.checked = true;
      checkbox.dispatchEvent(new Event('change', { bubbles: true }));
    });
    (document.getElementById('archiveRestoreSelectedBtn') as HTMLButtonElement).click();

    await vi.waitFor(() => expect(h.restoreTodos).toHaveBeenCalledWith('alpha', [12, 14]));
    await vi.waitFor(() => expect(document.querySelectorAll('[data-archive-local-id]')).toHaveLength(0));
    expect(h.restoreTodos).toHaveBeenCalledTimes(1);
  });

  it('keeps every selected row when the atomic restore request fails', async () => {
    h.role = 'maintainer';
    h.listArchivedTodos.mockResolvedValue({ todos: [todo(1, 12, 'One'), todo(2, 14, 'Two')], nextCursor: null, hasMore: false });
    h.restoreTodos.mockRejectedValue(new Error('network'));
    const { renderArchive } = await import('./archive.js');
    await renderArchive('alpha');
    document.querySelectorAll<HTMLInputElement>('[data-archive-select]').forEach((checkbox) => {
      checkbox.checked = true;
      checkbox.dispatchEvent(new Event('change', { bubbles: true }));
    });
    (document.getElementById('archiveRestoreSelectedBtn') as HTMLButtonElement).click();

    await vi.waitFor(() => expect(h.showToast).toHaveBeenCalled());
    expect(document.querySelectorAll('[data-archive-local-id]')).toHaveLength(2);
    expect(document.querySelectorAll<HTMLInputElement>('[data-archive-select]:checked')).toHaveLength(2);
    expect(h.listArchivedTodos).toHaveBeenCalledTimes(1);
  });
});

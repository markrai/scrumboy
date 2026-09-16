import { apiFetch, listArchivedTodos, restoreTodos } from '../api.js';
import { requestTodoDialogClose, openTodoDialog } from '../dialogs/todo.js';
import { app } from '../dom/elements.js';
import { on, off } from '../events.js';
import { apiErrorMessage, formatDate, t } from '../i18n/index.js';
import { SseConnectionManager } from '../core/sse-client.js';
import { recordLocalMutation, setBulkUpdating } from '../realtime/guard.js';
import {
  getAuthStatusAvailable,
  getBoard,
  getBoardMembers,
  getEditingTodo,
  getProjectId,
  getSlug,
  getTagColors,
  getUser,
} from '../state/selectors.js';
import { setBoard, setBoardMembers, setOpenTodoSegment, setTagColors } from '../state/mutations.js';
import type { Board, Tag, Todo } from '../types.js';
import { escapeHTML, isTemporaryBoard, renderAvatarContent, renderUserAvatar, sanitizeHexColor, showToast } from '../utils.js';
import { navigate } from '../router.js';
import { bootstrapLoadedBoardView } from './board-load-bootstrap.js';

const ARCHIVE_PAGE_SIZE = 50;
const ARCHIVE_BATCH_MAX = 500;

let currentRole: string | null = null;
let archiveTodos: Todo[] = [];
let archiveNextCursor: string | null = null;
let archiveHasMore = false;
let archiveLoading = false;
let archiveLoadFailed = false;
let archiveSelectedLocalIds = new Set<number>();
let archiveRenderSequence = 0;
let archiveReloadPending = false;
let archiveRealtimeBound = false;
let archiveEscapeBound = false;
let archiveAnonSseManager: SseConnectionManager | null = null;
let archiveEventsSlug: string | null = null;
let archiveRefreshTimer: ReturnType<typeof setTimeout> | null = null;
let archiveTagColors = new Map<string, string>();

type ArchiveChangedDetail = {
  slug?: string;
  localId?: number;
  action?: 'restored' | 'deleted';
};

function canMutateArchive(): boolean {
  return currentRole === 'maintainer' || isTemporaryBoard(getBoard());
}

function archiveLane(todo: Todo): { name: string; isDone: boolean } {
  const key = todo.columnKey || todo.status;
  const lane = getBoard()?.columnOrder?.find((item) => item.key === key);
  return { name: lane?.name || key, isDone: lane?.isDone === true };
}

function archivedDate(todo: Todo): string {
  if (!todo.archivedAt) return '';
  return formatDate(todo.archivedAt, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

function installArchiveTagCatalog(catalog: readonly Tag[]): void {
  const colors = { ...getTagColors() };
  const catalogColors = new Map<string, string>();
  for (const tag of catalog) {
    if (!tag || typeof tag.name !== 'string' || !tag.name.trim()) continue;
    const key = tag.name.toLocaleLowerCase();
    for (const existingName of Object.keys(colors)) {
      if (existingName.toLocaleLowerCase() === key) delete colors[existingName];
    }
    if (typeof tag.color === 'string' && tag.color) {
      catalogColors.set(key, tag.color);
      colors[tag.name] = tag.color;
    }
  }
  archiveTagColors = catalogColors;
  setTagColors(colors);
}

function archiveTagHtml(todo: Todo): string {
  return (todo.tags || []).map((name) => {
    const color = sanitizeHexColor(archiveTagColors.get(name.toLocaleLowerCase()));
    const style = color ? ` style="border-color:${color};background:${color}20;color:${color}"` : '';
    return `<span class="tag"${style}>${escapeHTML(name)}</span>`;
  }).join('');
}

function archiveAssigneeHtml(todo: Todo): string {
  if (todo.assigneeUserId == null) return '';
  const member = getBoardMembers().find((item) => Number(item.userId) === Number(todo.assigneeUserId));
  if (!member) return '';
  const name = member.name || member.email || String(member.userId);
  return `<span class="archive-row__assignee" title="${escapeHTML(name)}">${renderAvatarContent(member)}<span>${escapeHTML(name)}</span></span>`;
}

function archivePriorityHtml(todo: Todo): string {
  if (!todo.priorityKey) return '';
  const tier = getBoard()?.priorityOrder?.find((item) => item.key === todo.priorityKey);
  if (!tier) return '';
  const color = sanitizeHexColor(tier.color);
  const style = color ? ` style="border-color:${color};background:${color}20;color:${color}"` : '';
  return `<span class="archive-row__badge"${style}>${escapeHTML(tier.name)}</span>`;
}

function archiveRowHtml(todo: Todo): string {
  const lane = archiveLane(todo);
  const selectable = canMutateArchive();
  const checked = archiveSelectedLocalIds.has(todo.localId);
  return `
    <article class="archive-row${checked ? ' archive-row--selected' : ''}" data-archive-local-id="${todo.localId}">
      ${selectable ? `
        <label class="archive-row__select">
          <input type="checkbox" data-archive-select="${todo.localId}" ${checked ? 'checked' : ''} aria-label="${escapeHTML(t('archive.selectStory', { id: todo.localId }))}" />
        </label>
      ` : ''}
      <button type="button" class="archive-row__open" data-archive-open="${todo.localId}">
        <span class="archive-row__title"><span class="archive-row__id">#${todo.localId}</span>${escapeHTML(todo.title)}</span>
        <span class="archive-row__meta">
          <span class="archive-row__badge">${escapeHTML(t('archive.lane', { lane: lane.name }))}</span>
          ${lane.isDone ? `<span class="archive-row__badge archive-row__badge--done">${escapeHTML(t('archive.done'))}</span>` : ''}
          ${archivePriorityHtml(todo)}
          ${archiveAssigneeHtml(todo)}
          <time datetime="${escapeHTML(todo.archivedAt || '')}">${escapeHTML(t('archive.archivedOn', { date: archivedDate(todo) }))}</time>
        </span>
        ${(todo.tags || []).length > 0 ? `<span class="archive-row__tags">${archiveTagHtml(todo)}</span>` : ''}
      </button>
    </article>
  `;
}

function syncArchiveSelectionUi(): void {
  const count = archiveSelectedLocalIds.size;
  const bar = document.getElementById('archiveSelectionBar');
  const countEl = document.getElementById('archiveSelectionCount');
  const restoreBtn = document.getElementById('archiveRestoreSelectedBtn') as HTMLButtonElement | null;
  if (bar) bar.hidden = !canMutateArchive() || count === 0;
  if (countEl) countEl.textContent = t('archive.selectionCount', { count });
  if (restoreBtn) restoreBtn.disabled = count === 0 || count > ARCHIVE_BATCH_MAX || archiveLoading;
  document.querySelectorAll<HTMLElement>('[data-archive-local-id]').forEach((row) => {
    const id = Number(row.dataset.archiveLocalId);
    row.classList.toggle('archive-row--selected', archiveSelectedLocalIds.has(id));
  });
}

function renderArchiveList(): void {
  const list = document.getElementById('archiveList');
  const status = document.getElementById('archiveStatus');
  const loadMore = document.getElementById('archiveLoadMoreBtn') as HTMLButtonElement | null;
  const retry = document.getElementById('archiveRetryBtn') as HTMLButtonElement | null;
  const selectAll = document.getElementById('archiveSelectAllBtn') as HTMLButtonElement | null;
  if (!list || !status) return;

  if (archiveLoadFailed && archiveTodos.length === 0) {
    list.innerHTML = '';
    status.textContent = t('archive.loadFailed');
    status.hidden = false;
    if (retry) retry.hidden = false;
  } else if (archiveLoading && archiveTodos.length === 0) {
    list.innerHTML = '';
    status.textContent = t('archive.loading');
    status.hidden = false;
    if (retry) retry.hidden = true;
  } else if (archiveTodos.length === 0) {
    list.innerHTML = '';
    status.textContent = t('archive.empty');
    status.hidden = false;
    if (retry) retry.hidden = true;
  } else {
    list.innerHTML = archiveTodos.map(archiveRowHtml).join('');
    status.hidden = true;
    if (retry) retry.hidden = true;
  }

  if (loadMore) {
    loadMore.hidden = !archiveHasMore;
    loadMore.disabled = archiveLoading;
    loadMore.textContent = archiveLoading && archiveTodos.length > 0
      ? t('archive.loading')
      : archiveLoadFailed && archiveTodos.length > 0
        ? t('common.retry')
        : t('board.loadMore');
  }
  if (selectAll) {
    selectAll.hidden = !canMutateArchive() || archiveTodos.length === 0;
    selectAll.disabled = archiveLoading;
  }
  syncArchiveSelectionUi();
}

function archiveTopbarHtml(board: Board): string {
  const image = board.project.image
    ? `<span class="archive-project-image"><img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" /></span>`
    : '<span class="archive-project-image"><span class="project-image-topbar-placeholder">📦</span></span>';
  return `
    <div class="topbar archive-topbar">
      <button class="btn btn--ghost" type="button" id="archiveBackBtn" data-i18n-text="archive.backToBoard">${escapeHTML(t('archive.backToBoard'))}</button>
      ${image}
      <div class="brand">${escapeHTML(board.project.name)}</div>
      <div class="archive-topbar__title" data-i18n-text="archive.title">${escapeHTML(t('archive.title'))}</div>
      <div class="spacer"></div>
      ${renderUserAvatar(getUser())}
    </div>
  `;
}

function renderArchiveShell(board: Board): void {
  app.innerHTML = `
    <div class="page page--archive">
      ${archiveTopbarHtml(board)}
      <main class="container archive-container">
        <div class="archive-heading">
          <div>
            <h1 data-i18n-text="archive.title">${escapeHTML(t('archive.title'))}</h1>
            <p class="muted" data-i18n-text="archive.description">${escapeHTML(t('archive.description'))}</p>
          </div>
          <button class="btn btn--ghost" type="button" id="archiveSelectAllBtn" data-i18n-text="archive.selectAll">${escapeHTML(t('archive.selectAll'))}</button>
        </div>
        <div class="archive-state" id="archiveStatus" role="status" aria-live="polite"></div>
        <button class="btn btn--ghost" type="button" id="archiveRetryBtn" hidden data-i18n-text="common.retry">${escapeHTML(t('common.retry'))}</button>
        <div class="archive-list" id="archiveList"></div>
        <div class="archive-load-more"><button class="btn btn--ghost" type="button" id="archiveLoadMoreBtn" hidden data-i18n-text="board.loadMore">${escapeHTML(t('board.loadMore'))}</button></div>
        <div class="archive-selection-bar" id="archiveSelectionBar" hidden aria-live="polite">
          <span id="archiveSelectionCount"></span>
          <button class="btn" type="button" id="archiveRestoreSelectedBtn" data-i18n-text="archive.restoreSelected">${escapeHTML(t('archive.restoreSelected'))}</button>
        </div>
      </main>
    </div>
  `;

  document.getElementById('archiveBackBtn')?.addEventListener('click', () => navigate(`/${board.project.slug || getSlug() || ''}`));
  document.getElementById('archiveRetryBtn')?.addEventListener('click', () => void loadArchivePage(true));
  document.getElementById('archiveLoadMoreBtn')?.addEventListener('click', () => void loadArchivePage(false));
  document.getElementById('archiveSelectAllBtn')?.addEventListener('click', () => {
    if (!canMutateArchive()) return;
    archiveSelectedLocalIds = new Set(archiveTodos.slice(0, ARCHIVE_BATCH_MAX).map((todo) => todo.localId));
    if (archiveTodos.length > ARCHIVE_BATCH_MAX) showToast(t('board.bulkArchive.limit'));
    renderArchiveList();
  });
  document.getElementById('archiveRestoreSelectedBtn')?.addEventListener('click', () => void restoreArchiveSelection());
  document.getElementById('archiveList')?.addEventListener('click', (event) => {
    const target = event.target as HTMLElement;
    const open = target.closest<HTMLElement>('[data-archive-open]');
    if (!open) return;
    const localId = Number(open.dataset.archiveOpen);
    const slug = getSlug();
    if (slug && Number.isInteger(localId) && localId > 0) navigate(`/${slug}/archive/t/${localId}`);
  });
  document.getElementById('archiveList')?.addEventListener('change', (event) => {
    const checkbox = (event.target as HTMLElement).closest<HTMLInputElement>('[data-archive-select]');
    if (!checkbox || !canMutateArchive()) return;
    const localId = Number(checkbox.dataset.archiveSelect);
    if (checkbox.checked && archiveSelectedLocalIds.size >= ARCHIVE_BATCH_MAX && !archiveSelectedLocalIds.has(localId)) {
      checkbox.checked = false;
      showToast(t('board.bulkArchive.limit'));
      return;
    }
    if (checkbox.checked) archiveSelectedLocalIds.add(localId);
    else archiveSelectedLocalIds.delete(localId);
    syncArchiveSelectionUi();
  });
}

async function loadArchivePage(reset: boolean): Promise<void> {
  const slug = getSlug();
  if (!slug) return;
  if (archiveLoading) {
    if (reset) archiveReloadPending = true;
    return;
  }
  if (!reset && !archiveHasMore) return;

  archiveLoading = true;
  archiveLoadFailed = false;
  if (reset) {
    archiveTodos = [];
    archiveNextCursor = null;
    archiveHasMore = false;
    archiveSelectedLocalIds.clear();
  }
  renderArchiveList();
  try {
    const page = await listArchivedTodos(slug, { limit: ARCHIVE_PAGE_SIZE, afterCursor: reset ? null : archiveNextCursor });
    if (getSlug() !== slug) return;
    const byId = new Map<number, Todo>();
    for (const todo of reset ? [] : archiveTodos) byId.set(todo.id, todo);
    for (const todo of page.todos || []) byId.set(todo.id, todo);
    archiveTodos = Array.from(byId.values());
    archiveNextCursor = page.nextCursor || null;
    archiveHasMore = page.hasMore === true;
  } catch (error) {
    archiveLoadFailed = true;
    if (archiveTodos.length > 0) showToast(apiErrorMessage(error, { fallbackKey: 'archive.loadFailed' }));
  } finally {
    archiveLoading = false;
    renderArchiveList();
    if (archiveReloadPending) {
      archiveReloadPending = false;
      void loadArchivePage(true);
    }
  }
}

async function restoreArchiveSelection(): Promise<void> {
  const slug = getSlug();
  const localIds = Array.from(archiveSelectedLocalIds);
  if (!slug || !canMutateArchive() || localIds.length === 0) return;
  if (localIds.length > ARCHIVE_BATCH_MAX) {
    showToast(t('board.bulkArchive.limit'));
    return;
  }
  setBulkUpdating(true);
  try {
    recordLocalMutation();
    const result = await restoreTodos(slug, localIds);
    archiveSelectedLocalIds.clear();
    showToast(t('archive.restoredMultiple', { count: result.transitionedCount }));
    await loadArchivePage(true);
  } catch (error) {
    showToast(apiErrorMessage(error, { fallbackKey: 'archive.restoreFailed' }));
    syncArchiveSelectionUi();
  } finally {
    setBulkUpdating(false);
  }
}

async function openArchivedTodo(slug: string, localId: number): Promise<void> {
  try {
    const todo = await apiFetch<Todo>(`/api/board/${encodeURIComponent(slug)}/todos/${localId}`);
    if (!todo.archivedAt) {
      navigate(`/${slug}/t/${localId}`);
      return;
    }
    await openTodoDialog({ mode: 'edit', todo, onNavigateToLinkedTodo: navigate, role: currentRole });
    setOpenTodoSegment(String(localId));
  } catch (error) {
    showToast(apiErrorMessage(error, { fallbackKey: 'board.openTodo.failed' }));
    navigate(`/${slug}/archive`);
  }
}

async function reconcileOpenArchivedTodo(): Promise<void> {
  const slug = getSlug();
  const editing = getEditingTodo();
  if (!slug || !editing?.archivedAt || !window.location.pathname.includes('/archive/t/')) return;
  try {
    const latest = await apiFetch<Todo>(`/api/board/${encodeURIComponent(slug)}/todos/${editing.localId}`);
    if (latest.archivedAt) return;
  } catch {
    // A hard delete and a restore both make the open archived projection stale.
  }
  await requestTodoDialogClose({ force: true });
  showToast(t('archive.storyChanged'));
  navigate(`/${slug}/archive`);
}

function scheduleArchiveRefresh(): void {
  if (archiveRefreshTimer !== null) clearTimeout(archiveRefreshTimer);
  archiveRefreshTimer = setTimeout(() => {
    archiveRefreshTimer = null;
    void loadArchivePage(true).then(() => reconcileOpenArchivedTodo());
  }, 300);
}

function onArchiveRealtimeEvent(payload: unknown): void {
  if (!archiveEventsSlug || getSlug() !== archiveEventsSlug) return;
  const event = payload as { type?: string; projectId?: number };
  if (event.type === 'ping' || event.type === 'todo.assigned' || event.type === 'todo.creator_activity' || event.type === 'wall.refresh_needed' || event.type === 'wall.transient') return;
  const projectId = getProjectId();
  if (typeof event.projectId === 'number' && projectId != null && event.projectId !== projectId) return;
  if (event.type === 'refresh_needed') scheduleArchiveRefresh();
}

function onArchiveEscapeKeydown(ev: KeyboardEvent): void {
  if (ev.key !== 'Escape' || ev.repeat || ev.defaultPrevented) return;
  // Let native <dialog> cancel handling win (todo detail, settings, etc.).
  if (document.querySelector('dialog[open]')) return;
  const slug = getSlug();
  if (!slug || !archiveEventsSlug) return;
  ev.preventDefault();
  navigate(`/${slug}`);
}

function connectArchiveEvents(slug: string): void {
  stopArchiveEvents();
  archiveEventsSlug = slug;
  if (!archiveEscapeBound) {
    document.addEventListener('keydown', onArchiveEscapeKeydown);
    archiveEscapeBound = true;
  }
  if (getAuthStatusAvailable() && getUser()) {
    on('realtime:event', onArchiveRealtimeEvent);
    archiveRealtimeBound = true;
    return;
  }
  const manager = new SseConnectionManager(`/api/board/${slug}/events`, {
    label: `archive/${slug}/events`,
    onMessage: (event) => {
      try {
        onArchiveRealtimeEvent(JSON.parse(event.data));
      } catch {
        // Ignore malformed realtime messages; the next valid refresh reconciles the list.
      }
    },
  });
  archiveAnonSseManager = manager;
  manager.open();
}

export function stopArchiveEvents(): void {
  ++archiveRenderSequence;
  if (archiveRefreshTimer !== null) {
    clearTimeout(archiveRefreshTimer);
    archiveRefreshTimer = null;
  }
  if (archiveEscapeBound) {
    document.removeEventListener('keydown', onArchiveEscapeKeydown);
    archiveEscapeBound = false;
  }
  if (archiveRealtimeBound) {
    off('realtime:event', onArchiveRealtimeEvent);
    archiveRealtimeBound = false;
  }
  archiveAnonSseManager?.stop();
  archiveAnonSseManager = null;
  archiveEventsSlug = null;
}

window.addEventListener('scrumboy:archive-list-changed', (event) => {
  const detail = (event as CustomEvent<ArchiveChangedDetail>).detail || {};
  if (!archiveEventsSlug || detail.slug !== archiveEventsSlug) return;
  scheduleArchiveRefresh();
});

export async function renderArchive(slug: string | null, openTodoSegment: string | null = null): Promise<void> {
  if (!slug) throw new Error('Slug is required');
  stopArchiveEvents();
  const sequence = ++archiveRenderSequence;
  setBoardMembers([]);
  currentRole = null;
  archiveTodos = [];
  archiveNextCursor = null;
  archiveHasMore = false;
  archiveLoadFailed = false;
  archiveTagColors = new Map();
  archiveSelectedLocalIds.clear();
  app.innerHTML = `<div class="page page--archive"><main class="container archive-container"><div class="archive-state" role="status">${escapeHTML(t('archive.loading'))}</div></main></div>`;

  const encodedSlug = encodeURIComponent(slug);
  const [board, catalogResponse] = await Promise.all([
    apiFetch<Board>(`/api/board/${encodedSlug}?limitPerLane=1`),
    apiFetch<unknown>(`/api/board/${encodedSlug}/tags`).catch(() => []),
  ]);
  if (sequence !== archiveRenderSequence) return;
  installArchiveTagCatalog(Array.isArray(catalogResponse) ? catalogResponse as Tag[] : []);
  const rendered = await bootstrapLoadedBoardView({
    board,
    slug,
    tag: '',
    search: '',
    isCurrent: () => sequence === archiveRenderSequence && getSlug() === slug,
    setResolvedRole: (role) => { currentRole = role; },
    markMembersFetched: () => {},
    renderLoadedBoard: () => {
      setBoard(board);
      renderArchiveShell(board);
    },
    markLoadSuccess: () => {},
  });
  if (!rendered || sequence !== archiveRenderSequence) return;
  connectArchiveEvents(slug);
  await loadArchivePage(true);
  if (openTodoSegment) {
    const localId = Number.parseInt(openTodoSegment, 10);
    if (Number.isInteger(localId) && localId > 0) await openArchivedTodo(slug, localId);
  } else {
    setOpenTodoSegment(null);
  }
}

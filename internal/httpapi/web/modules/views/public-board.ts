/**
 * Public read-only board view.
 *
 * Entered only after the member/temporary board route returned an
 * access-denial or not-found result and GET /api/public/board/{slug} succeeds.
 * This module is a separate boundary from views/board.ts: it never imports the
 * member editor, drag/drop, context menu, selection, bulk edit, settings,
 * Wall, Agenda, VoiceFlow, or member/realtime-bus code, and it calls only the
 * public API and the public event stream.
 */

import { app } from '../dom/elements.js';
import { t } from '../i18n/index.js';
import { showToast } from '../utils.js';
import { getMarkdownNotesEnabled, getMermaidNotesEnabled, getSlug, getUser } from '../state/selectors.js';
import { setBoard, setBoardAccess, setProjectId } from '../state/mutations.js';
import { PUBLIC_BOARD_ACCESS } from '../state/board-access.js';
import {
  fetchPublicBoardSnapshot,
  fetchPublicLanePage,
  fetchPublicSprints,
  isPublicAbort,
  isPublicNotFound,
  normalizePublicQuery,
  publicBoardEventsPath,
  PublicBoardApiError,
  PUBLIC_DEFAULT_PAGE_SIZE,
  PUBLIC_MAX_PAGE_SIZE,
  samePublicQuery,
  EMPTY_PUBLIC_QUERY,
  type PublicBoardQuery,
  type PublicBoardSnapshot,
  type PublicSprint,
  type PublicTodo,
} from '../public-board-api.js';
import {
  buildPublicBoardHtml,
  buildPublicColumnsHtml,
  buildPublicFilterErrorHtml,
  buildPublicFiltersHtml,
  buildPublicMobileTabsHtml,
  buildPublicUnavailableHtml,
  isPublicQueryActive,
  publicPriorityMap,
  showPublicPoints,
  type PublicBoardViewModel,
  type PublicLaneState,
} from './public-board-rendering.js';
import { closePublicTodo, getOpenPublicTodoLocalId, openPublicTodo, type PublicTodoDialogContext } from '../dialogs/public-todo.js';
import { SseConnectionManager, type SseConnectionPolicy } from '../core/sse-client.js';

/**
 * Public stream policy. The server sends comment-only heartbeats (~15s) that
 * never reach onmessage, so the data-ping watchdog is disabled. Reconnects use
 * jittered exponential backoff from 5s, and the backoff resets only after 30s
 * of stable connection, keeping attempts well under 20/minute/IP.
 */
export const PUBLIC_STREAM_POLICY: SseConnectionPolicy = Object.freeze({
  staleAfterMs: null,
  initialBackoffMs: 5_000,
  maxBackoffMs: 60_000,
  jitterRatio: 0.3,
  stableAfterMs: 30_000,
});

const REFRESH_DEBOUNCE_MS = 400;
const SEARCH_DEBOUNCE_MS = 350;
const MOBILE_BREAKPOINT_PX = 620;

export type PublicRouteInput = { readonly slug: string; readonly openTodoSegment: string | null };
export type PublicResolution = 'rendered' | 'not-public' | 'stale';

type PublicSession = {
  readonly slug: string;
  snapshot: PublicBoardSnapshot;
  columns: Record<string, PublicTodo[]>;
  lanes: Record<string, PublicLaneState>;
  query: PublicBoardQuery;
  sprints: PublicSprint[];
  activeMobileTab: string;
  /** Incremented whenever the board data set is replaced; stale page/snapshot results are dropped. */
  dataEpoch: number;
  stream: SseConnectionManager | null;
  streamOpenedOnce: boolean;
  refreshTimer: ReturnType<typeof setTimeout> | null;
  refreshInFlight: boolean;
  refreshQueued: boolean;
  snapshotController: AbortController | null;
  laneControllers: Map<string, AbortController>;
  searchTimer: ReturnType<typeof setTimeout> | null;
  /** True once the sprint vocabulary was loaded for the current sprints-enabled state. */
  sprintsLoaded: boolean;
  /** Incremented per sprint vocabulary request; only the latest response may apply. */
  sprintsSeq: number;
  sprintsController: AbortController | null;
  /** A filter change failed to load: the board region shows an error, not stale cards. */
  filterError: boolean;
  rendered: { filters: string; columns: string; tabs: string };
};

let session: PublicSession | null = null;
let resolutionSeq = 0;
let resolutionController: AbortController | null = null;

// ---- URL <-> query ----

export function readPublicQueryFromUrl(): PublicBoardQuery {
  const url = new URL(window.location.href);
  const sprintRaw = url.searchParams.get('sprintNumber');
  const sprint = sprintRaw && /^\d{1,9}$/.test(sprintRaw) ? Number(sprintRaw) : null;
  return normalizePublicQuery({
    search: url.searchParams.get('search') ?? '',
    tags: url.searchParams.getAll('tag'),
    sprintNumber: sprint,
    priority: url.searchParams.get('priority'),
  });
}

function writePublicQueryToUrl(query: PublicBoardQuery): void {
  const url = new URL(window.location.href);
  for (const key of ['search', 'tag', 'sprintNumber', 'priority']) url.searchParams.delete(key);
  if (query.search) url.searchParams.set('search', query.search);
  for (const tag of query.tags) url.searchParams.append('tag', tag);
  if (query.sprintNumber != null) url.searchParams.set('sprintNumber', String(query.sprintNumber));
  if (query.priority) url.searchParams.set('priority', query.priority);
  history.replaceState(history.state ?? {}, '', url.pathname + url.search + url.hash);
}

function boardPathWithQuery(slug: string): string {
  return `/${slug}${window.location.search}`;
}

function storyPathWithQuery(slug: string, localId: number): string {
  return `/${slug}/t/${localId}${window.location.search}`;
}

// ---- Session state ----

function lanesFromSnapshot(snapshot: PublicBoardSnapshot): Record<string, PublicLaneState> {
  const lanes: Record<string, PublicLaneState> = {};
  for (const column of snapshot.workflow) {
    const meta = snapshot.columnsMeta[column.key];
    lanes[column.key] = { ...meta, loading: false, error: false };
  }
  return lanes;
}

function columnsFromSnapshot(snapshot: PublicBoardSnapshot): Record<string, PublicTodo[]> {
  const columns: Record<string, PublicTodo[]> = {};
  for (const column of snapshot.workflow) columns[column.key] = [...(snapshot.columns[column.key] ?? [])];
  return columns;
}

function applySnapshot(s: PublicSession, snapshot: PublicBoardSnapshot): void {
  s.snapshot = snapshot;
  s.columns = columnsFromSnapshot(snapshot);
  s.lanes = lanesFromSnapshot(snapshot);
  if (!snapshot.workflow.some((column) => column.key === s.activeMobileTab)) {
    s.activeMobileTab = snapshot.workflow[0]?.key ?? '';
  }
}

function viewModel(s: PublicSession): PublicBoardViewModel {
  return {
    snapshot: s.snapshot,
    columns: s.columns,
    lanes: s.lanes,
    query: s.query,
    sprints: s.sprints,
    activeMobileTab: s.activeMobileTab,
    isMobile: typeof window !== 'undefined' && window.innerWidth <= MOBILE_BREAKPOINT_PX,
    user: getUser(),
  };
}

function isCurrent(s: PublicSession): boolean {
  return session === s;
}

function teardown(s: PublicSession): void {
  s.stream?.stop();
  s.stream = null;
  if (s.refreshTimer !== null) clearTimeout(s.refreshTimer);
  s.refreshTimer = null;
  if (s.searchTimer !== null) clearTimeout(s.searchTimer);
  s.searchTimer = null;
  s.snapshotController?.abort();
  s.snapshotController = null;
  for (const controller of s.laneControllers.values()) controller.abort();
  s.laneControllers.clear();
  s.sprintsSeq++;
  s.sprintsController?.abort();
  s.sprintsController = null;
  s.refreshQueued = false;
}

/** Ends any public board view and its stream. Safe to call repeatedly. */
export function stopPublicBoard(): void {
  resolutionSeq++;
  resolutionController?.abort();
  resolutionController = null;
  const s = session;
  session = null;
  if (s) {
    teardown(s);
    closePublicTodo();
    setBoardAccess(null);
  }
}

export function isPublicBoardSessionFor(slug: string | null): boolean {
  return !!slug && !!session && session.slug === slug;
}

// ---- Rendering ----

function captureListScroll(root: Element): Map<string, number> {
  const out = new Map<string, number>();
  root.querySelectorAll<HTMLElement>('[data-public-list]').forEach((el) => {
    out.set(el.getAttribute('data-public-list') ?? '', el.scrollTop);
  });
  return out;
}

function restoreListScroll(root: Element, scroll: Map<string, number>): void {
  root.querySelectorAll<HTMLElement>('[data-public-list]').forEach((el) => {
    const top = scroll.get(el.getAttribute('data-public-list') ?? '');
    if (top) el.scrollTop = top;
  });
}

/** Updates only the regions whose markup changed, preserving focus and lane scroll. */
function renderBoardRegions(s: PublicSession): void {
  if (!isCurrent(s)) return;
  const view = viewModel(s);
  const boardEl = app.querySelector('[data-public-board]');
  const tabsEl = app.querySelector('#publicMobileTabs');
  const filtersEl = app.querySelector('.public-board-filters');
  if (!boardEl || !tabsEl || !filtersEl) {
    renderFull(s);
    return;
  }
  const columns = boardRegionHtml(s, view);
  if (columns !== s.rendered.columns) {
    const scroll = captureListScroll(boardEl);
    boardEl.innerHTML = columns;
    restoreListScroll(boardEl, scroll);
    s.rendered.columns = columns;
  }
  const tabs = buildPublicMobileTabsHtml(view);
  if (tabs !== s.rendered.tabs) {
    tabsEl.innerHTML = tabs;
    s.rendered.tabs = tabs;
  }
  const filters = buildPublicFiltersHtml(view);
  if (filters !== s.rendered.filters) {
    filtersEl.outerHTML = filters;
    s.rendered.filters = filters;
  }
  syncNoResults(s);
}

function syncNoResults(s: PublicSession): void {
  const container = app.querySelector('[data-public-board-page] .container');
  if (!container) return;
  const existing = container.querySelector('.public-board__no-results');
  const total = s.snapshot.workflow.reduce((sum, column) => sum + (s.lanes[column.key]?.totalCount ?? 0), 0);
  const show = !s.filterError && total === 0 && isPublicQueryActive(s.query);
  if (show && !existing) {
    const el = document.createElement('div');
    el.className = 'empty public-board__no-results';
    el.setAttribute('role', 'status');
    const title = document.createElement('div');
    title.className = 'empty__title';
    title.setAttribute('data-i18n-text', 'publicBoard.empty');
    title.textContent = t('publicBoard.empty');
    el.append(title);
    container.querySelector('.mobile-board-wrapper')?.before(el);
  } else if (!show && existing) {
    existing.remove();
  }
}

function boardRegionHtml(s: PublicSession, view: PublicBoardViewModel): string {
  return s.filterError ? buildPublicFilterErrorHtml() : buildPublicColumnsHtml(view);
}

function renderFull(s: PublicSession): void {
  if (!isCurrent(s)) return;
  const view = viewModel(s);
  app.innerHTML = buildPublicBoardHtml(view);
  const columns = boardRegionHtml(s, view);
  if (s.filterError) {
    const boardEl = app.querySelector('[data-public-board]');
    if (boardEl) boardEl.innerHTML = columns;
    syncNoResults(s);
  }
  s.rendered = { filters: buildPublicFiltersHtml(view), columns, tabs: buildPublicMobileTabsHtml(view) };
  bindPageHandlers(s);
}

function bindPageHandlers(s: PublicSession): void {
  const page = app.querySelector<HTMLElement>('[data-public-board-page]');
  if (!page) return;
  // All handlers are bound on this freshly created page element; they are read-only.
  page.addEventListener('click', (event) => {
    if (!isCurrent(s)) return;
    const target = event.target as HTMLElement;
    const card = target.closest<HTMLElement>('[data-public-local-id]');
    if (card) {
      const localId = Number(card.getAttribute('data-public-local-id'));
      if (Number.isSafeInteger(localId) && localId > 0) openStoryFromBoard(s, localId);
      return;
    }
    if (target.closest('[data-public-retry]')) {
      void reloadSnapshot(s, 'filter');
      return;
    }
    const more = target.closest<HTMLElement>('[data-public-load-more]');
    if (more) {
      void loadMore(s, more.getAttribute('data-public-load-more') ?? '');
      return;
    }
    const tag = target.closest<HTMLElement>('[data-public-tag]');
    if (tag) {
      toggleTag(s, tag.getAttribute('data-public-tag') ?? '');
      return;
    }
    const tab = target.closest<HTMLElement>('[data-public-tab]');
    if (tab) {
      s.activeMobileTab = tab.getAttribute('data-public-tab') ?? s.activeMobileTab;
      renderBoardRegions(s);
      return;
    }
    if (target.closest('#publicSearchClear')) {
      const input = page.querySelector<HTMLInputElement>('#publicSearchInput');
      if (input) input.value = '';
      setQuery(s, { ...s.query, search: '' });
      input?.focus();
      return;
    }
    if (target.closest('#publicBrandLink')) {
      window.location.assign('/');
    }
  });
  page.addEventListener('change', (event) => {
    if (!isCurrent(s)) return;
    const target = event.target as HTMLSelectElement;
    if (target.id === 'publicSprintFilter') {
      const n = Number(target.value);
      setQuery(s, { ...s.query, sprintNumber: Number.isSafeInteger(n) && n > 0 ? n : null });
    } else if (target.id === 'publicPriorityFilter') {
      setQuery(s, { ...s.query, priority: target.value || null });
    }
  });
  page.addEventListener('input', (event) => {
    if (!isCurrent(s)) return;
    const target = event.target as HTMLInputElement;
    if (target.id !== 'publicSearchInput') return;
    page.querySelector('#publicSearchClear')?.toggleAttribute('hidden', target.value.trim() === '');
    if (s.searchTimer !== null) clearTimeout(s.searchTimer);
    s.searchTimer = setTimeout(() => {
      s.searchTimer = null;
      if (!isCurrent(s)) return;
      setQuery(s, { ...s.query, search: target.value });
    }, SEARCH_DEBOUNCE_MS);
  });
}

// ---- Filters, refresh, pagination ----

function toggleTag(s: PublicSession, tag: string): void {
  if (!tag) {
    setQuery(s, { ...s.query, tags: [] });
    return;
  }
  const key = tag.toLocaleLowerCase();
  const has = s.query.tags.some((existing) => existing.toLocaleLowerCase() === key);
  setQuery(s, { ...s.query, tags: has ? s.query.tags.filter((existing) => existing.toLocaleLowerCase() !== key) : [...s.query.tags, tag] });
}

function setQuery(s: PublicSession, next: Partial<PublicBoardQuery>): void {
  if (!isCurrent(s)) return;
  const query = normalizePublicQuery({ ...s.query, ...next });
  if (samePublicQuery(query, s.query)) return;
  s.query = query;
  writePublicQueryToUrl(query);
  void reloadSnapshot(s, 'filter');
}

function loadedPageSize(s: PublicSession): number {
  let max = PUBLIC_DEFAULT_PAGE_SIZE;
  for (const key of Object.keys(s.columns)) max = Math.max(max, s.columns[key].length);
  return Math.min(PUBLIC_MAX_PAGE_SIZE, max);
}

/**
 * Replaces the board data with a fresh snapshot for the current filters.
 * Every replacement increments dataEpoch, aborting in-flight snapshot and lane
 * requests, so pages from an older query or project are never merged.
 */
async function reloadSnapshot(s: PublicSession, kind: 'filter' | 'refresh'): Promise<void> {
  if (!isCurrent(s)) return;
  const epoch = ++s.dataEpoch;
  s.snapshotController?.abort();
  for (const controller of s.laneControllers.values()) controller.abort();
  s.laneControllers.clear();
  const controller = new AbortController();
  s.snapshotController = controller;
  const query = s.query;
  try {
    const snapshot = await fetchPublicBoardSnapshot(s.slug, query, {
      signal: controller.signal,
      limitPerLane: kind === 'refresh' ? loadedPageSize(s) : PUBLIC_DEFAULT_PAGE_SIZE,
    });
    if (!isCurrent(s) || epoch !== s.dataEpoch) return;
    s.filterError = false;
    applySnapshot(s, snapshot);
    renderBoardRegions(s);
    syncSprintVocabulary(s, kind);
  } catch (err) {
    if (!isCurrent(s) || epoch !== s.dataEpoch || isPublicAbort(err)) return;
    if (isPublicNotFound(err)) {
      enterUnavailable(s);
      return;
    }
    if (err instanceof PublicBoardApiError && err.kind === 'invalid_request' && isPublicQueryActive(query)) {
      // A filter the board no longer supports: fall back to the unfiltered view.
      s.query = EMPTY_PUBLIC_QUERY;
      writePublicQueryToUrl(s.query);
      renderFull(s);
      void reloadSnapshot(s, 'filter');
      return;
    }
    if (kind === 'filter') {
      // The displayed cards belong to the previous query; never present them
      // as matching the newly selected filters. Show an error with Retry.
      s.filterError = true;
      renderBoardRegions(s);
    }
    // A failed realtime refresh keeps the current content: it still matches
    // the current filters, and realtime or the next action retries.
  } finally {
    if (s.snapshotController === controller) s.snapshotController = null;
  }
}

/** Coalesced realtime/reconnect refresh: at most one in flight plus one queued. */
function requestRefresh(s: PublicSession): void {
  if (!isCurrent(s) || s.refreshTimer !== null) return;
  s.refreshTimer = setTimeout(() => {
    s.refreshTimer = null;
    void runRefresh(s);
  }, REFRESH_DEBOUNCE_MS);
}

async function runRefresh(s: PublicSession): Promise<void> {
  if (!isCurrent(s)) return;
  if (s.refreshInFlight) {
    s.refreshQueued = true;
    return;
  }
  s.refreshInFlight = true;
  try {
    await reloadSnapshot(s, 'refresh');
  } finally {
    s.refreshInFlight = false;
  }
  if (s.refreshQueued && isCurrent(s)) {
    s.refreshQueued = false;
    requestRefresh(s);
  }
}

async function loadMore(s: PublicSession, key: string): Promise<void> {
  if (!isCurrent(s)) return;
  const lane = s.lanes[key];
  if (!lane || lane.loading || !lane.nextCursor) return;
  const epoch = s.dataEpoch;
  const cursor = lane.nextCursor;
  const query = s.query;
  s.lanes[key] = { ...lane, loading: true, error: false };
  renderBoardRegions(s);
  const controller = new AbortController();
  s.laneControllers.get(key)?.abort();
  s.laneControllers.set(key, controller);
  try {
    const page = await fetchPublicLanePage(s.slug, key, query, cursor, { signal: controller.signal, limit: PUBLIC_DEFAULT_PAGE_SIZE });
    if (!isCurrent(s) || epoch !== s.dataEpoch) return;
    const seen = new Set((s.columns[key] ?? []).map((todo) => todo.localId));
    const additions = page.items.filter((todo) => !seen.has(todo.localId));
    s.columns[key] = [...(s.columns[key] ?? []), ...additions];
    s.lanes[key] = { hasMore: page.hasMore, nextCursor: page.nextCursor, totalCount: page.totalCount, loading: false, error: false };
    renderBoardRegions(s);
  } catch (err) {
    if (!isCurrent(s) || epoch !== s.dataEpoch || isPublicAbort(err)) return;
    if (isPublicNotFound(err)) {
      enterUnavailable(s);
      return;
    }
    s.lanes[key] = { ...lane, loading: false, error: true };
    renderBoardRegions(s);
  } finally {
    if (s.laneControllers.get(key) === controller) s.laneControllers.delete(key);
  }
}

/**
 * Keeps the public sprint vocabulary consistent with the latest snapshot.
 * The public stream carries no reason, so every successful realtime refresh
 * may reflect a sprint create/rename/delete; refreshes are already coalesced,
 * so this adds at most one /sprints request per refresh. Filter reloads do not
 * change the vocabulary and only fetch after sprints become enabled.
 */
function syncSprintVocabulary(s: PublicSession, kind: 'initial' | 'filter' | 'refresh'): void {
  if (!isCurrent(s)) return;
  if (!s.snapshot.project.sprintsEnabled) {
    s.sprintsSeq++;
    s.sprintsController?.abort();
    s.sprintsController = null;
    s.sprintsLoaded = false;
    if (s.sprints.length > 0) {
      s.sprints = [];
      renderBoardRegions(s);
    }
    if (s.query.sprintNumber != null) clearSprintFilter(s);
    return;
  }
  if (kind === 'filter' && s.sprintsLoaded) return;
  void loadSprints(s);
}

async function loadSprints(s: PublicSession): Promise<void> {
  const seq = ++s.sprintsSeq;
  s.sprintsController?.abort();
  const controller = new AbortController();
  s.sprintsController = controller;
  try {
    const sprints = await fetchPublicSprints(s.slug, { signal: controller.signal });
    if (!isCurrent(s) || seq !== s.sprintsSeq || !s.snapshot.project.sprintsEnabled) return;
    s.sprints = sprints;
    s.sprintsLoaded = true;
    const active = s.query.sprintNumber;
    if (active != null && !sprints.some((sprint) => sprint.number === active)) {
      clearSprintFilter(s);
      return;
    }
    renderBoardRegions(s);
  } catch (err) {
    if (!isCurrent(s) || seq !== s.sprintsSeq || isPublicAbort(err)) return;
    if (isPublicNotFound(err)) {
      enterUnavailable(s);
      return;
    }
    // Transient: keep the previous vocabulary; the next refresh retries.
  } finally {
    if (s.sprintsController === controller) s.sprintsController = null;
  }
}

/**
 * The active sprint no longer exists (or sprints were disabled). The server
 * would keep filtering by that number while the control can no longer show
 * it, so drop only the sprint filter, tell the visitor, and reload.
 */
function clearSprintFilter(s: PublicSession): void {
  if (!isCurrent(s) || s.query.sprintNumber == null) return;
  showToast(t('publicBoard.sprintFilterCleared'));
  setQuery(s, { sprintNumber: null });
  renderBoardRegions(s);
}

// ---- Story detail ----

function dialogContext(s: PublicSession): PublicTodoDialogContext {
  return {
    slug: s.slug,
    priorities: publicPriorityMap(s.snapshot.priorities),
    sprints: s.sprints,
    showPoints: showPublicPoints(s.snapshot),
    markdownEnabled: getMarkdownNotesEnabled(),
    mermaidEnabled: getMermaidNotesEnabled(),
    onNavigateToStory: (localId) => openStoryFromBoard(s, localId),
    onClosedByUser: () => {
      if (!isCurrent(s)) return;
      if (/^\/[^/]+\/t\/\d+\/?$/.test(window.location.pathname)) {
        history.replaceState(history.state ?? {}, '', boardPathWithQuery(s.slug));
      }
    },
  };
}

function openStoryFromBoard(s: PublicSession, localId: number): void {
  if (!isCurrent(s)) return;
  const target = storyPathWithQuery(s.slug, localId);
  if (window.location.pathname + window.location.search !== target) {
    history.pushState({}, '', target);
  }
  void openStory(s, localId);
}

async function openStory(s: PublicSession, localId: number): Promise<void> {
  const result = await openPublicTodo(dialogContext(s), localId);
  if (!isCurrent(s)) {
    closePublicTodo();
    return;
  }
  if (result === 'unavailable' || result === 'failed') {
    showToast(t(result === 'unavailable' ? 'publicBoard.story.unavailable' : 'publicBoard.story.loadFailed'));
    history.replaceState(history.state ?? {}, '', boardPathWithQuery(s.slug));
  }
}

function parseLocalIdSegment(segment: string | null): number | null {
  if (!segment || !/^\d{1,15}$/.test(segment)) return null;
  const n = Number(segment);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

// ---- Realtime and revocation ----

function handleStreamMessage(s: PublicSession, data: unknown): void {
  if (!isCurrent(s) || typeof data !== 'string') return;
  let type: unknown;
  try {
    type = (JSON.parse(data) as { type?: unknown } | null)?.type;
  } catch {
    return;
  }
  if (type === 'refresh_needed') {
    requestRefresh(s);
  } else if (type === 'access_revoked') {
    enterUnavailable(s);
  }
}

function connectStream(s: PublicSession): void {
  const manager = new SseConnectionManager(publicBoardEventsPath(s.slug), {
    label: 'public-board',
    onOpen: () => {
      if (!isCurrent(s)) return;
      // Events are not replayed across reconnects; catch up once per reconnect.
      if (s.streamOpenedOnce) requestRefresh(s);
      s.streamOpenedOnce = true;
    },
    onMessage: (event) => handleStreamMessage(s, event.data),
    // A failed stream is not proof of unpublication; a coalesced public read
    // decides (authoritative 404 → unavailable, otherwise keep content).
    onTransportError: () => {
      if (isCurrent(s)) requestRefresh(s);
    },
  }, PUBLIC_STREAM_POLICY);
  s.stream = manager;
  manager.open();
}

/**
 * Terminal for this view: stops the stream, cancels pending work, clears all
 * public content and story details, and shows a generic unavailable state.
 * Nothing reconnects until fresh navigation resolves the board again.
 */
function enterUnavailable(s: PublicSession): void {
  if (!isCurrent(s)) return;
  session = null;
  teardown(s);
  closePublicTodo();
  s.columns = {};
  s.lanes = {};
  setBoardAccess(null);
  app.innerHTML = buildPublicUnavailableHtml();
  app.querySelector('#publicHomeBtn')?.addEventListener('click', () => window.location.assign('/'));
}

// ---- Entry points ----

/**
 * Attempts the public projection for a slug whose member/temporary read was
 * denied or not found. Returns 'not-public' for any public failure so the
 * caller keeps the existing unavailable/sign-in behavior.
 */
export async function resolvePublicBoard(input: PublicRouteInput): Promise<PublicResolution> {
  stopPublicBoard();
  const seq = resolutionSeq;
  const controller = new AbortController();
  resolutionController = controller;
  let query = readPublicQueryFromUrl();
  let snapshot: PublicBoardSnapshot;
  try {
    try {
      snapshot = await fetchPublicBoardSnapshot(input.slug, query, { signal: controller.signal });
    } catch (err) {
      if (!(err instanceof PublicBoardApiError) || err.kind !== 'invalid_request' || !isPublicQueryActive(query)) throw err;
      // Unsupported filter in the URL: drop it rather than hiding the board.
      query = EMPTY_PUBLIC_QUERY;
      writePublicQueryToUrl(query);
      snapshot = await fetchPublicBoardSnapshot(input.slug, query, { signal: controller.signal });
    }
  } catch {
    return seq === resolutionSeq && getSlug() === input.slug ? 'not-public' : 'stale';
  } finally {
    if (resolutionController === controller) resolutionController = null;
  }
  if (seq !== resolutionSeq || getSlug() !== input.slug) return 'stale';

  // The router has already stopped member-board events for this slug; clear
  // any member board state so no member-only code path sees a board.
  setBoard(null);
  setProjectId(null);
  setBoardAccess(PUBLIC_BOARD_ACCESS);

  const s: PublicSession = {
    slug: input.slug,
    snapshot,
    columns: {},
    lanes: {},
    query,
    sprints: [],
    activeMobileTab: snapshot.workflow[0]?.key ?? '',
    dataEpoch: 0,
    stream: null,
    streamOpenedOnce: false,
    refreshTimer: null,
    refreshInFlight: false,
    refreshQueued: false,
    snapshotController: null,
    laneControllers: new Map(),
    searchTimer: null,
    sprintsLoaded: false,
    sprintsSeq: 0,
    sprintsController: null,
    filterError: false,
    rendered: { filters: '', columns: '', tabs: '' },
  };
  applySnapshot(s, snapshot);
  session = s;
  renderFull(s);
  connectStream(s);
  syncSprintVocabulary(s, 'initial');
  const localId = parseLocalIdSegment(input.openTodoSegment);
  if (localId !== null) await openStory(s, localId);
  return 'rendered';
}

/** In-board navigation (filters in the URL, story deep links, back/forward). */
export async function applyPublicBoardRoute(input: PublicRouteInput): Promise<void> {
  const s = session;
  if (!s || s.slug !== input.slug) return;
  const query = readPublicQueryFromUrl();
  if (!samePublicQuery(query, s.query)) {
    s.query = query;
    renderFull(s);
    void reloadSnapshot(s, 'filter');
  }
  const localId = parseLocalIdSegment(input.openTodoSegment);
  if (localId === null) {
    closePublicTodo();
  } else if (getOpenPublicTodoLocalId() !== localId) {
    await openStory(s, localId);
  }
}

export function __getPublicBoardSessionForTest(): Readonly<PublicSession> | null {
  return session;
}

export function __requestPublicRefreshForTest(): void {
  if (session) requestRefresh(session);
}

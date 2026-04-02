import { app, settingsDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { fetchProjectMembers, invalidateMembersCache } from '../members-cache.js';
import { navigate } from '../router.js';
import { escapeHTML, showToast, renderUserAvatar, renderAvatarContent, processImageFile, sanitizeHexColor } from '../utils.js';
import {
  getBoard,
  getAuthStatusAvailable,
  getProjectId,
  getMobileTab,
  getSlug,
  getTag,
  getSearch,
  getSprintIdFromUrl,
  getEditingTodo,
  getTagColors,
  getUser,
  getBoardLaneMeta,
  getLaneDisplayCount,
  getBoardMembers,
} from '../state/selectors.js';
import {
  setProjectId,
  setBoard,
  setSlug,
  setTag,
  setSearch,
  setOpenTodoSegment,
  setMobileTab,
  setTagColors,
  setSettingsActiveTab,
  setBoardMembers,
  setBoardLaneMeta,
  setLaneLoading,
  appendLaneTodos,
} from '../state/mutations.js';
import { isAnonymousBoard } from '../utils.js';
import { openTodoDialog } from '../dialogs/todo.js';
import { renderSettingsModal } from '../dialogs/settings.js';
import { initDnD, columnsSpec, setDnDColumns, dragInProgress, dragJustEnded } from '../features/drag-drop.js';
import { setContextMenuStatus, setContextMenuRole } from '../features/context-menu-button.js';
import type { BoardMember } from '../state/state.js';
import { Board, Todo, MobileTab, TodoStatus, LanePageResponse } from '../types.js';
import { registerBoardRefresher, registerSprintsRefresher, invalidateBoard, getBoardLimitPerLaneFloor, resetBoardLimitPerLaneFloor } from '../orchestration/board-refresh.js';
import { normalizeSprints } from '../sprints.js';
import { emit, on, off } from '../events.js';
import {
  getLastBoardInteractionTimestamp,
  getLastLocalMutationTimestamp,
  recordBoardInteraction,
  recordLocalMutation,
} from '../realtime/guard.js';

// Symbol for idempotent listener attachment
const BOUND_FLAG = Symbol('bound');
const HIGHLIGHT_CLASS = "card--highlight";

// Global variable to track user's role in current project
let currentUserProjectRole: string | null = null;
// Track last project ID we fetched members for to prevent duplicate fetches
let lastFetchedProjectId: number | null = null;
let boardLoadSequence = 0;
let resolverController: AbortController | null = null;
let highlightRafId: number | null = null;
let highlightTimeoutId: ReturnType<typeof setTimeout> | null = null;
let boardEventsSource: EventSource | null = null;
let boardEventsSlug: string | null = null;
let realtimeRefetchTimeoutId: ReturnType<typeof setTimeout> | null = null;
let realtimeForceRefreshTimeoutId: ReturnType<typeof setTimeout> | null = null;
let pendingRealtimeRefreshSlug: string | null = null;
let boardPointerInteractionActive = false;
let boardPointerReleaseTimeoutId: ReturnType<typeof setTimeout> | null = null;
let todoDialogOpeningInProgress = false;
let boardInteractionListenersBound = false;
const realtimeRefetchDebounceMs = 400;
const localRealtimeGuardMs = 1500;
const boardInteractionGuardMs = 400;
const maxRefreshDelayMs = 2000;
/** Timestamp of last successful board load; used to gate SSE onopen refetch. */
let lastBoardLoadTimestamp = 0;
/** Slug of last successful board load; used for slug-aware onopen guard. */
let lastSuccessfulBoardLoadSlug: string | null = null;
/** Slug for which initial load is in flight; onopen must not refetch while this is set. */
let initialBoardLoadInFlight: string | null = null;
const SSE_ONOPEN_SKIP_MS = 2000;

const DEBUG_BOARD_LOAD = typeof localStorage !== "undefined" && localStorage.getItem("scrumboy_debug_board_load") === "1";
function debugLog(msg: string, slug?: string | null): void {
  if (DEBUG_BOARD_LOAD) {
    console.log(`[board-load] ${msg}`, slug != null ? `(slug=${slug})` : "");
  }
}

function getBoardColumns(board: Board): Array<{ key: string; title: string; color?: string; isDone: boolean }> {
  const order = (board as any).columnOrder as Array<{ key: string; name: string; color?: string; isDone?: boolean }> | undefined;
  if (order && order.length > 0) {
    return order.map((c) => ({ key: c.key, title: c.name, color: c.color, isDone: !!c.isDone }));
  }
  return columnsSpec().map((c) => ({ key: c.key, title: c.title, isDone: c.key === "DONE", color: undefined }));
}

type LaneMetaState = { hasMore: boolean; nextCursor: string | null; loading: boolean; totalCount?: number };

function laneMetaKeyCandidates(key: string): string[] {
  const lower = key.toLowerCase();
  const upper = key.toUpperCase();
  const out = [key, lower, upper];
  // Legacy compatibility: map between old status key and workflow key.
  if (lower === "doing" || upper === "IN_PROGRESS") out.push("IN_PROGRESS", "doing");
  return Array.from(new Set(out));
}

function buildLaneMetaFromBoard(board: Board): Record<TodoStatus, LaneMetaState> {
  const rawMeta = (board?.columnsMeta ?? {}) as Record<string, { hasMore?: boolean; nextCursor?: string | null; totalCount?: number }>;
  const keys = new Set<string>();

  getBoardColumns(board).forEach((c) => keys.add(c.key));
  Object.keys(board?.columns ?? {}).forEach((k) => keys.add(k));
  Object.keys(rawMeta).forEach((k) => keys.add(k));

  const out: Record<TodoStatus, LaneMetaState> = {};
  keys.forEach((key) => {
    let source: { hasMore?: boolean; nextCursor?: string | null; totalCount?: number } | undefined;
    for (const candidate of laneMetaKeyCandidates(key)) {
      if (rawMeta[candidate] != null) {
        source = rawMeta[candidate];
        break;
      }
    }
    out[key] = {
      hasMore: source?.hasMore === true,
      nextCursor: source?.nextCursor ?? null,
      loading: false,
      totalCount: source?.totalCount,
    };
  });

  return out;
}

function hasActiveBoardSubsetFilter(tag: string | null | undefined, search: string | null | undefined, sprintId: string | null | undefined): boolean {
  return !!(
    (tag && tag.trim() !== "")
    || (search && search.trim() !== "")
    || (sprintId && sprintId.trim() !== "")
  );
}

function getRequestedBoardLimitPerLane(tag: string | null | undefined, search: string | null | undefined, sprintId: string | null | undefined): number {
  if (!hasActiveBoardSubsetFilter(tag, search, sprintId)) return 20;

  // Preserve the current filtered subset size across a drag-triggered refresh.
  const counts = Array.from(document.querySelectorAll<HTMLElement>(".col__list"))
    .map((el) => el.querySelectorAll("[data-todo-local-id]").length);
  return counts.length > 0 ? Math.max(20, ...counts) : 20;
}

// Mobile chips pagination: structured data and page state (combined tags + sprints)
type ChipType = "tag" | "sprint";
interface ChipData {
  type: ChipType;
  id: string;
  name: string;
  active: boolean;
  color: string | null;
  /** True when this sprint chip represents the project's active sprint (state ACTIVE). */
  isActiveSprint?: boolean;
  /** True when this sprint chip represents a closed sprint (state CLOSED). */
  isClosedSprint?: boolean;
  /** True when this sprint chip represents a planned sprint (state PLANNED). */
  isPlannedSprint?: boolean;
}
let lastDisplayChipData: ChipData[] = [];
/** Cached members lookup; rebuilt when members change. Avoids repeated Object.fromEntries during render. */
let membersByUserIdCache: Record<number, BoardMember> = {};
let membersByUserIdCacheSource: BoardMember[] | null = null;

function getMembersByUserId(): Record<number, BoardMember> {
  const members = getBoardMembers();
  if (
    members !== membersByUserIdCacheSource ||
    membersByUserIdCacheSource?.length !== members.length
  ) {
    membersByUserIdCacheSource = members;
    membersByUserIdCache = Object.fromEntries(members.map((m) => [m.userId, m]));
  }
  return membersByUserIdCache;
}

type SprintChipData = { sprints: { id: number; number: number; name: string; state?: string; todoCount?: number }[]; unscheduledCount?: number };
let lastSprintsData: SprintChipData | null = null;
let lastSprintsDataSlug: string | null = null;
let lastRenderedChipsHTML = "";
/** Lightweight render signature for updateBoardContent skip; avoids stale UI from board-only comparison. */
let lastUpdateBoardContentBoard: Board | null = null;
let lastUpdateBoardContentTag = "";
let lastUpdateBoardContentSearch = "";
let lastUpdateBoardContentSprintId: string | null = null;
let mobileTagPage = 0;
let mobileTagPageBoundaries: number[] = [];
let mobileTagPaginationResizeBound = false;

// Declare renderProjects function (will be available after Step 2)
declare function renderProjects(): Promise<void>;

// Runtime access to renderProjects from projects view (after Step 2)
// For now, we'll use a dynamic import that will work once projects.js exists
async function getRenderProjects(): Promise<() => Promise<void>> {
  try {
    // @ts-ignore - projects.js will exist after Step 2
    const projectsModule = await import('./projects.js');
    return projectsModule.renderProjects;
  } catch {
    return (window as any).renderProjects || renderProjects;
  }
}

type RealtimeEvent = {
  type?: string;
  projectId?: number;
};

function clearRealtimeRefetchTimer(): void {
  if (realtimeRefetchTimeoutId !== null) {
    clearTimeout(realtimeRefetchTimeoutId);
    realtimeRefetchTimeoutId = null;
  }
}

function clearRealtimeForceRefreshTimer(): void {
  if (realtimeForceRefreshTimeoutId !== null) {
    clearTimeout(realtimeForceRefreshTimeoutId);
    realtimeForceRefreshTimeoutId = null;
  }
}

function clearPendingRealtimeRefresh(): void {
  pendingRealtimeRefreshSlug = null;
  clearRealtimeRefetchTimer();
  clearRealtimeForceRefreshTimer();
}

function clearBoardPointerReleaseTimer(): void {
  if (boardPointerReleaseTimeoutId !== null) {
    clearTimeout(boardPointerReleaseTimeoutId);
    boardPointerReleaseTimeoutId = null;
  }
}

function getLocalRealtimeGuardRemaining(): number {
  return Math.max(0, localRealtimeGuardMs - (Date.now() - getLastLocalMutationTimestamp()));
}

function getBoardInteractionGuardRemaining(): number {
  return Math.max(0, boardInteractionGuardMs - (Date.now() - getLastBoardInteractionTimestamp()));
}

function isRealtimeRefreshBlocked(): boolean {
  return dragInProgress
    || boardPointerInteractionActive
    || todoDialogOpeningInProgress
    || getLocalRealtimeGuardRemaining() > 0
    || getBoardInteractionGuardRemaining() > 0;
}

function getRealtimeRefreshDelay(): number {
  const guardRemaining = Math.max(getLocalRealtimeGuardRemaining(), getBoardInteractionGuardRemaining());
  return Math.max(realtimeRefetchDebounceMs, guardRemaining);
}

function scheduleRealtimeRefreshAttempt(delay: number): void {
  clearRealtimeRefetchTimer();
  realtimeRefetchTimeoutId = setTimeout(() => {
    realtimeRefetchTimeoutId = null;
    flushPendingRealtimeRefresh();
  }, delay);
}

function ensureRealtimeForceRefreshTimer(): void {
  if (pendingRealtimeRefreshSlug == null || realtimeForceRefreshTimeoutId !== null) return;
  realtimeForceRefreshTimeoutId = setTimeout(() => {
    realtimeForceRefreshTimeoutId = null;
    flushPendingRealtimeRefresh(true);
  }, maxRefreshDelayMs);
}

function flushPendingRealtimeRefresh(force = false): void {
  const slug = pendingRealtimeRefreshSlug;
  if (!slug) {
    clearRealtimeRefetchTimer();
    clearRealtimeForceRefreshTimer();
    return;
  }

  if (getSlug() !== slug) {
    debugLog("flushPendingRealtimeRefresh dropped pending slug mismatch", slug);
    clearPendingRealtimeRefresh();
    return;
  }

  if (!force && isRealtimeRefreshBlocked()) {
    debugLog("flushPendingRealtimeRefresh deferred because interaction guard active", slug);
    scheduleRealtimeRefreshAttempt(getRealtimeRefreshDelay());
    ensureRealtimeForceRefreshTimer();
    return;
  }

  clearPendingRealtimeRefresh();
  debugLog(force ? "flushPendingRealtimeRefresh forcing invalidateBoard" : "flushPendingRealtimeRefresh running invalidateBoard", slug);
  invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl()).catch((err: any) => {
    console.warn("Realtime board refresh failed:", err?.message || err);
  });
}

function disconnectBoardEvents(): void {
  clearPendingRealtimeRefresh();
  clearBoardPointerReleaseTimer();
  boardPointerInteractionActive = false;
  todoDialogOpeningInProgress = false;
  if (boardEventsSource) {
    boardEventsSource.close();
    boardEventsSource = null;
  }
  boardEventsSlug = null;
}

function refetchBoardFromRealtime(slug: string): void {
  if (pendingRealtimeRefreshSlug === slug) return;
  pendingRealtimeRefreshSlug = slug;
  debugLog("refetchBoardFromRealtime queued invalidateBoard", slug);
  scheduleRealtimeRefreshAttempt(getRealtimeRefreshDelay());
  ensureRealtimeForceRefreshTimer();
}

function connectBoardEvents(slug: string): void {
  if (boardEventsSource && boardEventsSlug === slug) {
    debugLog("connectBoardEvents skipped (already connected for slug)", slug);
    return;
  }
  disconnectBoardEvents();
  debugLog("connectBoardEvents running", slug);

  const source = new EventSource(`/api/board/${slug}/events`);
  boardEventsSource = source;
  boardEventsSlug = slug;

  source.onopen = () => {
    debugLog("SSE onopen fired", slug);
    if (boardEventsSource !== source || getSlug() !== slug) {
      debugLog("SSE onopen refetch skipped: source/slug mismatch", slug);
      return;
    }
    // Slug-aware: do not refetch if we navigated to a different board.
    if (lastSuccessfulBoardLoadSlug !== slug) {
      debugLog("SSE onopen refetch skipped: lastSuccessfulBoardLoadSlug !== slug", slug);
      return;
    }
    // Do not refetch while initial load for this slug is still in flight.
    if (initialBoardLoadInFlight === slug) {
      debugLog("SSE onopen refetch skipped: initialBoardLoadInFlight for slug", slug);
      return;
    }
    // Skip refetch if we just loaded (avoids duplicate fetch ~400ms after navigation).
    if (Date.now() - lastBoardLoadTimestamp < SSE_ONOPEN_SKIP_MS) {
      debugLog("SSE onopen refetch skipped: within SSE_ONOPEN_SKIP_MS", slug);
      return;
    }
    refetchBoardFromRealtime(slug);
  };

  source.onmessage = (event: MessageEvent) => {
    if (boardEventsSource !== source || getSlug() !== slug) return;
    try {
      const payload = JSON.parse(event.data) as RealtimeEvent;
      const currentProjectID = getProjectId();
      if (typeof payload.projectId === "number" && currentProjectID !== null && payload.projectId !== currentProjectID) {
        return;
      }
      if (payload.type === "members_updated") {
        invalidateMembersCache(payload.projectId!);
        emit("members-updated", { projectId: payload.projectId });
        return;
      }
      if (payload.type === "refresh_needed") {
        refetchBoardFromRealtime(slug);
        return;
      }
      refetchBoardFromRealtime(slug);
    } catch {
      refetchBoardFromRealtime(slug);
    }
  };

  source.onerror = () => {
    // Browser handles reconnect automatically for EventSource.
    if (boardEventsSource !== source) return;
  };
}

export function stopBoardEvents(): void {
  disconnectBoardEvents();
}

// Helper function for tag color
function getTagColor(tagName: string): string | null {
  return getTagColors()[tagName] || null;
}

function isModifiedFibonacciModeEnabled(): boolean {
  const mode = getBoard()?.project?.estimationMode;
  return mode == null || mode === "MODIFIED_FIBONACCI";
}

// Set tag parameter in URL
function setTagParam(tag: string): void {
  const url = new URL(window.location.href);
  if (tag) url.searchParams.set("tag", tag);
  else url.searchParams.delete("tag");
  history.replaceState({}, "", url.pathname + url.search);
}

// Set sprint filter in URL. null = no sprint filter (omit param). "scheduled" = Scheduled (sprint_id IS NOT NULL).
// "unscheduled" = sprint_id IS NULL. Numeric string = specific sprint.
function setSprintParam(sprintId: string | null): void {
  const url = new URL(window.location.href);
  if (sprintId) url.searchParams.set("sprintId", sprintId);
  else url.searchParams.delete("sprintId");
  history.replaceState({}, "", url.pathname + url.search);
}

// Set search parameter in URL
function setSearchParam(search: string): void {
  const url = new URL(window.location.href);
  if (search) url.searchParams.set("search", search);
  else url.searchParams.delete("search");
  history.replaceState({}, "", url.pathname + url.search);
}

function clearResolverRequest(): void {
  if (resolverController) {
    resolverController.abort();
    resolverController = null;
  }
}

export function abortTodoResolverRequest(): void {
  clearResolverRequest();
}

function replaceBoardPath(slug: string): void {
  const url = new URL(window.location.href);
  const qs = url.search ? url.search : "";
  history.replaceState({}, "", `/${slug}${qs}`);
}

function findTodoInBoardByLocalId(localId: number): Todo | null {
  const board = getBoard();
  if (!board || !board.columns) return null;
  const cols = board.columns;
  for (const c of Object.keys(cols) as Array<keyof typeof cols>) {
    const todos = cols[c] || [];
    const t = todos.find((x) => x.localId === localId);
    if (t) return t;
  }
  return null;
}

function isSameEditingTodo(localId: number): boolean {
  return (getEditingTodo()?.localId || null) === localId;
}

function scheduleCardHighlight(todo: Todo): void {
  if (highlightRafId !== null) {
    cancelAnimationFrame(highlightRafId);
    highlightRafId = null;
  }
  if (highlightTimeoutId !== null) {
    clearTimeout(highlightTimeoutId);
    highlightTimeoutId = null;
  }
  const localId = todo.localId;
  highlightRafId = requestAnimationFrame(() => {
    highlightRafId = null;
    if (!isSameEditingTodo(localId)) return;
    const el = (document.querySelector(`[data-todo-local-id="${localId}"]`) ||
      document.getElementById(`todo_${todo.id}`)) as HTMLElement | null;
    if (!el) return;
    el.scrollIntoView({ behavior: "smooth", block: "nearest" });
    el.classList.add(HIGHLIGHT_CLASS);
    highlightTimeoutId = setTimeout(() => {
      highlightTimeoutId = null;
      el.classList.remove(HIGHLIGHT_CLASS);
    }, 2000);
  });
}

function getCombinedChipData(
  displayTags: { name: string; color?: string }[],
  activeTag: string,
  lastSprintsData: SprintChipData | null,
  activeSprintId: string | null
): ChipData[] {
  if (activeSprintId === "assigned") activeSprintId = "scheduled";
  const out: ChipData[] = [];
  out.push({ type: "tag", id: "", name: "All", active: activeTag === "", color: null });
  for (const t of displayTags) {
    out.push({ type: "tag", id: t.name, name: t.name, active: activeTag === t.name, color: (t.color || getTagColor(t.name)) || null });
  }
  if (lastSprintsData) {
    // Sprint "All" (no sprint filter) is not shown; tag "All" already clears sprint on click.
    out.push({ type: "sprint", id: "scheduled", name: "Scheduled", active: activeSprintId === "scheduled", color: null });
    out.push({ type: "sprint", id: "unscheduled", name: "Unscheduled", active: activeSprintId === "unscheduled", color: null });
    const seenSprintIds = new Set<number>();
    const nameCount = new Map<string, number>();
    for (const s of lastSprintsData.sprints) nameCount.set(s.name, (nameCount.get(s.name) ?? 0) + 1);
    for (const s of lastSprintsData.sprints) {
      if (seenSprintIds.has(s.id)) continue;
      seenSprintIds.add(s.id);
      const label = (nameCount.get(s.name) ?? 0) > 1 ? `${s.name} (${s.number})` : s.name;
      const isActiveSprint = s.state === "ACTIVE";
      const isClosedSprint = s.state === "CLOSED";
      const isPlannedSprint = s.state === "PLANNED";
      out.push({ type: "sprint", id: String(s.number), name: label, active: activeSprintId === String(s.number), color: null, isActiveSprint, isClosedSprint, isPlannedSprint });
    }
  }
  return out;
}

function buildChipHTML(d: ChipData): string {
  const activeClass = d.active ? "chip--active" : "";
  const label = escapeHTML(d.name);
  if (d.type === "tag") {
    const safe = sanitizeHexColor(d.color);
    const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20;"` : "";
    return `<button class="chip ${activeClass}" data-tag="${escapeHTML(d.id)}" ${colorStyle}>${label}</button>`;
  }
  if (d.id === "__all__") {
    return `<button class="chip chip--sprint ${activeClass}" data-sprint-clear="1">${label}</button>`;
  }
  const activeSprintClass = d.isActiveSprint ? " chip--active-sprint" : "";
  const closedSprintClass = d.isClosedSprint ? " chip--closed-sprint" : "";
  const plannedSprintClass = d.isPlannedSprint ? " chip--planned-sprint" : "";
  return `<button class="chip chip--sprint${activeSprintClass}${closedSprintClass}${plannedSprintClass} ${activeClass}" data-sprint-id="${escapeHTML(d.id)}">${label}</button>`;
}

function buildChipsHTML(data: ChipData[]): string {
  return data.map(buildChipHTML).join("");
}

/**
 * Chips-only update for the deferred sprints callback. Updates only #tagChips contents.
 * Does not touch board, filters wrapper, DnD, or board-level listeners.
 * Use updateBoardContent for full board+filters updates (SSE, filter change, search).
 */
function updateChipsOnly(sprintId: string | null): void {
  const board = getBoard();
  if (!board) return;
  const isAnonymousTempBoard = board.project.expiresAt != null && board.project.creatorUserId == null;
  const displayTags = isAnonymousTempBoard
    ? board.tags.filter((t) => t.count > 0)
    : board.tags;
  const tag = getTag();
  const combinedChipData = getCombinedChipData(displayTags, tag || "", lastSprintsData, sprintId ?? null);
  lastDisplayChipData = combinedChipData;
  const chipsHTML = buildChipsHTML(combinedChipData);
  if (chipsHTML === lastRenderedChipsHTML) return;
  lastRenderedChipsHTML = chipsHTML;
  const tagChipsEl = document.getElementById("tagChips");
  if (tagChipsEl) {
    tagChipsEl.innerHTML = chipsHTML;
    attachChipsDelegatedHandler();
    initMobileTagPagination();
  }
}

/**
 * UI sync helper: patch local sprint state and re-render chips when a sprint is activated or closed.
 * Called from the sprint-updated event handler. Safe for number or string sprintId.
 */
export function notifySprintStateChanged(sprintId: number | string, newState: 'ACTIVE' | 'CLOSED'): void {
  if (!lastSprintsData || getSlug() !== lastSprintsDataSlug) return;
  const id = Number(sprintId);
  const sprint = lastSprintsData.sprints.find((s) => s.id === id);
  if (!sprint) return;
  if (sprint.state === newState) return;
  sprint.state = newState;
  updateChipsOnly(getSprintIdFromUrl());
}

let sprintEventSubscribed = false;
function ensureSprintSubscription(): void {
  if (sprintEventSubscribed) return;
  sprintEventSubscribed = true;
  on("sprint-updated", (payload: { sprintId?: number | string; state?: string } | undefined) => {
    if (payload && payload.sprintId != null && (payload.state === "ACTIVE" || payload.state === "CLOSED")) {
      notifySprintStateChanged(payload.sprintId, payload.state);
    }
  });
}

function isTrackedBoardPointerEvent(event: PointerEvent): boolean {
  if (event.isPrimary === false) return false;
  if (event.pointerType === "mouse") return true;
  if (event.pointerType) return false;
  return typeof window !== "undefined"
    && typeof window.matchMedia === "function"
    && window.matchMedia("(pointer: fine)").matches;
}

function isBoardInteractionTarget(target: EventTarget | null): target is Element {
  return target instanceof Element && target.closest(".card, .col__list, [data-load-more]") != null;
}

function attachBoardInteractionListeners(): void {
  if (boardInteractionListenersBound) return;
  boardInteractionListenersBound = true;

  const finishPointerInteraction = () => {
    clearBoardPointerReleaseTimer();
    if (!boardPointerInteractionActive) return;
    boardPointerInteractionActive = false;
    recordBoardInteraction();
    flushPendingRealtimeRefresh();
  };

  document.addEventListener("pointerdown", (event: PointerEvent) => {
    if (!isTrackedBoardPointerEvent(event)) return;
    if (!isBoardInteractionTarget(event.target)) return;
    clearBoardPointerReleaseTimer();
    boardPointerInteractionActive = true;
    recordBoardInteraction();
  }, true);

  document.addEventListener("pointerup", () => {
    if (!boardPointerInteractionActive) return;
    clearBoardPointerReleaseTimer();
    boardPointerReleaseTimeoutId = setTimeout(() => {
      boardPointerReleaseTimeoutId = null;
      finishPointerInteraction();
    }, 0);
  }, true);

  document.addEventListener("pointercancel", finishPointerInteraction, true);
  document.addEventListener("click", finishPointerInteraction, true);
  document.addEventListener("auxclick", finishPointerInteraction, true);
  document.addEventListener("contextmenu", finishPointerInteraction, true);
  document.addEventListener("visibilitychange", () => {
    finishPointerInteraction();
  });
}

/** Attach delegated handlers for card click, load-more, and context menu. Call once when board is created. */
function attachBoardDelegationHandlers(): void {
  const boardEl = document.querySelector(".board");
  if (!boardEl) return;
  attachBoardInteractionListeners();
  if ((boardEl as any)[BOUND_FLAG]) return;
  (boardEl as any)[BOUND_FLAG] = true;

  boardEl.addEventListener("click", (e: Event) => {
    const card = (e.target as HTMLElement).closest("[data-todo-id]");
    if (card) {
      if ((e.target as HTMLElement).closest(".card__drag-handle")) return;
      if (dragInProgress || dragJustEnded) return;
      const id = Number(card.getAttribute("data-todo-id"));
      const todo = findTodoInBoard(id);
      if (todo) openTodoFromCard(todo);
      return;
    }
    const loadMore = (e.target as HTMLElement).closest("[data-load-more]");
    if (loadMore) {
      (document.activeElement as HTMLElement)?.blur();
      const status = loadMore.getAttribute("data-load-more") as TodoStatus;
      if (status) handleLoadMore(status);
      return;
    }
  });

  boardEl.addEventListener("contextmenu", (e: Event) => {
    const colList = (e.target as HTMLElement).closest(".col__list");
    if (!colList) return;
    const contextMenu = document.getElementById("contextMenu");
    if (!contextMenu) return;
    e.preventDefault();
    const status = colList.getAttribute("data-status");
    if (status) {
      setContextMenuStatus(status);
      setContextMenuRole(currentUserProjectRole);
      const contextMenuNewTodo = document.getElementById("contextMenuNewTodo");
      if (contextMenuNewTodo) {
        (contextMenuNewTodo as HTMLElement).style.display =
          isAnonymousBoard(getBoard()) || currentUserProjectRole === "maintainer" ? "" : "none";
      }
      const mouseEvent = e as MouseEvent;
      (contextMenu as HTMLElement).style.display = "block";
      (contextMenu as HTMLElement).style.left = `${mouseEvent.pageX}px`;
      (contextMenu as HTMLElement).style.top = `${mouseEvent.pageY}px`;
    }
  });
}

function attachChipsDelegatedHandler(): void {
  const tagChipsEl = document.getElementById("tagChips");
  if (!tagChipsEl) return;
  tagChipsEl.onclick = (e: MouseEvent) => {
    const chip = (e.target as HTMLElement).closest("[data-tag], [data-sprint-id], [data-sprint-clear]") as HTMLElement | null;
    if (!chip) return;
    const additive = e.ctrlKey || e.metaKey;
    if (chip.hasAttribute("data-tag")) {
      const nextTag = chip.getAttribute("data-tag") ?? "";
      if (additive) {
        setTagParam(nextTag);
      } else {
        setTagParam(nextTag);
        setSprintParam(null);
      }
      loadBoardBySlug(getSlug(), new URL(window.location.href).searchParams.get("tag") ?? "", getSearch(), getSprintIdFromUrl()).catch((err: any) => showToast(err.message));
    } else if (chip.hasAttribute("data-sprint-clear")) {
      if (additive) {
        setSprintParam(null);
      } else {
        setSprintParam(null);
        setTagParam("");
      }
      loadBoardBySlug(getSlug(), new URL(window.location.href).searchParams.get("tag") ?? "", getSearch(), getSprintIdFromUrl()).catch((err: any) => showToast(err.message));
    } else if (chip.hasAttribute("data-sprint-id")) {
      const nextSprint = chip.getAttribute("data-sprint-id") ?? "";
      if (additive) {
        setSprintParam(nextSprint);
      } else {
        setSprintParam(nextSprint);
        setTagParam("");
      }
      loadBoardBySlug(getSlug(), new URL(window.location.href).searchParams.get("tag") ?? "", getSearch(), getSprintIdFromUrl()).catch((err: any) => showToast(err.message));
    }
  };
}

const MOBILE_TAG_BREAKPOINT = 767;
const MOBILE_TAG_ROWS_PER_PAGE = 2;

function initMobileTagPagination(): void {
  const tagChipsEl = document.getElementById("tagChips");
  const chipsNav = document.getElementById("chipsNav");
  if (!tagChipsEl || !chipsNav) return;

  const isMobile = window.matchMedia(`(max-width: ${MOBILE_TAG_BREAKPOINT}px)`).matches;

  // One-time resize listener
  if (!mobileTagPaginationResizeBound) {
    mobileTagPaginationResizeBound = true;
    let resizeTimeout: ReturnType<typeof setTimeout> | null = null;
    window.addEventListener("resize", () => {
      if (resizeTimeout) clearTimeout(resizeTimeout);
      resizeTimeout = setTimeout(() => {
        mobileTagPage = 0;
        initMobileTagPagination();
      }, 150);
    });
  }

  if (!isMobile) {
    // Desktop: all chips already in DOM, hide nav
    chipsNav.classList.remove("is-visible");
    chipsNav.setAttribute("aria-hidden", "true");
    attachChipsDelegatedHandler();
    return;
  }

  if (lastDisplayChipData.length <= 1) {
    chipsNav.classList.remove("is-visible");
    chipsNav.setAttribute("aria-hidden", "true");
    attachChipsDelegatedHandler();
    return;
  }

  // Ensure all chips are in DOM for measurement (e.g. after resize we might have had a slice)
  tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData);

  // Measure: all chips are in DOM, get positions and group by row
  const chipEls = Array.from(tagChipsEl.querySelectorAll<HTMLElement>(".chip"));
  if (chipEls.length === 0) {
    return;
  }

  const rects = chipEls.map((el) => el.getBoundingClientRect());
  const rowTolerance = 2;
  const rows: number[] = [];
  let currentRow = 0;
  let lastTop = rects[0].top;
  for (let i = 0; i < rects.length; i++) {
    if (Math.abs(rects[i].top - lastTop) > rowTolerance) {
      currentRow++;
      lastTop = rects[i].top;
    }
    rows[i] = currentRow;
  }
  const numRows = currentRow + 1;

  // Page boundaries: each page = MOBILE_TAG_ROWS_PER_PAGE rows; boundaries are start indices per page
  mobileTagPageBoundaries = [0];
  for (let p = 1; p * MOBILE_TAG_ROWS_PER_PAGE < numRows; p++) {
    const rowStart = p * MOBILE_TAG_ROWS_PER_PAGE;
    const idx = chipEls.findIndex((_, i) => rows[i] >= rowStart);
    if (idx >= 0) mobileTagPageBoundaries.push(idx);
  }
  mobileTagPageBoundaries.push(chipEls.length);

  const numPages = mobileTagPageBoundaries.length - 1;
  if (numPages <= 1) {
    chipsNav.classList.remove("is-visible");
    chipsNav.setAttribute("aria-hidden", "true");
    attachChipsDelegatedHandler();
    return;
  }

  // Reset to first page when board/filter changed (lastDisplayChipData was just set)
  mobileTagPage = 0;

  // Show only chips for current page
  const start = mobileTagPageBoundaries[mobileTagPage];
  const end = mobileTagPageBoundaries[mobileTagPage + 1];
  tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(start, end));

  chipsNav.classList.add("is-visible");
  chipsNav.setAttribute("aria-hidden", "false");

  const prevBtn = chipsNav.querySelector(".chips-nav__prev") as HTMLButtonElement;
  const nextBtn = chipsNav.querySelector(".chips-nav__next") as HTMLButtonElement;
  prevBtn?.replaceWith(prevBtn.cloneNode(true));
  nextBtn?.replaceWith(nextBtn.cloneNode(true));
  const newPrev = chipsNav.querySelector(".chips-nav__prev") as HTMLButtonElement;
  const newNext = chipsNav.querySelector(".chips-nav__next") as HTMLButtonElement;
  if (newPrev) newPrev.disabled = mobileTagPage === 0;
  if (newNext) newNext.disabled = mobileTagPage === numPages - 1;

  newPrev?.addEventListener("click", () => {
    if (mobileTagPage <= 0) return;
    mobileTagPage--;
    const s = mobileTagPageBoundaries[mobileTagPage];
    const e = mobileTagPageBoundaries[mobileTagPage + 1];
    tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(s, e));
    if (newPrev) newPrev.disabled = mobileTagPage === 0;
    if (newNext) newNext.disabled = mobileTagPage === numPages - 1;
  });
  newNext?.addEventListener("click", () => {
    if (mobileTagPage >= numPages - 1) return;
    mobileTagPage++;
    const s = mobileTagPageBoundaries[mobileTagPage];
    const e = mobileTagPageBoundaries[mobileTagPage + 1];
    tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(s, e));
    if (newPrev) newPrev.disabled = mobileTagPage === 0;
    if (newNext) newNext.disabled = mobileTagPage === numPages - 1;
  });

  attachChipsDelegatedHandler();
}

/** Options for renderTodoCard to avoid per-card lookups. */
type RenderTodoCardOpts = {
  tagColors?: Record<string, string>;
  showPointsMode?: boolean;
};

// Render a single todo card
function renderTodoCard(t: Todo, columnColor?: string, membersByUserId?: Record<number, BoardMember>, opts?: RenderTodoCardOpts): string {
  const showPoints = (opts?.showPointsMode ?? isModifiedFibonacciModeEnabled()) && t.estimationPoints != null;
  const tagColors = opts?.tagColors ?? null;
  const tags = (t.tags || []).map((tagName) => {
    const tagColor = tagColors ? (tagColors[tagName] ?? null) : getTagColor(tagName);
    const safe = sanitizeHexColor(tagColor);
    const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"` : "";
    return `<span class="tag" ${colorStyle}>${escapeHTML(tagName)}</span>`;
  }).join("");
  const borderStyle = columnColor ? ` style="border-color:${escapeHTML(columnColor)}"` : "";
  const assignee = membersByUserId != null && t.assigneeUserId != null ? membersByUserId?.[t.assigneeUserId] : null;
  const avatarHTML = assignee
    ? `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`
    : '';
  const pointsHTML = showPoints ? `<span class="card__points" aria-label="Estimation points">${t.estimationPoints}</span>` : "";
  const footerContent = pointsHTML + avatarHTML;
  return `
    <button class="card card--${t.status.toLowerCase()}"${borderStyle} data-todo-id="${t.id}" data-todo-local-id="${t.localId}"${t.assigneeUserId != null ? ` data-assignee-user-id="${t.assigneeUserId}"` : ""} id="todo_${t.id}" type="button">
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${t.localId}</span>
          <span class="card__title">${escapeHTML(t.title)}</span>
        </div>
        ${tags || footerContent ? `
  <div class="card__tags">
    <span class="card__tags-list">
      ${tags}
    </span>
    <span class="card__badges">
      ${footerContent}
    </span>
  </div>
` : ""}
      </div>
      <div class="card__drag-handle" aria-label="Drag to reorder">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
          <circle cx="4" cy="3" r="1.5"/>
          <circle cx="4" cy="8" r="1.5"/>
          <circle cx="4" cy="13" r="1.5"/>
          <circle cx="12" cy="3" r="1.5"/>
          <circle cx="12" cy="8" r="1.5"/>
          <circle cx="12" cy="13" r="1.5"/>
        </svg>
      </div>
    </button>
  `;
}

/**
 * Patch assignee avatars into cards that were rendered without members.
 * Avoids full board rebuild when members arrive after first paint.
 * Call after setBoardMembers(members) so getMembersByUserId() returns the new lookup.
 */
function hydrateAvatarsOnCards(members: BoardMember[]): void {
  const boardEl = document.querySelector(".board");
  if (!boardEl) return;
  const cards = Array.from(boardEl.querySelectorAll<HTMLElement>("[data-assignee-user-id]"));
  const toHydrate = cards.filter((c) => c.dataset.avatarHydrated !== "1");
  if (toHydrate.length === 0) return;
  const membersByUserId = getMembersByUserId();
  toHydrate.forEach((card) => {
    const assigneeUserId = parseInt(card.getAttribute("data-assignee-user-id") ?? "", 10);
    if (!Number.isFinite(assigneeUserId)) return;
    const assignee = membersByUserId[assigneeUserId];
    if (!assignee) return;
    const avatarHTML = `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`;
    const badges = card.querySelector(".card__badges");
    if (badges) {
      badges.insertAdjacentHTML("beforeend", avatarHTML);
    } else {
      const footer = card.querySelector(".card__footer");
      if (footer) {
        footer.insertAdjacentHTML("beforeend", avatarHTML);
      } else {
        const dragHandle = card.querySelector(".card__drag-handle");
        if (dragHandle) {
          dragHandle.insertAdjacentHTML("afterend", `<div class="card__footer">${avatarHTML}</div>`);
        }
      }
    }
    card.dataset.avatarHydrated = "1";
  });
}

async function runWhileTodoDialogOpening(task: () => Promise<void>): Promise<void> {
  todoDialogOpeningInProgress = true;
  recordBoardInteraction();
  try {
    await task();
  } finally {
    todoDialogOpeningInProgress = false;
    recordBoardInteraction();
    flushPendingRealtimeRefresh();
  }
}

function openTodoFromCard(todo: Todo): void {
  void runWhileTodoDialogOpening(
    () => openTodoDialog({ mode: "edit", todo, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole }),
  ).catch((err: any) => {
    console.warn("Failed to open todo dialog:", err?.message || err);
  });
  setOpenTodoSegment(String(todo.localId));
  const slug = getSlug();
  if (!slug) return;
  const url = new URL(window.location.href);
  const targetPath = `/${slug}/t/${todo.localId}`;
  if (url.pathname === targetPath) return;
  history.pushState({}, "", `${targetPath}${url.search}`);
}

// Load more todos for a lane (targeted column append)
async function handleLoadMore(status: TodoStatus): Promise<void> {
  const slug = getSlug();
  const tag = getTag();
  const search = getSearch();
  const sprintId = getSprintIdFromUrl();
  if (!slug) return;
  const meta = getBoardLaneMeta()[status];
  if (!meta?.hasMore || meta.loading) return;

  setLaneLoading(status, true);
  try {
    const params = new URLSearchParams();
    params.set("limit", "20");
    if (meta.nextCursor) params.set("afterCursor", meta.nextCursor);
    if (tag) params.set("tag", tag);
    if (search) params.set("search", search);
    if (sprintId) params.set("sprintId", sprintId);
    const qs = params.toString();
    const res = await apiFetch<LanePageResponse>(`/api/board/${slug}/lanes/${status}${qs ? `?${qs}` : ""}`);
    const items = res?.items ?? [];
    const nextCursor = res?.nextCursor ?? null;
    const hasMore = res?.hasMore ?? false;

    appendLaneTodos(status, items, nextCursor ?? null, hasMore);

    // Targeted column append: add cards to #list_{status}
    // Card clicks are handled by delegated handler on .board
    const listEl = document.getElementById(`list_${status}`);
    if (listEl) {
      const board = getBoard();
      const columnColor = board?.columnOrder?.find((col) => col.key === status)?.color;
      const membersByUserId = getMembersByUserId();
      const tagColors = getTagColors();
      const showPointsMode = isModifiedFibonacciModeEnabled();
      const cardOpts: RenderTodoCardOpts = { tagColors, showPointsMode };
      items.forEach((t) => {
        const card = document.createElement("div");
        card.innerHTML = renderTodoCard(t, columnColor, membersByUserId, cardOpts);
        const btn = card.firstElementChild;
        if (btn) listEl.appendChild(btn);
      });
    }

    // Update Load more button visibility
    const loadMoreEl = document.querySelector(`[data-load-more="${status}"]`);
    if (loadMoreEl) {
      (loadMoreEl as HTMLElement).style.display = hasMore ? "" : "none";
    }

    // Update column count (total in lane, not displayed count)
    const countEl = document.querySelector(`[data-count-for="${status}"]`);
    if (countEl) {
      countEl.textContent = String(getLaneDisplayCount(status));
    }

    updateMobileTabs();
  } catch (err: any) {
    showToast(err.message || "Failed to load more");
  } finally {
    setLaneLoading(status, false);
    checkMobileLoadMoreVisibility();
  }
}

// Find a todo in the board by ID
function findTodoInBoard(id: number): Todo | null {
  const board = getBoard();
  if (!board || !board.columns) return null;
  const cols = board.columns;
  for (const c of Object.keys(cols) as Array<keyof typeof cols>) {
    const todos = cols[c] || [];
    const t = todos.find((x) => x.id === id);
    if (t) return t;
  }
  return null;
}

// Per-lane scroll handler refs so we can call them on tab switch
const mobileLoadMoreHandlers = new Map<string, () => void>();

// Set up scroll-based visibility for the mobile triangle per lane
function initMobileLoadMoreVisibility(): void {
  if (window.innerWidth > 620) return;
  mobileLoadMoreHandlers.clear();

  document.querySelectorAll<HTMLElement>("[data-load-more]").forEach((loadMoreEl) => {
    const status = loadMoreEl.getAttribute("data-load-more");
    if (!status) return;
    const listEl = document.getElementById(`list_${status}`);
    if (!listEl) return;

    const update = () => {
      const meta = getBoardLaneMeta()[status as TodoStatus];
      const distFromBottom = listEl.scrollHeight - listEl.scrollTop - listEl.clientHeight;
      const atBottom = distFromBottom < 40;
      if (atBottom && meta?.hasMore && !meta?.loading) {
        loadMoreEl.classList.add("visible");
      } else {
        loadMoreEl.classList.remove("visible");
      }
    };

    mobileLoadMoreHandlers.set(status, update);
    listEl.addEventListener("scroll", update, { passive: true });
    update();
  });
}

function checkMobileLoadMoreVisibility(): void {
  mobileLoadMoreHandlers.forEach((fn) => fn());
}

// Update mobile tabs display
function updateMobileTabs(): void {
  const board = getBoard();
  const boardCols = board ? getBoardColumns(board) : columnsSpec().map((c) => ({ key: c.key, title: c.title, isDone: c.key === "DONE" }));
  const firstKey = boardCols[0]?.key || "BACKLOG";
  const slug = getSlug();
  if (!getMobileTab()) {
    // Try to restore last tab for this project from localStorage
    const savedTab = slug ? localStorage.getItem(`mobileTab_${slug}`) : null;
    if (savedTab && boardCols.some((c) => c.key === savedTab)) {
      setMobileTab(savedTab as MobileTab);
    } else {
      setMobileTab(firstKey as MobileTab);
    }
  }

  // Update tab active states
  const tabs = document.querySelectorAll(".mobile-tab");
  tabs.forEach((tab) => {
    const tabKey = tab.getAttribute("data-tab");
    if (tabKey === getMobileTab()) {
      tab.classList.add("mobile-tab--active");
    } else {
      tab.classList.remove("mobile-tab--active");
    }
  });

  // Show/hide columns based on active tab
  const columns = document.querySelectorAll(".board .col");
  columns.forEach((col) => {
    const colKey = col.getAttribute("data-column");
    if (colKey && colKey === getMobileTab()) {
      col.classList.add("col--mobile-active");
    } else {
      col.classList.remove("col--mobile-active");
    }
  });
  checkMobileLoadMoreVisibility();
}

// Handle project image upload
async function handleProjectImageUpload(projectId: number): Promise<void> {
  const input = document.createElement("input");
  input.type = "file";
  input.accept = "image/*";
  input.onchange = async (e) => {
    const file = (e.target as HTMLInputElement).files?.[0];
    if (!file) return;
    try {
      const finalDataUrl = await processImageFile(file);
      recordLocalMutation();
      await apiFetch(`/api/projects/${projectId}`, {
        method: "PATCH",
        body: JSON.stringify({ image: finalDataUrl }),
      });
      syncTopbarFromBoard({ project: { image: finalDataUrl } });
      if (getSlug()) {
        await loadBoardBySlug(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
      } else {
        const renderProjects = await getRenderProjects();
        await renderProjects();
      }
      showToast("Project image updated");
    } catch (err: any) {
      showToast(err?.message ?? String(err) ?? "Upload failed");
    }
  };
  input.click();
}

/** Sync #projectImageBtn children from board.project.image (incremental board updates). */
function syncTopbarFromBoard(board: { project: { image?: string } }): void {
  const btn = document.getElementById("projectImageBtn");
  if (!btn) return;

  const img = btn.querySelector<HTMLImageElement>("img.project-image-topbar") ?? btn.querySelector("img");

  if (board.project.image) {
    const src = board.project.image;
    if (img) {
      img.src = src;
    } else {
      btn.innerHTML = `<img src="${escapeHTML(src)}" alt="" class="project-image-topbar" />`;
    }
  } else {
    if (img) img.remove();
    if (!btn.querySelector(".project-image-topbar-placeholder")) {
      btn.innerHTML = `<span class="project-image-topbar-placeholder">📷</span>`;
    }
  }
}

// Full board + filters update. Use for SSE refresh, filter change, search.
// For chips-only updates (e.g. deferred sprints load), use updateChipsOnly instead.
function updateBoardContent(board: Board, tag: string, search: string, sprintId: string | null): void {
  // Skip full rebuild when render signature matches (board + tag + search + sprintId)
  if (
    board === lastUpdateBoardContentBoard &&
    tag === lastUpdateBoardContentTag &&
    search === lastUpdateBoardContentSearch &&
    sprintId === lastUpdateBoardContentSprintId
  ) {
    return;
  }

  setBoard(board);

  // Update tag colors from board data
  const tagColors = { ...getTagColors() };
  board.tags.forEach(t => {
    if (t.color) {
      tagColors[t.name] = t.color;
    }
  });
  setTagColors(tagColors);

  // Check if this is an anonymous temporary board
  const isAnonymousTempBoard = board.project.expiresAt != null && board.project.creatorUserId == null;
  
  // Filter tags for display: on anonymous boards, only show tags with count > 0
  const displayTags = isAnonymousTempBoard 
    ? board.tags.filter(t => t.count > 0)
    : board.tags;

  const combinedChipData = getCombinedChipData(displayTags, tag || "", lastSprintsData, sprintId ?? null);
  lastDisplayChipData = combinedChipData;
  const chipsHTML = buildChipsHTML(combinedChipData);

  // Chips guard: skip filters DOM and initMobileTagPagination when chips HTML unchanged
  const chipsUnchanged = chipsHTML === lastRenderedChipsHTML;
  if (!chipsUnchanged) {
    lastRenderedChipsHTML = chipsHTML;
    const filtersEl = document.querySelector(".filters");
    if (filtersEl) {
      filtersEl.innerHTML = `
        <div class="filters__label">Tags:</div>
        <div class="chips-wrapper">
          <div class="chips-viewport">
            <div class="chips" id="tagChips">${chipsHTML}</div>
          </div>
          <div class="chips-nav" id="chipsNav" aria-hidden="true">
            <button type="button" class="chips-nav__prev" aria-label="Previous tags">‹</button>
            <button type="button" class="chips-nav__next" aria-label="Next tags">›</button>
          </div>
        </div>
      `;
    }
    initMobileTagPagination();
  }

  // Precompute for card render loop
  const showPointsMode = isModifiedFibonacciModeEnabled();
  const membersByUserId = getMembersByUserId();

  // Update board columns
  const boardEl = document.querySelector(".board");
  if (boardEl) {
    // Remove existing "No results" message if present
    const existingNoResults = boardEl.querySelector(".no-results");
    if (existingNoResults) {
      existingNoResults.remove();
    }

    const boardCols = getBoardColumns(board);
    setDnDColumns(boardCols.map((c) => ({ key: c.key, title: c.title, color: c.color })));
    const cardOpts: RenderTodoCardOpts = { tagColors, showPointsMode };
    boardEl.innerHTML = boardCols
      .map((c) => {
        const todos = board.columns[c.key] || [];
        const isMobileActive = getMobileTab() === c.key;
        const laneMeta = getBoardLaneMeta()[c.key as TodoStatus];
        const showLoadMore = laneMeta?.hasMore && !laneMeta?.loading;
        const displayCount = getLaneDisplayCount(c.key as TodoStatus);
        return `
          <section class="col ${isMobileActive ? "col--mobile-active" : ""}" data-column="${c.key}">
            <div class="col__head col__head--${c.key.toLowerCase()}" ${c.color ? `style="background:${escapeHTML(c.color)};"` : ""}>
              <span class="col__title">${escapeHTML(c.title)}</span>
              <span class="col__count" data-count-for="${c.key}">${displayCount}</span>
            </div>
            <div class="col__list" data-status="${c.key}" id="list_${c.key}">
              ${todos.map((t) => renderTodoCard(t, c.color, membersByUserId, cardOpts)).join("")}
            </div>
            ${showLoadMore ? `<div class="col__load-more" data-load-more="${c.key}"><button class="btn btn--ghost btn--small col__load-more--desktop" type="button">Load more</button><span class="col__load-more--mobile" role="button" tabindex="0" aria-label="Load more">▼</span></div>` : ""}
          </section>
        `;
      })
      .join("");

    // Add "No results" state if search is active and no todos match
    if (search && search.trim() !== "") {
      const totalTodos = Object.values(board.columns).reduce((sum, todos) => sum + todos.length, 0);
      if (totalTodos === 0) {
        const noResults = document.createElement("div");
        noResults.className = "no-results";
        noResults.textContent = `No todos found matching "${escapeHTML(search)}"`;
        boardEl.appendChild(noResults);
      }
    }
  }

  if (currentUserProjectRole === "maintainer" || isAnonymousBoard(board)) {
    initDnD();
  }

  initMobileLoadMoreVisibility();

  // Update mobile tabs
  updateMobileTabs();
  
  // Update mobile tab counts
  const mobileTabsEl = document.getElementById("mobileTabs");
  if (mobileTabsEl) {
    const boardCols = getBoardColumns(board);
    const tabLabels: Record<string, string> = {};
    boardCols.forEach((c) => { tabLabels[c.key] = c.title; });
    
    mobileTabsEl.querySelectorAll(".mobile-tab").forEach((tab) => {
      const tabKey = tab.getAttribute("data-tab");
      if (tabKey && tabLabels[tabKey]) {
        const count = getLaneDisplayCount(tabKey as TodoStatus);
        const textSpan = tab.querySelector(".mobile-tab__text");
        if (textSpan) {
          textSpan.textContent = `${tabLabels[tabKey]} ${count}`;
        } else {
          // Fallback if span doesn't exist yet
          tab.textContent = `${tabLabels[tabKey]} ${count}`;
        }
        // Re-apply active class if needed
        if (tabKey === getMobileTab()) {
          tab.classList.add("mobile-tab--active");
        }
      }
    });
  }

  lastUpdateBoardContentBoard = board;
  lastUpdateBoardContentTag = tag;
  lastUpdateBoardContentSearch = search;
  lastUpdateBoardContentSprintId = sprintId;
}

function renderBoardFromData(board: Board, projectId: number, tag: string, search: string, sprintId: string | null, opts: { backLabel?: string; backHref?: string; minimalTopbar?: boolean } = {}): void {
  const boardCols = getBoardColumns(board);
  setDnDColumns(boardCols.map((c) => ({ key: c.key, title: c.title, color: c.color })));
  // Detect mobile view for placeholder text
  const isMobile = window.innerWidth <= 620;
  const searchPlaceholder = isMobile ? "Search" : "Search todos...";
  const backLabel = opts.backLabel || "← Projects";
  const backHref = opts.backHref || "";
  const minimalTopbar = !!opts.minimalTopbar;
  setProjectId(projectId);
  setBoard(board);

  // Role is now resolved in loadBoardBySlug before calling renderBoardFromData.
  // Initialize DnD if user is maintainer (role already set).
  if (currentUserProjectRole === "maintainer" || isAnonymousBoard(board)) {
    initDnD();
  }

  // Restore saved mobile tab for this project
  const initialCols = getBoardColumns(board);
  const firstColKey = initialCols[0]?.key || "BACKLOG";
  const slug = getSlug();
  if (slug) {
    const savedTab = localStorage.getItem(`mobileTab_${slug}`);
    if (savedTab && initialCols.some((c) => c.key === savedTab)) {
      setMobileTab(savedTab as MobileTab);
    } else {
      // Default to first workflow column if no saved tab
      setMobileTab(firstColKey as MobileTab);
    }
  } else {
    // No slug, use first workflow column
    setMobileTab(firstColKey as MobileTab);
  }

  // Check if we're already on a board page - if so, only update board content
  // We check for the board container, not just the topbar, because projects page also has a topbar
  const existingBoardContainer = document.querySelector(".board");
  if (existingBoardContainer) {
    updateBoardContent(board, tag, search, sprintId);
    syncTopbarFromBoard(board);
    return;
  }

  // Update tag colors from board data
  const tagColors = { ...getTagColors() };
  board.tags.forEach(t => {
    if (t.color) {
      tagColors[t.name] = t.color;
    }
  });
  setTagColors(tagColors);

  // Check if this is an anonymous temporary board (expiresAt IS NOT NULL AND creatorUserId IS NULL)
  const isAnonymousTempBoard = board.project.expiresAt != null && board.project.creatorUserId == null;
  
  // Filter tags for display: on anonymous boards, only show tags with count > 0
  const displayTags = isAnonymousTempBoard 
    ? board.tags.filter(t => t.count > 0)
    : board.tags;

  const combinedChipData = getCombinedChipData(displayTags, tag || "", lastSprintsData, sprintId ?? null);
  lastDisplayChipData = combinedChipData;
  const chipsHTML = buildChipsHTML(combinedChipData);

  // Minimal topbar (used for temporary/anonymous boards): logo, project name, rename (if anonymous temp), New Todo, Settings
  const topbarHTML = minimalTopbar
    ? `
      <div class="topbar">
        <div class="brand">
          <button class="brand-link" id="brandLink" style="background: none; border: none; padding: 0; cursor: pointer;">
            <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
          </button>
        </div>
        ${isAnonymousTempBoard 
          ? (board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" style="width: 32px; height: 32px; pointer-events: none; flex-shrink: 0;" />` : `<span class="project-image-topbar-placeholder" style="width: 32px; height: 32px; flex-shrink: 0;">📷</span>`)
          : ''}
        <div class="brand">${escapeHTML(board.project.name)}</div>
        <div class="spacer"></div>
        <div class="search-input-wrapper">
          <input 
            type="text" 
            id="searchInput" 
            class="search-input" 
            placeholder="${searchPlaceholder}" 
            value="${escapeHTML(search || "")}"
          />
          ${search && search.trim() !== "" ? `<button class="search-clear" id="searchClear" aria-label="Clear search" title="Clear search">✕</button>` : ''}
        </div>
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="Rename project">Rename</button>` : ''}
        ${(isAnonymousTempBoard || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn">New Todo</button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${!getUser() ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="Settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${renderUserAvatar(getUser())}
      </div>
    `
    : `
      <div class="topbar">
        <button class="btn btn--ghost" id="backBtn">${escapeHTML(backLabel)}</button>
        ${isAnonymousTempBoard 
          ? (board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" style="width: 32px; height: 32px; pointer-events: none; flex-shrink: 0;" />` : `<span class="project-image-topbar-placeholder" style="width: 32px; height: 32px; flex-shrink: 0;">📷</span>`)
          : `<button class="project-image-topbar-btn" id="projectImageBtn" title="Change project image">
            ${board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" />` : `<span class="project-image-topbar-placeholder">📷</span>`}
          </button>`}
        <div class="brand">${escapeHTML(board.project.name)}</div>
        <div class="spacer"></div>
        <div class="search-input-wrapper">
          <input 
            type="text" 
            id="searchInput" 
            class="search-input" 
            placeholder="${searchPlaceholder}" 
            value="${escapeHTML(search || "")}"
          />
          ${search && search.trim() !== "" ? `<button class="search-clear" id="searchClear" aria-label="Clear search" title="Clear search">✕</button>` : ''}
        </div>
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="Rename project">Rename</button>` : ''}
        ${(isAnonymousTempBoard || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn">New Todo</button>` : ''}
        ${!isAnonymousTempBoard && currentUserProjectRole === 'maintainer' ? `<button class="btn btn--danger" id="deleteProjectBtn">Delete Project</button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${!getUser() ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="Settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${renderUserAvatar(getUser())}
      </div>
    `;

  app.innerHTML = `
    <div class="page">
      ${topbarHTML}

      <div class="container">
        <div class="filters">
          <div class="filters__label">Tags:</div>
          <div class="chips-wrapper">
            <div class="chips-viewport">
              <div class="chips" id="tagChips">${chipsHTML}</div>
            </div>
            <div class="chips-nav" id="chipsNav" aria-hidden="true">
              <button type="button" class="chips-nav__prev" aria-label="Previous tags">‹</button>
              <button type="button" class="chips-nav__next" aria-label="Next tags">›</button>
            </div>
          </div>
        </div>

        <div class="mobile-board-wrapper">
          <div class="mobile-tabs" id="mobileTabs">
            ${boardCols.map((c) => {
              const tabStyle = c.color ? ` style="--lane-color:${escapeHTML(c.color)};--lane-shadow:${escapeHTML(c.color)}d9;background:${escapeHTML(c.color)};color:#ffffff;"` : "";
              return `
            <button class="mobile-tab ${getMobileTab() === c.key ? "mobile-tab--active" : ""}" data-tab="${c.key}"${tabStyle}><span class="mobile-tab__text">${escapeHTML(c.title)} ${getLaneDisplayCount(c.key as TodoStatus)}</span></button>
            `;
            }).join("")}
            <div id="mobileTabDropZones">
              ${boardCols.map((c) => {
                const dropStyle = c.color ? ` style="--lane-color:${escapeHTML(c.color)};--lane-shadow:${escapeHTML(c.color)}d9;background:${escapeHTML(c.color)};"` : "";
                return `<div id="tab_drop_${c.key}" class="mobile-tab-drop" data-status="${c.key}"${dropStyle}></div>`;
              }).join("")}
            </div>
          </div>

          <div class="board">
          ${(() => {
            const membersByUserId = getMembersByUserId();
            const showPointsMode = isModifiedFibonacciModeEnabled();
            const cardOpts: RenderTodoCardOpts = { tagColors, showPointsMode };
            return boardCols
            .map((c) => {
              const todos = board.columns[c.key] || [];
              const isMobileActive = getMobileTab() === c.key;
              const laneMeta = getBoardLaneMeta()[c.key as TodoStatus];
              const showLoadMore = laneMeta?.hasMore && !laneMeta?.loading;
              return `
                <section class="col ${isMobileActive ? "col--mobile-active" : ""}" data-column="${c.key}">
                  <div class="col__head col__head--${c.key.toLowerCase()}" ${c.color ? `style="background:${escapeHTML(c.color)};"` : ""}>
                    <span class="col__title">${escapeHTML(c.title)}</span>
                    <span class="col__count" data-count-for="${c.key}">${getLaneDisplayCount(c.key as TodoStatus)}</span>
                  </div>
                  <div class="col__list" data-status="${c.key}" id="list_${c.key}">
                    ${todos.map((t) => renderTodoCard(t, c.color, membersByUserId, cardOpts)).join("")}
                  </div>
                  ${showLoadMore ? `<div class="col__load-more" data-load-more="${c.key}"><button class="btn btn--ghost btn--small col__load-more--desktop" type="button">Load more</button><span class="col__load-more--mobile" role="button" tabindex="0" aria-label="Load more">▼</span></div>` : ""}
                </section>
              `;
            })
            .join("");
          })()}
          </div>
        </div>
      </div>
    </div>
  `;

  // Only attach event listeners for elements that exist (anonymous mode omits some)
  const brandLink = document.getElementById("brandLink");
  if (brandLink && !(brandLink as any)[BOUND_FLAG]) {
    brandLink.addEventListener("click", async () => {
      try {
        // Copy current URL to clipboard before navigating
        const currentUrl = window.location.href;
        await navigator.clipboard.writeText(currentUrl);
        // Navigate immediately, toast will show on landing page
        window.location.href = "/?copied=1";
      } catch (err) {
        // Fallback if clipboard API fails (e.g., insecure context)
        window.location.href = "/?copied=0";
      }
    });
    (brandLink as any)[BOUND_FLAG] = true;
  }
  const backBtn = document.getElementById("backBtn");
  if (backBtn && !(backBtn as any)[BOUND_FLAG]) {
    backBtn.addEventListener("click", () => {
      const isRelativePath = !backHref || (!backHref.startsWith("http://") && !backHref.startsWith("https://"));
      if (isRelativePath) {
        navigate(backHref || "/");
        return;
      }
      window.location.href = backHref;
    });
    (backBtn as any)[BOUND_FLAG] = true;
  }
  const projectImageBtn = document.getElementById("projectImageBtn");
  if (projectImageBtn && !(projectImageBtn as any)[BOUND_FLAG]) {
    projectImageBtn.addEventListener("click", async () => {
      await handleProjectImageUpload(projectId);
    });
    (projectImageBtn as any)[BOUND_FLAG] = true;
  }
  const renameProjectBtn = document.getElementById("renameProjectBtn");
  if (renameProjectBtn && !(renameProjectBtn as any)[BOUND_FLAG]) {
    renameProjectBtn.addEventListener("click", async () => {
      // Create a dialog for renaming (consistent with app style)
      const dialog = document.createElement("dialog");
      dialog.className = "dialog";
      dialog.innerHTML = `
        <form method="dialog" class="dialog__form" id="renameProjectForm">
          <div class="dialog__header">
            <div class="dialog__title">Rename Project</div>
            <button class="btn btn--ghost" type="button" id="renameProjectDialogClose" aria-label="Close">✕</button>
          </div>

          <label class="field">
            <div class="field__label">Project Name</div>
            <input 
              type="text" 
              id="renameProjectName" 
              class="input" 
              placeholder="Project name" 
              maxlength="200" 
              value="${escapeHTML(board.project.name)}"
              required 
              autofocus
            />
          </label>

          <div class="dialog__footer">
            <div class="spacer"></div>
            <button type="button" class="btn btn--ghost" id="renameProjectCancel">Cancel</button>
            <button type="submit" class="btn" id="renameProjectSubmit">Rename</button>
          </div>
        </form>
      `;
      document.body.appendChild(dialog);
      (dialog as HTMLDialogElement).showModal();

      const closeBtn = document.getElementById("renameProjectDialogClose");
      const cancelBtn = document.getElementById("renameProjectCancel");
      const form = document.getElementById("renameProjectForm") as HTMLFormElement;
      const nameInput = document.getElementById("renameProjectName") as HTMLInputElement;

      const close = () => {
        document.body.removeChild(dialog);
      };

      if (closeBtn) {
        closeBtn.addEventListener("click", close);
      }
      if (cancelBtn) {
        cancelBtn.addEventListener("click", close);
      }
      dialog.addEventListener("click", (e) => {
        if (e.target === dialog) {
          close();
        }
      });

      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        const newName = nameInput.value.trim();
        if (!newName || newName === board.project.name) {
          close();
          return;
        }

        try {
          recordLocalMutation();
          await apiFetch(`/api/projects/${projectId}`, {
            method: "PATCH",
            body: JSON.stringify({ name: newName }),
          });
          
          // Update the project name in the DOM immediately
          const topbar = document.querySelector(".topbar");
          if (topbar) {
            // Find the .brand element that contains the project name
            // In minimal topbar: first .brand has logo button, second has project name
            // In regular topbar: only one .brand with project name
            const brandElements = Array.from(topbar.querySelectorAll(".brand"));
            // Find the .brand that doesn't contain a button (the one with project name)
            for (const brand of brandElements) {
              if (!brand.querySelector("button")) {
                brand.textContent = newName;
                break;
              }
            }
            // Fallback: if all have buttons, update the last one (shouldn't happen, but safe)
            if (brandElements.length > 0 && brandElements[brandElements.length - 1].querySelector("button")) {
              brandElements[brandElements.length - 1].textContent = newName;
            }
          }
          
          // Update the board state to reflect the new name
          const currentBoard = getBoard();
          if (currentBoard) {
            currentBoard.project.name = newName;
            setBoard(currentBoard);
          }
          
          close();
          showToast("Project renamed");
        } catch (err: any) {
          showToast(err.message);
        }
      });
    });
    (renameProjectBtn as any)[BOUND_FLAG] = true;
  }
  const newTodoBtn = document.getElementById("newTodoBtn");
  if (newTodoBtn && !(newTodoBtn as any)[BOUND_FLAG]) {
    newTodoBtn.addEventListener("click", () => openTodoDialog({ mode: "create", role: currentUserProjectRole }));
    (newTodoBtn as any)[BOUND_FLAG] = true;
  }
  // Setup manage members button event listener (extracted for reuse)
  const setupManageMembersButton = (projId: number, projectName?: string) => {
    const btn = document.getElementById("manageMembersBtn");
    if (btn && !(btn as any)[BOUND_FLAG]) {
      btn.addEventListener("click", async () => {
      try {
        // Remove any existing members dialog first to prevent duplicates
        const existingDialog = document.getElementById("membersDialog");
        if (existingDialog && existingDialog.parentNode) {
          if ((existingDialog as HTMLDialogElement).open) {
            (existingDialog as HTMLDialogElement).close();
          }
          document.body.removeChild(existingDialog);
        }

        // Bypass cache: fetch members directly so modal always shows current state.
        // (fetchProjectMembers can return stale data; maintainers and contributors may see different lists otherwise.)
        const currentMembers = await apiFetch<BoardMember[]>(`/api/projects/${projId}/members`);
        const members: any[] = Array.isArray(currentMembers) ? currentMembers : [];
        const isMaintainer = currentUserProjectRole === "maintainer";
        let available: any[] = [];
        if (isMaintainer) {
          available = await apiFetch(`/api/projects/${projId}/available-users`) as any[];
          if (!Array.isArray(available)) available = [];
        }
        const currentUserId = getUser()?.id;

        // Create dialog
        const dialog = document.createElement("dialog");
        dialog.id = "membersDialog";
        dialog.className = "dialog";
        
        const roleLower = (r: string) => String(r || "").toLowerCase();
        const authorityRoles = ["maintainer"];
        const isRemovableRole = (r: string) => ["contributor", "editor", "viewer"].includes(roleLower(r));
        const isAuthorityRole = (r: string) => authorityRoles.includes(roleLower(r));

        const renderMembersList = () => {
          if (members.length === 0) {
            return '<div class="muted" style="padding: 12px; text-align: center;">No members yet</div>';
          }
          const maintainerCount = members.filter((m: any) => authorityRoles.includes(roleLower(m.role))).length;
          return `
            <div style="max-height: 200px; overflow-y: auto; margin-bottom: 16px;" id="currentMembersListContainer">
              ${members.map((m: any) => {
                const role = roleLower(m.role);
                const canRemove = isMaintainer && (isRemovableRole(role) || (isAuthorityRole(role) && maintainerCount > 1));
                const removeBtn = canRemove
                  ? `<button type="button" class="btn btn--ghost btn--small" data-member-id="${m.userId}" data-member-name="${escapeHTML(m.name)}" title="Remove from project">Remove</button>`
                  : "";
                const isSelf = Number(m.userId) === Number(currentUserId);
                const isLastMaintainer = isAuthorityRole(role) && maintainerCount === 1;
                const canDemoteSelf = isMaintainer && isSelf ? false : true;
                const demoteDisabled = isLastMaintainer || !canDemoteSelf;
                const roleControl = isMaintainer
                  ? `<select class="member-role-select input" data-member-id="${m.userId}" style="min-width: 120px; font-size: 0.875rem;">
                      <option value="viewer" ${role === "viewer" ? "selected" : ""} ${demoteDisabled ? "disabled" : ""}>Viewer</option>
                      <option value="contributor" ${role === "contributor" ? "selected" : ""} ${demoteDisabled ? "disabled" : ""}>Contributor</option>
                      <option value="maintainer" ${role === "maintainer" ? "selected" : ""}>Maintainer</option>
                    </select>`
                  : `<span style="text-transform: capitalize; font-size: 0.875rem; color: var(--text-muted, #6b7280);">${escapeHTML(m.role)}</span>`;
                return `
                <div style="display: flex; justify-content: space-between; align-items: center; padding: 8px 12px; border-bottom: 1px solid var(--border, #e5e7eb);">
                  <div>
                    <div style="font-weight: 500;">${escapeHTML(m.name || String(m.userId))}</div>
                  </div>
                  <div style="display: flex; align-items: center; gap: 12px;">
                    ${roleControl}
                    ${removeBtn}
                  </div>
                </div>
              `;
              }).join('')}
            </div>
          `;
        };

        dialog.innerHTML = `
          <form method="dialog" class="dialog__form" id="addMemberForm">
            <div class="dialog__header">
              <div class="dialog__title">${isMaintainer ? "Manage Members" : "Members"}</div>
              ${projectName ? `<div class="muted" style="font-size: 0.875rem; margin-top: 4px;">Project: ${escapeHTML(projectName)}</div>` : ""}
              <button class="btn btn--ghost" type="button" id="addMemberDialogClose" aria-label="Close">✕</button>
            </div>

            <div style="margin-bottom: 20px;">
              <div style="font-weight: 500; margin-bottom: 8px;">Current Members</div>
              <div id="currentMembersList">${renderMembersList()}</div>
            </div>

            ${isMaintainer ? (available.length > 0 ? `
              <div style="border-top: 1px solid var(--border, #e5e7eb); padding-top: 20px; margin-top: 20px;">
                <div style="font-weight: 500; margin-bottom: 12px;">Add New Member</div>
                <label class="field">
                  <div class="field__label">User</div>
                  <select id="addMemberUser" class="input" required>
                    <option value="">Select a user...</option>
                    ${available.map((u: any) => `<option value="${u.id}">${escapeHTML(u.name)} (${escapeHTML(u.email)})</option>`).join('')}
                  </select>
                </label>

                <label class="field">
                  <div class="field__label">Role</div>
                  <select id="addMemberRole" class="input" required>
                    <option value="viewer">Viewer</option>
                    <option value="contributor" selected>Contributor</option>
                    <option value="maintainer">Maintainer</option>
                  </select>
                </label>
              </div>
            ` : '<div class="muted" style="padding: 12px; text-align: center; border-top: 1px solid var(--border, #e5e7eb); margin-top: 20px; padding-top: 20px;">All users are already members</div>') : ''}

            <div class="dialog__footer">
              <div class="spacer"></div>
              <button type="button" class="btn btn--ghost" id="addMemberCancel">Close</button>
              ${isMaintainer && available.length > 0 ? `<button type="submit" class="btn" id="addMemberSubmit">Add Member</button>` : ''}
            </div>
          </form>
        `;
        document.body.appendChild(dialog);
        (dialog as HTMLDialogElement).showModal();

        const closeBtn = document.getElementById("addMemberDialogClose");
        const cancelBtn = document.getElementById("addMemberCancel");
        const form = document.getElementById("addMemberForm") as HTMLFormElement;
        const userSelect = document.getElementById("addMemberUser") as HTMLSelectElement;
        const roleSelect = document.getElementById("addMemberRole") as HTMLSelectElement;
        const currentMembersList = document.getElementById("currentMembersList");

        // Store references for cleanup
        let isClosed = false;
        
        const handleMembersUpdated = (payload: { projectId?: number }) => {
          if (payload?.projectId !== projId || isClosed) return;
          fetchProjectMembers(projId).then((fresh) => {
            if (isClosed || !Array.isArray(fresh)) return;
            members.length = 0;
            members.push(...fresh);
            setBoardMembers(fresh);
            if (currentMembersList) {
              currentMembersList.innerHTML = renderMembersList();
            }
          }).catch(() => {});
        };
        on("members-updated", handleMembersUpdated);

        const close = () => {
          if (isClosed) return; // Prevent double-closing
          isClosed = true;
          off("members-updated", handleMembersUpdated);
          
          // Explicitly close the dialog before removing it to ensure state is updated
          if (dialog.open) {
            (dialog as HTMLDialogElement).close();
          }
          // Remove from DOM immediately after closing
          if (dialog.parentNode) {
            document.body.removeChild(dialog);
          }
        };

        // Handle dialog's native cancel event (from ESC key)
        // Let the dialog close naturally via native behavior, then clean up DOM
        dialog.addEventListener("cancel", () => {
          // Dialog closes automatically on cancel, just clean up DOM after a brief delay
          setTimeout(() => {
            if (dialog.parentNode) {
              document.body.removeChild(dialog);
            }
          }, 0);
        });

        if (closeBtn) closeBtn.addEventListener("click", close);
        if (cancelBtn) cancelBtn.addEventListener("click", close);
        dialog.addEventListener("click", (e) => {
          if (e.target === dialog) close();
        });

        // Delegated handler for role change (maintainers only)
        if (currentMembersList) {
          currentMembersList.addEventListener("change", async (e) => {
            const select = (e.target as HTMLElement).closest("select.member-role-select");
            if (!select) return;
            const targetUserId = parseInt((select as HTMLSelectElement).getAttribute("data-member-id")!, 10);
            const newRole = (select as HTMLSelectElement).value as "viewer" | "contributor" | "maintainer";
            const member = members.find((m: any) => Number(m.userId) === targetUserId);
            if (!member) return;
            const memberRole = roleLower(member.role);
            const previousRole = (memberRole === "maintainer" || memberRole === "owner") ? "maintainer" : (memberRole === "viewer" ? "viewer" : "contributor");
            if (newRole === previousRole) return; // No-op
            if (previousRole === "maintainer" && (newRole === "contributor" || newRole === "viewer")) {
              if (!confirm(`Demote ${escapeHTML(member.name || "this member")} to ${newRole}?`)) {
                (select as HTMLSelectElement).value = previousRole;
                return;
              }
            }
            try {
              recordLocalMutation();
              const result: any = await apiFetch(`/api/projects/${projId}/members/${targetUserId}`, {
                method: "PATCH",
                body: JSON.stringify({ role: newRole }),
              });
              if (Array.isArray(result)) {
                members.length = 0;
                members.push(...result);
                setBoardMembers(result);
              }
              invalidateMembersCache(projId);
              if (currentMembersList) {
                currentMembersList.innerHTML = renderMembersList();
              }
              showToast("Role updated");
            } catch (err: any) {
              (select as HTMLSelectElement).value = previousRole;
              showToast(err.message || "Failed to update role");
            }
          });
        }

        // Delegated handler for Remove from project (survives re-renders of member list)
        if (currentMembersList) {
          currentMembersList.addEventListener("click", async (e) => {
            const removeBtn = (e.target as HTMLElement).closest("button[data-member-id]");
            if (!removeBtn) return;
            const targetUserId = parseInt(removeBtn.getAttribute("data-member-id")!, 10);
            const name = removeBtn.getAttribute("data-member-name") || "this member";
            if (!confirm(`Remove ${name} from this project?`)) return;
            try {
              recordLocalMutation();
              const result: any = await apiFetch(`/api/projects/${projId}/members/${targetUserId}`, { method: "DELETE" });
              if (Array.isArray(result)) {
                members.length = 0;
                members.push(...result);
                setBoardMembers(result);
              }
              invalidateMembersCache(projId);
              if (currentMembersList) {
                currentMembersList.innerHTML = renderMembersList();
              }
              if (targetUserId === currentUserId) {
                close();
                navigate("/");
                return;
              }
              // Refetch available users so removed member reappears in Add section (if dropdown exists)
              try {
                const availableUsers = await apiFetch(`/api/projects/${projId}/available-users`) as any[];
                available.length = 0;
                available.push(...(Array.isArray(availableUsers) ? availableUsers : []));
                const addMemberSelect = dialog.querySelector("#addMemberUser") as HTMLSelectElement | null;
                if (addMemberSelect) {
                  addMemberSelect.innerHTML = `
                    <option value="">Select a user...</option>
                    ${available.map((u: any) => `<option value="${u.id}">${escapeHTML(u.name)} (${escapeHTML(u.email)})</option>`).join("")}
                  `;
                }
              } catch {
                // Ignore refetch errors
              }
              showToast("Member removed from project");
            } catch (err: any) {
              showToast(err.message || "Failed to remove member");
            }
          });
        }

        if (form && available.length > 0) {
          form.addEventListener("submit", async (e) => {
            e.preventDefault();
            const userId = parseInt(userSelect.value, 10);
            const role = roleSelect.value;

            if (!userId || !role) {
              close();
              return;
            }

            try {
              recordLocalMutation();
              const result: any = await apiFetch(`/api/projects/${projId}/members`, {
                method: "POST",
                body: JSON.stringify({ user_id: userId, role }),
              });
              
              // Update members list from API response
              if (Array.isArray(result)) {
                members.length = 0;
                members.push(...result);
                setBoardMembers(result);
              }
              invalidateMembersCache(projId);
              
              // Remove added user from available list
              const addedUserIndex = available.findIndex((u: any) => u.id === userId);
              if (addedUserIndex >= 0) {
                available.splice(addedUserIndex, 1);
              }

              // Update the UI
              if (currentMembersList) {
                currentMembersList.innerHTML = renderMembersList();
              }

              // Update the user select dropdown
              if (userSelect) {
                userSelect.innerHTML = `
                  <option value="">Select a user...</option>
                  ${available.map((u: any) => `<option value="${u.id}">${escapeHTML(u.name)} (${escapeHTML(u.email)})</option>`).join('')}
                `;
              }

              // Hide add section if no more users available
              if (available.length === 0) {
                const addSection = form.querySelector('div[style*="border-top"]');
                if (addSection) {
                  addSection.outerHTML = '<div class="muted" style="padding: 12px; text-align: center; border-top: 1px solid var(--border, #e5e7eb); margin-top: 20px; padding-top: 20px;">All users are already members</div>';
                }
                const submitBtn = document.getElementById("addMemberSubmit");
                if (submitBtn) {
                  submitBtn.remove();
                }
              }

              showToast("Member added successfully");
            } catch (err: any) {
              showToast(err.message || "Failed to add member");
            }
          });
        }
      } catch (err: any) {
        showToast(err.message || "Failed to load members");
      }
      });
      (btn as any)[BOUND_FLAG] = true;
    }
  };

  setupManageMembersButton(projectId, board.project.name);

  const deleteProjectBtn = document.getElementById("deleteProjectBtn");
  if (deleteProjectBtn && !(deleteProjectBtn as any)[BOUND_FLAG]) {
    deleteProjectBtn.addEventListener("click", async () => {
      if (!confirm("Delete this project and all its todos?")) return;
      try {
        recordLocalMutation();
        await apiFetch(`/api/projects/${projectId}`, { method: "DELETE" });
        navigate("/");
      } catch (err: any) {
        showToast(err.message);
      }
    });
    (deleteProjectBtn as any)[BOUND_FLAG] = true;
  }

  initMobileTagPagination();

  // Add search input handler
  const searchInput = document.getElementById("searchInput") as HTMLInputElement;
  if (searchInput && !(searchInput as any)[BOUND_FLAG]) {
    let searchTimeout: ReturnType<typeof setTimeout> | null = null;
    
    // Function to handle clear button click
    const handleClearClick = () => {
      searchInput.value = "";
      setSearchParam("");
      loadBoardBySlug(getSlug(), getTag(), null, getSprintIdFromUrl()).catch((err: any) => showToast(err.message));
      updateClearButton();
    };
    
    // Function to update clear button visibility
    const updateClearButton = () => {
      const clearBtn = document.getElementById("searchClear");
      const wrapper = searchInput.closest(".search-input-wrapper");
      if (wrapper) {
        const hasValue = searchInput.value.trim() !== "";
        if (hasValue && !clearBtn) {
          // Add clear button
          const btn = document.createElement("button");
          btn.className = "search-clear";
          btn.id = "searchClear";
          btn.setAttribute("aria-label", "Clear search");
          btn.setAttribute("title", "Clear search");
          btn.textContent = "✕";
          btn.addEventListener("click", handleClearClick);
          wrapper.appendChild(btn);
        } else if (!hasValue && clearBtn) {
          // Remove clear button
          clearBtn.remove();
        }
      }
    };
    
    searchInput.addEventListener("input", (e) => {
      const input = e.target as HTMLInputElement;
      const value = input.value; // Keep untrimmed value in input
      updateClearButton();
      clearTimeout(searchTimeout);
      searchTimeout = setTimeout(() => {
        const trimmedValue = value.trim();
        setSearchParam(trimmedValue);
        // loadBoardBySlug will detect existing topbar and only update board content
        loadBoardBySlug(getSlug(), getTag(), trimmedValue || null, getSprintIdFromUrl()).catch((err: any) => showToast(err.message));
      }, 300); // 300ms debounce
    });
    
    // Attach handler to existing clear button (if present from initial render)
    const existingClearBtn = document.getElementById("searchClear");
    if (existingClearBtn) {
      existingClearBtn.addEventListener("click", handleClearClick);
    }
    
    // Initialize clear button visibility
    updateClearButton();
    
    (searchInput as any)[BOUND_FLAG] = true;
  }

  const settingsBtn = document.getElementById("settingsBtn");
  if (settingsBtn && !(settingsBtn as any)[BOUND_FLAG]) {
    settingsBtn.addEventListener("click", async () => {
      await renderSettingsModal();
      (settingsDialog as HTMLDialogElement).showModal();
    });
    (settingsBtn as any)[BOUND_FLAG] = true;
  }

  const userAvatarBtn = document.getElementById("userAvatarBtn");
  if (userAvatarBtn && !(userAvatarBtn as any)[BOUND_FLAG]) {
    userAvatarBtn.addEventListener("click", async () => {
      setSettingsActiveTab("profile");
      await renderSettingsModal();
      (settingsDialog as HTMLDialogElement).showModal();
    });
    (userAvatarBtn as any)[BOUND_FLAG] = true;
  }

  // Mobile tab switching
  const mobileTabsEl = document.getElementById("mobileTabs");
  if (mobileTabsEl) {
    mobileTabsEl.querySelectorAll("[data-tab]").forEach((el) => {
      if (!(el as any)[BOUND_FLAG]) {
        el.addEventListener("click", (e) => {
          e.preventDefault();
          const tab = el.getAttribute("data-tab");
          if (tab) {
            setMobileTab(tab as any);
            // Save to localStorage per project
            const slug = getSlug();
            if (slug) {
              localStorage.setItem(`mobileTab_${slug}`, tab);
            }
            updateMobileTabs();
          }
        });
        (el as any)[BOUND_FLAG] = true;
      }
    });
  }

  attachBoardDelegationHandlers();
  initMobileLoadMoreVisibility();

  if (currentUserProjectRole === "maintainer" || isAnonymousBoard(board)) {
    initDnD();
  }

  // Add "No results" state if search is active and no todos match
  if (search && search.trim() !== "") {
    const totalTodos = Object.values(board.columns).reduce((sum, todos) => sum + todos.length, 0);
    if (totalTodos === 0) {
      const boardEl = document.querySelector(".board");
      if (boardEl) {
        const noResults = document.createElement("div");
        noResults.className = "no-results";
        noResults.textContent = `No todos found matching "${escapeHTML(search)}"`;
        boardEl.appendChild(noResults);
      }
    }
  }

  // Initialize mobile tabs display
  updateMobileTabs();
}

// Load board by slug
export async function loadBoardBySlug(slug: string | null, tag: string | null, search: string | null, sprintId: string | null = null): Promise<void> {
  if (!slug) {
    throw new Error("Slug is required");
  }
  debugLog("loadBoardBySlug start", slug);
  clearPendingRealtimeRefresh();
  const requestSeq = ++boardLoadSequence;
  const requestSlug = slug;
  const requestTag = tag || "";
  const requestSearch = search || "";
  const requestSprintId = sprintId ?? null;
  // Clear stale members from prior board immediately; prevents stale data if fetch fails early.
  setBoardMembers([]);
  lastRenderedChipsHTML = "";
  lastUpdateBoardContentBoard = null;
  const params = new URLSearchParams();
  params.set("limitPerLane", String(Math.max(getRequestedBoardLimitPerLane(requestTag, requestSearch, requestSprintId), getBoardLimitPerLaneFloor())));
  if (tag) params.set("tag", tag);
  if (search) params.set("search", search);
  if (requestSprintId) params.set("sprintId", requestSprintId);
  const qs = params.toString() ? `?${params.toString()}` : "";
  let board: Board;
  try {
    board = await apiFetch<Board>(`/api/board/${slug}${qs}`);
  } catch (err: any) {
    if (err?.status === 400 && requestSprintId) {
      const url = new URL(window.location.href);
      url.searchParams.delete("sprintId");
      const newUrl = url.pathname + (url.search ? url.search : "");
      history.replaceState({}, "", newUrl);
      await loadBoardBySlug(slug, tag, search, null);
      return;
    }
    throw err;
  }
  if (requestSeq !== boardLoadSequence) return;
  const currentUrl = new URL(window.location.href);
  const currentPath = currentUrl.pathname.match(/^\/([a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)(?:\/t\/\d+)?\/?$/);
  const currentSlug = currentPath ? currentPath[1] : "";
  const currentTag = currentUrl.searchParams.get("tag") || "";
  const currentSearch = currentUrl.searchParams.get("search") || "";
  const currentSprintId = currentUrl.searchParams.get("sprintId") || null;
  if (currentSlug !== requestSlug || currentTag !== requestTag || currentSearch !== requestSearch || (currentSprintId || null) !== (requestSprintId || null)) return;
  const projectId = board?.project?.id;
  if (!projectId) {
    throw new Error("Invalid board response");
  }
  resetBoardLimitPerLaneFloor();
  setSlug(slug);
  setProjectId(projectId);
  setTag(tag || "");
  setSearch(search || "");
  // Defer sprints — render board first, then load in background
  if (slug !== lastSprintsDataSlug) {
    lastSprintsData = null;
  }
  const effectiveSprintId = requestSprintId;
  // Initialize boardLaneMeta from current board workflow keys (compat with legacy key variants).
  setBoardLaneMeta(buildLaneMetaFromBoard(board));

  // Fetch project members BEFORE rendering so role-dependent buttons appear correctly on first load
  const isTemporary = !!board?.project?.expiresAt;
  const user = getUser();
  if (user && projectId && !isAnonymousBoard(board)) {
    try {
      const members = await fetchProjectMembers(projectId);
      if (requestSeq !== boardLoadSequence || getSlug() !== requestSlug) return;
      setBoardMembers(members);
      const currentMember = members.find((m) => m.userId === user.id);
      currentUserProjectRole = currentMember ? currentMember.role : null;
      lastFetchedProjectId = projectId;
    } catch {
      if (requestSeq !== boardLoadSequence) return;
      setBoardMembers([]);
      currentUserProjectRole = null;
    }
  } else {
    // Reset role for anonymous boards or when not logged in
    currentUserProjectRole = null;
  }

  // Render board with role already resolved
  // Temporary boards:
  // - Full mode: show the same back-to-projects button as durable projects.
  // - Anonymous deployment: never show a back-to-projects path (no project enumeration).
  const showBackToProjects = !!getAuthStatusAvailable();
  renderBoardFromData(board, projectId, tag || "", search || "", effectiveSprintId, {
    backLabel: "← Projects",
    backHref: showBackToProjects ? "/" : "",
    minimalTopbar: isTemporary && !showBackToProjects,
  });
  lastBoardLoadTimestamp = Date.now();
  lastSuccessfulBoardLoadSlug = slug;
  debugLog("loadBoardBySlug end (success)", slug);

  // Note: Avatars are already rendered in renderTodoCard() since members were fetched before rendering.
  // No need to call hydrateAvatarsOnCards() here.

  if (!isAnonymousBoard(board) && lastSprintsDataSlug !== slug) {
    lastSprintsDataSlug = slug;
    apiFetch<SprintChipData | null>(`/api/board/${slug}/sprints`)
      .then((sprintsResp) => {
        if (requestSeq !== boardLoadSequence) return;
        const sprints = normalizeSprints(sprintsResp);
        lastSprintsData = sprints.length > 0 ? { ...(sprintsResp || {}), sprints } : null;
        if (getSlug() === requestSlug) {
          updateChipsOnly(requestSprintId);
        }
      })
      .catch(() => {
        if (requestSeq !== boardLoadSequence) return;
        lastSprintsData = null;
        lastSprintsDataSlug = null;
      });
  }
}

// Register board refresher with orchestration layer
registerBoardRefresher(async (slug: string, tag?: string, search?: string, sprintId?: string | null) => {
  await loadBoardBySlug(slug, tag || null, search || null, sprintId ?? null);
});

// Register sprints-only refresher (chips update without full board reload)
registerSprintsRefresher(async (slug: string) => {
  if (getSlug() !== slug) return;
  try {
    const sprintsResp = await apiFetch<SprintChipData | null>(`/api/board/${slug}/sprints`);
    const sprints = normalizeSprints(sprintsResp);
    lastSprintsData = sprints.length > 0 ? { ...(sprintsResp || {}), sprints } : null;
    lastSprintsDataSlug = slug;
    if (getSlug() === slug) updateChipsOnly(getSprintIdFromUrl());
  } catch {
    lastSprintsData = null;
    lastSprintsDataSlug = null;
  }
});

ensureSprintSubscription();

function clearPendingHighlight(): void {
  if (highlightRafId !== null) {
    cancelAnimationFrame(highlightRafId);
    highlightRafId = null;
  }
  if (highlightTimeoutId !== null) {
    clearTimeout(highlightTimeoutId);
    highlightTimeoutId = null;
  }
}

export function onTodoDialogClosed(): void {
  clearResolverRequest();
  clearPendingHighlight();
  setOpenTodoSegment(null);
}

async function resolveTodoByLocalId(slug: string, localId: number): Promise<Todo> {
  clearResolverRequest();
  const controller = new AbortController();
  resolverController = controller;
  try {
    return await apiFetch<Todo>(`/api/board/${slug}/todos/${localId}`, { signal: controller.signal } as RequestInit);
  } finally {
    if (resolverController === controller) {
      resolverController = null;
    }
  }
}

async function openTodoFromPath(slug: string, openTodoSegment: string): Promise<void> {
  const localId = parseInt(openTodoSegment, 10);
  if (Number.isNaN(localId)) return;
  if (isSameEditingTodo(localId)) return;

  const todoFromBoard = findTodoInBoardByLocalId(localId);
  if (todoFromBoard) {
    await runWhileTodoDialogOpening(
      () => openTodoDialog({ mode: "edit", todo: todoFromBoard, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole }),
    );
    setOpenTodoSegment(String(localId));
    scheduleCardHighlight(todoFromBoard);
    return;
  }

  try {
    const resolved = await resolveTodoByLocalId(slug, localId);
    if (isSameEditingTodo(localId)) return;
    await runWhileTodoDialogOpening(
      () => openTodoDialog({ mode: "edit", todo: resolved, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole }),
    );
    setOpenTodoSegment(String(localId));
  } catch (err: any) {
    if (err?.name === "AbortError") return;
    if (err?.status === 404) {
      showToast("Todo not found");
    } else if (err?.status === 403) {
      showToast("You don't have access to this todo");
    } else {
      showToast("Failed to load todo");
    }
    replaceBoardPath(slug);
    setOpenTodoSegment(null);
  }
}

// Main render function for board view
export async function renderBoard(
  slug: string | null,
  tag: string,
  search: string,
  sprintId: string | null,
  openTodoId: string | null = null,
  openTodoSegment: string | null = null,
  opts: { skipLoad?: boolean; prefetchedBoard?: Board } = {}
): Promise<void> {
  if (!slug) throw new Error("Slug is required");
  debugLog("renderBoard start", slug);
  if (opts.skipLoad) {
    if (!getBoard() || getSlug() !== slug) {
      opts.skipLoad = false;
    }
  }
  if (opts.prefetchedBoard && opts.prefetchedBoard.project?.id) {
    const board = opts.prefetchedBoard;
    const projectId = board.project.id;
    setBoardMembers([]);
    setSlug(slug);
    setProjectId(projectId);
    setTag(tag || "");
    setSearch(search || "");
    if (slug !== lastSprintsDataSlug) {
      lastSprintsData = null;
    }
    setBoardLaneMeta(buildLaneMetaFromBoard(board));
    const isTemporary = !!board?.project?.expiresAt;
    
    // Fetch project members BEFORE rendering so role-dependent buttons appear correctly
    const user = getUser();
    if (user && projectId && !isAnonymousBoard(board)) {
      try {
        const members = await fetchProjectMembers(projectId);
        if (getSlug() !== slug) return;
        setBoardMembers(members);
        const currentMember = members.find((m) => m.userId === user.id);
        currentUserProjectRole = currentMember ? currentMember.role : null;
        lastFetchedProjectId = projectId;
      } catch {
        if (getSlug() !== slug) return;
        setBoardMembers([]);
        currentUserProjectRole = null;
      }
    } else {
      currentUserProjectRole = null;
    }
    
    const showBackToProjects = !!getAuthStatusAvailable();
    renderBoardFromData(board, projectId, tag || "", search || "", sprintId, {
      backLabel: "← Projects",
      backHref: showBackToProjects ? "/" : "",
      minimalTopbar: isTemporary && !showBackToProjects,
    });
    lastBoardLoadTimestamp = Date.now();
    lastSuccessfulBoardLoadSlug = slug;
    if (getSlug() === slug) connectBoardEvents(slug);
    // Note: Avatars are already rendered in renderTodoCard() since members were fetched before rendering.
    // No need to call hydrateAvatarsOnCards() here.
    if (!isAnonymousBoard(board) && lastSprintsDataSlug !== slug) {
      lastSprintsDataSlug = slug;
      apiFetch<SprintChipData | null>(`/api/board/${slug}/sprints`)
        .then((sprintsResp) => {
          if (getSlug() !== slug) return;
          const sprints = normalizeSprints(sprintsResp);
          lastSprintsData = sprints.length > 0 ? { ...(sprintsResp || {}), sprints } : null;
          if (getSlug() === slug) {
            updateChipsOnly(sprintId);
          }
        })
        .catch(() => {
          if (getSlug() === slug) {
            lastSprintsData = null;
            lastSprintsDataSlug = null;
          }
        });
    }
  } else if (!opts.skipLoad) {
    initialBoardLoadInFlight = slug;
    try {
      await loadBoardBySlug(slug, tag, search || null, sprintId);
    } finally {
      initialBoardLoadInFlight = null;
      if (getSlug() === slug) connectBoardEvents(slug);
    }
  }
  if (openTodoSegment) {
    await openTodoFromPath(slug, openTodoSegment);
    return;
  }
  setOpenTodoSegment(null);
  clearResolverRequest();

  if (getEditingTodo()) {
    const dialog = document.getElementById("todoDialog") as HTMLDialogElement | null;
    if (dialog?.open) {
      dialog.close();
    }
  }

  if (openTodoId) {
    const todoId = parseInt(openTodoId, 10);
    if (!Number.isNaN(todoId)) {
      const board = getBoard();
      let opened = false;
      if (board?.columns) {
        const statuses = Object.keys(board.columns) as Array<keyof typeof board.columns>;
        let todo: Todo | undefined;
        for (const st of statuses) {
          const list = board.columns[st] || [];
          todo = list.find((t) => t.id === todoId);
          if (todo) break;
        }
        if (todo) {
          openTodoDialog({ mode: "edit", todo, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole });
          opened = true;
        }
      }
      // Only clean URL when we actually opened the modal (so refresh doesn't re-open)
      if (opened) {
        const url = new URL(window.location.href);
        url.searchParams.delete("openTodoId");
        const newUrl = url.pathname + (url.search ? url.search : "");
        history.replaceState({}, "", newUrl);
      }
    }
  }
}

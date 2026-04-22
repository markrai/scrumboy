import { showToast } from '../utils.js';
import {
  getAuthStatusAvailable,
  getProjectId,
  getSlug,
  getTag,
  getSearch,
  getSprintIdFromUrl,
  getUser,
} from '../state/selectors.js';
import { invalidateMembersCache } from '../members-cache.js';
import { on, off, emit } from '../events.js';
import {
  getLastBoardInteractionTimestamp,
  getLastLocalMutationTimestamp,
  recordBoardInteraction,
  isBulkUpdating,
} from '../realtime/guard.js';
import { playAssignmentSound, showAssignmentDesktopNotification } from '../core/assignmentNotify.js';
import { SseConnectionManager } from '../core/sse-client.js';
import { registerAnonymousSseRestart } from '../core/realtime.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { dragInProgress } from '../features/drag-drop.js';
import { clearTodoMultiSelection, updateBulkEditBar } from './board-selection.js';

const DEBUG_BOARD_LOAD = typeof localStorage !== "undefined" && localStorage.getItem("scrumboy_debug_board_load") === "1";
export function debugLog(msg: string, slug?: string | null): void {
  if (DEBUG_BOARD_LOAD) {
    console.log(`[board-load] ${msg}`, slug != null ? `(slug=${slug})` : "");
  }
}

let boardAnonSseManager: SseConnectionManager | null = null;
let boardEventsSlug: string | null = null;
/** Logged-in path: listen on app bus instead of owning EventSource. */
let boardRealtimeBound = false;

const assignmentTodoDebounceMs = 1500;
const assignmentToastLastByTodoId = new Map<number, number>();
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

type RealtimeTodoAssignedPayload = {
  todoId: number;
  title: string;
  assigneeId: number;
  actorUserId?: number;
};

type RealtimeEvent = {
  id?: string;
  type?: string;
  projectId?: number;
  reason?: string;
  payload?: RealtimeTodoAssignedPayload;
};

function useMergedGlobalRealtime(): boolean {
  return !!(getAuthStatusAvailable() && getUser());
}

// Logged-in boards consume merged realtime from /api/me/realtime. Keep this
// classification aligned with the anonymous board SSE path below:
// - todo.assigned: no board reload here; assignment refresh arrives via the
//   synthetic refresh_needed line emitted by the SSE bridge
// - members_updated: invalidate members cache only
// - refresh_needed and other non-ping project-scoped events: queue a board refetch
function onBoardRealtimeEvent(_payload: unknown): void {
  const slug = boardEventsSlug;
  if (!slug || getSlug() !== slug) return;
  const payload = _payload as RealtimeEvent;
  const currentProjectID = getProjectId();
  if (typeof payload.projectId === 'number' && currentProjectID !== null && payload.projectId !== currentProjectID) {
    return;
  }
  // Assignment toast/sound/unread are handled in core/realtime.ts (after id dedupe).
  if (payload.type === 'todo.assigned') {
    return;
  }
  try {
    if (payload.type === 'members_updated') {
      invalidateMembersCache(payload.projectId!);
      emit('members-updated', { projectId: payload.projectId });
      return;
    }
    // Scrumbaby wall events are consumed by the wall dialog when open; they
    // must NOT invalidate the board. Transient drag events are high-frequency
    // and never hit the durable store, so they are also forwarded as-is for
    // consumers that care (the wall dialog subscribes only while mounted).
    if (payload.type === 'wall.refresh_needed') {
      emit('wall:refresh_needed', { projectId: payload.projectId });
      return;
    }
    if (payload.type === 'wall.transient') {
      emit('wall:transient', payload);
      return;
    }
    if (isBulkUpdating()) return;
    if (payload.type === 'refresh_needed') {
      refetchBoardFromRealtime(slug);
      return;
    }
    refetchBoardFromRealtime(slug);
  } catch {
    if (!isBulkUpdating()) refetchBoardFromRealtime(slug);
  }
}

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

export function clearPendingRealtimeRefresh(): void {
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

// Pending realtime refreshes are slug-scoped. They are dropped after
// navigation, deferred while local interaction guards are active, retried after
// max(realtimeRefetchDebounceMs, guardRemaining), and force-flushed after
// maxRefreshDelayMs so the board eventually reloads.
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

export function disconnectBoardEvents(): void {
  clearPendingRealtimeRefresh();
  clearBoardPointerReleaseTimer();
  boardPointerInteractionActive = false;
  todoDialogOpeningInProgress = false;
  clearTodoMultiSelection();
  updateBulkEditBar();
  if (boardRealtimeBound) {
    off('realtime:event', onBoardRealtimeEvent);
    boardRealtimeBound = false;
  }
  if (boardAnonSseManager) {
    boardAnonSseManager.stop();
    boardAnonSseManager = null;
  }
  boardEventsSlug = null;
}

function refetchBoardFromRealtime(slug: string): void {
  if (isBulkUpdating()) return;
  if (pendingRealtimeRefreshSlug === slug) return;
  pendingRealtimeRefreshSlug = slug;
  debugLog("refetchBoardFromRealtime queued invalidateBoard", slug);
  scheduleRealtimeRefreshAttempt(getRealtimeRefreshDelay());
  ensureRealtimeForceRefreshTimer();
}

export function connectBoardEvents(slug: string): void {
  if (boardEventsSlug === slug && (boardAnonSseManager !== null || boardRealtimeBound)) {
    debugLog("connectBoardEvents skipped (already connected for slug)", slug);
    return;
  }
  disconnectBoardEvents();
  debugLog("connectBoardEvents running", slug);

  boardEventsSlug = slug;

  if (useMergedGlobalRealtime()) {
    on('realtime:event', onBoardRealtimeEvent);
    boardRealtimeBound = true;
    return;
  }

  const url = new URL(`/api/board/${slug}/events`, window.location.origin).toString();
  const manager = new SseConnectionManager(url, {
    label: `board/${slug}/events`,
    // Anonymous-board onopen is conservative: skip reconnect refetches while
    // the initial board load is still in flight, while the manager/slug is
    // stale, or immediately after a successful load for the same slug.
    onOpen: () => {
      debugLog("SSE onopen fired", slug);
      if (isBulkUpdating()) return;
      if (boardAnonSseManager !== manager || getSlug() !== slug) {
        debugLog("SSE onopen refetch skipped: manager/slug mismatch", slug);
        return;
      }
      if (lastSuccessfulBoardLoadSlug !== slug) {
        debugLog("SSE onopen refetch skipped: lastSuccessfulBoardLoadSlug !== slug", slug);
        return;
      }
      if (initialBoardLoadInFlight === slug) {
        debugLog("SSE onopen refetch skipped: initialBoardLoadInFlight for slug", slug);
        return;
      }
      if (Date.now() - lastBoardLoadTimestamp < SSE_ONOPEN_SKIP_MS) {
        debugLog("SSE onopen refetch skipped: within SSE_ONOPEN_SKIP_MS", slug);
        return;
      }
      refetchBoardFromRealtime(slug);
    },
    // Mirror onBoardRealtimeEvent semantics for anonymous board SSE.
    onMessage: (event: MessageEvent) => {
      if (boardAnonSseManager !== manager || getSlug() !== slug) return;
      try {
        const payload = JSON.parse(event.data) as RealtimeEvent;
        if (payload.type === "ping") {
          return;
        }
        const currentProjectID = getProjectId();
        if (typeof payload.projectId === "number" && currentProjectID !== null && payload.projectId !== currentProjectID) {
          return;
        }
        if (payload.type === "todo.assigned") {
          const inner = payload.payload;
          if (!inner || typeof inner.todoId !== "number") {
            return;
          }
          const me = getUser();
          if (!me) {
            return;
          }
          if (Number(inner.assigneeId) !== Number(me.id)) {
            return;
          }
          if (typeof inner.actorUserId === "number" && Number(inner.actorUserId) === Number(me.id)) {
            return;
          }
          const now = Date.now();
          const last = assignmentToastLastByTodoId.get(inner.todoId);
          if (last !== undefined && now - last < assignmentTodoDebounceMs) {
            return;
          }
          assignmentToastLastByTodoId.set(inner.todoId, now);
          const t = typeof inner.title === "string" ? inner.title : "";
          showToast(`Assigned: ${t || "Todo"}`);
          playAssignmentSound();
          showAssignmentDesktopNotification(t || "Todo");
          return;
        }
        if (payload.type === "members_updated") {
          invalidateMembersCache(payload.projectId!);
          emit("members-updated", { projectId: payload.projectId });
          return;
        }
        if (payload.type === 'wall.refresh_needed') {
          emit('wall:refresh_needed', { projectId: payload.projectId });
          return;
        }
        if (payload.type === 'wall.transient') {
          emit('wall:transient', payload);
          return;
        }
        if (isBulkUpdating()) return;
        if (payload.type === "refresh_needed") {
          refetchBoardFromRealtime(slug);
          return;
        }
        refetchBoardFromRealtime(slug);
      } catch {
        if (!isBulkUpdating()) refetchBoardFromRealtime(slug);
      }
    },
  });
  boardAnonSseManager = manager;
  manager.open();
}

// One registration for app lifetime: handler always reads current `boardAnonSseManager` (null when disconnected).
registerAnonymousSseRestart((reason) => {
  if (useMergedGlobalRealtime()) return;
  boardAnonSseManager?.restartRequested(reason);
});

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

export function attachBoardInteractionListeners(): void {
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

export async function runWhileTodoDialogOpening(task: () => Promise<void>): Promise<void> {
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

export function setInitialBoardLoadInFlight(slug: string | null): void {
  initialBoardLoadInFlight = slug;
}

export function markBoardLoadSucceeded(slug: string): void {
  lastBoardLoadTimestamp = Date.now();
  lastSuccessfulBoardLoadSlug = slug;
}

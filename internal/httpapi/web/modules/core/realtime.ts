/**
 * Global EventSource for logged-in users: GET /api/me/realtime
 * Emits deduplicated `realtime:event` on the app bus; handles assignment side effects here (not in board.ts).
 */

import { emit } from '../events.js';
import { getAuthStatusAvailable, getProjectId, getUser } from '../state/selectors.js';
import { showToast } from '../utils.js';
import { playAssignmentSound, showAssignmentDesktopNotification } from './assignmentNotify.js';
import { appendTodoAssignedNotification, incrementUnread } from './notifications.js';
import { isSseDebugEnabled, SseConnectionManager } from './sse-client.js';
import { scheduleResumeResync } from './foreground-resume.js';

const MAX_SEEN_IDS = 500;
const seenEventIds = new Set<string>();

let globalManager: SseConnectionManager | null = null;

let anonymousSseRestart: ((reason: string) => void) | null = null;
let foregroundLifecycleInited = false;

function trimSeenIfNeeded(): void {
  while (seenEventIds.size > MAX_SEEN_IDS) {
    const first = seenEventIds.values().next().value;
    if (first === undefined) break;
    seenEventIds.delete(first);
  }
}

function rememberSeen(id: string): void {
  seenEventIds.add(id);
  trimSeenIfNeeded();
}

type RealtimePayload = {
  id?: string;
  type?: string;
  projectId?: number;
  payload?: { todoId?: number; title?: string; assigneeId?: number; actorUserId?: number };
};

function handleIncomingMessage(ev: MessageEvent): void {
  let parsed: RealtimePayload;
  try {
    parsed = JSON.parse(ev.data) as RealtimePayload;
  } catch {
    return;
  }
  // Defense in depth: SseConnectionManager strips ping before onMessage; never emit bus events for it.
  if (parsed.type === 'ping') {
    return;
  }

  const id = typeof parsed.id === 'string' && parsed.id !== '' ? parsed.id : undefined;
  if (id) {
    if (seenEventIds.has(id)) {
      return;
    }
    rememberSeen(id);
  }

  emit('realtime:event', parsed);
  handleTodoAssignedSideEffects(parsed);
}

/**
 * Single slot updated by board.ts: the callback reads the current anonymous EventSource manager
 * (`boardAnonSseManager`) at call time — not a per-board registration list — so leaving a board or
 * switching slugs does not leave stale restart targets (manager is stopped and nulled in disconnect).
 */
export function registerAnonymousSseRestart(fn: (reason: string) => void): void {
  anonymousSseRestart = fn;
}

/**
 * One-time: visibility / bfcache pageshow / online → debounced global + anonymous SSE restart + resume resync.
 * Idempotent and safe to call from router on load; listeners attach at most once.
 */
export function initForegroundLifecycle(): void {
  if (foregroundLifecycleInited || typeof document === 'undefined') {
    return;
  }
  foregroundLifecycleInited = true;

  if (isSseDebugEnabled()) {
    console.log('[realtime] foreground lifecycle listeners attached (once)');
  }

  const onForeground = (reason: string) => {
    restartGlobalSse(reason);
    try {
      anonymousSseRestart?.(reason);
    } catch {
      /* ignore */
    }
    scheduleResumeResync(reason);
  };

  const onVisibilityChange = () => {
    if (document.visibilityState === 'visible') {
      onForeground('visibility');
    }
  };

  const onPageShow = (ev: PageTransitionEvent) => {
    // Only bfcache restores — avoids churn on routine SPA navigations where some browsers fire pageshow.
    if (!ev.persisted) {
      return;
    }
    onForeground('pageshow-bfcache');
  };

  const onOnline = () => {
    onForeground('online');
  };

  document.addEventListener('visibilitychange', onVisibilityChange);
  window.addEventListener('pageshow', onPageShow);
  window.addEventListener('online', onOnline);
}

/** For tests / diagnostics: true after initForegroundLifecycle attached listeners. */
export function isForegroundLifecycleInitialized(): boolean {
  return foregroundLifecycleInited;
}

/** Recycle global SSE (e.g. from lifecycle handlers). No-op if not logged in or manager missing. */
export function restartGlobalSse(reason: string): void {
  if (!getAuthStatusAvailable() || !getUser() || !globalManager) {
    return;
  }
  globalManager.restartRequested(reason);
}

export function startGlobalRealtime(): void {
  if (!getAuthStatusAvailable() || !getUser()) {
    return;
  }

  if (!globalManager) {
    const url = new URL('/api/me/realtime', window.location.origin).toString();
    globalManager = new SseConnectionManager(url, {
      label: 'me/realtime',
      onMessage: handleIncomingMessage,
    });
  }
  globalManager.open();
}

export function stopGlobalRealtime(): void {
  if (globalManager) {
    globalManager.stop();
    globalManager = null;
  }
  seenEventIds.clear();
}

function handleTodoAssignedSideEffects(parsed: RealtimePayload): void {
  if (parsed.type !== 'todo.assigned') return;
  if (!getAuthStatusAvailable() || !getUser()) return;
  const inner = parsed.payload;
  if (!inner || typeof inner.todoId !== 'number') return;
  const me = getUser()!;
  if (Number(inner.assigneeId) !== Number(me.id)) return;
  // No chime/toast when you assigned the work to yourself.
  if (typeof inner.actorUserId === 'number' && Number(inner.actorUserId) === Number(me.id)) {
    return;
  }

  const t = typeof inner.title === 'string' ? inner.title : '';
  showToast(`Assigned: ${t || 'Todo'}`);
  playAssignmentSound();
  showAssignmentDesktopNotification(t || 'Todo');

  appendTodoAssignedNotification(parsed);

  const pid = typeof parsed.projectId === 'number' ? parsed.projectId : null;
  const cur = getProjectId();
  if (pid !== null && cur !== null && pid === cur) {
    return;
  }
  incrementUnread();
}

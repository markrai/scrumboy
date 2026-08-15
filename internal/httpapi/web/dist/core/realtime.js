/**
 * Global EventSource for logged-in users: GET /api/me/realtime
 * Emits deduplicated `realtime:event` on the app bus; handles assignment and
 * creator-activity toast side effects here (not in board.ts).
 */
import { emit } from '../events.js';
import { getAuthStatusAvailable, getProjectId, getUser } from '../state/selectors.js';
import { showToast } from '../utils.js';
import { playAssignmentSound, showAssignmentDesktopNotification } from './assignmentNotify.js';
import { appendTodoAssignedNotification, incrementUnread } from './notifications.js';
import { isSseDebugEnabled, SseConnectionManager } from './sse-client.js';
import { scheduleResumeResync } from './foreground-resume.js';
import { t } from '../i18n/index.js';
const MAX_SEEN_IDS = 500;
const seenEventIds = new Set();
let globalManager = null;
let anonymousSseRestart = null;
let foregroundLifecycleInited = false;
function trimSeenIfNeeded() {
    while (seenEventIds.size > MAX_SEEN_IDS) {
        const first = seenEventIds.values().next().value;
        if (first === undefined)
            break;
        seenEventIds.delete(first);
    }
}
function rememberSeen(id) {
    seenEventIds.add(id);
    trimSeenIfNeeded();
}
function handleIncomingMessage(ev) {
    let parsed;
    try {
        parsed = JSON.parse(ev.data);
    }
    catch {
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
    handleCreatorActivitySideEffects(parsed);
}
/**
 * Single slot updated by board.ts: the callback reads the current anonymous EventSource manager
 * (`boardAnonSseManager`) at call time — not a per-board registration list — so leaving a board or
 * switching slugs does not leave stale restart targets (manager is stopped and nulled in disconnect).
 */
export function registerAnonymousSseRestart(fn) {
    anonymousSseRestart = fn;
}
/**
 * One-time: visibility / bfcache pageshow / online → debounced global + anonymous SSE restart + resume resync.
 * Idempotent and safe to call from router on load; listeners attach at most once.
 */
export function initForegroundLifecycle() {
    if (foregroundLifecycleInited || typeof document === 'undefined') {
        return;
    }
    foregroundLifecycleInited = true;
    if (isSseDebugEnabled()) {
        console.log('[realtime] foreground lifecycle listeners attached (once)');
    }
    const onForeground = (reason) => {
        restartGlobalSse(reason);
        try {
            anonymousSseRestart?.(reason);
        }
        catch {
            /* ignore */
        }
        scheduleResumeResync(reason);
    };
    const onVisibilityChange = () => {
        if (document.visibilityState === 'visible') {
            onForeground('visibility');
        }
    };
    const onPageShow = (ev) => {
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
export function isForegroundLifecycleInitialized() {
    return foregroundLifecycleInited;
}
/** Recycle global SSE (e.g. from lifecycle handlers). No-op if not logged in or manager missing. */
export function restartGlobalSse(reason) {
    if (!getAuthStatusAvailable() || !getUser() || !globalManager) {
        return;
    }
    globalManager.restartRequested(reason);
}
export function startGlobalRealtime() {
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
export function stopGlobalRealtime() {
    if (globalManager) {
        globalManager.stop();
        globalManager = null;
    }
    seenEventIds.clear();
}
function handleTodoAssignedSideEffects(parsed) {
    if (parsed.type !== 'todo.assigned')
        return;
    if (!getAuthStatusAvailable() || !getUser())
        return;
    const inner = parsed.payload;
    if (!inner || typeof inner.todoId !== 'number')
        return;
    const me = getUser();
    if (Number(inner.assigneeId) !== Number(me.id))
        return;
    // No chime/toast when you assigned the work to yourself.
    if (typeof inner.actorUserId === 'number' && Number(inner.actorUserId) === Number(me.id)) {
        return;
    }
    const title = typeof inner.title === 'string' ? inner.title : '';
    const fallbackTitle = title || t("realtime.todoFallback");
    showToast(t("realtime.assigned", { title: fallbackTitle }));
    playAssignmentSound();
    showAssignmentDesktopNotification(fallbackTitle);
    appendTodoAssignedNotification(parsed);
    const pid = typeof parsed.projectId === 'number' ? parsed.projectId : null;
    const cur = getProjectId();
    if (pid !== null && cur !== null && pid === cur) {
        return;
    }
    incrementUnread();
}
function handleCreatorActivitySideEffects(parsed) {
    if (parsed.type !== 'todo.creator_activity')
        return;
    if (!getAuthStatusAvailable() || !getUser())
        return;
    if (typeof parsed.projectId !== 'number' || parsed.projectId <= 0)
        return;
    if (typeof parsed.projectSlug !== 'string' || parsed.projectSlug === '')
        return;
    const inner = parsed.payload;
    if (!inner || typeof inner.todoId !== 'number' || inner.todoId <= 0)
        return;
    if (typeof inner.localId !== 'number' || inner.localId <= 0)
        return;
    if (inner.activityReason !== 'todo_updated' && inner.activityReason !== 'todo_moved')
        return;
    const title = typeof inner.title === 'string' ? inner.title : '';
    showToast(t('realtime.creatorActivity', { title: title || t('realtime.todoFallback') }));
}
export function __handleIncomingRealtimeMessageForTest(data) {
    handleIncomingMessage(new MessageEvent('message', { data }));
}
export function __resetRealtimeSeenEventIdsForTest() {
    seenEventIds.clear();
}

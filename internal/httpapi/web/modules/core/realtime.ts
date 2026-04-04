/**
 * Single global EventSource for logged-in users: GET /api/me/realtime
 * Emits deduplicated `realtime:event` on the app bus; handles assignment side effects here (not in board.ts).
 */

import { emit } from '../events.js';
import { getAuthStatusAvailable, getProjectId, getUser } from '../state/selectors.js';
import { showToast } from '../utils.js';
import { playAssignmentSound, showAssignmentDesktopNotification } from './assignmentNotify.js';
import { incrementUnread } from './notifications.js';

const MAX_SEEN_IDS = 500;
const seenEventIds = new Set<string>();

let globalES: EventSource | null = null;

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

export function startGlobalRealtime(): void {
  if (!getAuthStatusAvailable() || !getUser()) return;
  if (globalES) return;

  const url = new URL('/api/me/realtime', window.location.origin);
  const es = new EventSource(url.toString());
  globalES = es;

  es.onmessage = (ev: MessageEvent) => {
    let parsed: RealtimePayload;
    try {
      parsed = JSON.parse(ev.data) as RealtimePayload;
    } catch {
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
  };

  es.onerror = () => {
    // Browser reconnects EventSource automatically.
  };
}

export function stopGlobalRealtime(): void {
  if (globalES) {
    globalES.close();
    globalES = null;
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

  const pid = typeof parsed.projectId === 'number' ? parsed.projectId : null;
  const cur = getProjectId();
  if (pid !== null && cur !== null && pid === cur) {
    return;
  }
  incrementUnread();
}

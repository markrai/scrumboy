// Realtime plumbing for the Scrumbaby/Wall feature.
//
// Responsibilities:
//   - GET /wall on demand (initial load and SSE-driven refetch).
//   - Apply transient pointer-position events from other users.
//   - Subscribe / unsubscribe the wall-dialog instance to the shared SSE bus.
//
// The "editing guard" is critical: if a local edit is in progress, we
// deliberately *skip* the DOM-nuking refetch and set a pending flag so the
// editor can flush a single refetch when it's done. The flag itself lives in
// `wall-state` so `wall-edit-controller` can read/write it without a circular
// import.
//
// Public surface:
//   - `refetchDoc({ onApplyDoc })` returns a Promise.
//   - `applyTransient(payload)` is a synchronous DOM patch.
//   - `startRealtime({ slug, onApplyDoc, onApplyTransient })` returns a
//     `stop()` handle that unsubscribes from the event bus.

import { wallDialog, wallSurface } from "../dom/elements.js";
import { on, off } from "../events.js";
import { showToast } from "../utils.js";
import { updateEdgesForNote, type WallDocument } from "./wall-rendering.js";
import { fetchWall } from "./wall-api.js";
import {
  getActiveEditNoteId,
  getMounted,
  setPendingRefetch,
  type Mounted,
} from "./wall-state.js";

export interface RefetchDocOptions {
  /** Called once with the normalized document on success. */
  onApplyDoc: (state: Mounted, doc: WallDocument) => void;
}

// Phase 0 debug counters. Off by default; surfaced via test-only getters
// and via console.debug only when `window.__scrumboyWallDebug === true`.
const realtimeCounters = {
  refreshNeededReceived: 0,
  refetchDocInvocations: 0,
};

function debugEnabled(): boolean {
  return (globalThis as any).__scrumboyWallDebug === true;
}

/**
 * Fetch the wall document, deferring if an edit is in progress.
 *
 * Defer semantics:
 *   - A guard at the top catches SSE-echoes that arrive between a right-click
 *     create and the PATCH.
 *   - A second guard after `await fetchWall` catches the inverse race where the
 *     GET was already in flight when the user entered edit mode.
 */
export async function refetchDoc(opts: RefetchDocOptions): Promise<void> {
  const state = getMounted();
  if (!state) return;
  if (getActiveEditNoteId()) {
    setPendingRefetch(true);
    return;
  }
  realtimeCounters.refetchDocInvocations += 1;
  try {
    const doc = await fetchWall(state.slug);
    if (getMounted() !== state) return;
    if (getActiveEditNoteId()) {
      setPendingRefetch(true);
      return;
    }
    opts.onApplyDoc(state, doc);
    if (debugEnabled()) {
      console.debug("wall refetch applied", {
        refetches: realtimeCounters.refetchDocInvocations,
        refreshEvents: realtimeCounters.refreshNeededReceived,
      });
    }
  } catch (err: any) {
    if (err?.status === 404) {
      showToast("This board does not have a wall.");
      (wallDialog as HTMLDialogElement | null)?.close();
      return;
    }
    console.warn("wall refetch failed", err);
  }
}

/**
 * Apply a wall.transient SSE payload to the DOM without a refetch.
 *
 * Echo suppression: transients originated by the local user are ignored.
 * Drag suppression: notes the local user is currently dragging are ignored.
 */
export function applyTransient(
  payload: unknown,
  noteElementById: (id: string) => HTMLElement | null,
): void {
  const state = getMounted();
  if (!state || !wallSurface) return;
  const envelope = payload as any;
  const p = envelope?.payload ?? envelope;
  if (!p || typeof p !== "object") return;
  const noteId = typeof p.noteId === "string" ? p.noteId : null;
  const x = typeof p.x === "number" ? p.x : null;
  const y = typeof p.y === "number" ? p.y : null;
  const by = typeof p.by === "number" ? p.by : null;
  if (!noteId || x === null || y === null) return;
  if (by !== null && state.userId !== null && by === state.userId) return;
  const el = noteElementById(noteId);
  if (!el) return;
  if (el.classList.contains("wall-note--dragging")) return;
  el.style.left = `${Math.round(x)}px`;
  el.style.top = `${Math.round(y)}px`;
  updateEdgesForNote(wallSurface, noteId, x + el.offsetWidth / 2, y + el.offsetHeight / 2);
}

export interface StartRealtimeOptions {
  onRefreshNeeded: () => void;
  onTransient: (payload: unknown) => void;
}

/**
 * Wire up `wall:refresh_needed` and `wall:transient` SSE handlers; returns a
 * stop() to tear them down. Exposed so tests can assert subscribe/unsubscribe
 * without touching the full openWallDialog lifecycle.
 */
export function startRealtime(opts: StartRealtimeOptions): () => void {
  // Phase 0: count raw refresh_needed events so we can later compare against
  // refetchDocInvocations once debounce/coalescing lands in Phase 2.
  const wrappedRefresh = () => {
    realtimeCounters.refreshNeededReceived += 1;
    opts.onRefreshNeeded();
  };
  on("wall:refresh_needed", wrappedRefresh);
  on("wall:transient", opts.onTransient);
  return () => {
    off("wall:refresh_needed", wrappedRefresh);
    off("wall:transient", opts.onTransient);
  };
}

/** Test helper: read the Phase 0 realtime counters. */
export function __getRealtimeCounters(): { refreshNeededReceived: number; refetchDocInvocations: number } {
  return {
    refreshNeededReceived: realtimeCounters.refreshNeededReceived,
    refetchDocInvocations: realtimeCounters.refetchDocInvocations,
  };
}

/** Test helper: reset the Phase 0 realtime counters between test cases. */
export function __resetRealtimeCounters(): void {
  realtimeCounters.refreshNeededReceived = 0;
  realtimeCounters.refetchDocInvocations = 0;
}

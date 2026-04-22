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
import { updateEdgesForNote } from "./wall-rendering.js";
import { fetchWall } from "./wall-api.js";
import { getActiveEditNoteId, getMounted, isDragActive, setPendingRefetch, } from "./wall-state.js";
/**
 * Phase 2: trailing-debounce window for `wall.refresh_needed` events.
 *
 * Rationale: bursts of durable mutations (multi-note move commits, multi
 * delete, several collaborators editing at once) produce multiple SSE
 * events in rapid succession. Without coalescing, each one fires a full
 * `refetchDoc -> renderSurface` which clears `wallSurface.innerHTML` and
 * rebuilds every note. On NAS / high-RTT links this manifests as hitching.
 *
 * 120ms is the smallest window that reliably captures a single human burst
 * without adding perceivable latency to cross-user updates.
 */
const REFRESH_DEBOUNCE_MS = 120;
// Phase 0 debug counters. Off by default; surfaced via test-only getters
// and via console.debug only when `window.__scrumboyWallDebug === true`.
const realtimeCounters = {
    refreshNeededReceived: 0,
    refetchDocInvocations: 0,
};
function debugEnabled() {
    return globalThis.__scrumboyWallDebug === true;
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
export async function refetchDoc(opts) {
    const state = getMounted();
    if (!state)
        return;
    if (getActiveEditNoteId()) {
        setPendingRefetch(true);
        return;
    }
    realtimeCounters.refetchDocInvocations += 1;
    try {
        const doc = await fetchWall(state.slug);
        if (getMounted() !== state)
            return;
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
    }
    catch (err) {
        if (err?.status === 404) {
            showToast("This board does not have a wall.");
            wallDialog?.close();
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
export function applyTransient(payload, noteElementById) {
    const state = getMounted();
    if (!state || !wallSurface)
        return;
    const envelope = payload;
    const p = envelope?.payload ?? envelope;
    if (!p || typeof p !== "object")
        return;
    const noteId = typeof p.noteId === "string" ? p.noteId : null;
    const x = typeof p.x === "number" ? p.x : null;
    const y = typeof p.y === "number" ? p.y : null;
    const by = typeof p.by === "number" ? p.by : null;
    if (!noteId || x === null || y === null)
        return;
    if (by !== null && state.userId !== null && by === state.userId)
        return;
    const el = noteElementById(noteId);
    if (!el)
        return;
    if (el.classList.contains("wall-note--dragging"))
        return;
    el.style.left = `${Math.round(x)}px`;
    el.style.top = `${Math.round(y)}px`;
    updateEdgesForNote(wallSurface, noteId, x + el.offsetWidth / 2, y + el.offsetHeight / 2);
}
let currentDebounceHandle = null;
export function startRealtime(opts) {
    // Phase 2: trailing debounce around `wall:refresh_needed`. Each event
    // re-arms the timer; when it fires we call `opts.onRefreshNeeded()` *unless*
    // a local drag is active, in which case we re-arm so mid-drag refetches
    // cannot wipe the DOM. Edit-mode is already guarded inside `refetchDoc`.
    let debounceTimer = null;
    let pending = false;
    function fireDebounced() {
        debounceTimer = null;
        if (!pending)
            return;
        if (isDragActive()) {
            debounceTimer = setTimeout(fireDebounced, REFRESH_DEBOUNCE_MS);
            return;
        }
        pending = false;
        opts.onRefreshNeeded();
    }
    const wrappedRefresh = () => {
        realtimeCounters.refreshNeededReceived += 1;
        pending = true;
        if (debounceTimer !== null)
            clearTimeout(debounceTimer);
        debounceTimer = setTimeout(fireDebounced, REFRESH_DEBOUNCE_MS);
    };
    on("wall:refresh_needed", wrappedRefresh);
    on("wall:transient", opts.onTransient);
    currentDebounceHandle = {
        hasPending: () => pending,
        flushNow: () => {
            if (debounceTimer !== null) {
                clearTimeout(debounceTimer);
                debounceTimer = null;
            }
            if (!pending)
                return;
            if (isDragActive())
                return;
            pending = false;
            opts.onRefreshNeeded();
        },
        clear: () => {
            if (debounceTimer !== null) {
                clearTimeout(debounceTimer);
                debounceTimer = null;
            }
            pending = false;
        },
    };
    const handle = currentDebounceHandle;
    return () => {
        off("wall:refresh_needed", wrappedRefresh);
        off("wall:transient", opts.onTransient);
        // If a refresh is pending and no drag/edit is blocking, flush once so
        // teardown does not lose a convergence. Otherwise drop: the dialog is
        // going away and a fresh mount will GET /wall on open anyway.
        if (pending && !isDragActive() && !getActiveEditNoteId()) {
            pending = false;
            if (debounceTimer !== null) {
                clearTimeout(debounceTimer);
                debounceTimer = null;
            }
            opts.onRefreshNeeded();
        }
        else {
            if (debounceTimer !== null) {
                clearTimeout(debounceTimer);
                debounceTimer = null;
            }
            pending = false;
        }
        if (currentDebounceHandle === handle) {
            currentDebounceHandle = null;
        }
    };
}
/** Test helper: query + drive the currently-armed refresh-needed debounce. */
export function __getRefreshDebounceHandle() {
    return currentDebounceHandle;
}
/** Test helper: read the debounce window used by startRealtime. */
export function __getRefreshDebounceMs() {
    return REFRESH_DEBOUNCE_MS;
}
/** Test helper: read the Phase 0 realtime counters. */
export function __getRealtimeCounters() {
    return {
        refreshNeededReceived: realtimeCounters.refreshNeededReceived,
        refetchDocInvocations: realtimeCounters.refetchDocInvocations,
    };
}
/** Test helper: reset the Phase 0 realtime counters between test cases. */
export function __resetRealtimeCounters() {
    realtimeCounters.refreshNeededReceived = 0;
    realtimeCounters.refetchDocInvocations = 0;
}

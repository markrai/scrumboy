// Drag + resize gesture controller for the Scrumbaby/Wall feature.
//
// This module owns:
//   - Multi-participant note drag (with rAF-coalesced moves, transient fanout).
//   - Single-note resize.
//   - Trash-strip visibility + hit test.
//   - Per-note transient scheduling / flushing.
//
// It does NOT own:
//   - Delete confirmation (kept in the caller so the controller stays free of
//     `confirmDelete` / `utils.js` coupling).
//   - `findNote` / `noteElementById` lookups (kept behind injected callbacks so
//     the controller can test in isolation without the full wall doc model).
//
// The injection shape is small on purpose: one options bag per top-level
// gesture function, filled in once from `wall.ts::bindSurfaceHandlers`.
import { wallSurface, wallTrash } from "../dom/elements.js";
import { clampDim, updateEdgesForEndpoint, MAX_NOTE_HEIGHT, MAX_NOTE_WIDTH, MIN_NOTE_HEIGHT, MIN_NOTE_WIDTH, } from "./wall-rendering.js";
import { formatWallStoryEndpoint, storyEdgeCenter } from "./wall-edge-endpoint.js";
import { DRAG_TRANSIENT_COALESCE_MS, TRANSIENT_COALESCE_MS } from "./wall-postbaby-constants.js";
import { postTransient } from "./wall-api.js";
import { getMounted, setDragActive } from "./wall-state.js";
import { MAX_CANVAS_COORD, canvasDelta, clampCanvasCoord, getViewportState, getWallContent, } from "./wall-viewport.js";
// Phase 0 debug counters. Lifetime-accumulating across all drags in the
// current session; reset via __resetDragCounters() in tests. Per-drag deltas
// are also emitted via console.debug when window.__scrumboyWallDebug is true.
const dragCounters = {
    edgeUpdateBatches: 0,
    edgeUpdateCalls: 0,
};
function debugEnabled() {
    return globalThis.__scrumboyWallDebug === true;
}
/** Test helper: read the Phase 0 drag counters. */
export function __getDragCounters() {
    return {
        edgeUpdateBatches: dragCounters.edgeUpdateBatches,
        edgeUpdateCalls: dragCounters.edgeUpdateCalls,
    };
}
/** Test helper: reset the Phase 0 drag counters between test cases. */
export function __resetDragCounters() {
    dragCounters.edgeUpdateBatches = 0;
    dragCounters.edgeUpdateCalls = 0;
}
// ---- Transient (non-durable) drag fanout ---------------------------------
export function scheduleTransient(state, noteId, x, y) {
    let entry = state.transient.get(noteId);
    if (!entry) {
        entry = { lastX: x, lastY: y, lastSentAt: 0, timer: null };
        state.transient.set(noteId, entry);
    }
    entry.lastX = x;
    entry.lastY = y;
    const now = performance.now();
    const elapsed = now - entry.lastSentAt;
    if (elapsed >= TRANSIENT_COALESCE_MS) {
        sendTransientNow(state, noteId);
        return;
    }
    if (!entry.timer) {
        entry.timer = setTimeout(() => {
            if (getMounted() !== state)
                return;
            sendTransientNow(state, noteId);
        }, TRANSIENT_COALESCE_MS - elapsed);
    }
}
export function flushTransient(state, noteId) {
    const entry = state.transient.get(noteId);
    if (!entry)
        return;
    if (entry.timer) {
        clearTimeout(entry.timer);
        entry.timer = null;
    }
    sendTransientNow(state, noteId);
    state.transient.delete(noteId);
}
export function sendTransientNow(state, noteId) {
    const entry = state.transient.get(noteId);
    if (!entry)
        return;
    if (entry.timer) {
        clearTimeout(entry.timer);
        entry.timer = null;
    }
    entry.lastSentAt = performance.now();
    const target = entry.storyLocalId == null
        ? { noteId }
        : { storyLocalId: entry.storyLocalId };
    void postTransient(state.slug, { ...target, x: entry.lastX, y: entry.lastY });
}
function storyTransientKey(localId) {
    return formatWallStoryEndpoint(localId);
}
function scheduleStoryTransient(state, localId, x, y) {
    const key = storyTransientKey(localId);
    let entry = state.transient.get(key);
    if (!entry) {
        entry = { storyLocalId: localId, lastX: x, lastY: y, lastSentAt: 0, timer: null };
        state.transient.set(key, entry);
    }
    entry.lastX = x;
    entry.lastY = y;
    const elapsed = performance.now() - entry.lastSentAt;
    if (elapsed >= TRANSIENT_COALESCE_MS) {
        sendTransientNow(state, key);
    }
    else if (!entry.timer) {
        entry.timer = setTimeout(() => {
            if (getMounted() === state)
                sendTransientNow(state, key);
        }, TRANSIENT_COALESCE_MS - elapsed);
    }
}
function flushStoryTransient(state, localId) {
    const key = storyTransientKey(localId);
    const entry = state.transient.get(key);
    if (!entry)
        return;
    if (entry.timer)
        clearTimeout(entry.timer);
    entry.timer = null;
    sendTransientNow(state, key);
    state.transient.delete(key);
}
// ---- Trash strip -------------------------------------------------------
export function showTrash(visible) {
    if (!wallTrash)
        return;
    wallTrash.classList.toggle("wall-trash--visible", visible);
    if (!visible)
        wallTrash.classList.remove("wall-trash--active");
}
export function updateTrashHoverAny(participants) {
    if (!wallTrash)
        return;
    const active = participants.some((p) => isOverTrash(p.el));
    wallTrash.classList.toggle("wall-trash--active", active);
}
export function isOverTrash(noteEl) {
    if (!wallTrash)
        return false;
    // Screen-space OK: trash is a fixed viewport overlay; compare screen rects.
    const a = noteEl.getBoundingClientRect();
    const b = wallTrash.getBoundingClientRect();
    return !(a.right < b.left || a.left > b.right || a.bottom < b.top || a.top > b.bottom);
}
// ---- Drag (document-scoped, rAF) ----------------------------------------
export function beginDrag(opts) {
    const { state, ev, noteEl, noteId, downX, downY } = opts;
    const primary = opts.findNote(noteId);
    if (!primary)
        return;
    opts.cancelColorTimer(state, noteId);
    const surface = wallSurface;
    if (!surface)
        return;
    const surfaceRect = surface.getBoundingClientRect();
    const noteRect = noteEl.getBoundingClientRect();
    const { panX, panY, zoom: z } = getViewportState();
    // shiftX/shiftY: screen-space offset from pointer to note top-left at pointerdown.
    const shiftX = downX - noteRect.left;
    const shiftY = downY - noteRect.top;
    const isGroup = state.selected.has(noteId) && state.selected.size > 1;
    const participants = [];
    if (isGroup) {
        for (const id of state.selected) {
            const n = opts.findNote(id);
            const el = opts.noteElementById(id);
            if (!n || !el)
                continue;
            participants.push({ id, el, startX: n.x, startY: n.y, note: n });
        }
    }
    else {
        participants.push({ id: noteId, el: noteEl, startX: primary.x, startY: primary.y, note: primary });
    }
    for (const p of participants)
        p.el.classList.add("wall-note--dragging");
    showTrash(true);
    // Phase 2: signal the refresh-needed debounce in wall-realtime to hold off
    // on refetching while this drag is in flight. Cleared in onUp below (and
    // defensively in wall.ts teardown if the dialog closes mid-drag).
    setDragActive(true);
    let animationFrameId = null;
    let lastClientX = ev.clientX;
    let lastClientY = ev.clientY;
    // Phase 0 per-drag stats; aggregated into module-level `dragCounters` when
    // this drag ends so tests / operators can inspect a full session.
    const dragStats = { edgeUpdateBatches: 0, edgeUpdateCalls: 0, startedAt: performance.now() };
    // Phase 1: single group-timer (one setTimeout shared by all participants)
    // instead of N per-note timers. Per-note positions still fan out when the
    // timer fires via sendTransientNow, so the HTTP payload shape is unchanged;
    // only the wake-up count collapses from N to 1 per DRAG_TRANSIENT_COALESCE_MS
    // window. The primary drag id keys the timer so teardown/close is obvious.
    let groupTransientTimer = null;
    let groupTransientLastSentAt = 0;
    function storeTransientPosition(id, x, y) {
        let entry = state.transient.get(id);
        if (!entry) {
            entry = { lastX: x, lastY: y, lastSentAt: 0, timer: null };
            state.transient.set(id, entry);
        }
        else {
            entry.lastX = x;
            entry.lastY = y;
        }
    }
    function flushGroupTransientsNow() {
        if (groupTransientTimer !== null) {
            clearTimeout(groupTransientTimer);
            groupTransientTimer = null;
        }
        groupTransientLastSentAt = performance.now();
        for (const p of participants) {
            if (state.transient.has(p.id))
                sendTransientNow(state, p.id);
        }
    }
    function scheduleGroupTransient() {
        const now = performance.now();
        const elapsed = now - groupTransientLastSentAt;
        if (elapsed >= DRAG_TRANSIENT_COALESCE_MS) {
            flushGroupTransientsNow();
            return;
        }
        if (groupTransientTimer !== null)
            return;
        groupTransientTimer = setTimeout(() => {
            groupTransientTimer = null;
            if (getMounted() !== state)
                return;
            flushGroupTransientsNow();
        }, DRAG_TRANSIENT_COALESCE_MS - elapsed);
    }
    function moveAt(clientX, clientY) {
        lastClientX = clientX;
        lastClientY = clientY;
        if (animationFrameId !== null)
            return;
        animationFrameId = requestAnimationFrame(() => {
            animationFrameId = null;
            const screenNoteLeft = lastClientX - shiftX;
            const screenNoteTop = lastClientY - shiftY;
            const rawX = (screenNoteLeft - surfaceRect.left - panX) / z;
            const rawY = (screenNoteTop - surfaceRect.top - panY) / z;
            let minDeltaX = rawX - primary.x;
            let minDeltaY = rawY - primary.y;
            for (const p of participants) {
                if (p.startX + minDeltaX < -MAX_CANVAS_COORD)
                    minDeltaX = -MAX_CANVAS_COORD - p.startX;
                if (p.startY + minDeltaY < -MAX_CANVAS_COORD)
                    minDeltaY = -MAX_CANVAS_COORD - p.startY;
                if (p.startX + minDeltaX > MAX_CANVAS_COORD)
                    minDeltaX = MAX_CANVAS_COORD - p.startX;
                if (p.startY + minDeltaY > MAX_CANVAS_COORD)
                    minDeltaY = MAX_CANVAS_COORD - p.startY;
            }
            let edgeCallsThisTick = 0;
            for (const p of participants) {
                const nx = clampCanvasCoord(p.startX + minDeltaX);
                const ny = clampCanvasCoord(p.startY + minDeltaY);
                p.el.style.left = `${Math.round(nx)}px`;
                p.el.style.top = `${Math.round(ny)}px`;
                storeTransientPosition(p.id, nx, ny);
                const edgeRoot = getWallContent() ?? wallSurface;
                if (edgeRoot) {
                    updateEdgesForEndpoint(edgeRoot, p.id, nx + p.el.offsetWidth / 2, ny + p.el.offsetHeight / 2);
                    edgeCallsThisTick += 1;
                }
            }
            scheduleGroupTransient();
            if (edgeCallsThisTick > 0) {
                dragStats.edgeUpdateBatches += 1;
                dragStats.edgeUpdateCalls += edgeCallsThisTick;
            }
            updateTrashHoverAny(participants);
        });
    }
    const onMove = (mv) => {
        mv.preventDefault();
        moveAt(mv.clientX, mv.clientY);
    };
    const onUp = (up) => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        if (animationFrameId !== null) {
            cancelAnimationFrame(animationFrameId);
            animationFrameId = null;
        }
        // Cancel any pending group-timer wake-up; the drop path below will
        // schedule+flush each participant's final position explicitly, so the
        // coalesced mid-drag POST is no longer needed.
        if (groupTransientTimer !== null) {
            clearTimeout(groupTransientTimer);
            groupTransientTimer = null;
        }
        // Phase 2: release the refresh-needed debounce gate. Any SSE refresh
        // events that arrived during the drag have been deferred by the debounce;
        // the subsequent PATCH from onCommitDragPositions (or a new remote event)
        // will drive the next refetch.
        setDragActive(false);
        for (const p of participants)
            p.el.classList.remove("wall-note--dragging");
        const overlappingTrash = participants.some((p) => isOverTrash(p.el));
        showTrash(false);
        const finalizeDragStats = (outcome) => {
            dragCounters.edgeUpdateBatches += dragStats.edgeUpdateBatches;
            dragCounters.edgeUpdateCalls += dragStats.edgeUpdateCalls;
            if (debugEnabled()) {
                console.debug("wall drag ended", {
                    outcome,
                    participants: participants.length,
                    edgeUpdateBatches: dragStats.edgeUpdateBatches,
                    edgeUpdateCalls: dragStats.edgeUpdateCalls,
                    durationMs: Math.round(performance.now() - dragStats.startedAt),
                });
            }
        };
        if (overlappingTrash) {
            // Revert visuals to starting positions before confirming so notes
            // don't sit over the trash while the native dialog is up.
            for (const p of participants) {
                p.el.style.left = `${Math.round(p.startX)}px`;
                p.el.style.top = `${Math.round(p.startY)}px`;
                const edgeRoot = getWallContent() ?? wallSurface;
                if (edgeRoot) {
                    updateEdgesForEndpoint(edgeRoot, p.id, p.startX + p.el.offsetWidth / 2, p.startY + p.el.offsetHeight / 2);
                }
            }
            opts.onDropOnTrash(participants.map((p) => p.id), isGroup);
            finalizeDragStats("trash");
            return;
        }
        const commits = [];
        for (const p of participants) {
            const finalX = parseInt(p.el.style.left || "0", 10);
            const finalY = parseInt(p.el.style.top || "0", 10);
            scheduleTransient(state, p.id, finalX, finalY);
            flushTransient(state, p.id);
            if (finalX !== p.note.x || finalY !== p.note.y) {
                commits.push({ id: p.id, x: finalX, y: finalY });
            }
        }
        if (commits.length > 0)
            opts.onCommitDragPositions(commits);
        if (isGroup)
            opts.onClearSelectionAfterGroupDrop();
        finalizeDragStats("commit");
        void up;
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}
/** Drag a canonical story placement without sticky-note selection/trash semantics. */
export function beginStoryDrag(opts) {
    const { state, ev, storyEl, story, downX, downY } = opts;
    const surface = wallSurface;
    if (!surface)
        return;
    const surfaceRect = surface.getBoundingClientRect();
    const itemRect = storyEl.getBoundingClientRect();
    const { panX, panY, zoom } = getViewportState();
    const shiftX = downX - itemRect.left;
    const shiftY = downY - itemRect.top;
    let frame = null;
    let clientX = ev.clientX;
    let clientY = ev.clientY;
    storyEl.classList.add("wall-story--dragging");
    setDragActive(true);
    const paint = () => {
        frame = null;
        const x = clampCanvasCoord((clientX - shiftX - surfaceRect.left - panX) / zoom);
        const y = clampCanvasCoord((clientY - shiftY - surfaceRect.top - panY) / zoom);
        storyEl.style.left = `${Math.round(x)}px`;
        storyEl.style.top = `${Math.round(y)}px`;
        scheduleStoryTransient(state, story.localId, x, y);
        // Keep incident edges glued to the story on every painted frame, the
        // same way note drags do. Runs inside the existing rAF throttle.
        const edgeRoot = getWallContent() ?? wallSurface;
        const resolved = edgeRoot ? storyEdgeCenter(edgeRoot, story.localId, zoom) : null;
        if (edgeRoot && resolved) {
            updateEdgesForEndpoint(edgeRoot, resolved.endpoint, resolved.cx, resolved.cy);
        }
    };
    const onMove = (move) => {
        move.preventDefault();
        clientX = move.clientX;
        clientY = move.clientY;
        if (frame === null)
            frame = requestAnimationFrame(paint);
    };
    const onUp = () => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        if (frame !== null) {
            cancelAnimationFrame(frame);
            frame = null;
            paint();
        }
        storyEl.classList.remove("wall-story--dragging");
        setDragActive(false);
        const x = parseInt(storyEl.style.left || String(story.x), 10);
        const y = parseInt(storyEl.style.top || String(story.y), 10);
        scheduleStoryTransient(state, story.localId, x, y);
        flushStoryTransient(state, story.localId);
        if (x !== story.x || y !== story.y)
            opts.onCommit(story.localId, x, y);
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
    // Apply the threshold-crossing move that promoted this gesture to a drag.
    onMove(ev);
}
export function startResize(opts) {
    const { state, ev, noteEl, noteId } = opts;
    const note = opts.findNote(noteId);
    if (!note)
        return;
    const startX = ev.clientX;
    const startY = ev.clientY;
    const origW = note.width;
    const origH = note.height;
    const onMove = (mv) => {
        mv.preventDefault();
        const dw = canvasDelta(mv.clientX - startX);
        const dh = canvasDelta(mv.clientY - startY);
        const w = clampDim(origW + dw, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH);
        const h = clampDim(origH + dh, MIN_NOTE_HEIGHT, MAX_NOTE_HEIGHT);
        noteEl.style.width = `${Math.round(w)}px`;
        noteEl.style.height = `${Math.round(h)}px`;
    };
    const onUp = () => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        const finalW = parseInt(noteEl.style.width || "0", 10);
        const finalH = parseInt(noteEl.style.height || "0", 10);
        if (finalW !== origW || finalH !== origH) {
            opts.onCommitResize(noteId, finalW, finalH);
        }
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}

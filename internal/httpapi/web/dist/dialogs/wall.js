// Scrumbaby sticky-note wall dialog (Postbaby-parity).
//
// Life-cycle:
//   openWallDialog(opts) is the single public entry point, imported lazily
//   from board.ts on first click of #wallBtn so this module (and Postbaby
//   constants / rendering helpers) is not part of the initial board bundle.
//
// Interaction model (delegated on #wallSurface):
//   - Right-click on empty canvas -> POST /wall/notes at pointer position.
//   - Single-click on a note -> delayed color-cycle timer (DOUBLE_TAP_MS).
//     Fires nextColor + PATCH color unless cancelled by dblclick or drag.
//   - Double-click on a note -> cancel color timer; enter edit mode
//     (display <div> swapped for textarea). Blur or Escape commits via PATCH
//     text, Enter (without Shift) also commits.
//   - Pointerdown on a note that moves >= DRAG_THRESHOLD_PX -> drag. Pointer
//     move/up listeners attach to document only during the drag; rAF writes
//     left/top; per-note transient sends coalesce at ~TRANSIENT_COALESCE_MS.
//     On pointerup we emit one final transient then PATCH x,y. Drop over
//     the trash strip prompts a simple confirm then DELETE.
//
// Transport:
//   Durable mutations are note-scoped: POST /notes, PATCH /notes/{id},
//   DELETE /notes/{id}. POST /wall/transient is non-durable and only fans
//   out SSE wall.transient events. GET /wall is side-effect-free.
import { wallDialog, wallSurface, closeWallBtn, wallTrash, } from "../dom/elements.js";
import { apiFetch } from "../api.js";
import { showToast } from "../utils.js";
import { on, off } from "../events.js";
import { getUser } from "../state/selectors.js";
import { canEditWall } from "./wall-permissions.js";
import { buildNoteElement, renderEmptyWallHtml, clampDim, enterEditMode, exitEditMode, isEditing, ensureEdgeOverlay, renderEdges, updateEdgesForNote, beginEdgePreview, getNoteCenterFromElement, MIN_NOTE_WIDTH, MIN_NOTE_HEIGHT, MAX_NOTE_WIDTH, MAX_NOTE_HEIGHT, } from "./wall-rendering.js";
import { DOUBLE_TAP_MS, DRAG_THRESHOLD_PX, TRANSIENT_COALESCE_MS, DEFAULT_NOTE_WIDTH, DEFAULT_NOTE_HEIGHT, RAINBOW_COLORS, nextColor, } from "./wall-postbaby-constants.js";
const TEARDOWN_MARKER = Symbol("wallMounted");
let mounted = null;
// Guard against SSE-driven `refetchDoc` blowing away an in-progress edit. A
// freshly right-clicked note enters edit mode synchronously, but the server's
// `wall.refresh_needed` echo would otherwise re-render `#wallSurface` and
// destroy the textarea (and focus) before the user can type a single key.
// While this is non-null the refetch path defers; when the edit finishes we
// flush any deferred refresh.
let activeWallEditNoteId = null;
let pendingRefetchWhileEditing = false;
// Public entry: open the wall dialog and boot its full lifecycle.
export async function openWallDialog(opts) {
    if (!wallDialog || !wallSurface) {
        showToast("Wall is not available here.");
        return;
    }
    const dialog = wallDialog;
    if (mounted) {
        if (!dialog.open)
            dialog.showModal();
        return;
    }
    const canEdit = canEditWall(opts.role);
    const user = getUser();
    const state = {
        projectId: opts.projectId,
        slug: opts.slug,
        role: opts.role,
        canEdit,
        doc: { notes: [], edges: [], version: 0 },
        userId: user?.id ?? null,
        onRefreshNeeded: () => { void refetchDoc(); },
        onTransient: (payload) => applyTransient(payload),
        abort: new AbortController(),
        prevHtmlOverflow: document.documentElement.style.overflow,
        transient: new Map(),
        colorTimers: new Map(),
        lastTapAt: new Map(),
        selected: new Set(),
    };
    mounted = state;
    dialog[TEARDOWN_MARKER] = true;
    wallSurface.classList.toggle("wall-surface--readonly", !canEdit);
    renderSurface();
    if (closeWallBtn) {
        closeWallBtn.addEventListener("click", () => dialog.close(), { signal: state.abort.signal });
    }
    dialog.addEventListener("close", teardown, { signal: state.abort.signal, once: true });
    dialog.addEventListener("cancel", () => dialog.close(), { signal: state.abort.signal });
    on("wall:refresh_needed", state.onRefreshNeeded);
    on("wall:transient", state.onTransient);
    bindSurfaceHandlers(state);
    // Lock body scroll while the wall occupies the viewport.
    document.documentElement.style.overflow = "hidden";
    dialog.showModal();
    await refetchDoc();
}
function teardown() {
    const state = mounted;
    if (!state)
        return;
    off("wall:refresh_needed", state.onRefreshNeeded);
    off("wall:transient", state.onTransient);
    for (const t of state.colorTimers.values())
        clearTimeout(t);
    state.colorTimers.clear();
    for (const entry of state.transient.values()) {
        if (entry.timer)
            clearTimeout(entry.timer);
    }
    state.transient.clear();
    state.selected.clear();
    state.abort.abort();
    if (wallSurface)
        wallSurface.innerHTML = "";
    if (wallTrash) {
        wallTrash.classList.remove("wall-trash--visible");
        wallTrash.classList.remove("wall-trash--active");
    }
    if (wallDialog)
        wallDialog[TEARDOWN_MARKER] = false;
    document.documentElement.style.overflow = state.prevHtmlOverflow;
    mounted = null;
    activeWallEditNoteId = null;
    pendingRefetchWhileEditing = false;
}
async function refetchDoc() {
    const state = mounted;
    if (!state)
        return;
    // Don't nuke the DOM while the user is typing into a freshly created note.
    // We'll re-run this refetch the moment the edit commits or is cancelled.
    if (activeWallEditNoteId) {
        pendingRefetchWhileEditing = true;
        return;
    }
    try {
        const doc = await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall`);
        if (!mounted || mounted !== state)
            return;
        // Second check: the user may have right-clicked to create a note while
        // this GET was in flight. Respect the freshly active edit and postpone.
        if (activeWallEditNoteId) {
            pendingRefetchWhileEditing = true;
            return;
        }
        state.doc = normalizeDoc(doc);
        renderSurface();
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
function normalizeDoc(doc) {
    if (!doc || !Array.isArray(doc.notes))
        return { notes: [], edges: [], version: 0 };
    return {
        notes: doc.notes.map((n) => ({ ...n })),
        edges: Array.isArray(doc.edges) ? doc.edges.map((e) => ({ ...e })) : [],
        version: typeof doc.version === "number" ? doc.version : 0,
        updatedAt: doc.updatedAt,
    };
}
function renderSurface() {
    const state = mounted;
    if (!state || !wallSurface)
        return;
    wallSurface.innerHTML = "";
    if (state.doc.notes.length === 0) {
        wallSurface.innerHTML = renderEmptyWallHtml(state.canEdit);
        return;
    }
    const frag = document.createDocumentFragment();
    for (const note of state.doc.notes) {
        frag.appendChild(buildNoteElement(note, state.canEdit));
    }
    wallSurface.appendChild(frag);
    // SVG overlay must be appended after notes are in the DOM so noteCenter()
    // can read offsetLeft/offsetWidth on the freshly-mounted note elements.
    ensureEdgeOverlay(wallSurface);
    renderEdges(wallSurface, state.doc.edges ?? []);
    // Drop selection entries whose notes no longer exist (remote delete,
    // server-side reconcile), then reapply the `--selected` class.
    pruneSelection();
    syncSelectionDom();
}
// ---- Multi-select state -------------------------------------------------
function syncSelectionDom() {
    const state = mounted;
    if (!state || !wallSurface)
        return;
    const all = wallSurface.querySelectorAll(".wall-note");
    all.forEach((el) => {
        const id = el.dataset.noteId || "";
        el.classList.toggle("wall-note--selected", state.selected.has(id));
    });
}
function pruneSelection() {
    const state = mounted;
    if (!state)
        return;
    if (state.selected.size === 0)
        return;
    const live = new Set(state.doc.notes.map((n) => n.id));
    for (const id of Array.from(state.selected)) {
        if (!live.has(id))
            state.selected.delete(id);
    }
}
function clearSelection() {
    const state = mounted;
    if (!state)
        return;
    if (state.selected.size === 0)
        return;
    state.selected.clear();
    syncSelectionDom();
}
function setSelection(ids) {
    const state = mounted;
    if (!state)
        return;
    state.selected = new Set(ids);
    syncSelectionDom();
}
function toggleSelection(id) {
    const state = mounted;
    if (!state)
        return;
    if (state.selected.has(id))
        state.selected.delete(id);
    else
        state.selected.add(id);
    syncSelectionDom();
}
function noteElementById(id) {
    if (!wallSurface)
        return null;
    return wallSurface.querySelector(`.wall-note[data-note-id="${CSS.escape(id)}"]`);
}
function updateNoteElement(note) {
    const el = noteElementById(note.id);
    if (!el)
        return;
    el.dataset.version = String(note.version);
    el.style.left = `${Math.round(note.x)}px`;
    el.style.top = `${Math.round(note.y)}px`;
    el.style.width = `${Math.round(note.width)}px`;
    el.style.height = `${Math.round(note.height)}px`;
    // Only rewrite background when not actively editing (editing state uses a
    // dedicated background so the textarea is legible).
    if (!isEditing(el)) {
        el.style.background = note.color;
    }
    el.dataset.colorIndex = String(RAINBOW_COLORS.findIndex((c) => c.toUpperCase() === note.color.toUpperCase()));
    const display = el.querySelector(".wall-note__display");
    if (display && !isEditing(el))
        display.textContent = note.text;
    // Keep edge endpoints in sync with the note's authoritative center after a
    // size or position change (e.g. resize commit, remote PATCH echo).
    if (wallSurface) {
        updateEdgesForNote(wallSurface, note.id, note.x + note.width / 2, note.y + note.height / 2);
    }
}
function findNote(id) {
    return mounted?.doc.notes.find((n) => n.id === id);
}
function replaceNoteInDoc(updated) {
    const state = mounted;
    if (!state)
        return;
    const idx = state.doc.notes.findIndex((n) => n.id === updated.id);
    if (idx >= 0)
        state.doc.notes[idx] = updated;
}
async function createNoteAt(x, y) {
    const state = mounted;
    if (!state || !state.canEdit)
        return;
    try {
        const created = await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/notes`, {
            method: "POST",
            body: JSON.stringify({
                x: Math.max(0, Math.round(x)),
                y: Math.max(0, Math.round(y)),
                width: DEFAULT_NOTE_WIDTH,
                height: DEFAULT_NOTE_HEIGHT,
                color: RAINBOW_COLORS[0],
                text: "",
            }),
        });
        if (!mounted || mounted !== state)
            return;
        state.doc.notes.push(created);
        renderSurface();
        // Drop straight into edit mode so the user can type right away.
        const el = noteElementById(created.id);
        if (el)
            beginEdit(el, created);
    }
    catch (err) {
        console.warn("wall create note failed", err);
        showToast("Could not add note");
    }
}
async function patchNote(id, patch) {
    const state = mounted;
    if (!state)
        return;
    const current = findNote(id);
    if (!current)
        return;
    try {
        const updated = await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/notes/${encodeURIComponent(id)}`, {
            method: "PATCH",
            body: JSON.stringify({ ifVersion: current.version, ...patch }),
        });
        if (!mounted || mounted !== state)
            return;
        replaceNoteInDoc(updated);
        updateNoteElement(updated);
    }
    catch (err) {
        if (err?.status === 409) {
            showToast("Another editor changed this note \u2014 reloading wall.");
            await refetchDoc();
            return;
        }
        console.warn("wall patch failed", err);
        showToast("Could not update note");
    }
}
async function deleteNote(id) {
    const state = mounted;
    if (!state)
        return;
    try {
        await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/notes/${encodeURIComponent(id)}`, {
            method: "DELETE",
        });
        if (!mounted || mounted !== state)
            return;
        state.doc.notes = state.doc.notes.filter((n) => n.id !== id);
        // Drop dependent edges client-side too; server already does the same on
        // DELETE /notes/{id}, this just keeps the local doc consistent before
        // the next refetch.
        if (state.doc.edges) {
            state.doc.edges = state.doc.edges.filter((e) => e.from !== id && e.to !== id);
        }
        renderSurface();
    }
    catch (err) {
        console.warn("wall delete failed", err);
        showToast("Could not delete note");
    }
}
async function createEdge(fromId, toId) {
    const state = mounted;
    if (!state || !state.canEdit)
        return;
    if (fromId === toId)
        return;
    // Local duplicate guard so we don't fire a useless POST when the user
    // re-draws an existing connection.
    const existing = (state.doc.edges ?? []).find((e) => (e.from === fromId && e.to === toId) || (e.from === toId && e.to === fromId));
    if (existing)
        return;
    try {
        const created = await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/edges`, {
            method: "POST",
            body: JSON.stringify({ from: fromId, to: toId }),
        });
        if (!mounted || mounted !== state)
            return;
        if (!state.doc.edges)
            state.doc.edges = [];
        // Server returns the existing edge on duplicate (idempotent); de-dupe.
        if (!state.doc.edges.some((e) => e.id === created.id)) {
            state.doc.edges.push(created);
        }
        if (wallSurface)
            renderEdges(wallSurface, state.doc.edges);
    }
    catch (err) {
        console.warn("wall edge create failed", err);
        showToast("Could not draw connection");
    }
}
async function deleteEdge(edgeId) {
    const state = mounted;
    if (!state || !state.canEdit)
        return;
    try {
        await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/edges/${encodeURIComponent(edgeId)}`, {
            method: "DELETE",
        });
        if (!mounted || mounted !== state)
            return;
        if (state.doc.edges) {
            state.doc.edges = state.doc.edges.filter((e) => e.id !== edgeId);
        }
        if (wallSurface)
            renderEdges(wallSurface, state.doc.edges ?? []);
    }
    catch (err) {
        console.warn("wall edge delete failed", err);
        showToast("Could not delete connection");
    }
}
// ---- Transient (non-durable) drag fanout ---------------------------------
function scheduleTransient(state, noteId, x, y) {
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
            if (!mounted || mounted !== state)
                return;
            sendTransientNow(state, noteId);
        }, TRANSIENT_COALESCE_MS - elapsed);
    }
}
function flushTransient(state, noteId) {
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
function sendTransientNow(state, noteId) {
    const entry = state.transient.get(noteId);
    if (!entry)
        return;
    if (entry.timer) {
        clearTimeout(entry.timer);
        entry.timer = null;
    }
    entry.lastSentAt = performance.now();
    void apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/transient`, {
        method: "POST",
        body: JSON.stringify({ noteId, x: entry.lastX, y: entry.lastY }),
    }).catch(() => { });
}
function applyTransient(payload) {
    const state = mounted;
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
    // Echo suppression: ignore transients this user originated.
    if (by !== null && state.userId !== null && by === state.userId)
        return;
    const el = noteElementById(noteId);
    if (!el)
        return;
    if (el.classList.contains("wall-note--dragging"))
        return; // ignore for locally-dragging notes
    el.style.left = `${Math.round(x)}px`;
    el.style.top = `${Math.round(y)}px`;
    // Keep edges glued during remote-driven moves too, so other clients see
    // their connection lines follow the moving note in real time.
    if (wallSurface) {
        updateEdgesForNote(wallSurface, noteId, x + el.offsetWidth / 2, y + el.offsetHeight / 2);
    }
}
// ---- Delegated interaction orchestration --------------------------------
function bindSurfaceHandlers(state) {
    const surface = wallSurface;
    if (!surface)
        return;
    if (!state.canEdit)
        return;
    // Prevent native dblclick from selecting text or zooming; also acts as our
    // hook for note-vs-canvas distinctions that single pointerdown can't catch
    // when the user dblclicks without moving.
    surface.addEventListener("dblclick", (ev) => {
        const target = ev.target;
        if (!target)
            return;
        const noteEl = target.closest(".wall-note");
        if (noteEl) {
            // Note dblclick handled via pointerdown/tapLength logic below, but we
            // also intercept here so browser-native dblclick doesn't select text.
            ev.preventDefault();
            const noteId = noteEl.dataset.noteId || "";
            const note = findNote(noteId);
            if (note) {
                cancelColorTimer(state, noteId);
                beginEdit(noteEl, note);
            }
            return;
        }
        // Empty canvas: no create on double-click (right-click only).
        ev.preventDefault();
    }, { signal: state.abort.signal });
    surface.addEventListener("pointerdown", (ev) => {
        const target = ev.target;
        if (!target)
            return;
        const noteEl = target.closest(".wall-note");
        // Click landed on the editor textarea itself: let native focus and text
        // editing handle it; pointer events stop here.
        if (target.classList.contains("wall-note__editor"))
            return;
        // Resize handle starts a resize, not a drag or color cycle.
        if (target.classList.contains("wall-note__resize-handle") && noteEl) {
            const noteId = noteEl.dataset.noteId || "";
            if (noteId) {
                ev.preventDefault();
                startResize(state, ev, noteEl, noteId);
            }
            return;
        }
        if (noteEl) {
            const noteId = noteEl.dataset.noteId || "";
            if (!noteId)
                return;
            // Don't hijack pointerdown while this specific note is being edited;
            // that lets the user click inside the textarea to move the caret.
            if (isEditing(noteEl))
                return;
            // Postbaby parity: Shift+left-mouse on a note begins an edge drag.
            // Right-button is reserved for contextmenu (delete) - we never start
            // an edge drag from button !== 0.
            if (ev.shiftKey && ev.button === 0) {
                ev.preventDefault();
                beginEdgeDrag(state, ev, noteEl, noteId);
                return;
            }
            // Ctrl/Meta+click: toggle this note in the selection and do not arm
            // the normal single-click (color-cycle) path.
            if ((ev.ctrlKey || ev.metaKey) && ev.button === 0) {
                ev.preventDefault();
                toggleSelection(noteId);
                return;
            }
            // Plain click on a note while a multi-selection is active replaces the
            // selection with just this note before the normal interaction runs.
            // That lets a single click exit multi-select without an extra step
            // while preserving color-cycle / drag / dblclick on the chosen note.
            if (ev.button === 0 && state.selected.size > 0 && !state.selected.has(noteId)) {
                setSelection([noteId]);
            }
            armNoteInteraction(state, ev, noteEl, noteId);
            return;
        }
        // Empty canvas, primary button: begin marquee (unless Shift is held —
        // Shift is reserved for edge-from-note and has no empty-canvas meaning).
        if (ev.button === 0 && !ev.shiftKey) {
            beginMarquee(state, ev);
            return;
        }
    }, { signal: state.abort.signal });
    // Postbaby parity: right-click on empty canvas adds a note; right-click on
    // an edge deletes it (after confirm); right-click on a note is swallowed
    // (no native menu, no creation) so it doesn't conflict with note drag.
    surface.addEventListener("contextmenu", (ev) => {
        const target = ev.target;
        if (!target)
            return;
        const noteEl = target.closest(".wall-note");
        const edgeHit = target.closest(".wall-edge-hit");
        if (edgeHit) {
            ev.preventDefault();
            ev.stopPropagation();
            const groupNode = edgeHit.parentNode;
            const edgeId = groupNode && groupNode instanceof SVGGElement
                ? groupNode.dataset?.edgeId || ""
                : "";
            if (edgeId && window.confirm("Delete this connection?")) {
                void deleteEdge(edgeId);
            }
            return;
        }
        if (noteEl) {
            // Suppress the browser menu over notes; do nothing else (no delete UX
            // here - drop on trash is the only delete path for notes).
            ev.preventDefault();
            return;
        }
        ev.preventDefault();
        const rect = surface.getBoundingClientRect();
        const x = ev.clientX - rect.left;
        const y = ev.clientY - rect.top;
        void createNoteAt(x, y);
    }, { signal: state.abort.signal });
}
// ---- Marquee multi-select (empty-canvas drag) --------------------------
function beginMarquee(state, ev) {
    if (!wallSurface)
        return;
    const surface = wallSurface;
    const surfaceRect = surface.getBoundingClientRect();
    const startX = ev.clientX - surfaceRect.left;
    const startY = ev.clientY - surfaceRect.top;
    const downClientX = ev.clientX;
    const downClientY = ev.clientY;
    let marqueeEl = null;
    let promoted = false;
    const ensureMarquee = () => {
        if (!marqueeEl) {
            marqueeEl = document.createElement("div");
            marqueeEl.className = "wall-marquee";
            surface.appendChild(marqueeEl);
        }
        return marqueeEl;
    };
    const paint = (curX, curY) => {
        const left = Math.min(startX, curX);
        const top = Math.min(startY, curY);
        const width = Math.abs(curX - startX);
        const height = Math.abs(curY - startY);
        const el = ensureMarquee();
        el.style.left = `${Math.round(left)}px`;
        el.style.top = `${Math.round(top)}px`;
        el.style.width = `${Math.round(width)}px`;
        el.style.height = `${Math.round(height)}px`;
    };
    const onMove = (mv) => {
        const dx = mv.clientX - downClientX;
        const dy = mv.clientY - downClientY;
        if (!promoted && dx * dx + dy * dy < DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX)
            return;
        promoted = true;
        mv.preventDefault();
        const curX = mv.clientX - surfaceRect.left;
        const curY = mv.clientY - surfaceRect.top;
        paint(curX, curY);
    };
    const onUp = (up) => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        if (marqueeEl) {
            marqueeEl.remove();
            marqueeEl = null;
        }
        if (!promoted) {
            // Plain click on empty canvas: clear any existing selection.
            clearSelection();
            return;
        }
        const endX = up.clientX - surfaceRect.left;
        const endY = up.clientY - surfaceRect.top;
        const rect = {
            left: Math.min(startX, endX),
            top: Math.min(startY, endY),
            right: Math.max(startX, endX),
            bottom: Math.max(startY, endY),
        };
        const picked = [];
        for (const note of state.doc.notes) {
            const nLeft = note.x;
            const nTop = note.y;
            const nRight = note.x + note.width;
            const nBottom = note.y + note.height;
            const intersects = !(nRight < rect.left || nLeft > rect.right || nBottom < rect.top || nTop > rect.bottom);
            if (intersects)
                picked.push(note.id);
        }
        setSelection(picked);
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}
// ---- Shift+drag edge creation -------------------------------------------
function beginEdgeDrag(state, ev, sourceEl, sourceId) {
    if (!wallSurface)
        return;
    const surface = wallSurface;
    const surfaceRect = surface.getBoundingClientRect();
    const start = getNoteCenterFromElement(surface, sourceEl);
    const preview = beginEdgePreview(surface, start);
    // Initial endpoint follows the pointer immediately (Postbaby's preview
    // line jumps to the cursor as soon as the drag starts).
    preview.update(ev.clientX - surfaceRect.left, ev.clientY - surfaceRect.top);
    const onMove = (mv) => {
        mv.preventDefault();
        preview.update(mv.clientX - surfaceRect.left, mv.clientY - surfaceRect.top);
    };
    const onUp = (up) => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        preview.end();
        // Resolve drop target via elementFromPoint, then walk to the nearest
        // .wall-note. Reject same-source drops; createEdge() handles dupes.
        const dropTarget = document.elementFromPoint(up.clientX, up.clientY);
        const targetNote = dropTarget?.closest(".wall-note") ?? null;
        if (!targetNote)
            return;
        const targetId = targetNote.dataset.noteId || "";
        if (!targetId || targetId === sourceId)
            return;
        void createEdge(sourceId, targetId);
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}
function cancelColorTimer(state, noteId) {
    const t = state.colorTimers.get(noteId);
    if (t) {
        clearTimeout(t);
        state.colorTimers.delete(noteId);
    }
}
// Arm a potential drag. If pointer up without significant movement, treat as
// single-click (start delayed color cycle). If movement exceeds
// DRAG_THRESHOLD_PX, promote to a drag.
function armNoteInteraction(state, ev, noteEl, noteId) {
    ev.preventDefault();
    const startX = ev.clientX;
    const startY = ev.clientY;
    let promoted = false;
    const onMove = (mv) => {
        if (promoted)
            return;
        const dx = mv.clientX - startX;
        const dy = mv.clientY - startY;
        if (dx * dx + dy * dy >= DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX) {
            promoted = true;
            document.removeEventListener("pointermove", onMove);
            document.removeEventListener("pointerup", onUp);
            document.removeEventListener("pointercancel", onUp);
            beginDrag(state, mv, noteEl, noteId, startX, startY);
        }
    };
    const onUp = (up) => {
        if (promoted)
            return;
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        // Short-distance click: disambiguate single-click (color cycle) vs
        // dblclick (edit) via the browser's dblclick event firing within
        // DOUBLE_TAP_MS. We schedule the color cycle here; the dblclick handler
        // cancels it if a second click lands fast.
        const tapNow = performance.now();
        const lastTap = state.lastTapAt.get(noteId) ?? 0;
        state.lastTapAt.set(noteId, tapNow);
        if (tapNow - lastTap < DOUBLE_TAP_MS) {
            // Second tap arrived within the threshold; edit path is driven by the
            // dblclick event. Cancel any pending color timer from the first tap.
            cancelColorTimer(state, noteId);
            return;
        }
        // First tap: schedule color cycle after DOUBLE_TAP_MS so dblclick can
        // still win.
        cancelColorTimer(state, noteId);
        const t = setTimeout(() => {
            state.colorTimers.delete(noteId);
            const note = findNote(noteId);
            const el = noteElementById(noteId);
            if (!note || !el)
                return;
            if (isEditing(el))
                return;
            const { color, index } = nextColor(note.color);
            el.style.background = color;
            el.dataset.colorIndex = String(index);
            void patchNote(noteId, { color });
        }, DOUBLE_TAP_MS);
        state.colorTimers.set(noteId, t);
        void up;
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}
function beginDrag(state, ev, noteEl, noteId, downX, downY) {
    const primary = findNote(noteId);
    if (!primary)
        return;
    cancelColorTimer(state, noteId);
    const surface = wallSurface;
    const surfaceRect = surface.getBoundingClientRect();
    const noteRect = noteEl.getBoundingClientRect();
    // shiftX/shiftY: offset from pointer-down position to the primary note's
    // top-left, measured within the surface. Using the original downX/downY
    // (rather than current pointer position) preserves the visual feel of
    // Postbaby's `shiftX = clientX - rect.left` so the note doesn't jump.
    const shiftX = downX - noteRect.left;
    const shiftY = downY - noteRect.top;
    // Group drag when the grabbed note is part of a multi-selection; otherwise
    // single-note drag (and clear any stale lingering selection if user grabs
    // a note that isn't in it — the armNoteInteraction path already replaced
    // the selection by now, so this is cheap to double-check).
    const isGroup = state.selected.has(noteId) && state.selected.size > 1;
    const participants = [];
    if (isGroup) {
        for (const id of state.selected) {
            const n = findNote(id);
            const el = noteElementById(id);
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
    let animationFrameId = null;
    let lastClientX = ev.clientX;
    let lastClientY = ev.clientY;
    function moveAt(clientX, clientY) {
        lastClientX = clientX;
        lastClientY = clientY;
        if (animationFrameId !== null)
            return;
        animationFrameId = requestAnimationFrame(() => {
            animationFrameId = null;
            // Primary note's new top-left, clamped to surface origin.
            const rawX = lastClientX - surfaceRect.left - shiftX;
            const rawY = lastClientY - surfaceRect.top - shiftY;
            // Cap the delta so no participant crosses x=0 or y=0. This preserves
            // the group's internal spacing when one member bumps the wall edge.
            let minDeltaX = rawX - primary.x;
            let minDeltaY = rawY - primary.y;
            for (const p of participants) {
                if (p.startX + minDeltaX < 0)
                    minDeltaX = -p.startX;
                if (p.startY + minDeltaY < 0)
                    minDeltaY = -p.startY;
            }
            for (const p of participants) {
                const nx = p.startX + minDeltaX;
                const ny = p.startY + minDeltaY;
                p.el.style.left = `${Math.round(nx)}px`;
                p.el.style.top = `${Math.round(ny)}px`;
                scheduleTransient(state, p.id, nx, ny);
                if (wallSurface) {
                    updateEdgesForNote(wallSurface, p.id, nx + p.el.offsetWidth / 2, ny + p.el.offsetHeight / 2);
                }
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
        for (const p of participants)
            p.el.classList.remove("wall-note--dragging");
        const overlappingTrash = participants.some((p) => isOverTrash(p.el));
        showTrash(false);
        if (overlappingTrash) {
            // Revert visuals to starting positions before confirming so notes
            // don't sit over the trash while the native dialog is up.
            for (const p of participants) {
                p.el.style.left = `${Math.round(p.startX)}px`;
                p.el.style.top = `${Math.round(p.startY)}px`;
                if (wallSurface) {
                    updateEdgesForNote(wallSurface, p.id, p.startX + p.el.offsetWidth / 2, p.startY + p.el.offsetHeight / 2);
                }
            }
            const n = participants.length;
            const prompt = n === 1 ? "Delete this note?" : `Delete ${n} notes?`;
            const ok = window.confirm(prompt);
            if (ok) {
                for (const p of participants) {
                    void deleteNote(p.id);
                }
            }
            if (isGroup)
                clearSelection();
            return;
        }
        // Final transient flush + durable PATCH for each moved participant.
        for (const p of participants) {
            const finalX = parseInt(p.el.style.left || "0", 10);
            const finalY = parseInt(p.el.style.top || "0", 10);
            scheduleTransient(state, p.id, finalX, finalY);
            flushTransient(state, p.id);
            if (finalX !== p.note.x || finalY !== p.note.y) {
                void patchNote(p.id, { x: finalX, y: finalY });
            }
        }
        // Drop clears the group selection (user-requested behavior).
        if (isGroup)
            clearSelection();
        void up;
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}
function startResize(state, ev, noteEl, noteId) {
    const note = findNote(noteId);
    if (!note)
        return;
    const startX = ev.clientX;
    const startY = ev.clientY;
    const origW = note.width;
    const origH = note.height;
    const onMove = (mv) => {
        mv.preventDefault();
        const dw = mv.clientX - startX;
        const dh = mv.clientY - startY;
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
            void patchNote(noteId, { width: finalW, height: finalH });
        }
    };
    document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
    document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
    document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}
// ---- Edit mode ----------------------------------------------------------
function beginEdit(noteEl, note) {
    const state = mounted;
    if (!state || !state.canEdit)
        return;
    if (isEditing(noteEl))
        return;
    activeWallEditNoteId = note.id;
    const ta = enterEditMode(noteEl, note.text);
    ta.focus();
    const end = ta.value.length;
    try {
        ta.setSelectionRange(end, end);
    }
    catch { /* ignore */ }
    let finished = false;
    const finish = (commit) => {
        if (finished)
            return;
        finished = true;
        activeWallEditNoteId = null;
        const newText = ta.value;
        exitEditMode(noteEl, commit ? newText : note.text);
        ta.removeEventListener("blur", onBlur);
        ta.removeEventListener("keydown", onKey);
        if (commit && newText !== note.text) {
            void patchNote(note.id, { text: newText });
        }
        // Flush any refresh_needed events that arrived while the user was typing.
        if (pendingRefetchWhileEditing) {
            pendingRefetchWhileEditing = false;
            void refetchDoc();
        }
    };
    const onBlur = () => finish(true);
    const onKey = (ev) => {
        if (ev.key === "Escape") {
            ev.preventDefault();
            finish(false);
        }
        else if (ev.key === "Enter" && !ev.shiftKey) {
            ev.preventDefault();
            finish(true);
        }
        // Shift+Enter falls through to native newline insertion.
    };
    ta.addEventListener("blur", onBlur);
    ta.addEventListener("keydown", onKey);
}
// ---- Trash strip -------------------------------------------------------
function showTrash(visible) {
    if (!wallTrash)
        return;
    wallTrash.classList.toggle("wall-trash--visible", visible);
    if (!visible)
        wallTrash.classList.remove("wall-trash--active");
}
function updateTrashHoverAny(participants) {
    if (!wallTrash)
        return;
    const active = participants.some((p) => isOverTrash(p.el));
    wallTrash.classList.toggle("wall-trash--active", active);
}
function isOverTrash(noteEl) {
    if (!wallTrash)
        return false;
    const a = noteEl.getBoundingClientRect();
    const b = wallTrash.getBoundingClientRect();
    return !(a.right < b.left || a.left > b.right || a.bottom < b.top || a.top > b.bottom);
}

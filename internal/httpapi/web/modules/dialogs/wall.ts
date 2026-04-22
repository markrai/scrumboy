// Scrumbaby sticky-note wall dialog (Postbaby-parity).
//
// Life-cycle:
//   openWallDialog(opts) is the single public entry point, imported lazily
//   from board.ts on first click of #wallBtn so this module (and Postbaby
//   constants / rendering helpers) is not part of the initial board bundle.
//
// Interaction model (delegated on #wallSurface):
//   - Double-click on empty canvas -> POST /wall/notes at pointer position.
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

import {
  wallDialog,
  wallSurface,
  closeWallBtn,
  wallTrash,
} from "../dom/elements.js";
import { apiFetch } from "../api.js";
import { showToast } from "../utils.js";
import { on, off } from "../events.js";
import { getUser } from "../state/selectors.js";
import { canEditWall, type WallRole } from "./wall-permissions.js";
import {
  buildNoteElement,
  renderEmptyWallHtml,
  clampDim,
  enterEditMode,
  exitEditMode,
  isEditing,
  ensureEdgeOverlay,
  renderEdges,
  updateEdgesForNote,
  beginEdgePreview,
  getNoteCenterFromElement,
  MIN_NOTE_WIDTH,
  MIN_NOTE_HEIGHT,
  MAX_NOTE_WIDTH,
  MAX_NOTE_HEIGHT,
  type WallDocument,
  type WallEdge,
  type WallNote,
} from "./wall-rendering.js";
import {
  DOUBLE_TAP_MS,
  DRAG_THRESHOLD_PX,
  TRANSIENT_COALESCE_MS,
  DEFAULT_NOTE_WIDTH,
  DEFAULT_NOTE_HEIGHT,
  RAINBOW_COLORS,
  nextColor,
} from "./wall-postbaby-constants.js";

export interface OpenWallDialogOptions {
  projectId: number;
  slug: string;
  role: WallRole;
}

const TEARDOWN_MARKER = Symbol("wallMounted");

type TransientEntry = {
  lastX: number;
  lastY: number;
  lastSentAt: number;
  timer: ReturnType<typeof setTimeout> | null;
};

type Mounted = {
  projectId: number;
  slug: string;
  role: WallRole;
  canEdit: boolean;
  doc: WallDocument;
  userId: number | null;
  onRefreshNeeded: () => void;
  onTransient: (payload: unknown) => void;
  abort: AbortController;
  prevHtmlOverflow: string;
  // Per-note transient coalescing. One map entry per actively dragging note.
  transient: Map<string, TransientEntry>;
  // Pending single-click color-cycle timers per note.
  colorTimers: Map<string, ReturnType<typeof setTimeout>>;
  // Track last-clicked timestamp per note for mouse dblclick fallback.
  lastTapAt: Map<string, number>;
  // Empty-canvas dblclick detection.
  lastEmptyTapAt: number;
  lastEmptyTapPos: { x: number; y: number } | null;
};

let mounted: Mounted | null = null;

// Public entry: open the wall dialog and boot its full lifecycle.
export async function openWallDialog(opts: OpenWallDialogOptions): Promise<void> {
  if (!wallDialog || !wallSurface) {
    showToast("Wall is not available here.");
    return;
  }
  const dialog = wallDialog as HTMLDialogElement;
  if (mounted) {
    if (!dialog.open) dialog.showModal();
    return;
  }

  const canEdit = canEditWall(opts.role);
  const user = getUser();
  const state: Mounted = {
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
    lastEmptyTapAt: 0,
    lastEmptyTapPos: null,
  };
  mounted = state;
  (dialog as any)[TEARDOWN_MARKER] = true;

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

function teardown(): void {
  const state = mounted;
  if (!state) return;
  off("wall:refresh_needed", state.onRefreshNeeded);
  off("wall:transient", state.onTransient);
  for (const t of state.colorTimers.values()) clearTimeout(t);
  state.colorTimers.clear();
  for (const entry of state.transient.values()) {
    if (entry.timer) clearTimeout(entry.timer);
  }
  state.transient.clear();
  state.abort.abort();
  if (wallSurface) wallSurface.innerHTML = "";
  if (wallTrash) {
    wallTrash.classList.remove("wall-trash--visible");
    wallTrash.classList.remove("wall-trash--active");
  }
  if (wallDialog) (wallDialog as any)[TEARDOWN_MARKER] = false;
  document.documentElement.style.overflow = state.prevHtmlOverflow;
  mounted = null;
}

async function refetchDoc(): Promise<void> {
  const state = mounted;
  if (!state) return;
  try {
    const doc = await apiFetch<WallDocument>(`/api/board/${encodeURIComponent(state.slug)}/wall`);
    if (!mounted || mounted !== state) return;
    state.doc = normalizeDoc(doc);
    renderSurface();
  } catch (err: any) {
    if (err?.status === 404) {
      showToast("This board does not have a wall.");
      (wallDialog as HTMLDialogElement | null)?.close();
      return;
    }
    console.warn("wall refetch failed", err);
  }
}

function normalizeDoc(doc: WallDocument | null | undefined): WallDocument {
  if (!doc || !Array.isArray(doc.notes)) return { notes: [], edges: [], version: 0 };
  return {
    notes: doc.notes.map((n) => ({ ...n })),
    edges: Array.isArray(doc.edges) ? doc.edges.map((e) => ({ ...e })) : [],
    version: typeof doc.version === "number" ? doc.version : 0,
    updatedAt: doc.updatedAt,
  };
}

function renderSurface(): void {
  const state = mounted;
  if (!state || !wallSurface) return;
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
}

function noteElementById(id: string): HTMLElement | null {
  if (!wallSurface) return null;
  return wallSurface.querySelector<HTMLElement>(`.wall-note[data-note-id="${CSS.escape(id)}"]`);
}

function updateNoteElement(note: WallNote): void {
  const el = noteElementById(note.id);
  if (!el) return;
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
  const display = el.querySelector<HTMLElement>(".wall-note__display");
  if (display && !isEditing(el)) display.textContent = note.text;
  // Keep edge endpoints in sync with the note's authoritative center after a
  // size or position change (e.g. resize commit, remote PATCH echo).
  if (wallSurface) {
    updateEdgesForNote(wallSurface, note.id, note.x + note.width / 2, note.y + note.height / 2);
  }
}

function findNote(id: string): WallNote | undefined {
  return mounted?.doc.notes.find((n) => n.id === id);
}

function replaceNoteInDoc(updated: WallNote): void {
  const state = mounted;
  if (!state) return;
  const idx = state.doc.notes.findIndex((n) => n.id === updated.id);
  if (idx >= 0) state.doc.notes[idx] = updated;
}

async function createNoteAt(x: number, y: number): Promise<void> {
  const state = mounted;
  if (!state || !state.canEdit) return;
  try {
    const created = await apiFetch<WallNote>(`/api/board/${encodeURIComponent(state.slug)}/wall/notes`, {
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
    if (!mounted || mounted !== state) return;
    state.doc.notes.push(created);
    renderSurface();
    // Drop straight into edit mode so the user can type right away.
    const el = noteElementById(created.id);
    if (el) beginEdit(el, created);
  } catch (err) {
    console.warn("wall create note failed", err);
    showToast("Could not add note");
  }
}

async function patchNote(id: string, patch: Partial<Pick<WallNote, "x" | "y" | "width" | "height" | "color" | "text">>): Promise<void> {
  const state = mounted;
  if (!state) return;
  const current = findNote(id);
  if (!current) return;
  try {
    const updated = await apiFetch<WallNote>(`/api/board/${encodeURIComponent(state.slug)}/wall/notes/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify({ ifVersion: current.version, ...patch }),
    });
    if (!mounted || mounted !== state) return;
    replaceNoteInDoc(updated);
    updateNoteElement(updated);
  } catch (err: any) {
    if (err?.status === 409) {
      showToast("Another editor changed this note \u2014 reloading wall.");
      await refetchDoc();
      return;
    }
    console.warn("wall patch failed", err);
    showToast("Could not update note");
  }
}

async function deleteNote(id: string): Promise<void> {
  const state = mounted;
  if (!state) return;
  try {
    await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/notes/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
    if (!mounted || mounted !== state) return;
    state.doc.notes = state.doc.notes.filter((n) => n.id !== id);
    // Drop dependent edges client-side too; server already does the same on
    // DELETE /notes/{id}, this just keeps the local doc consistent before
    // the next refetch.
    if (state.doc.edges) {
      state.doc.edges = state.doc.edges.filter((e) => e.from !== id && e.to !== id);
    }
    renderSurface();
  } catch (err) {
    console.warn("wall delete failed", err);
    showToast("Could not delete note");
  }
}

async function createEdge(fromId: string, toId: string): Promise<void> {
  const state = mounted;
  if (!state || !state.canEdit) return;
  if (fromId === toId) return;
  // Local duplicate guard so we don't fire a useless POST when the user
  // re-draws an existing connection.
  const existing = (state.doc.edges ?? []).find(
    (e) => (e.from === fromId && e.to === toId) || (e.from === toId && e.to === fromId),
  );
  if (existing) return;
  try {
    const created = await apiFetch<WallEdge>(`/api/board/${encodeURIComponent(state.slug)}/wall/edges`, {
      method: "POST",
      body: JSON.stringify({ from: fromId, to: toId }),
    });
    if (!mounted || mounted !== state) return;
    if (!state.doc.edges) state.doc.edges = [];
    // Server returns the existing edge on duplicate (idempotent); de-dupe.
    if (!state.doc.edges.some((e) => e.id === created.id)) {
      state.doc.edges.push(created);
    }
    if (wallSurface) renderEdges(wallSurface, state.doc.edges);
  } catch (err) {
    console.warn("wall edge create failed", err);
    showToast("Could not draw connection");
  }
}

async function deleteEdge(edgeId: string): Promise<void> {
  const state = mounted;
  if (!state || !state.canEdit) return;
  try {
    await apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/edges/${encodeURIComponent(edgeId)}`, {
      method: "DELETE",
    });
    if (!mounted || mounted !== state) return;
    if (state.doc.edges) {
      state.doc.edges = state.doc.edges.filter((e) => e.id !== edgeId);
    }
    if (wallSurface) renderEdges(wallSurface, state.doc.edges ?? []);
  } catch (err) {
    console.warn("wall edge delete failed", err);
    showToast("Could not delete connection");
  }
}

// ---- Transient (non-durable) drag fanout ---------------------------------

function scheduleTransient(state: Mounted, noteId: string, x: number, y: number): void {
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
      if (!mounted || mounted !== state) return;
      sendTransientNow(state, noteId);
    }, TRANSIENT_COALESCE_MS - elapsed);
  }
}

function flushTransient(state: Mounted, noteId: string): void {
  const entry = state.transient.get(noteId);
  if (!entry) return;
  if (entry.timer) {
    clearTimeout(entry.timer);
    entry.timer = null;
  }
  sendTransientNow(state, noteId);
  state.transient.delete(noteId);
}

function sendTransientNow(state: Mounted, noteId: string): void {
  const entry = state.transient.get(noteId);
  if (!entry) return;
  if (entry.timer) {
    clearTimeout(entry.timer);
    entry.timer = null;
  }
  entry.lastSentAt = performance.now();
  void apiFetch(`/api/board/${encodeURIComponent(state.slug)}/wall/transient`, {
    method: "POST",
    body: JSON.stringify({ noteId, x: entry.lastX, y: entry.lastY }),
  }).catch(() => {});
}

function applyTransient(payload: unknown): void {
  const state = mounted;
  if (!state || !wallSurface) return;
  const envelope = payload as any;
  const p = envelope?.payload ?? envelope;
  if (!p || typeof p !== "object") return;
  const noteId = typeof p.noteId === "string" ? p.noteId : null;
  const x = typeof p.x === "number" ? p.x : null;
  const y = typeof p.y === "number" ? p.y : null;
  const by = typeof p.by === "number" ? p.by : null;
  if (!noteId || x === null || y === null) return;
  // Echo suppression: ignore transients this user originated.
  if (by !== null && state.userId !== null && by === state.userId) return;
  const el = noteElementById(noteId);
  if (!el) return;
  if (el.classList.contains("wall-note--dragging")) return; // ignore for locally-dragging notes
  el.style.left = `${Math.round(x)}px`;
  el.style.top = `${Math.round(y)}px`;
  // Keep edges glued during remote-driven moves too, so other clients see
  // their connection lines follow the moving note in real time.
  if (wallSurface) {
    updateEdgesForNote(wallSurface, noteId, x + el.offsetWidth / 2, y + el.offsetHeight / 2);
  }
}

// ---- Delegated interaction orchestration --------------------------------

function bindSurfaceHandlers(state: Mounted): void {
  const surface = wallSurface;
  if (!surface) return;
  if (!state.canEdit) return;

  // Prevent native dblclick from selecting text or zooming; also acts as our
  // hook for note-vs-canvas distinctions that single pointerdown can't catch
  // when the user dblclicks without moving.
  surface.addEventListener("dblclick", (ev: MouseEvent) => {
    const target = ev.target as HTMLElement | null;
    if (!target) return;
    const noteEl = target.closest<HTMLElement>(".wall-note");
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
    // Empty-canvas double-click: create a note at pointer position.
    ev.preventDefault();
    const rect = surface.getBoundingClientRect();
    const x = ev.clientX - rect.left;
    const y = ev.clientY - rect.top;
    void createNoteAt(x, y);
  }, { signal: state.abort.signal });

  surface.addEventListener("pointerdown", (ev: PointerEvent) => {
    const target = ev.target as HTMLElement | null;
    if (!target) return;

    const noteEl = target.closest<HTMLElement>(".wall-note");

    // Click landed on the editor textarea itself: let native focus and text
    // editing handle it; pointer events stop here.
    if (target.classList.contains("wall-note__editor")) return;

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
      if (!noteId) return;
      // Don't hijack pointerdown while this specific note is being edited;
      // that lets the user click inside the textarea to move the caret.
      if (isEditing(noteEl)) return;
      // Postbaby parity: Shift+left-mouse on a note begins an edge drag.
      // Right-button is reserved for contextmenu (delete) - we never start
      // an edge drag from button !== 0.
      if (ev.shiftKey && ev.button === 0) {
        ev.preventDefault();
        beginEdgeDrag(state, ev, noteEl, noteId);
        return;
      }
      armNoteInteraction(state, ev, noteEl, noteId);
      return;
    }

    // Empty canvas: no action on pointerdown; creation is dblclick / RMB.
  }, { signal: state.abort.signal });

  // Postbaby parity: right-click on empty canvas adds a note; right-click on
  // an edge deletes it (after confirm); right-click on a note is swallowed
  // (no native menu, no creation) so it doesn't conflict with note drag.
  surface.addEventListener("contextmenu", (ev: MouseEvent) => {
    const target = ev.target as HTMLElement | null;
    if (!target) return;
    const noteEl = target.closest<HTMLElement>(".wall-note");
    const edgeHit = target.closest<SVGElement>(".wall-edge-hit");
    if (edgeHit) {
      ev.preventDefault();
      ev.stopPropagation();
      const groupNode = edgeHit.parentNode as Element | null;
      const edgeId =
        groupNode && groupNode instanceof SVGGElement
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

// ---- Shift+drag edge creation -------------------------------------------

function beginEdgeDrag(state: Mounted, ev: PointerEvent, sourceEl: HTMLElement, sourceId: string): void {
  if (!wallSurface) return;
  const surface = wallSurface;
  const surfaceRect = surface.getBoundingClientRect();
  const start = getNoteCenterFromElement(surface, sourceEl);
  const preview = beginEdgePreview(surface, start);
  // Initial endpoint follows the pointer immediately (Postbaby's preview
  // line jumps to the cursor as soon as the drag starts).
  preview.update(ev.clientX - surfaceRect.left, ev.clientY - surfaceRect.top);

  const onMove = (mv: PointerEvent) => {
    mv.preventDefault();
    preview.update(mv.clientX - surfaceRect.left, mv.clientY - surfaceRect.top);
  };
  const onUp = (up: PointerEvent) => {
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    document.removeEventListener("pointercancel", onUp);
    preview.end();

    // Resolve drop target via elementFromPoint, then walk to the nearest
    // .wall-note. Reject same-source drops; createEdge() handles dupes.
    const dropTarget = document.elementFromPoint(up.clientX, up.clientY) as HTMLElement | null;
    const targetNote = dropTarget?.closest<HTMLElement>(".wall-note") ?? null;
    if (!targetNote) return;
    const targetId = targetNote.dataset.noteId || "";
    if (!targetId || targetId === sourceId) return;
    void createEdge(sourceId, targetId);
  };
  document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
  document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
  document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}

function cancelColorTimer(state: Mounted, noteId: string): void {
  const t = state.colorTimers.get(noteId);
  if (t) {
    clearTimeout(t);
    state.colorTimers.delete(noteId);
  }
}

// Arm a potential drag. If pointer up without significant movement, treat as
// single-click (start delayed color cycle). If movement exceeds
// DRAG_THRESHOLD_PX, promote to a drag.
function armNoteInteraction(state: Mounted, ev: PointerEvent, noteEl: HTMLElement, noteId: string): void {
  ev.preventDefault();
  const startX = ev.clientX;
  const startY = ev.clientY;
  let promoted = false;

  const onMove = (mv: PointerEvent) => {
    if (promoted) return;
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
  const onUp = (up: PointerEvent) => {
    if (promoted) return;
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
      if (!note || !el) return;
      if (isEditing(el)) return;
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

// ---- Drag (document-scoped, rAF) ----------------------------------------

function beginDrag(state: Mounted, ev: PointerEvent, noteEl: HTMLElement, noteId: string, downX: number, downY: number): void {
  const note = findNote(noteId);
  if (!note) return;
  cancelColorTimer(state, noteId);

  const surface = wallSurface!;
  const surfaceRect = surface.getBoundingClientRect();
  const noteRect = noteEl.getBoundingClientRect();
  // shiftX/shiftY: offset from pointer-down position to the note's top-left,
  // measured within the surface. Using the original downX/downY (rather than
  // current pointer position) preserves the visual feel of Postbaby's
  // `shiftX = clientX - rect.left` so the note doesn't jump on drag start.
  const shiftX = downX - noteRect.left;
  const shiftY = downY - noteRect.top;

  noteEl.classList.add("wall-note--dragging");
  showTrash(true);

  let animationFrameId: number | null = null;
  let lastClientX = ev.clientX;
  let lastClientY = ev.clientY;

  function moveAt(clientX: number, clientY: number) {
    lastClientX = clientX;
    lastClientY = clientY;
    if (animationFrameId !== null) return;
    animationFrameId = requestAnimationFrame(() => {
      animationFrameId = null;
      const nx = Math.max(0, lastClientX - surfaceRect.left - shiftX);
      const ny = Math.max(0, lastClientY - surfaceRect.top - shiftY);
      noteEl.style.left = `${Math.round(nx)}px`;
      noteEl.style.top = `${Math.round(ny)}px`;
      scheduleTransient(state, noteId, nx, ny);
      // Keep any edges glued to this note while it moves under the cursor.
      if (wallSurface) {
        updateEdgesForNote(wallSurface, noteId, nx + noteEl.offsetWidth / 2, ny + noteEl.offsetHeight / 2);
      }
      updateTrashHover(noteEl);
    });
  }

  const onMove = (mv: PointerEvent) => {
    mv.preventDefault();
    moveAt(mv.clientX, mv.clientY);
  };
  const onUp = (up: PointerEvent) => {
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    document.removeEventListener("pointercancel", onUp);
    if (animationFrameId !== null) {
      cancelAnimationFrame(animationFrameId);
      animationFrameId = null;
    }
    noteEl.classList.remove("wall-note--dragging");

    const finalX = parseInt(noteEl.style.left || "0", 10);
    const finalY = parseInt(noteEl.style.top || "0", 10);
    const overlappingTrash = isOverTrash(noteEl);
    showTrash(false);

    if (overlappingTrash) {
      // Revert the visual position before confirming so the note doesn't
      // sit over the trash while the dialog is up.
      noteEl.style.left = `${Math.round(note.x)}px`;
      noteEl.style.top = `${Math.round(note.y)}px`;
      const ok = window.confirm("Delete this note?");
      if (ok) {
        void deleteNote(noteId);
      }
      return;
    }

    // Final transient flush + durable PATCH.
    scheduleTransient(state, noteId, finalX, finalY);
    flushTransient(state, noteId);
    if (finalX !== note.x || finalY !== note.y) {
      void patchNote(noteId, { x: finalX, y: finalY });
    }
    void up;
  };
  document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
  document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
  document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
}

function startResize(state: Mounted, ev: PointerEvent, noteEl: HTMLElement, noteId: string): void {
  const note = findNote(noteId);
  if (!note) return;
  const startX = ev.clientX;
  const startY = ev.clientY;
  const origW = note.width;
  const origH = note.height;

  const onMove = (mv: PointerEvent) => {
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

function beginEdit(noteEl: HTMLElement, note: WallNote): void {
  const state = mounted;
  if (!state || !state.canEdit) return;
  if (isEditing(noteEl)) return;
  const ta = enterEditMode(noteEl, note.text);
  ta.focus();
  // Caret at end for natural continuation.
  const end = ta.value.length;
  try { ta.setSelectionRange(end, end); } catch { /* ignore */ }

  let finished = false;
  const finish = (commit: boolean) => {
    if (finished) return;
    finished = true;
    const newText = ta.value;
    exitEditMode(noteEl, commit ? newText : note.text);
    ta.removeEventListener("blur", onBlur);
    ta.removeEventListener("keydown", onKey);
    if (commit && newText !== note.text) {
      void patchNote(note.id, { text: newText });
    }
  };
  const onBlur = () => finish(true);
  const onKey = (ev: KeyboardEvent) => {
    if (ev.key === "Escape") {
      ev.preventDefault();
      finish(false);
    } else if (ev.key === "Enter" && !ev.shiftKey) {
      ev.preventDefault();
      finish(true);
    }
    // Shift+Enter falls through to native newline insertion.
  };
  ta.addEventListener("blur", onBlur);
  ta.addEventListener("keydown", onKey);
}

// ---- Trash strip -------------------------------------------------------

function showTrash(visible: boolean): void {
  if (!wallTrash) return;
  wallTrash.classList.toggle("wall-trash--visible", visible);
  if (!visible) wallTrash.classList.remove("wall-trash--active");
}

function updateTrashHover(noteEl: HTMLElement): void {
  if (!wallTrash) return;
  wallTrash.classList.toggle("wall-trash--active", isOverTrash(noteEl));
}

function isOverTrash(noteEl: HTMLElement): boolean {
  if (!wallTrash) return false;
  const a = noteEl.getBoundingClientRect();
  const b = wallTrash.getBoundingClientRect();
  return !(a.right < b.left || a.left > b.right || a.bottom < b.top || a.top > b.bottom);
}

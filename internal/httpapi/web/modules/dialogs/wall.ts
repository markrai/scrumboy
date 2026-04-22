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

import {
  wallDialog,
  wallSurface,
  closeWallBtn,
  wallTrash,
} from "../dom/elements.js";
import {
  createEdgeRemote,
  createNote as createNoteRemote,
  deleteEdgeRemote,
  deleteNoteRemote,
  patchNoteRemote,
} from "./wall-api.js";
import { confirmDelete, showToast } from "../utils.js";
import { getUser } from "../state/selectors.js";
import { canEditWall, type WallRole } from "./wall-permissions.js";
import {
  buildNoteElement,
  renderEmptyWallHtml,
  isEditing,
  ensureEdgeOverlay,
  renderEdges,
  updateEdgesForNote,
  beginEdgePreview,
  getNoteCenterFromElement,
  type WallDocument,
  type WallEdge,
  type WallNote,
} from "./wall-rendering.js";
import {
  DOUBLE_TAP_MS,
  DRAG_THRESHOLD_PX,
  DEFAULT_NOTE_WIDTH,
  DEFAULT_NOTE_HEIGHT,
  RAINBOW_COLORS,
  nextColor,
} from "./wall-postbaby-constants.js";
import {
  getMounted,
  setMounted,
  resetEditGuards,
  setDragActive,
  type Mounted,
} from "./wall-state.js";
import {
  clearSelection,
  pruneSelection,
  setSelection,
  syncSelectionDom,
  toggleSelection,
} from "./wall-selection.js";
import {
  beginDrag as beginDragController,
  startResize as startResizeController,
} from "./wall-drag-controller.js";
import {
  applyTransient as applyTransientImpl,
  refetchDoc as refetchDocImpl,
  startRealtime,
} from "./wall-realtime.js";
import { beginEdit as beginEditController } from "./wall-edit-controller.js";
import { openWallNoteContextMenu } from "./wall-note-context-menu.js";

export interface OpenWallDialogOptions {
  projectId: number;
  slug: string;
  role: WallRole;
}

const TEARDOWN_MARKER = Symbol("wallMounted");

// Public entry: open the wall dialog and boot its full lifecycle.
export async function openWallDialog(opts: OpenWallDialogOptions): Promise<void> {
  if (!wallDialog || !wallSurface) {
    showToast("Wall is not available here.");
    return;
  }
  const dialog = wallDialog as HTMLDialogElement;
  if (getMounted()) {
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
    selected: new Set<string>(),
  };
  setMounted(state);
  (dialog as any)[TEARDOWN_MARKER] = true;

  wallSurface.classList.toggle("wall-surface--readonly", !canEdit);
  renderSurface();

  if (closeWallBtn) {
    closeWallBtn.addEventListener("click", () => dialog.close(), { signal: state.abort.signal });
  }
  dialog.addEventListener("close", teardown, { signal: state.abort.signal, once: true });
  dialog.addEventListener("cancel", () => dialog.close(), { signal: state.abort.signal });

  const stopRealtime = startRealtime({
    onRefreshNeeded: state.onRefreshNeeded,
    onTransient: state.onTransient,
  });
  state.abort.signal.addEventListener("abort", stopRealtime, { once: true });

  bindSurfaceHandlers(state);

  // Lock body scroll while the wall occupies the viewport.
  document.documentElement.style.overflow = "hidden";

  dialog.showModal();
  await refetchDoc();
}

function teardown(): void {
  const state = getMounted();
  if (!state) return;
  for (const t of state.colorTimers.values()) clearTimeout(t);
  state.colorTimers.clear();
  for (const entry of state.transient.values()) {
    if (entry.timer) clearTimeout(entry.timer);
  }
  state.transient.clear();
  state.selected.clear();
  state.abort.abort();
  if (wallSurface) wallSurface.innerHTML = "";
  if (wallTrash) {
    wallTrash.classList.remove("wall-trash--visible");
    wallTrash.classList.remove("wall-trash--active");
  }
  if (wallDialog) (wallDialog as any)[TEARDOWN_MARKER] = false;
  document.documentElement.style.overflow = state.prevHtmlOverflow;
  setMounted(null);
  resetEditGuards();
  setDragActive(false);
}

function refetchDoc(): Promise<void> {
  return refetchDocImpl({
    onApplyDoc: (state, doc) => {
      const next = normalizeDoc(doc);
      const diff = diffWallDoc(state.doc, next);
      state.doc = next;
      if (diff.kind === "full") {
        renderSurface();
        return;
      }
      // Fast path: single-note field changes (or nothing at all). No
      // innerHTML wipe, so any note that the user just saved does not
      // blink, and collaborative single-note updates are cheap.
      wallRenderCounters.incrementalPatches += 1;
      if (debugEnabled()) {
        console.debug("wall incremental apply", {
          fullRebuilds: wallRenderCounters.fullRebuilds,
          incrementalPatches: wallRenderCounters.incrementalPatches,
          changedNotes: diff.kind === "incremental" ? diff.changedNotes.length : 0,
          noop: diff.kind === "noop",
        });
      }
      if (diff.kind === "incremental") {
        for (const note of diff.changedNotes) updateNoteElement(note);
      }
    },
  });
}

type WallDocDiff =
  | { kind: "full" }
  | { kind: "noop" }
  | { kind: "incremental"; changedNotes: WallNote[] };

// Compare the currently-rendered doc against an incoming one. We only take
// the fast path when the set of notes and the set of edges are unchanged
// in identity and shape. Any add/remove/reorder, any edge endpoint change,
// or a mismatched count falls back to the full rebuild so renderer state
// (edge overlay, selection pruning, empty-wall placeholder) stays correct.
function diffWallDoc(prev: WallDocument, next: WallDocument): WallDocDiff {
  const prevNotes = prev.notes ?? [];
  const nextNotes = next.notes ?? [];
  const prevEdges = prev.edges ?? [];
  const nextEdges = next.edges ?? [];
  if (prevNotes.length !== nextNotes.length) return { kind: "full" };
  if (prevEdges.length !== nextEdges.length) return { kind: "full" };

  const prevNotesById = new Map<string, WallNote>();
  for (const n of prevNotes) prevNotesById.set(n.id, n);
  const changedNotes: WallNote[] = [];
  for (const n of nextNotes) {
    const prevNote = prevNotesById.get(n.id);
    if (!prevNote) return { kind: "full" };
    if (wallNoteFieldsDiffer(prevNote, n)) changedNotes.push(n);
  }

  const prevEdgesById = new Map<string, WallEdge>();
  for (const e of prevEdges) prevEdgesById.set(e.id, e);
  for (const e of nextEdges) {
    const prevEdge = prevEdgesById.get(e.id);
    if (!prevEdge) return { kind: "full" };
    // Endpoint change for the same id should never happen on the server,
    // but if it does we prefer the full rebuild since edge endpoints are
    // only repainted via renderEdges / updateEdgesForNote.
    if (prevEdge.from !== e.from || prevEdge.to !== e.to) return { kind: "full" };
  }

  if (changedNotes.length === 0) return { kind: "noop" };
  return { kind: "incremental", changedNotes };
}

function wallNoteFieldsDiffer(a: WallNote, b: WallNote): boolean {
  return (
    a.x !== b.x ||
    a.y !== b.y ||
    a.width !== b.width ||
    a.height !== b.height ||
    a.color !== b.color ||
    a.text !== b.text ||
    a.version !== b.version
  );
}

/** Test-only: expose the diff helper so unit tests can exercise it without mounting the wall. */
export const __diffWallDocForTest = diffWallDoc;

function applyTransient(payload: unknown): void {
  applyTransientImpl(payload, noteElementById);
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

// Phase 0/3 debug counters. `fullRebuilds` ticks whenever renderSurface()
// wipes and re-mounts the wall; `incrementalPatches` ticks whenever the
// Phase 3 fast path in refetchDoc() applies a single-note/no-op diff
// without touching innerHTML.
const wallRenderCounters = {
  fullRebuilds: 0,
  incrementalPatches: 0,
};

function debugEnabled(): boolean {
  return (globalThis as any).__scrumboyWallDebug === true;
}

/** Test helper: read the Phase 0 wall render counters. */
export function __getWallRenderCounters(): { fullRebuilds: number; incrementalPatches: number } {
  return {
    fullRebuilds: wallRenderCounters.fullRebuilds,
    incrementalPatches: wallRenderCounters.incrementalPatches,
  };
}

/** Test helper: reset the Phase 0 wall render counters between test cases. */
export function __resetWallRenderCounters(): void {
  wallRenderCounters.fullRebuilds = 0;
  wallRenderCounters.incrementalPatches = 0;
}

function renderSurface(): void {
  const state = getMounted();
  if (!state || !wallSurface) return;
  wallRenderCounters.fullRebuilds += 1;
  if (debugEnabled()) {
    console.debug("wall full rebuild", {
      fullRebuilds: wallRenderCounters.fullRebuilds,
      incrementalPatches: wallRenderCounters.incrementalPatches,
      notes: state.doc.notes.length,
      edges: state.doc.edges?.length ?? 0,
    });
  }
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
  return getMounted()?.doc.notes.find((n) => n.id === id);
}

function replaceNoteInDoc(updated: WallNote): void {
  const state = getMounted();
  if (!state) return;
  const idx = state.doc.notes.findIndex((n) => n.id === updated.id);
  if (idx >= 0) state.doc.notes[idx] = updated;
}

async function createNoteAt(x: number, y: number): Promise<void> {
  const state = getMounted();
  if (!state || !state.canEdit) return;
  try {
    const created = await createNoteRemote(state.slug, {
      x: Math.max(0, Math.round(x)),
      y: Math.max(0, Math.round(y)),
      width: DEFAULT_NOTE_WIDTH,
      height: DEFAULT_NOTE_HEIGHT,
      color: RAINBOW_COLORS[0],
      text: "",
    });
    if (getMounted() !== state) return;
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
  const state = getMounted();
  if (!state) return;
  const current = findNote(id);
  if (!current) return;
  try {
    const updated = await patchNoteRemote(state.slug, id, { ifVersion: current.version, ...patch });
    if (getMounted() !== state) return;
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
  const state = getMounted();
  if (!state) return;
  try {
    await deleteNoteRemote(state.slug, id);
    if (getMounted() !== state) return;
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
  const state = getMounted();
  if (!state || !state.canEdit) return;
  if (fromId === toId) return;
  // Local duplicate guard so we don't fire a useless POST when the user
  // re-draws an existing connection.
  const existing = (state.doc.edges ?? []).find(
    (e) => (e.from === fromId && e.to === toId) || (e.from === toId && e.to === fromId),
  );
  if (existing) return;
  try {
    const created = await createEdgeRemote(state.slug, fromId, toId);
    if (getMounted() !== state) return;
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
  const state = getMounted();
  if (!state || !state.canEdit) return;
  try {
    await deleteEdgeRemote(state.slug, edgeId);
    if (getMounted() !== state) return;
    if (state.doc.edges) {
      state.doc.edges = state.doc.edges.filter((e) => e.id !== edgeId);
    }
    if (wallSurface) renderEdges(wallSurface, state.doc.edges ?? []);
  } catch (err) {
    console.warn("wall edge delete failed", err);
    showToast("Could not delete connection");
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
    // Empty canvas: no create on double-click (right-click only).
    ev.preventDefault();
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
        startResizeController({
          state,
          ev,
          noteEl,
          noteId,
          findNote,
          onCommitResize: (id, width, height) => {
            void patchNote(id, { width, height });
          },
        });
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
      // Only primary-button presses participate in click/double-click/drag
      // note interactions. Right-click is handled by the contextmenu path
      // below; arming here would schedule a color-cycle timer and fire it
      // alongside the delete-confirm dialog.
      if (ev.button !== 0) return;
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
  // an edge or note deletes it (after confirm).
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
      if (edgeId) {
        void confirmDelete("Delete this connection?").then((confirmed) => {
          if (confirmed) {
            void deleteEdge(edgeId);
          }
        });
      }
      return;
    }
    if (noteEl) {
      ev.preventDefault();
      const noteId = noteEl.dataset.noteId || "";
      if (!noteId) return;
      // Defensive clear in case an input sequence armed a color timer
      // before the contextmenu event arrived.
      cancelColorTimer(state, noteId);

      // Parity with wall-drag-controller.ts drag-to-trash: treat this as a
      // group op only when the right-clicked note is itself part of a
      // multi-selection. A right-click on an unselected note (even if other
      // notes are selected) deletes only that note and leaves selection alone.
      const isGroup = state.selected.has(noteId) && state.selected.size > 1;
      const groupIds = isGroup ? Array.from(state.selected) : [noteId];
      const deleteLabel = isGroup ? `Delete ${groupIds.length} notes` : "Delete";
      const confirmPrompt = isGroup
        ? `Delete ${groupIds.length} notes?`
        : "Delete this note?";

      void openWallNoteContextMenu(ev.clientX, ev.clientY, state.abort.signal, {
        showCreateTodo: !isGroup,
        deleteLabel,
      }).then(async (choice) => {
        if (choice === "create-todo") {
          // Defensive: the Create-Todo item is hidden in the group case, so
          // this branch can only run for single-note menus.
          if (isGroup) return;
          const note = findNote(noteId);
          if (!note) return;
          // Dynamic import keeps todo.ts out of the wall bundle so the
          // lazy-loaded wall module stays small; the todo dialog is only
          // pulled in the first time a user picks this action.
          const mod = await import("./todo.js");
          await mod.openTodoDialog({
            mode: "create",
            role: state.role,
            initialTitle: note.text,
          });
        } else if (choice === "delete") {
          const confirmed = await confirmDelete(confirmPrompt);
          if (!confirmed) return;
          for (const id of groupIds) void deleteNote(id);
          if (isGroup) clearSelection();
        }
      });
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

function beginMarquee(state: Mounted, ev: PointerEvent): void {
  if (!wallSurface) return;
  const surface = wallSurface;
  const surfaceRect = surface.getBoundingClientRect();
  const startX = ev.clientX - surfaceRect.left;
  const startY = ev.clientY - surfaceRect.top;
  const downClientX = ev.clientX;
  const downClientY = ev.clientY;

  let marqueeEl: HTMLDivElement | null = null;
  let promoted = false;

  const ensureMarquee = (): HTMLDivElement => {
    if (!marqueeEl) {
      marqueeEl = document.createElement("div");
      marqueeEl.className = "wall-marquee";
      surface.appendChild(marqueeEl);
    }
    return marqueeEl;
  };

  const paint = (curX: number, curY: number) => {
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

  const onMove = (mv: PointerEvent) => {
    const dx = mv.clientX - downClientX;
    const dy = mv.clientY - downClientY;
    if (!promoted && dx * dx + dy * dy < DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX) return;
    promoted = true;
    mv.preventDefault();
    const curX = mv.clientX - surfaceRect.left;
    const curY = mv.clientY - surfaceRect.top;
    paint(curX, curY);
  };

  const onUp = (up: PointerEvent) => {
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
    const picked: string[] = [];
    for (const note of state.doc.notes) {
      const nLeft = note.x;
      const nTop = note.y;
      const nRight = note.x + note.width;
      const nBottom = note.y + note.height;
      const intersects = !(
        nRight < rect.left || nLeft > rect.right || nBottom < rect.top || nTop > rect.bottom
      );
      if (intersects) picked.push(note.id);
    }
    setSelection(picked);
  };

  document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
  document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
  document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
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
      beginDragController({
        state,
        ev: mv,
        noteEl,
        noteId,
        downX: startX,
        downY: startY,
        findNote,
        noteElementById,
        cancelColorTimer,
        onCommitDragPositions: (commits) => {
          for (const c of commits) void patchNote(c.id, { x: c.x, y: c.y });
        },
        onDropOnTrash: (participantIds, isGroup) => {
          const n = participantIds.length;
          const prompt = n === 1 ? "Delete this note?" : `Delete ${n} notes?`;
          void confirmDelete(prompt).then((ok) => {
            if (ok) {
              for (const id of participantIds) void deleteNote(id);
            }
            if (isGroup) clearSelection();
          });
        },
        onClearSelectionAfterGroupDrop: () => clearSelection(),
      });
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

// ---- Edit mode ----------------------------------------------------------

function beginEdit(noteEl: HTMLElement, note: WallNote): void {
  beginEditController(noteEl, note, {
    onCommitText: (id, text) => { void patchNote(id, { text }); },
    onFlushDeferredRefetch: () => { void refetchDoc(); },
  });
}

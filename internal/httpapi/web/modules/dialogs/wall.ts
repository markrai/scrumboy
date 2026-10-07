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
  patchStoryRemote,
  pinStoryRemote,
  unpinStoryRemote,
} from "./wall-api.js";
import { confirmDelete, showToast } from "../utils.js";
import { hydrateI18n, I18N_LOCALE_CHANGED, t } from "../i18n/index.js";
import { getBoard, getBoardMembers, getTagColors, getUser } from "../state/selectors.js";
import { canEditWall, type WallRole } from "./wall-permissions.js";
import {
  buildNoteElement,
  buildStoryElement,
  renderEmptyWallHtml,
  isEditing,
  ensureEdgeOverlay,
  renderEdges,
  updateEdgesForEndpoint,
  beginEdgePreview,
  type WallDocument,
  type WallEdge,
  type WallNote,
  type WallStory,
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
  beginStoryDrag,
  startResize as startResizeController,
} from "./wall-drag-controller.js";
import {
  applyTransient as applyTransientImpl,
  refetchDoc as refetchDocImpl,
  startRealtime,
} from "./wall-realtime.js";
import { beginEdit as beginEditController } from "./wall-edit-controller.js";
import { openWallNoteContextMenu } from "./wall-note-context-menu.js";
import { openWallStoryContextMenu } from "./wall-story-context-menu.js";
import { chooseWallStoryPosition } from "./wall-story-placement.js";
import {
  measureStoryCanvasRect,
  WALL_STORY_ESTIMATED_HEIGHT,
  WALL_STORY_WIDTH,
} from "./wall-story-geometry.js";
import {
  canonicalEndpointForElement,
  formatWallStoryEndpoint,
  parseWallEdgeEndpoint,
  storyEdgeCenter,
  wallEdgeEndpointCenter,
} from "./wall-edge-endpoint.js";
import {
  clampCanvasCoord,
  ensureWallContent,
  fitToNotes,
  getWallContent,
  getViewportState,
  initWallViewport,
  screenToCanvas,
  setViewportState,
  teardownWallViewport,
} from "./wall-viewport.js";
import {
  bindWallNavigation,
  cancelWallNavigationGestures,
  isSpacePanArmed,
} from "./wall-viewport-nav.js";
import {
  getWallCanvasMode,
  isWallPanMode,
  loadWallCanvasMode,
  toggleWallCanvasMode,
  type WallCanvasMode,
} from "./wall-canvas-mode.js";
import { buildPriorityTierMap } from "../views/board-rendering.js";

export interface OpenWallDialogOptions {
  projectId: number;
  slug: string;
  role: WallRole;
  /** When supplied, pin idempotently and focus this canonical story. */
  storyLocalId?: number;
}

const TEARDOWN_MARKER = Symbol("wallMounted");
const SELECT_MODE_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-square-dashed-icon lucide-square-dashed"><path d="M5 3a2 2 0 0 0-2 2"/><path d="M19 3a2 2 0 0 1 2 2"/><path d="M21 19a2 2 0 0 1-2 2"/><path d="M5 21a2 2 0 0 1-2-2"/><path d="M9 3h1"/><path d="M9 21h1"/><path d="M14 3h1"/><path d="M14 21h1"/><path d="M3 9v1"/><path d="M21 9v1"/><path d="M3 14v1"/><path d="M21 14v1"/></svg>`;
const PAN_MODE_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-hand-icon lucide-hand"><path d="M18 11V6a2 2 0 0 0-2-2a2 2 0 0 0-2 2"/><path d="M14 10V4a2 2 0 0 0-2-2a2 2 0 0 0-2 2v2"/><path d="M10 10.5V6a2 2 0 0 0-2-2a2 2 0 0 0-2 2v8"/><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15"/></svg>`;

function wallModeButtonState(mode: WallCanvasMode): {
  pressed: "true" | "false";
  label: string;
  title: string;
  svg: string;
} {
  if (mode === "pan") {
    return {
      pressed: "true",
      label: t("wall.mode.pan.label"),
      title: t("wall.mode.pan.title"),
      svg: PAN_MODE_SVG,
    };
  }
  return {
    pressed: "false",
    label: t("wall.mode.select.label"),
    title: t("wall.mode.select.title"),
    svg: SELECT_MODE_SVG,
  };
}

function syncWallCanvasModeUi(): void {
  const mode = getWallCanvasMode();
  if (wallSurface) {
    wallSurface.classList.toggle("wall-surface--pan-mode", mode === "pan");
    wallSurface.classList.toggle("wall-surface--select-mode", mode === "select");
    if (mode !== "pan") wallSurface.classList.remove("wall-surface--panning");
  }
  const btn = document.getElementById("wallModeToggleBtn");
  if (!(btn instanceof HTMLButtonElement)) return;
  const next = wallModeButtonState(mode);
  btn.setAttribute("aria-pressed", next.pressed);
  btn.setAttribute("aria-label", next.label);
  btn.setAttribute("title", next.title);
  btn.innerHTML = next.svg;
}

function toggleWallCanvasModeFromUi(): void {
  cancelWallNavigationGestures();
  toggleWallCanvasMode();
  syncWallCanvasModeUi();
  const modeBtn = document.getElementById("wallModeToggleBtn");
  if (modeBtn instanceof HTMLButtonElement) modeBtn.blur();
}

// Locale-change handler for the open wall. Relocalizes static shell chrome via
// hydrateI18n plus stateful text hydration cannot reach (mode button, trash
// alt, empty-state copy, per-note ARIA, open context-menu count label). Must
// never refetch, reopen, rebuild the note DOM, or reset viewport/selection/
// edit/context-menu state.
function onWallLocaleChange(): void {
  if (!getMounted()) return;
  if (wallDialog) hydrateI18n(wallDialog);
  syncWallLocaleState();
}

function syncWallLocaleState(): void {
  const state = getMounted();
  if (!state) return;

  // Mode button label/title/aria are state-derived; re-resolve via t() so the
  // current Pan/Select mode is preserved (hydrateI18n would otherwise stamp
  // the static Select-mode fallback).
  syncWallCanvasModeUi();

  // Trash alt is not covered by the hydrator's supported attributes.
  if (wallTrash) wallTrash.setAttribute("alt", t("wall.shell.trashAlt"));

  if (!wallSurface) return;

  // Empty-state placeholder: re-render its copy in place (no notes exist, so
  // no note DOM is touched). Skipped entirely when notes are present.
  const empty = wallSurface.querySelector(".wall-empty");
  if (empty) {
    empty.remove();
    wallSurface.insertAdjacentHTML("beforeend", renderEmptyWallHtml(state.canEdit));
  }

  // Existing note ARIA labels (resize handle + any open editor). Attribute-only
  // updates: the textarea node and its typed value/caret are never replaced.
  wallSurface.querySelectorAll(".wall-note__resize-handle").forEach((el) => {
    el.setAttribute("aria-label", t("wall.note.resize"));
  });
  wallSurface.querySelectorAll(".wall-note__editor").forEach((el) => {
    el.setAttribute("aria-label", t("wall.note.edit"));
  });
  wallSurface.querySelectorAll<HTMLElement>(".wall-story[data-story-local-id]").forEach((el) => {
    el.setAttribute("aria-label", `${t("wall.menu.openStory")} #${el.dataset.storyLocalId}`);
  });

  // An open note context menu hosted in #wallDialog: hydrateI18n already
  // handled the create item and the single-note delete label. Re-resolve the
  // count-based group delete label without moving the menu or changing IDs.
  if (wallDialog) {
    wallDialog.querySelectorAll<HTMLElement>(".wall-note-context-menu [data-i18n-count]").forEach((el) => {
      const count = Number(el.dataset.i18nCount);
      if (Number.isFinite(count)) {
        el.textContent = t("wall.menu.deleteCount", { count });
      }
    });
  }
}

// Public entry: open the wall dialog and boot its full lifecycle.
export async function openWallDialog(opts: OpenWallDialogOptions): Promise<void> {
  if (!wallDialog || !wallSurface) {
    showToast(t("wall.toast.unavailable"));
    return;
  }
  const dialog = wallDialog as HTMLDialogElement;
  if (getMounted()) {
    if (!dialog.open) dialog.showModal();
    if (opts.storyLocalId != null) await ensureStoryOnWall(opts.storyLocalId);
    return;
  }

  const canEdit = canEditWall(opts.role);
  const user = getUser();
  const state: Mounted = {
    projectId: opts.projectId,
    slug: opts.slug,
    role: opts.role,
    canEdit,
    doc: { notes: [], edges: [], stories: [], version: 0 },
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

  loadWallCanvasMode();
  wallSurface.classList.toggle("wall-surface--readonly", !canEdit);
  const content = ensureWallContent(wallSurface);
  initWallViewport(wallSurface, content, opts.slug);
  bindWallNavigation(wallSurface, state.abort.signal, () => dialog.open);
  syncWallCanvasModeUi();
  renderSurface();

  if (closeWallBtn) {
    closeWallBtn.addEventListener("click", () => dialog.close(), { signal: state.abort.signal });
  }
  const fitBtn = document.getElementById("wallFitViewBtn");
  if (fitBtn) {
    fitBtn.addEventListener("click", () => {
      const m = getMounted();
      if (m) fitToNotes(m.doc.notes, m.doc.stories);
      if (fitBtn instanceof HTMLButtonElement) fitBtn.blur();
    }, { signal: state.abort.signal });
  }
  const modeBtn = document.getElementById("wallModeToggleBtn");
  if (modeBtn instanceof HTMLButtonElement) {
    modeBtn.addEventListener("click", (ev) => {
      ev.preventDefault();
      toggleWallCanvasModeFromUi();
    }, { signal: state.abort.signal });
  }
  window.addEventListener("keydown", (ev: KeyboardEvent) => {
    if (!dialog.open) return;
    if (ev.target instanceof HTMLElement && ev.target.matches("textarea, input, select, [contenteditable='true']")) return;
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
    const m = getMounted();
    if (!m) return;
    if (ev.key === "f" || ev.key === "F") {
      ev.preventDefault();
      fitToNotes(m.doc.notes, m.doc.stories);
      return;
    }
    if (ev.key === "s" || ev.key === "S") {
      ev.preventDefault();
      toggleWallCanvasModeFromUi();
      return;
    }
    if (ev.key === "Delete") {
      if (ev.repeat) return;
      if (m.selected.size === 0) return;
      ev.preventDefault();
      confirmAndDeleteSelectedNotes(m);
    }
  }, { signal: state.abort.signal });
  dialog.addEventListener("close", teardown, { signal: state.abort.signal, once: true });
  dialog.addEventListener("cancel", () => dialog.close(), { signal: state.abort.signal });

  const stopRealtime = startRealtime({
    onRefreshNeeded: state.onRefreshNeeded,
    onTransient: state.onTransient,
  });
  state.abort.signal.addEventListener("abort", stopRealtime, { once: true });

  // Relocalize the open wall in place on locale change. Scoped to the wall's
  // AbortController so it is bound exactly once per open and removed on
  // teardown (no stacking across repeated open/close cycles). Never refetches
  // the wall doc or rebuilds note DOM wholesale.
  document.addEventListener(I18N_LOCALE_CHANGED, onWallLocaleChange, { signal: state.abort.signal });

  bindSurfaceHandlers(state);

  // Lock body scroll while the wall occupies the viewport.
  document.documentElement.style.overflow = "hidden";

  dialog.showModal();
  await refetchDoc();
  if (opts.storyLocalId != null) await ensureStoryOnWall(opts.storyLocalId);
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
  cancelWallNavigationGestures();
  syncWallCanvasModeUi();
  state.abort.abort();
  teardownWallViewport();
  if (wallSurface) {
    wallSurface.querySelectorAll(".wall-empty").forEach((el) => el.remove());
    const content = wallSurface.querySelector(".wall-content");
    if (content) content.innerHTML = "";
  }
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
        for (const story of diff.changedStories) updateStoryElement(story);
      }
    },
  });
}

type WallDocDiff =
  | { kind: "full" }
  | { kind: "noop" }
  | { kind: "incremental"; changedNotes: WallNote[]; changedStories: WallStory[] };

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
  const prevStories = prev.stories ?? [];
  const nextStories = next.stories ?? [];
  if (prevNotes.length !== nextNotes.length) return { kind: "full" };
  if (prevEdges.length !== nextEdges.length) return { kind: "full" };
  if (prevStories.length !== nextStories.length) return { kind: "full" };

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
    // only repainted via renderEdges / updateEdgesForEndpoint.
    if (prevEdge.from !== e.from || prevEdge.to !== e.to) return { kind: "full" };
  }

  const prevStoriesById = new Map<number, WallStory>();
  for (const story of prevStories) prevStoriesById.set(story.localId, story);
  const changedStories: WallStory[] = [];
  for (const story of nextStories) {
    const previous = prevStoriesById.get(story.localId);
    if (!previous) return { kind: "full" };
    if (
      previous.x !== story.x ||
      previous.y !== story.y ||
      previous.version !== story.version ||
      JSON.stringify(previous.todo) !== JSON.stringify(story.todo)
    ) changedStories.push(story);
  }

  if (changedNotes.length === 0 && changedStories.length === 0) return { kind: "noop" };
  return { kind: "incremental", changedNotes, changedStories };
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
  applyTransientImpl(payload, noteElementById, storyElementByLocalId);
}

function normalizeDoc(doc: WallDocument | null | undefined): WallDocument {
  if (!doc || !Array.isArray(doc.notes)) return { notes: [], edges: [], stories: [], version: 0 };
  return {
    notes: doc.notes.map((n) => ({ ...n })),
    edges: Array.isArray(doc.edges) ? doc.edges.map((e) => ({ ...e })) : [],
    stories: Array.isArray(doc.stories)
      ? doc.stories.map((story) => ({ ...story, todo: { ...story.todo, tags: [...(story.todo.tags ?? [])] } }))
      : [],
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

function wallContentLayer(): HTMLElement | null {
  if (!wallSurface) return null;
  return getWallContent() ?? ensureWallContent(wallSurface);
}

function renderSurface(): void {
  const state = getMounted();
  if (!state || !wallSurface) return;
  const content = wallContentLayer();
  if (!content) return;
  wallRenderCounters.fullRebuilds += 1;
  if (debugEnabled()) {
    console.debug("wall full rebuild", {
      fullRebuilds: wallRenderCounters.fullRebuilds,
      incrementalPatches: wallRenderCounters.incrementalPatches,
      notes: state.doc.notes.length,
      stories: state.doc.stories?.length ?? 0,
      edges: state.doc.edges?.length ?? 0,
    });
  }
  wallSurface.querySelectorAll(".wall-empty").forEach((el) => el.remove());
  content.innerHTML = "";
  if (state.doc.notes.length === 0 && (state.doc.stories?.length ?? 0) === 0) {
    wallSurface.insertAdjacentHTML("beforeend", renderEmptyWallHtml(state.canEdit));
    return;
  }
  const frag = document.createDocumentFragment();
  for (const note of state.doc.notes) {
    frag.appendChild(buildNoteElement(note, state.canEdit));
  }
  const stories = state.doc.stories ?? [];
  if (stories.length > 0) {
    const cardContext = wallCardRenderContext();
    for (const story of stories) {
      frag.appendChild(buildStoryElement(story, cardContext.members, cardContext.opts));
    }
  }
  content.appendChild(frag);
  // SVG overlay must be appended after notes and stories are in the DOM so
  // endpoint centers can read offsets on the freshly-mounted elements.
  ensureEdgeOverlay(content);
  renderEdges(content, state.doc.edges ?? [], getViewportState().zoom);
  // Drop selection entries whose notes no longer exist (remote delete,
  // server-side reconcile), then reapply the `--selected` class.
  pruneSelection();
  syncSelectionDom();
}

function wallCardRenderContext() {
  const members = Object.fromEntries(getBoardMembers().map((member) => [member.userId, member]));
  const board = getBoard();
  return {
    members,
    opts: {
      tagColors: getTagColors(),
      showPointsMode: board?.project?.estimationMode == null || board.project.estimationMode === "MODIFIED_FIBONACCI",
      priorityTiers: board ? buildPriorityTierMap(board) : undefined,
    },
  };
}

function noteElementById(id: string): HTMLElement | null {
  if (!wallSurface) return null;
  return wallSurface.querySelector<HTMLElement>(`.wall-note[data-note-id="${CSS.escape(id)}"]`);
}

function storyElementByLocalId(localId: number): HTMLElement | null {
  if (!wallSurface) return null;
  return wallSurface.querySelector<HTMLElement>(`.wall-story[data-story-local-id="${localId}"]`);
}

function updateStoryElement(story: WallStory): void {
  const current = storyElementByLocalId(story.localId);
  if (!current || current.classList.contains("wall-story--dragging")) return;
  const cardContext = wallCardRenderContext();
  const replacement = buildStoryElement(story, cardContext.members, cardContext.opts);
  current.replaceWith(replacement);
  // Keep incident mixed edges glued to the replacement's measured center
  // without requiring a full wall rebuild.
  const content = wallContentLayer();
  if (content) {
    const resolved = storyEdgeCenter(content, story.localId, getViewportState().zoom);
    if (resolved) updateEdgesForEndpoint(content, resolved.endpoint, resolved.cx, resolved.cy);
  }
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
  const content = wallContentLayer();
  if (content) {
    updateEdgesForEndpoint(content, note.id, note.x + note.width / 2, note.y + note.height / 2);
  }
}

function findNote(id: string): WallNote | undefined {
  return getMounted()?.doc.notes.find((n) => n.id === id);
}

function findStory(localId: number): WallStory | undefined {
  return getMounted()?.doc.stories?.find((story) => story.localId === localId);
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
      x: Math.round(clampCanvasCoord(x)),
      y: Math.round(clampCanvasCoord(y)),
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
    showToast(t("wall.toast.addNoteFailed"));
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
      showToast(t("wall.toast.staleReload"));
      await refetchDoc();
      return;
    }
    console.warn("wall patch failed", err);
    showToast(t("wall.toast.updateNoteFailed"));
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
    showToast(t("wall.toast.deleteNoteFailed"));
  }
}

function visibleCanvasRect(): { x: number; y: number; width: number; height: number } {
  const rect = wallSurface?.getBoundingClientRect() ?? new DOMRect(0, 0, 800, 600);
  const topLeft = screenToCanvas(rect.left, rect.top);
  const bottomRight = screenToCanvas(rect.right, rect.bottom);
  return {
    x: topLeft.x,
    y: topLeft.y,
    width: Math.max(1, bottomRight.x - topLeft.x),
    height: Math.max(1, bottomRight.y - topLeft.y),
  };
}

function automaticStoryPosition(state: Mounted): { x: number; y: number } {
  const viewport = getViewportState();
  const occupied = [
    ...state.doc.notes.map((note) => ({ x: note.x, y: note.y, width: note.width, height: note.height })),
    ...(state.doc.stories ?? []).map((story) => measureStoryCanvasRect(
      story,
      storyElementByLocalId(story.localId),
      viewport.zoom,
    )),
  ];
  return chooseWallStoryPosition(visibleCanvasRect(), occupied, {
    width: WALL_STORY_WIDTH,
    height: WALL_STORY_ESTIMATED_HEIGHT,
  });
}

async function pinStoryAt(localId: number, x: number, y: number): Promise<void> {
  const state = getMounted();
  if (!state || !state.canEdit) return;
  const existing = findStory(localId);
  if (existing) {
    focusStory(localId);
    return;
  }
  try {
    await pinStoryRemote(state.slug, {
      localId,
      x: Math.round(clampCanvasCoord(x)),
      y: Math.round(clampCanvasCoord(y)),
    });
    if (getMounted() !== state) return;
    await refetchDoc();
    focusStory(localId);
  } catch (err) {
    console.warn("wall pin story failed", err);
    showToast(t("wall.toast.pinStoryFailed"));
  }
}

async function ensureStoryOnWall(localId: number): Promise<void> {
  const state = getMounted();
  if (!state) return;
  if (findStory(localId)) {
    focusStory(localId);
    return;
  }
  if (!state.canEdit) return;
  const position = automaticStoryPosition(state);
  await pinStoryAt(localId, position.x, position.y);
}

function focusStory(localId: number): void {
  const story = findStory(localId);
  const element = storyElementByLocalId(localId);
  const surface = wallSurface;
  if (!story || !element || !surface) return;
  const viewport = getViewportState();
  const measured = measureStoryCanvasRect(story, element, viewport.zoom);
  const rect = surface.getBoundingClientRect();
  setViewportState({
    ...viewport,
    panX: rect.width / 2 - (story.x + measured.width / 2) * viewport.zoom,
    panY: rect.height / 2 - (story.y + measured.height / 2) * viewport.zoom,
  });
  element.classList.add("wall-story--highlight");
  setTimeout(() => element.classList.remove("wall-story--highlight"), 1600);
}

async function patchStory(localId: number, x: number, y: number): Promise<void> {
  const state = getMounted();
  const current = findStory(localId);
  if (!state || !current) return;
  try {
    const placement = await patchStoryRemote(state.slug, localId, {
      ifVersion: current.version,
      x: Math.round(clampCanvasCoord(x)),
      y: Math.round(clampCanvasCoord(y)),
    });
    if (getMounted() !== state) return;
    const story = findStory(localId);
    if (!story) return;
    Object.assign(story, placement);
    updateStoryElement(story);
  } catch (err: any) {
    if (err?.status === 409) {
      showToast(t("wall.toast.staleReload"));
      await refetchDoc();
      return;
    }
    console.warn("wall move story failed", err);
    showToast(t("wall.toast.moveStoryFailed"));
    await refetchDoc();
  }
}

async function unpinStory(localId: number): Promise<void> {
  const state = getMounted();
  if (!state || !state.canEdit) return;
  try {
    await unpinStoryRemote(state.slug, localId);
    if (getMounted() !== state) return;
    state.doc.stories = (state.doc.stories ?? []).filter((story) => story.localId !== localId);
    renderSurface();
  } catch (err) {
    console.warn("wall unpin story failed", err);
    showToast(t("wall.toast.removeStoryFailed"));
  }
}

async function openStory(localId: number): Promise<void> {
  const state = getMounted();
  const story = findStory(localId);
  if (!state || !story) return;
  const mod = await import("./todo.js");
  await mod.openTodoDialog({ mode: "edit", role: state.role, todo: story.todo });
}

function confirmAndDeleteNotes(state: Mounted, ids: string[], isGroup: boolean): void {
  if (!state.canEdit || ids.length === 0) return;
  const prompt = isGroup
    ? t("wall.confirm.deleteNotesCount", { count: ids.length })
    : t("wall.confirm.deleteNote");
  void confirmDelete(prompt).then((confirmed) => {
    if (!confirmed) return;
    for (const id of ids) void deleteNote(id);
    if (isGroup) clearSelection();
  });
}

function confirmAndDeleteSelectedNotes(state: Mounted): void {
  if (state.selected.size === 0) return;
  const isGroup = state.selected.size > 1;
  confirmAndDeleteNotes(state, Array.from(state.selected), isGroup);
}

async function createEdge(fromEndpoint: string, toEndpoint: string): Promise<void> {
  const state = getMounted();
  if (!state || !state.canEdit) return;
  if (fromEndpoint === toEndpoint) return;
  // Local duplicate guard so we don't fire a useless POST when the user
  // re-draws an existing connection. Both endpoints are canonical, so plain
  // string comparison covers note and story endpoints in either direction.
  const existing = (state.doc.edges ?? []).find(
    (e) => (e.from === fromEndpoint && e.to === toEndpoint) || (e.from === toEndpoint && e.to === fromEndpoint),
  );
  if (existing) return;
  try {
    const created = await createEdgeRemote(state.slug, fromEndpoint, toEndpoint);
    if (getMounted() !== state) return;
    if (!state.doc.edges) state.doc.edges = [];
    // Server returns the existing edge on duplicate (idempotent); de-dupe.
    if (!state.doc.edges.some((e) => e.id === created.id)) {
      state.doc.edges.push(created);
    }
    const content = wallContentLayer();
    if (content) renderEdges(content, state.doc.edges, getViewportState().zoom);
  } catch (err) {
    console.warn("wall edge create failed", err);
    showToast(t("wall.toast.drawEdgeFailed"));
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
    const content = wallContentLayer();
    if (content) renderEdges(content, state.doc.edges ?? [], getViewportState().zoom);
  } catch (err) {
    console.warn("wall edge delete failed", err);
    showToast(t("wall.toast.deleteEdgeFailed"));
  }
}

// ---- Delegated interaction orchestration --------------------------------

function bindSurfaceHandlers(state: Mounted): void {
  const surface = wallSurface;
  if (!surface) return;

  // Prevent native dblclick from selecting text or zooming; also acts as our
  // hook for note-vs-canvas distinctions that single pointerdown can't catch
  // when the user dblclicks without moving.
  surface.addEventListener("dblclick", (ev: MouseEvent) => {
    const target = ev.target as HTMLElement | null;
    if (!target) return;
    const noteEl = target.closest<HTMLElement>(".wall-note");
    if (noteEl && state.canEdit) {
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
    const storyEl = target.closest<HTMLElement>(".wall-story");

    if (storyEl) {
      if (isSpacePanArmed() || ev.button !== 0) return;
      const localId = Number(storyEl.dataset.storyLocalId);
      if (!Number.isSafeInteger(localId) || localId <= 0) return;
      if (!state.canEdit) {
        ev.preventDefault();
        void openStory(localId);
        return;
      }
      // Shift+primary begins an edge drag, taking precedence over the
      // ordinary story open/drag interaction below.
      if (ev.shiftKey && ev.button === 0) {
        ev.preventDefault();
        beginEdgeDrag(state, ev, formatWallStoryEndpoint(localId));
        return;
      }
      armStoryInteraction(state, ev, storyEl, localId);
      return;
    }

    // Click landed on the editor textarea itself: let native focus and text
    // editing handle it; pointer events stop here.
    if (target.classList.contains("wall-note__editor")) return;

    // Resize handle starts a resize, not a drag or color cycle.
    if (state.canEdit && target.classList.contains("wall-note__resize-handle") && noteEl) {
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
      if (!state.canEdit) return;
      if (isSpacePanArmed()) return;
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
        beginEdgeDrag(state, ev, noteId);
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
    // Space+drag is reserved for viewport pan (wall-viewport-nav).
    if (state.canEdit && ev.button === 0 && !ev.shiftKey && !isSpacePanArmed() && !isWallPanMode()) {
      beginMarquee(state, ev);
      return;
    }
  }, { signal: state.abort.signal });

  // Postbaby parity: right-click on empty canvas adds a note; right-click on
  // an edge or note deletes it (after confirm).
  surface.addEventListener("contextmenu", (ev: MouseEvent) => {
    const target = ev.target as HTMLElement | null;
    if (!target) return;
    const storyEl = target.closest<HTMLElement>(".wall-story");
    const noteEl = target.closest<HTMLElement>(".wall-note");
    const edgeHit = target.closest<SVGElement>(".wall-edge-hit");
    if (storyEl) {
      ev.preventDefault();
      ev.stopPropagation();
      const localId = Number(storyEl.dataset.storyLocalId);
      if (!Number.isSafeInteger(localId) || localId <= 0) return;
      void openWallStoryContextMenu(ev.clientX, ev.clientY, state.abort.signal, state.canEdit).then((choice) => {
        if (choice === "open") void openStory(localId);
        if (choice === "remove") void unpinStory(localId);
      });
      return;
    }
    if (edgeHit && state.canEdit) {
      ev.preventDefault();
      ev.stopPropagation();
      const groupNode = edgeHit.parentNode as Element | null;
      const edgeId =
        groupNode && groupNode instanceof SVGGElement
          ? groupNode.dataset?.edgeId || ""
          : "";
      if (edgeId) {
        void confirmDelete(t("wall.confirm.deleteConnection")).then((confirmed) => {
          if (confirmed) {
            void deleteEdge(edgeId);
          }
        });
      }
      return;
    }
    if (noteEl && state.canEdit) {
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

      void openWallNoteContextMenu(ev.clientX, ev.clientY, state.abort.signal, {
        showCreateTodo: !isGroup,
        deleteCount: isGroup ? groupIds.length : undefined,
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
          confirmAndDeleteNotes(state, groupIds, isGroup);
        }
      });
      return;
    }
    ev.preventDefault();
    const { x, y } = screenToCanvas(ev.clientX, ev.clientY);
    if (!state.canEdit) return;
    if (ev.ctrlKey) {
      void import("./todo.js").then((mod) => mod.openTodoDialog({
        mode: "create",
        role: state.role,
        onCreated: (todo) => pinStoryAt(todo.localId, x, y),
      }));
      return;
    }
    void createNoteAt(x, y);
  }, { signal: state.abort.signal });
}

// ---- Marquee multi-select (empty-canvas drag) --------------------------

function beginMarquee(state: Mounted, ev: PointerEvent): void {
  const content = wallContentLayer();
  if (!content) return;
  const start = screenToCanvas(ev.clientX, ev.clientY);
  const startX = start.x;
  const startY = start.y;
  const downClientX = ev.clientX;
  const downClientY = ev.clientY;

  let marqueeEl: HTMLDivElement | null = null;
  let promoted = false;

  const ensureMarquee = (): HTMLDivElement => {
    if (!marqueeEl) {
      marqueeEl = document.createElement("div");
      marqueeEl.className = "wall-marquee";
      content.appendChild(marqueeEl);
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
    // Screen-space OK: promotion threshold is a screen-pixel feel constant.
    if (!promoted && dx * dx + dy * dy < DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX) return;
    promoted = true;
    mv.preventDefault();
    const cur = screenToCanvas(mv.clientX, mv.clientY);
    paint(cur.x, cur.y);
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
    const end = screenToCanvas(up.clientX, up.clientY);
    const endX = end.x;
    const endY = end.y;
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

function beginEdgeDrag(state: Mounted, ev: PointerEvent, sourceEndpoint: string): void {
  const content = wallContentLayer();
  if (!content) return;
  const parsed = parseWallEdgeEndpoint(sourceEndpoint);
  const start = parsed ? wallEdgeEndpointCenter(content, parsed, getViewportState().zoom) : null;
  if (!start) return;
  const preview = beginEdgePreview(content, start);
  const initial = screenToCanvas(ev.clientX, ev.clientY);
  preview.update(initial.x, initial.y);

  const onMove = (mv: PointerEvent) => {
    mv.preventDefault();
    const pt = screenToCanvas(mv.clientX, mv.clientY);
    preview.update(pt.x, pt.y);
  };
  const onUp = (up: PointerEvent) => {
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    document.removeEventListener("pointercancel", onCancel);
    preview.end();

    // Screen-space OK: elementFromPoint is a screen-based hit test API.
    // Only pointerup may resolve a drop and create an edge.
    const dropTarget = document.elementFromPoint(up.clientX, up.clientY) as HTMLElement | null;
    const targetEndpoint = canonicalEndpointForElement(dropTarget);
    if (!targetEndpoint || targetEndpoint === sourceEndpoint) return;
    void createEdge(sourceEndpoint, targetEndpoint);
  };
  const onCancel = () => {
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    document.removeEventListener("pointercancel", onCancel);
    preview.end();
  };
  document.addEventListener("pointermove", onMove, { signal: state.abort.signal, passive: false });
  document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
  document.addEventListener("pointercancel", onCancel, { signal: state.abort.signal });
}

function cancelColorTimer(state: Mounted, noteId: string): void {
  const t = state.colorTimers.get(noteId);
  if (t) {
    clearTimeout(t);
    state.colorTimers.delete(noteId);
  }
}

function armStoryInteraction(
  state: Mounted,
  ev: PointerEvent,
  storyEl: HTMLElement,
  localId: number,
): void {
  ev.preventDefault();
  const startX = ev.clientX;
  const startY = ev.clientY;
  let promoted = false;
  const onMove = (move: PointerEvent) => {
    if (promoted) return;
    const dx = move.clientX - startX;
    const dy = move.clientY - startY;
    if (dx * dx + dy * dy < DRAG_THRESHOLD_PX * DRAG_THRESHOLD_PX) return;
    const story = findStory(localId);
    if (!story) return;
    promoted = true;
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    document.removeEventListener("pointercancel", onUp);
    beginStoryDrag({
      state,
      ev: move,
      storyEl,
      story,
      downX: startX,
      downY: startY,
      onCommit: (id, x, y) => { void patchStory(id, x, y); },
    });
  };
  const onUp = () => {
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    document.removeEventListener("pointercancel", onUp);
    if (!promoted) void openStory(localId);
  };
  document.addEventListener("pointermove", onMove, { signal: state.abort.signal });
  document.addEventListener("pointerup", onUp, { signal: state.abort.signal });
  document.addEventListener("pointercancel", onUp, { signal: state.abort.signal });
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
    // Screen-space OK: drag promotion uses screen-pixel threshold.
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
          const prompt = n === 1
            ? t("wall.confirm.deleteNote")
            : t("wall.confirm.deleteNotesCount", { count: n });
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

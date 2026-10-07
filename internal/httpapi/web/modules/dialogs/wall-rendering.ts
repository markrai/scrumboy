// Pure rendering helpers for the Scrumbaby sticky-note wall.
//
// Postbaby-parity note DOM model:
//   - Display mode is the resting state: a `.wall-note__display` <div> with
//     textContent + `white-space: pre-wrap`. No contenteditable. No HTML
//     injection.
//   - Edit mode is a short-lived `<textarea class="wall-note__editor">`
//     mounted by enterEditMode() and removed by exitEditMode(). Only the
//     edit mode exposes a text-input element - never an always-visible one.
//   - Corner resize handle is kept (user decision). A per-note delete button
//     is intentionally NOT rendered; deletion happens by dragging over the
//     trash strip owned by wall.ts.
//
// This file is DOM-only: no network, no state mutations. wall.ts owns
// orchestration, event wiring, and teardown.

import { escapeHTML, HEX_COLOR_RE, sanitizeHexColor } from "../utils.js";
import { t } from "../i18n/index.js";
import { colorIndexFromHex } from "./wall-postbaby-constants.js";
import { screenToCanvas } from "./wall-viewport.js";
import type { Todo } from "../types.js";
import type { BoardMember } from "../state/state.js";
import { renderStoryCardContent, type RenderTodoCardOpts } from "../views/board-rendering.js";

export interface WallNote {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  color: string;
  text: string;
  version: number;
}

// WallEdge mirrors the backend store: undirected connection between two
// notes. No per-edge version - the document version on WallDocument is the
// only realtime fingerprint.
export interface WallEdge {
  id: string;
  from: string;
  to: string;
}

export interface WallStory {
  localId: number;
  x: number;
  y: number;
  version: number;
  todo: Todo;
}

export interface WallDocument {
  notes: WallNote[];
  edges?: WallEdge[];
  stories?: WallStory[];
  version: number;
  updatedAt?: number;
}

export const WALL_STORY_WIDTH = 280;
export const WALL_STORY_HEIGHT = 148;

export function buildStoryElement(
  story: WallStory,
  membersByUserId?: Record<number, BoardMember>,
  opts?: RenderTodoCardOpts,
): HTMLElement {
  const el = document.createElement("div");
  const archivedClass = story.todo.archivedAt ? " wall-story--archived" : "";
  el.className = `wall-story card card--${story.todo.status.toLowerCase()}${archivedClass}`;
  el.dataset.storyLocalId = String(story.localId);
  el.dataset.version = String(story.version);
  el.dataset.todoId = String(story.todo.id);
  el.style.left = `${Math.round(story.x)}px`;
  el.style.top = `${Math.round(story.y)}px`;
  el.style.width = `${WALL_STORY_WIDTH}px`;
  el.setAttribute("role", "button");
  el.setAttribute("tabindex", "0");
  el.setAttribute("aria-label", `${t("wall.menu.openStory")} #${story.localId}`);
  el.innerHTML = renderStoryCardContent(story.todo, membersByUserId, opts);
  return el;
}

const DEFAULT_NOTE_COLOR = "#ffd966";

// Clamp to the same limits the backend enforces. Keep in sync with
// internal/store/wall.go (clampNoteDim / validateWallColor).
export const MIN_NOTE_WIDTH = 120;
export const MIN_NOTE_HEIGHT = 80;
export const MAX_NOTE_WIDTH = 600;
export const MAX_NOTE_HEIGHT = 600;

export function sanitizeNoteColor(color: string | null | undefined): string {
  const safe = sanitizeHexColor(color || "");
  if (safe && HEX_COLOR_RE.test(safe)) {
    return safe;
  }
  return DEFAULT_NOTE_COLOR;
}

export function clampDim(v: number, min: number, max: number): number {
  if (!Number.isFinite(v)) return min;
  return Math.max(min, Math.min(max, v));
}

export function noteStyleAttr(n: Pick<WallNote, "x" | "y" | "width" | "height" | "color">): string {
  const w = clampDim(n.width, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH);
  const h = clampDim(n.height, MIN_NOTE_HEIGHT, MAX_NOTE_HEIGHT);
  const color = sanitizeNoteColor(n.color);
  return `left:${Math.round(n.x)}px;top:${Math.round(n.y)}px;width:${Math.round(w)}px;height:${Math.round(h)}px;background:${color};`;
}

export function buildNoteElement(note: WallNote, canEdit: boolean): HTMLElement {
  const el = document.createElement("div");
  el.className = "wall-note";
  el.dataset.noteId = note.id;
  el.dataset.version = String(note.version);
  el.dataset.colorIndex = String(colorIndexFromHex(note.color));
  el.setAttribute("style", noteStyleAttr(note));

  const display = document.createElement("div");
  display.className = "wall-note__display";
  display.textContent = note.text;
  el.appendChild(display);

  if (canEdit) {
    const handle = document.createElement("div");
    handle.className = "wall-note__resize-handle";
    handle.setAttribute("aria-label", t("wall.note.resize"));
    handle.setAttribute("role", "slider");
    el.appendChild(handle);
  }

  return el;
}

// Swap the display div for a Postbaby-style edit textarea. Returns the
// textarea so wall.ts can wire commit/cancel handlers and focus it.
export function enterEditMode(noteEl: HTMLElement, initialText: string): HTMLTextAreaElement {
  const existing = noteEl.querySelector<HTMLTextAreaElement>(".wall-note__editor");
  if (existing) return existing;

  noteEl.classList.add("wall-note--editing");
  const display = noteEl.querySelector<HTMLElement>(".wall-note__display");
  if (display) display.remove();

  const ta = document.createElement("textarea");
  ta.className = "wall-note__editor";
  ta.value = initialText;
  ta.maxLength = 5000;
  ta.spellcheck = true;
  ta.setAttribute("aria-label", t("wall.note.edit"));
  // Insert before the resize handle so the handle stays on top.
  const handle = noteEl.querySelector<HTMLElement>(".wall-note__resize-handle");
  if (handle) {
    noteEl.insertBefore(ta, handle);
  } else {
    noteEl.appendChild(ta);
  }
  return ta;
}

// Remove the textarea, restore the display div with the given text. Safe to
// call when edit mode is not active (no-op in that case).
export function exitEditMode(noteEl: HTMLElement, text: string): void {
  const ta = noteEl.querySelector<HTMLTextAreaElement>(".wall-note__editor");
  if (ta) ta.remove();
  noteEl.classList.remove("wall-note--editing");
  let display = noteEl.querySelector<HTMLElement>(".wall-note__display");
  if (!display) {
    display = document.createElement("div");
    display.className = "wall-note__display";
    const handle = noteEl.querySelector<HTMLElement>(".wall-note__resize-handle");
    if (handle) {
      noteEl.insertBefore(display, handle);
    } else {
      noteEl.appendChild(display);
    }
  }
  display.textContent = text;
}

export function isEditing(noteEl: HTMLElement): boolean {
  return noteEl.classList.contains("wall-note--editing");
}

export function renderEmptyWallHtml(canEdit: boolean): string {
  const hint = canEdit
    ? t("wall.empty.hintEditable")
    : t("wall.empty.hintReadonly");
  return `<div class="wall-empty" role="status">${escapeHTML(t("wall.empty.title"))}<br/><span class="muted">${escapeHTML(hint)}</span></div>`;
}

// =====================================================================
// EDGE OVERLAY (Postbaby parity: Shift+drag draws lines between notes)
//
// The overlay is a single SVG positioned absolutely over the wall surface.
// Notes are still positioned in the surface's normal flow; the overlay sits
// above them with `pointer-events: none` so notes still receive pointer
// events. Each edge gets a `<g>` containing:
//   - a wide transparent hit line (`pointer-events: stroke`) for context
//     menu clicks
//   - a thin visible line painted on top, non-interactive
// The visible coordinates are the centers of the connected note elements
// (read at render time, so resizes/moves work after a re-render).
// =====================================================================

const SVG_NS = "http://www.w3.org/2000/svg";
const EDGE_OVERLAY_ID = "wallEdgeOverlay";
const EDGE_HIT_STROKE_WIDTH = 14;
const EDGE_LINE_STROKE = "#9ec6ff";
const EDGE_LINE_OPACITY = "0.8";
const EDGE_PREVIEW_STROKE = "#9ec6ff";
const EDGE_PREVIEW_OPACITY = "0.55";

export function ensureEdgeOverlay(surface: HTMLElement): SVGSVGElement {
  let svg = surface.querySelector<SVGSVGElement>(`#${EDGE_OVERLAY_ID}`);
  if (svg) return svg;
  svg = document.createElementNS(SVG_NS, "svg") as SVGSVGElement;
  svg.setAttribute("id", EDGE_OVERLAY_ID);
  svg.setAttribute("class", "wall-edge-overlay");
  // The overlay lives inside `.wall-content`, which is intentionally 0x0 (it
  // is only a transform anchor; notes are absolutely positioned). Edges are
  // drawn in canvas coordinates and rely on `overflow: visible` to paint
  // outside that box. Chromium will NOT paint SVG geometry that lies outside a
  // *zero-sized* outer <svg> viewport (Firefox/WebKit tolerate it), so a
  // `width/height: 100%` (== 0 here) overlay renders every connection line
  // invisibly in Chrome/Edge. Give the SVG a non-zero box so painting is
  // enabled; `overflow: visible` then lets lines extend to any canvas coord
  // (including negative) in all engines. See wall-rendering.test.ts.
  svg.style.position = "absolute";
  svg.style.left = "0";
  svg.style.top = "0";
  svg.style.width = "1px";
  svg.style.height = "1px";
  svg.style.overflow = "visible";
  svg.style.pointerEvents = "none";
  // Postbaby parity: lines paint *under* notes (.wall-note is z-index: 2 in
  // styles.css). This overlay uses z-index: 0 via .wall-edge-overlay; a
  // dragging note still rises above everything via .wall-note--dragging.
  surface.appendChild(svg);
  return svg;
}

function noteCenter(surface: HTMLElement, noteId: string): { cx: number; cy: number } | null {
  const el = surface.querySelector<HTMLElement>(`.wall-note[data-note-id="${CSS.escape(noteId)}"]`);
  if (!el) return null;
  // Use offsetLeft/Top + offsetWidth/Height so coordinates are relative to
  // the surface (matches where SVG sits) rather than the viewport.
  const cx = el.offsetLeft + el.offsetWidth / 2;
  const cy = el.offsetTop + el.offsetHeight / 2;
  return { cx, cy };
}

export function renderEdges(surface: HTMLElement, edges: WallEdge[]): void {
  const svg = ensureEdgeOverlay(surface);
  // Drop only edge groups; preserve any in-progress preview line so the
  // user's drag isn't visually interrupted by a re-render mid-flight.
  const groups = svg.querySelectorAll(".wall-edge-group");
  groups.forEach((g) => g.remove());

  for (const edge of edges) {
    if (!edge || !edge.id || !edge.from || !edge.to) continue;
    if (edge.from === edge.to) continue;
    const a = noteCenter(surface, edge.from);
    const b = noteCenter(surface, edge.to);
    if (!a || !b) continue;

    const g = document.createElementNS(SVG_NS, "g");
    g.setAttribute("class", "wall-edge-group");
    (g as SVGGElement).dataset.edgeId = edge.id;
    (g as SVGGElement).dataset.from = edge.from;
    (g as SVGGElement).dataset.to = edge.to;

    const hit = document.createElementNS(SVG_NS, "line");
    hit.setAttribute("class", "wall-edge-hit");
    hit.setAttribute("x1", String(a.cx));
    hit.setAttribute("y1", String(a.cy));
    hit.setAttribute("x2", String(b.cx));
    hit.setAttribute("y2", String(b.cy));
    hit.setAttribute("stroke", "transparent");
    hit.setAttribute("stroke-width", String(EDGE_HIT_STROKE_WIDTH));
    hit.setAttribute("stroke-linecap", "round");
    hit.setAttribute("pointer-events", "stroke");
    (hit as SVGLineElement).style.cursor = "pointer";
    g.appendChild(hit);

    const line = document.createElementNS(SVG_NS, "line");
    line.setAttribute("class", "wall-edge-line");
    line.setAttribute("x1", String(a.cx));
    line.setAttribute("y1", String(a.cy));
    line.setAttribute("x2", String(b.cx));
    line.setAttribute("y2", String(b.cy));
    line.setAttribute("stroke", EDGE_LINE_STROKE);
    line.setAttribute("stroke-opacity", EDGE_LINE_OPACITY);
    line.setAttribute("stroke-width", "2");
    line.setAttribute("stroke-linecap", "round");
    (line as SVGLineElement).style.pointerEvents = "none";
    g.appendChild(line);

    svg.appendChild(g);
  }
}

// Update the endpoints of every edge that touches `noteId` to the given
// surface-local center. Used during drag/transient to keep lines glued to
// the moving note without a full re-render.
export function updateEdgesForNote(surface: HTMLElement, noteId: string, cx: number, cy: number): void {
  const svg = surface.querySelector<SVGSVGElement>(`#${EDGE_OVERLAY_ID}`);
  if (!svg) return;
  const groups = svg.querySelectorAll<SVGGElement>(".wall-edge-group");
  groups.forEach((g) => {
    const from = g.dataset.from;
    const to = g.dataset.to;
    if (from !== noteId && to !== noteId) return;
    const lines = g.querySelectorAll<SVGLineElement>("line");
    lines.forEach((ln) => {
      if (from === noteId) {
        ln.setAttribute("x1", String(cx));
        ln.setAttribute("y1", String(cy));
      }
      if (to === noteId) {
        ln.setAttribute("x2", String(cx));
        ln.setAttribute("y2", String(cy));
      }
    });
  });
}

// Begin a Shift+drag preview line. Returns a controller with update/end
// methods so wall.ts can drive it. The preview line lives on the overlay
// and is removed by `end()` regardless of outcome.
export function beginEdgePreview(surface: HTMLElement, fromCenter: { cx: number; cy: number }): {
  update: (x: number, y: number) => void;
  end: () => void;
} {
  const svg = ensureEdgeOverlay(surface);
  const line = document.createElementNS(SVG_NS, "line");
  line.setAttribute("class", "wall-edge-preview");
  line.setAttribute("x1", String(fromCenter.cx));
  line.setAttribute("y1", String(fromCenter.cy));
  line.setAttribute("x2", String(fromCenter.cx));
  line.setAttribute("y2", String(fromCenter.cy));
  line.setAttribute("stroke", EDGE_PREVIEW_STROKE);
  line.setAttribute("stroke-opacity", EDGE_PREVIEW_OPACITY);
  line.setAttribute("stroke-width", "2");
  line.setAttribute("stroke-linecap", "round");
  line.setAttribute("stroke-dasharray", "6 4");
  (line as SVGLineElement).style.pointerEvents = "none";
  svg.appendChild(line);
  return {
    update(x, y) {
      line.setAttribute("x2", String(x));
      line.setAttribute("y2", String(y));
    },
    end() {
      if (line.parentNode) line.parentNode.removeChild(line);
    },
  };
}

export function getNoteCenterFromElement(surface: HTMLElement, noteEl: HTMLElement): { cx: number; cy: number } {
  // We use offsetLeft/Top against the surface so the SVG (also positioned
  // inside the surface) lines up. Falls back to bounding rects when offset
  // ancestor differs (shouldn't happen in normal layout).
  if (noteEl.offsetParent === surface || surface.contains(noteEl)) {
    return {
      cx: noteEl.offsetLeft + noteEl.offsetWidth / 2,
      cy: noteEl.offsetTop + noteEl.offsetHeight / 2,
    };
  }
  // Screen-space fallback: convert note center through the active viewport
  // transform (offsetLeft path above is canvas-local when parent is .wall-content).
  const a = noteEl.getBoundingClientRect();
  const c = screenToCanvas(a.left + a.width / 2, a.top + a.height / 2);
  return { cx: c.x, cy: c.y };
}

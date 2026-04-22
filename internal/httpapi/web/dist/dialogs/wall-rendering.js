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
import { HEX_COLOR_RE, sanitizeHexColor } from "../utils.js";
import { colorIndexFromHex } from "./wall-postbaby-constants.js";
const DEFAULT_NOTE_COLOR = "#ffd966";
// Clamp to the same limits the backend enforces. Keep in sync with
// internal/store/wall.go (clampNoteDim / validateWallColor).
export const MIN_NOTE_WIDTH = 120;
export const MIN_NOTE_HEIGHT = 80;
export const MAX_NOTE_WIDTH = 600;
export const MAX_NOTE_HEIGHT = 600;
export function sanitizeNoteColor(color) {
    const safe = sanitizeHexColor(color || "");
    if (safe && HEX_COLOR_RE.test(safe)) {
        return safe;
    }
    return DEFAULT_NOTE_COLOR;
}
export function clampDim(v, min, max) {
    if (!Number.isFinite(v))
        return min;
    return Math.max(min, Math.min(max, v));
}
export function noteStyleAttr(n) {
    const w = clampDim(n.width, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH);
    const h = clampDim(n.height, MIN_NOTE_HEIGHT, MAX_NOTE_HEIGHT);
    const color = sanitizeNoteColor(n.color);
    return `left:${Math.round(n.x)}px;top:${Math.round(n.y)}px;width:${Math.round(w)}px;height:${Math.round(h)}px;background:${color};`;
}
export function buildNoteElement(note, canEdit) {
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
        handle.setAttribute("aria-label", "Resize note");
        handle.setAttribute("role", "slider");
        el.appendChild(handle);
    }
    return el;
}
// Swap the display div for a Postbaby-style edit textarea. Returns the
// textarea so wall.ts can wire commit/cancel handlers and focus it.
export function enterEditMode(noteEl, initialText) {
    const existing = noteEl.querySelector(".wall-note__editor");
    if (existing)
        return existing;
    noteEl.classList.add("wall-note--editing");
    const display = noteEl.querySelector(".wall-note__display");
    if (display)
        display.remove();
    const ta = document.createElement("textarea");
    ta.className = "wall-note__editor";
    ta.value = initialText;
    ta.maxLength = 5000;
    ta.spellcheck = true;
    ta.setAttribute("aria-label", "Edit note text");
    // Insert before the resize handle so the handle stays on top.
    const handle = noteEl.querySelector(".wall-note__resize-handle");
    if (handle) {
        noteEl.insertBefore(ta, handle);
    }
    else {
        noteEl.appendChild(ta);
    }
    return ta;
}
// Remove the textarea, restore the display div with the given text. Safe to
// call when edit mode is not active (no-op in that case).
export function exitEditMode(noteEl, text) {
    const ta = noteEl.querySelector(".wall-note__editor");
    if (ta)
        ta.remove();
    noteEl.classList.remove("wall-note--editing");
    let display = noteEl.querySelector(".wall-note__display");
    if (!display) {
        display = document.createElement("div");
        display.className = "wall-note__display";
        const handle = noteEl.querySelector(".wall-note__resize-handle");
        if (handle) {
            noteEl.insertBefore(display, handle);
        }
        else {
            noteEl.appendChild(display);
        }
    }
    display.textContent = text;
}
export function isEditing(noteEl) {
    return noteEl.classList.contains("wall-note--editing");
}
export function renderEmptyWallHtml(canEdit) {
    const hint = canEdit
        ? "Right-click the canvas to add your first sticky note. Hold Shift and drag from one note to another to draw a connection."
        : "Waiting for a contributor to add one.";
    return `<div class="wall-empty" role="status">No notes yet.<br/><span class="muted">${hint}</span></div>`;
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
export function ensureEdgeOverlay(surface) {
    let svg = surface.querySelector(`#${EDGE_OVERLAY_ID}`);
    if (svg)
        return svg;
    svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("id", EDGE_OVERLAY_ID);
    svg.setAttribute("class", "wall-edge-overlay");
    // Cover the entire surface; SVG itself ignores pointer events, only
    // hit-line children opt back in.
    svg.style.position = "absolute";
    svg.style.left = "0";
    svg.style.top = "0";
    svg.style.width = "100%";
    svg.style.height = "100%";
    svg.style.pointerEvents = "none";
    // Postbaby parity: lines paint *under* notes (.wall-note is z-index: 2 in
    // styles.css). This overlay uses z-index: 0 via .wall-edge-overlay; a
    // dragging note still rises above everything via .wall-note--dragging.
    surface.appendChild(svg);
    return svg;
}
function noteCenter(surface, noteId) {
    const el = surface.querySelector(`.wall-note[data-note-id="${CSS.escape(noteId)}"]`);
    if (!el)
        return null;
    // Use offsetLeft/Top + offsetWidth/Height so coordinates are relative to
    // the surface (matches where SVG sits) rather than the viewport.
    const cx = el.offsetLeft + el.offsetWidth / 2;
    const cy = el.offsetTop + el.offsetHeight / 2;
    return { cx, cy };
}
export function renderEdges(surface, edges) {
    const svg = ensureEdgeOverlay(surface);
    // Drop only edge groups; preserve any in-progress preview line so the
    // user's drag isn't visually interrupted by a re-render mid-flight.
    const groups = svg.querySelectorAll(".wall-edge-group");
    groups.forEach((g) => g.remove());
    for (const edge of edges) {
        if (!edge || !edge.id || !edge.from || !edge.to)
            continue;
        if (edge.from === edge.to)
            continue;
        const a = noteCenter(surface, edge.from);
        const b = noteCenter(surface, edge.to);
        if (!a || !b)
            continue;
        const g = document.createElementNS(SVG_NS, "g");
        g.setAttribute("class", "wall-edge-group");
        g.dataset.edgeId = edge.id;
        g.dataset.from = edge.from;
        g.dataset.to = edge.to;
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
        hit.style.cursor = "pointer";
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
        line.style.pointerEvents = "none";
        g.appendChild(line);
        svg.appendChild(g);
    }
}
// Update the endpoints of every edge that touches `noteId` to the given
// surface-local center. Used during drag/transient to keep lines glued to
// the moving note without a full re-render.
export function updateEdgesForNote(surface, noteId, cx, cy) {
    const svg = surface.querySelector(`#${EDGE_OVERLAY_ID}`);
    if (!svg)
        return;
    const groups = svg.querySelectorAll(".wall-edge-group");
    groups.forEach((g) => {
        const from = g.dataset.from;
        const to = g.dataset.to;
        if (from !== noteId && to !== noteId)
            return;
        const lines = g.querySelectorAll("line");
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
export function beginEdgePreview(surface, fromCenter) {
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
    line.style.pointerEvents = "none";
    svg.appendChild(line);
    return {
        update(x, y) {
            line.setAttribute("x2", String(x));
            line.setAttribute("y2", String(y));
        },
        end() {
            if (line.parentNode)
                line.parentNode.removeChild(line);
        },
    };
}
export function getNoteCenterFromElement(surface, noteEl) {
    // We use offsetLeft/Top against the surface so the SVG (also positioned
    // inside the surface) lines up. Falls back to bounding rects when offset
    // ancestor differs (shouldn't happen in normal layout).
    if (noteEl.offsetParent === surface || surface.contains(noteEl)) {
        return {
            cx: noteEl.offsetLeft + noteEl.offsetWidth / 2,
            cy: noteEl.offsetTop + noteEl.offsetHeight / 2,
        };
    }
    const a = noteEl.getBoundingClientRect();
    const s = surface.getBoundingClientRect();
    return { cx: a.left - s.left + a.width / 2, cy: a.top - s.top + a.height / 2 };
}

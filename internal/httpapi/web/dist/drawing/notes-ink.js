import { hasI18nKey, t } from "../i18n/index.js";
import { findErasedStrokeIds } from "./drawing-eraser.js";
import { freezeImageCapture } from "./drawing-math.js";
import { DRAWING_COLORS, DRAWING_SIZES } from "./drawing-palette.js";
import { bindDrawingPointer } from "./drawing-pointer.js";
import { createPencilIcon, renderStrokesInto, SVG_NS, updatePreviewPath } from "./drawing-svg.js";
import { HIGHLIGHTER_OPACITY, MAX_NOTES_INK_BYTES, MAX_NOTES_INK_STROKES, newStrokeId, PEN_OPACITY, stripScreenKept, } from "./drawing-types.js";
export const NOTES_INK_EXTEND_PX = 48;
export const NOTES_INK_REFLOW_TOLERANCE_PX = 24;
function label(key, fallback) {
    return hasI18nKey(key) ? t(key) : fallback;
}
function strokeExtent(strokes) {
    let maxX = 0;
    let maxY = 0;
    for (const s of strokes) {
        for (const p of s.points) {
            if (p[0] > maxX)
                maxX = p[0];
            if (p[1] > maxY)
                maxY = p[1];
        }
    }
    return { maxX, maxY };
}
function cloneStrokes(strokes) {
    return strokes.map((s) => ({ ...s, points: s.points.map((p) => [p[0], p[1], p[2]]) }));
}
export function buildNotesInkDocument(strokes, referenceWidth) {
    const { maxX, maxY } = strokeExtent(strokes);
    return {
        version: 1,
        width: Math.max(1, Math.ceil(Math.max(referenceWidth, maxX + 1))),
        height: Math.max(1, Math.ceil(maxY + 1)),
        strokes: cloneStrokes(strokes),
    };
}
export function mountNotesInk(host) {
    const { scroller, content } = host;
    let strokes = [];
    let baseline = "[]";
    let referenceWidth = 0;
    let past = [];
    let future = [];
    let eraseSnapshot = null;
    let editable = false;
    let locked = false;
    let drawing = false;
    let rev = 0;
    let tool = "pen";
    let color = DRAWING_COLORS[0];
    let penSize = 3;
    let highlighterSize = 16;
    let surfaceWidth = 1;
    let surfaceHeight = 1;
    let destroyed = false;
    scroller.classList.add("notes-ink-host");
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "notes-ink-layer");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    const strokeGroup = document.createElementNS(SVG_NS, "g");
    strokeGroup.setAttribute("class", "drawing-strokes");
    const previewGroup = document.createElementNS(SVG_NS, "g");
    previewGroup.setAttribute("class", "drawing-preview");
    svg.append(strokeGroup, previewGroup);
    scroller.appendChild(svg);
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "todo-notes-tab notes-ink-toggle";
    toggle.append(createPencilIcon(16));
    toggle.setAttribute("aria-pressed", "false");
    toggle.setAttribute("aria-label", label("drawing.notesToggle", "Draw on notes"));
    toggle.title = label("drawing.notesToggle", "Draw on notes");
    toggle.hidden = true;
    const tools = document.createElement("div");
    tools.className = "notes-ink-tools";
    tools.setAttribute("role", "toolbar");
    tools.setAttribute("aria-label", label("drawing.toolbar", "Drawing tools"));
    tools.hidden = true;
    const notice = document.createElement("div");
    notice.className = "notes-ink-notice";
    notice.setAttribute("role", "status");
    notice.hidden = true;
    const makeButton = (text, cls, aria) => {
        const b = document.createElement("button");
        b.type = "button";
        b.className = `notes-ink-tools__btn ${cls}`;
        b.textContent = text;
        if (aria)
            b.setAttribute("aria-label", aria);
        return b;
    };
    const toolButtons = new Map();
    for (const [key, text] of [["pen", label("drawing.tool.pen", "Pen")], ["highlighter", label("drawing.tool.highlighter", "Highlighter")], ["eraser", label("drawing.tool.eraser", "Eraser")]]) {
        const b = makeButton(text, `notes-ink-tools__tool notes-ink-tools__tool--${key}`);
        b.addEventListener("click", () => {
            tool = key;
            syncTools();
        });
        toolButtons.set(key, b);
        tools.appendChild(b);
    }
    const colorButtons = [];
    for (const c of DRAWING_COLORS) {
        const b = makeButton("", "notes-ink-tools__swatch", `${label("drawing.color", "Color")} ${c}`);
        b.style.setProperty("--swatch", c);
        b.dataset.color = c;
        b.addEventListener("click", () => {
            color = c;
            if (tool === "eraser")
                tool = "pen";
            syncTools();
        });
        colorButtons.push(b);
        tools.appendChild(b);
    }
    const sizeButtons = [];
    for (const size of DRAWING_SIZES) {
        const b = makeButton("", "notes-ink-tools__size", `${label("drawing.size", "Brush size")} ${size}`);
        b.style.setProperty("--dot", `${Math.min(16, size + 2)}px`);
        b.dataset.size = String(size);
        b.addEventListener("click", () => {
            if (tool === "highlighter")
                highlighterSize = size;
            else
                penSize = size;
            syncTools();
        });
        sizeButtons.push(b);
        tools.appendChild(b);
    }
    const undoBtn = makeButton(label("drawing.undo", "Undo"), "notes-ink-tools__undo");
    const redoBtn = makeButton(label("drawing.redo", "Redo"), "notes-ink-tools__redo");
    const clearBtn = makeButton(label("drawing.clear", "Clear"), "notes-ink-tools__clear");
    tools.append(undoBtn, redoBtn, clearBtn);
    host.toggleHost?.appendChild(toggle);
    if (host.toolsHost)
        host.toolsHost.appendChild(tools);
    (host.noticeHost ?? host.toolsHost)?.appendChild(notice);
    const currentStyle = () => ({
        tool: tool === "highlighter" ? "highlighter" : "pen",
        color,
        cssSize: tool === "highlighter" ? highlighterSize : penSize,
        opacity: tool === "highlighter" ? HIGHLIGHTER_OPACITY : PEN_OPACITY,
    });
    const syncTools = () => {
        for (const [key, b] of toolButtons)
            b.setAttribute("aria-pressed", String(key === tool));
        for (const b of colorButtons)
            b.setAttribute("aria-pressed", String(b.dataset.color === color && tool !== "eraser"));
        const activeSize = tool === "highlighter" ? highlighterSize : penSize;
        for (const b of sizeButtons)
            b.setAttribute("aria-pressed", String(Number(b.dataset.size) === activeSize && tool !== "eraser"));
        undoBtn.disabled = past.length === 0;
        redoBtn.disabled = future.length === 0;
        clearBtn.disabled = strokes.length === 0;
        svg.dataset.tool = tool;
    };
    const render = () => renderStrokesInto(strokeGroup, strokes);
    const syncNotice = () => {
        if (strokes.length === 0) {
            notice.hidden = true;
            notice.textContent = "";
            svg.dataset.reflow = "false";
            return;
        }
        const doc = buildNotesInkDocument(strokes, referenceWidth);
        const reflowed = Math.abs(surfaceWidth - doc.width) > NOTES_INK_REFLOW_TOLERANCE_PX;
        svg.dataset.reflow = String(reflowed);
        notice.hidden = !reflowed;
        notice.textContent = reflowed
            ? label("drawing.notesReflow", "The notes area is a different width than when this handwriting was drawn. Handwriting keeps its original position, so it may not line up with re-wrapped text.")
            : "";
    };
    const layout = () => {
        if (destroyed)
            return;
        const width = Math.max(1, scroller.clientWidth);
        const contentBottom = content.offsetTop + content.offsetHeight;
        const { maxY } = strokeExtent(strokes);
        const height = Math.max(1, Math.ceil(Math.max(scroller.clientHeight, contentBottom, maxY + (drawing ? NOTES_INK_EXTEND_PX : 8))));
        if (width !== surfaceWidth || height !== surfaceHeight) {
            surfaceWidth = width;
            surfaceHeight = height;
            svg.setAttribute("width", String(width));
            svg.setAttribute("height", String(height));
            svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
            svg.style.width = `${width}px`;
            svg.style.height = `${height}px`;
        }
        if (scroller.lastElementChild !== svg)
            scroller.appendChild(svg);
        syncNotice();
    };
    const changed = () => {
        rev += 1;
        render();
        syncTools();
        layout();
        host.onChange?.();
    };
    const pushHistory = () => {
        past.push(cloneStrokes(strokes));
        if (past.length > 100)
            past.shift();
        future = [];
    };
    const onOutcome = (outcome) => {
        if (outcome.status !== "finished")
            return;
        const stroke = stripScreenKept(outcome.stroke);
        const candidate = [...strokes, stroke];
        const width = referenceWidth > 0 ? referenceWidth : surfaceWidth;
        if (new TextEncoder().encode(JSON.stringify(buildNotesInkDocument(candidate, width))).length > MAX_NOTES_INK_BYTES) {
            notice.hidden = false;
            notice.textContent = label("drawing.notesFull", "The notes handwriting is full. Erase strokes to add more.");
            return;
        }
        pushHistory();
        if (referenceWidth <= 0 || surfaceWidth > referenceWidth)
            referenceWidth = surfaceWidth;
        strokes = candidate;
        changed();
    };
    const pointer = bindDrawingPointer({
        element: svg,
        getTool: () => tool,
        getStyle: currentStyle,
        freeze: () => {
            if (!drawing)
                return null;
            const r = svg.getBoundingClientRect();
            if (!(r.width > 0) || !(r.height > 0))
                return null;
            return freezeImageCapture({ rectLeft: r.left, rectTop: r.top, rectWidth: r.width, rectHeight: r.height, naturalWidth: r.width, naturalHeight: r.height });
        },
        canStartStroke: () => strokes.length < MAX_NOTES_INK_STROKES,
        newStrokeId,
        onPreview: (points, size, style) => updatePreviewPath(previewGroup, { tool: style.tool, color: style.color, size, opacity: style.opacity, points }, points),
        onOutcome,
        onErase: (x, y, rx, ry) => {
            const hits = findErasedStrokeIds(strokes, x, y, rx, ry);
            if (hits.length === 0)
                return;
            if (!eraseSnapshot)
                eraseSnapshot = cloneStrokes(strokes);
            const remove = new Set(hits);
            strokes = strokes.filter((s) => !remove.has(s.id));
            render();
        },
        onEraseEnd: () => {
            if (!eraseSnapshot)
                return;
            past.push(eraseSnapshot);
            if (past.length > 100)
                past.shift();
            future = [];
            eraseSnapshot = null;
            changed();
        },
        onEraseCancel: () => {
            if (!eraseSnapshot)
                return;
            strokes = eraseSnapshot;
            eraseSnapshot = null;
            render();
            syncTools();
        },
        onRejected: () => {
            notice.hidden = false;
            notice.textContent = label("drawing.notesStrokeLimit", `The notes handwriting has the maximum of ${MAX_NOTES_INK_STROKES} strokes.`);
        },
    });
    const swallow = (ev) => {
        if (!drawing)
            return;
        ev.stopPropagation();
    };
    svg.addEventListener("pointerdown", swallow);
    svg.addEventListener("mousedown", (ev) => {
        if (!drawing)
            return;
        ev.preventDefault();
        ev.stopPropagation();
    });
    svg.addEventListener("click", swallow);
    svg.addEventListener("dblclick", swallow);
    svg.addEventListener("touchstart", swallow, { passive: true });
    const onScroll = () => {
        if (pointer.isActive())
            pointer.finishActive();
    };
    scroller.addEventListener("scroll", onScroll, { passive: true });
    const ro = typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(() => {
            if (pointer.isActive())
                pointer.finishActive();
            layout();
        })
        : null;
    ro?.observe(scroller);
    ro?.observe(content);
    const setDrawing = (on) => {
        const next = on && editable && !locked;
        if (next === drawing)
            return;
        if (!next && pointer.isActive())
            pointer.finishActive();
        drawing = next;
        svg.classList.toggle("notes-ink-layer--drawing", drawing);
        scroller.classList.toggle("notes-ink-host--drawing", drawing);
        toggle.setAttribute("aria-pressed", String(drawing));
        tools.hidden = !drawing;
        syncTools();
        layout();
        host.onDrawingChange?.(drawing);
    };
    toggle.addEventListener("click", (ev) => {
        ev.preventDefault();
        setDrawing(!drawing);
    });
    undoBtn.addEventListener("click", () => {
        const prev = past.pop();
        if (!prev)
            return;
        future.push(cloneStrokes(strokes));
        strokes = prev;
        changed();
    });
    redoBtn.addEventListener("click", () => {
        const next = future.pop();
        if (!next)
            return;
        past.push(cloneStrokes(strokes));
        strokes = next;
        changed();
    });
    clearBtn.addEventListener("click", () => {
        if (strokes.length === 0)
            return;
        pushHistory();
        strokes = [];
        changed();
    });
    syncTools();
    layout();
    return {
        setDocument(doc) {
            if (pointer.isActive())
                pointer.abortActive("user-cancel");
            strokes = doc ? cloneStrokes(doc.strokes) : [];
            referenceWidth = doc ? doc.width : 0;
            baseline = JSON.stringify(strokes);
            past = [];
            future = [];
            eraseSnapshot = null;
            rev = 0;
            render();
            syncTools();
            layout();
        },
        getDocument() {
            return strokes.length > 0 ? buildNotesInkDocument(strokes, referenceWidth > 0 ? referenceWidth : surfaceWidth) : null;
        },
        saveValue() {
            if (JSON.stringify(strokes) === baseline)
                return undefined;
            return strokes.length > 0 ? buildNotesInkDocument(strokes, referenceWidth > 0 ? referenceWidth : surfaceWidth) : null;
        },
        isDirty: () => JSON.stringify(strokes) !== baseline,
        hasInk: () => strokes.length > 0,
        revision: () => rev,
        setEditable(next) {
            editable = next;
            toggle.hidden = !editable;
            if (!editable)
                setDrawing(false);
        },
        setLocked(next) {
            locked = next;
            toggle.disabled = locked;
            if (locked)
                setDrawing(false);
        },
        isDrawing: () => drawing,
        setDrawing,
        layout,
        destroy() {
            if (destroyed)
                return;
            pointer.destroy();
            destroyed = true;
            ro?.disconnect();
            scroller.removeEventListener("scroll", onScroll);
            svg.remove();
            toggle.remove();
            tools.remove();
            notice.remove();
            scroller.classList.remove("notes-ink-host", "notes-ink-host--drawing");
        },
    };
}

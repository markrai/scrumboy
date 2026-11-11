import { findErasedStrokeIds } from "../drawing/drawing-eraser.js";
import { freezeWallCapture } from "../drawing/drawing-math.js";
import { bindDrawingPointer, type DrawingPointerHandle } from "../drawing/drawing-pointer.js";
import type { StrokeStyle } from "../drawing/drawing-sampler.js";
import { createPencilIcon, createStrokeElement, SVG_NS, updatePreviewPath } from "../drawing/drawing-svg.js";
import {
  HIGHLIGHTER_OPACITY,
  MAX_WALL_STROKES,
  newStrokeId,
  PEN_OPACITY,
  stripScreenKept,
  type DrawingStroke,
  type StrokeOutcome,
  type StrokeTool,
} from "../drawing/drawing-types.js";
import { sanitizeStroke } from "../drawing/drawing-validate.js";
import { DRAWING_COLORS, DRAWING_SIZES } from "../drawing/drawing-palette.js";
import { hasI18nKey, t } from "../i18n/index.js";
import { fetchWall, postWallDrawings, type WallDrawingCommitResult } from "./wall-api.js";
import type { WallDocument } from "./wall-rendering.js";
import { getViewportState, getViewportSurfaceRect, onViewportChange } from "./wall-viewport.js";
import { isWallDrawingOrSaving, releaseDrawingGuards, setDrawingCommitInFlight, setPointerDrawingActive } from "./wall-state.js";

export const WALL_DRAWING_OVERLAY_ID = "wallDrawingOverlay";
const MAX_COMMIT_STROKES = 50;

function label(key: string, fallback: string): string {
  return hasI18nKey(key) ? t(key) : fallback;
}

export function sanitizeWallDrawings(raw: unknown): DrawingStroke[] {
  if (!Array.isArray(raw)) return [];
  const out: DrawingStroke[] = [];
  const seen = new Set<string>();
  for (const item of raw) {
    const s = sanitizeStroke(item, { kind: "world" });
    if (!s || seen.has(s.id)) continue;
    seen.add(s.id);
    out.push(s);
  }
  return out;
}

export function ensureWallDrawingOverlay(content: HTMLElement): { svg: SVGSVGElement; layer: SVGGElement; active: SVGGElement } {
  let svg = content.querySelector<SVGSVGElement>(`:scope > #${WALL_DRAWING_OVERLAY_ID}`);
  if (!svg) {
    svg = document.createElementNS(SVG_NS, "svg") as SVGSVGElement;
    svg.setAttribute("id", WALL_DRAWING_OVERLAY_ID);
    svg.setAttribute("class", "wall-drawing-overlay");
    svg.setAttribute("aria-hidden", "true");
    svg.style.position = "absolute";
    svg.style.left = "0";
    svg.style.top = "0";
    svg.style.width = "1px";
    svg.style.height = "1px";
    svg.style.overflow = "visible";
    svg.style.pointerEvents = "none";
    const layer = document.createElementNS(SVG_NS, "g");
    layer.setAttribute("class", "wall-drawing-layer");
    const active = document.createElementNS(SVG_NS, "g");
    active.setAttribute("class", "wall-drawing-active");
    svg.append(layer, active);
  }
  if (svg.parentElement !== content || svg !== content.lastElementChild) content.appendChild(svg);
  return {
    svg,
    layer: svg.querySelector<SVGGElement>("g.wall-drawing-layer") as SVGGElement,
    active: svg.querySelector<SVGGElement>("g.wall-drawing-active") as SVGGElement,
  };
}

export function renderWallDrawingLayer(content: HTMLElement, strokes: readonly DrawingStroke[], hidden: ReadonlySet<string> = new Set()): void {
  const { layer } = ensureWallDrawingOverlay(content);
  const existing = new Map<string, Element>();
  for (const el of Array.from(layer.children)) {
    const id = el.getAttribute("data-stroke-id");
    if (id) existing.set(id, el);
  }
  const keep = new Set<string>();
  for (const stroke of strokes) {
    if (hidden.has(stroke.id)) continue;
    keep.add(stroke.id);
    if (existing.has(stroke.id)) continue;
    const el = createStrokeElement(stroke);
    if (el) layer.appendChild(el);
  }
  for (const [id, el] of existing) {
    if (!keep.has(id)) el.remove();
  }
}

export function wallDrawingsDiffer(prev: readonly DrawingStroke[] | undefined, next: readonly DrawingStroke[] | undefined): boolean {
  const a = prev ?? [];
  const b = next ?? [];
  if (a.length !== b.length) return true;
  const ids = new Set(a.map((s) => s.id));
  return b.some((s) => !ids.has(s.id));
}

export type WallDrawingContext = {
  surface: HTMLElement;
  slug: string;
  userId: number | null;
  canEdit: boolean;
  isCurrent: () => boolean;
  content: () => HTMLElement | null;
  doc: () => WallDocument | null;
  setDoc: (mutate: (doc: WallDocument) => void) => void;
  isSpacePanArmed: () => boolean;
  onModeChange: (drawing: boolean) => void;
  requestRefetch: () => void;
  notify: (message: string) => void;
  retryDelaysMs?: readonly number[];
};

export type WallDrawingController = {
  render(): void;
  isDrawing(): boolean;
  setDrawMode(on: boolean): void;
  markDeferredRefetch(): void;
  hasUnsavedWork(): boolean;
  retry(): void;
  discard(): void;
  destroy(): void;
};

type CommitFailure = { kind: "transport" | "rejected" | "conflict"; status: number | null };

const DEFAULT_RETRY_DELAYS_MS: readonly number[] = [1000, 3000];

export function classifyWallCommitError(err: unknown): CommitFailure {
  const raw = (err as { status?: unknown } | null)?.status;
  const status = typeof raw === "number" && raw > 0 ? raw : null;
  if (status === 409) return { kind: "conflict", status };
  if (status === null || status === 408 || status === 425 || status === 429 || status >= 500) return { kind: "transport", status };
  return { kind: "rejected", status };
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

export function mountWallDrawing(ctx: WallDrawingContext): WallDrawingController {
  const slug = ctx.slug;
  const userId = ctx.userId;
  const canEdit = ctx.canEdit;
  const retryDelays = ctx.retryDelaysMs ?? DEFAULT_RETRY_DELAYS_MS;
  const guardOwner = {};
  let alive = true;
  let drawMode = false;
  let tool: StrokeTool = "pen";
  let color = DRAWING_COLORS[0];
  let penSize = 4;
  let highlighterSize = 16;
  const queuedAdds: DrawingStroke[] = [];
  const pendingErase = new Set<string>();
  const sentAdds = new Set<string>();
  const gestureHits = new Set<string>();
  let inFlight: { adds: Set<string>; erase: Set<string> } | null = null;
  let failure: CommitFailure | null = null;
  let deferredRefetch = false;

  const live = () => alive && ctx.isCurrent();

  const capture = document.createElement("div");
  capture.className = "wall-draw-capture";
  capture.hidden = true;
  ctx.surface.appendChild(capture);

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "wall-draw-toggle-btn";
  toggle.append(createPencilIcon(22));
  toggle.setAttribute("aria-pressed", "false");
  toggle.setAttribute("aria-label", label("wall.draw.toggle", "Drawing mode"));
  toggle.title = label("wall.draw.toggleTitle", "Draw on the wall (D)");
  toggle.hidden = !canEdit;

  const palette = document.createElement("div");
  palette.className = "wall-draw-palette";
  palette.setAttribute("role", "toolbar");
  palette.setAttribute("aria-label", label("drawing.toolbar", "Drawing tools"));
  palette.hidden = true;

  const status = document.createElement("div");
  status.className = "wall-draw-status";
  status.setAttribute("role", "alert");
  status.hidden = true;
  const statusText = document.createElement("span");
  statusText.className = "wall-draw-status__text";
  const retryBtn = document.createElement("button");
  retryBtn.type = "button";
  retryBtn.className = "wall-draw-status__btn wall-draw-status__retry";
  retryBtn.textContent = label("wall.draw.retry", "Retry");
  const discardBtn = document.createElement("button");
  discardBtn.type = "button";
  discardBtn.className = "wall-draw-status__btn wall-draw-status__discard";
  discardBtn.textContent = label("wall.draw.discard", "Discard");
  status.append(statusText, retryBtn, discardBtn);

  const toolButtons = new Map<StrokeTool, HTMLButtonElement>();
  for (const [key, text] of [["pen", label("drawing.tool.pen", "Pen")], ["highlighter", label("drawing.tool.highlighter", "Highlighter")], ["eraser", label("drawing.tool.eraser", "Eraser")]] as const) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = `wall-draw-palette__btn wall-draw-palette__tool--${key}`;
    b.textContent = text;
    b.addEventListener("click", () => {
      tool = key;
      syncPalette();
    });
    toolButtons.set(key, b);
    palette.appendChild(b);
  }
  const colorButtons: HTMLButtonElement[] = [];
  for (const c of DRAWING_COLORS) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "wall-draw-palette__btn wall-draw-palette__swatch";
    b.style.setProperty("--swatch", c);
    b.dataset.color = c;
    b.setAttribute("aria-label", `${label("drawing.color", "Color")} ${c}`);
    b.addEventListener("click", () => {
      color = c;
      if (tool === "eraser") tool = "pen";
      syncPalette();
    });
    colorButtons.push(b);
    palette.appendChild(b);
  }
  const sizeButtons: HTMLButtonElement[] = [];
  for (const size of DRAWING_SIZES) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "wall-draw-palette__btn wall-draw-palette__size";
    b.style.setProperty("--dot", `${Math.min(18, size + 2)}px`);
    b.dataset.size = String(size);
    b.setAttribute("aria-label", `${label("drawing.size", "Brush size")} ${size}`);
    b.addEventListener("click", () => {
      if (tool === "highlighter") highlighterSize = size;
      else penSize = size;
      syncPalette();
    });
    sizeButtons.push(b);
    palette.appendChild(b);
  }
  ctx.surface.parentElement?.append(toggle, palette, status);

  const syncPalette = () => {
    for (const [key, b] of toolButtons) b.setAttribute("aria-pressed", String(key === tool));
    for (const b of colorButtons) b.setAttribute("aria-pressed", String(b.dataset.color === color && tool !== "eraser"));
    const active = tool === "highlighter" ? highlighterSize : penSize;
    for (const b of sizeButtons) b.setAttribute("aria-pressed", String(Number(b.dataset.size) === active && tool !== "eraser"));
    capture.dataset.tool = tool;
  };

  const liveStrokes = (): DrawingStroke[] => ctx.doc()?.drawings ?? [];

  const hiddenIds = (): Set<string> => new Set([...pendingErase, ...gestureHits]);

  const activeGroup = (): SVGGElement | null => {
    const content = ctx.content();
    return content ? ensureWallDrawingOverlay(content).active : null;
  };

  const renderPending = () => {
    if (!live()) return;
    const group = activeGroup();
    if (!group) return;
    for (const el of Array.from(group.querySelectorAll("[data-stroke-id]"))) el.remove();
    const committed = new Set(liveStrokes().map((s) => s.id));
    const hidden = hiddenIds();
    for (const s of queuedAdds) {
      if (hidden.has(s.id) || committed.has(s.id)) continue;
      const el = createStrokeElement(s);
      if (!el) continue;
      el.classList.add("wall-drawing-pending");
      group.appendChild(el);
    }
  };

  const render = () => {
    if (!live()) return;
    const content = ctx.content();
    if (!content) return;
    renderWallDrawingLayer(content, liveStrokes(), hiddenIds());
    renderPending();
  };

  const syncStatus = () => {
    if (!alive) return;
    if (!failure) {
      status.hidden = true;
      return;
    }
    const message =
      failure.kind === "conflict"
        ? label("wall.draw.failedConflict", "The wall changed while your drawing was being saved. Your strokes are kept here, not saved yet.")
        : failure.kind === "rejected"
          ? label("wall.draw.failedRejected", "The server refused these drawing changes. They are kept here, not saved.")
          : label("wall.draw.failedTransport", "Your drawing could not be confirmed as saved. It is kept here; retrying is safe.");
    statusText.textContent = failure.status ? `${message} (HTTP ${failure.status})` : message;
    status.hidden = false;
  };

  const maybeRefetch = () => {
    if (deferredRefetch && live() && !isWallDrawingOrSaving()) {
      deferredRefetch = false;
      ctx.requestRefetch();
    }
  };

  const applyServerDoc = (doc: WallDocument | WallDrawingCommitResult) => {
    if (!live()) return;
    const strokes = sanitizeWallDrawings((doc as WallDocument).drawings);
    const version = typeof doc.version === "number" ? doc.version : ctx.doc()?.version ?? 0;
    ctx.setDoc((d) => {
      d.drawings = strokes;
      d.version = version;
    });
    render();
  };

  const send = async (adds: DrawingStroke[], erase: string[]): Promise<CommitFailure | null> => {
    let conflictRetried = false;
    let transportAttempt = 0;
    for (;;) {
      try {
        const result = await postWallDrawings(slug, { baseVersion: ctx.doc()?.version ?? 0, add: adds, erase });
        if (!live()) return null;
        applyServerDoc(result);
        return null;
      } catch (err) {
        const failed = classifyWallCommitError(err);
        if (!live()) return failed;
        if (failed.kind === "conflict" && !conflictRetried) {
          conflictRetried = true;
          try {
            const fresh = await fetchWall(slug);
            if (!live()) return failed;
            applyServerDoc(fresh);
          } catch (refetchErr) {
            if (!live()) return failed;
            const refetchFailed = classifyWallCommitError(refetchErr);
            return refetchFailed.kind === "conflict" ? failed : refetchFailed;
          }
          continue;
        }
        if (failed.kind === "transport" && transportAttempt < retryDelays.length) {
          await sleep(retryDelays[transportAttempt]);
          transportAttempt += 1;
          if (!live()) return failed;
          continue;
        }
        return failed;
      }
    }
  };

  const acknowledge = (adds: Set<string>, erase: Set<string>) => {
    for (let i = queuedAdds.length - 1; i >= 0; i -= 1) {
      if (adds.has(queuedAdds[i].id)) {
        sentAdds.delete(queuedAdds[i].id);
        queuedAdds.splice(i, 1);
      }
    }
    for (const id of erase) pendingErase.delete(id);
  };

  const flush = async (): Promise<void> => {
    if (!live() || inFlight || failure) return;
    if (queuedAdds.length === 0 && pendingErase.size === 0) {
      maybeRefetch();
      return;
    }
    const adds = queuedAdds.slice(0, MAX_COMMIT_STROKES);
    const erase = Array.from(pendingErase).slice(0, MAX_WALL_STROKES);
    const batch = { adds: new Set(adds.map((s) => s.id)), erase: new Set(erase) };
    inFlight = batch;
    for (const s of adds) sentAdds.add(s.id);
    setDrawingCommitInFlight(true, guardOwner);
    const failed = await send(adds, erase);
    if (!alive || inFlight !== batch) return;
    inFlight = null;
    setDrawingCommitInFlight(false, guardOwner);
    if (!live()) return;
    if (failed) failure = failed;
    else acknowledge(batch.adds, batch.erase);
    render();
    syncStatus();
    if (!failure) {
      if (queuedAdds.length > 0 || pendingErase.size > 0) void flush();
      else maybeRefetch();
    }
  };

  const retry = () => {
    if (!live() || inFlight) return;
    failure = null;
    syncStatus();
    void flush();
  };

  const discard = () => {
    if (!live() || inFlight) return;
    const maybeSaved = sentAdds.size > 0 || pendingErase.size > 0;
    queuedAdds.length = 0;
    pendingErase.clear();
    sentAdds.clear();
    failure = null;
    syncStatus();
    render();
    ctx.notify(
      maybeSaved
        ? label("wall.draw.discardedMaybeSaved", "Unconfirmed drawing changes were discarded. Some may already have been saved; the wall is being reloaded from the server.")
        : label("wall.draw.discarded", "Unsaved drawing changes were discarded."),
    );
    ctx.requestRefetch();
  };

  retryBtn.addEventListener("click", retry);
  discardBtn.addEventListener("click", discard);

  const currentStyle = (): StrokeStyle => ({
    tool: tool === "highlighter" ? "highlighter" : "pen",
    color,
    cssSize: tool === "highlighter" ? highlighterSize : penSize,
    opacity: tool === "highlighter" ? HIGHLIGHTER_OPACITY : PEN_OPACITY,
  });

  const onOutcome = (outcome: StrokeOutcome) => {
    if (outcome.status !== "finished" || !live()) return;
    queuedAdds.push(stripScreenKept(outcome.stroke));
    renderPending();
    if (outcome.limitReached) ctx.notify(label("drawing.status.pointLimit", "Stroke ended at the point limit. Start a new stroke to continue."));
    void flush();
  };

  const commitEraseGesture = () => {
    if (gestureHits.size === 0) return;
    for (const id of gestureHits) {
      const queuedIndex = queuedAdds.findIndex((s) => s.id === id);
      const neverSent = queuedIndex >= 0 && !sentAdds.has(id) && !(inFlight?.adds.has(id) ?? false);
      if (neverSent) {
        queuedAdds.splice(queuedIndex, 1);
        continue;
      }
      if (queuedIndex >= 0 && !(inFlight?.adds.has(id) ?? false)) queuedAdds.splice(queuedIndex, 1);
      pendingErase.add(id);
    }
    gestureHits.clear();
    render();
    void flush();
  };

  const cancelEraseGesture = () => {
    if (gestureHits.size === 0) return;
    gestureHits.clear();
    render();
  };

  const pointer: DrawingPointerHandle = bindDrawingPointer({
    element: capture,
    getTool: () => tool,
    getStyle: currentStyle,
    freeze: () => {
      if (!live()) return null;
      const r = getViewportSurfaceRect();
      const { panX, panY, zoom } = getViewportState();
      if (!(zoom > 0)) return null;
      return freezeWallCapture({ viewportLeft: r.left, viewportTop: r.top, panX, panY, zoom });
    },
    canStartStroke: () => {
      const committed = new Set(liveStrokes().map((s) => s.id));
      const pending = queuedAdds.filter((s) => !committed.has(s.id)).length;
      return liveStrokes().length + pending < MAX_WALL_STROKES;
    },
    newStrokeId,
    userId: userId ?? undefined,
    onPreview: (points, size, style) => {
      if (!live()) return;
      const group = activeGroup();
      if (group) updatePreviewPath(group, { tool: style.tool, color: style.color, size, opacity: style.opacity, points }, points);
    },
    onOutcome,
    onErase: (x, y, rx, ry) => {
      if (!live()) return;
      const hidden = hiddenIds();
      const committed = new Set(liveStrokes().map((s) => s.id));
      const candidates = [...liveStrokes(), ...queuedAdds.filter((s) => !committed.has(s.id))].filter((s) => !hidden.has(s.id));
      const hits = findErasedStrokeIds(candidates, x, y, rx, ry);
      if (hits.length === 0) return;
      for (const id of hits) gestureHits.add(id);
      render();
    },
    onEraseEnd: commitEraseGesture,
    onEraseCancel: cancelEraseGesture,
    onActiveChange: (active) => {
      setPointerDrawingActive(active, guardOwner);
      if (!active) maybeRefetch();
    },
    onRejected: () => ctx.notify(label("wall.draw.limit", `This wall has the maximum of ${MAX_WALL_STROKES} strokes. Erase strokes to draw more.`)),
    shouldIgnore: (ev) => ev.button === 1 || ctx.isSpacePanArmed(),
  });

  const stopIfDrawing = (ev: Event) => {
    const pe = ev as PointerEvent;
    if (pe.button === 1 || ctx.isSpacePanArmed()) return;
    ev.stopPropagation();
  };
  const blockContextMenu = (ev: Event) => {
    ev.preventDefault();
    ev.stopPropagation();
  };
  capture.addEventListener("pointerdown", stopIfDrawing);
  capture.addEventListener("contextmenu", blockContextMenu);
  capture.addEventListener("dblclick", blockContextMenu);

  const hasUnsavedWork = () => queuedAdds.length > 0 || pendingErase.size > 0 || inFlight !== null;
  const onBeforeUnload = (ev: BeforeUnloadEvent) => {
    if (!alive || !hasUnsavedWork()) return;
    ev.preventDefault();
    ev.returnValue = "";
  };
  window.addEventListener("beforeunload", onBeforeUnload);

  const unsubscribeViewport = onViewportChange(() => {
    if (pointer.isActive()) pointer.finishActive();
  });

  const setDrawMode = (on: boolean) => {
    const next = on && canEdit && live();
    if (next === drawMode) return;
    if (!next && pointer.isActive()) pointer.finishActive();
    drawMode = next;
    capture.hidden = !drawMode;
    palette.hidden = !drawMode;
    toggle.setAttribute("aria-pressed", String(drawMode));
    ctx.surface.classList.toggle("wall-surface--draw-mode", drawMode);
    syncPalette();
    ctx.onModeChange(drawMode);
  };

  toggle.addEventListener("click", (ev) => {
    ev.preventDefault();
    setDrawMode(!drawMode);
    toggle.blur();
  });

  syncPalette();

  return {
    render,
    isDrawing: () => drawMode,
    setDrawMode,
    markDeferredRefetch: () => {
      deferredRefetch = true;
    },
    hasUnsavedWork,
    retry,
    discard,
    destroy() {
      if (!alive) return;
      pointer.destroy();
      alive = false;
      inFlight = null;
      unsubscribeViewport();
      window.removeEventListener("beforeunload", onBeforeUnload);
      releaseDrawingGuards(guardOwner);
      capture.remove();
      toggle.remove();
      palette.remove();
      status.remove();
      ctx.surface.classList.remove("wall-surface--draw-mode");
    },
  };
}

// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  postWallDrawings: vi.fn(),
  fetchWall: vi.fn(),
}));

vi.mock("./wall-api.js", () => ({
  postWallDrawings: api.postWallDrawings,
  fetchWall: api.fetchWall,
}));

import {
  ensureWallDrawingOverlay,
  mountWallDrawing,
  renderWallDrawingLayer,
  wallDrawingsDiffer,
} from "./wall-drawing.js";
import { isWallDrawingOrSaving, resetDrawingGuards, setDrawingCommitInFlight, setPointerDrawingActive } from "./wall-state.js";
import { __getRefreshDebounceHandle, startRealtime } from "./wall-realtime.js";
import { emit } from "../events.js";
import { enterWallDrawMode, getWallCanvasMode, loadWallCanvasMode, resetWallCanvasMode, setWallCanvasMode } from "./wall-canvas-mode.js";
import type { DrawingStroke } from "../drawing/drawing-types.js";
import type { WallDocument } from "./wall-rendering.js";

const stroke = (id: string, x = 0): DrawingStroke => ({ id, tool: "pen", color: "#112233", size: 4, opacity: 1, createdAt: 1, points: [[x, -5, 0.5], [x + 10, 5, 0.5]] });

function pe(type: string, x: number, y: number, pointerId = 1): PointerEvent {
  return new (globalThis as any).PointerEvent(type, { pointerId, pointerType: "mouse", clientX: x, clientY: y, button: 0, bubbles: true, cancelable: true });
}

describe("wall drawing overlay", () => {
  it("uses the 1x1-at-origin geometry and paints raw world coordinates", () => {
    const content = document.createElement("div");
    content.appendChild(document.createElement("div"));
    const { svg, layer, active } = ensureWallDrawingOverlay(content);
    expect(svg.style.width).toBe("1px");
    expect(svg.style.height).toBe("1px");
    expect(svg.style.overflow).toBe("visible");
    expect(svg.style.pointerEvents).toBe("none");
    expect(svg.getAttribute("viewBox")).toBeNull();
    expect(content.lastElementChild).toBe(svg);
    renderWallDrawingLayer(content, [stroke("a", -90000)]);
    expect(layer.querySelector("path")?.getAttribute("d")).toContain("M -90000 -5");
    expect(active.childElementCount).toBe(0);
  });

  it("updates incrementally by stroke id without touching the in-flight layer", () => {
    const content = document.createElement("div");
    const { layer, active } = ensureWallDrawingOverlay(content);
    const inflight = document.createElementNS("http://www.w3.org/2000/svg", "path");
    active.appendChild(inflight);
    renderWallDrawingLayer(content, [stroke("a"), stroke("b")]);
    const a = layer.querySelector('[data-stroke-id="a"]');
    renderWallDrawingLayer(content, [stroke("a"), stroke("c")]);
    expect(layer.querySelector('[data-stroke-id="a"]')).toBe(a);
    expect(layer.querySelector('[data-stroke-id="b"]')).toBeNull();
    expect(layer.querySelector('[data-stroke-id="c"]')).not.toBeNull();
    renderWallDrawingLayer(content, [stroke("a"), stroke("c")], new Set(["c"]));
    expect(layer.querySelector('[data-stroke-id="c"]')).toBeNull();
    expect(active.contains(inflight)).toBe(true);
  });

  it("classifies drawings-only peer changes as changes", () => {
    expect(wallDrawingsDiffer([stroke("a")], [stroke("a")])).toBe(false);
    expect(wallDrawingsDiffer([stroke("a")], [stroke("b")])).toBe(true);
    expect(wallDrawingsDiffer([], [stroke("a")])).toBe(true);
    expect(wallDrawingsDiffer(undefined, [])).toBe(false);
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const httpError = (status: number) => Object.assign(new Error(`HTTP ${status}`), { status });

type Board = {
  slug: string;
  doc: WallDocument;
  current: boolean;
  refetches: number;
  notices: string[];
  host: HTMLElement;
  surface: HTMLElement;
  content: HTMLElement;
  ctl: ReturnType<typeof mountWallDrawing>;
};

function mountBoard(slug: string, opts: { canEdit?: boolean; retryDelaysMs?: number[]; drawings?: DrawingStroke[] } = {}): Board {
  const host = document.createElement("div");
  const surface = document.createElement("div");
  const content = document.createElement("div");
  surface.appendChild(content);
  host.appendChild(surface);
  document.body.appendChild(host);
  const board = {
    slug,
    doc: { notes: [], edges: [], stories: [], drawings: opts.drawings ?? [], version: 3 } as WallDocument,
    current: true,
    refetches: 0,
    notices: [] as string[],
    host,
    surface,
    content,
  } as Board;
  board.ctl = mountWallDrawing({
    surface,
    slug,
    userId: 7,
    canEdit: opts.canEdit ?? true,
    isCurrent: () => board.current,
    content: () => (board.current ? content : null),
    doc: () => (board.current ? board.doc : null),
    setDoc: (mutate) => {
      if (board.current) mutate(board.doc);
    },
    isSpacePanArmed: () => false,
    onModeChange: () => undefined,
    requestRefetch: () => {
      board.refetches += 1;
    },
    notify: (m) => board.notices.push(m),
    retryDelaysMs: opts.retryDelaysMs ?? [],
  });
  board.ctl.render();
  return board;
}

function closeBoard(board: Board): void {
  board.current = false;
  board.ctl.destroy();
  board.host.remove();
}

const capture = (board: Board) => board.host.querySelector<HTMLElement>(".wall-draw-capture")!;
const status = (board: Board) => board.host.querySelector<HTMLElement>(".wall-draw-status")!;
const pendingPaths = (board: Board) => board.content.querySelectorAll(".wall-drawing-active path[data-stroke-id]");
const layerPaths = (board: Board) => board.content.querySelectorAll(".wall-drawing-layer path[data-stroke-id]");

function drawOn(board: Board) {
  board.ctl.setDrawMode(true);
  const el = capture(board);
  el.dispatchEvent(pe("pointerdown", 10, 10));
  el.dispatchEvent(pe("pointermove", 40, 10));
  el.dispatchEvent(pe("pointerup", 60, 10));
}

function eraseAt(board: Board, x: number, y: number, end: "pointerup" | "pointercancel" = "pointerup") {
  board.ctl.setDrawMode(true);
  board.host.querySelector<HTMLButtonElement>(".wall-draw-palette__tool--eraser")!.click();
  const el = capture(board);
  el.dispatchEvent(pe("pointerdown", x, y));
  el.dispatchEvent(pe(end, x, y));
}

const settle = async () => {
  for (let i = 0; i < 30; i += 1) await Promise.resolve();
};

const committed = (body: { add: DrawingStroke[] }, ...extra: DrawingStroke[]) => ({ notes: [], drawings: [...extra, ...body.add], version: 4, applied: true });

describe("wall drawing controller", () => {
  const boards: Board[] = [];
  const open = (slug: string, opts?: Parameters<typeof mountBoard>[1]) => {
    const b = mountBoard(slug, opts);
    boards.push(b);
    return b;
  };

  beforeEach(() => {
    document.body.innerHTML = "";
    api.postWallDrawings.mockReset();
    api.fetchWall.mockReset();
    resetDrawingGuards();
  });

  afterEach(() => {
    for (const b of boards.splice(0)) if (b.current) closeBoard(b);
    resetDrawingGuards();
  });

  it("captures world coordinates, isolates the gesture, and commits against the base version", async () => {
    api.postWallDrawings.mockImplementation(async (_slug: string, body: any) => committed(body));
    const board = open("a");
    board.ctl.setDrawMode(true);
    const bubbled = vi.fn();
    board.surface.addEventListener("pointerdown", bubbled);
    drawOn(board);
    expect(bubbled).not.toHaveBeenCalled();
    await settle();
    const body = api.postWallDrawings.mock.calls[0][1];
    expect(api.postWallDrawings.mock.calls[0][0]).toBe("a");
    expect(body.baseVersion).toBe(3);
    expect(body.add[0].by).toBe(7);
    expect(body.add[0].points[0]).toEqual([10, 10, 0.5]);
    expect(board.doc.version).toBe(4);
    expect(layerPaths(board)).toHaveLength(1);
    expect(pendingPaths(board)).toHaveLength(0);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
    expect(isWallDrawingOrSaving()).toBe(false);
  });

  it("uses a Lucide pencil icon instead of a raw glyph on the toggle", () => {
    const board = open("a");
    const toggle = board.host.querySelector<HTMLButtonElement>(".wall-draw-toggle-btn")!;
    expect(toggle.querySelector("svg.lucide-pencil")).not.toBeNull();
    expect(toggle.textContent).toBe("");
  });

  it("refetches, rebases and retries once on a stale version", async () => {
    let calls = 0;
    api.postWallDrawings.mockImplementation(async (_slug: string, body: any) => {
      calls += 1;
      if (calls === 1) throw httpError(409);
      return { notes: [], drawings: [stroke("peer"), ...body.add], version: body.baseVersion + 1, applied: true };
    });
    api.fetchWall.mockResolvedValue({ notes: [], drawings: [stroke("peer")], version: 9 });
    const board = open("a");
    drawOn(board);
    await settle();
    expect(api.fetchWall).toHaveBeenCalledWith("a");
    expect(api.postWallDrawings.mock.calls[1][1].baseVersion).toBe(9);
    expect(board.doc.version).toBe(10);
    expect(status(board).hidden).toBe(true);
  });

  it("keeps strokes after HTTP 500, stops after bounded retries, and saves them on Retry with the same ids", async () => {
    api.postWallDrawings.mockRejectedValue(httpError(500));
    const board = open("a", { retryDelaysMs: [0, 0] });
    drawOn(board);
    await vi.waitFor(() => expect(status(board).hidden).toBe(false));
    expect(api.postWallDrawings).toHaveBeenCalledTimes(3);
    const ids = api.postWallDrawings.mock.calls.map((c) => c[1].add[0].id);
    expect(new Set(ids).size).toBe(1);
    expect(pendingPaths(board)).toHaveLength(1);
    expect(board.ctl.hasUnsavedWork()).toBe(true);
    expect(status(board).hidden).toBe(false);
    expect(status(board).textContent).toContain("HTTP 500");
    expect(isWallDrawingOrSaving()).toBe(false);
    await new Promise((r) => setTimeout(r, 10));
    expect(api.postWallDrawings).toHaveBeenCalledTimes(3);
    api.postWallDrawings.mockImplementation(async (_slug: string, body: any) => committed(body));
    status(board).querySelector<HTMLButtonElement>(".wall-draw-status__retry")!.click();
    await settle();
    expect(api.postWallDrawings.mock.calls[3][1].add[0].id).toBe(ids[0]);
    expect(board.doc.drawings?.map((s) => s.id)).toEqual([ids[0]]);
    expect(pendingPaths(board)).toHaveLength(0);
    expect(status(board).hidden).toBe(true);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
  });

  it("keeps strokes after a network rejection", async () => {
    api.postWallDrawings.mockRejectedValue(new TypeError("Failed to fetch"));
    const board = open("a");
    drawOn(board);
    await settle();
    expect(pendingPaths(board)).toHaveLength(1);
    expect(board.ctl.hasUnsavedWork()).toBe(true);
    expect(status(board).hidden).toBe(false);
  });

  it("does not auto-retry a definitive rejection and lets the user discard honestly", async () => {
    api.postWallDrawings.mockRejectedValue(httpError(400));
    const board = open("a", { retryDelaysMs: [0, 0] });
    drawOn(board);
    await settle();
    expect(api.postWallDrawings).toHaveBeenCalledTimes(1);
    expect(status(board).hidden).toBe(false);
    status(board).querySelector<HTMLButtonElement>(".wall-draw-status__discard")!.click();
    expect(pendingPaths(board)).toHaveLength(0);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
    expect(board.refetches).toBe(1);
    expect(board.notices.at(-1)).toContain("may already have been saved");
  });

  it("retries a failed erase without losing its intent", async () => {
    const board = open("a", { drawings: [stroke("a", 0)] });
    api.postWallDrawings.mockRejectedValueOnce(httpError(503));
    eraseAt(board, 5, 0);
    await settle();
    expect(api.postWallDrawings.mock.calls[0][1].erase).toEqual(["a"]);
    expect(layerPaths(board)).toHaveLength(0);
    expect(board.ctl.hasUnsavedWork()).toBe(true);
    expect(status(board).hidden).toBe(false);
    api.postWallDrawings.mockResolvedValueOnce({ notes: [], drawings: [], version: 4, applied: true });
    board.ctl.retry();
    await settle();
    expect(api.postWallDrawings.mock.calls[1][1].erase).toEqual(["a"]);
    expect(board.doc.drawings).toEqual([]);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
  });

  it("converges through an idempotent retry when a committed request loses its response", async () => {
    let sent: DrawingStroke | null = null;
    api.postWallDrawings
      .mockImplementationOnce(async (_slug: string, body: any) => {
        sent = body.add[0];
        throw new TypeError("connection reset");
      })
      .mockImplementationOnce(async (_slug: string, body: any) => {
        expect(body.add[0].id).toBe(sent!.id);
        return { notes: [], drawings: [sent], version: 4, applied: false };
      });
    const board = open("a", { retryDelaysMs: [0] });
    drawOn(board);
    await vi.waitFor(() => expect(board.ctl.hasUnsavedWork()).toBe(false));
    expect(api.postWallDrawings).toHaveBeenCalledTimes(2);
    expect(board.doc.drawings).toHaveLength(1);
    expect(layerPaths(board)).toHaveLength(1);
    expect(pendingPaths(board)).toHaveLength(0);
    expect(status(board).hidden).toBe(true);
  });

  it("keeps user work after a second 409 and lets Retry save it", async () => {
    api.postWallDrawings.mockRejectedValue(httpError(409));
    api.fetchWall.mockResolvedValue({ notes: [], drawings: [], version: 9 });
    const board = open("a");
    drawOn(board);
    await settle();
    expect(api.postWallDrawings).toHaveBeenCalledTimes(2);
    expect(api.fetchWall).toHaveBeenCalledTimes(1);
    expect(pendingPaths(board)).toHaveLength(1);
    expect(board.ctl.hasUnsavedWork()).toBe(true);
    expect(status(board).hidden).toBe(false);
    expect(board.notices).toEqual([]);
    api.postWallDrawings.mockImplementation(async (_slug: string, body: any) => committed(body));
    board.ctl.retry();
    await settle();
    expect(board.doc.drawings).toHaveLength(1);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
  });

  it("reports in-flight work as unsaved until the server acknowledges it", async () => {
    const pending = deferred<unknown>();
    api.postWallDrawings.mockReturnValueOnce(pending.promise);
    const board = open("a");
    drawOn(board);
    await settle();
    expect(board.ctl.hasUnsavedWork()).toBe(true);
    expect(board.doc.drawings).toEqual([]);
    pending.resolve(committed(api.postWallDrawings.mock.calls[0][1]));
    await settle();
    expect(board.ctl.hasUnsavedWork()).toBe(false);
  });

  it("never lets Board A's late response mutate Board B after A closes", async () => {
    const lateA = deferred<unknown>();
    api.postWallDrawings.mockReturnValueOnce(lateA.promise);
    const a = open("a");
    drawOn(a);
    await settle();
    closeBoard(a);
    const b = open("b");
    lateA.resolve({ notes: [], drawings: [stroke("from-a")], version: 99, applied: true });
    await settle();
    expect(b.doc.version).toBe(3);
    expect(b.doc.drawings).toEqual([]);
    expect(layerPaths(b)).toHaveLength(0);
    expect(pendingPaths(b)).toHaveLength(0);
    expect(b.ctl.hasUnsavedWork()).toBe(false);
    expect(b.refetches).toBe(0);
  });

  it("never sends a late 409 recovery for Board A against Board B", async () => {
    const lateA = deferred<unknown>();
    api.postWallDrawings.mockReturnValueOnce(lateA.promise);
    api.fetchWall.mockResolvedValue({ notes: [], drawings: [], version: 50 });
    const a = open("a");
    drawOn(a);
    await settle();
    closeBoard(a);
    const b = open("b");
    lateA.reject(httpError(409));
    await settle();
    expect(api.fetchWall).not.toHaveBeenCalled();
    expect(api.postWallDrawings).toHaveBeenCalledTimes(1);
    expect(api.postWallDrawings.mock.calls.every((c) => c[0] === "a")).toBe(true);
    expect(b.doc.version).toBe(3);
  });

  it("keeps a newer controller's save guard when a stale commit completes", async () => {
    const lateA = deferred<unknown>();
    const pendingB = deferred<unknown>();
    api.postWallDrawings.mockReturnValueOnce(lateA.promise).mockReturnValueOnce(pendingB.promise);
    const a = open("a");
    drawOn(a);
    await settle();
    closeBoard(a);
    const b = open("b");
    drawOn(b);
    await settle();
    expect(isWallDrawingOrSaving()).toBe(true);
    lateA.resolve({ notes: [], drawings: [], version: 4, applied: true });
    await settle();
    expect(isWallDrawingOrSaving()).toBe(true);
    lateA.promise.then(() => undefined);
    pendingB.resolve(committed(api.postWallDrawings.mock.calls[1][1]));
    await settle();
    expect(isWallDrawingOrSaving()).toBe(false);
    expect(b.doc.drawings).toHaveLength(1);
  });

  it("does not commit an eraser gesture interrupted by native pointer cancellation", async () => {
    const board = open("a", { drawings: [stroke("a", 0)] });
    eraseAt(board, 5, 0, "pointercancel");
    await settle();
    expect(api.postWallDrawings).not.toHaveBeenCalled();
    expect(layerPaths(board)).toHaveLength(1);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
  });

  it("does not commit an eraser gesture interrupted by teardown", async () => {
    const board = open("a", { drawings: [stroke("a", 0)] });
    board.ctl.setDrawMode(true);
    board.host.querySelector<HTMLButtonElement>(".wall-draw-palette__tool--eraser")!.click();
    capture(board).dispatchEvent(pe("pointerdown", 5, 0));
    closeBoard(board);
    await settle();
    expect(api.postWallDrawings).not.toHaveBeenCalled();
    expect(isWallDrawingOrSaving()).toBe(false);
  });

  it("commits a normal completed eraser gesture as a tombstone", async () => {
    api.postWallDrawings.mockResolvedValue({ notes: [], drawings: [], version: 4, applied: true });
    const board = open("a", { drawings: [stroke("a", 0)] });
    eraseAt(board, 5, 0);
    await settle();
    expect(api.postWallDrawings.mock.calls[0][1].erase).toEqual(["a"]);
    expect(board.doc.drawings).toEqual([]);
  });

  it("drops a never-sent stroke locally but tombstones one that may have reached the server", async () => {
    api.postWallDrawings.mockRejectedValue(httpError(500));
    const board = open("a");
    drawOn(board);
    await settle();
    const sentId = api.postWallDrawings.mock.calls[0][1].add[0].id;
    api.postWallDrawings.mockReset();
    api.postWallDrawings.mockResolvedValue({ notes: [], drawings: [], version: 4, applied: true });
    board.host.querySelector<HTMLButtonElement>(".wall-draw-palette__tool--eraser")!.click();
    const el = capture(board);
    el.dispatchEvent(pe("pointerdown", 30, 10));
    el.dispatchEvent(pe("pointerup", 30, 10));
    board.ctl.retry();
    await settle();
    const body = api.postWallDrawings.mock.calls[0][1];
    expect(body.add).toEqual([]);
    expect(body.erase).toEqual([sentId]);
    expect(board.ctl.hasUnsavedWork()).toBe(false);
  });

  it("offers no drawing mode to read-only viewers", () => {
    const board = open("a", { canEdit: false });
    board.ctl.setDrawMode(true);
    expect(board.ctl.isDrawing()).toBe(false);
    expect(board.host.querySelector<HTMLButtonElement>(".wall-draw-toggle-btn")!.hidden).toBe(true);
  });
});

describe("wall realtime guard", () => {
  afterEach(() => resetDrawingGuards());

  it("defers refresh while a stroke is captured or committing", () => {
    vi.useFakeTimers();
    const onRefreshNeeded = vi.fn();
    const stop = startRealtime({ onRefreshNeeded, onTransient: () => undefined });
    try {
      setPointerDrawingActive(true);
      emit("wall:refresh_needed" as any, {} as any);
      vi.advanceTimersByTime(1000);
      expect(onRefreshNeeded).not.toHaveBeenCalled();
      setPointerDrawingActive(false);
      setDrawingCommitInFlight(true);
      vi.advanceTimersByTime(1000);
      expect(onRefreshNeeded).not.toHaveBeenCalled();
      expect(__getRefreshDebounceHandle()?.hasPending()).toBe(true);
      setDrawingCommitInFlight(false);
      vi.advanceTimersByTime(200);
      expect(onRefreshNeeded).toHaveBeenCalledTimes(1);
    } finally {
      stop();
      vi.useRealTimers();
    }
  });
});

describe("wall draw mode persistence", () => {
  it("never persists draw mode as the remembered canvas mode", () => {
    resetWallCanvasMode();
    setWallCanvasMode("pan");
    enterWallDrawMode();
    expect(getWallCanvasMode()).toBe("draw");
    expect(loadWallCanvasMode()).toBe("pan");
    resetWallCanvasMode();
  });
});

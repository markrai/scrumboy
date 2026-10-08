// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  FIT_ZOOM_MIN,
  ZOOM_MAX,
  ZOOM_MIN,
  canvasDelta,
  clampPanForZoom,
  ensureWallContent,
  fitToNotes,
  getViewportState,
  initWallViewport,
  loadViewport,
  normalizePersistedViewport,
  saveViewportNow,
  screenToCanvas,
  storageKey,
  teardownWallViewport,
  zoomAround,
} from "./wall-viewport.js";
import { rect } from "./wall-test-harness.js";

describe("wall-viewport", () => {
  let surface: HTMLElement;

  beforeEach(() => {
    document.body.innerHTML = "";
    surface = document.createElement("div");
    surface.id = "wallSurface";
    surface.style.width = "800px";
    surface.style.height = "600px";
    document.body.appendChild(surface);
    surface.getBoundingClientRect = () => rect(0, 0, 800, 600);
    const content = ensureWallContent(surface);
    initWallViewport(surface, content, "proj-a");
  });

  afterEach(() => {
    teardownWallViewport();
    localStorage.clear();
  });

  it("screenToCanvas is identity at pan=0 zoom=1", () => {
    const p = screenToCanvas(200, 150);
    expect(p.x).toBe(200);
    expect(p.y).toBe(150);
  });

  it("screenToCanvas accounts for pan and zoom", () => {
    initWallViewport(surface, ensureWallContent(surface), "proj-b", { panX: 100, panY: 50, zoom: 2 });
    const p = screenToCanvas(300, 250);
    expect(p.x).toBe(100);
    expect(p.y).toBe(100);
  });

  it("canvasDelta divides by zoom", () => {
    initWallViewport(surface, ensureWallContent(surface), "proj-c", { panX: 0, panY: 0, zoom: 2 });
    expect(canvasDelta(20)).toBe(10);
  });

  it("zoomAround keeps the canvas point under the cursor", () => {
    const before = screenToCanvas(400, 300);
    zoomAround(400, 300, 2);
    const after = screenToCanvas(400, 300);
    expect(after.x).toBeCloseTo(before.x, 5);
    expect(after.y).toBeCloseTo(before.y, 5);
  });

  it("clamps zoom to configured bounds", () => {
    initWallViewport(surface, ensureWallContent(surface), "z", { panX: 0, panY: 0, zoom: ZOOM_MAX });
    zoomAround(100, 100, 2);
    const p = screenToCanvas(0, 0);
    expect(p.x).toBeDefined();
    initWallViewport(surface, ensureWallContent(surface), "z2", { panX: 0, panY: 0, zoom: ZOOM_MIN });
    zoomAround(100, 100, 0.01);
    expect(screenToCanvas(0, 0).x).toBeDefined();
  });

  it("loadViewport rejects malformed JSON and non-finite fields", () => {
    localStorage.setItem(storageKey("bad"), "not-json");
    expect(loadViewport("bad")).toEqual({ panX: 0, panY: 0, zoom: 1 });

    localStorage.setItem(storageKey("nan"), JSON.stringify({ panX: NaN, panY: 0, zoom: 1 }));
    expect(loadViewport("nan")).toEqual({ panX: 0, panY: 0, zoom: 1 });

    localStorage.setItem(storageKey("ok"), JSON.stringify({ panX: 10, panY: -20, zoom: 1.5 }));
    expect(loadViewport("ok")).toEqual({ panX: 10, panY: -20, zoom: 1.5 });
  });

  it("saveViewport swallows localStorage throws", () => {
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => saveViewportNow("x", { panX: 1, panY: 2, zoom: 1 })).not.toThrow();
    spy.mockRestore();
  });

  it("normalizePersistedViewport returns null for invalid records", () => {
    expect(normalizePersistedViewport(null)).toBeNull();
    expect(normalizePersistedViewport({ panX: 1 })).toBeNull();
  });

  it("fitToNotes recenters on note bounding box", () => {
    fitToNotes([
      { id: "n1", x: 100, y: 100, width: 180, height: 140, color: "#fff", text: "", version: 1 },
      { id: "n2", x: 400, y: 300, width: 180, height: 140, color: "#fff", text: "", version: 1 },
    ]);
    const center = screenToCanvas(400, 300);
    expect(center.x).toBeGreaterThan(200);
    expect(center.y).toBeGreaterThan(150);
  });

  it("fitToNotes empty wall resets to identity", () => {
    initWallViewport(surface, ensureWallContent(surface), "empty", { panX: 500, panY: 500, zoom: 0.5 });
    fitToNotes([]);
    expect(screenToCanvas(100, 100)).toEqual({ x: 100, y: 100 });
  });

  it("fitToNotes zooms out below manual ZOOM_MIN for a wide spread (issue 2)", () => {
    // ~20000px spread on an 800x600 surface needs zoom well under ZOOM_MIN.
    fitToNotes([
      { id: "a", x: -10000, y: -10000, width: 180, height: 140, color: "#fff", text: "", version: 1 },
      { id: "b", x: 10000, y: 10000, width: 180, height: 140, color: "#fff", text: "", version: 1 },
    ]);
    const { zoom } = getViewportState();
    expect(zoom).toBeLessThan(ZOOM_MIN);
    expect(zoom).toBeGreaterThanOrEqual(FIT_ZOOM_MIN);
  });

  it("fitToNotes measures a rendered story taller than the estimate at non-1 zoom", () => {
    const content = ensureWallContent(surface);
    const storyElement = document.createElement("div");
    storyElement.className = "wall-story";
    storyElement.dataset.storyLocalId = "7";
    Object.defineProperties(storyElement, {
      offsetWidth: { configurable: true, value: 0 },
      offsetHeight: { configurable: true, value: 0 },
    });
    // At the current 0.5 zoom this screen box is 280x800 canvas units.
    storyElement.getBoundingClientRect = () => rect(0, 0, 140, 400);
    content.appendChild(storyElement);
    initWallViewport(surface, content, "tall-story", { panX: 0, panY: 0, zoom: 0.5 });

    fitToNotes([], [{
      localId: 7,
      x: 100,
      y: 100,
      version: 1,
      todo: {},
    } as any]);

    expect(getViewportState().zoom).toBeCloseTo((600 - 48 * 2) / 800, 5);
  });

  it("clampPanForZoom uses the passed zoom, not module state (issue 3)", () => {
    // Bound scales with zoom: a small zoom clamps pan tighter than zoom=3.
    const atLowZoom = clampPanForZoom(1_000_000, 0.02);
    const atHighZoom = clampPanForZoom(1_000_000, 3);
    expect(atLowZoom).toBeLessThan(atHighZoom);
  });

  it("loadViewport clamps stored pan against its own zoom (issue 3)", () => {
    // Large pan saved with a tiny zoom must clamp to that zoom's bound on load.
    localStorage.setItem(
      storageKey("zedge"),
      JSON.stringify({ panX: 99_999_999, panY: 0, zoom: 0.2 }),
    );
    const v = loadViewport("zedge");
    expect(v.zoom).toBe(0.2);
    expect(v.panX).toBe(clampPanForZoom(99_999_999, 0.2));
  });
});

import { describe, expect, it } from "vitest";
import { freezeImageCapture, freezeWallCapture, type FrozenCapture } from "./drawing-math.js";
import { StrokeCapture, normalizePressure, shouldKeepSample, type RawSample, type StrokeStyle } from "./drawing-sampler.js";
import { perpendicularDistance, simplifyRDPIndices, simplifyWithAnchors, type ScreenPos } from "./drawing-simplify.js";
import { hasPressureVariation, pointsToSvgPath, pressureWidthFactor, strokeOutlinePath } from "./drawing-spline.js";
import { eraserHitsStroke, findErasedStrokeIds } from "./drawing-eraser.js";
import { sanitizeDrawingDocument, sanitizeStroke, drawingByteSize } from "./drawing-validate.js";
import {
  MAX_STROKE_POINTS,
  MAX_TODO_DRAWING_BYTES,
  stripScreenKept,
  type CompletedStroke,
  type DrawingStroke,
  type StrokeOutcome,
} from "./drawing-types.js";

const PEN: StrokeStyle = { tool: "pen", color: "#e11d48", cssSize: 4, opacity: 1 };

function image(rectWidth: number, rectHeight: number, naturalWidth = 4000, naturalHeight = 3000): FrozenCapture {
  return freezeImageCapture({ rectLeft: 10, rectTop: 20, rectWidth, rectHeight, naturalWidth, naturalHeight });
}

function wall(zoom: number): FrozenCapture {
  return freezeWallCapture({ viewportLeft: 5, viewportTop: 7, panX: 100, panY: -50, zoom });
}

function capture(frozen: FrozenCapture, opts: { pressureCapable?: boolean; maxPoints?: number } = {}): StrokeCapture {
  return new StrokeCapture(frozen, PEN, { id: "s_test", createdAt: 1, pressureCapable: !!opts.pressureCapable, maxPoints: opts.maxPoints });
}

function finished(outcome: StrokeOutcome): CompletedStroke {
  if (outcome.status !== "finished") throw new Error(`expected finished, got ${outcome.status}`);
  return outcome.stroke;
}

function path(frozen: FrozenCapture, offsets: Array<[number, number, number?]>): RawSample[] {
  return offsets.map(([dx, dy, p]) => ({ clientX: frozen.originX + dx, clientY: frozen.originY + dy, pressure: p }));
}

describe("screen-space sampling gate", () => {
  it("keeps samples at >= 2 CSS px and drops closer ones", () => {
    const last = { sx: 0, sy: 0, pressure: 0.5, pressureAnchor: false };
    expect(shouldKeepSample(last, { sx: 1.9, sy: 0, pressure: 0.5, pressureAnchor: false })).toBe(false);
    expect(shouldKeepSample(last, { sx: 2, sy: 0, pressure: 0.5, pressureAnchor: false })).toBe(true);
    expect(shouldKeepSample(last, { sx: 1.2, sy: 1.6, pressure: 0.5, pressureAnchor: false })).toBe(true);
  });

  it("keeps stationary samples whose pressure changes by >= 0.15", () => {
    const last = { sx: 0, sy: 0, pressure: 0.2, pressureAnchor: false };
    expect(shouldKeepSample(last, { sx: 0, sy: 0, pressure: 0.34, pressureAnchor: false })).toBe(false);
    expect(shouldKeepSample(last, { sx: 0, sy: 0, pressure: 0.35, pressureAnchor: false })).toBe(true);
  });

  it("uses fixed pressure for non-pen input and the default for zero pen readings", () => {
    expect(normalizePressure(0.9, false)).toBe(0.5);
    expect(normalizePressure(0, true)).toBe(0.5);
    expect(normalizePressure(0.8, true)).toBe(0.8);
    expect(normalizePressure(Number.NaN, true)).toBe(0.5);
  });

  it("makes identical decisions for the same client path at different image widths, zooms and DPR", () => {
    const offsets: Array<[number, number]> = [];
    for (let i = 0; i <= 60; i += 1) offsets.push([i * 0.7, Math.sin(i / 5) * 9]);
    const frames = [image(400, 300), image(800, 600), wall(0.02), wall(0.2), wall(1), wall(3)];
    const g = globalThis as { devicePixelRatio?: number };
    const original = g.devicePixelRatio;
    const kept: number[] = [];
    try {
      for (const dpr of [1, 2]) {
        g.devicePixelRatio = dpr;
        for (const frozen of frames) {
          const c = capture(frozen);
          const raws = path(frozen, offsets);
          c.begin(raws[0]);
          c.add(raws.slice(1));
          kept.push(c.keptCount);
        }
      }
    } finally {
      g.devicePixelRatio = original;
    }
    expect(new Set(kept).size).toBe(1);
  });

  it("maps logical outputs exactly by the frozen transform", () => {
    const small = finished((() => {
      const c = capture(image(400, 300));
      c.begin({ clientX: 10 + 100, clientY: 20 + 75 });
      return c.finish({ clientX: 10 + 200, clientY: 20 + 150 });
    })());
    const large = finished((() => {
      const c = capture(image(800, 600));
      c.begin({ clientX: 10 + 100, clientY: 20 + 75 });
      return c.finish({ clientX: 10 + 200, clientY: 20 + 150 });
    })());
    expect(small.points).toEqual([[1000, 750, 0.5], [2000, 1500, 0.5]]);
    expect(large.points).toEqual([[500, 375, 0.5], [1000, 750, 0.5]]);
  });
});

describe("stroke capture lifecycle", () => {
  it("always keeps the first and final samples and ends where the pointer lifted", () => {
    const frozen = wall(1);
    const c = capture(frozen);
    c.begin({ clientX: 5 + 100 + 0, clientY: 7 - 50 + 0 });
    c.add(path(frozen, [[0.5, 0], [1, 0]]));
    const s = finished(c.finish({ clientX: 5 + 100 + 1.5, clientY: 7 - 50 }));
    expect(s.points[0]).toEqual([0, 0, 0.5]);
    expect(s.points[s.points.length - 1]).toEqual([1.5, 0, 0.5]);
  });

  it("dedupes an identical final sample", () => {
    const frozen = wall(1);
    const c = capture(frozen);
    c.begin({ clientX: 105, clientY: -43 });
    const s = finished(c.finish({ clientX: 105, clientY: -43 }));
    expect(s.points).toHaveLength(1);
    expect(s.screenKept).toBe(1);
  });

  it("produces a single-point dot at the converted brush size", () => {
    const c = capture(image(400, 300));
    c.begin({ clientX: 60, clientY: 70 });
    const s = finished(c.finish());
    expect(s.points).toHaveLength(1);
    expect(s.size).toBe(40);
    expect(pointsToSvgPath(s.points)).toMatch(/^M \d+(\.\d)? \d+(\.\d)? L /);
  });

  it("finalizes exactly at the 512th kept point with limitReached and no continuation", () => {
    const frozen = wall(1);
    const c = capture(frozen);
    c.begin({ clientX: frozen.originX, clientY: frozen.originY });
    const raws: RawSample[] = [];
    for (let i = 1; i < 2000; i += 1) raws.push({ clientX: frozen.originX + i * 3, clientY: frozen.originY + ((i % 2) * 6) });
    const outcome = c.add(raws);
    expect(outcome?.status).toBe("finished");
    if (outcome?.status !== "finished") return;
    expect(outcome.limitReached).toBe(true);
    expect(outcome.stroke.screenKept).toBe(MAX_STROKE_POINTS);
    expect(outcome.stroke.points.length).toBeLessThanOrEqual(MAX_STROKE_POINTS);
    expect(c.add(raws)).toBeNull();
    expect(c.isActive).toBe(false);
  });

  it("drops non-finite samples at intake", () => {
    const frozen = wall(1);
    const c = capture(frozen);
    c.begin({ clientX: 105, clientY: -43 });
    c.add([{ clientX: Number.NaN, clientY: 1 }, { clientX: Infinity, clientY: 2 }]);
    expect(c.keptCount).toBe(1);
  });

  it("abort carries no stroke data", () => {
    const c = capture(wall(1));
    c.begin({ clientX: 1, clientY: 1 });
    const outcome = c.abort("native-cancel");
    expect(outcome).toEqual({ status: "aborted", reason: "native-cancel" });
    expect(c.previewPoints()).toEqual([]);
  });

  it("finishing under a frozen transform ignores later layout changes", () => {
    const t = { rectLeft: 0, rectTop: 0, rectWidth: 400, rectHeight: 300, naturalWidth: 4000, naturalHeight: 3000 };
    const frozen = freezeImageCapture(t);
    t.rectWidth = 800;
    const c = new StrokeCapture(frozen, PEN, { id: "s", createdAt: 0, pressureCapable: false });
    c.begin({ clientX: 100, clientY: 100 });
    const s = finished(c.finish({ clientX: 200, clientY: 100 }));
    expect(s.points[1][0]).toBe(2000);
  });

  it("excludes screenKept from serialized strokes", () => {
    const c = capture(wall(1));
    c.begin({ clientX: 105, clientY: -43 });
    const s = finished(c.finish({ clientX: 125, clientY: -43 }));
    const serialized = JSON.parse(JSON.stringify(stripScreenKept(s)));
    expect(serialized).not.toHaveProperty("screenKept");
    expect(Object.keys(serialized).sort()).toEqual(["color", "createdAt", "id", "opacity", "points", "size", "tool"]);
  });
});

describe("pressure anchors", () => {
  it("retains a stationary pressure ramp that plain RDP would collapse", () => {
    const frozen = wall(1);
    const c = capture(frozen, { pressureCapable: true });
    const ramp: Array<[number, number, number]> = [[0, 0, 0.1], [0.2, 0.1, 0.3], [0.1, -0.1, 0.5], [0, 0, 0.7], [0.1, 0.1, 0.9]];
    const raws = path(frozen, ramp);
    c.begin(raws[0]);
    c.add(raws.slice(1, -1));
    const s = finished(c.finish(raws[raws.length - 1]));
    expect(s.points.map((p) => p[2])).toEqual([0.1, 0.3, 0.5, 0.7, 0.9]);
    const plain = simplifyRDPIndices(ramp.map(([x, y]) => [x, y] as ScreenPos), 1.5);
    expect(plain).toEqual([0, 4]);
  });

  it("marks spatially admitted samples that cross a pressure step and simplifies straight runs between", () => {
    const positions: ScreenPos[] = [];
    const anchors: boolean[] = [];
    for (let i = 0; i < 40; i += 1) {
      positions.push([i * 3, 0]);
      anchors.push(i === 13 || i === 27);
    }
    const survivors = simplifyWithAnchors(positions, anchors, 1.5);
    expect(survivors).toEqual([0, 13, 27, 39]);
  });
});

describe("RDP index contract and degeneracy", () => {
  it("handles 0, 1 and 2 points", () => {
    expect(simplifyRDPIndices([], 1.5)).toEqual([]);
    expect(simplifyRDPIndices([[1, 1]], 1.5)).toEqual([0]);
    expect(simplifyRDPIndices([[1, 1], [2, 2]], 1.5)).toEqual([0, 1]);
  });

  it("returns ascending unique survivors including both endpoints", () => {
    const pts: ScreenPos[] = [];
    for (let i = 0; i < 200; i += 1) pts.push([i, Math.round(Math.sin(i / 7) * 30)]);
    const s = simplifyRDPIndices(pts, 1.5);
    expect(s[0]).toBe(0);
    expect(s[s.length - 1]).toBe(199);
    for (let i = 1; i < s.length; i += 1) expect(s[i]).toBeGreaterThan(s[i - 1]);
  });

  it("uses point-to-endpoint distance for coincident endpoints", () => {
    expect(perpendicularDistance([3, 4], [0, 0], [0, 0])).toBe(5);
    const loop: ScreenPos[] = [[0, 0], [10, 0], [10, 10], [0, 0]];
    expect(simplifyRDPIndices(loop, 1.5)).toEqual([0, 1, 2, 3]);
  });

  it("clamps the projection to the segment", () => {
    expect(perpendicularDistance([-3, 4], [0, 0], [10, 0])).toBe(5);
  });

  it("rejects non-finite input with RangeError", () => {
    expect(() => perpendicularDistance([Number.NaN, 0], [0, 0], [1, 1])).toThrow(RangeError);
  });
});

describe("rendering geometry", () => {
  it("emits a cubic Bezier path through every point", () => {
    const d = pointsToSvgPath([[0, 0, 0.5], [10, 0, 0.5], [20, 10, 0.5]]);
    expect(d.startsWith("M 0 0")).toBe(true);
    expect((d.match(/ C /g) ?? []).length).toBe(2);
    expect(d.endsWith("20 10")).toBe(true);
  });

  it("keeps constant width for strokes without pressure variation", () => {
    expect(hasPressureVariation({ tool: "pen", points: [[0, 0, 0.5], [1, 1, 0.5]] })).toBe(false);
    expect(hasPressureVariation({ tool: "highlighter", points: [[0, 0, 0.2], [1, 1, 0.9]] })).toBe(false);
    expect(hasPressureVariation({ tool: "pen", points: [[0, 0, 0.2], [1, 1, 0.9]] })).toBe(true);
  });

  it("builds a closed filled outline whose width follows pressure", () => {
    expect(pressureWidthFactor(0.5)).toBeCloseTo(1, 10);
    expect(pressureWidthFactor(0)).toBeCloseTo(0.4, 10);
    expect(pressureWidthFactor(1)).toBeCloseTo(1.6, 10);
    const d = strokeOutlinePath([[0, 0, 0.1], [50, 0, 0.5], [100, 0, 1]], 10);
    expect(d.startsWith("M ")).toBe(true);
    expect(d.endsWith("Z")).toBe(true);
    const ys = Array.from(d.matchAll(/L (-?[\d.]+) (-?[\d.]+)/g)).map((m) => Math.abs(Number(m[2])));
    expect(Math.max(...ys)).toBeGreaterThan(7);
    expect(strokeOutlinePath([[5, 5, 0.5]], 4)).toContain("A 2 2");
  });
});

describe("stroke-level eraser", () => {
  const stroke: DrawingStroke = {
    id: "a", tool: "pen", color: "#000000", size: 2, opacity: 1, createdAt: 0,
    points: [[0, 0, 0.5], [100, 0, 0.5]],
  };

  it("converts the 12 CSS px radius per context", () => {
    expect(image(400, 300).eraserRadii(12)).toEqual({ rx: 120, ry: 120 });
    expect(image(400, 600).eraserRadii(12)).toEqual({ rx: 120, ry: 60 });
    expect(wall(3).eraserRadii(12)).toEqual({ rx: 4, ry: 4 });
  });

  it("hits within radius plus half width and misses outside", () => {
    expect(eraserHitsStroke(stroke, 50, 12.9, 12, 12)).toBe(true);
    expect(eraserHitsStroke(stroke, 50, 13.5, 12, 12)).toBe(false);
    expect(eraserHitsStroke(stroke, 50, 20, 12, 30)).toBe(true);
    expect(eraserHitsStroke(stroke, -14, 0, 12, 12)).toBe(false);
  });

  it("returns only the hit stroke ids", () => {
    const other: DrawingStroke = { ...stroke, id: "b", points: [[0, 500, 0.5]] };
    expect(findErasedStrokeIds([stroke, other], 0, 499, 12, 12)).toEqual(["b"]);
  });
});

describe("document validation", () => {
  const good = { version: 1, width: 100, height: 50, strokes: [{ id: "s1", tool: "pen", color: "#112233", size: 3, opacity: 1, points: [[1, 2, 0.5]], createdAt: 5 }] };

  it("accepts a well-formed document", () => {
    expect(sanitizeDrawingDocument(good)?.strokes).toHaveLength(1);
  });

  it("rejects malformed colors, tools, bounds and markup injection", () => {
    const bad = (patch: Record<string, unknown>) => ({ ...good, strokes: [{ ...good.strokes[0], ...patch }] });
    expect(sanitizeDrawingDocument(bad({ color: "red\" onload=\"x" }))).toBeNull();
    expect(sanitizeDrawingDocument(bad({ tool: "eraser" }))).toBeNull();
    expect(sanitizeDrawingDocument(bad({ points: [[101, 2, 0.5]] }))).toBeNull();
    expect(sanitizeDrawingDocument(bad({ points: [[1, 2, 1.5]] }))).toBeNull();
    expect(sanitizeDrawingDocument({ ...good, version: 2 })).toBeNull();
    expect(sanitizeStroke({ ...good.strokes[0], points: [[200000, 0, 0.5]] }, { kind: "world" })).toBeNull();
  });

  it("keeps the worst-case 50 x 512-point document measurable against the byte ceiling", () => {
    const strokes = [];
    for (let s = 0; s < 50; s += 1) {
      const points = [];
      for (let i = 0; i < 512; i += 1) points.push([1234.5, 678.9, 0.55]);
      strokes.push({ id: `s${s}`, tool: "pen", color: "#112233", size: 3.25, opacity: 1, points, createdAt: 1700000000000 });
    }
    const doc = sanitizeDrawingDocument({ version: 1, width: 4000, height: 3000, strokes });
    expect(doc).not.toBeNull();
    expect(drawingByteSize(doc!)).toBeGreaterThan(MAX_TODO_DRAWING_BYTES);
  });
});

// @vitest-environment happy-dom
//
// Phase 0 characterization tests for the wall debug counter surface.
//
// Scope: the `__get*Counters` / `__reset*Counters` helpers only. Full
// integration of the counter increments (renderSurface / beginDrag) is
// covered implicitly by the existing wall-rendering, wall-gesture-matrix,
// and wall-interactions suites not regressing.

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../dom/elements.js", () => {
  const wallSurface = document.createElement("div");
  wallSurface.id = "wallSurface";
  document.body.appendChild(wallSurface);
  return {
    wallDialog: null,
    wallSurface,
    wallTrash: null,
    closeWallBtn: null,
  };
});
vi.mock("../api.js", () => ({ apiFetch: vi.fn() }));

import {
  __getWallRenderCounters,
  __resetWallRenderCounters,
} from "./wall.js";
import {
  __getDragCounters,
  __resetDragCounters,
} from "./wall-drag-controller.js";

describe("wall.ts Phase 0 render counters", () => {
  beforeEach(() => {
    __resetWallRenderCounters();
  });

  it("starts at zero", () => {
    expect(__getWallRenderCounters()).toEqual({ fullRebuilds: 0, incrementalPatches: 0 });
  });

  it("returns a snapshot, not a live reference", () => {
    const snap = __getWallRenderCounters();
    (snap as any).fullRebuilds = 999;
    expect(__getWallRenderCounters().fullRebuilds).toBe(0);
  });
});

describe("wall-drag-controller.ts Phase 0 drag counters", () => {
  beforeEach(() => {
    __resetDragCounters();
  });

  it("starts at zero", () => {
    expect(__getDragCounters()).toEqual({ edgeUpdateBatches: 0, edgeUpdateCalls: 0 });
  });

  it("returns a snapshot, not a live reference", () => {
    const snap = __getDragCounters();
    (snap as any).edgeUpdateBatches = 999;
    expect(__getDragCounters().edgeUpdateBatches).toBe(0);
  });
});

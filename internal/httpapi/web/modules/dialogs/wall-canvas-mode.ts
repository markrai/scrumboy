export type WallCanvasMode = "select" | "pan" | "draw";

// Global (not per-project) canvas-mode preference. The wall is per-project but
// the Select/Pan toggle is remembered across every board's wall via a single
// localStorage key. Mirrors the resilient try/catch storage pattern used by
// wall-viewport.ts.
const CANVAS_MODE_STORAGE_KEY = "scrumboy.wall.canvasMode";

let wallCanvasMode: WallCanvasMode = "select";

export function getWallCanvasMode(): WallCanvasMode {
  return wallCanvasMode;
}

/** Coerce arbitrary input to a valid mode; anything but "pan" falls to "select". */
export function normalizeWallCanvasMode(raw: unknown): WallCanvasMode {
  return raw === "pan" ? "pan" : "select";
}

function saveWallCanvasMode(mode: WallCanvasMode): void {
  try {
    localStorage.setItem(CANVAS_MODE_STORAGE_KEY, mode);
  } catch {
    // private mode / quota / disabled — ignore
  }
}

export function setWallCanvasMode(mode: WallCanvasMode): void {
  wallCanvasMode = normalizeWallCanvasMode(mode);
  saveWallCanvasMode(wallCanvasMode);
}

export function toggleWallCanvasMode(): WallCanvasMode {
  wallCanvasMode = wallCanvasMode === "pan" ? "select" : wallCanvasMode === "select" ? "pan" : "select";
  saveWallCanvasMode(wallCanvasMode);
  return wallCanvasMode;
}

export function isWallPanMode(): boolean {
  return wallCanvasMode === "pan";
}

let modeBeforeDraw: "select" | "pan" = "select";

export function enterWallDrawMode(): void {
  if (wallCanvasMode !== "draw") modeBeforeDraw = wallCanvasMode;
  wallCanvasMode = "draw";
}

export function exitWallDrawMode(): void {
  if (wallCanvasMode === "draw") wallCanvasMode = modeBeforeDraw;
}

export function isWallDrawMode(): boolean {
  return wallCanvasMode === "draw";
}

/** Load the persisted global preference into memory and return it. */
export function loadWallCanvasMode(): WallCanvasMode {
  try {
    wallCanvasMode = normalizeWallCanvasMode(localStorage.getItem(CANVAS_MODE_STORAGE_KEY));
  } catch {
    wallCanvasMode = "select";
  }
  return wallCanvasMode;
}

/** Test-only: reset in-memory mode and clear the persisted preference. */
export function resetWallCanvasMode(): void {
  wallCanvasMode = "select";
  try {
    localStorage.removeItem(CANVAS_MODE_STORAGE_KEY);
  } catch {
    // ignore
  }
}

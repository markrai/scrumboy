// Global (not per-project) canvas-mode preference. The wall is per-project but
// the Select/Pan toggle is remembered across every board's wall via a single
// localStorage key. Mirrors the resilient try/catch storage pattern used by
// wall-viewport.ts.
const CANVAS_MODE_STORAGE_KEY = "scrumboy.wall.canvasMode";
let wallCanvasMode = "select";
export function getWallCanvasMode() {
    return wallCanvasMode;
}
/** Coerce arbitrary input to a valid mode; anything but "pan" falls to "select". */
export function normalizeWallCanvasMode(raw) {
    return raw === "pan" ? "pan" : "select";
}
function saveWallCanvasMode(mode) {
    try {
        localStorage.setItem(CANVAS_MODE_STORAGE_KEY, mode);
    }
    catch {
        // private mode / quota / disabled — ignore
    }
}
export function setWallCanvasMode(mode) {
    wallCanvasMode = normalizeWallCanvasMode(mode);
    saveWallCanvasMode(wallCanvasMode);
}
export function toggleWallCanvasMode() {
    wallCanvasMode = wallCanvasMode === "pan" ? "select" : wallCanvasMode === "select" ? "pan" : "select";
    saveWallCanvasMode(wallCanvasMode);
    return wallCanvasMode;
}
export function isWallPanMode() {
    return wallCanvasMode === "pan";
}
let modeBeforeDraw = "select";
export function enterWallDrawMode() {
    if (wallCanvasMode !== "draw")
        modeBeforeDraw = wallCanvasMode;
    wallCanvasMode = "draw";
}
export function exitWallDrawMode() {
    if (wallCanvasMode === "draw")
        wallCanvasMode = modeBeforeDraw;
}
export function isWallDrawMode() {
    return wallCanvasMode === "draw";
}
/** Load the persisted global preference into memory and return it. */
export function loadWallCanvasMode() {
    try {
        wallCanvasMode = normalizeWallCanvasMode(localStorage.getItem(CANVAS_MODE_STORAGE_KEY));
    }
    catch {
        wallCanvasMode = "select";
    }
    return wallCanvasMode;
}
/** Test-only: reset in-memory mode and clear the persisted preference. */
export function resetWallCanvasMode() {
    wallCanvasMode = "select";
    try {
        localStorage.removeItem(CANVAS_MODE_STORAGE_KEY);
    }
    catch {
        // ignore
    }
}

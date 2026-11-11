export const MAX_STROKE_POINTS = 512;
export const MAX_TODO_DRAWING_STROKES = 50;
export const MAX_WALL_STROKES = 500;
export const MAX_TODO_DRAWING_BYTES = 256 * 1024;
export const MAX_SUBMITTED_DRAWING_BYTES = 768 * 1024;
export const MAX_WALL_DRAWING_BYTES = 1024 * 1024;
export const MAX_DRAWING_DIMENSION = 20000;
export const MAX_WORLD_COORDINATE = 100000;
export const MAX_STROKE_SIZE = 20000;
export const MAX_NOTES_INK_STROKES = 1000;
export const MAX_NOTES_INK_BYTES = 512 * 1024;
export const MAX_NOTES_INK_DIMENSION = 200000;
export const MIN_POINT_DISTANCE_PX = 2;
export const PRESSURE_ANCHOR_DELTA = 0.15;
export const RDP_EPSILON_PX = 1.5;
export const ERASER_RADIUS_PX = 12;
export const DEFAULT_PRESSURE = 0.5;
export const PEN_OPACITY = 1;
export const HIGHLIGHTER_OPACITY = 0.35;
export const STANDALONE_DEFAULT_WIDTH = 800;
export const STANDALONE_DEFAULT_HEIGHT = 600;
export const DRAWING_ID_RE = /^drw_[A-Za-z0-9_-]{6,64}$/;
export const STROKE_ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
export const STROKE_COLOR_RE = /^#[0-9a-fA-F]{6}$/;
export function stripScreenKept(stroke) {
    const out = {
        id: stroke.id,
        tool: stroke.tool,
        color: stroke.color,
        size: stroke.size,
        opacity: stroke.opacity,
        points: stroke.points.map((p) => [p[0], p[1], p[2]]),
        createdAt: stroke.createdAt,
    };
    if (stroke.by !== undefined)
        out.by = stroke.by;
    return out;
}
function randomHex(bytes) {
    const buf = new Uint8Array(bytes);
    const c = globalThis.crypto;
    if (c && typeof c.getRandomValues === "function") {
        c.getRandomValues(buf);
    }
    else {
        for (let i = 0; i < bytes; i += 1)
            buf[i] = Math.floor(Math.random() * 256);
    }
    let out = "";
    for (const b of buf)
        out += b.toString(16).padStart(2, "0");
    return out;
}
export function newDrawingId() {
    return `drw_${randomHex(16)}`;
}
export function newStrokeId() {
    return `s_${randomHex(10)}`;
}
export function emptyDrawing(width, height) {
    return { version: 1, width, height, strokes: [] };
}
export function cloneDrawing(doc) {
    return {
        version: 1,
        width: doc.width,
        height: doc.height,
        strokes: doc.strokes.map((s) => ({ ...s, points: s.points.map((p) => [p[0], p[1], p[2]]) })),
    };
}

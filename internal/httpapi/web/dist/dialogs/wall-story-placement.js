import { clampCanvasCoord } from "./wall-viewport.js";
const MARGIN = 24;
const OVERLAP_LIMIT = 0.12;
function overlapRatio(a, b) {
    const width = Math.max(0, Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x));
    const height = Math.max(0, Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y));
    return (width * height) / Math.max(1, a.width * a.height);
}
/** Pick a deterministic visible position, preferring the upper-right reading area. */
export function chooseWallStoryPosition(visible, occupied, size) {
    const minX = visible.x + MARGIN;
    const minY = visible.y + MARGIN;
    const maxX = Math.max(minX, visible.x + visible.width - size.width - MARGIN);
    const maxY = Math.max(minY, visible.y + visible.height - size.height - MARGIN);
    const baseX = minX + (maxX - minX) * 0.68;
    const baseY = minY + (maxY - minY) * 0.28;
    const stepX = size.width + 20;
    const stepY = size.height + 20;
    const offsets = [
        [0, 0], [0, 1], [-1, 0], [-1, 1], [0, 2], [-2, 0], [-1, 2], [-2, 1],
        [1, 0], [1, 1], [0, 3], [-2, 2], [-3, 0], [-3, 1], [1, 2], [-1, 3],
    ];
    let best = { x: baseX, y: baseY, score: Number.POSITIVE_INFINITY };
    for (const [ox, oy] of offsets) {
        const x = Math.max(minX, Math.min(maxX, baseX + ox * stepX));
        const y = Math.max(minY, Math.min(maxY, baseY + oy * stepY));
        const candidate = { x, y, width: size.width, height: size.height };
        const score = occupied.reduce((sum, rect) => sum + overlapRatio(candidate, rect), 0);
        if (score < best.score)
            best = { x, y, score };
        if (score <= OVERLAP_LIMIT) {
            return { x: Math.round(clampCanvasCoord(x)), y: Math.round(clampCanvasCoord(y)) };
        }
    }
    return { x: Math.round(clampCanvasCoord(best.x)), y: Math.round(clampCanvasCoord(best.y)) };
}

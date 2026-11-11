export function strokeBounds(stroke) {
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const p of stroke.points) {
        if (p[0] < minX)
            minX = p[0];
        if (p[1] < minY)
            minY = p[1];
        if (p[0] > maxX)
            maxX = p[0];
        if (p[1] > maxY)
            maxY = p[1];
    }
    const pad = stroke.size / 2;
    return { minX: minX - pad, minY: minY - pad, maxX: maxX + pad, maxY: maxY + pad };
}
function normalizedSegmentDistance(px, py, ax, ay, bx, by) {
    const dx = bx - ax;
    const dy = by - ay;
    const lenSq = dx * dx + dy * dy;
    if (lenSq === 0)
        return Math.hypot(px - ax, py - ay);
    let t = ((px - ax) * dx + (py - ay) * dy) / lenSq;
    if (t < 0)
        t = 0;
    else if (t > 1)
        t = 1;
    return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
}
export function eraserHitsStroke(stroke, x, y, rx, ry) {
    if (!(rx > 0) || !(ry > 0) || !Number.isFinite(x) || !Number.isFinite(y))
        return false;
    if (stroke.points.length === 0)
        return false;
    const ex = rx + stroke.size / 2;
    const ey = ry + stroke.size / 2;
    const b = strokeBounds(stroke);
    if (x < b.minX - rx || x > b.maxX + rx || y < b.minY - ry || y > b.maxY + ry)
        return false;
    const px = x / ex;
    const py = y / ey;
    const pts = stroke.points;
    if (pts.length === 1) {
        return Math.hypot(px - pts[0][0] / ex, py - pts[0][1] / ey) <= 1;
    }
    for (let i = 0; i + 1 < pts.length; i += 1) {
        const d = normalizedSegmentDistance(px, py, pts[i][0] / ex, pts[i][1] / ey, pts[i + 1][0] / ex, pts[i + 1][1] / ey);
        if (d <= 1)
            return true;
    }
    return false;
}
export function findErasedStrokeIds(strokes, x, y, rx, ry) {
    const out = [];
    for (const s of strokes) {
        if (eraserHitsStroke(s, x, y, rx, ry))
            out.push(s.id);
    }
    return out;
}

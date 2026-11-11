import { RDP_EPSILON_PX } from "./drawing-types.js";
export { RDP_EPSILON_PX };
export function perpendicularDistance(p, a, b) {
    const values = [p[0], p[1], a[0], a[1], b[0], b[1]];
    for (const v of values) {
        if (!Number.isFinite(v))
            throw new RangeError("perpendicularDistance requires finite coordinates");
    }
    const dx = b[0] - a[0];
    const dy = b[1] - a[1];
    const lenSq = dx * dx + dy * dy;
    if (lenSq === 0) {
        return Math.hypot(p[0] - a[0], p[1] - a[1]);
    }
    let t = ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / lenSq;
    if (t < 0)
        t = 0;
    else if (t > 1)
        t = 1;
    return Math.hypot(p[0] - (a[0] + t * dx), p[1] - (a[1] + t * dy));
}
function rdpRange(positions, first, last, epsilon, keep) {
    const stack = [[first, last]];
    while (stack.length > 0) {
        const [lo, hi] = stack.pop();
        if (hi - lo < 2)
            continue;
        let maxDist = -1;
        let index = -1;
        for (let i = lo + 1; i < hi; i += 1) {
            const d = perpendicularDistance(positions[i], positions[lo], positions[hi]);
            if (d > maxDist) {
                maxDist = d;
                index = i;
            }
        }
        if (index >= 0 && maxDist > epsilon) {
            keep[index] = 1;
            stack.push([lo, index], [index, hi]);
        }
    }
}
export function simplifyRDPIndices(positions, epsilon = RDP_EPSILON_PX) {
    if (positions.length <= 2)
        return positions.map((_, i) => i);
    const keep = new Uint8Array(positions.length);
    keep[0] = 1;
    keep[positions.length - 1] = 1;
    rdpRange(positions, 0, positions.length - 1, epsilon, keep);
    const out = [];
    for (let i = 0; i < keep.length; i += 1)
        if (keep[i])
            out.push(i);
    return out;
}
export function simplifyWithAnchors(positions, anchors, epsilon = RDP_EPSILON_PX) {
    const n = positions.length;
    if (n <= 2)
        return positions.map((_, i) => i);
    const keep = new Uint8Array(n);
    const anchorIdx = [];
    for (let i = 0; i < n; i += 1) {
        if (i === 0 || i === n - 1 || anchors[i]) {
            anchorIdx.push(i);
            keep[i] = 1;
        }
    }
    for (let k = 0; k + 1 < anchorIdx.length; k += 1) {
        rdpRange(positions, anchorIdx[k], anchorIdx[k + 1], epsilon, keep);
    }
    const out = [];
    for (let i = 0; i < n; i += 1)
        if (keep[i])
            out.push(i);
    return out;
}

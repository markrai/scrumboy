import { DEFAULT_PRESSURE, type DrawingPoint, type DrawingStroke } from "./drawing-types.js";

function f(n: number): string {
  const r = Math.round(n * 10) / 10;
  return Object.is(r, -0) ? "0" : String(r);
}

export function pointsToSvgPath(points: readonly DrawingPoint[]): string {
  if (points.length === 0) return "";
  if (points.length === 1) {
    const [x, y] = points[0];
    return `M ${f(x)} ${f(y)} L ${f(x + 0.1)} ${f(y + 0.1)}`;
  }
  if (points.length === 2) {
    return `M ${f(points[0][0])} ${f(points[0][1])} L ${f(points[1][0])} ${f(points[1][1])}`;
  }
  let d = `M ${f(points[0][0])} ${f(points[0][1])}`;
  for (let i = 0; i < points.length - 1; i += 1) {
    const p0 = points[i === 0 ? 0 : i - 1];
    const p1 = points[i];
    const p2 = points[i + 1];
    const p3 = points[i + 2 < points.length ? i + 2 : i + 1];
    const cp1x = p1[0] + (p2[0] - p0[0]) / 6;
    const cp1y = p1[1] + (p2[1] - p0[1]) / 6;
    const cp2x = p2[0] - (p3[0] - p1[0]) / 6;
    const cp2y = p2[1] - (p3[1] - p1[1]) / 6;
    d += ` C ${f(cp1x)} ${f(cp1y)}, ${f(cp2x)} ${f(cp2y)}, ${f(p2[0])} ${f(p2[1])}`;
  }
  return d;
}

export function pressureWidthFactor(pressure: number): number {
  const p = Number.isFinite(pressure) ? Math.min(1, Math.max(0, pressure)) : DEFAULT_PRESSURE;
  return 0.4 + 1.2 * p;
}

export function hasPressureVariation(stroke: Pick<DrawingStroke, "tool" | "points">): boolean {
  if (stroke.tool !== "pen") return false;
  for (const p of stroke.points) {
    if (Math.abs(p[2] - DEFAULT_PRESSURE) > 0.005) return true;
  }
  return false;
}

type Sample = { x: number; y: number; r: number };

const SEGMENT_SUBDIVISIONS = 4;

export function sampleCenterline(points: readonly DrawingPoint[], size: number): Sample[] {
  const half = size / 2;
  if (points.length <= 2) {
    return points.map((p) => ({ x: p[0], y: p[1], r: half * pressureWidthFactor(p[2]) }));
  }
  const out: Sample[] = [{ x: points[0][0], y: points[0][1], r: half * pressureWidthFactor(points[0][2]) }];
  for (let i = 0; i < points.length - 1; i += 1) {
    const p0 = points[i === 0 ? 0 : i - 1];
    const p1 = points[i];
    const p2 = points[i + 1];
    const p3 = points[i + 2 < points.length ? i + 2 : i + 1];
    const c1x = p1[0] + (p2[0] - p0[0]) / 6;
    const c1y = p1[1] + (p2[1] - p0[1]) / 6;
    const c2x = p2[0] - (p3[0] - p1[0]) / 6;
    const c2y = p2[1] - (p3[1] - p1[1]) / 6;
    for (let k = 1; k <= SEGMENT_SUBDIVISIONS; k += 1) {
      const t = k / SEGMENT_SUBDIVISIONS;
      const u = 1 - t;
      const x = u * u * u * p1[0] + 3 * u * u * t * c1x + 3 * u * t * t * c2x + t * t * t * p2[0];
      const y = u * u * u * p1[1] + 3 * u * u * t * c1y + 3 * u * t * t * c2y + t * t * t * p2[1];
      const pressure = p1[2] + (p2[2] - p1[2]) * t;
      out.push({ x, y, r: half * pressureWidthFactor(pressure) });
    }
  }
  return out;
}

function circlePath(x: number, y: number, r: number): string {
  const rr = Math.max(r, 0.05);
  return `M ${f(x - rr)} ${f(y)} A ${f(rr)} ${f(rr)} 0 1 0 ${f(x + rr)} ${f(y)} A ${f(rr)} ${f(rr)} 0 1 0 ${f(x - rr)} ${f(y)} Z`;
}

export function strokeOutlinePath(points: readonly DrawingPoint[], size: number): string {
  if (points.length === 0) return "";
  const samples = sampleCenterline(points, size);
  const dedup: Sample[] = [];
  for (const s of samples) {
    const last = dedup[dedup.length - 1];
    if (last && Math.hypot(s.x - last.x, s.y - last.y) < 1e-6) {
      last.r = Math.max(last.r, s.r);
      continue;
    }
    dedup.push({ ...s });
  }
  if (dedup.length === 1) {
    return circlePath(dedup[0].x, dedup[0].y, dedup[0].r);
  }
  const left: Array<[number, number]> = [];
  const right: Array<[number, number]> = [];
  for (let i = 0; i < dedup.length; i += 1) {
    const prev = dedup[Math.max(0, i - 1)];
    const next = dedup[Math.min(dedup.length - 1, i + 1)];
    let tx = next.x - prev.x;
    let ty = next.y - prev.y;
    const len = Math.hypot(tx, ty) || 1;
    tx /= len;
    ty /= len;
    const nx = -ty;
    const ny = tx;
    const s = dedup[i];
    left.push([s.x + nx * s.r, s.y + ny * s.r]);
    right.push([s.x - nx * s.r, s.y - ny * s.r]);
  }
  const first = dedup[0];
  const last = dedup[dedup.length - 1];
  let d = `M ${f(left[0][0])} ${f(left[0][1])}`;
  for (let i = 1; i < left.length; i += 1) d += ` L ${f(left[i][0])} ${f(left[i][1])}`;
  const endR = Math.max(last.r, 0.05);
  d += ` A ${f(endR)} ${f(endR)} 0 0 1 ${f(right[right.length - 1][0])} ${f(right[right.length - 1][1])}`;
  for (let i = right.length - 2; i >= 0; i -= 1) d += ` L ${f(right[i][0])} ${f(right[i][1])}`;
  const startR = Math.max(first.r, 0.05);
  d += ` A ${f(startR)} ${f(startR)} 0 0 1 ${f(left[0][0])} ${f(left[0][1])} Z`;
  return d;
}

import {
  MAX_DRAWING_DIMENSION,
  MAX_NOTES_INK_DIMENSION,
  MAX_NOTES_INK_STROKES,
  MAX_STROKE_POINTS,
  MAX_STROKE_SIZE,
  MAX_TODO_DRAWING_BYTES,
  MAX_TODO_DRAWING_STROKES,
  MAX_WORLD_COORDINATE,
  STROKE_COLOR_RE,
  STROKE_ID_RE,
  type DrawingDocument,
  type DrawingPoint,
  type DrawingStroke,
} from "./drawing-types.js";

function finite(v: unknown): v is number {
  return typeof v === "number" && Number.isFinite(v);
}

export type StrokeBoundsMode = { kind: "document"; width: number; height: number } | { kind: "world" };

export function sanitizeStroke(raw: unknown, bounds: StrokeBoundsMode): DrawingStroke | null {
  if (!raw || typeof raw !== "object") return null;
  const s = raw as Record<string, unknown>;
  if (typeof s.id !== "string" || !STROKE_ID_RE.test(s.id)) return null;
  if (s.tool !== "pen" && s.tool !== "highlighter") return null;
  if (typeof s.color !== "string" || !STROKE_COLOR_RE.test(s.color)) return null;
  if (!finite(s.size) || s.size <= 0 || s.size > MAX_STROKE_SIZE) return null;
  if (!finite(s.opacity) || s.opacity < 0 || s.opacity > 1) return null;
  if (!Array.isArray(s.points) || s.points.length === 0 || s.points.length > MAX_STROKE_POINTS) return null;
  const points: DrawingPoint[] = [];
  for (const p of s.points) {
    if (!Array.isArray(p) || p.length !== 3) return null;
    const [x, y, pr] = p;
    if (!finite(x) || !finite(y) || !finite(pr) || pr < 0 || pr > 1) return null;
    if (bounds.kind === "document") {
      if (x < 0 || y < 0 || x > bounds.width || y > bounds.height) return null;
    } else if (Math.abs(x) > MAX_WORLD_COORDINATE || Math.abs(y) > MAX_WORLD_COORDINATE) {
      return null;
    }
    points.push([x, y, pr]);
  }
  const createdAt = finite(s.createdAt) ? s.createdAt : 0;
  const out: DrawingStroke = {
    id: s.id,
    tool: s.tool,
    color: s.color,
    size: s.size,
    opacity: s.opacity,
    points,
    createdAt,
  };
  if (finite(s.by)) out.by = s.by;
  return out;
}

export type DocumentLimits = { maxStrokes: number; maxDimension: number };

const REFERENCE_LIMITS: DocumentLimits = { maxStrokes: MAX_TODO_DRAWING_STROKES, maxDimension: MAX_DRAWING_DIMENSION };
export const NOTES_INK_LIMITS: DocumentLimits = { maxStrokes: MAX_NOTES_INK_STROKES, maxDimension: MAX_NOTES_INK_DIMENSION };

export function sanitizeNotesInk(raw: unknown): DrawingDocument | null {
  return sanitizeDrawingDocument(raw, NOTES_INK_LIMITS);
}

export function sanitizeDrawingDocument(raw: unknown, limits: DocumentLimits = REFERENCE_LIMITS): DrawingDocument | null {
  if (!raw || typeof raw !== "object") return null;
  const d = raw as Record<string, unknown>;
  if (d.version !== 1) return null;
  if (!finite(d.width) || !finite(d.height)) return null;
  if (d.width <= 0 || d.height <= 0 || d.width > limits.maxDimension || d.height > limits.maxDimension) return null;
  if (!Array.isArray(d.strokes) || d.strokes.length > limits.maxStrokes) return null;
  const strokes: DrawingStroke[] = [];
  const seen = new Set<string>();
  for (const raw of d.strokes) {
    const s = sanitizeStroke(raw, { kind: "document", width: d.width, height: d.height });
    if (!s || seen.has(s.id)) return null;
    seen.add(s.id);
    strokes.push(s);
  }
  return { version: 1, width: d.width, height: d.height, strokes };
}

export function drawingByteSize(doc: DrawingDocument): number {
  return new TextEncoder().encode(JSON.stringify(doc)).length;
}

export function drawingFitsBudget(doc: DrawingDocument): boolean {
  return doc.strokes.length <= MAX_TODO_DRAWING_STROKES && drawingByteSize(doc) <= MAX_TODO_DRAWING_BYTES;
}

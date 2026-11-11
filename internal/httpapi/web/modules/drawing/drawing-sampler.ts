import { roundCoord, roundPressure, roundSize, type FrozenCapture } from "./drawing-math.js";
import { simplifyWithAnchors, type ScreenPos } from "./drawing-simplify.js";
import {
  DEFAULT_PRESSURE,
  MAX_STROKE_POINTS,
  MIN_POINT_DISTANCE_PX,
  PRESSURE_ANCHOR_DELTA,
  RDP_EPSILON_PX,
  type CompletedStroke,
  type DrawingPoint,
  type InkTool,
  type StrokeAbortReason,
  type StrokeOutcome,
} from "./drawing-types.js";

const MIN_POINT_DISTANCE_SQ = MIN_POINT_DISTANCE_PX * MIN_POINT_DISTANCE_PX;
const PRESSURE_EPSILON = 1e-9;

export function isPressureStep(a: number, b: number): boolean {
  return Math.abs(a - b) >= PRESSURE_ANCHOR_DELTA - PRESSURE_EPSILON;
}

export type SampledPoint = { sx: number; sy: number; pressure: number; pressureAnchor: boolean };

export type RawSample = { clientX: number; clientY: number; pressure?: number };

export type StrokeStyle = {
  tool: InkTool;
  color: string;
  cssSize: number;
  opacity: number;
};

export type StrokeCaptureOptions = {
  id: string;
  createdAt: number;
  by?: number;
  pressureCapable: boolean;
  maxPoints?: number;
};

export function shouldKeepSample(lastKept: SampledPoint, s: SampledPoint): boolean {
  const dx = s.sx - lastKept.sx;
  const dy = s.sy - lastKept.sy;
  if (dx * dx + dy * dy >= MIN_POINT_DISTANCE_SQ) return true;
  return isPressureStep(s.pressure, lastKept.pressure);
}

export function normalizePressure(raw: number | undefined, pressureCapable: boolean): number {
  if (!pressureCapable) return DEFAULT_PRESSURE;
  if (typeof raw !== "number" || !Number.isFinite(raw) || raw <= 0) return DEFAULT_PRESSURE;
  return raw > 1 ? 1 : raw;
}

type CaptureState = "idle" | "active" | "done";

export class StrokeCapture {
  private readonly screen: ScreenPos[] = [];
  private readonly logical: DrawingPoint[] = [];
  private readonly anchors: boolean[] = [];
  private readonly samples: SampledPoint[] = [];
  private state: CaptureState = "idle";
  private readonly maxPoints: number;
  private readonly size: number;

  constructor(
    private readonly capture: FrozenCapture,
    private readonly style: StrokeStyle,
    private readonly opts: StrokeCaptureOptions,
  ) {
    this.maxPoints = Math.max(1, Math.min(opts.maxPoints ?? MAX_STROKE_POINTS, MAX_STROKE_POINTS));
    this.size = roundSize(capture.brushToLogical(style.cssSize));
  }

  get isActive(): boolean {
    return this.state === "active";
  }

  get keptCount(): number {
    return this.screen.length;
  }

  get logicalSize(): number {
    return this.size;
  }

  previewPoints(): DrawingPoint[] {
    return this.logical.slice();
  }

  begin(sample: RawSample): boolean {
    if (this.state !== "idle") return false;
    const s = this.toSampled(sample);
    if (!s) return false;
    this.state = "active";
    this.keep(s, false);
    return true;
  }

  add(samples: readonly RawSample[]): StrokeOutcome | null {
    if (this.state !== "active") return null;
    for (const raw of samples) {
      const s = this.toSampled(raw);
      if (!s) continue;
      const last = this.samples[this.samples.length - 1];
      if (!shouldKeepSample(last, s)) continue;
      const anchor = isPressureStep(s.pressure, last.pressure);
      this.keep(s, anchor);
      if (this.screen.length >= this.maxPoints) {
        return this.complete(true);
      }
    }
    return null;
  }

  finish(final?: RawSample): StrokeOutcome {
    if (this.state !== "active") {
      return { status: "aborted", reason: "user-cancel" };
    }
    if (final) {
      const s = this.toSampled(final);
      if (s) {
        const last = this.samples[this.samples.length - 1];
        const identical = s.sx === last.sx && s.sy === last.sy && s.pressure === last.pressure;
        if (!identical && this.screen.length < this.maxPoints) {
          const anchor = isPressureStep(s.pressure, last.pressure);
          this.keep(s, anchor);
        }
      }
    }
    return this.complete(false);
  }

  abort(reason: StrokeAbortReason): StrokeOutcome {
    this.state = "done";
    this.screen.length = 0;
    this.logical.length = 0;
    this.anchors.length = 0;
    this.samples.length = 0;
    return { status: "aborted", reason };
  }

  private toSampled(raw: RawSample): SampledPoint | null {
    if (!Number.isFinite(raw.clientX) || !Number.isFinite(raw.clientY)) return null;
    const pressure = normalizePressure(raw.pressure, this.opts.pressureCapable);
    return {
      sx: raw.clientX - this.capture.originX,
      sy: raw.clientY - this.capture.originY,
      pressure,
      pressureAnchor: false,
    };
  }

  private keep(s: SampledPoint, anchor: boolean): void {
    const p = this.capture.toLogical(s.sx, s.sy);
    s.pressureAnchor = anchor;
    this.samples.push(s);
    this.screen.push([s.sx, s.sy]);
    this.anchors.push(anchor);
    this.logical.push([roundCoord(p.x), roundCoord(p.y), roundPressure(s.pressure)]);
  }

  private complete(limitReached: boolean): StrokeOutcome {
    this.state = "done";
    let survivors: number[];
    try {
      survivors = simplifyWithAnchors(this.screen, this.anchors, RDP_EPSILON_PX);
    } catch {
      return { status: "aborted", reason: "user-cancel" };
    }
    const points = survivors.map((i) => {
      const p = this.logical[i];
      return [p[0], p[1], p[2]] as DrawingPoint;
    });
    const stroke: CompletedStroke = {
      id: this.opts.id,
      tool: this.style.tool,
      color: this.style.color,
      size: this.size,
      opacity: this.style.opacity,
      points,
      screenKept: this.screen.length,
      createdAt: this.opts.createdAt,
    };
    if (this.opts.by !== undefined) stroke.by = this.opts.by;
    return { status: "finished", limitReached, stroke };
  }
}

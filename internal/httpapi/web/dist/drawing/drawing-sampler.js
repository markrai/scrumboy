import { roundCoord, roundPressure, roundSize } from "./drawing-math.js";
import { simplifyWithAnchors } from "./drawing-simplify.js";
import { DEFAULT_PRESSURE, MAX_STROKE_POINTS, MIN_POINT_DISTANCE_PX, PRESSURE_ANCHOR_DELTA, RDP_EPSILON_PX, } from "./drawing-types.js";
const MIN_POINT_DISTANCE_SQ = MIN_POINT_DISTANCE_PX * MIN_POINT_DISTANCE_PX;
const PRESSURE_EPSILON = 1e-9;
export function isPressureStep(a, b) {
    return Math.abs(a - b) >= PRESSURE_ANCHOR_DELTA - PRESSURE_EPSILON;
}
export function shouldKeepSample(lastKept, s) {
    const dx = s.sx - lastKept.sx;
    const dy = s.sy - lastKept.sy;
    if (dx * dx + dy * dy >= MIN_POINT_DISTANCE_SQ)
        return true;
    return isPressureStep(s.pressure, lastKept.pressure);
}
export function normalizePressure(raw, pressureCapable) {
    if (!pressureCapable)
        return DEFAULT_PRESSURE;
    if (typeof raw !== "number" || !Number.isFinite(raw) || raw <= 0)
        return DEFAULT_PRESSURE;
    return raw > 1 ? 1 : raw;
}
export class StrokeCapture {
    constructor(capture, style, opts) {
        this.capture = capture;
        this.style = style;
        this.opts = opts;
        this.screen = [];
        this.logical = [];
        this.anchors = [];
        this.samples = [];
        this.state = "idle";
        this.maxPoints = Math.max(1, Math.min(opts.maxPoints ?? MAX_STROKE_POINTS, MAX_STROKE_POINTS));
        this.size = roundSize(capture.brushToLogical(style.cssSize));
    }
    get isActive() {
        return this.state === "active";
    }
    get keptCount() {
        return this.screen.length;
    }
    get logicalSize() {
        return this.size;
    }
    previewPoints() {
        return this.logical.slice();
    }
    begin(sample) {
        if (this.state !== "idle")
            return false;
        const s = this.toSampled(sample);
        if (!s)
            return false;
        this.state = "active";
        this.keep(s, false);
        return true;
    }
    add(samples) {
        if (this.state !== "active")
            return null;
        for (const raw of samples) {
            const s = this.toSampled(raw);
            if (!s)
                continue;
            const last = this.samples[this.samples.length - 1];
            if (!shouldKeepSample(last, s))
                continue;
            const anchor = isPressureStep(s.pressure, last.pressure);
            this.keep(s, anchor);
            if (this.screen.length >= this.maxPoints) {
                return this.complete(true);
            }
        }
        return null;
    }
    finish(final) {
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
    abort(reason) {
        this.state = "done";
        this.screen.length = 0;
        this.logical.length = 0;
        this.anchors.length = 0;
        this.samples.length = 0;
        return { status: "aborted", reason };
    }
    toSampled(raw) {
        if (!Number.isFinite(raw.clientX) || !Number.isFinite(raw.clientY))
            return null;
        const pressure = normalizePressure(raw.pressure, this.opts.pressureCapable);
        return {
            sx: raw.clientX - this.capture.originX,
            sy: raw.clientY - this.capture.originY,
            pressure,
            pressureAnchor: false,
        };
    }
    keep(s, anchor) {
        const p = this.capture.toLogical(s.sx, s.sy);
        s.pressureAnchor = anchor;
        this.samples.push(s);
        this.screen.push([s.sx, s.sy]);
        this.anchors.push(anchor);
        this.logical.push([roundCoord(p.x), roundCoord(p.y), roundPressure(s.pressure)]);
    }
    complete(limitReached) {
        this.state = "done";
        let survivors;
        try {
            survivors = simplifyWithAnchors(this.screen, this.anchors, RDP_EPSILON_PX);
        }
        catch {
            return { status: "aborted", reason: "user-cancel" };
        }
        const points = survivors.map((i) => {
            const p = this.logical[i];
            return [p[0], p[1], p[2]];
        });
        const stroke = {
            id: this.opts.id,
            tool: this.style.tool,
            color: this.style.color,
            size: this.size,
            opacity: this.style.opacity,
            points,
            screenKept: this.screen.length,
            createdAt: this.opts.createdAt,
        };
        if (this.opts.by !== undefined)
            stroke.by = this.opts.by;
        return { status: "finished", limitReached, stroke };
    }
}

import type { FrozenCapture } from "./drawing-math.js";
import { StrokeCapture, type RawSample, type StrokeStyle } from "./drawing-sampler.js";
import { ERASER_RADIUS_PX, type DrawingPoint, type StrokeAbortReason, type StrokeOutcome, type StrokeTool } from "./drawing-types.js";

export type DrawingPointerHost = {
  element: HTMLElement;
  getTool(): StrokeTool;
  getStyle(): StrokeStyle;
  freeze(): FrozenCapture | null;
  canStartStroke(): boolean;
  newStrokeId(): string;
  now?(): number;
  userId?: number;
  onPreview(points: DrawingPoint[], size: number, style: StrokeStyle): void;
  onOutcome(outcome: StrokeOutcome): void;
  onErase(x: number, y: number, rx: number, ry: number): void;
  onEraseEnd?(): void;
  onEraseCancel?(): void;
  onActiveChange?(active: boolean): void;
  onRejected?(): void;
  shouldIgnore?(ev: PointerEvent): boolean;
};

export type DrawingPointerHandle = {
  isActive(): boolean;
  finishActive(): void;
  abortActive(reason: StrokeAbortReason): void;
  destroy(): void;
};

type Active =
  | { kind: "ink"; pointerId: number; pointerType: string; capture: StrokeCapture; frozen: FrozenCapture }
  | { kind: "erase"; pointerId: number; pointerType: string; frozen: FrozenCapture; rx: number; ry: number };

function coalesced(ev: PointerEvent): RawSample[] {
  const list = typeof ev.getCoalescedEvents === "function" ? ev.getCoalescedEvents() : [];
  const source = list && list.length > 0 ? list : [ev];
  return source.map((e) => ({ clientX: e.clientX, clientY: e.clientY, pressure: e.pressure }));
}

export function bindDrawingPointer(host: DrawingPointerHost): DrawingPointerHandle {
  const el = host.element;
  let active: Active | null = null;

  const release = (pointerId: number) => {
    try {
      if (typeof el.hasPointerCapture === "function" && el.hasPointerCapture(pointerId)) {
        el.releasePointerCapture(pointerId);
      }
    } catch {
      return;
    }
  };

  const setActive = (next: Active | null) => {
    const was = active !== null;
    active = next;
    const is = active !== null;
    if (was !== is) host.onActiveChange?.(is);
  };

  const emit = (outcome: StrokeOutcome) => {
    host.onPreview([], 0, host.getStyle());
    host.onOutcome(outcome);
  };

  const endInk = (outcome: StrokeOutcome) => {
    if (!active) return;
    const pointerId = active.pointerId;
    setActive(null);
    release(pointerId);
    emit(outcome);
  };

  const endErase = () => {
    if (!active) return;
    const pointerId = active.pointerId;
    setActive(null);
    release(pointerId);
    host.onEraseEnd?.();
  };

  const cancelErase = () => {
    if (!active) return;
    const pointerId = active.pointerId;
    setActive(null);
    release(pointerId);
    host.onEraseCancel?.();
  };

  const onDown = (ev: PointerEvent) => {
    if (host.shouldIgnore?.(ev)) return;
    if (ev.pointerType === "mouse" && ev.button !== 0) return;
    if (active) {
      if (ev.pointerId === active.pointerId) return;
      if (active.pointerType === "pen" && ev.pointerType === "touch") {
        ev.preventDefault();
        return;
      }
      if (active.pointerType === "touch" && ev.pointerType === "touch") {
        ev.preventDefault();
        if (active.kind === "ink") endInk(active.capture.abort("second-pointer"));
        else cancelErase();
      }
      return;
    }
    const frozen = host.freeze();
    if (!frozen) return;
    const tool = host.getTool();
    ev.preventDefault();
    if (tool === "eraser") {
      const { rx, ry } = frozen.eraserRadii(ERASER_RADIUS_PX);
      setActive({ kind: "erase", pointerId: ev.pointerId, pointerType: ev.pointerType, frozen, rx, ry });
      try {
        el.setPointerCapture(ev.pointerId);
      } catch {
        void 0;
      }
      const p = frozen.toLogical(ev.clientX - frozen.originX, ev.clientY - frozen.originY);
      host.onErase(p.x, p.y, rx, ry);
      return;
    }
    if (!host.canStartStroke()) {
      host.onRejected?.();
      return;
    }
    const style = host.getStyle();
    const capture = new StrokeCapture(frozen, style, {
      id: host.newStrokeId(),
      createdAt: host.now ? host.now() : Date.now(),
      by: host.userId,
      pressureCapable: ev.pointerType === "pen",
    });
    if (!capture.begin({ clientX: ev.clientX, clientY: ev.clientY, pressure: ev.pressure })) return;
    setActive({ kind: "ink", pointerId: ev.pointerId, pointerType: ev.pointerType, capture, frozen });
    try {
      el.setPointerCapture(ev.pointerId);
    } catch {
      void 0;
    }
    host.onPreview(capture.previewPoints(), capture.logicalSize, style);
  };

  const onMove = (ev: PointerEvent) => {
    if (!active || ev.pointerId !== active.pointerId) return;
    ev.preventDefault();
    if (active.kind === "erase") {
      const { frozen, rx, ry } = active;
      for (const s of coalesced(ev)) {
        if (!Number.isFinite(s.clientX) || !Number.isFinite(s.clientY)) continue;
        const p = frozen.toLogical(s.clientX - frozen.originX, s.clientY - frozen.originY);
        host.onErase(p.x, p.y, rx, ry);
      }
      return;
    }
    const outcome = active.capture.add(coalesced(ev));
    if (outcome) {
      endInk(outcome);
      return;
    }
    host.onPreview(active.capture.previewPoints(), active.capture.logicalSize, host.getStyle());
  };

  const onUp = (ev: PointerEvent) => {
    if (!active || ev.pointerId !== active.pointerId) return;
    ev.preventDefault();
    if (active.kind === "erase") {
      endErase();
      return;
    }
    endInk(active.capture.finish({ clientX: ev.clientX, clientY: ev.clientY, pressure: ev.pressure }));
  };

  const onCancel = (ev: PointerEvent) => {
    if (!active || ev.pointerId !== active.pointerId) return;
    if (active.kind === "erase") {
      cancelErase();
      return;
    }
    endInk(active.capture.abort("native-cancel"));
  };

  el.addEventListener("pointerdown", onDown);
  el.addEventListener("pointermove", onMove);
  el.addEventListener("pointerup", onUp);
  el.addEventListener("pointercancel", onCancel);
  el.addEventListener("lostpointercapture", onCancel as EventListener);

  return {
    isActive: () => active !== null,
    finishActive() {
      if (!active) return;
      if (active.kind === "erase") endErase();
      else endInk(active.capture.finish());
    },
    abortActive(reason: StrokeAbortReason) {
      if (!active) return;
      if (active.kind === "erase") cancelErase();
      else endInk(active.capture.abort(reason));
    },
    destroy() {
      if (active) {
        if (active.kind === "erase") cancelErase();
        else endInk(active.capture.abort("user-cancel"));
      }
      el.removeEventListener("pointerdown", onDown);
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", onUp);
      el.removeEventListener("pointercancel", onCancel);
      el.removeEventListener("lostpointercapture", onCancel as EventListener);
    },
  };
}

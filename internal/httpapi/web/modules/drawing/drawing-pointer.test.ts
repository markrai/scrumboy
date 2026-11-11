// @vitest-environment happy-dom
import { describe, expect, it } from "vitest";
import { freezeWallCapture } from "./drawing-math.js";
import { bindDrawingPointer } from "./drawing-pointer.js";
import type { StrokeOutcome, StrokeTool } from "./drawing-types.js";

function pe(type: string, init: { pointerId: number; pointerType: string; x: number; y: number; pressure?: number; button?: number }): PointerEvent {
  const ev = new (globalThis as any).PointerEvent(type, {
    pointerId: init.pointerId,
    pointerType: init.pointerType,
    clientX: init.x,
    clientY: init.y,
    pressure: init.pressure ?? 0.5,
    button: init.button ?? 0,
    bubbles: true,
    cancelable: true,
  });
  return ev as PointerEvent;
}

function setup(tool: StrokeTool = "pen", canStart = true) {
  const el = document.createElement("div");
  document.body.appendChild(el);
  const outcomes: StrokeOutcome[] = [];
  const erased: Array<[number, number]> = [];
  const activity: boolean[] = [];
  const eraseEvents: string[] = [];
  let rejected = 0;
  const handle = bindDrawingPointer({
    element: el,
    getTool: () => tool,
    getStyle: () => ({ tool: "pen", color: "#000000", cssSize: 4, opacity: 1 }),
    freeze: () => freezeWallCapture({ viewportLeft: 0, viewportTop: 0, panX: 0, panY: 0, zoom: 2 }),
    canStartStroke: () => canStart,
    newStrokeId: () => "s_1",
    onPreview: () => {},
    onOutcome: (o) => outcomes.push(o),
    onErase: (x, y) => erased.push([x, y]),
    onEraseEnd: () => eraseEvents.push("end"),
    onEraseCancel: () => eraseEvents.push("cancel"),
    onActiveChange: (a) => activity.push(a),
    onRejected: () => {
      rejected += 1;
    },
  });
  return { el, outcomes, erased, activity, eraseEvents, handle, rejected: () => rejected };
}

describe("drawing pointer state machine", () => {
  it("finishes on pointerup with world-converted points and size", () => {
    const { el, outcomes, activity } = setup();
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointermove", { pointerId: 1, pointerType: "mouse", x: 30, y: 10 }));
    el.dispatchEvent(pe("pointerup", { pointerId: 1, pointerType: "mouse", x: 50, y: 10 }));
    expect(outcomes).toHaveLength(1);
    const o = outcomes[0];
    expect(o.status).toBe("finished");
    if (o.status !== "finished") return;
    expect(o.stroke.size).toBe(2);
    expect(o.stroke.points[0]).toEqual([5, 5, 0.5]);
    expect(o.stroke.points[o.stroke.points.length - 1]).toEqual([25, 5, 0.5]);
    expect(activity).toEqual([true, false]);
  });

  it("aborts and discards on native pointercancel", () => {
    const { el, outcomes } = setup();
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "touch", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointercancel", { pointerId: 1, pointerType: "touch", x: 10, y: 10 }));
    expect(outcomes).toEqual([{ status: "aborted", reason: "native-cancel" }]);
  });

  it("aborts a touch stroke when a second touch arrives", () => {
    const { el, outcomes, handle } = setup();
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "touch", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointerdown", { pointerId: 2, pointerType: "touch", x: 90, y: 90 }));
    expect(outcomes).toEqual([{ status: "aborted", reason: "second-pointer" }]);
    expect(handle.isActive()).toBe(false);
  });

  it("ignores incidental touch during a pen stroke and preserves the pen stroke", () => {
    const { el, outcomes } = setup();
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "pen", x: 10, y: 10, pressure: 0.3 }));
    el.dispatchEvent(pe("pointerdown", { pointerId: 2, pointerType: "touch", x: 90, y: 90 }));
    el.dispatchEvent(pe("pointermove", { pointerId: 2, pointerType: "touch", x: 95, y: 95 }));
    el.dispatchEvent(pe("pointerup", { pointerId: 2, pointerType: "touch", x: 95, y: 95 }));
    expect(outcomes).toHaveLength(0);
    el.dispatchEvent(pe("pointerup", { pointerId: 1, pointerType: "pen", x: 40, y: 10, pressure: 0.9 }));
    expect(outcomes).toHaveLength(1);
    const o = outcomes[0];
    expect(o.status === "finished" && o.stroke.points.map((p) => p[2])).toEqual([0.3, 0.9]);
  });

  it("finishes the captured segment when the host reports a transform change", () => {
    const { el, outcomes, handle } = setup();
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointermove", { pointerId: 1, pointerType: "mouse", x: 30, y: 10 }));
    handle.finishActive();
    expect(outcomes[0].status).toBe("finished");
    el.dispatchEvent(pe("pointermove", { pointerId: 1, pointerType: "mouse", x: 60, y: 10 }));
    expect(outcomes).toHaveLength(1);
  });

  it("rejects new strokes before capture when the document is at its ceiling", () => {
    const { el, outcomes, rejected, handle } = setup("pen", false);
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    expect(rejected()).toBe(1);
    expect(handle.isActive()).toBe(false);
    expect(outcomes).toHaveLength(0);
  });

  it("routes eraser gestures to hit testing without producing strokes", () => {
    const { el, outcomes, erased } = setup("eraser");
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointermove", { pointerId: 1, pointerType: "mouse", x: 20, y: 10 }));
    el.dispatchEvent(pe("pointerup", { pointerId: 1, pointerType: "mouse", x: 20, y: 10 }));
    expect(erased).toEqual([[5, 5], [10, 5]]);
    expect(outcomes).toHaveLength(0);
  });

  it("cancels, never completes, an eraser gesture on native pointercancel", () => {
    const { el, eraseEvents, activity } = setup("eraser");
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "touch", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointermove", { pointerId: 1, pointerType: "touch", x: 20, y: 10 }));
    el.dispatchEvent(pe("pointercancel", { pointerId: 1, pointerType: "touch", x: 20, y: 10 }));
    expect(eraseEvents).toEqual(["cancel"]);
    expect(activity).toEqual([true, false]);
  });

  it("cancels an eraser gesture on a second touch, explicit abort, and destroy", () => {
    const touch = setup("eraser");
    touch.el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "touch", x: 10, y: 10 }));
    touch.el.dispatchEvent(pe("pointerdown", { pointerId: 2, pointerType: "touch", x: 50, y: 50 }));
    expect(touch.eraseEvents).toEqual(["cancel"]);
    const aborted = setup("eraser");
    aborted.el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    aborted.handle.abortActive("user-cancel");
    expect(aborted.eraseEvents).toEqual(["cancel"]);
    const destroyed = setup("eraser");
    destroyed.el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    destroyed.handle.destroy();
    expect(destroyed.eraseEvents).toEqual(["cancel"]);
  });

  it("completes an eraser gesture on pointerup", () => {
    const { el, eraseEvents } = setup("eraser");
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    el.dispatchEvent(pe("pointerup", { pointerId: 1, pointerType: "mouse", x: 10, y: 10 }));
    expect(eraseEvents).toEqual(["end"]);
  });

  it("ignores non-primary mouse buttons", () => {
    const { el, handle } = setup();
    el.dispatchEvent(pe("pointerdown", { pointerId: 1, pointerType: "mouse", x: 10, y: 10, button: 1 }));
    expect(handle.isActive()).toBe(false);
  });
});

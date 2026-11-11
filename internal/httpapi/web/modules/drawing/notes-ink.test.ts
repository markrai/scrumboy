// @vitest-environment jsdom
import MarkdownIt from "markdown-it";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from "../platform/runtime.js";
import type { AcquiredServerResource, ServerTransport } from "../platform/server-transport.js";
import { mountNotesInk, type NotesInkController } from "./notes-ink.js";
import type { DrawingDocument } from "./drawing-types.js";

const flush = async () => {
  for (let i = 0; i < 10; i += 1) await Promise.resolve();
};

function pe(type: string, x: number, y: number, pointerType = "mouse"): PointerEvent {
  return new PointerEvent(type, { pointerId: 5, pointerType, clientX: x, clientY: y, button: 0, bubbles: true, cancelable: true });
}

const inkDoc = (width = 400): DrawingDocument => ({
  version: 1,
  width,
  height: 300,
  strokes: [{ id: "a", tool: "pen", color: "#e11d48", size: 3, opacity: 1, createdAt: 1, points: [[50, 250, 0.5], [150, 250, 0.5]] }],
});

beforeAll(async () => {
  const rects = () => Object.assign([], { item: () => null }) as unknown as DOMRectList;
  const rect = () => ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON() {} }) as DOMRect;
  Range.prototype.getClientRects = rects;
  Range.prototype.getBoundingClientRect = rect;
  (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
  await import("../../scripts/codemirror-vendor-entry.mjs");
});

describe("notes ink surface", () => {
  let parent: HTMLElement;
  let toggleHost: HTMLElement;
  let toolsHost: HTMLElement;
  let ink: NotesInkController;
  let editor: any;
  let changes: number;

  beforeEach(async () => {
    document.body.innerHTML = "";
    const transport: ServerTransport = {
      request: vi.fn(),
      openEventStream: vi.fn(),
      logout: vi.fn(async () => undefined),
      acquireResource: vi.fn(async (path: string): Promise<AcquiredServerResource> => ({ url: `blob:${path}`, release: () => undefined })),
    };
    installAppRuntime({ kind: "browser", transport: () => transport } as unknown as AppRuntime);
    toggleHost = document.createElement("div");
    toolsHost = document.createElement("div");
    parent = document.createElement("div");
    document.body.append(toggleHost, toolsHost, parent);
    const { createNotesEditor } = await import("../dialogs/todo-notes-editor.js");
    editor = createNotesEditor({ parent, maxLength: 20000 });
    editor.setValue("Line one\nLine two");
    await flush();
    Object.defineProperty(editor.scroller, "clientWidth", { configurable: true, value: 400 });
    Object.defineProperty(editor.scroller, "clientHeight", { configurable: true, value: 300 });
    changes = 0;
    ink = mountNotesInk({ scroller: editor.scroller, content: editor.content, toggleHost, toolsHost, onChange: () => (changes += 1) });
    ink.setEditable(true);
    ink.layout();
  });

  afterEach(() => {
    ink.destroy();
    editor.destroy();
    resetAppRuntimeForTests();
  });

  const svg = () => editor.scroller.querySelector("svg.notes-ink-layer") as SVGSVGElement;
  const paths = () => svg().querySelectorAll("g.drawing-strokes path");
  const toggle = () => toggleHost.querySelector<HTMLButtonElement>(".notes-ink-toggle")!;
  const scrollTo = (top: number) => {
    svg().getBoundingClientRect = () => ({ left: 10, top: 20 - top, width: 400, height: 600, right: 410, bottom: 620 - top, x: 10, y: 20 - top, toJSON() {} }) as DOMRect;
  };
  const draw = (x0: number, y0: number, x1: number, y1: number, end = "pointerup") => {
    svg().dispatchEvent(pe("pointerdown", x0, y0));
    svg().dispatchEvent(pe("pointermove", (x0 + x1) / 2, (y0 + y1) / 2));
    svg().dispatchEvent(pe(end, x1, y1));
  };

  it("mounts inside the CodeMirror scroller without adding a scroll container", () => {
    expect(svg().parentElement).toBe(editor.scroller);
    expect(editor.scroller.classList.contains("notes-ink-host")).toBe(true);
    expect(parent.querySelectorAll(".notes-ink-layer")).toHaveLength(1);
    expect(svg().getAttribute("width")).toBe("400");
    expect(Number(svg().getAttribute("height"))).toBeGreaterThanOrEqual(300);
  });

  it("does not capture input until drawing mode is enabled", () => {
    scrollTo(0);
    draw(60, 60, 160, 60);
    expect(paths()).toHaveLength(0);
    expect(svg().classList.contains("notes-ink-layer--drawing")).toBe(false);
  });

  it("records strokes in document coordinates that include the scroll offset", () => {
    toggle().click();
    expect(ink.isDrawing()).toBe(true);
    expect(toolsHost.querySelector<HTMLElement>(".notes-ink-tools")!.hidden).toBe(false);
    scrollTo(200);
    draw(60, 30, 160, 30);
    const doc = ink.getDocument()!;
    expect(doc.strokes).toHaveLength(1);
    expect(doc.strokes[0].points[0]).toEqual([50, 210, 0.5]);
    expect(doc.width).toBe(400);
    expect(doc.height).toBe(211);
    expect(changes).toBe(1);
  });

  it("keeps ink visible after leaving drawing mode and stops intercepting input", () => {
    toggle().click();
    scrollTo(0);
    draw(60, 60, 160, 60);
    toggle().click();
    expect(ink.isDrawing()).toBe(false);
    expect(svg().classList.contains("notes-ink-layer--drawing")).toBe(false);
    expect(paths()).toHaveLength(1);
    draw(60, 90, 160, 90);
    expect(ink.getDocument()!.strokes).toHaveLength(1);
  });

  it("reports unchanged, changed and cleared save values against the loaded baseline", () => {
    ink.setDocument(inkDoc());
    expect(ink.saveValue()).toBeUndefined();
    toggle().click();
    scrollTo(0);
    draw(60, 60, 160, 60);
    expect(ink.saveValue()?.strokes).toHaveLength(2);
    toolsHost.querySelector<HTMLButtonElement>(".notes-ink-tools__clear")!.click();
    expect(ink.saveValue()).toBeNull();
    toolsHost.querySelector<HTMLButtonElement>(".notes-ink-tools__undo")!.click();
    expect(ink.saveValue()?.strokes).toHaveLength(2);
    ink.setDocument(null);
    expect(ink.saveValue()).toBeUndefined();
    expect(paths()).toHaveLength(0);
  });

  it("does not commit an erase interrupted by native pointer cancellation", () => {
    ink.setDocument(inkDoc());
    toggle().click();
    toolsHost.querySelector<HTMLButtonElement>(".notes-ink-tools__tool--eraser")!.click();
    scrollTo(0);
    svg().dispatchEvent(pe("pointerdown", 110, 270, "touch"));
    expect(paths()).toHaveLength(0);
    svg().dispatchEvent(pe("pointercancel", 110, 270, "touch"));
    expect(paths()).toHaveLength(1);
    expect(ink.saveValue()).toBeUndefined();
  });

  it("erases on a completed gesture with one undo step", () => {
    ink.setDocument(inkDoc());
    toggle().click();
    toolsHost.querySelector<HTMLButtonElement>(".notes-ink-tools__tool--eraser")!.click();
    scrollTo(0);
    svg().dispatchEvent(pe("pointerdown", 110, 270));
    svg().dispatchEvent(pe("pointerup", 110, 270));
    expect(ink.saveValue()).toBeNull();
    toolsHost.querySelector<HTMLButtonElement>(".notes-ink-tools__undo")!.click();
    expect(ink.saveValue()).toBeUndefined();
    expect(paths()).toHaveLength(1);
  });

  it("is display-only when read-only or while hydration is locked", () => {
    ink.setDocument(inkDoc());
    ink.setEditable(false);
    expect(toggle().hidden).toBe(true);
    ink.setDrawing(true);
    expect(ink.isDrawing()).toBe(false);
    expect(paths()).toHaveLength(1);
    ink.setEditable(true);
    ink.setLocked(true);
    expect(toggle().disabled).toBe(true);
    ink.setDrawing(true);
    expect(ink.isDrawing()).toBe(false);
  });

  it("states the reflow limitation when the notes width differs from the drawing width", () => {
    ink.setDocument(inkDoc(800));
    const notice = toolsHost.querySelector<HTMLElement>(".notes-ink-notice")!;
    expect(notice.hidden).toBe(false);
    expect(notice.textContent).toContain("original position");
    ink.setDocument(inkDoc(405));
    expect(notice.hidden).toBe(true);
  });

  it("survives a note reload inside the same editor", async () => {
    ink.setDocument(inkDoc());
    editor.setValue("A completely different note");
    await flush();
    ink.layout();
    expect(svg().parentElement).toBe(editor.scroller);
    expect(paths()).toHaveLength(1);
  });

  it("swallows editor pointer handling while drawing so text is not selected", () => {
    const seen = vi.fn();
    editor.scroller.parentElement.addEventListener("mousedown", seen);
    toggle().click();
    svg().dispatchEvent(new MouseEvent("mousedown", { bubbles: true, cancelable: true }));
    expect(seen).not.toHaveBeenCalled();
    toggle().click();
    svg().dispatchEvent(new MouseEvent("mousedown", { bubbles: true, cancelable: true }));
    expect(seen).toHaveBeenCalledTimes(1);
  });
});

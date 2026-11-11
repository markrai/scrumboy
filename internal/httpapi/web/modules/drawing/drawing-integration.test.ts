// @vitest-environment happy-dom
import MarkdownIt from "markdown-it";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { findNoteImageRefs, formatImageAlt, formatMediaMarkdown, parseImageAlt } from "../note-images.js";
import { DrawingBudgetError, DrawingSession } from "./drawing-session.js";
import type { DrawingDocument } from "./drawing-types.js";
import { renderMarkdownPreviewInto } from "../markdown-preview.js";
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from "../platform/runtime.js";

const U = "11111111-1111-4111-8111-111111111111";
const A = "drw_aaaaaaaa";
const md = new MarkdownIt("default", { html: false, breaks: true, linkify: false });

const doc = (strokes = 1, pad = 0): DrawingDocument => ({
  version: 1,
  width: 400,
  height: 300,
  strokes: Array.from({ length: strokes }, (_, i) => ({
    id: `s${i}`, tool: "pen" as const, color: "#112233", size: 4, opacity: 1, createdAt: 1,
    points: Array.from({ length: 2 + pad }, () => [10, 10, 0.5] as [number, number, number]),
  })),
});

describe("image alt metadata grammar", () => {
  it.each([
    ["shot|draw:drw_aaaaaaaa|480", { alt: "shot", width: 480, drawingId: A }],
    ["shot|draw:drw_aaaaaaaa", { alt: "shot", width: null, drawingId: A }],
    ["shot|480", { alt: "shot", width: 480, drawingId: null }],
    ["a|b|draw:drw_aaaaaaaa|1600", { alt: "a|b", width: 1600, drawingId: A }],
    ["shot|draw:bad", { alt: "shot|draw:bad", width: null, drawingId: null }],
    ["shot|draw:drw_aaaaaaaa|20", { alt: "shot|draw:drw_aaaaaaaa|20", width: null, drawingId: null }],
    ["shot|480|draw:drw_aaaaaaaa", { alt: "shot|480", width: null, drawingId: A }],
  ])("parses %s", (alt, want) => {
    expect(parseImageAlt(alt)).toEqual(want);
  });

  it("keeps the width terminal so legacy width parsing still matches", () => {
    const alt = formatImageAlt("shot", 480, A);
    expect(alt).toBe("shot|draw:drw_aaaaaaaa|480");
    expect(/\|([1-9][0-9]{1,3})$/.exec(alt)?.[1]).toBe("480");
    expect(parseImageAlt(alt)).toEqual({ alt: "shot", width: 480, drawingId: A });
  });

  it("finds annotated and standalone references and rejects mismatched standalone tags", () => {
    const text = `![a|draw:${A}|300](/api/assets/${U})\n\n![s|draw:${A}](/api/board/b/drawings/${A})\n\n![bad|draw:drw_zzzzzzzz](/api/board/b/drawings/${A})`;
    const refs = findNoteImageRefs(text, md.parse(text, {}) as any);
    expect(refs.map((r) => [r.kind, r.drawingId, r.width])).toEqual([["asset", A, 300], ["drawing", A, null]]);
    expect(formatMediaMarkdown(refs[1], "drw_bbbbbbbb", "new")).toBe("![s|draw:drw_bbbbbbbb](/api/board/new/drawings/drw_bbbbbbbb)");
  });
});

describe("dialog drawing session", () => {
  it("submits only changed drawings that the body still references", async () => {
    const s = new DrawingSession();
    await s.hydrate("/x", async () => ({ drawings: { [A]: doc(1), drw_cccccccc: { version: 9 } } }));
    expect(s.has(A)).toBe(true);
    expect(s.has("drw_cccccccc")).toBe(true);
    expect(s.get("drw_cccccccc")).toBeUndefined();
    expect(s.savePayload([A])).toBeNull();
    s.setDraft(A, doc(2));
    s.setDraft("drw_dddddddd", doc(1));
    const payload = s.savePayload([A]);
    expect(Object.keys(payload ?? {})).toEqual([A]);
    expect(s.revision).toBe(2);
    s.clear();
    expect(s.has(A)).toBe(false);
    expect(s.revision).toBe(0);
  });

  it("records hydration failure so saves can refuse to strip annotations", async () => {
    const s = new DrawingSession();
    await s.hydrate("/x", async () => {
      throw new Error("offline");
    });
    expect(s.hydrationFailed).toBe(true);
  });

  it("enforces the per-save submitted byte budget", () => {
    const s = new DrawingSession();
    const ids: string[] = [];
    for (let i = 0; i < 4; i += 1) {
      const id = `drw_big0000${i}`;
      ids.push(id);
      s.setDraft(id, doc(50, 500));
    }
    expect(() => s.savePayload(ids)).toThrow(DrawingBudgetError);
  });
});

describe("markdown preview drawings", () => {
  beforeAll(async () => {
    (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
    (window as any).DOMPurify = { sanitize: (html: string) => html };
  });

  afterEach(() => resetAppRuntimeForTests());

  function runtime() {
    installAppRuntime({
      kind: "browser",
      transport: () => ({
        request: vi.fn(),
        openEventStream: vi.fn(),
        logout: vi.fn(),
        acquireResource: vi.fn(async (path: string) => ({ url: `blob:${path}`, release: () => undefined })),
      }),
    } as unknown as AppRuntime);
  }

  it("overlays annotated images and renders standalone drawings from the resolver", async () => {
    runtime();
    const container = document.createElement("div");
    document.body.append(container);
    const resolve = vi.fn(async () => doc(3));
    await renderMarkdownPreviewInto(container, `![a|draw:${A}|300](/api/assets/${U})\n\n![s|draw:${A}](/api/board/b/drawings/${A})`, { inlineImagesEnabled: true, resolveDrawing: resolve });
    const standalone = container.querySelector<HTMLElement>(".todo-notes-drawing")!;
    expect(standalone.querySelectorAll("path.drawing-stroke")).toHaveLength(3);
    const annotated = container.querySelector<HTMLElement>(".todo-notes-image[data-drawing-id]:not(.todo-notes-drawing)")!;
    expect(annotated.querySelector("svg.todo-notes-image__annotation-layer path")).not.toBeNull();
    expect(annotated.querySelector("img")?.style.width).toBe("300px");
  });

  it("degrades to plain images and inert text without a resolver", async () => {
    runtime();
    const container = document.createElement("div");
    document.body.append(container);
    await renderMarkdownPreviewInto(container, `![a|draw:${A}|300](/api/assets/${U})\n\n![s|draw:${A}](/api/board/b/drawings/${A})`, { inlineImagesEnabled: true });
    expect(container.querySelector("svg")).toBeNull();
    expect(container.querySelector("img")?.alt).toBe("a");
    expect(container.textContent).toContain(`/api/board/b/drawings/${A}`);
  });
});

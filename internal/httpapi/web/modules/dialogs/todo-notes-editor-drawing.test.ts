// @vitest-environment jsdom
import MarkdownIt from "markdown-it";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from "../platform/runtime.js";
import type { AcquiredServerResource, ServerTransport } from "../platform/server-transport.js";
import type { DrawingDocument } from "../drawing/drawing-types.js";
import type { NotesDrawingHost } from "./todo-notes-editor.js";

const U1 = "11111111-1111-4111-8111-111111111111";
const A = "drw_aaaaaaaa";
const B = "drw_bbbbbbbb";
const SLUG = "board-1";
const annotated = (id: string, width = "") => `![shot|draw:${id}${width}](/api/assets/${U1})`;
const standalone = (id: string) => `![Sketch|draw:${id}](/api/board/${SLUG}/drawings/${id})`;

const doc = (strokes = 1): DrawingDocument => ({
  version: 1,
  width: 400,
  height: 300,
  strokes: Array.from({ length: strokes }, (_, i) => ({
    id: `s${i}`, tool: "pen" as const, color: "#e11d48", size: 4, opacity: 1, createdAt: 1, points: [[10, 10, 0.5], [50, 60, 0.5]] as [number, number, number][],
  })),
});

function installTransport(): void {
  const transport: ServerTransport = {
    request: vi.fn(),
    openEventStream: vi.fn(),
    logout: vi.fn(async () => undefined),
    acquireResource: vi.fn(async (path: string): Promise<AcquiredServerResource> => ({ url: `blob:native${path}`, release: () => undefined })),
  };
  installAppRuntime({ kind: "browser", transport: () => transport } as unknown as AppRuntime);
}

const flush = async () => {
  for (let i = 0; i < 10; i += 1) await Promise.resolve();
};

type FakeHost = NotesDrawingHost & {
  docs: Map<string, DrawingDocument>;
  clipboard: Map<string, DrawingDocument>;
  notices: string[];
};

function fakeHost(): FakeHost {
  const listeners = new Set<(id: string | null) => void>();
  let counter = 0;
  const host: FakeHost = {
    docs: new Map(),
    clipboard: new Map(),
    notices: [],
    canEdit: () => true,
    slug: () => SLUG,
    get: (id) => host.docs.get(id),
    isKnown: (id) => host.docs.has(id),
    subscribe: (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    adoptPasted: (id) => {
      const d = host.clipboard.get(id);
      if (!d) return null;
      counter += 1;
      const fresh = `drw_fork${String(counter).padStart(4, "0")}`;
      host.docs.set(fresh, d);
      return fresh;
    },
    rememberCopied: (ids) => {
      for (const id of ids) {
        const d = host.docs.get(id);
        if (d) host.clipboard.set(id, d);
      }
    },
    notify: (m) => {
      host.notices.push(m);
    },
  };
  return host;
}

beforeAll(async () => {
  const rects = () => Object.assign([], { item: () => null }) as unknown as DOMRectList;
  const rect = () => ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON() {} }) as DOMRect;
  Range.prototype.getClientRects = rects;
  Range.prototype.getBoundingClientRect = rect;
  (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
  await import("../../scripts/codemirror-vendor-entry.mjs");
});

describe("visual notes editor drawings", () => {
  let parent: HTMLElement;

  beforeEach(() => {
    document.body.innerHTML = "";
    parent = document.createElement("div");
    document.body.append(parent);
    installTransport();
  });

  afterEach(() => {
    resetAppRuntimeForTests();
  });

  async function editor(value: string, host: FakeHost | null) {
    const { createNotesEditor } = await import("./todo-notes-editor.js");
    const ed = createNotesEditor({ parent, maxLength: 20000 });
    if (host) ed.setDrawingHost(host);
    ed.setValue(value);
    await flush();
    return ed;
  }

  const view = () => (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(parent.querySelector(".cm-editor"));
  const widgets = () => Array.from(parent.querySelectorAll<HTMLElement>(".todo-notes-image[data-path]"));
  const key = (k: string) => {
    const content = parent.querySelector(".cm-content") as HTMLElement;
    content.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
  };

  it("keeps rendering stored image annotations in intrinsic space without any drawing control", async () => {
    const host = fakeHost();
    host.docs.set(A, doc(2));
    const ed = await editor(`x\n${annotated(A, "|480")}\ny`, host);
    const w = widgets()[0];
    expect(w.dataset.drawingId).toBe(A);
    const svg = w.querySelector("svg.todo-notes-image__annotation-layer");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 400 300");
    expect(svg?.querySelectorAll("path.drawing-stroke")).toHaveLength(2);
    expect(w.querySelector(".todo-notes-image__btn--draw")).toBeNull();
    expect(w.querySelector("button")).toBeNull();
    expect(ed.drawingIds()).toEqual([A]);
  });

  it("renders annotated images as plain images when drawing is disabled", async () => {
    await editor(annotated(A, "|480"), null);
    const w = widgets()[0];
    expect(w.dataset.drawingId).toBeUndefined();
    expect(w.querySelector("svg")).toBeNull();
    expect(w.querySelector(".todo-notes-image__btn--draw")).toBeNull();
    expect(w.querySelector("img")?.style.width).toBe("480px");
  });

  it("keeps the drawing tag when resizing", async () => {
    const host = fakeHost();
    host.docs.set(A, doc());
    const ed = await editor(`x ${annotated(A)} y`, host);
    widgets()[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    key("+");
    expect(ed.getValue()).toMatch(new RegExp(`^x !\\[shot\\|draw:${A}\\|\\d+\\]\\(/api/assets/${U1}\\) y$`));
    key("0");
    expect(ed.getValue()).toBe(`x ${annotated(A)} y`);
  });

  it("offers no editing entry point on stored standalone drawings and keeps their Markdown untouched", async () => {
    const host = fakeHost();
    host.docs.set(A, doc(2));
    const body = `Top
${standalone(A)}
`;
    const ed = await editor(body, host);
    const w = widgets().find((el) => el.dataset.standalone === "1")!;
    expect(w.querySelector("svg.todo-notes-drawing__svg path")).not.toBeNull();
    expect(w.querySelector("button")).toBeNull();
    w.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    await flush();
    expect(ed.getValue()).toBe(body);
    expect(document.querySelector(".drawing-modal")).toBeNull();
  });

  it("forks pasted annotations from the clipboard cache and strips unrecoverable ones", async () => {
    const host = fakeHost();
    const ed = await editor("", host);
    host.clipboard.set(A, doc(3));
    const v = view();
    v.dispatch({ changes: { from: 0, insert: `${annotated(A, "|300")}\n${annotated(B)}\n${standalone(B)}` }, userEvent: "input.paste" });
    await flush();
    const value = ed.getValue();
    expect(value).not.toContain(A);
    expect(value).toMatch(/!\[shot\|draw:drw_fork0001\|300\]/);
    expect(value).toContain(`![shot](/api/assets/${U1})`);
    expect(value.endsWith("\nSketch")).toBe(true);
    expect(host.notices).toHaveLength(1);
    expect(host.docs.get("drw_fork0001")?.strokes).toHaveLength(3);
  });

  it("leaves pastes of known drawings untouched so same-note copies share until edited", async () => {
    const host = fakeHost();
    host.docs.set(A, doc());
    const ed = await editor(annotated(A), host);
    const v = view();
    v.dispatch({ changes: { from: v.state.doc.length, insert: `\n${annotated(A)}` }, userEvent: "input.paste" });
    expect(ed.getValue()).toBe(`${annotated(A)}\n${annotated(A)}`);
  });

  it("strips unresolved references before save and converts unresolved standalone drawings to text", async () => {
    const host = fakeHost();
    host.docs.set(A, doc());
    const ed = await editor(`${annotated(A, "|200")}\n\n${annotated(B, "|300")}\n\n${standalone(B)}`, host);
    expect(ed.stripDrawingRefs((id) => !host.isKnown(id))).toBe(2);
    expect(ed.getValue()).toBe(`${annotated(A, "|200")}\n\n![shot|300](/api/assets/${U1})\n\nSketch`);
  });

  it("renders standalone drawings and inserts new ones in canonical form", async () => {
    const host = fakeHost();
    host.docs.set(A, doc(2));
    const ed = await editor(`Top\n${standalone(A)}\n`, host);
    const w = widgets().find((el) => el.dataset.standalone === "1")!;
    expect(w.querySelector("svg.todo-notes-drawing__svg")?.querySelectorAll("path")).toHaveLength(2);
    const v = view();
    v.dispatch({ selection: (window as any).ScrumboyCodeMirror.EditorSelection.cursor(3) });
    host.docs.set(B, doc());
    expect(ed.insertStandaloneDrawing(B, "Drawing")).toBe(true);
    expect(ed.getValue()).toBe(`Top\n![Drawing|draw:${B}](/api/board/${SLUG}/drawings/${B})\n${standalone(A)}\n`);
    expect(ed.drawingIds()).toEqual([B, A]);
  });

  it("refreshes overlays when the host reports a changed drawing", async () => {
    const host = fakeHost();
    host.docs.set(A, doc(1));
    let notify: ((id: string | null) => void) | null = null;
    host.subscribe = (l) => {
      notify = l;
      return () => undefined;
    };
    await editor(annotated(A), host);
    host.docs.set(A, doc(4));
    notify!(A);
    expect(widgets()[0].querySelectorAll("svg path.drawing-stroke")).toHaveLength(4);
  });
});

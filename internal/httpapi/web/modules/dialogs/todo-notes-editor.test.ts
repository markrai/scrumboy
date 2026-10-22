// @vitest-environment jsdom
import MarkdownIt from "markdown-it";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from "../platform/runtime.js";
import type { AcquiredServerResource, ServerTransport } from "../platform/server-transport.js";

const U1 = "11111111-1111-4111-8111-111111111111";
const U2 = "22222222-2222-4222-9222-222222222222";
const img = (uuid: string, alt = "shot") => `![${alt}](/api/assets/${uuid})`;

type Fake = { acquired: string[]; released: string[]; fail: Set<string> };

function installTransport(): Fake {
  const fake: Fake = { acquired: [], released: [], fail: new Set() };
  const transport: ServerTransport = {
    request: vi.fn(),
    openEventStream: vi.fn(),
    logout: vi.fn(async () => undefined),
    acquireResource: vi.fn(async (path: string): Promise<AcquiredServerResource> => {
      fake.acquired.push(path);
      if (fake.fail.has(path)) throw new Error("gone");
      let released = false;
      return {
        url: `blob:native${path}`,
        release: () => {
          if (released) return;
          released = true;
          fake.released.push(path);
        },
      };
    }),
  };
  installAppRuntime({ kind: "browser", transport: () => transport } as unknown as AppRuntime);
  return fake;
}

const flush = async () => {
  for (let i = 0; i < 10; i += 1) await Promise.resolve();
};

beforeAll(async () => {
  // jsdom has no layout; CodeMirror only needs these to exist.
  const rects = () => Object.assign([], { item: () => null }) as unknown as DOMRectList;
  const rect = () => ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON() {} }) as DOMRect;
  Range.prototype.getClientRects = rects;
  Range.prototype.getBoundingClientRect = rect;
  (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
  // The same entry the vendor bundle is built from.
  await import("../../scripts/codemirror-vendor-entry.mjs");
});

describe("visual notes editor", () => {
  let fake: Fake;
  let parent: HTMLElement;
  let announced: string[];

  beforeEach(() => {
    document.body.innerHTML = "";
    parent = document.createElement("div");
    document.body.append(parent);
    fake = installTransport();
    announced = [];
  });

  afterEach(() => {
    resetAppRuntimeForTests();
  });

  async function editor(value: string, maxLength = 20000) {
    const { createNotesEditor } = await import("./todo-notes-editor.js");
    const ed = createNotesEditor({ parent, maxLength, announce: (m) => announced.push(m) });
    ed.setValue(value);
    await flush();
    return ed;
  }

  const widgets = () => Array.from(parent.querySelectorAll<HTMLElement>(".todo-notes-image[data-path]"));
  const view = () => (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(parent.querySelector(".cm-editor"));
  const key = (k: string) => {
    const content = parent.querySelector(".cm-content") as HTMLElement;
    content.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
  };

  it("shows image references as images and leaves code as text", async () => {
    await editor(`Intro\n\n${img(U1)}\n\n\`${img(U2)}\`\n\n\`\`\`\n${img(U2)}\n\`\`\``);
    expect(widgets().map((w) => w.dataset.path)).toEqual([`/api/assets/${U1}`]);
    expect(widgets()[0].querySelector("img")?.getAttribute("src")).toBe(`blob:native/api/assets/${U1}`);
    expect(parent.querySelector(".cm-content")?.textContent).toContain(img(U2));
  });

  it("returns the loaded text byte-for-byte until it is edited", async () => {
    for (const body of ["a\r\nb\r\n", "mixed\r\nline\nend\r", "😀 Ünïcode\n```mermaid\ngraph TD;A-->B\n```", `\t${img(U1)}  \n`]) {
      const ed = await editor(body);
      expect(ed.getValue()).toBe(body);
      ed.destroy();
    }
  });

  it("keeps CRLF line endings after an edit of a CRLF note", async () => {
    const ed = await editor(`one\r\n${img(U1)}\r\ntwo`);
    // Type at the end through a CodeMirror transaction (what keyboard input produces).
    const v = view();
    v.dispatch({ changes: { from: v.state.doc.length, insert: "\nthree" } });
    expect(ed.getValue()).toBe(`one\r\n${img(U1)}\r\ntwo\r\nthree`);
  });

  it("selects an image on click, deletes only its reference, and undo/redo restore it", async () => {
    const body = `before\n${img(U1)}\nafter`;
    const ed = await editor(body);
    widgets()[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(widgets()[0].classList.contains("is-selected")).toBe(true);
    expect(announced.at(-1)).toContain("Image selected: shot");
    key("Backspace");
    expect(ed.getValue()).toBe("before\n\nafter");
    expect(widgets()).toHaveLength(0);
    expect(fake.released).toEqual([]); // the file is kept (grace period), the asset is never deleted here
    const { undo, redo } = (window as any).ScrumboyCodeMirror;
    const v = view();
    undo(v);
    expect(ed.getValue()).toBe(body);
    redo(v);
    expect(ed.getValue()).toBe("before\n\nafter");
  });

  it("resizes from the keyboard by rewriting only the width suffix", async () => {
    const body = `x ${img(U1)} y`;
    const ed = await editor(body);
    widgets()[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    key("+");
    const sized = ed.getValue();
    expect(sized).toMatch(new RegExp(`^x !\\[shot\\|\\d+\\]\\(/api/assets/${U1}\\) y$`));
    expect(widgets()[0].classList.contains("todo-notes-image--sized")).toBe(true);
    expect(widgets()[0].classList.contains("is-selected")).toBe(true);
    key("0");
    expect(ed.getValue()).toBe(body);
    key("0");
    expect(ed.getValue()).toBe(body); // "0" never replaces a selected image
    key("Escape");
    expect(widgets()[0].classList.contains("is-selected")).toBe(false);
    expect(view().state.selection.main.head).toBe(2 + img(U1).length);
    // The caret is right after the image: Backspace removes it as one unit, never half of it.
    key("Backspace");
    expect(ed.getValue()).toBe("x  y");
  });

  it("puts uploaded images on their own lines", async () => {
    const ed = await editor("ab\ncd");
    const v = view();
    const { EditorSelection } = (window as any).ScrumboyCodeMirror;
    for (const [pos, want] of [
      [1, `a\n${img(U1)}\nb\ncd`],
      [2, `ab\n${img(U1)}\ncd`],
      [3, `ab\n${img(U1)}\ncd`],
      [5, `ab\ncd\n${img(U1)}\n`],
    ] as const) {
      ed.setValue("ab\ncd");
      const fresh = view();
      fresh.dispatch({ selection: EditorSelection.cursor(pos) });
      ed.uploadTarget.begin("u", { batch: "b", index: 0, length: 60 });
      ed.uploadTarget.finish("u", img(U1));
      expect(ed.getValue()).toBe(want);
    }
    expect(v).toBeTruthy();
  });

  it("inserts finished uploads in paste order as single undoable edits", async () => {
    const ed = await editor("Text");
    const v = view();
    const { EditorSelection, undo } = (window as any).ScrumboyCodeMirror;
    v.dispatch({ selection: EditorSelection.cursor(4) });
    const t = ed.uploadTarget;
    expect(t.begin("a", { batch: "b1", index: 0, length: 60 })).toBe(true);
    expect(t.begin("b", { batch: "b1", index: 1, length: 60 })).toBe(true);
    expect(t.begin("c", { batch: "b1", index: 2, length: 60 })).toBe(true);
    expect(parent.querySelectorAll(".todo-notes-image--uploading")).toHaveLength(3);
    expect(ed.getValue()).toBe("Text"); // reservations are never written into the note
    t.finish("c", img(U2, "c"));
    t.finish("a", img(U1, "a"));
    t.finish("b", null);
    expect(ed.getValue()).toBe(`Text\n${img(U1, "a")}\n${img(U2, "c")}\n`);
    expect(parent.querySelectorAll(".todo-notes-image--uploading")).toHaveLength(0);
    undo(v);
    expect(ed.getValue()).toBe(`Text\n${img(U2, "c")}\n`);
  });

  describe("uploads with selected text", () => {
    const cm = () => (window as any).ScrumboyCodeMirror;
    const reserve = (ed: Awaited<ReturnType<typeof editor>>, id: string, index = 0, batch = "b") =>
      expect(ed.uploadTarget.begin(id, { batch, index, length: 60 })).toBe(true);

    it("keeps the selection until the image arrives, then replaces it in one undo step", async () => {
      const ed = await editor("keep REPLACE keep");
      const v = view();
      v.dispatch({ selection: cm().EditorSelection.range(5, 12) });
      reserve(ed, "a");
      expect(ed.getValue()).toBe("keep REPLACE keep");
      expect(cm().undo(v)).toBe(false); // starting an upload adds no undo step
      ed.uploadTarget.finish("a", img(U1));
      expect(ed.getValue()).toBe(`keep \n${img(U1)}\n keep`);
      expect(v.state.selection.main.head).toBe(`keep \n${img(U1)}\n`.length);
      cm().undo(v);
      expect(ed.getValue()).toBe("keep REPLACE keep");
      expect(v.state.sliceDoc(v.state.selection.main.from, v.state.selection.main.to)).toBe("REPLACE");
      cm().redo(v);
      expect(ed.getValue()).toBe(`keep \n${img(U1)}\n keep`);
    });

    it("leaves the selected text and the undo history alone when the upload fails or is cancelled", async () => {
      const ed = await editor("keep REPLACE keep");
      const v = view();
      v.dispatch({ selection: cm().EditorSelection.range(5, 12) });
      reserve(ed, "a");
      reserve(ed, "b", 1);
      ed.uploadTarget.finish("a", null); // network failure
      ed.uploadTarget.finish("b", null); // cancelled (abort finishes with null too)
      expect(ed.getValue()).toBe("keep REPLACE keep");
      expect(parent.querySelectorAll(".todo-notes-image--uploading")).toHaveLength(0);
      expect(cm().undo(v)).toBe(false);
      // The selection is still the user's: a later upload replaces it as usual.
      reserve(ed, "c", 0, "b2");
      ed.uploadTarget.finish("c", img(U1));
      expect(ed.getValue()).toBe(`keep \n${img(U1)}\n keep`);
    });

    it("replaces the selection once per batch, keeping paste order when uploads finish out of order", async () => {
      const ed = await editor("A SEL B");
      const v = view();
      v.dispatch({ selection: cm().EditorSelection.range(2, 5) });
      reserve(ed, "a", 0);
      reserve(ed, "b", 1);
      reserve(ed, "c", 2);
      ed.uploadTarget.finish("b", img(U2, "b"));
      ed.uploadTarget.finish("a", img(U1, "a"));
      ed.uploadTarget.finish("c", null);
      expect(ed.getValue()).toBe(`A \n${img(U1, "a")}\n${img(U2, "b")}\n B`);
      cm().undo(v);
      expect(ed.getValue()).toBe(`A \n${img(U2, "b")}\n B`);
      cm().undo(v);
      expect(ed.getValue()).toBe("A SEL B");
    });

    it("keeps the user's edits made while uploading", async () => {
      // Text typed before the selection moves it; the image still replaces it.
      let ed = await editor("one SEL two");
      let v = view();
      v.dispatch({ selection: cm().EditorSelection.range(4, 7) });
      reserve(ed, "a");
      v.dispatch({ changes: { from: 0, insert: "zero " }, selection: cm().EditorSelection.cursor(5) });
      ed.uploadTarget.finish("a", img(U1));
      expect(ed.getValue()).toBe(`zero one \n${img(U1)}\n two`);
      ed.destroy();

      // Typing and the image are separate undo steps, and redo restores the image.
      cm().undo(v);
      expect(ed.getValue()).toBe("zero one SEL two");
      cm().redo(v);
      expect(ed.getValue()).toBe(`zero one \n${img(U1)}\n two`);
      ed.destroy();

      ed = await editor("one SEL two");
      v = view();
      v.dispatch({ selection: cm().EditorSelection.range(4, 7) });
      reserve(ed, "a");
      [..."<< "].forEach((ch, i) =>
        v.dispatch({ changes: { from: i, insert: ch }, selection: cm().EditorSelection.cursor(i + 1), userEvent: "input.type" }),
      );
      ed.uploadTarget.finish("a", img(U1));
      cm().undo(v);
      expect(ed.getValue()).toBe("<< one SEL two");
      cm().redo(v);
      expect(ed.getValue()).toBe(`<< one \n${img(U1)}\n two`);
      ed.destroy();

      // Text edited inside the selection wins: the image is added and nothing is removed.
      ed = await editor("one SEL two");
      v = view();
      v.dispatch({ selection: cm().EditorSelection.range(4, 7) });
      reserve(ed, "a");
      v.dispatch({ changes: { from: 5, to: 6, insert: "ELECTE" } });
      ed.uploadTarget.finish("a", img(U1));
      expect(ed.getValue()).toBe(`one \n${img(U1)}\nSELECTEL two`);
      ed.destroy();

      // Deleted selection: the image goes where it was.
      ed = await editor("one SEL two");
      v = view();
      v.dispatch({ selection: cm().EditorSelection.range(4, 7) });
      reserve(ed, "a");
      v.dispatch({ changes: { from: 4, to: 7 } });
      ed.uploadTarget.finish("a", img(U1));
      expect(ed.getValue()).toBe(`one \n${img(U1)}\n two`);
    });
  });

  describe("mixed line endings", () => {
    const head = img(U1, "first");
    const mid = img(U2, "middle");
    const U3 = "33333333-3333-4333-8333-333333333333";
    const tail = img(U3, "last");
    // CRLF, LF and lone CR, with images at the start, in the middle and at the end, plus code,
    // Mermaid and Unicode that must survive untouched.
    const body =
      `${head}\r\nIntro 😀\nline\rcr\r\n\r\n${mid}\n\n` + "```mermaid\r\ngraph TD;A-->B\n```\r\r\n" + `end ${tail}`;

    async function run(edit: (v: any) => void) {
      const ed = await editor(body);
      edit(view());
      return ed.getValue();
    }

    const select = (_v: unknown, index: number) => {
      const w = widgets()[index];
      w.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      expect(w.classList.contains("is-selected")).toBe(true);
    };

    it("removes an image at the start, middle or end without touching other bytes", async () => {
      for (const [index, ref] of [[0, head], [1, mid], [2, tail]] as const) {
        const value = await run((v) => {
          select(v, index);
          key("Delete");
        });
        expect(value).toBe(body.replace(ref, ""));
        resetAndClear();
      }
    });

    it("resizes an image by rewriting only its width", async () => {
      for (const [index, ref, alt] of [[0, head, "first"], [1, mid, "middle"], [2, tail, "last"]] as const) {
        const value = await run((v) => {
          select(v, index);
          key("+");
        });
        const width = value.match(new RegExp(`!\\[${alt}\\|(\\d+)\\]`))?.[1];
        expect(width).toBeTruthy();
        expect(value).toBe(body.replace(ref, ref.replace(`[${alt}]`, `[${alt}|${width}]`)));
        resetAndClear();
      }
    });

    it("restores original bytes when an edit is undone after other edits", async () => {
      const value = await run((v) => {
        // Delete a stretch spanning CRLF, LF and CR breaks, edit elsewhere, then undo the deletion.
        v.dispatch({ changes: { from: head.length, to: head.length + 20 } });
        v.dispatch({ changes: { from: v.state.doc.length, insert: "!" } });
        const { undo } = cm();
        undo(v);
        undo(v);
        v.dispatch({ changes: { from: v.state.doc.length, insert: "?" } });
      });
      expect(value).toBe(`${body}?`);
    });

    it("gives typed line breaks the form of the line break ending their line", async () => {
      const value = await run((v) => {
        const lf = v.state.doc.toString();
        const afterIntro = lf.indexOf("Intro 😀") + "Intro 😀".length; // followed by LF
        const afterCr = lf.indexOf("cr") + 2; // "cr" ends with CRLF
        v.dispatch({ changes: [{ from: afterIntro, insert: "\nnew" }, { from: afterCr, insert: "\nalso" }] });
      });
      expect(value).toBe(body.replace("Intro 😀\n", "Intro 😀\nnew\n").replace("line\rcr\r\n", "line\rcr\r\nalso\r\n"));
    });

    it("always produces the edited text, for random edits of random mixed notes", async () => {
      const { applyEditsPreservingLineBreaks } = await import("./todo-notes-editor.js");
      const { EditorState } = cm();
      let seed = 12345;
      const rand = (n: number) => {
        seed = (seed * 1103515245 + 12345) % 2147483648;
        return Math.floor(seed / 65536) % n;
      };
      const pieces = ["a", "é", "😀", " ", "\r\n", "\n", "\r", head];
      const randomText = (len: number) => Array.from({ length: len }, () => pieces[rand(pieces.length)]).join("");
      for (let round = 0; round < 2000; round += 1) {
        const original = randomText(rand(30));
        let state = EditorState.create({ doc: original.replace(/\r\n|\r|\n/g, "\n") });
        let changes = state.changes([]);
        for (let step = 0; step < 1 + rand(4); step += 1) {
          const len = state.doc.length;
          const from = rand(len + 1);
          const to = from + rand(len - from + 1);
          const tr = state.update({ changes: { from, to, insert: randomText(rand(4)).replace(/\r\n|\r/g, "\n") } });
          changes = changes.compose(tr.changes);
          state = tr.state;
        }
        const out = applyEditsPreservingLineBreaks(original, changes);
        expect(JSON.stringify([original, out.replace(/\r\n|\r|\n/g, "\n")])).toBe(JSON.stringify([original, state.doc.toString()]));
      }
    });

    it("keeps lone CRs that an edit moves next to an LF as separate line breaks", async () => {
      const { applyEditsPreservingLineBreaks } = await import("./todo-notes-editor.js");
      const { EditorState } = cm();
      // "a\r\rb\nc" with "b" deleted: CR CR LF would read as two line breaks instead of three.
      const state = EditorState.create({ doc: "a\n\nb\nc" });
      const tr = state.update({ changes: { from: 3, to: 4 } });
      expect(applyEditsPreservingLineBreaks("a\r\rb\nc", tr.changes)).toBe("a\n\n\nc");
    });

    const cm = () => (window as any).ScrumboyCodeMirror;
    function resetAndClear() {
      document.body.innerHTML = "";
      parent = document.createElement("div");
      document.body.append(parent);
    }
  });

  it("refuses uploads and typing beyond the notes limit", async () => {
    const ed = await editor("x".repeat(90), 100);
    expect(ed.uploadTarget.begin("a", { batch: "b", index: 0, length: 60 })).toBe(false);
    const v = view();
    v.dispatch({ changes: { from: 0, insert: "y".repeat(20) } });
    expect(ed.getValue()).toBe("x".repeat(90));
  });

  it("is read-only when notes cannot be edited", async () => {
    const ed = await editor(`a ${img(U1)}`);
    ed.setReadOnly(true);
    widgets()[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    key("Backspace");
    expect(ed.getValue()).toBe(`a ${img(U1)}`);
    expect(ed.uploadTarget.isReadOnly()).toBe(true);
  });

  it("acquires each image once and releases everything on clear", async () => {
    const ed = await editor(`${img(U1)}\n\n${img(U1, "again")}\n\n${img(U2)}`);
    expect(fake.acquired.sort()).toEqual([`/api/assets/${U1}`, `/api/assets/${U2}`]);
    ed.clear();
    expect(fake.released.sort()).toEqual([`/api/assets/${U1}`, `/api/assets/${U2}`]);
    expect(ed.getValue()).toBe("");
  });

  it("shows a selectable fallback for an image that cannot be loaded", async () => {
    fake.fail.add(`/api/assets/${U1}`);
    const ed = await editor(img(U1, "broken"));
    await flush();
    const w = widgets()[0];
    expect(w.classList.contains("todo-notes-image--unavailable")).toBe(true);
    expect(w.textContent).toContain("broken");
    w.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    key("Delete");
    expect(ed.getValue()).toBe("");
  });
});

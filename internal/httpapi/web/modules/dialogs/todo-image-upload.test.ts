// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchForm = vi.fn();
const showToast = vi.fn();
vi.mock("../api.js", () => ({ apiFetchForm: (...args: unknown[]) => apiFetchForm(...args) }));
vi.mock("../utils.js", () => ({ showToast: (msg: string) => showToast(msg) }));

import { altTextForFile, createTodoImageUploader, MAX_FILES_PER_ACTION, type ImageUploadContext } from "./todo-image-upload.js";

type Deferred = { resolve(v: unknown): void; reject(e: unknown): void; signal?: AbortSignal; path: string };

function png(name = "shot.png", size = 10, type = "image/png"): File {
  return new File([new Uint8Array(size)], name, { type });
}

function pasteEvent(files: File[], text = ""): Event {
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    value: { files, getData: (kind: string) => (kind === "text/plain" ? text : "") },
  });
  return event;
}

function dropEvent(files: File[], types = ["Files"]): Event {
  const event = new Event("drop", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "dataTransfer", { value: { files, types } });
  return event;
}

describe("todo image uploader", () => {
  let textarea: HTMLTextAreaElement;
  let context: ImageUploadContext | null;
  let calls: Deferred[];
  let pendingStates: Array<[number, number]>;
  let inputs: number;

  beforeEach(() => {
    document.body.innerHTML = "";
    textarea = document.createElement("textarea");
    textarea.maxLength = 20000;
    document.body.append(textarea);
    context = { slug: "board", todoLocalId: 7 };
    calls = [];
    pendingStates = [];
    inputs = 0;
    textarea.addEventListener("input", () => (inputs += 1));
    apiFetchForm.mockReset();
    showToast.mockReset();
    apiFetchForm.mockImplementation((path: string, _form: FormData, opts: { signal?: AbortSignal }) =>
      new Promise((resolve, reject) => {
        const d: Deferred = { resolve, reject, signal: opts?.signal, path };
        opts?.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
        calls.push(d);
      }),
    );
  });

  afterEach(() => vi.restoreAllMocks());

  function uploader() {
    const u = createTodoImageUploader({
      textarea,
      getContext: () => context,
      onPendingChange: (p, total) => pendingStates.push([p, total]),
    });
    u.attach();
    return u;
  }

  const settle = () => new Promise((r) => setTimeout(r, 0));

  it("intercepts an image-only paste, inserts a placeholder and uploads to the story", async () => {
    const u = uploader();
    textarea.value = "Before ";
    textarea.setSelectionRange(7, 7);
    const event = pasteEvent([png()]);
    textarea.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    expect(textarea.value).toMatch(/^Before !\[Uploading image…\]\(scrumboy-upload:[^)]+\)\n$/);
    expect(calls[0].path).toBe("/api/board/board/assets?todo=7");
    expect(u.pending()).toBe(1);
    calls[0].resolve({ assetId: "a", url: "/api/assets/11111111-1111-4111-8111-111111111111" });
    await settle();
    expect(textarea.value).toBe("Before ![shot](/api/assets/11111111-1111-4111-8111-111111111111)\n");
    expect(u.pending()).toBe(0);
    expect(pendingStates.at(-1)).toEqual([0, 0]);
    expect(inputs).toBeGreaterThanOrEqual(2);
  });

  it("lets text plus image paste through as text", () => {
    uploader();
    const event = pasteEvent([png()], "A1\tB1");
    textarea.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(apiFetchForm).not.toHaveBeenCalled();
  });

  it("ignores HTML-only clipboard content", () => {
    uploader();
    const event = pasteEvent([], "");
    textarea.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(apiFetchForm).not.toHaveBeenCalled();
  });

  it("uploads dropped image files and ignores dropped text", () => {
    uploader();
    const files = dropEvent([png("a.png"), png("b.jpg", 10, "image/jpeg")]);
    textarea.dispatchEvent(files);
    expect(files.defaultPrevented).toBe(true);
    expect(calls).toHaveLength(2);
    const text = dropEvent([], ["text/plain"]);
    textarea.dispatchEvent(text);
    expect(text.defaultPrevented).toBe(false);
    expect(calls).toHaveLength(2);
  });

  it("limits files per action and rejects unsupported or oversized files", () => {
    uploader();
    textarea.dispatchEvent(dropEvent(Array.from({ length: MAX_FILES_PER_ACTION + 2 }, (_, i) => png(`${i}.png`))));
    expect(calls).toHaveLength(MAX_FILES_PER_ACTION);
    expect(showToast).toHaveBeenCalledWith(expect.stringContaining("up to"));
    showToast.mockReset();
    textarea.dispatchEvent(dropEvent([png("ok.png"), png("huge.png", 11 * 1024 * 1024)]));
    expect(calls).toHaveLength(MAX_FILES_PER_ACTION + 1);
    expect(showToast).toHaveBeenCalledTimes(1);
  });

  it("has no file picker: uploads come only from paste and drop", () => {
    expect("pickFiles" in uploader()).toBe(false);
  });

  it("does not insert when the user deleted the placeholder", async () => {
    uploader();
    textarea.dispatchEvent(pasteEvent([png()]));
    textarea.value = "user rewrote everything";
    calls[0].resolve({ assetId: "a", url: "/api/assets/11111111-1111-4111-8111-111111111111" });
    await settle();
    expect(textarea.value).toBe("user rewrote everything");
  });

  it("removes the placeholder and maps errors to messages on failure", async () => {
    uploader();
    const cases: Array<[number, string | undefined, string]> = [
      [413, undefined, "too large"],
      [400, "unsupported_image_type", "PNG, JPEG and GIF"],
      [429, undefined, "Too many uploads"],
      [503, undefined, "busy"],
      [507, undefined, "storage"],
      [403, undefined, "cannot add images"],
      [500, undefined, "upload failed"],
    ];
    for (const [status, reason, text] of cases) {
      showToast.mockReset();
      textarea.value = "";
      textarea.dispatchEvent(pasteEvent([png()]));
      const err = Object.assign(new Error("x"), { status, data: { error: { details: { reason } } } });
      calls.at(-1)!.reject(err);
      await settle();
      expect(textarea.value).toBe("");
      expect(showToast.mock.calls[0][0].toLowerCase()).toContain(text.toLowerCase());
    }
  });

  it("puts back the text an image was pasted over when the upload fails or is cancelled", async () => {
    const u = uploader();
    textarea.value = "keep SELECTED keep";
    textarea.setSelectionRange(5, 13);
    textarea.dispatchEvent(pasteEvent([png()]));
    expect(textarea.value).toMatch(/^keep !\[Uploading image…\]\(scrumboy-upload:[^)]+\)\n keep$/);
    calls[0].reject(Object.assign(new Error("x"), { status: 500 }));
    await settle();
    expect(textarea.value).toBe("keep SELECTED keep");

    textarea.setSelectionRange(5, 13);
    textarea.dispatchEvent(pasteEvent([png()]));
    u.abortAll();
    await settle();
    expect(textarea.value).toBe("keep SELECTED keep");

    textarea.setSelectionRange(5, 13);
    textarea.dispatchEvent(pasteEvent([png()]));
    calls.at(-1)!.resolve({ assetId: "a", url: "/api/assets/11111111-1111-4111-8111-111111111111" });
    await settle();
    expect(textarea.value).toBe("keep ![shot](/api/assets/11111111-1111-4111-8111-111111111111)\n keep");
  });

  it("aborts in-flight uploads without a toast", async () => {
    const u = uploader();
    textarea.dispatchEvent(pasteEvent([png()]));
    textarea.dispatchEvent(pasteEvent([png("b.png")]));
    u.abortAll();
    await settle();
    expect(calls.every((c) => c.signal?.aborted)).toBe(true);
    expect(textarea.value).toBe("");
    expect(showToast).not.toHaveBeenCalled();
    expect(u.pending()).toBe(0);
  });

  it("refuses when the notes would exceed maxLength", () => {
    uploader();
    textarea.maxLength = 30;
    textarea.value = "x".repeat(25);
    textarea.dispatchEvent(pasteEvent([png()]));
    expect(apiFetchForm).not.toHaveBeenCalled();
    expect(showToast).toHaveBeenCalledWith(expect.stringContaining("too long"));
  });

  it("does nothing for read-only notes or when uploads are not allowed", () => {
    uploader();
    textarea.readOnly = true;
    const ro = pasteEvent([png()]);
    textarea.dispatchEvent(ro);
    textarea.readOnly = false;
    context = null;
    const disallowed = pasteEvent([png()]);
    textarea.dispatchEvent(disallowed);
    expect(ro.defaultPrevented || disallowed.defaultPrevented).toBe(false);
    expect(apiFetchForm).not.toHaveBeenCalled();
  });

  it("uploads new-story images without a todo parameter", () => {
    context = { slug: "my board", todoLocalId: null };
    uploader();
    textarea.dispatchEvent(pasteEvent([png()]));
    expect(calls[0].path).toBe("/api/board/my%20board/assets");
  });

  it("derives safe alt text from file names", () => {
    expect(altTextForFile("Screenshot 2026-10-22 at 10.00.png")).toBe("Screenshot 2026-10-22 at 10.00");
    expect(altTextForFile("a](javascript:x)[b.png")).toBe("a javascript:x b");
    // "|" would read as a display width (![alt|480](...)); backticks could open code spans.
    expect(altTextForFile("diagram|480.png")).toBe("diagram 480");
    expect(altTextForFile("a`b`.png")).toBe("a b");
    expect(altTextForFile("image.png")).toBe("image");
    expect(altTextForFile("")).toBe("image");
    expect(altTextForFile("x".repeat(300) + ".png")).toHaveLength(100);
  });
});

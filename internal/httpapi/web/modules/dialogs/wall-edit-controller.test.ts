// @vitest-environment happy-dom

import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { beginEdit } from "./wall-edit-controller.js";
import { buildNoteElement, isEditing, type WallNote } from "./wall-rendering.js";
import { resetEditGuards, setMounted, type Mounted } from "./wall-state.js";
import { initWallTestI18n } from "./wall-test-harness.js";
import enCatalog from "../i18n/locales/en.json";

function note(overrides: Partial<WallNote> = {}): WallNote {
  return {
    id: "n1",
    x: 10,
    y: 20,
    width: 200,
    height: 160,
    color: "#FFFFFF",
    text: "hello",
    version: 3,
    ...overrides,
  };
}

function mountEditable(): void {
  const state = {
    projectId: 1,
    slug: "alpha",
    role: "maintainer",
    canEdit: true,
    doc: { notes: [note()], version: 1 },
    userId: 1,
    onRefreshNeeded: vi.fn(),
    onTransient: vi.fn(),
    abort: new AbortController(),
    prevHtmlOverflow: "",
    transient: new Map(),
    colorTimers: new Map(),
    lastTapAt: new Map(),
    selected: new Set<string>(),
  } satisfies Mounted;
  setMounted(state);
}

function dispatchEscape(ta: HTMLTextAreaElement): void {
  ta.dispatchEvent(
    new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }),
  );
}

beforeAll(async () => {
  await initWallTestI18n({ en: enCatalog as Record<string, string> });
});

afterEach(() => {
  setMounted(null);
  resetEditGuards();
  document.body.innerHTML = "";
});

describe("wall-edit-controller", () => {
  it("Escape commits changed text and exits edit mode", () => {
    mountEditable();
    const n = note({ text: "" });
    const el = buildNoteElement(n, true);
    document.body.appendChild(el);
    const onCommitText = vi.fn();
    beginEdit(el, n, { onCommitText, onFlushDeferredRefetch: vi.fn() });

    const ta = el.querySelector<HTMLTextAreaElement>(".wall-note__editor");
    expect(ta).toBeTruthy();
    ta!.value = "typed on escape";
    dispatchEscape(ta!);

    expect(onCommitText).toHaveBeenCalledOnce();
    expect(onCommitText).toHaveBeenCalledWith("n1", "typed on escape");
    expect(isEditing(el)).toBe(false);
    expect(el.querySelector(".wall-note__display")?.textContent).toBe("typed on escape");
  });

  it("Escape with no edits skips onCommitText", () => {
    mountEditable();
    const n = note({ text: "hello" });
    const el = buildNoteElement(n, true);
    document.body.appendChild(el);
    const onCommitText = vi.fn();
    beginEdit(el, n, { onCommitText, onFlushDeferredRefetch: vi.fn() });

    const ta = el.querySelector<HTMLTextAreaElement>(".wall-note__editor");
    expect(ta).toBeTruthy();
    dispatchEscape(ta!);

    expect(onCommitText).not.toHaveBeenCalled();
    expect(isEditing(el)).toBe(false);
    expect(el.querySelector(".wall-note__display")?.textContent).toBe("hello");
  });

  it("Escape after blur does not double-commit", () => {
    mountEditable();
    const n = note({ text: "hello" });
    const el = buildNoteElement(n, true);
    document.body.appendChild(el);
    const onCommitText = vi.fn();
    beginEdit(el, n, { onCommitText, onFlushDeferredRefetch: vi.fn() });

    const ta = el.querySelector<HTMLTextAreaElement>(".wall-note__editor");
    expect(ta).toBeTruthy();
    ta!.value = "updated";
    ta!.dispatchEvent(new Event("blur"));
    dispatchEscape(ta!);

    expect(onCommitText).toHaveBeenCalledOnce();
    expect(onCommitText).toHaveBeenCalledWith("n1", "updated");
  });

  it("blocks commit of over-limit text and keeps the draft in the editor", () => {
    mountEditable();
    const n = note({ text: "hello" });
    const el = buildNoteElement(n, true);
    document.body.appendChild(el);
    const onCommitText = vi.fn();
    beginEdit(el, n, { onCommitText, onFlushDeferredRefetch: vi.fn() });

    const ta = el.querySelector<HTMLTextAreaElement>(".wall-note__editor");
    expect(ta).toBeTruthy();
    ta!.value = "😀".repeat(4001);
    ta!.dispatchEvent(new InputEvent("input", { bubbles: true }));
    ta!.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }),
    );

    expect(onCommitText).not.toHaveBeenCalled();
    expect(isEditing(el)).toBe(true);
    expect(ta!.value).toBe("😀".repeat(4001));
    expect(ta!.validationMessage.length).toBeGreaterThan(0);
  });
});

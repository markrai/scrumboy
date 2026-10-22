// @vitest-environment jsdom
// Image insertion gating and the save lock in the todo dialog.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const originalShowModal = HTMLDialogElement.prototype.showModal;

function installTodoDom(): void {
  document.body.innerHTML = `
    <dialog id="todoDialog">
      <h2 id="todoDialogTitle"></h2>
      <form id="todoForm">
        <input id="todoTitle" />
        <div id="todoBodyToggle" hidden>
          <button id="todoBodyWriteTab" type="button"></button>
          <button id="todoBodyPreviewTab" type="button"></button>
        </div>
        <label id="todoBodyLabel" for="todoBody">Notes</label>
        <textarea id="todoBody" maxlength="20000"></textarea>
        <div id="todoBodyEditor" hidden></div>
        <div id="todoBodyEditorStatus" role="status"></div>
        <div id="todoBodyPreview" hidden></div>
        <input id="todoTags" />
        <select id="todoStatus"></select>
        <select id="todoAssignee"></select>
        <select id="todoSprint"></select>
        <select id="todoEstimationPoints"></select>
        <div id="todoEstimationField"></div>
        <div id="todoAssigneeField"></div>
        <div id="todoSprintField"></div>
        <div id="todoLinksField"></div>
        <div id="todoDialogCreated"><span class="todo-dialog-datetime-value"></span></div>
        <div id="todoDialogUpdated"><span class="todo-dialog-datetime-value"></span></div>
        <button id="closeTodoBtn" type="button"></button>
        <button id="deleteTodoBtn" type="button"></button>
        <button id="shareTodoBtn" type="button"></button>
        <button id="addTagBtn" type="button"></button>
        <div id="tagsChips"></div>
        <button id="saveTodoBtn" type="submit">Save</button>
      </form>
    </dialog>
  `;
}

type Gate = {
  inlineImages?: boolean;
  markdown?: boolean;
  expiresAt?: string;
  canEditNotes?: boolean;
  canSubmitTodo?: boolean;
};

const apiFetchForm = vi.fn();

function mockTodoModule(gate: Gate = {}): void {
  const g = { inlineImages: true, markdown: true, canEditNotes: true, canSubmitTodo: true, ...gate };
  vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]), apiFetchForm }));
  vi.doMock("../state/selectors.js", () => ({
    getBoard: () => ({ columnOrder: [{ key: "backlog", name: "Backlog" }], project: { creatorUserId: 1, expiresAt: g.expiresAt } }),
    getBoardMembers: () => [],
    getInlineImagesEnabled: () => g.inlineImages,
    getMarkdownNotesEnabled: () => g.markdown,
    getMermaidNotesEnabled: () => false,
    getSlug: () => "board",
    getTagColors: () => ({}),
    getUser: () => null,
  }));
  vi.doMock("../state/mutations.js", () => ({
    setAvailableTags: vi.fn(),
    setAvailableTagsMap: vi.fn(),
    setEditingTodo: vi.fn(),
    setTagColors: vi.fn(),
  }));
  vi.doMock("../utils.js", () => ({
    escapeHTML: (s: string) => String(s),
    isAnonymousBoard: () => false,
    showToast: vi.fn(),
  }));
  vi.doMock("../sprints.js", () => ({ normalizeSprints: () => [], boardSprintsEnabled: () => false }));
  vi.doMock("./todo-links.js", () => ({
    bindShareTodoButton: vi.fn(),
    bindTodoDialogLinkLifecycle: vi.fn(),
    initializeTodoDialogLinks: vi.fn(),
    resetTodoDialogLinks: vi.fn(),
  }));
  vi.doMock("./todo-permissions.js", () => ({
    computeTodoDialogPermissions: () => ({
      canSubmitTodo: g.canSubmitTodo,
      canDeleteTodo: false,
      canEditAssignment: true,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: g.canEditNotes,
      canEditTitle: true,
      canEditStatus: true,
    }),
    setTodoFormPermissions: vi.fn(),
  }));
  vi.doMock("./todo-tags.js", () => ({
    getTagsFromChips: () => [],
    renderTagsChips: vi.fn(),
    resetTodoTagAutocompleteBindings: vi.fn(),
    setupTagAutocomplete: vi.fn(),
  }));
}

const editTodo = { localId: 7, title: "Story", body: "", tags: [], columnKey: "backlog" };

describe("todo dialog image insertion", () => {
  beforeEach(() => {
    vi.resetModules();
    installTodoDom();
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      cb(0);
      return 1;
    });
    vi.stubGlobal("matchMedia", () => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
      media: "",
      onchange: null,
    }));
    HTMLDialogElement.prototype.showModal = function showModalStub() {
      (this as HTMLDialogElement).open = true;
    };
    apiFetchForm.mockReset();
  });

  afterEach(() => {
    HTMLDialogElement.prototype.showModal = originalShowModal;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  const pasteImage = (target: HTMLElement): Event => {
    const event = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", {
      value: { files: [new File([new Uint8Array(4)], "a.png", { type: "image/png" })], getData: () => "" },
    });
    target.dispatchEvent(event);
    return event;
  };

  it("has no separate image button or file picker", async () => {
    mockTodoModule();
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "edit", todo: editTodo, role: "maintainer" });
    expect(document.querySelector("#todoBodyInsertImage, #todoBodyImageInput, #todoDialog input[type=file]")).toBeNull();
  });

  it.each([
    ["maintainer editing", {}, "edit", true],
    ["maintainer creating", {}, "create", true],
    ["flag off", { inlineImages: false }, "edit", false],
    ["markdown off", { markdown: false }, "edit", false],
    ["temporary board", { expiresAt: "2030-01-01T00:00:00Z" }, "edit", false],
    ["notes read-only (viewer / unassigned contributor)", { canEditNotes: false }, "edit", false],
    ["cannot create", { canSubmitTodo: false }, "create", false],
  ] as const)("pasted images upload only when allowed: %s", async (_name, gate, mode, uploads) => {
    apiFetchForm.mockImplementation(() => new Promise(() => {}));
    mockTodoModule(gate);
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode, todo: mode === "edit" ? editTodo : undefined, role: "maintainer" });
    const event = pasteImage(document.getElementById("todoBody") as HTMLElement);
    expect(event.defaultPrevented).toBe(uploads);
    expect(apiFetchForm).toHaveBeenCalledTimes(uploads ? 1 : 0);
  });

  it("locks save while uploading and aborts on close", async () => {
    let signal: AbortSignal | undefined;
    apiFetchForm.mockImplementation((path: string, _f: FormData, opts: { signal?: AbortSignal }) => {
      signal = opts.signal;
      expect(path).toBe("/api/board/board/assets?todo=7");
      return new Promise((_resolve, reject) => opts.signal?.addEventListener("abort", () => reject(new DOMException("x", "AbortError"))));
    });
    mockTodoModule();
    const { openTodoDialog, isTodoImageUploadPending } = await import("./todo.js");
    await openTodoDialog({ mode: "edit", todo: editTodo, role: "maintainer" });
    pasteImage(document.getElementById("todoBody") as HTMLElement);
    const save = document.getElementById("saveTodoBtn") as HTMLButtonElement;
    expect(isTodoImageUploadPending()).toBe(true);
    expect(save.disabled).toBe(true);
    expect(save.textContent).toContain("0/1");

    (document.getElementById("todoDialog") as HTMLDialogElement).dispatchEvent(new Event("close"));
    await new Promise((r) => setTimeout(r, 0));
    expect(signal?.aborted).toBe(true);
    expect(isTodoImageUploadPending()).toBe(false);
    expect(save.disabled).toBe(false);
  });
});

describe("todo dialog visual notes editor", () => {
  beforeEach(async () => {
    vi.resetModules();
    installTodoDom();
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      cb(0);
      return 1;
    });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() }));
    HTMLDialogElement.prototype.showModal = function showModalStub() {
      (this as HTMLDialogElement).open = true;
    };
    Range.prototype.getClientRects = () => Object.assign([], { item: () => null }) as unknown as DOMRectList;
    Range.prototype.getBoundingClientRect = () => ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON() {} }) as DOMRect;
    const { default: MarkdownIt } = await import("markdown-it");
    (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
    await import("../../scripts/codemirror-vendor-entry.mjs");
    apiFetchForm.mockReset();
  });

  afterEach(() => {
    HTMLDialogElement.prototype.showModal = originalShowModal;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    delete (window as any).ScrumboyCodeMirror;
    document.body.innerHTML = "";
  });

  const U1 = "11111111-1111-4111-8111-111111111111";
  const body = `Intro\r\n\r\n![shot|240](/api/assets/${U1})\r\n`;
  const editor = () => document.getElementById("todoBodyEditor") as HTMLElement;
  const textarea = () => document.getElementById("todoBody") as HTMLTextAreaElement;

  it("edits notes in the visual editor with images shown and the text kept exact", async () => {
    mockTodoModule();
    const { openTodoDialog, getTodoNotesValue } = await import("./todo.js");
    await openTodoDialog({ mode: "edit", todo: { ...editTodo, body }, role: "maintainer" });
    expect(editor().hidden).toBe(false);
    expect(textarea().hidden).toBe(true);
    const widget = editor().querySelector(".todo-notes-image[data-path]") as HTMLElement;
    expect(widget.dataset.path).toBe(`/api/assets/${U1}`);
    expect((widget.querySelector("img") as HTMLImageElement).style.width).toBe("240px");
    expect(getTodoNotesValue()).toBe(body);
    expect(editor().querySelector(".cm-content")?.getAttribute("aria-labelledby")).toBe("todoBodyLabel");
  });

  it("keeps the textarea when inline images are off, and is read-only for viewers", async () => {
    mockTodoModule({ inlineImages: false });
    let mod = await import("./todo.js");
    await mod.openTodoDialog({ mode: "edit", todo: { ...editTodo, body }, role: "maintainer" });
    expect(editor().hidden).toBe(true);
    expect(textarea().hidden).toBe(false);
    expect(mod.getTodoNotesValue()).toBe(body.replace(/\r\n/g, "\n"));

    vi.resetModules();
    installTodoDom();
    mockTodoModule({ canEditNotes: false });
    mod = await import("./todo.js");
    await mod.openTodoDialog({ mode: "edit", todo: { ...editTodo, body }, role: "viewer" });
    expect(editor().querySelector(".cm-content")?.getAttribute("contenteditable")).toBe("false");
    expect(editor().querySelector(".cm-editor")?.classList.contains("is-readonly")).toBe(true);
  });

  it("releases the editor's images when the dialog closes", async () => {
    mockTodoModule();
    const { openTodoDialog, getTodoNotesValue } = await import("./todo.js");
    await openTodoDialog({ mode: "edit", todo: { ...editTodo, body }, role: "maintainer" });
    (document.getElementById("todoDialog") as HTMLDialogElement).dispatchEvent(new Event("close"));
    expect(editor().querySelector(".todo-notes-image[data-path]")).toBeNull();
    expect(getTodoNotesValue()).toBe("");
  });
});

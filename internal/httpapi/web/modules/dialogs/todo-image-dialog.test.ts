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
        <button id="todoBodyInsertImage" type="button" hidden></button>
        <input id="todoBodyImageInput" type="file" hidden />
        <textarea id="todoBody" maxlength="20000"></textarea>
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

  const button = () => document.getElementById("todoBodyInsertImage") as HTMLButtonElement;

  it.each([
    ["maintainer editing", {}, "edit", false],
    ["maintainer creating", {}, "create", false],
    ["flag off", { inlineImages: false }, "edit", true],
    ["markdown off", { markdown: false }, "edit", true],
    ["temporary board", { expiresAt: "2030-01-01T00:00:00Z" }, "edit", true],
    ["notes read-only (viewer / unassigned contributor)", { canEditNotes: false }, "edit", true],
    ["cannot create", { canSubmitTodo: false }, "create", true],
  ] as const)("insert button visibility: %s", async (_name, gate, mode, hidden) => {
    mockTodoModule(gate);
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode, todo: mode === "edit" ? editTodo : undefined, role: "maintainer" });
    expect(button().hidden).toBe(hidden);
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
    const input = document.getElementById("todoBodyImageInput") as HTMLInputElement;
    Object.defineProperty(input, "files", { value: [new File([new Uint8Array(4)], "a.png", { type: "image/png" })], configurable: true });
    input.dispatchEvent(new Event("change"));
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

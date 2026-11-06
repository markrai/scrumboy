// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let capturedFrame: FrameRequestCallback | null = null;
let coarsePointer = false;

function installTodoDom(): void {
  document.body.innerHTML = `
    <dialog id="todoDialog">
      <h2 id="todoDialogTitle"></h2>
      <form id="todoForm">
        <input id="todoTitle" />
        <div id="todoBodyToggle">
          <button id="todoBodyWriteTab" type="button"></button>
          <button id="todoBodyPreviewTab" type="button"></button>
        </div>
        <textarea id="todoBody"></textarea>
        <div id="todoBodyPreview"></div>
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
        <button id="saveTodoBtn" type="button"></button>
      </form>
    </dialog>
  `;
}

function installDialogPolyfill(): void {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      if (!this.hasAttribute("open")) return;
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
}

function mockTodoModule(): void {
  vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]) }));
  vi.doMock("../state/selectors.js", () => ({
    getBoard: () => ({
      columnOrder: [
        { key: "backlog", name: "Backlog" },
        { key: "doing", name: "Doing" },
      ],
      project: { creatorUserId: 1 },
    }),
    getBoardMembers: () => [],
    getMarkdownNotesEnabled: () => false,
    getMermaidNotesEnabled: () => false,
    getInlineImagesEnabled: () => false,
    getSlug: () => "",
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
    isAnonymousBoard: () => true,
    showConfirmDialog: vi.fn(),
    showToast: vi.fn(),
  }));
  vi.doMock("../sprints.js", () => ({ normalizeSprints: () => [] }));
  vi.doMock("./todo-links.js", () => ({
    bindShareTodoButton: vi.fn(),
    bindTodoDialogLinkLifecycle: vi.fn(),
    initializeTodoDialogLinks: vi.fn(),
    resetTodoDialogLinks: vi.fn(),
  }));
  vi.doMock("./todo-permissions.js", () => ({
    computeTodoDialogPermissions: () => ({
      canSubmitTodo: true,
      canDeleteTodo: true,
      canEditAssignment: true,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
      canEditTitle: true,
      canEditStatus: true,
      canEditLinks: true,
    }),
    getTodoFormPermissions: () => ({
      canSubmitTodo: true,
      canDeleteTodo: true,
      canEditAssignment: true,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
      canEditTitle: true,
      canEditStatus: true,
      canEditLinks: true,
    }),
    setTodoFormPermissions: vi.fn(),
  }));
  vi.doMock("./todo-tags.js", () => ({
    getTagsFromChips: () => [],
    normalizeTagName: (tag: string) => tag,
    removeTag: vi.fn(),
    renderTagAutocomplete: vi.fn(),
    renderTagsChips: vi.fn(),
    resetTodoTagAutocompleteBindings: vi.fn(),
    setupTagAutocomplete: vi.fn(),
  }));
}

describe("todo dialog initial focus", () => {
  beforeEach(() => {
    vi.resetModules();
    installTodoDom();
    installDialogPolyfill();
    capturedFrame = null;
    coarsePointer = false;
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      capturedFrame = cb;
      return 1;
    });
    vi.stubGlobal("matchMedia", () => ({
      matches: coarsePointer,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
      media: "",
      onchange: null,
    }));
    mockTodoModule();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
    document.body.innerHTML = "";
  });

  it("focuses the title when nothing took focus before the frame runs", async () => {
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    expect(capturedFrame).not.toBeNull();
    capturedFrame!(0);

    expect((document.getElementById("todoTitle") as HTMLInputElement).matches(":focus")).toBe(true);
  });

  it("does not steal focus already placed on a field when the frame runs late", async () => {
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    expect(capturedFrame).not.toBeNull();
    // Simulate keystrokes landing in the tags box before the deferred
    // focus frame runs (slow main thread, assistive tech, automation).
    (document.getElementById("todoTags") as HTMLInputElement).focus();
    capturedFrame!(0);

    expect((document.getElementById("todoTags") as HTMLInputElement).matches(":focus")).toBe(true);
    expect((document.getElementById("todoTitle") as HTMLInputElement).matches(":focus")).toBe(false);
  });

  it("still prefers the close button on coarse pointers when untouched", async () => {
    coarsePointer = true;
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    expect(capturedFrame).not.toBeNull();
    capturedFrame!(0);

    expect((document.getElementById("closeTodoBtn") as HTMLButtonElement).matches(":focus")).toBe(true);
  });

  it("respects a user-chosen field on coarse pointers too", async () => {
    coarsePointer = true;
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    expect(capturedFrame).not.toBeNull();
    (document.getElementById("todoTags") as HTMLInputElement).focus();
    capturedFrame!(0);

    expect((document.getElementById("todoTags") as HTMLInputElement).matches(":focus")).toBe(true);
  });
});

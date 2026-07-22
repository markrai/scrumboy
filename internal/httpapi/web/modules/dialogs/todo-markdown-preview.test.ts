// @vitest-environment jsdom
// DOMPurify does not support happy-dom. Use jsdom for sanitizer tests.
import createDOMPurify from "dompurify";
import MarkdownIt from "markdown-it";
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
        <textarea id="todoBody"></textarea>
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
        <button id="saveTodoBtn" type="button"></button>
      </form>
    </dialog>
  `;
}

function installMarkdownVendors(): void {
  (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) =>
    new MarkdownIt(preset, options);
  (window as any).DOMPurify = createDOMPurify(window);
}

function installDialogStubs(): void {
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
}

function mockTodoModule(markdownNotesEnabled: boolean, mermaidNotesEnabled = false): void {
  vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]) }));
  vi.doMock("../state/selectors.js", () => ({
    getBoard: () => ({ columnOrder: [{ key: "backlog", name: "Backlog" }], project: { creatorUserId: 1 } }),
    getBoardMembers: () => [],
    getMarkdownNotesEnabled: () => markdownNotesEnabled,
    getMermaidNotesEnabled: () => mermaidNotesEnabled,
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
    escapeHTML: (s: string) =>
      String(s)
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#039;"),
    isAnonymousBoard: () => false,
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
      canDeleteTodo: false,
      canEditAssignment: true,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
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

describe("todo markdown preview", () => {
  beforeEach(() => {
    vi.resetModules();
    installTodoDom();
    installDialogStubs();
    installMarkdownVendors();
  });

  afterEach(() => {
    HTMLDialogElement.prototype.showModal = originalShowModal;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("keeps the notes field textarea-only when the feature flag is off", async () => {
    mockTodoModule(false);
    const { openTodoDialog } = await import("./todo.js");

    await openTodoDialog({ mode: "create", role: "maintainer" });

    expect((document.getElementById("todoBodyToggle") as HTMLElement).hidden).toBe(true);
    expect((document.getElementById("todoBody") as HTMLTextAreaElement).hidden).toBe(false);
    expect((document.getElementById("todoBodyPreview") as HTMLElement).hidden).toBe(true);
  });

  it("shows the markdown/preview toggle and empty preview state when enabled", async () => {
    mockTodoModule(true);
    const { openTodoDialog } = await import("./todo.js");

    await openTodoDialog({ mode: "create", role: "maintainer" });

    const toggle = document.getElementById("todoBodyToggle") as HTMLElement;
    const body = document.getElementById("todoBody") as HTMLTextAreaElement;
    const previewTab = document.getElementById("todoBodyPreviewTab") as HTMLButtonElement;
    const preview = document.getElementById("todoBodyPreview") as HTMLElement;

    expect(toggle.hidden).toBe(false);
    previewTab.click();

    expect(body.hidden).toBe(true);
    expect(preview.hidden).toBe(false);
    expect(preview.classList.contains("todo-markdown-preview--empty")).toBe(true);
  });

  describe("theme change rerender", () => {
    afterEach(() => {
      vi.doUnmock("../markdown-preview.js");
      vi.resetModules();
    });

    it("re-renders mermaid preview when the app theme changes while preview is active", async () => {
      const renderSpy = vi.fn().mockResolvedValue(undefined);
      vi.doMock("../markdown-preview.js", () => ({
        renderMarkdownPreviewInto: renderSpy,
      }));
      mockTodoModule(true, true);
      const { openTodoDialog } = await import("./todo.js");
      const { applyTheme } = await import("../theme.js");

      await openTodoDialog({ mode: "create", role: "maintainer" });
      (document.getElementById("todoBodyPreviewTab") as HTMLButtonElement).click();
      expect(renderSpy).toHaveBeenCalledTimes(1);

      applyTheme("light");

      expect(renderSpy).toHaveBeenCalledTimes(2);
    });
  });

  it("renders preview without mutating the textarea value and preserves raw markdown in edit mode", async () => {
    mockTodoModule(true);
    const { openTodoDialog } = await import("./todo.js");

    const rawTitle = "**plain title**";
    const rawBody = "# Heading\n\n- item\n\n**bold**";
    await openTodoDialog({
      mode: "edit",
      role: "maintainer",
      todo: {
        title: rawTitle,
        body: rawBody,
        columnKey: "backlog",
        tags: [],
      },
    });

    const title = document.getElementById("todoTitle") as HTMLInputElement;
    const body = document.getElementById("todoBody") as HTMLTextAreaElement;
    const previewTab = document.getElementById("todoBodyPreviewTab") as HTMLButtonElement;
    const markdownTab = document.getElementById("todoBodyWriteTab") as HTMLButtonElement;
    const preview = document.getElementById("todoBodyPreview") as HTMLElement;

    expect(title.value).toBe(rawTitle);
    expect(body.value).toBe(rawBody);

    previewTab.click();
    expect(body.value).toBe(rawBody);
    expect(preview.innerHTML).toContain("<h1>Heading</h1>");
    expect(preview.innerHTML).toContain("<strong>bold</strong>");

    markdownTab.click();
    expect(body.hidden).toBe(false);
    expect(body.value).toBe(rawBody);
  });
});

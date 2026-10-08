// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DIALOG_CLOSE_REQUEST_EVENT } from "../core/modal-outside-click.js";

const showConfirmDialogMock = vi.fn();

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

function renderTags(tags: string[]): void {
  const chips = document.getElementById("tagsChips");
  if (!chips) return;
  chips.innerHTML = tags
    .map((tag) => `<span class="tag-chip" data-tag="${tag}">${tag}</span>`)
    .join("");
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
    isAnonymousBoard: () => true,
    showConfirmDialog: showConfirmDialogMock,
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
    getTagsFromChips: () =>
      Array.from(document.querySelectorAll("#tagsChips .tag-chip")).map(
        (chip) => chip.getAttribute("data-tag") || "",
      ),
    normalizeTagName: (tag: string) => tag,
    removeTag: vi.fn(),
    renderTagAutocomplete: vi.fn(),
    renderTagsChips: (tags: string[]) => {
      renderTags(tags);
    },
    resetTodoTagAutocompleteBindings: vi.fn(),
    setupTagAutocomplete: vi.fn(),
  }));
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

function simulateOutsideClose(dialog: HTMLDialogElement): boolean {
  const closeRequest = new CustomEvent(DIALOG_CLOSE_REQUEST_EVENT, {
    cancelable: true,
    detail: { reason: "outside" },
  });
  const allowed = dialog.dispatchEvent(closeRequest);
  if (allowed) {
    dialog.close();
  }
  return allowed;
}

describe("todo close guard", () => {
  beforeEach(() => {
    vi.resetModules();
    installTodoDom();
    installDialogPolyfill();
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
    showConfirmDialogMock.mockReset();
    mockTodoModule();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
    document.body.innerHTML = "";
  });

  it("closes immediately without confirmation when clean via close button", async () => {
    const { openTodoDialog, requestTodoDialogClose } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    const dialog = document.getElementById("todoDialog") as HTMLDialogElement;
    expect(dialog.open).toBe(true);

    await expect(requestTodoDialogClose({ reason: "button" })).resolves.toBe(true);
    expect(showConfirmDialogMock).not.toHaveBeenCalled();
    expect(dialog.open).toBe(false);
  });

  it("uses the outside-close request path and only prompts when dirty", async () => {
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    const dialog = document.getElementById("todoDialog") as HTMLDialogElement;
    const cleanAllowed = simulateOutsideClose(dialog);
    expect(cleanAllowed).toBe(true);
    expect(showConfirmDialogMock).not.toHaveBeenCalled();
    expect(dialog.open).toBe(false);

    await openTodoDialog({ mode: "create", role: "maintainer" });
    (document.getElementById("todoTitle") as HTMLInputElement).value = "Dirty";
    showConfirmDialogMock.mockResolvedValue(false);

    const dirtyAllowed = simulateOutsideClose(dialog);
    await flushPromises();

    expect(dirtyAllowed).toBe(false);
    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(dialog.open).toBe(true);
  });

  it("prevents the native cancel close on Esc when dirty and closes after confirmation", async () => {
    showConfirmDialogMock.mockResolvedValue(true);
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    const dialog = document.getElementById("todoDialog") as HTMLDialogElement;
    (document.getElementById("todoBody") as HTMLTextAreaElement).value = "Dirty notes";

    const cancelEvent = new Event("cancel", { cancelable: true });
    dialog.dispatchEvent(cancelEvent);
    await flushPromises();

    expect(cancelEvent.defaultPrevented).toBe(true);
    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(dialog.open).toBe(false);
  });

  it("tracks title, body, status, and tags in dirty detection and clears when reverted", async () => {
    const { __isTodoDialogDirtyForTest, openTodoDialog, requestTodoDialogClose } = await import("./todo.js");
    await openTodoDialog({
      mode: "edit",
      role: "maintainer",
      todo: {
        title: "Alpha",
        body: "Body",
        columnKey: "backlog",
        tags: ["Bug"],
      },
    });

    const title = document.getElementById("todoTitle") as HTMLInputElement;
    const body = document.getElementById("todoBody") as HTMLTextAreaElement;
    const status = document.getElementById("todoStatus") as HTMLSelectElement;

    expect(__isTodoDialogDirtyForTest()).toBe(false);

    title.value = "Beta";
    expect(__isTodoDialogDirtyForTest()).toBe(true);
    title.value = "Alpha";
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    body.value = "Changed";
    expect(__isTodoDialogDirtyForTest()).toBe(true);
    body.value = "Body";
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    status.value = "doing";
    expect(__isTodoDialogDirtyForTest()).toBe(true);
    status.value = "backlog";
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    renderTags(["Feature"]);
    expect(__isTodoDialogDirtyForTest()).toBe(true);
    renderTags(["Bug"]);
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    await expect(requestTodoDialogClose({ reason: "button" })).resolves.toBe(true);
    expect(showConfirmDialogMock).not.toHaveBeenCalled();
  });

  it("runs the create-success callback once for the matching dialog generation", async () => {
    const callback = vi.fn();
    const { getTodoDialogOpenGeneration, notifyTodoCreated, openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer", onCreated: callback });
    const generation = getTodoDialogOpenGeneration();
    const todo = { id: 1, localId: 7, title: "Created", status: "BACKLOG" } as any;

    await notifyTodoCreated(todo, generation);
    await notifyTodoCreated(todo, generation);

    expect(callback).toHaveBeenCalledTimes(1);
    expect(callback).toHaveBeenCalledWith(todo);
  });

  it("clears create callbacks on cancel/edit and rejects stale success generations", async () => {
    const first = vi.fn();
    const second = vi.fn();
    const { getTodoDialogOpenGeneration, notifyTodoCreated, openTodoDialog, requestTodoDialogClose } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer", onCreated: first });
    const firstGeneration = getTodoDialogOpenGeneration();
    await requestTodoDialogClose({ force: true, reason: "cancel" });
    await notifyTodoCreated({ id: 1, localId: 1, title: "Late", status: "BACKLOG" } as any, firstGeneration);
    expect(first).not.toHaveBeenCalled();

    await openTodoDialog({ mode: "create", role: "maintainer", onCreated: second });
    const secondGeneration = getTodoDialogOpenGeneration();
    await openTodoDialog({
      mode: "edit",
      role: "maintainer",
      todo: { id: 2, localId: 2, title: "Existing", body: "", columnKey: "backlog", tags: [] },
    });
    await notifyTodoCreated({ id: 3, localId: 3, title: "Late", status: "BACKLOG" } as any, secondGeneration);
    expect(second).not.toHaveBeenCalled();
  });

  it("does not turn a successful create into a failure when its callback rejects", async () => {
    const callback = vi.fn().mockRejectedValue(new Error("placement failed"));
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const { getTodoDialogOpenGeneration, notifyTodoCreated, openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer", onCreated: callback });
    await expect(notifyTodoCreated(
      { id: 1, localId: 1, title: "Created", status: "BACKLOG" } as any,
      getTodoDialogOpenGeneration(),
    )).resolves.toBeUndefined();
    expect(callback).toHaveBeenCalledTimes(1);
  });
});

// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DIALOG_CLOSE_REQUEST_EVENT } from "../core/modal-outside-click.js";
import enCatalog from "../i18n/locales/en.json";
import pseudoCatalog from "../i18n/locales/pseudo.json";

const showConfirmDialogMock = vi.fn();
const apiFetchMock = vi.fn();
let fullProject = false;
let activeUserRole = "maintainer";

function installTodoDom(): void {
  document.body.innerHTML = `
    <dialog id="todoDialog">
      <h2 id="todoDialogTitle"></h2>
      <form id="todoForm">
        <div id="todoReferenceTabs" role="tablist">
          <button id="todoDetailsTab" type="button" role="tab" aria-selected="true">Details</button>
          <button id="todoReferencesTab" type="button" role="tab" aria-selected="false">References</button>
        </div>
        <div id="todoDetailsPanel">
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
        <div id="todoDialogCreated"><span class="todo-dialog-datetime-value"></span><span id="todoDialogCreatedBy"></span><button id="todoCreatorEditBtn" type="button" hidden></button><button id="todoCreatorResetBtn" type="button" hidden></button></div>
        <div id="todoCreatorSelector" hidden><input id="todoCreatorSearch" /><button id="todoCreatorSelectorClose" type="button"></button><div id="todoCreatorStatus"></div><div id="todoCreatorOptions"></div></div>
        <div id="todoDialogUpdated"><span class="todo-dialog-datetime-value"></span></div>
        <button id="closeTodoBtn" type="button"></button>
        <button id="deleteTodoBtn" type="button"></button>
        <button id="shareTodoBtn" type="button"></button>
        <button id="addTagBtn" type="button"></button>
        <div id="tagsChips"></div>
        <button id="saveTodoBtn" type="button"></button>
        </div>
        <div id="todoReferencesPanel" hidden>
          <div id="todoExternalResourcesField">
            <div id="externalResourceControls"><select id="externalResourcePicker"></select><button id="addExternalResourceBtn" type="button"></button></div>
            <div id="externalResourcesChips"></div>
          </div>
        </div>
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
  vi.doMock("../api.js", () => ({ apiFetch: apiFetchMock }));
  vi.doMock("../state/selectors.js", () => ({
    getBoard: () => ({
      columnOrder: [
        { key: "backlog", name: "Backlog" },
        { key: "doing", name: "Doing" },
      ],
      project: { id: 1, creatorUserId: 1 },
    }),
    getBoardMembers: () => fullProject ? [
      { userId: 10, name: "Ada", role: activeUserRole },
      { userId: 11, name: "Grace", role: "viewer" },
    ] : [],
    getMarkdownNotesEnabled: () => false,
    getMermaidNotesEnabled: () => false,
    getInlineImagesEnabled: () => false,
    getSlug: () => fullProject ? "alpha" : "",
    getTagColors: () => ({}),
    getUser: () => fullProject ? { id: 10 } : null,
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
    isAnonymousBoard: () => !fullProject,
    showConfirmDialog: showConfirmDialogMock,
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
    computeTodoDialogPermissions: (options: { todo?: { archivedAt?: string | null } }) => ({
      canSubmitTodo: true,
      canDeleteTodo: true,
      canEditAssignment: true,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
      canEditTitle: true,
      canEditCreator: fullProject && activeUserRole === "maintainer" && !options.todo?.archivedAt,
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
      canEditCreator: fullProject && activeUserRole === "maintainer",
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
  beforeEach(async () => {
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
    apiFetchMock.mockReset().mockResolvedValue([]);
    fullProject = false;
    activeUserRole = "maintainer";
    mockTodoModule();
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({ locale: "en", loadLocale: async (locale) => locale === "pseudo" ? pseudoCatalog : enCatalog, storage: null });
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

  it.each([
    { role: "viewer", assigneeUserId: 10, archivedAt: null },
    { role: "contributor", assigneeUserId: 10, archivedAt: null },
    { role: "contributor", assigneeUserId: 99, archivedAt: null },
    { role: "maintainer", assigneeUserId: 10, archivedAt: "2026-11-02T12:00:00Z" },
  ])("retains read-only creator attribution for $role dialog context", async ({ role, assigneeUserId, archivedAt }) => {
    fullProject = true;
    activeUserRole = role;
    const { __isTodoDialogDirtyForTest, openTodoDialog } = await import("./todo.js");
    await openTodoDialog({
      mode: "edit",
      role,
      todo: {
        localId: 4,
        title: "Attributed",
        body: "Body",
        columnKey: "backlog",
        createdByUserId: 11,
        assigneeUserId,
        archivedAt,
        createdAt: "2026-11-01T12:00:00Z",
        tags: [],
      },
    });

    expect(document.getElementById("todoDialogCreatedBy")?.textContent).toBe("by Grace");
    expect((document.getElementById("todoCreatorEditBtn") as HTMLButtonElement).hidden).toBe(true);
    expect(__isTodoDialogDirtyForTest()).toBe(false);
    expect(apiFetchMock.mock.calls.some((call) => String(call[0]).includes("/available-users"))).toBe(false);

    const i18n = await import("../i18n/index.js");
    await i18n.setLocale("pseudo");
    expect(document.getElementById("todoDialogCreatedBy")?.textContent).toContain("Grace");
    expect(__isTodoDialogDirtyForTest()).toBe(false);
  });

  it("keeps a clean dialog clean when creator authorization is revoked", async () => {
    fullProject = true;
    const { __isTodoDialogDirtyForTest, openTodoDialog } = await import("./todo.js");
    await openTodoDialog({
      mode: "edit",
      role: "maintainer",
      todo: {
        localId: 4,
        title: "Alpha",
        body: "Body",
        columnKey: "backlog",
        createdByUserId: 10,
        createdAt: "2026-11-01T12:00:00Z",
        tags: [],
      },
    });

    activeUserRole = "viewer";
    const events = await import("../events.js");
    events.emit("members-updated", { projectId: 1 });

    expect(__isTodoDialogDirtyForTest()).toBe(false);
    expect(document.getElementById("todoDialogCreatedBy")?.textContent).toBe("by Ada");
    expect((document.getElementById("todoCreatorEditBtn") as HTMLButtonElement).hidden).toBe(true);
  });

  it("discards only the pending creator mutation when authorization is revoked", async () => {
    fullProject = true;
    const { __isTodoDialogDirtyForTest, getPendingTodoCreatorId, openTodoDialog } = await import("./todo.js");
    await openTodoDialog({
      mode: "edit",
      role: "maintainer",
      todo: {
        localId: 4,
        title: "Alpha",
        body: "Body",
        columnKey: "backlog",
        createdByUserId: 10,
        createdAt: "2026-11-01T12:00:00Z",
        tags: [],
      },
    });
    (document.getElementById("todoCreatorEditBtn") as HTMLButtonElement).click();
    await flushPromises();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    (document.getElementById("todoTitle") as HTMLInputElement).value = "Pending title";
    (document.getElementById("todoBody") as HTMLTextAreaElement).value = "Pending body";

    activeUserRole = "viewer";
    const events = await import("../events.js");
    events.emit("members-updated", { projectId: 1 });

    expect(getPendingTodoCreatorId()).toBeNull();
    expect((document.getElementById("todoTitle") as HTMLInputElement).value).toBe("Pending title");
    expect((document.getElementById("todoBody") as HTMLTextAreaElement).value).toBe("Pending body");
    expect(__isTodoDialogDirtyForTest()).toBe(true);
    expect(document.getElementById("todoCreatorOptions")?.children).toHaveLength(0);
    expect((document.getElementById("todoCreatorSelector") as HTMLElement).hidden).toBe(true);
  });

  it("tracks pending creator selection without treating selector activity as a change", async () => {
    fullProject = true;
    const { __isTodoDialogDirtyForTest, getPendingTodoCreatorId, openTodoDialog, requestTodoDialogClose } = await import("./todo.js");
    await openTodoDialog({
      mode: "edit",
      role: "maintainer",
      todo: {
        localId: 4,
        title: "Alpha",
        body: "Body",
        columnKey: "backlog",
        createdByUserId: 10,
        createdAt: "2026-11-01T12:00:00Z",
        tags: [],
      },
    });

    expect(__isTodoDialogDirtyForTest()).toBe(false);
    (document.getElementById("todoCreatorEditBtn") as HTMLButtonElement).click();
    await flushPromises();
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    const search = document.getElementById("todoCreatorSearch") as HTMLInputElement;
    search.value = "Grace";
    search.dispatchEvent(new Event("input", { bubbles: true }));
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    expect(getPendingTodoCreatorId()).toBe(11);
    expect(__isTodoDialogDirtyForTest()).toBe(true);

    (document.getElementById("todoCreatorResetBtn") as HTMLButtonElement).click();
    expect(getPendingTodoCreatorId()).toBeNull();
    expect(__isTodoDialogDirtyForTest()).toBe(false);

    (document.getElementById("todoTitle") as HTMLInputElement).value = "Still dirty";
    (document.getElementById("todoCreatorEditBtn") as HTMLButtonElement).click();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    (document.getElementById("todoCreatorResetBtn") as HTMLButtonElement).click();
    expect(getPendingTodoCreatorId()).toBeNull();
    expect(__isTodoDialogDirtyForTest()).toBe(true);
    (document.getElementById("todoTitle") as HTMLInputElement).value = "Alpha";

    (document.getElementById("todoCreatorEditBtn") as HTMLButtonElement).click();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    showConfirmDialogMock.mockResolvedValue(false);
    await expect(requestTodoDialogClose({ reason: "button" })).resolves.toBe(false);
    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
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

  it("shows References immediately when creating a Todo in an authenticated project", async () => {
    fullProject = true;
    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });
    const tab = document.getElementById("todoReferencesTab") as HTMLButtonElement;
    const details = document.getElementById("todoDetailsPanel") as HTMLElement;
    const references = document.getElementById("todoReferencesPanel") as HTMLElement;
    expect(tab.hidden).toBe(false);
    tab.click();
    expect(references.hidden).toBe(false);
    expect(details.hidden).toBe(true);
    expect(tab.getAttribute("aria-selected")).toBe("true");
    (document.getElementById("todoDetailsTab") as HTMLButtonElement).click();
    expect(references.hidden).toBe(true);
    expect(details.hidden).toBe(false);
    tab.click();
    (document.getElementById("todoTitle") as HTMLInputElement).dispatchEvent(new Event("invalid"));
    expect(details.hidden).toBe(false);
    expect(references.hidden).toBe(true);
  });

  it("does not turn a successful create into a failure when its callback rejects", async () => {
    const callback = vi.fn().mockRejectedValue(new Error("placement failed"));
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const { getTodoDialogOpenGeneration, notifyTodoCreated, openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer", onCreated: callback });
    await expect(notifyTodoCreated(
      { id: 1, localId: 1, title: "Created", status: "BACKLOG" } as any,
      getTodoDialogOpenGeneration(),
    )).resolves.toBe(0);
    expect(callback).toHaveBeenCalledTimes(1);
  });
});

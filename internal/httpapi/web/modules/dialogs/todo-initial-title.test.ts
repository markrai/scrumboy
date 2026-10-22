// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import enCatalog from "../i18n/locales/en.json";
import pseudoCatalog from "../i18n/locales/pseudo.json";

// Install the DOM shell the todo dialog pulls in via elements.ts before
// importing the module under test. All fields are stubs: the Title input
// is the only one we assert against; the rest exist so elements.ts's
// getElementById lookups resolve.
function installTodoDom(): HTMLInputElement {
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
        <input id="todoDueAt" type="datetime-local" />
        <div id="todoDialogOverdue" hidden>Overdue</div>
        <div id="todoEstimationField"></div>
        <div id="todoAssigneeField"></div>
        <div id="todoSprintField"></div>
        <div id="todoLinksField"></div>
        <div id="todoDialogCreated"><span class="todo-dialog-datetime-value"></span><span id="todoDialogCreatedBy"></span></div>
        <div id="todoDialogUpdated"><span class="todo-dialog-datetime-value"></span></div>
        <button id="closeTodoBtn"></button>
        <button id="deleteTodoBtn"></button>
        <button id="shareTodoBtn"></button>
        <button id="addTagBtn"></button>
        <div id="tagsChips"></div>
        <button id="saveTodoBtn"></button>
      </form>
    </dialog>
  `;
  return document.getElementById("todoTitle") as HTMLInputElement;
}

describe("openTodoDialog initialTitle seeding", () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.useRealTimers();
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  it("normalizeSeedTitle trims and collapses whitespace/newlines when no maxLength is set", async () => {
    installTodoDom();
    const { __normalizeSeedTitleForTest } = await import("./todo.js");

    expect(__normalizeSeedTitleForTest(undefined)).toBe("");
    expect(__normalizeSeedTitleForTest("")).toBe("");
    expect(__normalizeSeedTitleForTest("  hello  ")).toBe("hello");
    expect(__normalizeSeedTitleForTest("line1\nline2\tline3")).toBe("line1 line2 line3");
    expect(__normalizeSeedTitleForTest("a   b")).toBe("a b");
    expect(__normalizeSeedTitleForTest("   \n\t  ")).toBe("");
  });

  it("normalizeSeedTitle truncates to the Title input's maxLength when set", async () => {
    const input = installTodoDom();
    input.maxLength = 12;
    const { __normalizeSeedTitleForTest } = await import("./todo.js");

    expect(__normalizeSeedTitleForTest("abcdefghijklmnopqrstuvwxyz")).toBe("abcdefghijkl");
    expect(__normalizeSeedTitleForTest("line1\nline2\tline3")).toBe("line1 line2 ");
  });

  it("seeds the Title input in create mode from initialTitle", async () => {
    const input = installTodoDom();

    vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]) }));
    vi.doMock("../state/selectors.js", () => ({
      getBoard: () => ({ columnOrder: [{ key: "backlog", name: "Backlog" }] }),
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
      escapeHTML: (s: string) => s,
      isAnonymousBoard: () => true,
      showToast: vi.fn(),
    }));
    vi.doMock("../sprints.js", () => ({ normalizeSprints: () => [], boardSprintsEnabled: () => true }));
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

    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({
      mode: "create",
      initialTitle: "  sticky\nnote text  ",
      role: "maintainer",
    });

    expect(input.value).toBe("sticky note text");
  });

  it("leaves the Title input empty in create mode when initialTitle is omitted (non-regression)", async () => {
    const input = installTodoDom();
    input.value = "leftover";

    vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]) }));
    vi.doMock("../state/selectors.js", () => ({
      getBoard: () => ({ columnOrder: [{ key: "backlog", name: "Backlog" }] }),
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
      escapeHTML: (s: string) => s,
      isAnonymousBoard: () => true,
      showToast: vi.fn(),
    }));
    vi.doMock("../sprints.js", () => ({ normalizeSprints: () => [], boardSprintsEnabled: () => true }));
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

    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    expect(input.value).toBe("");
  });

  it("updates and resets the overdue header across status, due-date, archived, and create states", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 2, 13, 12, 0, 15, 250));
    installTodoDom();
    (document.getElementById("todoDialog") as HTMLDialogElement).showModal = vi.fn();

    vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]) }));
    vi.doMock("../state/selectors.js", () => ({
      getBoard: () => ({ columnOrder: [
        { key: "doing", name: "Doing", isDone: false },
        { key: "complete", name: "Complete", isDone: true },
      ] }),
      getBoardMembers: () => [],
      getMarkdownNotesEnabled: () => false,
      getMermaidNotesEnabled: () => false,
      getInlineImagesEnabled: () => false,
      getSlug: () => "",
      getTagColors: () => ({}),
      getUser: () => null,
    }));
    vi.doMock("../state/mutations.js", () => ({
      setAvailableTags: vi.fn(), setAvailableTagsMap: vi.fn(), setEditingTodo: vi.fn(), setTagColors: vi.fn(),
    }));
    vi.doMock("../utils.js", () => ({
      escapeHTML: (s: string) => s, isAnonymousBoard: () => true, showToast: vi.fn(),
    }));
    vi.doMock("../sprints.js", () => ({ normalizeSprints: () => [], boardSprintsEnabled: () => true }));
    vi.doMock("./todo-links.js", () => ({
      bindShareTodoButton: vi.fn(), bindTodoDialogLinkLifecycle: vi.fn(), initializeTodoDialogLinks: vi.fn(), resetTodoDialogLinks: vi.fn(),
    }));
    vi.doMock("./todo-permissions.js", () => ({
      computeTodoDialogPermissions: () => ({
        canSubmitTodo: true, canDeleteTodo: false, canEditAssignment: true,
        canChangeEstimation: true, canEditTags: true, canEditNotes: true,
        canEditTitle: true, canEditStatus: true,
      }),
      setTodoFormPermissions: vi.fn(),
    }));
    vi.doMock("./todo-tags.js", () => ({
      getTagsFromChips: () => [], renderTagsChips: vi.fn(), resetTodoTagAutocompleteBindings: vi.fn(), setupTagAutocomplete: vi.fn(),
    }));

    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({ locale: "en", loadLocale: async () => enCatalog });
    const { openTodoDialog } = await import("./todo.js");
    const indicator = document.getElementById("todoDialogOverdue") as HTMLElement;
    const status = document.getElementById("todoStatus") as HTMLSelectElement;
    const due = document.getElementById("todoDueAt") as HTMLInputElement;
    const past = new Date(Date.now() - 60_000).toISOString();
    const futureLocal = new Date(Date.now() + 60_000);
    const localValue = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}T${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;

    await openTodoDialog({ mode: "edit", todo: { title: "Late", status: "doing", columnKey: "doing", dueAt: past, tags: [] }, role: "maintainer" });
    expect(indicator.hidden).toBe(false);

    status.value = "complete";
    status.dispatchEvent(new Event("change"));
    expect(indicator.hidden).toBe(true);
    status.value = "doing";
    status.dispatchEvent(new Event("change"));
    expect(indicator.hidden).toBe(false);

    const preciseFuture = new Date(Date.now() + 30_000).toISOString();
    await openTodoDialog({ mode: "edit", todo: { title: "Soon", status: "doing", columnKey: "doing", dueAt: preciseFuture, tags: [] }, role: "maintainer" });
    expect(indicator.hidden).toBe(true);

    due.value = localValue(futureLocal);
    due.dispatchEvent(new Event("input"));
    expect(indicator.hidden).toBe(true);

    await openTodoDialog({ mode: "edit", todo: { title: "Archived", status: "doing", columnKey: "doing", dueAt: past, archivedAt: past, tags: [] }, role: "maintainer" });
    expect(indicator.hidden).toBe(true);
    await openTodoDialog({ mode: "create", status: "doing", role: "maintainer" });
    expect(indicator.hidden).toBe(true);
  });

  it("renders unknown sprint states as raw text in the sprint select", async () => {
    installTodoDom();
    const showModal = vi.fn();
    (document.getElementById("todoDialog") as HTMLDialogElement).showModal = showModal;

    vi.doMock("../api.js", () => ({
      apiFetch: vi.fn(async (path: string) => {
        if (path.endsWith("/tags")) return [];
        if (path.endsWith("/sprints")) {
          return { sprints: [{ id: 7, name: "Sprint 7", state: "PAUSED" }] };
        }
        return [];
      }),
    }));
    vi.doMock("../state/selectors.js", () => ({
      getBoard: () => ({ columnOrder: [{ key: "backlog", name: "Backlog" }] }),
      getBoardMembers: () => [],
      getMarkdownNotesEnabled: () => false,
      getMermaidNotesEnabled: () => false,
      getInlineImagesEnabled: () => false,
      getSlug: () => "demo-board",
      getTagColors: () => ({}),
      getUser: () => ({ id: 1, name: "Ada" }),
    }));
    vi.doMock("../state/mutations.js", () => ({
      setAvailableTags: vi.fn(),
      setAvailableTagsMap: vi.fn(),
      setEditingTodo: vi.fn(),
      setTagColors: vi.fn(),
    }));
    vi.doMock("../utils.js", () => ({
      escapeHTML: (s: string) => s,
      isAnonymousBoard: () => false,
      showConfirmDialog: vi.fn(),
      showToast: vi.fn(),
    }));
    vi.doMock("../sprints.js", () => ({ normalizeSprints: (res: { sprints?: unknown[] } | null) => res?.sprints ?? [], boardSprintsEnabled: () => true }));
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

    const { openTodoDialog } = await import("./todo.js");
    await openTodoDialog({ mode: "create", role: "maintainer" });

    const sprintSelect = document.getElementById("todoSprint") as HTMLSelectElement;
    expect(Array.from(sprintSelect.options).map((option) => option.textContent)).toContain("Sprint 7 (PAUSED)");
    expect(showModal).toHaveBeenCalled();
  });

  it("shows historical creator attribution only while the creator resolves as a current member", async () => {
    installTodoDom();
    const dialog = document.getElementById("todoDialog") as HTMLDialogElement;
    dialog.showModal = vi.fn();
    let members = [{ userId: 7, name: "Ada", email: "ada@example.com", role: "maintainer" }];

    vi.doMock("../api.js", () => ({ apiFetch: vi.fn().mockResolvedValue([]) }));
    vi.doMock("../state/selectors.js", () => ({
      getBoard: () => ({ columnOrder: [{ key: "backlog", name: "Backlog" }] }),
      getBoardMembers: () => members,
      getMarkdownNotesEnabled: () => false,
      getMermaidNotesEnabled: () => false,
      getInlineImagesEnabled: () => false,
      getSlug: () => "",
      getTagColors: () => ({}),
      getUser: () => ({ id: 9, name: "Grace" }),
    }));
    vi.doMock("../state/mutations.js", () => ({
      setAvailableTags: vi.fn(),
      setAvailableTagsMap: vi.fn(),
      setEditingTodo: vi.fn(),
      setTagColors: vi.fn(),
    }));
    vi.doMock("../utils.js", () => ({
      escapeHTML: (s: string) => s,
      isAnonymousBoard: () => false,
      showConfirmDialog: vi.fn(),
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
        canSubmitTodo: true,
        canDeleteTodo: true,
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

    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: async (locale) => (locale === "pseudo" ? pseudoCatalog : enCatalog),
      storage: null,
    });
    const { openTodoDialog } = await import("./todo.js");
    const todo = { title: "Attributed", columnKey: "backlog", createdByUserId: 7, tags: [] };
    await openTodoDialog({ mode: "edit", todo, role: "maintainer" });
    const createdBy = document.getElementById("todoDialogCreatedBy") as HTMLElement;
    expect(createdBy.textContent).toBe("by Ada");

    await i18n.setLocale("pseudo");
    expect(createdBy.textContent).toBe("[!! by Ada !!]");

    dialog.dispatchEvent(new Event("close"));
    createdBy.textContent = "closed";
    await i18n.setLocale("en");
    expect(createdBy.textContent).toBe("closed");

    members = [];
    await openTodoDialog({ mode: "edit", todo, role: "maintainer" });
    expect(createdBy.textContent).toBe("");
  });
});

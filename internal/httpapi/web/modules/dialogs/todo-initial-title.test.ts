// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
        <div id="todoEstimationField"></div>
        <div id="todoAssigneeField"></div>
        <div id="todoSprintField"></div>
        <div id="todoLinksField"></div>
        <div id="todoDialogCreated"><span class="todo-dialog-datetime-value"></span></div>
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
});

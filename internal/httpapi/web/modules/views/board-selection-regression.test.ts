// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";

const archiveTodosMock = vi.hoisted(() => vi.fn());

vi.mock("../dialogs/bulk-edit.js", () => ({
  initBulkEditDialog: vi.fn(),
  openBulkEditDialog: vi.fn(),
}));
vi.mock("../api.js", () => ({
  archiveTodos: archiveTodosMock,
}));

const enCatalog = {
  "board.selection.multiple": "Edit {count} selected",
  "board.selection.single": "Edit 1 selected",
  "board.bulkArchive.limit": "Select no more than 500 stories",
  "board.select.toggle": "Select",
  "board.select.done": "Done",
  "board.select.all": "Select all ({count})",
  "board.select.notLoaded": "{count} more not loaded",
  "board.loadMore": "Load more",
  "board.todo.dragCard": "Drag card",
  "board.bulkArchive.archivedMultiple": "Archived {count} stories",
};

function boardFixture() {
  document.body.innerHTML = `
    <div id="toast"></div>
    <dialog id="settingsDialog"></dialog>
    <div id="bulkEditBar" style="display: none"></div>
    <button id="bulkEditBarBtn" type="button"></button>
    <button id="bulkArchiveBarBtn" type="button" hidden></button>
    <button id="bulkSelectAllBtn" type="button" hidden></button>
    <button id="bulkClearBtn" type="button" hidden></button>
    <span id="bulkSelectHint" hidden></span>
    <button id="boardSelectToggle" type="button"><svg class="lucide lucide-copy-check" aria-hidden="true"></svg><span class="board-select-toggle__label"></span></button>
    <div class="board">
      <section class="col"><div class="col__list">
        <button data-todo-id="1" class="card"></button>
        <button data-todo-id="2" class="card"></button>
        <button data-todo-id="3" class="card"></button>
      </div></section>
      <section class="col"><div class="col__list">
        <button data-todo-id="4" class="card"></button>
        <button data-todo-id="5" class="card"></button>
      </div></section>
    </div>
  `;
}

async function loadSelectionModule() {
  return await import("./board-selection.js");
}

describe("board selection regressions", () => {
  beforeEach(async () => {
    vi.resetModules();
    boardFixture();
    archiveTodosMock.mockReset();
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async () => enCatalog),
    });
  });

  afterEach(async () => {
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  it("keeps the toolbar for one selected todo but disables the bulk edit action", async () => {
    const selection = await loadSelectionModule();
    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);

    expect((document.getElementById("bulkEditBar") as HTMLElement).style.display).toBe("");
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("Edit 1 selected");
    expect((document.getElementById("bulkEditBarBtn") as HTMLButtonElement).disabled).toBe(true);

    selection.toggleTodoSelection(2);

    expect((document.getElementById("bulkEditBarBtn") as HTMLButtonElement).disabled).toBe(false);
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("Edit 2 selected");
  });

  it("scopes Ctrl+A to the member board and preserves inputs, dialogs, and public boards", async () => {
    const selection = await loadSelectionModule();
    selection.ensureBulkEditUi({ getRole: () => "contributor", syncSelectionClasses: () => {} });
    selection.clearTodoMultiSelection();

    const input = document.createElement("input");
    document.body.appendChild(input);
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true }));
    expect(selection.getSelectedTodoIds().size).toBe(0);

    const dialog = document.createElement("dialog");
    dialog.setAttribute("open", "");
    document.body.appendChild(dialog);
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true }));
    expect(selection.getSelectedTodoIds().size).toBe(0);
    dialog.remove();

    // No board context (focus outside the board, nothing selected): browser Select-all is left alone.
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true }));
    expect(selection.getSelectedTodoIds().size).toBe(0);

    // Focus inside the board selects every visible card.
    document.querySelector('[data-todo-id="1"]')?.dispatchEvent(new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true }));
    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([1, 2, 3, 4, 5]);

    // Select mode counts as board context even when focus is outside the board.
    selection.clearTodoMultiSelection();
    selection.setSelectMode(true);
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true }));
    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([1, 2, 3, 4, 5]);

    selection.clearTodoMultiSelection();
    document.body.innerHTML = `
      <div class="board" data-public-board>
        <button data-todo-id="9" class="card"></button>
      </div>
    `;
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true }));
    expect(selection.getSelectedTodoIds().size).toBe(0);
  });

  it("never opens the bulk dialog for a single selected todo", async () => {
    const selection = await loadSelectionModule();
    const bulkEdit = await import("../dialogs/bulk-edit.js");
    const openSpy = bulkEdit.openBulkEditDialog as unknown as Mock;
    selection.ensureBulkEditUi({ getRole: () => "contributor", syncSelectionClasses: () => {} });

    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);
    (document.getElementById("bulkEditBarBtn") as HTMLButtonElement).click();
    expect(openSpy).not.toHaveBeenCalled();

    selection.toggleTodoSelection(2);
    (document.getElementById("bulkEditBarBtn") as HTMLButtonElement).click();
    expect(openSpy).toHaveBeenCalledTimes(1);
    expect(openSpy.mock.calls[0][0].sort()).toEqual([1, 2]);
  });

  it("syncs card aria-pressed with selection state", async () => {
    const selection = await loadSelectionModule();
    selection.clearTodoMultiSelection();

    selection.toggleTodoSelection(1);
    const card1 = document.querySelector('[data-todo-id="1"]') as HTMLElement;
    expect(card1.classList.contains("card--selected")).toBe(true);
    expect(card1.getAttribute("aria-pressed")).toBe("true");

    selection.toggleTodoSelection(1);
    expect(card1.getAttribute("aria-pressed")).toBe("false");

    selection.toggleTodoSelection(2);
    selection.selectRangeTo(4);
    for (const id of [2, 3, 4]) {
      expect(document.querySelector(`[data-todo-id="${id}"]`)?.getAttribute("aria-pressed")).toBe("true");
    }

    selection.clearTodoMultiSelection();
    expect(document.querySelectorAll(".card--selected")).toHaveLength(0);
    expect(document.querySelector('[data-todo-id="2"]')?.getAttribute("aria-pressed")).toBe("false");
  });

  it("filter refresh re-render drops removed stories so they cannot be bulk-edited or archived", async () => {
    const mutations = await import("../state/mutations.js");
    const selection = await loadSelectionModule();
    const bulkEdit = await import("../dialogs/bulk-edit.js");
    const openSpy = bulkEdit.openBulkEditDialog as unknown as Mock;
    const boardView = await import("./board.js");
    selection.ensureBulkEditUi({ getRole: () => "maintainer", syncSelectionClasses: () => {} });

    // Start with everything selected, as before a filter change.
    selection.selectAllVisible();
    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([1, 2, 3, 4, 5]);

    // Run the real board refresh/filter path with a payload that filters out every
    // story but one. This guards the original bug where the refresh bypassed
    // pruning and the removed stories stayed selected for bulk edit / archive.
    const keptTodo = { id: 1, localId: 11, title: "One", status: "backlog", tags: [] as string[] };
    const filteredBoard = {
      project: { id: 1, name: "Alpha", slug: "alpha", dominantColor: "#000000", creatorUserId: 1 },
      tags: [],
      columnOrder: [
        { key: "backlog", name: "Backlog" },
        { key: "doing", name: "Doing" },
      ],
      columns: { backlog: [keptTodo], doing: [] },
    };
    mutations.setSlug("alpha");
    archiveTodosMock.mockResolvedValue({ transitionedCount: 1 });
    boardView.updateBoardContent(filteredBoard as never, [], "", null, null, null, null);

    // Removed stories are deselected; the survivor keeps class + aria in sync.
    expect(Array.from(selection.getSelectedTodoIds())).toEqual([1]);
    const survivor = document.querySelector('[data-todo-id="1"]') as HTMLElement;
    expect(survivor.classList.contains("card--selected")).toBe(true);
    expect(survivor.getAttribute("aria-pressed")).toBe("true");
    expect(document.querySelectorAll(".card--selected")).toHaveLength(1);

    // A single survivor cannot be bulk-edited.
    expect((document.getElementById("bulkEditBarBtn") as HTMLButtonElement).disabled).toBe(true);
    openSpy.mockClear();
    (document.getElementById("bulkEditBarBtn") as HTMLButtonElement).click();
    expect(openSpy).not.toHaveBeenCalled();

    // Archiving only covers the story still on the board, never the removed ones.
    (document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement).click();
    await vi.waitFor(() => expect(archiveTodosMock).toHaveBeenCalledTimes(1));
    expect(archiveTodosMock).toHaveBeenCalledWith("alpha", [11]);
  });

  it("ignores a second archive click while one batch is in flight", async () => {
    const mutations = await import("../state/mutations.js");
    mutations.setSlug("alpha");
    mutations.setBoard({
      project: { id: 1, name: "Alpha", slug: "alpha", dominantColor: "#000000", creatorUserId: 1 },
      tags: [],
      columns: {
        backlog: [
          { id: 1, localId: 12, title: "One", status: "backlog" },
          { id: 2, localId: 14, title: "Two", status: "backlog" },
        ],
      },
    });
    archiveTodosMock.mockImplementation(() => new Promise(() => {}));
    const selection = await loadSelectionModule();
    selection.ensureBulkEditUi({ getRole: () => "maintainer", syncSelectionClasses: () => {} });
    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);
    selection.toggleTodoSelection(2);

    const archiveButton = document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement;
    archiveButton.click();
    archiveButton.click();

    expect(archiveTodosMock).toHaveBeenCalledTimes(1);
    expect(archiveTodosMock).toHaveBeenCalledWith("alpha", [12, 14]);
    expect(archiveButton.disabled).toBe(true);
  });

  it("excludes hidden lanes from the select-all count and the unloaded hint", async () => {
    const cols = document.querySelectorAll(".col");
    cols[0].setAttribute("data-column", "backlog");
    cols[1].setAttribute("data-column", "doing");
    (cols[1] as HTMLElement).style.display = "none";
    const mutations = await import("../state/mutations.js");
    mutations.setBoard({
      project: { id: 1, name: "A", slug: "a", dominantColor: "#000000", creatorUserId: 1 },
      tags: [],
      columns: {
        backlog: [
          { id: 1, localId: 1, title: "One", status: "backlog" },
          { id: 2, localId: 2, title: "Two", status: "backlog" },
          { id: 3, localId: 3, title: "Three", status: "backlog" },
        ],
        doing: [
          { id: 4, localId: 4, title: "Four", status: "doing" },
          { id: 5, localId: 5, title: "Five", status: "doing" },
        ],
      },
    } as never);
    mutations.setBoardLaneMeta({
      backlog: { hasMore: true, nextCursor: "x", loading: false, totalCount: 10 },
      doing: { hasMore: true, nextCursor: "y", loading: false, totalCount: 20 },
    } as never);
    const selection = await loadSelectionModule();
    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);

    expect(document.getElementById("bulkSelectAllBtn")?.textContent).toBe("Select all (3)");
    const hint = document.getElementById("bulkSelectHint") as HTMLElement;
    expect(hint.hidden).toBe(false);
    expect(hint.textContent).toBe("7 more not loaded");
    expect((document.getElementById("bulkSelectAllBtn") as HTMLButtonElement).title).toBe("7 more not loaded");
    expect((document.getElementById("bulkSelectAllBtn") as HTMLButtonElement).getAttribute("aria-label")).toBe(
      "Select all (3). 7 more not loaded",
    );

    // The warning stays visible as text (no hover needed) even with nothing selected in select mode.
    selection.setSelectMode(true);
    selection.clearTodoMultiSelection({ keepSelectMode: true });
    expect((document.getElementById("bulkSelectHint") as HTMLElement).hidden).toBe(false);
    expect((document.getElementById("bulkSelectHint") as HTMLElement).textContent).toBe("7 more not loaded");
  });
});

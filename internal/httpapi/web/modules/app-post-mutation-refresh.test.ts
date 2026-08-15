// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from "vitest";

const { apiFetchMock, loadBoardBySlugMock, state } = vi.hoisted(() => ({
  apiFetchMock: vi.fn().mockResolvedValue({}),
  loadBoardBySlugMock: vi.fn().mockResolvedValue(undefined),
  state: {
    editingTodo: null as any,
  },
}));

vi.mock("../dist/dom/elements.js", () => ({
  app: document.getElementById("app"),
  toast: document.getElementById("toast"),
  todoDialog: document.getElementById("todoDialog"),
  todoForm: document.getElementById("todoForm"),
  todoDialogTitle: document.getElementById("todoDialogTitle"),
  todoTitle: document.getElementById("todoTitle"),
  todoBody: document.getElementById("todoBody"),
  todoTags: document.getElementById("todoTags"),
  todoStatus: document.getElementById("todoStatus"),
  todoEstimationPoints: document.getElementById("todoEstimationPoints"),
  todoPriority: document.getElementById("todoPriority"),
  deleteTodoBtn: document.getElementById("deleteTodoBtn"),
  closeTodoBtn: document.getElementById("closeTodoBtn"),
  settingsDialog: document.getElementById("settingsDialog"),
  closeSettingsBtn: document.getElementById("closeSettingsBtn"),
}));

vi.mock("../dist/theme.js", () => ({
  initTheme: vi.fn(),
  handleThemeChange: vi.fn(),
  getStoredTheme: vi.fn(),
  THEME_SYSTEM: "system",
  THEME_DARK: "dark",
  THEME_LIGHT: "light",
}));

vi.mock("../dist/utils.js", () => ({
  escapeHTML: (value: string) => value,
  showToast: vi.fn(),
  showConfirmDialog: vi.fn().mockResolvedValue(true),
}));

vi.mock("../dist/api.js", () => ({ apiFetch: apiFetchMock }));
vi.mock("../dist/router.js", () => ({
  navigate: vi.fn(),
  router: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../dist/state/selectors.js", () => ({
  getRoute: vi.fn(),
  getProjectId: vi.fn(() => 1),
  getBoard: vi.fn(() => ({ project: { id: 1, slug: "alpha" }, columns: {} })),
  getAuthStatusAvailable: vi.fn(() => true),
  getMobileTab: vi.fn(() => "backlog"),
  getSlug: vi.fn(() => "alpha"),
  getTag: vi.fn(() => "bug"),
  getSearch: vi.fn(() => "find"),
  getSprintIdFromUrl: vi.fn(() => "7"),
  getAssigneeFromUrl: vi.fn(() => "42"),
  getSortFromUrl: vi.fn(() => "newest"),
  getPriorityFromUrl: vi.fn(() => "high"),
  getProjectView: vi.fn(),
  getProjectsTab: vi.fn(),
  getProjects: vi.fn(() => []),
  getSettingsProjectId: vi.fn(),
  getEditingTodo: vi.fn(() => state.editingTodo),
  getAvailableTags: vi.fn(() => []),
  getAutocompleteSuggestion: vi.fn(),
  getAvailableTagsMap: vi.fn(() => new Map()),
  getTagColors: vi.fn(() => ({})),
  getUser: vi.fn(() => ({ id: 1 })),
  getSettingsActiveTab: vi.fn(),
  getBackupImportBtn: vi.fn(),
  getBackupData: vi.fn(),
  getBackupPreview: vi.fn(),
  getAuthStatusChecked: vi.fn(() => true),
}));

vi.mock("../dist/state/mutations.js", () => ({
  setProjectId: vi.fn(),
  setBoard: vi.fn(),
  setSlug: vi.fn(),
  setTag: vi.fn(),
  setMobileTab: vi.fn(),
  setProjects: vi.fn(),
  setProjectsTab: vi.fn(),
  setProjectView: vi.fn(),
  setEditingTodo: vi.fn((todo: any) => {
    state.editingTodo = todo;
  }),
  setAvailableTags: vi.fn(),
  setAvailableTagsMap: vi.fn(),
  setAutocompleteSuggestion: vi.fn(),
  setTagColors: vi.fn(),
  setSettingsProjectId: vi.fn(),
  setSettingsActiveTab: vi.fn(),
  setBackupImportBtn: vi.fn(),
  setBackupData: vi.fn(),
  setBackupPreview: vi.fn(),
}));

vi.mock("../dist/dialogs/todo.js", () => ({
  openTodoDialog: vi.fn(),
  renderTagsChips: vi.fn(),
  setupTagAutocomplete: vi.fn(),
  removeTag: vi.fn(),
  renderTagAutocomplete: vi.fn(),
  getTagsFromChips: vi.fn(() => []),
  resetAssigneeSelect: vi.fn(),
  getTodoFormPermissions: vi.fn(() => ({ canSubmitTodo: true })),
  requestTodoDialogClose: vi.fn().mockResolvedValue(true),
}));

vi.mock("../dist/dialogs/todo-submit.js", () => ({
  buildTodoCreatePayload: vi.fn(() => ({})),
  buildTodoPatchPayload: vi.fn(() => ({})),
  shouldSubmitSprintAssignment: vi.fn(() => false),
}));

vi.mock("../dist/dialogs/settings.js", () => ({
  renderSettingsModal: vi.fn().mockResolvedValue(undefined),
  invalidateTagsCache: vi.fn(),
  resumeAuthenticationMethodFlow: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../dist/features/drag-drop.js", () => ({
  initDnD: vi.fn(),
  columnsSpec: vi.fn(),
  dragInProgress: false,
  dragJustEnded: false,
}));
vi.mock("../dist/features/context-menu.js", () => ({ setupContextMenuCloseHandler: vi.fn() }));
vi.mock("../dist/features/context-menu-button.js", () => ({ setupContextMenuButtonHandler: vi.fn() }));
vi.mock("../dist/views/index.js", () => ({
  loadBoardBySlug: loadBoardBySlugMock,
  onTodoDialogClosed: vi.fn(),
}));
vi.mock("../dist/realtime/guard.js", () => ({ recordLocalMutation: vi.fn() }));
vi.mock("../dist/pwaUpdate.js", () => ({ registerPwaGlobals: vi.fn() }));
vi.mock("../dist/core/keybindings.js", () => ({ initKeybindings: vi.fn() }));
vi.mock("../dist/core/modal-outside-click.js", () => ({ initModalOutsideClickClose: vi.fn() }));
vi.mock("../dist/i18n/index.js", () => ({
  I18N_LOCALE_CHANGED: "i18n:locale-changed",
  apiErrorMessage: vi.fn(() => "error"),
  hydrateI18n: vi.fn(),
  initI18n: vi.fn().mockResolvedValue(undefined),
  t: (key: string) => key,
}));
vi.mock("../dist/i18n/qa.js", () => ({ installI18nQa: vi.fn() }));
vi.mock("../dist/sprints.js", () => ({ boardSprintsEnabled: vi.fn(() => false) }));

function installDom(): void {
  document.body.innerHTML = `
    <div id="app"></div>
    <div id="toast"></div>
    <dialog id="todoDialog"></dialog>
    <form id="todoForm">
      <h2 id="todoDialogTitle"></h2>
      <input id="todoTitle" value="Edited title" />
      <textarea id="todoBody"></textarea>
      <input id="todoTags" />
      <select id="todoStatus"><option value="backlog" selected>Backlog</option></select>
      <select id="todoEstimationPoints"><option value="" selected></option></select>
      <select id="todoPriority"><option value="low" selected>Low</option></select>
      <select id="todoAssignee"><option value="99" selected>User 99</option></select>
      <select id="todoSprint"><option value="7" selected>Sprint 7</option></select>
      <div id="todoSprintField"></div>
      <button id="deleteTodoBtn" type="button">Delete</button>
      <button id="closeTodoBtn" type="button">Close</button>
    </form>
    <dialog id="settingsDialog"></dialog>
    <button id="closeSettingsBtn" type="button">Close settings</button>
  `;
}

async function loadApp(): Promise<void> {
  await import("../app.js");
}

async function expectContextCompleteReload(): Promise<void> {
  await vi.waitFor(() => expect(loadBoardBySlugMock).toHaveBeenCalledTimes(1));
  expect(loadBoardBySlugMock).toHaveBeenCalledWith(
    "alpha",
    "bug",
    "find",
    "7",
    "42",
    "newest",
    "high",
  );
}

describe("app post-mutation board refresh context", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    installDom();
    window.history.replaceState({}, "", "/alpha?assignee=42&sort=newest&priority=high");
    state.editingTodo = {
      localId: 4,
      columnKey: "backlog",
      status: "Backlog",
      sprintId: null,
    };
  });

  it("preserves active board filters after saving and deleting a todo", async () => {
    await loadApp();

    document.getElementById("todoForm")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    await expectContextCompleteReload();
    const todoSubmit = await import("../dist/dialogs/todo-submit.js");
    expect(todoSubmit.buildTodoPatchPayload).toHaveBeenCalledWith(expect.objectContaining({
      assigneeUserId: 99,
      priorityKey: "low",
    }));

    // Creation changes the visible order under newest/oldest sorting, so it
    // must use the same context-complete follow-up load as edits and moves.
    loadBoardBySlugMock.mockClear();
    state.editingTodo = null;
    document.getElementById("todoForm")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await expectContextCompleteReload();

    loadBoardBySlugMock.mockClear();
    state.editingTodo = {
      localId: 4,
      columnKey: "backlog",
      status: "Backlog",
      sprintId: null,
    };

    (document.getElementById("deleteTodoBtn") as HTMLButtonElement).click();

    await expectContextCompleteReload();
  });
});

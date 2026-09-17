import { initBulkEditDialog, openBulkEditDialog } from '../dialogs/bulk-edit.js';
import { archiveTodos } from '../api.js';
import { apiErrorMessage, I18N_LOCALE_CHANGED, t } from '../i18n/index.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { recordLocalMutation, setBulkUpdating } from '../realtime/guard.js';
import { getAssigneeFromUrl, getBoard, getPriorityFromUrl, getSearch, getSlug, getSortFromUrl, getSprintIdFromUrl, getTagsFromUrl } from '../state/selectors.js';
import { isTemporaryBoard, showToast } from '../utils.js';

let selectedTodoIds = new Set<number>();
let bulkEditUiInitialized = false;
let getCurrentRole: (() => string | null) | null = null;
const ARCHIVE_BATCH_MAX = 500;

function selectionLabel(count: number): string {
  return count === 1
    ? t("board.selection.single")
    : t("board.selection.multiple", { count });
}

export function __selectionLabelForTest(count: number): string {
  return selectionLabel(count);
}

export function getSelectedTodoIds(): Set<number> {
  return selectedTodoIds;
}

export function clearTodoMultiSelection(): void {
  selectedTodoIds.clear();
  const bar = document.getElementById("bulkEditBar");
  const btn = document.getElementById("bulkEditBarBtn");
  if (bar) bar.style.display = "none";
  if (btn) btn.textContent = "";
  const archiveBtn = document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement | null;
  if (archiveBtn) archiveBtn.hidden = true;
  document.querySelectorAll(".board .card--selected").forEach((el) => el.classList.remove("card--selected"));
}

export function updateBulkEditBar(): void {
  const bar = document.getElementById("bulkEditBar");
  const btn = document.getElementById("bulkEditBarBtn");
  if (!bar || !btn) return;
  const n = selectedTodoIds.size;
  if (n >= 2) {
    bar.style.display = "";
    btn.textContent = selectionLabel(n);
  } else {
    bar.style.display = "none";
    btn.textContent = "";
  }
  const archiveBtn = document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement | null;
  if (archiveBtn) {
    const canArchive = getCurrentRole?.() === "maintainer" || isTemporaryBoard(getBoard());
    archiveBtn.hidden = n < 2 || !canArchive;
    archiveBtn.disabled = n > ARCHIVE_BATCH_MAX;
    archiveBtn.title = n > ARCHIVE_BATCH_MAX ? t("board.bulkArchive.limit") : "";
  }
}

function selectedLocalIds(): number[] {
  const ids = new Set(selectedTodoIds);
  const localIds: number[] = [];
  for (const todos of Object.values(getBoard()?.columns || {})) {
    for (const todo of todos) {
      if (ids.has(todo.id) && !todo.archivedAt) localIds.push(todo.localId);
    }
  }
  return localIds;
}

async function archiveSelection(): Promise<void> {
  const slug = getSlug();
  const localIds = selectedLocalIds();
  const canArchive = getCurrentRole?.() === "maintainer" || isTemporaryBoard(getBoard());
  if (!slug || !canArchive || localIds.length === 0) return;
  if (localIds.length > ARCHIVE_BATCH_MAX) {
    showToast(t("board.bulkArchive.limit"));
    return;
  }
  setBulkUpdating(true);
  try {
    recordLocalMutation();
    const result = await archiveTodos(slug, localIds);
    clearTodoMultiSelection();
    showToast(t("board.bulkArchive.archivedMultiple", { count: result.transitionedCount }));
    await invalidateBoard(slug, getTagsFromUrl(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl(), true);
  } catch (error) {
    showToast(apiErrorMessage(error, { fallbackKey: "board.bulkArchive.failed" }));
    updateBulkEditBar();
  } finally {
    setBulkUpdating(false);
  }
}

export function toggleTodoSelection(id: number): void {
  if (selectedTodoIds.has(id)) selectedTodoIds.delete(id);
  else selectedTodoIds.add(id);
  updateBulkEditBar();
  const card = document.querySelector(`[data-todo-id="${id}"]`);
  if (card) card.classList.toggle("card--selected", selectedTodoIds.has(id));
}

export function ensureBulkEditUi(opts: {
  getRole: () => string | null;
  syncSelectionClasses: (selectedIds: ReadonlySet<number>) => void;
}): void {
  getCurrentRole = opts.getRole;
  if (bulkEditUiInitialized) return;
  bulkEditUiInitialized = true;
  initBulkEditDialog(() => {
    clearTodoMultiSelection();
    updateBulkEditBar();
  });
  document.addEventListener(I18N_LOCALE_CHANGED, () => {
    updateBulkEditBar();
  });
  const barBtn = document.getElementById("bulkEditBarBtn");
  barBtn?.addEventListener("click", () => {
    void openBulkEditDialog(Array.from(selectedTodoIds), {
      role: opts.getRole(),
      onPruned: (remaining) => {
        selectedTodoIds = new Set(remaining);
        updateBulkEditBar();
        opts.syncSelectionClasses(selectedTodoIds);
      },
    });
  });
  document.getElementById("bulkArchiveBarBtn")?.addEventListener("click", () => {
    void archiveSelection();
  });
}

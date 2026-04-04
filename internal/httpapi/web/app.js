/* global Sortable */

import { app, toast, todoDialog, todoForm, todoDialogTitle, todoTitle, todoBody, todoTags, todoStatus, todoEstimationPoints, deleteTodoBtn, closeTodoBtn, settingsDialog, closeSettingsBtn } from './dist/dom/elements.js';
import { initTheme, handleThemeChange, getStoredTheme, THEME_SYSTEM, THEME_DARK, THEME_LIGHT } from './dist/theme.js';
import { escapeHTML, showToast } from './dist/utils.js';
import { apiFetch } from './dist/api.js';
import { navigate, router } from './dist/router.js';
import { getRoute, getProjectId, getBoard, getAuthStatusAvailable, getMobileTab, getSlug, getTag, getSearch, getSprintIdFromUrl, getProjectView, getProjectsTab, getProjects, getSettingsProjectId, getEditingTodo, getAvailableTags, getAutocompleteSuggestion, getAvailableTagsMap, getTagColors, getUser, getSettingsActiveTab, getBackupImportBtn, getBackupData, getBackupPreview, getAuthStatusChecked } from './dist/state/selectors.js';
import { setProjectId, setBoard, setSlug, setTag, setMobileTab, setProjects, setProjectsTab, setProjectView, setEditingTodo, setAvailableTags, setAvailableTagsMap, setAutocompleteSuggestion, setTagColors, setSettingsProjectId, setSettingsActiveTab, setBackupImportBtn, setBackupData, setBackupPreview } from './dist/state/mutations.js';
import { openTodoDialog, renderTagsChips, setupTagAutocomplete, removeTag, renderTagAutocomplete, getTagsFromChips, resetAssigneeSelect, getTodoFormPermissions } from './dist/dialogs/todo.js';
import { renderSettingsModal, invalidateTagsCache } from './dist/dialogs/settings.js';
import { initDnD, columnsSpec, dragInProgress, dragJustEnded } from './dist/features/drag-drop.js';
import { setupContextMenuCloseHandler } from './dist/features/context-menu.js';
import { setupContextMenuButtonHandler } from './dist/features/context-menu-button.js';
import { loadBoardBySlug, onTodoDialogClosed } from './dist/views/index.js';
import { recordLocalMutation } from './dist/realtime/guard.js';
import { registerPwaGlobals } from './dist/pwaUpdate.js';
import { initKeybindings } from './dist/core/keybindings.js';
import { initModalOutsideClickClose } from './dist/core/modal-outside-click.js';

let tagInputHandlersSetup = false;
const ALLOWED_ESTIMATION_POINTS = new Set([1, 2, 3, 5, 8, 13, 20, 40]);

// PWA update: register service worker and "New version available" dialog (must run early)
registerPwaGlobals();

// Initialize theme on page load
initTheme();

// Setup context menu handlers (one-time, global)
setupContextMenuCloseHandler();
setupContextMenuButtonHandler();

initModalOutsideClickClose();
initKeybindings({
  openSettings: async () => {
    setSettingsActiveTab("profile");
    await renderSettingsModal();
    settingsDialog.showModal();
  },
});

// User avatar button: delegated so it works on dashboard/projects/board even if a cached view bundle didn't bind it
app.addEventListener("click", async (e) => {
  if (!e.target.closest("#userAvatarBtn")) return;
  e.preventDefault();
  setSettingsActiveTab("profile");
  await renderSettingsModal();
  settingsDialog.showModal();
});

// Board back-to-projects (Esc) is handled by dist/core/keybindings.js (executeAction boardEscapeBack).
// Avatar keyboard activation uses native <button> click (Enter/Space); no separate keydown listener.

// renderAuth moved to modules/views/auth.ts
// renderNotFound moved to modules/views/notfound.ts
// renderBoardFromData and loadBoardBySlug moved to modules/views/board.ts

// renderProjects moved to modules/views/projects.ts

// columnsSpec moved to modules/features/drag-drop.ts

// setTagParam, renderTodoCard, findTodoInBoard, updateMobileTabs moved to modules/views/board.ts
// refreshCountsFromDOM removed - verified unused (orphan code)

// openTodoDialog and setMoveButtonsEnabled moved to modules/dialogs/todo.ts


// Tag autocomplete functions moved to modules/dialogs/todo.ts

// renderSettingsModal, updateTagColor, and deleteTag moved to modules/dialogs/settings.ts
// getTagColor and handleProjectImageUpload moved to modules/views/board.ts

closeTodoBtn.addEventListener("click", () => {
  setAutocompleteSuggestion(null);
  renderTagAutocomplete();
  todoDialog.close();
});
closeSettingsBtn.addEventListener("click", () => settingsDialog.close());

// Clear autocomplete and reset assignee select when dialog closes
function cleanupTodoDialogUrlOnClose() {
  const currentSlug = getSlug();
  if (!currentSlug) return;
  const url = new URL(window.location.href);
  const m = url.pathname.match(/^\/([a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)\/t\/\d+\/?$/);
  if (!m) return;
  if (m[1] !== currentSlug) return;
  history.replaceState({}, "", `/${currentSlug}${url.search}`);
}

todoDialog.addEventListener("close", () => {
  setEditingTodo(null);
  onTodoDialogClosed();
  cleanupTodoDialogUrlOnClose();
  setAutocompleteSuggestion(null);
  renderTagAutocomplete();
  resetAssigneeSelect();
});

deleteTodoBtn.addEventListener("click", async () => {
  const todo = getEditingTodo();
  if (!todo) return;
  if (!confirm("Delete this todo?")) return;
  try {
    recordLocalMutation();
    await apiFetch(`/api/board/${getSlug()}/todos/${todo.localId}`, { method: "DELETE" });
    setEditingTodo(null);
    onTodoDialogClosed();
    todoDialog.close();
    await loadBoardBySlug(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
  } catch (err) {
    showToast(err.message);
  }
});

todoForm.addEventListener("submit", async (e) => {
  e.preventDefault();

  if (!getTodoFormPermissions().canSubmitTodo) {
    return;
  }

  const title = todoTitle.value;
  const body = todoBody.value;
  const tags = getTagsFromChips();
  const columnKey = todoStatus.value;
  const estimationRaw = todoEstimationPoints?.value ?? "";
  const estimationParsed = estimationRaw === "" ? null : Number.parseInt(estimationRaw, 10);
  const hasValidEstimation = estimationParsed != null && ALLOWED_ESTIMATION_POINTS.has(estimationParsed);
  const estimationPayload = hasValidEstimation ? { estimationPoints: estimationParsed } : {};

  const sprintEl = document.getElementById("todoSprint");
  const sprintField = document.getElementById("todoSprintField");
  const showSprint = sprintEl && sprintField && sprintField.style.display !== "none";
  const sprintId = showSprint && sprintEl.value !== "" ? Number(sprintEl.value) : null;

  try {
    if (getEditingTodo()) {
      const todo = getEditingTodo();
      const assigneeEl = document.getElementById("todoAssignee");
      const assigneeUserId =
        assigneeEl && assigneeEl.value !== ""
          ? Number(assigneeEl.value)
          : assigneeEl
            ? null
            : undefined;
      const patchPayload = { title, body, tags, assigneeUserId, ...estimationPayload };
      if (showSprint) {
        patchPayload.sprintId = sprintId;
      }
      recordLocalMutation();
      await apiFetch(`/api/board/${getSlug()}/todos/${todo.localId}`, {
        method: "PATCH",
        body: JSON.stringify(patchPayload),
      });

      const currentColumnKey = (todo.columnKey || (todo.status || "").toLowerCase());
      if (columnKey !== currentColumnKey) {
        recordLocalMutation();
        await apiFetch(`/api/board/${getSlug()}/todos/${todo.localId}/move`, {
          method: "POST",
          body: JSON.stringify({ toColumnKey: columnKey, afterId: null, beforeId: null }),
        });
      }
      showToast("Todo updated");
    } else {
      const createPayload = { title, body, tags, columnKey, ...estimationPayload };
      if (showSprint) {
        createPayload.sprintId = sprintId;
      }
      const assigneeEl = document.getElementById("todoAssignee");
      if (assigneeEl) {
        createPayload.assigneeUserId =
          assigneeEl.value !== "" ? Number(assigneeEl.value) : null;
      }
      recordLocalMutation();
      await apiFetch(`/api/board/${getSlug()}/todos`, {
        method: "POST",
        body: JSON.stringify(createPayload),
      });
      showToast("Todo created");
    }

    todoDialog.close();
    // Invalidate tags cache so Settings modal shows newly created tags
    invalidateTagsCache();
    await loadBoardBySlug(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
  } catch (err) {
    showToast(err.message);
  }
});

router().catch((err) => showToast(err.message));

// Export render functions for views/index.js to re-export (breaking circular dependency)
// All render functions moved to modules/views/

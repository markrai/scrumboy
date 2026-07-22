/* global Sortable */

// Frontend working model:
// - Edit modules/**/*.ts. That is the only maintained source of truth for frontend logic.
// - Runtime boots from this app.js entry, which imports the committed dist/**/*.js bundle below.
// - dist/**/*.js is emitted runtime/build output, not the primary editing target.
// - source-side modules/**/*.js mirrors are unsupported and should not exist or be recreated.

import { app, toast, todoDialog, todoForm, todoDialogTitle, todoTitle, todoBody, todoTags, todoStatus, todoEstimationPoints, deleteTodoBtn, closeTodoBtn, settingsDialog, closeSettingsBtn } from './dist/dom/elements.js';
import { initTheme, handleThemeChange, getStoredTheme, THEME_SYSTEM, THEME_DARK, THEME_LIGHT } from './dist/theme.js';
import { escapeHTML, showToast, showConfirmDialog } from './dist/utils.js';
import { apiFetch } from './dist/api.js';
import { navigate, router } from './dist/router.js';
import { getRoute, getProjectId, getBoard, getAuthStatusAvailable, getMobileTab, getSlug, getTag, getSearch, getSprintIdFromUrl, getProjectView, getProjectsTab, getProjects, getSettingsProjectId, getEditingTodo, getAvailableTags, getAutocompleteSuggestion, getAvailableTagsMap, getTagColors, getUser, getSettingsActiveTab, getBackupImportBtn, getBackupData, getBackupPreview, getAuthStatusChecked } from './dist/state/selectors.js';
import { setProjectId, setBoard, setSlug, setTag, setMobileTab, setProjects, setProjectsTab, setProjectView, setEditingTodo, setAvailableTags, setAvailableTagsMap, setAutocompleteSuggestion, setTagColors, setSettingsProjectId, setSettingsActiveTab, setBackupImportBtn, setBackupData, setBackupPreview } from './dist/state/mutations.js';
import { openTodoDialog, renderTagsChips, setupTagAutocomplete, removeTag, renderTagAutocomplete, getTagsFromChips, resetAssigneeSelect, getTodoFormPermissions, requestTodoDialogClose } from './dist/dialogs/todo.js';
import { buildTodoCreatePayload, buildTodoPatchPayload } from './dist/dialogs/todo-submit.js';
import { renderSettingsModal, invalidateTagsCache, resumeAuthenticationMethodFlow } from './dist/dialogs/settings.js';
import { initDnD, columnsSpec, dragInProgress, dragJustEnded } from './dist/features/drag-drop.js';
import { setupContextMenuCloseHandler } from './dist/features/context-menu.js';
import { setupContextMenuButtonHandler } from './dist/features/context-menu-button.js';
import { loadBoardBySlug, onTodoDialogClosed } from './dist/views/index.js';
import { recordLocalMutation } from './dist/realtime/guard.js';
import { registerPwaGlobals } from './dist/pwaUpdate.js';
import { initKeybindings } from './dist/core/keybindings.js';
import { initModalOutsideClickClose } from './dist/core/modal-outside-click.js';
import { I18N_LOCALE_CHANGED, apiErrorMessage, hydrateI18n, initI18n, t } from './dist/i18n/index.js';
import { installI18nQa } from './dist/i18n/qa.js';

let tagInputHandlersSetup = false;

// PWA update: register service worker and "New version available" dialog (must run early)
registerPwaGlobals();

// Initialize theme on page load (wallpaper is applied after /api/auth/status in the router)
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

document.addEventListener(I18N_LOCALE_CHANGED, () => {
  hydrateI18n(document.body);
});

window.addEventListener("scrumboy:auth-method-return", async (event) => {
  const kind = typeof event.detail === "string" ? event.detail : "";
  setSettingsActiveTab("profile");
  await renderSettingsModal();
  settingsDialog.showModal();
  await resumeAuthenticationMethodFlow(kind);
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

closeTodoBtn.addEventListener("click", async () => {
  setAutocompleteSuggestion(null);
  renderTagAutocomplete();
  await requestTodoDialogClose({ reason: "button" });
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
  if (!await showConfirmDialog(
    t("todo.confirm.deleteMessage"),
    t("todo.confirm.deleteTitle"),
    t("todo.confirm.deleteAction"),
  )) return;
  try {
    recordLocalMutation();
    await apiFetch(`/api/board/${getSlug()}/todos/${todo.localId}`, { method: "DELETE" });
    setEditingTodo(null);
    onTodoDialogClosed();
    await requestTodoDialogClose({ force: true, reason: "delete" });
    await loadBoardBySlug(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
  } catch (err) {
    showToast(apiErrorMessage(err, { fallbackKey: "todo.deleteFailed" }));
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
      const patchPayload = buildTodoPatchPayload({
        title,
        body,
        tags,
        estimationRaw,
        assigneeEnabled: !!assigneeEl,
        assigneeUserId,
        sprintEnabled: !!showSprint,
        sprintId,
      });
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
      showToast(t("todo.updated"));
    } else {
      const assigneeEl = document.getElementById("todoAssignee");
      const assigneeUserId =
        assigneeEl && assigneeEl.value !== "" ? Number(assigneeEl.value) : null;
      const createPayload = buildTodoCreatePayload({
        title,
        body,
        tags,
        columnKey,
        estimationRaw,
        sprintEnabled: !!showSprint,
        sprintId,
        assigneeEnabled: !!assigneeEl,
        assigneeUserId,
      });
      recordLocalMutation();
      await apiFetch(`/api/board/${getSlug()}/todos`, {
        method: "POST",
        body: JSON.stringify(createPayload),
      });
      showToast(t("todo.created"));
    }

    await requestTodoDialogClose({ force: true, reason: "save" });
    // Invalidate tags cache so Settings modal shows newly created tags
    invalidateTagsCache();
    await loadBoardBySlug(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
  } catch (err) {
    showToast(apiErrorMessage(err, { fallbackKey: "todo.saveFailed" }));
  }
});

initI18n()
  .catch((err) => {
    console.warn("i18n initialization failed; continuing with English fallbacks.", err);
  })
  .then(() => {
    installI18nQa();
    hydrateI18n(document.body);
    return router().catch((err) => showToast(apiErrorMessage(err)));
  });

// Export render functions for views/index.js to re-export (breaking circular dependency)
// All render functions moved to modules/views/

import { settingsDialog, closeSettingsBtn } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { fetchProjectMembers } from '../members-cache.js';
import { escapeHTML, showToast, getAppVersion, showConfirmDialog, isAnonymousBoard, renderUserAvatar, processImageFile, renderAvatarContent, sanitizeHexColor } from '../utils.js';
import { getStoredTheme, handleThemeChange, THEME_SYSTEM, THEME_DARK, THEME_LIGHT } from '../theme.js';
import { getSlug, getTag, getSearch, getSprintIdFromUrl, getBoard, getProjectId, getProjects, getSettingsProjectId, getSettingsActiveTab, getTagColors, getUser, getAuthStatusAvailable, getBackupImportBtn, getBackupData, getBoardMembers } from '../state/selectors.js';
import { setSettingsProjectId, setSettingsActiveTab, setTagColors, setBoard, setBackupImportBtn, setBackupData, setBackupPreview, setUser, setBoardMembers, } from '../state/mutations.js';
import { renderRealBurndownChart, destroyBurndownChart, mountBurndownChart } from '../charts/burndown.js';
import { invalidateBoard, refreshSprintsAndChips } from '../orchestration/board-refresh.js';
import { emit } from '../events.js';
import { normalizeSprints } from '../sprints.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { KEY_ACTION_LIST, chordFromKeyboardEvent, formatChordForDisplay, getResolvedChordForAction, isTypingInTextField, reloadKeybindingsFromStorage, saveKeybindingOverride, setKeybindingsCaptureListening, } from '../core/keybindings.js';
import { requestDesktopNotificationPermission, getDesktopNotificationStatusDescription, } from '../core/assignmentNotify.js';
import { isPushSubscribed, subscribeToPush, unsubscribeFromPush } from '../core/push.js';
/** Active keybinding capture listener (settings customization); removed when starting a new capture or on abort. */
let keybindingCaptureKeydown = null;
function resetKeybindingCaptureUI() {
    if (keybindingCaptureKeydown) {
        window.removeEventListener("keydown", keybindingCaptureKeydown, true);
        keybindingCaptureKeydown = null;
    }
    setKeybindingsCaptureListening(false);
    document.querySelectorAll("[data-keybinding-capture]").forEach((b) => {
        const id = b.getAttribute("data-keybinding-action");
        if (id)
            b.textContent = formatChordForDisplay(getResolvedChordForAction(id));
        b.classList.remove("keybinding-capture--listening", "keybinding-capture--error");
    });
}
/** Avoid stacking `close` listeners if this module is re-evaluated (e.g. hot reload). */
let settingsKeybindingCloseListenerInstalled = false;
function installSettingsDialogCloseForKeybindingCaptureOnce() {
    if (settingsKeybindingCloseListenerInstalled)
        return;
    settingsKeybindingCloseListenerInstalled = true;
    settingsDialog.addEventListener("close", () => {
        resetKeybindingCaptureUI();
    });
}
installSettingsDialogCloseForKeybindingCaptureOnce();
// AbortController for per-render listener cleanup
let settingsAbortController = null;
let settingsProfileRefetchController = null;
let settingsProfileRefetchVersion = 0;
// Only one sprint row in edit mode at a time
let editingSprintId = null;
// Burndown chart sprint navigation: index into sorted sprints list
let burndownSprintIndex = 0;
// Cache for settings modal API calls
let cachedTags = null;
let cachedTagsHTML = null;
let cachedRealBurndownData = null;
let cachedTagsURL = null;
let cachedRealBurndownURL = null;
let cachedSprintsForCharts = null;
let workflowLaneCountsCache = null;
let workflowLaneCountsFetchGeneration = 0;
let workflowTabDraft = null;
let workflowTabDraftBaseline = null;
let workflowTabDraftSlug = null;
function cloneWorkflowLanesFromBoard() {
    const w = getBoard()?.columnOrder ?? [];
    return w.map((l) => ({
        key: l.key,
        name: l.name,
        color: normalizeWorkflowLaneColorForInput(l.color),
        isDone: !!l.isDone,
    }));
}
function ensureWorkflowDraftInitialized() {
    const slug = getSlug();
    if (!slug)
        return;
    if (workflowTabDraftSlug !== slug || workflowTabDraft === null || workflowTabDraftBaseline === null) {
        const lanes = cloneWorkflowLanesFromBoard();
        workflowTabDraft = lanes;
        workflowTabDraftBaseline = JSON.parse(JSON.stringify(lanes));
        workflowTabDraftSlug = slug;
    }
}
function syncWorkflowDraftFromBoardAfterMutation() {
    const lanes = cloneWorkflowLanesFromBoard();
    workflowTabDraft = lanes;
    workflowTabDraftBaseline = JSON.parse(JSON.stringify(lanes));
    workflowTabDraftSlug = getSlug() ?? null;
}
function resetWorkflowDraftToBaseline() {
    if (workflowTabDraftBaseline && workflowTabDraftSlug === getSlug()) {
        workflowTabDraft = JSON.parse(JSON.stringify(workflowTabDraftBaseline));
    }
    else {
        ensureWorkflowDraftInitialized();
    }
}
function clearWorkflowDraftState() {
    workflowTabDraft = null;
    workflowTabDraftBaseline = null;
    workflowTabDraftSlug = null;
}
function isWorkflowDraftDirty() {
    if (!workflowTabDraft || !workflowTabDraftBaseline)
        return false;
    if (workflowTabDraft.length !== workflowTabDraftBaseline.length)
        return true;
    for (let i = 0; i < workflowTabDraft.length; i++) {
        const a = workflowTabDraft[i];
        const b = workflowTabDraftBaseline[i];
        if (a.key !== b.key)
            return true;
        if (a.name.trim() !== b.name.trim())
            return true;
        if (a.color.trim().toLowerCase() !== b.color.trim().toLowerCase())
            return true;
    }
    return false;
}
function updateWorkflowSaveFooter() {
    const btn = document.querySelector("[data-workflow-save-changes]");
    if (btn)
        btn.disabled = !isWorkflowDraftDirty();
}
function invalidateWorkflowLaneCountsCache() {
    workflowLaneCountsCache = null;
    workflowLaneCountsFetchGeneration++;
}
// Helper function to invalidate tags cache (call when tags are modified)
export function invalidateTagsCache() {
    cachedTags = null;
    cachedTagsHTML = null;
    cachedTagsURL = null;
}
/** Update all user-avatar elements outside the settings dialog (e.g. topbar) after avatar change. */
function refreshAvatarsOutsideSettings() {
    const user = getUser();
    const content = renderAvatarContent(user);
    document.querySelectorAll('.user-avatar').forEach((el) => {
        if (el.closest('#settingsDialog'))
            return;
        el.innerHTML = content;
    });
}
function invalidateSettingsProfileRefetch() {
    settingsProfileRefetchVersion++;
    settingsProfileRefetchController?.abort();
    settingsProfileRefetchController = null;
}
// Helper function to invalidate chart cache (call when todos are modified)
function invalidateChartCache() {
    cachedRealBurndownData = null;
    cachedRealBurndownURL = null;
}
/**
 * Single source of truth for all settings tab switches (click + keyboard).
 * Handles workflow dirty checks, cache invalidation, re-render, and dialog height fix.
 */
async function switchSettingsTab(tabName) {
    if (tabName === getSettingsActiveTab())
        return;
    if (getSettingsActiveTab() === "workflow" && isWorkflowDraftDirty()) {
        const discard = await showConfirmDialog("You have unsaved changes. Discard them?", "Unsaved changes", "Discard");
        if (!discard)
            return;
        resetWorkflowDraftToBaseline();
    }
    if (tabName === "workflow") {
        invalidateWorkflowLaneCountsCache();
        clearWorkflowDraftState();
    }
    setSettingsActiveTab(tabName);
    await renderSettingsModal();
    const dialog = document.getElementById("settingsDialog");
    if (dialog && dialog.open) {
        const currentHeight = dialog.style.height;
        dialog.style.height = "auto";
        void dialog.offsetHeight;
        dialog.style.height = currentHeight || "";
    }
}
// Invalidate sprints cache when sprints are created/activated/closed (so Charts tab shows fresh list)
/** Auto-select sprint for Charts: active > last closed > first planned. */
function computeDefaultBurndownSprintIndex(sprints) {
    if (sprints.length === 0)
        return 0;
    const activeIdx = sprints.findIndex((s) => s.state === 'ACTIVE');
    if (activeIdx >= 0)
        return activeIdx;
    const closed = sprints
        .map((s, i) => ({ s, i }))
        .filter((x) => x.s.state === 'CLOSED');
    if (closed.length > 0) {
        const lastClosed = closed.reduce((a, b) => a.s.plannedEndAt >= b.s.plannedEndAt ? a : b);
        return lastClosed.i;
    }
    const plannedIdx = sprints.findIndex((s) => s.state === 'PLANNED');
    if (plannedIdx >= 0)
        return plannedIdx;
    return 0;
}
function invalidateSprintsForChartsCache() {
    cachedSprintsForCharts = null;
    cachedRealBurndownData = null;
    cachedRealBurndownURL = null;
}
// Helper function for tag color
function getTagColor(tagName) {
    return getTagColors()[tagName] || null;
}
// Render backup tab HTML
function renderBackupTabHTML() {
    const isAnonymousMode = !getAuthStatusAvailable();
    const replaceDisabled = isAnonymousMode ? 'disabled' : '';
    const replaceHidden = isAnonymousMode ? 'style="display: none;"' : '';
    return `
    <div class="settings-backup-section">
      <div class="settings-backup-export">
        <div class="settings-section__title">Export Data</div>
        <div class="settings-section__description muted">Download all your projects, todos, and tags as a JSON file.</div>
        <button class="btn" type="button" id="backupExportBtn">Export Backup</button>
      </div>
      <div class="settings-backup-import">
        <div class="settings-section__title">Import Data</div>
        <div class="settings-section__description muted">Restore from a backup file or merge data from another instance.</div>
        <input type="file" accept=".json" id="backupFileInput" style="margin-bottom: 16px;">
        <div style="margin-bottom: 16px;">
          <label style="display: block; margin-bottom: 8px;">
            <input type="radio" name="importMode" value="merge" checked>
            <span>Merge & update (recommended)</span>
          </label>
          <label style="display: block; margin-bottom: 8px;" ${replaceHidden}>
            <input type="radio" name="importMode" value="replace" ${replaceDisabled}>
            <span>Replace all data</span>
          </label>
          <label style="display: block; margin-bottom: 8px;">
            <input type="radio" name="importMode" value="copy">
            <span>Create a copy</span>
          </label>
        </div>
        <div id="backupPreview" class="settings-backup-preview" style="display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px;"></div>
        <div id="backupConfirmation" style="display: none; margin-bottom: 16px;">
          <input type="text" id="backupConfirmationInput" placeholder="Type REPLACE to confirm" class="settings-backup-confirmation" style="width: 100%; padding: 8px;">
        </div>
        <div id="backupWarnings" class="settings-backup-warnings" style="display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px; color: var(--muted);"></div>
        <button class="btn" type="button" id="backupImportBtn" disabled>Import</button>
      </div>
    </div>
  `;
}
// Backup handlers
async function handleBackupExport() {
    try {
        const response = await fetch("/api/backup/export", {
            headers: {
                "X-Scrumboy": "1"
            }
        });
        if (!response.ok) {
            const err = await response.json();
            showToast(err.error?.message || "Export failed");
            return;
        }
        const blob = await response.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        // Format: scrumboy-backup-2026-01-24-03-45-PM.json
        const now = new Date();
        const dateStr = now.toISOString().split('T')[0];
        const hours = now.getHours();
        const minutes = now.getMinutes().toString().padStart(2, '0');
        const ampm = hours >= 12 ? 'PM' : 'AM';
        const hours12 = (hours % 12 || 12).toString().padStart(2, '0');
        a.download = `scrumboy-backup-${dateStr}-${hours12}-${minutes}-${ampm}.json`;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        window.URL.revokeObjectURL(url);
        showToast("Backup exported successfully");
    }
    catch (err) {
        showToast(err.message || "Export failed");
    }
}
async function handleBackupFileSelect(e) {
    const target = e.target;
    const file = target.files?.[0];
    if (!file) {
        return;
    }
    try {
        const text = await file.text();
        const data = JSON.parse(text);
        // Validate structure
        if (!data.version || !data.projects || !Array.isArray(data.projects)) {
            showToast("Invalid backup file format");
            return;
        }
        // Store the data for import
        setBackupData(data);
        // Get selected import mode
        const importMode = document.querySelector('input[name="importMode"]:checked')?.value || "merge";
        // Call preview endpoint
        const preview = await apiFetch("/api/backup/preview", {
            method: "POST",
            body: JSON.stringify({
                data: data,
                importMode: importMode
            })
        });
        // Display preview
        const previewEl = document.getElementById("backupPreview");
        if (previewEl) {
            let previewHTML = `<strong>Preview:</strong><br>`;
            previewHTML += `Projects: ${preview.projects}<br>`;
            previewHTML += `Todos: ${preview.todos}<br>`;
            previewHTML += `Tags: ${preview.tags}<br>`;
            if (preview.links !== undefined && preview.links > 0) {
                previewHTML += `Links: ${preview.links}<br>`;
            }
            if (preview.willDelete !== undefined) {
                previewHTML += `Will delete: ${preview.willDelete} projects<br>`;
            }
            if (preview.willUpdate !== undefined) {
                previewHTML += `Will update: ${preview.willUpdate} items<br>`;
            }
            if (preview.willCreate !== undefined) {
                previewHTML += `Will create: ${preview.willCreate} items<br>`;
            }
            previewEl.innerHTML = previewHTML;
            previewEl.style.display = "block";
        }
        // Display warnings if any
        if (preview.warnings && preview.warnings.length > 0) {
            const warningsEl = document.getElementById("backupWarnings");
            if (warningsEl) {
                warningsEl.innerHTML = `<strong>Warnings:</strong><br>${preview.warnings.map((w) => escapeHTML(w)).join("<br>")}`;
                warningsEl.style.display = "block";
            }
        }
        setBackupPreview(preview);
        updateBackupUI();
    }
    catch (err) {
        showToast(err.message || "Failed to read backup file");
        setBackupData(null);
        setBackupPreview(null);
        updateBackupUI();
    }
}
function updateBackupUI() {
    // Use stored reference if available, otherwise find by ID
    const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn"));
    const confirmationDiv = document.getElementById("backupConfirmation");
    const confirmationInput = document.getElementById("backupConfirmationInput");
    const importMode = document.querySelector('input[name="importMode"]:checked')?.value || "merge";
    if (!getBackupData()) {
        if (importBtn) {
            importBtn.disabled = true;
        }
        if (confirmationDiv) {
            confirmationDiv.style.display = "none";
        }
        return;
    }
    // Show confirmation input for replace mode
    if (importMode === "replace") {
        if (confirmationDiv) {
            confirmationDiv.style.display = "block";
        }
        const isValid = confirmationInput && confirmationInput.value.trim() === "REPLACE";
        if (importBtn) {
            importBtn.disabled = !isValid;
            // Force update the disabled state
            if (isValid) {
                importBtn.removeAttribute("disabled");
            }
            else {
                importBtn.setAttribute("disabled", "disabled");
            }
        }
    }
    else {
        if (confirmationDiv) {
            confirmationDiv.style.display = "none";
        }
        if (importBtn) {
            importBtn.disabled = false;
            importBtn.removeAttribute("disabled");
        }
    }
}
async function handleBackupImport() {
    console.log("handleBackupImport: Function called");
    if (!getBackupData()) {
        console.log("handleBackupImport: No backup data");
        showToast("No backup file selected");
        return;
    }
    // Use stored reference if available, otherwise find by ID
    const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn"));
    console.log("handleBackupImport: Button found", {
        hasButton: !!importBtn,
        isDisabled: importBtn?.disabled,
        buttonText: importBtn?.textContent
    });
    if (importBtn && importBtn.disabled) {
        console.log("handleBackupImport: Button is disabled, returning");
        showToast("Please complete the confirmation to enable import");
        return;
    }
    const importMode = document.querySelector('input[name="importMode"]:checked')?.value || "merge";
    const confirmationInput = document.getElementById("backupConfirmationInput");
    console.log("handleBackupImport: Mode and confirmation", {
        importMode,
        confirmationValue: confirmationInput?.value,
        confirmationTrimmed: confirmationInput?.value?.trim()
    });
    // Validate confirmation for replace mode
    if (importMode === "replace") {
        if (!confirmationInput || confirmationInput.value.trim() !== "REPLACE") {
            console.log("handleBackupImport: Invalid confirmation");
            showToast("Please type REPLACE in the confirmation field");
            return;
        }
    }
    try {
        console.log("handleBackupImport: Starting", { importMode, hasData: !!getBackupData() });
        const body = {
            data: getBackupData(),
            importMode: importMode
        };
        if (importMode === "replace") {
            body.confirmation = confirmationInput.value.trim();
        }
        // In anonymous mode, import into current board (if viewing one)
        const currentSlug = getSlug();
        if (currentSlug) {
            body.targetSlug = currentSlug;
        }
        console.log("handleBackupImport: Request body prepared", {
            importMode: body.importMode,
            targetSlug: body.targetSlug,
            hasData: !!body.data,
            hasConfirmation: !!body.confirmation,
            projectsCount: body.data?.projects?.length
        });
        // Show loading state
        if (importBtn) {
            importBtn.disabled = true;
            importBtn.setAttribute("disabled", "disabled");
            const originalText = importBtn.textContent;
            importBtn.textContent = "Importing...";
        }
        console.log("handleBackupImport: Calling API...");
        const result = await apiFetch("/api/backup/import", {
            method: "POST",
            body: JSON.stringify(body)
        });
        console.log("handleBackupImport: API call completed", result);
        // Show results
        let message = `Import completed: `;
        if (result.imported !== undefined)
            message += `${result.imported} projects imported, `;
        if (result.updated !== undefined)
            message += `${result.updated} updated, `;
        if (result.created !== undefined)
            message += `${result.created} created`;
        showToast(message);
        // Show warnings if any
        if (result.warnings && result.warnings.length > 0) {
            const warningsEl = document.getElementById("backupWarnings");
            if (warningsEl) {
                warningsEl.innerHTML = `<strong>Warnings:</strong><br>${result.warnings.map((w) => escapeHTML(w)).join("<br>")}`;
                warningsEl.style.display = "block";
            }
        }
        // Reload the page to show updated data
        setTimeout(() => {
            window.location.reload();
        }, 1500);
    }
    catch (err) {
        console.error("handleBackupImport: ERROR CAUGHT", err);
        console.error("handleBackupImport: Error details", {
            message: err.message,
            status: err.status,
            data: err.data,
            stack: err.stack
        });
        const errorMsg = err.message || err.data?.error?.message || "Import failed";
        console.error("handleBackupImport: Showing toast with message:", errorMsg);
        showToast(errorMsg);
        // Re-enable button on error - use stored reference if available
        const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn"));
        if (importBtn) {
            importBtn.disabled = false;
            importBtn.removeAttribute("disabled");
            importBtn.textContent = "Import";
            console.log("handleBackupImport: Button restored");
        }
        else {
            console.error("handleBackupImport: Could not find button to restore");
        }
    }
}
async function setupBackupTab(signal) {
    // Export button
    const exportBtn = document.getElementById("backupExportBtn");
    if (exportBtn) {
        exportBtn.addEventListener("click", handleBackupExport, signal ? { signal } : undefined);
    }
    // File input
    const fileInput = document.getElementById("backupFileInput");
    if (fileInput) {
        fileInput.addEventListener("change", handleBackupFileSelect, signal ? { signal } : undefined);
    }
    // Import mode radio buttons
    document.querySelectorAll('input[name="importMode"]').forEach(radio => {
        radio.addEventListener("change", () => {
            // Clear confirmation input when switching modes
            const confirmationInput = document.getElementById("backupConfirmationInput");
            if (confirmationInput) {
                confirmationInput.value = "";
            }
            // Update UI when mode changes
            setTimeout(() => updateBackupUI(), 0);
        }, signal ? { signal } : undefined);
    });
    // Confirmation input
    const confirmationInput = document.getElementById("backupConfirmationInput");
    if (confirmationInput) {
        confirmationInput.addEventListener("input", () => {
            // Update UI immediately when typing
            updateBackupUI();
        }, signal ? { signal } : undefined);
        // Also trigger on paste
        confirmationInput.addEventListener("paste", () => {
            setTimeout(() => updateBackupUI(), 0);
        }, signal ? { signal } : undefined);
        // Trigger on keyup as well to catch all changes
        confirmationInput.addEventListener("keyup", () => {
            updateBackupUI();
        }, signal ? { signal } : undefined);
    }
    // Import button
    const importBtn = document.getElementById("backupImportBtn");
    if (importBtn) {
        importBtn.addEventListener("click", handleBackupImport, signal ? { signal } : undefined);
        setBackupImportBtn(importBtn);
    }
    // Call updateBackupUI to set initial state after a brief delay to ensure DOM is ready
    setTimeout(() => {
        updateBackupUI();
    }, 0);
}
// updateTagColor and deleteTag call view functions, so they need to import from app.js
// For durable projects: tag_id required (same authority rule as delete). For anonymous boards only: name-based allowed.
async function updateTagColor(tagName, tagId, color) {
    const projectId = getSettingsProjectId();
    const slug = getSlug();
    const isDurable = !!projectId;
    // Durable project: require tagId; never use name-based mutation.
    if (isDurable) {
        if (tagId == null || tagId <= 0) {
            showToast("Cannot update color: tag ID missing");
            return;
        }
        const url = `/api/projects/${projectId}/tags/id/${tagId}/color`;
        try {
            recordLocalMutation();
            await apiFetch(url, {
                method: "PATCH",
                body: JSON.stringify({ color }),
            });
            await applyTagColorSuccess(tagName, color, url);
        }
        catch (err) {
            showToast(err.message);
        }
        return;
    }
    // Board (slug): prefer tag_id; fall back to name only for anonymous boards.
    if (slug) {
        let url;
        if (tagId != null && tagId > 0) {
            url = `/api/board/${slug}/tags/id/${tagId}/color`;
        }
        else {
            url = `/api/board/${slug}/tags/${encodeURIComponent(tagName)}/color`;
        }
        try {
            recordLocalMutation();
            await apiFetch(url, {
                method: "PATCH",
                body: JSON.stringify({ color }),
            });
            await applyTagColorSuccess(tagName, color, url);
        }
        catch (err) {
            showToast(err.message);
        }
        return;
    }
    showToast("No project available");
}
async function applyTagColorSuccess(tagName, color, _url) {
    try {
        // Update local state
        const tagColors = { ...getTagColors() };
        if (color) {
            tagColors[tagName] = color;
        }
        else {
            delete tagColors[tagName];
        }
        setTagColors(tagColors);
        // Save tag colors to backend (user preference)
        if (getUser()) {
            try {
                await apiFetch("/api/user/preferences", {
                    method: "PUT",
                    body: JSON.stringify({ key: "tagColors", value: JSON.stringify(tagColors) }),
                });
            }
            catch (err) {
                // Ignore errors saving preferences
            }
        }
        // Update clear button visibility
        const clearBtn = document.querySelector(`.settings-color-clear[data-tag="${escapeHTML(tagName)}"]`);
        if (clearBtn) {
            clearBtn.style.display = color ? "" : "none";
        }
        invalidateTagsCache();
        // Refresh board to apply colors
        if (getSlug()) {
            await invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
        }
        showToast("Tag color updated");
    }
    catch (err) {
        showToast(err.message);
    }
}
async function deleteTag(tagName, tagId) {
    // Authority by tag_id only. For durable projects, tagId is required; no fallback to name.
    let url = null;
    const isDurableMode = !!getSettingsProjectId(); // Projects handler is durable-only
    if (getSlug()) {
        // Board view: prefer tag_id; fall back to name only for anonymous boards
        url = tagId != null ? `/api/board/${getSlug()}/tags/id/${tagId}` : `/api/board/${getSlug()}/tags/${encodeURIComponent(tagName)}`;
    }
    else if (isDurableMode) {
        // Durable projects: tagId required; no fallback to name
        if (tagId == null) {
            showToast("Cannot delete: tag ID missing");
            return;
        }
        url = `/api/projects/${getSettingsProjectId()}/tags/id/${tagId}`;
    }
    else {
        showToast("No project available");
        return;
    }
    try {
        recordLocalMutation();
        await apiFetch(url, {
            method: "DELETE",
        });
        const tagColors = { ...getTagColors() };
        delete tagColors[tagName];
        setTagColors(tagColors);
        if (getUser()) {
            try {
                await apiFetch("/api/user/preferences", {
                    method: "PUT",
                    body: JSON.stringify({ key: "tagColors", value: JSON.stringify(tagColors) }),
                });
            }
            catch (err) {
                // Ignore errors saving preferences
            }
        }
        invalidateTagsCache();
        await renderSettingsModal();
        if (getSlug()) {
            await invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
        }
        showToast(`Tag "${tagName}" deleted`);
    }
    catch (err) {
        showToast(err.message);
    }
}
function msToDateTimeLocalStr(ms) {
    const d = new Date(ms);
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    const hh = String(d.getHours()).padStart(2, "0");
    const mm = String(d.getMinutes()).padStart(2, "0");
    return `${y}-${m}-${day}T${hh}:${mm}`;
}
const DEFAULT_WORKFLOW_LANE_COLOR = "#64748b";
function normalizeWorkflowLaneColorForInput(color) {
    const s = color?.trim();
    return s && /^#[0-9a-fA-F]{6}$/.test(s) ? s : DEFAULT_WORKFLOW_LANE_COLOR;
}
async function fetchWorkflowLaneCountsState(slug) {
    try {
        const res = await apiFetch(`/api/board/${encodeURIComponent(slug)}/workflow/counts`);
        if (!res || typeof res.countsByColumnKey !== "object" || res.countsByColumnKey === null) {
            return { status: "error" };
        }
        return { status: "ok", counts: res.countsByColumnKey };
    }
    catch {
        return { status: "error" };
    }
}
function renderWorkflowTabContent(countsState) {
    const board = getBoard();
    const col = board?.columnOrder ?? [];
    if (!getSlug()) {
        return `<div class="settings-section"><div class="muted">No project in context.</div></div>`;
    }
    if (col.length === 0) {
        return `<div class="settings-section"><div class="muted">Workflow lanes are unavailable.</div></div>`;
    }
    ensureWorkflowDraftInitialized();
    const workflow = workflowTabDraft ?? [];
    const canDeleteAnyLane = workflow.length > 2;
    const loadingBanner = countsState.status === "loading"
        ? `<div class="muted settings-workflow-counts-banner" style="margin-bottom:10px;">Checking lane occupancy…</div>`
        : "";
    const errorBanner = countsState.status === "error"
        ? `<div class="settings-workflow-counts-banner settings-workflow-counts-banner--error muted" style="margin-bottom:10px;display:flex;flex-wrap:wrap;align-items:center;gap:8px;">
          Could not load lane occupancy. Delete stays disabled until this succeeds.
          <button type="button" class="btn btn--ghost btn--small" data-workflow-counts-retry>Retry</button>
        </div>`
        : "";
    const deleteCell = (lane) => {
        if (lane.isDone) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Done lane cannot be deleted">Delete</button>`;
        }
        if (!canDeleteAnyLane) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Workflow must keep at least 2 lanes">Delete</button>`;
        }
        if (countsState.status === "loading") {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Checking lane occupancy…">Delete</button>`;
        }
        if (countsState.status === "error") {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Could not verify lane is empty">Delete</button>`;
        }
        const n = countsState.counts[lane.key] ?? 0;
        if (n > 0) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Lane must be empty to delete" aria-label="Lane must be empty to delete">Delete</button>`;
        }
        return `<button class="btn btn--danger btn--small" type="button" data-workflow-delete="${escapeHTML(lane.key)}">Delete</button>`;
    };
    const saveDisabled = !isWorkflowDraftDirty();
    return `
    <div class="settings-section">
      <div class="settings-section__title">Workflow</div>
      <div class="settings-section__description muted">Edit lane labels and colors, then save. New lanes are inserted before the done lane. Keys stay immutable.</div>
      ${loadingBanner}
      ${errorBanner}
      <div class="settings-workflow-list">
        ${workflow
        .map((lane) => {
        const ic = normalizeWorkflowLaneColorForInput(lane.color);
        return `
          <div class="settings-workflow-row" data-workflow-key="${escapeHTML(lane.key)}" style="display:flex; gap:8px; align-items:center; margin-bottom:8px; flex-wrap:wrap; padding-left:4px;">
            <input
              class="input"
              data-workflow-name="${escapeHTML(lane.key)}"
              value="${escapeHTML(lane.name)}"
              maxlength="200"
              aria-label="Lane label for ${escapeHTML(lane.key)}"
              style="flex:1; min-width:120px;"
            />
            <input
              type="color"
              class="settings-color-picker"
              data-workflow-color="${escapeHTML(lane.key)}"
              value="${escapeHTML(ic)}"
              aria-label="Lane color for ${escapeHTML(lane.key)}"
              title="Lane color"
            />
            ${deleteCell(lane)}
          </div>
        `;
    })
        .join("")}
      </div>
      <div class="settings-workflow-add-row" style="display:flex; gap:8px; align-items:center; margin-top:12px;">
        <input
          class="input"
          type="text"
          data-workflow-ghost-input
          maxlength="200"
          placeholder="Add lane..."
          aria-label="Add lane"
          style="flex:1; min-width:0;"
        />
        <button type="button" class="btn btn--small" data-workflow-add>Add</button>
      </div>
      <div class="settings-workflow-footer">
        <button type="button" class="btn btn--ghost" data-workflow-draft-cancel>Cancel</button>
        <button type="button" class="btn" data-workflow-save-changes ${saveDisabled ? "disabled" : ""}>Save Changes</button>
      </div>
    </div>
  `;
}
async function addWorkflowLane(name) {
    const slug = getSlug();
    if (!slug) {
        showToast("No project available");
        return;
    }
    const trimmed = name.trim();
    if (!trimmed) {
        showToast("Lane name is required");
        return;
    }
    try {
        recordLocalMutation();
        await apiFetch(`/api/board/${slug}/workflow`, {
            method: "POST",
            body: JSON.stringify({ name: trimmed }),
        });
        invalidateWorkflowLaneCountsCache();
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
        syncWorkflowDraftFromBoardAfterMutation();
        await renderSettingsModal();
        showToast("Lane added");
    }
    catch (err) {
        showToast(err.message || "Failed to add lane");
    }
}
async function saveWorkflowDraftChanges() {
    const slug = getSlug();
    if (!slug || !workflowTabDraft || !workflowTabDraftBaseline)
        return;
    for (const lane of workflowTabDraft) {
        if (!lane.name.trim()) {
            showToast("Lane name is required");
            return;
        }
    }
    const baselineByKey = new Map(workflowTabDraftBaseline.map((l) => [l.key, l]));
    try {
        for (const lane of workflowTabDraft) {
            const base = baselineByKey.get(lane.key);
            if (!base)
                continue;
            const name = lane.name.trim();
            const color = lane.color.trim();
            if (name === base.name.trim() &&
                color.toLowerCase() === base.color.trim().toLowerCase()) {
                continue;
            }
            recordLocalMutation();
            await apiFetch(`/api/board/${slug}/workflow/${encodeURIComponent(lane.key)}`, {
                method: "PATCH",
                body: JSON.stringify({ name, color }),
            });
        }
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
        syncWorkflowDraftFromBoardAfterMutation();
        await renderSettingsModal();
        showToast("Workflow updated");
    }
    catch (err) {
        showToast(err.message || "Failed to update workflow");
    }
}
async function deleteWorkflowLane(key) {
    const slug = getSlug();
    if (!slug) {
        showToast("No project available");
        return;
    }
    const lane = getBoard()?.columnOrder?.find((item) => item.key === key);
    if (!lane) {
        showToast("Lane not found");
        return;
    }
    if (lane.isDone) {
        showToast("Done lane cannot be deleted");
        return;
    }
    const confirmed = await showConfirmDialog(`Delete lane "${lane.name}"? Only empty non-done lanes can be deleted.`, "Delete lane?", "Delete");
    if (!confirmed)
        return;
    try {
        recordLocalMutation();
        await apiFetch(`/api/board/${slug}/workflow/${encodeURIComponent(key)}`, {
            method: "DELETE",
        });
        invalidateWorkflowLaneCountsCache();
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
        syncWorkflowDraftFromBoardAfterMutation();
        await renderSettingsModal();
        showToast("Lane deleted");
    }
    catch (err) {
        showToast(err.message || "Failed to delete lane");
    }
}
function computeDefaultSprintStart(now) {
    const daysToMonday = (now.getDay() + 6) % 7; // 0=Sun, 1=Mon, ..., 6=Sat
    const monday = new Date(now.getTime());
    monday.setDate(monday.getDate() - daysToMonday);
    monday.setHours(9, 0, 0, 0);
    return monday;
}
function computeDefaultSprintEnd(start, weeks) {
    const normalizedWeeks = weeks === 1 || weeks === 2 ? weeks : 2;
    const end = new Date(start.getTime());
    end.setDate(end.getDate() + (normalizedWeeks * 7 - 1));
    end.setHours(23, 59, 0, 0);
    return end;
}
// Main render function
async function renderSprintsTabContent() {
    const slug = getSlug();
    if (!slug)
        return "<div class='muted'>No project in context.</div>";
    try {
        const res = await apiFetch(`/api/board/${slug}/sprints`);
        const sprints = normalizeSprints(res);
        const formatDate = (ms) => new Date(ms).toLocaleString(undefined, {
            month: "short",
            day: "numeric",
            year: "numeric",
            hour: "numeric",
            minute: "2-digit",
        });
        const listHTML = sprints.length === 0
            ? "<div class='muted'>No sprints yet. Create one above.</div>"
            : sprints.map((sp) => {
                const isEditing = editingSprintId === sp.id;
                const dateRange = `${formatDate(sp.plannedStartAt)} – ${formatDate(sp.plannedEndAt)}`;
                const stateBadge = `<span class="status-pill status-pill--${sp.state.toLowerCase()}">${sp.state}</span>`;
                const activateBtn = sp.state === "PLANNED" ? `<button class="btn btn--ghost btn--sm" data-sprint-activate="${sp.id}">Activate</button>` : "";
                const closeBtn = sp.state === "ACTIVE" ? `<button class="btn btn--ghost btn--sm" data-sprint-close="${sp.id}">Close</button>` : (sp.state === "CLOSED" ? `<button type="button" class="btn btn--ghost btn--sm settings-sprint-row__action-placeholder" aria-hidden="true" tabindex="-1">Close</button>` : "");
                const editBtn = `<button class="btn btn--ghost btn--sm" data-sprint-edit="${sp.id}">Edit</button>`;
                const deleteBtn = `<button class="btn btn--danger btn--sm" data-sprint-delete="${sp.id}">Delete</button>`;
                if (isEditing) {
                    const editingClass = " settings-sprint-row--editing";
                    const todoCount = sp.todoCount ?? 0;
                    const nameInput = (sp.state === "PLANNED" || sp.state === "CLOSED") ? `<input class="input" data-sprint-edit-name value="${escapeHTML(sp.name)}" style="min-width: 120px;" />` : `<strong>${escapeHTML(sp.name)}</strong>`;
                    const startDisplay = `<span class="muted">${formatDate(sp.plannedStartAt)}</span>`;
                    const startInput = sp.state === "PLANNED" ? `<input class="input" type="datetime-local" data-sprint-edit-start value="${msToDateTimeLocalStr(sp.plannedStartAt)}" style="min-width: 180px;" />` : startDisplay;
                    const endDisplay = `<span class="muted">${formatDate(sp.plannedEndAt)}</span>`;
                    const endInput = (sp.state === "PLANNED" || sp.state === "ACTIVE") ? `<input class="input" type="datetime-local" data-sprint-edit-end value="${msToDateTimeLocalStr(sp.plannedEndAt)}" style="min-width: 180px;" />` : endDisplay;
                    const endBlock = sp.state === "ACTIVE"
                        ? `<div class="settings-sprint-edit-end-block" style="display: inline-flex; align-items: center; gap: 6px;"><div class="field__label" style="margin-bottom: 0;">End</div>${endInput}</div>`
                        : endInput;
                    const saveCancelBlock = `<div class="settings-sprint-edit-save-cancel" style="display: inline-flex; align-items: center; gap: 8px;"><button class="btn btn--sm" data-sprint-save="${sp.id}">Save</button><button class="btn btn--ghost btn--sm" data-sprint-cancel="${sp.id}">Cancel</button></div>`;
                    return `
            <div class="settings-sprint-row${editingClass}" data-sprint-id="${sp.id}" data-sprint-state="${sp.state}" data-sprint-todo-count="${todoCount}" data-sprint-planned-start-at="${sp.plannedStartAt}" data-sprint-name="${escapeHTML(sp.name)}">
              <div class="settings-sprint-row__info" style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap; flex: 1;">
                ${nameInput}
                ${startInput}
                ${endBlock}
                ${saveCancelBlock}
              </div>
              <div class="settings-sprint-row__actions" style="display: flex; align-items: center; gap: 8px;">
                ${stateBadge}
              </div>
            </div>`;
                }
                const todoCount = sp.todoCount ?? 0;
                return `
            <div class="settings-sprint-row" data-sprint-id="${sp.id}" data-sprint-state="${sp.state}" data-sprint-todo-count="${todoCount}" data-sprint-planned-start-at="${sp.plannedStartAt}" data-sprint-name="${escapeHTML(sp.name)}">
              <div class="settings-sprint-row__info">
                <strong>${escapeHTML(sp.name)}</strong>
                <span class="muted" style="margin-left: 8px;">${escapeHTML(dateRange)}</span>
              </div>
              <div class="settings-sprint-row__actions" style="display: flex; align-items: center; gap: 8px;">
                ${stateBadge}
                ${activateBtn}
                ${closeBtn}
                ${editBtn}
                ${deleteBtn}
              </div>
            </div>`;
            }).join("");
        const defaultWeeks = getBoard()?.project?.defaultSprintWeeks === 1 ? 1 : 2;
        const now = new Date();
        const defaultStart = computeDefaultSprintStart(now);
        const defaultEnd = computeDefaultSprintEnd(defaultStart, defaultWeeks);
        const defaultStartStr = msToDateTimeLocalStr(defaultStart.getTime());
        const defaultEndStr = msToDateTimeLocalStr(defaultEnd.getTime());
        return `
      <div class="settings-section">
        <div class="settings-section__title">Create Sprint</div>
        <div class="settings-section__description muted">
          Default duration is
          <select id="sprintDefaultWeeksSelect" class="input" style="display: inline-block; width: auto; min-width: 64px; margin: 0 4px;">
            <option value="1" ${defaultWeeks === 1 ? "selected" : ""}>1</option>
            <option value="2" ${defaultWeeks === 2 ? "selected" : ""}>2</option>
          </select>
          weeks. You can customize start and end dates.
        </div>
        <div class="settings-create-sprint-form" style="display: flex; flex-wrap: wrap; gap: 12px; align-items: flex-end;">
          <label class="field settings-create-sprint-form__name" style="flex: 1; min-width: 120px;">
            <div class="field__label">Name</div>
            <input class="input" id="sprintNameInput" placeholder="e.g. Sprint 1 or 2026 Q1 Sprint 1" />
          </label>
          <div class="settings-create-sprint-form__dates" style="display: flex; gap: 12px; align-items: flex-end;">
            <label class="field" style="min-width: 140px;">
              <div class="field__label">Start</div>
              <input class="input" type="datetime-local" id="sprintStartInput" value="${defaultStartStr}" />
            </label>
            <label class="field" style="min-width: 140px;">
              <div class="field__label">End</div>
              <input class="input" type="datetime-local" id="sprintEndInput" value="${defaultEndStr}" />
            </label>
          </div>
          <div class="settings-create-sprint-form__submit">
            <button class="btn" id="createSprintBtn">Create Sprint</button>
          </div>
        </div>
        <div class="settings-section__title" style="margin-top: 24px;">Sprints</div>
        <div class="settings-section__description muted">Create and manage sprints for this project. Only one sprint can be active at a time.</div>
        <div class="settings-sprints-list" style="margin-bottom: 24px;">
          ${listHTML}
        </div>
      </div>`;
    }
    catch (err) {
        return `<div class='muted'>Error loading sprints: ${escapeHTML(err.message)}</div>`;
    }
}
export async function renderSettingsModal(options) {
    const contentEl = document.querySelector("#settingsDialog .dialog__content");
    if (!contentEl) {
        console.error("Settings dialog content element not found");
        return;
    }
    // Full mode only: show Profile tab (auth status endpoint exists only in full mode).
    const showProfileTab = !!getAuthStatusAvailable();
    // Show Users tab only if user has admin or owner role
    const currentUser = getUser();
    const showUsersTab = showProfileTab && (currentUser?.systemRole === "owner" || currentUser?.systemRole === "admin");
    // In board view we have a slug and can use capability routes.
    // In projects listing view (full mode), show all tags from all projects the user has access to.
    let tagsURL = null;
    let realBurndownURL = null;
    let hasProjectAccess = false;
    if (getSlug()) {
        // Board view: show tags from this specific board
        tagsURL = `/api/board/${getSlug()}/tags`;
        realBurndownURL = `/api/board/${getSlug()}/burndown`;
        setSettingsProjectId(null);
        hasProjectAccess = true;
    }
    else {
        // Projects listing view: show all tags from all projects the user has access to
        if (getUser()) {
            tagsURL = `/api/tags/mine`;
            hasProjectAccess = true;
        }
        // For charts, still need a project ID (use first available project)
        let projectId = getProjectId() || getSettingsProjectId();
        if (!projectId && Array.isArray(getProjects()) && getProjects().length > 0) {
            // Prefer a durable project if available; otherwise fall back to any project.
            const durable = getProjects().find((p) => !p.expiresAt);
            projectId = (durable || getProjects()[0]).id;
        }
        if (projectId) {
            setSettingsProjectId(projectId);
            realBurndownURL = `/api/projects/${projectId}/burndown`;
        }
    }
    // Show Sprints tab only when in board view and user is Maintainer+ for that project
    let boardMembers = getBoardMembers();
    // If in board view but members not yet loaded (e.g. race on open, or opened before fetch completed), fetch them
    const slug = getSlug();
    const projectId = getProjectId();
    if (slug && projectId && currentUser && boardMembers.length === 0 && getBoard() && !isAnonymousBoard(getBoard())) {
        try {
            boardMembers = await fetchProjectMembers(projectId);
            setBoardMembers(boardMembers);
        }
        catch {
            boardMembers = [];
        }
    }
    const myMember = currentUser ? boardMembers.find((m) => m.userId === currentUser.id) : null;
    const showSprintsTab = !!slug && hasProjectAccess && myMember?.role === "maintainer";
    const showWorkflowTab = !!slug && hasProjectAccess && myMember?.role === "maintainer";
    // Charts tab only applies in durable project board view (not Dashboard/Projects/Temporary Boards, not anonymous mode, not temporary boards)
    const board = getBoard();
    const isTemporaryBoard = !!(board?.project?.expiresAt);
    const showChartsTab = !!slug &&
        hasProjectAccess &&
        getAuthStatusAvailable() &&
        !isTemporaryBoard;
    // Initialize active tab (default to Profile or Customization if no projects)
    if (!getSettingsActiveTab()) {
        if (showProfileTab) {
            setSettingsActiveTab("profile");
        }
        else if (hasProjectAccess) {
            setSettingsActiveTab("tag-colors");
        }
        else {
            setSettingsActiveTab("customization");
        }
    }
    else if (!showProfileTab && getSettingsActiveTab() === "profile") {
        setSettingsActiveTab(hasProjectAccess ? "tag-colors" : "customization");
    }
    else if (!showChartsTab && getSettingsActiveTab() === "charts") {
        setSettingsActiveTab(hasProjectAccess ? "tag-colors" : "customization");
    }
    else if (!showWorkflowTab && getSettingsActiveTab() === "workflow") {
        setSettingsActiveTab(hasProjectAccess ? "tag-colors" : "customization");
    }
    // Fetch full user profile (including avatar) when Profile tab is shown (skip when re-rendering after avatar change)
    if (showProfileTab && getUser() && !options?.skipProfileRefetch) {
        const profileRefetchVersion = ++settingsProfileRefetchVersion;
        settingsProfileRefetchController?.abort();
        settingsProfileRefetchController = new AbortController();
        try {
            const me = await apiFetch("/api/me", { signal: settingsProfileRefetchController.signal });
            if (me && profileRefetchVersion === settingsProfileRefetchVersion) {
                setUser(me);
            }
        }
        catch {
            // Ignore - user may have logged out, or this refetch was invalidated by a newer render/avatar mutation.
        }
        finally {
            if (profileRefetchVersion === settingsProfileRefetchVersion) {
                settingsProfileRefetchController = null;
            }
        }
    }
    // Fetch tags and chart data only if we have project access
    let tags = [];
    let tagsHTML = "";
    let realBurndownData = [];
    // Check if URLs changed (invalidate cache)
    const tagsURLChanged = cachedTagsURL !== tagsURL;
    const realBurndownURLChanged = cachedRealBurndownURL !== realBurndownURL;
    if (hasProjectAccess) {
        try {
            // Fetch tags only if URL changed or cache is empty
            if (tagsURLChanged || cachedTags === null) {
                tags = await apiFetch(tagsURL);
                // Sort tags alphabetically by name
                tags.sort((a, b) => a.name.localeCompare(b.name));
                // Update tag colors map
                const tagColors = {};
                tags.forEach((tag) => {
                    if (tag.color) {
                        tagColors[tag.name] = tag.color;
                    }
                });
                setTagColors(tagColors);
                const isDurableProject = !!getSettingsProjectId();
                tagsHTML = tags.length === 0
                    ? "<div class='muted'>No tags yet. Create todos with tags to see them here.</div>"
                    : tags.map((tag) => {
                        const colorValue = sanitizeHexColor(tag.color, "#9CA3AF") || "#9CA3AF";
                        // Show delete only when: canDelete === true AND tagId != null (both required)
                        const showDelete = tag.canDelete === true && tag.tagId != null;
                        // Durable project: require tagId for color update; disable picker if missing
                        const hasTagId = tag.tagId != null && tag.tagId > 0;
                        const colorDisabled = isDurableProject && !hasTagId;
                        const tagIdAttr = hasTagId ? ` data-tag-id="${String(tag.tagId)}"` : "";
                        return `
                <div class="settings-tag-item">
                  <span class="settings-tag-name" title="${escapeHTML(tag.name)}">${escapeHTML(tag.name)}</span>
                  <div class="settings-tag-color-controls">
                    <input 
                      type="color" 
                      class="settings-color-picker" 
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      value="${colorValue}"
                      title="${colorDisabled ? "Tag ID missing — cannot update color" : "Tag color"}"
                      ${colorDisabled ? "disabled" : ""}
                    />
                    <button 
                      class="btn btn--ghost btn--small settings-color-clear" 
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      title="Clear color"
                      ${!tag.color ? 'style="display: none;"' : ''}
                      ${colorDisabled ? "disabled" : ""}
                    >Clear</button>
                    ${showDelete ? `<button 
                      class="btn btn--danger btn--small settings-tag-delete" 
                      data-tag="${escapeHTML(tag.name)}"
                      data-tag-id="${String(tag.tagId)}"
                      title="Delete tag"
                      aria-label="Delete tag"
                    >✕</button>` : ''}
                  </div>
                </div>
              `;
                    }).join("");
                // Cache tags data
                cachedTags = tags;
                cachedTagsHTML = tagsHTML;
                cachedTagsURL = tagsURL;
            }
            else {
                // Use cached data
                tags = cachedTags;
                tagsHTML = cachedTagsHTML;
            }
            // Lazy-load chart data and sprints only when Charts tab is active
            const activeTab = getSettingsActiveTab();
            if (activeTab === "charts") {
                // Fetch sprints for burndown navigation
                const slug = getSlug();
                if (slug && (cachedSprintsForCharts === null || realBurndownURLChanged)) {
                    try {
                        const sprintsRes = await apiFetch(`/api/board/${slug}/sprints`);
                        const rawSprints = normalizeSprints(sprintsRes);
                        cachedSprintsForCharts = [...rawSprints].sort((a, b) => a.plannedStartAt - b.plannedStartAt);
                        // Auto-select sprint: active > last closed > first planned
                        burndownSprintIndex = computeDefaultBurndownSprintIndex(cachedSprintsForCharts);
                    }
                    catch {
                        cachedSprintsForCharts = [];
                    }
                }
                // When a sprint is selected in board view, use sprint-scoped burndown endpoint
                const sprints = cachedSprintsForCharts ?? [];
                const burndownSprintIndexClamped = sprints.length > 0 ? Math.min(burndownSprintIndex, sprints.length - 1) : 0;
                const currentSprintForFetch = sprints.length > 0 ? sprints[burndownSprintIndexClamped] : null;
                const effectiveBurndownURL = slug && currentSprintForFetch
                    ? `/api/board/${slug}/sprints/${currentSprintForFetch.id}/burndown`
                    : realBurndownURL;
                const effectiveBurndownURLChanged = cachedRealBurndownURL !== effectiveBurndownURL;
                if (effectiveBurndownURLChanged || cachedRealBurndownData === null) {
                    if (effectiveBurndownURL) {
                        try {
                            realBurndownData = await apiFetch(effectiveBurndownURL);
                            cachedRealBurndownData = realBurndownData;
                            cachedRealBurndownURL = effectiveBurndownURL;
                        }
                        catch (err) {
                            console.error("Failed to fetch real burndown data:", err);
                            realBurndownData = [];
                            cachedRealBurndownData = [];
                        }
                    }
                    else {
                        realBurndownData = [];
                        cachedRealBurndownData = [];
                    }
                }
                else {
                    realBurndownData = cachedRealBurndownData;
                }
            }
            else {
                // Not viewing charts tab - use empty data or cached if available
                realBurndownData = cachedRealBurndownData || [];
            }
        }
        catch (err) {
            console.error("Failed to fetch tags:", err);
            tagsHTML = `<div class='muted'>Error loading tags: ${escapeHTML(err.message)}</div>`;
            // Clear cache on error
            cachedTags = null;
            cachedTagsHTML = null;
            cachedTagsURL = null;
        }
    }
    else {
        // No project access - clear cache
        cachedTags = null;
        cachedTagsHTML = null;
        cachedTagsURL = null;
        cachedRealBurndownData = null;
        cachedRealBurndownURL = null;
    }
    // Get version from meta tag (embedded at build time)
    const versionText = getAppVersion();
    // Update the Settings title to include version number
    const titleEl = document.querySelector("#settingsDialog .dialog__title");
    if (titleEl && versionText) {
        titleEl.innerHTML = `Settings <span style="font-size: 0.75em; color: var(--muted); opacity: 0.6; font-weight: normal;">v${escapeHTML(versionText)}</span>`;
    }
    else if (titleEl) {
        titleEl.textContent = "Settings";
    }
    const profileHTML = (() => {
        if (!showProfileTab)
            return "";
        const u = getUser();
        const twoFactorSection = u ? (u.twoFactorEnabled
            ? `
        <div class="settings-section" style="margin-top: 24px;">
          <div class="settings-section__title">Two-factor authentication</div>
          <div class="settings-section__description muted">2FA is enabled. You can disable it or regenerate recovery codes.</div>
          <div style="margin-top: 12px; display: flex; flex-wrap: wrap; gap: 8px;">
            <button class="btn btn--ghost" id="disable2FABtn">Disable 2FA</button>
            <button class="btn btn--ghost" id="regenerateRecoveryCodesBtn">Regenerate recovery codes</button>
          </div>
        </div>
      `
            : `
        <div class="settings-section" style="margin-top: 24px;">
          <div class="settings-section__title">Two-factor authentication</div>
          <div class="settings-section__description muted">Add an extra layer of security with an authenticator app.</div>
          <button class="btn" id="enable2FABtn" style="margin-top: 8px;">Enable 2FA</button>
        </div>
      `) : "";
        return `
      <div class="settings-section" style="position: relative;">
        <div class="settings-section__title">Profile</div>
        <div class="settings-section__description muted">Signed-in user for this instance.</div>
        ${u ? `
          <div class="profile-avatar-wrap" style="margin-bottom: 16px;">
            <div style="display: flex; align-items: center; gap: 12px;">
              ${renderUserAvatar(u, { id: 'profileAvatarBtn', ariaLabel: 'Change avatar' })}
              ${u.image ? `<button class="btn btn--ghost" id="removeAvatarBtn">Remove avatar</button>` : ""}
            </div>
            <div id="profileAvatarError" class="muted" style="display: none; margin-top: 8px;" role="alert"></div>
          </div>
          <div class="settings-kv">
            <div class="settings-kv__row"><div class="muted">Name</div><div>${escapeHTML(u.name || "")}</div></div>
            <div class="settings-kv__row"><div class="muted">Email</div><div>${escapeHTML(u.email || "")}</div></div>
            <div class="settings-kv__row"><div class="muted">User ID</div><div>${u.id != null ? escapeHTML(String(u.id)) : ""}</div></div>
            <div class="settings-kv__row"><div class="muted">System Role</div><div>${u.systemRole ? escapeHTML(u.systemRole.charAt(0).toUpperCase() + u.systemRole.slice(1)) : "User"}</div></div>
            <div class="settings-kv__row"><div class="muted">Authentication</div><div>Authenticated</div></div>
          </div>
          <div style="margin-top: 16px; display: flex; gap: 8px;">
            <button class="btn btn--danger" id="logoutBtn">Log out</button>
            ${u.isBootstrap ? `<button class="btn" id="createUserBtn">Create User</button>` : ""}
          </div>
          ${twoFactorSection}
        ` : `
          <div class="muted">Not signed in.</div>
        `}
      </div>
    `;
    })();
    reloadKeybindingsFromStorage();
    const keybindingRowsHTML = KEY_ACTION_LIST.map((meta) => {
        const chord = getResolvedChordForAction(meta.id);
        return `
      <div class="keybinding-row" data-keybinding-row="${meta.id}">
        <span class="keybinding-row__label">${escapeHTML(meta.label)}</span>
        <button type="button" class="btn btn--ghost keybinding-capture" data-keybinding-capture data-keybinding-action="${meta.id}">
          ${escapeHTML(formatChordForDisplay(chord))}
        </button>
      </div>`;
    }).join("");
    const desktopNotifyGranted = typeof Notification !== "undefined" && Notification.permission === "granted";
    let pushVapidServerReady = false;
    if (showProfileTab) {
        try {
            const r = await fetch('/api/push/vapid-public-key', { credentials: 'same-origin' });
            if (r.ok) {
                const j = await r.json();
                pushVapidServerReady = !!(j && typeof j.publicKey === 'string' && j.publicKey.trim() !== '');
            }
        }
        catch {
            pushVapidServerReady = false;
        }
    }
    const pushPwaDisabledNotice = !pushVapidServerReady
        ? (showProfileTab
            ? 'Web Push needs VAPID keys set - See pwa.md)'
            : 'Web Push is not available in anonymous mode.')
        : '';
    const customizationHTML = `
      <div class="settings-section">
        <div class="settings-section__title">Theme</div>
        <div class="settings-section__description muted">Choose your preferred color scheme.</div>
        <div class="theme-selector theme-selector--inline">
          <label class="theme-option theme-option--inline">
            <input type="radio" name="theme" value="system" ${getStoredTheme() === THEME_SYSTEM ? "checked" : ""}>
            <span>System</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="theme" value="dark" ${getStoredTheme() === THEME_DARK ? "checked" : ""}>
            <span>Dark</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="theme" value="light" ${getStoredTheme() === THEME_LIGHT ? "checked" : ""}>
            <span>Light</span>
          </label>
        </div>
      </div>
      <div class="settings-section">
        <div class="settings-section__title">Desktop notifications</div>
        <div class="settings-section__description muted">OS-level alerts when someone assigns you a todo (works when this tab is in the background).</div>
        <p class="muted" style="margin: 8px 0;">${escapeHTML(getDesktopNotificationStatusDescription())}</p>
        <button type="button" class="btn" id="desktopNotifyEnableBtn" ${desktopNotifyGranted ? "disabled" : ""}>${desktopNotifyGranted ? "Notifications enabled" : "Enable notifications"}</button>
      </div>
      ${pushPwaDisabledNotice ? `<p class="settings-push-vapid-notice" role="status">${escapeHTML(pushPwaDisabledNotice)}</p>` : ''}
      <div class="settings-section settings-section--push-pwa${!pushVapidServerReady ? ' settings-section--push-pwa-disabled' : ''}">
        <div class="settings-section__title">Background notifications (PWA)</div>
        <div class="settings-section__description muted">Alerts when someone assigns you a todo while this app is in the background or closed (best on an installed PWA). Requires VAPID keys on the server. When configured, sign-in triggers an automatic subscribe attempt (the browser may ask for permission). Use the toggle to turn Web Push off or back on for this browser only.</div>
        <label class="row" style="align-items:center;gap:8px;margin-top:10px;cursor:pointer;">
          <input type="checkbox" id="pushNotifyToggle" ${!pushVapidServerReady ? 'disabled' : ''} />
          <span>Web Push on this device</span>
        </label>
        <p class="muted" id="pushNotifyHint" style="margin:8px 0 0 0;font-size:13px;"></p>
      </div>
      <div class="settings-section settings-section--keybindings">
        <div class="settings-section__title">Keybindings</div>
        <div class="settings-section__description muted">Click a key to record a new shortcut. Press Esc to cancel while listening.</div>
        <div class="keybinding-list">
          ${keybindingRowsHTML}
        </div>
      </div>
    `;
    // Determine content for each tab
    const tagColorsContent = hasProjectAccess
        ? `
      <div class="settings-section">
        <div class="settings-section__title">Tag Colors</div>
        <div class="settings-section__description muted">Assign custom colors to tags. Colors will appear in filter chips and todo cards.</div>
        <div class="settings-tags-list">
          ${tagsHTML}
        </div>
      </div>
    `
        : `
      <div class="settings-section">
        <div class="settings-section__title">Tag Colors</div>
        <div class="settings-section__description muted">Assign custom colors to tags. Colors will appear in filter chips and todo cards.</div>
        <div class="muted">No projects available. Create a project to manage tag colors.</div>
      </div>
    `;
    // Build charts content with sprint navigation
    const sprints = cachedSprintsForCharts ?? [];
    if (sprints.length > 0 && burndownSprintIndex >= sprints.length) {
        burndownSprintIndex = Math.max(0, sprints.length - 1);
    }
    const currentSprint = sprints.length > 0 ? sprints[burndownSprintIndex] : null;
    const canPrev = sprints.length > 0 && burndownSprintIndex > 0;
    const canNext = sprints.length > 0 && burndownSprintIndex < sprints.length - 1;
    const dataIsSprintScoped = !!slug && !!currentSprint;
    const chartHTML = currentSprint
        ? renderRealBurndownChart(realBurndownData, currentSprint, { canPrev, canNext }, dataIsSprintScoped)
        : renderRealBurndownChart(realBurndownData, undefined, undefined, dataIsSprintScoped);
    const chartsContent = hasProjectAccess
        ? `
      <div class="settings-section">
        <div class="charts-container">
          <div class="chart-block">${chartHTML}</div>
        </div>
      </div>
    `
        : `
      <div class="settings-section">
        <div class="muted">No projects available. Create a project to view charts.</div>
      </div>
    `;
    // Render users tab content if needed
    let usersHTML = "";
    if (showUsersTab && getSettingsActiveTab() === "users") {
        usersHTML = await renderUsersTabContent();
    }
    // Render sprints tab content if needed
    let sprintsHTML = "";
    if (showSprintsTab && getSettingsActiveTab() === "sprints") {
        sprintsHTML = await renderSprintsTabContent();
    }
    let workflowHTML = "";
    if (showWorkflowTab && getSettingsActiveTab() === "workflow" && slug) {
        if (workflowLaneCountsCache && workflowLaneCountsCache.slug !== slug) {
            invalidateWorkflowLaneCountsCache();
        }
        const cached = workflowLaneCountsCache?.slug === slug ? workflowLaneCountsCache.state : null;
        if (cached !== null) {
            workflowHTML = renderWorkflowTabContent(cached);
        }
        else {
            workflowHTML = renderWorkflowTabContent({ status: "loading" });
            const gen = workflowLaneCountsFetchGeneration;
            void (async () => {
                const state = await fetchWorkflowLaneCountsState(slug);
                if (gen !== workflowLaneCountsFetchGeneration)
                    return;
                if (getSlug() !== slug)
                    return;
                workflowLaneCountsCache = { slug, state };
                if (getSettingsActiveTab() !== "workflow")
                    return;
                await renderSettingsModal();
            })();
        }
    }
    destroyBurndownChart();
    contentEl.innerHTML = `
    <div class="settings-tabs">
      ${showProfileTab ? `<button class="settings-tab ${getSettingsActiveTab() === "profile" ? "settings-tab--active" : ""}" data-tab="profile">Profile</button>` : ``}
      ${showUsersTab ? `<button class="settings-tab ${getSettingsActiveTab() === "users" ? "settings-tab--active" : ""}" data-tab="users">Users</button>` : ``}
      ${showSprintsTab ? `<button class="settings-tab ${getSettingsActiveTab() === "sprints" ? "settings-tab--active" : ""}" data-tab="sprints">Sprints</button>` : ``}
      ${showWorkflowTab ? `<button class="settings-tab ${getSettingsActiveTab() === "workflow" ? "settings-tab--active" : ""}" data-tab="workflow">Workflow</button>` : ``}
      <button class="settings-tab ${getSettingsActiveTab() === "customization" ? "settings-tab--active" : ""}" data-tab="customization">Customization</button>
      <button class="settings-tab ${getSettingsActiveTab() === "tag-colors" ? "settings-tab--active" : ""}" data-tab="tag-colors">Tag Colors</button>
      ${showChartsTab ? `<button class="settings-tab ${getSettingsActiveTab() === "charts" ? "settings-tab--active" : ""}" data-tab="charts">Charts</button>` : ``}
      <button class="settings-tab ${getSettingsActiveTab() === "backup" ? "settings-tab--active" : ""}" data-tab="backup">Backup</button>
    </div>
    <div class="settings-tab-content" id="settingsTabContent">
      ${getSettingsActiveTab() === "profile" ? profileHTML : getSettingsActiveTab() === "users" ? usersHTML : getSettingsActiveTab() === "sprints" ? sprintsHTML : getSettingsActiveTab() === "workflow" ? workflowHTML : getSettingsActiveTab() === "customization" ? customizationHTML : getSettingsActiveTab() === "tag-colors" ? tagColorsContent : getSettingsActiveTab() === "charts" ? chartsContent : getSettingsActiveTab() === "backup" ? renderBackupTabHTML() : ""}
    </div>
  `;
    // Abort previous listeners before attaching new ones
    if (keybindingCaptureKeydown) {
        window.removeEventListener("keydown", keybindingCaptureKeydown, true);
        keybindingCaptureKeydown = null;
    }
    setKeybindingsCaptureListening(false);
    settingsAbortController?.abort();
    settingsAbortController = new AbortController();
    const signal = settingsAbortController.signal;
    // Charts tab: burndown sprint navigation, mount uPlot chart, scrollbar behavior
    if (getSettingsActiveTab() === "charts") {
        const prevBtn = document.getElementById("burndown-prev");
        const nextBtn = document.getElementById("burndown-next");
        if (prevBtn) {
            prevBtn.addEventListener("click", async () => {
                if (burndownSprintIndex > 0) {
                    burndownSprintIndex--;
                    await renderSettingsModal();
                }
            }, { signal });
        }
        if (nextBtn) {
            nextBtn.addEventListener("click", async () => {
                const sprints = cachedSprintsForCharts ?? [];
                if (burndownSprintIndex < sprints.length - 1) {
                    burndownSprintIndex++;
                    await renderSettingsModal();
                }
            }, { signal });
        }
        const mount = contentEl.querySelector("#burndown-uplot-mount");
        if (mount) {
            destroyBurndownChart();
            mountBurndownChart(mount, realBurndownData, currentSprint ?? null, dataIsSprintScoped);
        }
        contentEl.classList.add("settings-content--charts");
        let scrollbarTimeout;
        contentEl.addEventListener("scroll", () => {
            contentEl.classList.add("scrollbar-visible");
            clearTimeout(scrollbarTimeout);
            scrollbarTimeout = setTimeout(() => {
                contentEl.classList.remove("scrollbar-visible");
            }, 1500);
        }, { signal });
    }
    else {
        contentEl.classList.remove("settings-content--charts");
        contentEl.classList.remove("scrollbar-visible");
    }
    if (getSettingsActiveTab() === "profile") {
        contentEl.classList.add("settings-content--profile");
    }
    else {
        contentEl.classList.remove("settings-content--profile");
    }
    // Setup tab switching (click)
    document.querySelectorAll(".settings-tab").forEach(tab => {
        tab.addEventListener("click", (e) => {
            const tabName = e.target.getAttribute("data-tab");
            if (tabName)
                void switchSettingsTab(tabName);
        }, { signal });
    });
    // Setup tab switching (keyboard: Tab cycles visible tabs)
    const settingsDlgForKeyboard = document.getElementById("settingsDialog");
    if (settingsDlgForKeyboard) {
        settingsDlgForKeyboard.addEventListener("keydown", (e) => {
            if (e.key !== "Tab" || e.shiftKey)
                return;
            if (isTypingInTextField())
                return;
            e.preventDefault();
            const tabs = Array.from(settingsDlgForKeyboard.querySelectorAll(".settings-tab[data-tab]"));
            if (tabs.length === 0)
                return;
            const current = getSettingsActiveTab();
            const idx = tabs.findIndex((t) => t.getAttribute("data-tab") === current);
            const next = (idx + 1) % tabs.length;
            const nextTab = tabs[next].getAttribute("data-tab");
            if (nextTab)
                void switchSettingsTab(nextTab);
        }, { signal });
    }
    // Setup backup tab if it's active
    if (getSettingsActiveTab() === "backup") {
        // Wait a tick for DOM to be ready
        setTimeout(() => {
            setupBackupTab(signal);
        }, 0);
    }
    if (getSettingsActiveTab() === "workflow") {
        const addInput = document.querySelector("[data-workflow-ghost-input]");
        const addLane = () => {
            if (!addInput)
                return;
            addWorkflowLane(addInput.value);
        };
        const addBtn = document.querySelector("[data-workflow-add]");
        if (addBtn) {
            addBtn.addEventListener("click", addLane, { signal });
        }
        if (addInput) {
            addInput.addEventListener("keydown", (e) => {
                if (e.key !== "Enter")
                    return;
                e.preventDefault();
                addLane();
            }, { signal });
        }
        document.querySelectorAll("[data-workflow-name]").forEach((inputEl) => {
            const key = inputEl.getAttribute("data-workflow-name");
            if (!key)
                return;
            inputEl.addEventListener("input", () => {
                const lane = workflowTabDraft?.find((l) => l.key === key);
                if (lane)
                    lane.name = inputEl.value;
                updateWorkflowSaveFooter();
            }, { signal });
        });
        document.querySelectorAll("[data-workflow-color]").forEach((colorEl) => {
            const key = colorEl.getAttribute("data-workflow-color");
            if (!key)
                return;
            colorEl.addEventListener("input", () => {
                const lane = workflowTabDraft?.find((l) => l.key === key);
                if (lane)
                    lane.color = colorEl.value || DEFAULT_WORKFLOW_LANE_COLOR;
                updateWorkflowSaveFooter();
            }, { signal });
        });
        document.querySelectorAll("[data-workflow-delete]").forEach((btn) => {
            btn.addEventListener("click", () => {
                const key = btn.getAttribute("data-workflow-delete");
                if (!key)
                    return;
                void deleteWorkflowLane(key);
            }, { signal });
        });
        const saveChangesBtn = document.querySelector("[data-workflow-save-changes]");
        if (saveChangesBtn) {
            saveChangesBtn.addEventListener("click", () => {
                void saveWorkflowDraftChanges();
            }, { signal });
        }
        const cancelDraftBtn = document.querySelector("[data-workflow-draft-cancel]");
        if (cancelDraftBtn) {
            cancelDraftBtn.addEventListener("click", () => {
                resetWorkflowDraftToBaseline();
                void renderSettingsModal();
            }, { signal });
        }
        const retryCountsBtn = document.querySelector("[data-workflow-counts-retry]");
        if (retryCountsBtn) {
            retryCountsBtn.addEventListener("click", () => {
                invalidateWorkflowLaneCountsCache();
                void renderSettingsModal();
            }, { signal });
        }
    }
    const settingsDlg = settingsDialog;
    if (settingsDlg) {
        const onDialogCancel = (e) => {
            if (getSettingsActiveTab() !== "workflow" || !isWorkflowDraftDirty())
                return;
            e.preventDefault();
            void showConfirmDialog("You have unsaved changes. Discard them?", "Unsaved changes", "Discard").then((discard) => {
                if (discard) {
                    resetWorkflowDraftToBaseline();
                    clearWorkflowDraftState();
                    settingsDlg.close();
                }
            });
        };
        settingsDlg.addEventListener("cancel", onDialogCancel, { signal });
        settingsDlg.addEventListener("close", () => clearWorkflowDraftState(), { signal });
    }
    if (closeSettingsBtn) {
        const onCloseClick = (e) => {
            if (getSettingsActiveTab() !== "workflow" || !isWorkflowDraftDirty())
                return;
            e.preventDefault();
            e.stopImmediatePropagation();
            void showConfirmDialog("You have unsaved changes. Discard them?", "Unsaved changes", "Discard").then((discard) => {
                if (discard) {
                    resetWorkflowDraftToBaseline();
                    clearWorkflowDraftState();
                    settingsDialog.close();
                }
            });
        };
        closeSettingsBtn.addEventListener("click", onCloseClick, { capture: true, signal });
    }
    // Setup logout button — use form POST so browser processes Set-Cookie from document response
    // (fetch/XHR responses don't always clear cookies reliably across browsers)
    const logoutBtn = document.getElementById("logoutBtn");
    if (logoutBtn) {
        logoutBtn.addEventListener("click", () => {
            settingsDialog.close();
            const form = document.createElement("form");
            form.method = "POST";
            form.action = "/api/auth/logout";
            document.body.appendChild(form);
            form.submit();
        }, { signal });
    }
    // Profile avatar click: open file picker to change avatar
    const profileAvatarBtn = document.getElementById("profileAvatarBtn");
    const profileAvatarError = document.getElementById("profileAvatarError");
    if (profileAvatarBtn) {
        profileAvatarBtn.addEventListener("click", () => {
            if (profileAvatarError) {
                profileAvatarError.style.display = "none";
                profileAvatarError.textContent = "";
            }
            const input = document.createElement("input");
            input.type = "file";
            input.accept = "image/*";
            input.onchange = async (e) => {
                const file = e.target.files?.[0];
                if (!file)
                    return;
                try {
                    invalidateSettingsProfileRefetch();
                    const dataUrl = await processImageFile(file);
                    const updated = await apiFetch("/api/me", {
                        method: "PATCH",
                        body: JSON.stringify({ image: dataUrl }),
                    });
                    if (updated)
                        setUser(updated);
                    refreshAvatarsOutsideSettings();
                    await renderSettingsModal({ skipProfileRefetch: true });
                    showToast("Avatar updated");
                }
                catch (err) {
                    const msg = err?.message ?? String(err) ?? "Upload failed";
                    showToast(msg);
                    if (profileAvatarError) {
                        profileAvatarError.textContent = msg;
                        profileAvatarError.style.display = "block";
                    }
                }
            };
            input.click();
        }, { signal });
    }
    // Remove avatar button
    const removeAvatarBtn = document.getElementById("removeAvatarBtn");
    if (removeAvatarBtn) {
        removeAvatarBtn.addEventListener("click", async () => {
            try {
                invalidateSettingsProfileRefetch();
                const updated = await apiFetch("/api/me", {
                    method: "PATCH",
                    body: JSON.stringify({ image: null }),
                });
                if (updated)
                    setUser(updated);
                refreshAvatarsOutsideSettings();
                await renderSettingsModal({ skipProfileRefetch: true });
                showToast("Avatar removed");
            }
            catch (err) {
                showToast(err.message);
            }
        }, { signal });
    }
    // Setup create user button (bootstrap only or admin/owner)
    const createUserBtn = document.getElementById("createUserBtn");
    if (createUserBtn) {
        createUserBtn.addEventListener("click", () => {
            showCreateUserDialog();
        }, { signal });
    }
    // Setup 2FA buttons
    const enable2FABtn = document.getElementById("enable2FABtn");
    if (enable2FABtn) {
        enable2FABtn.addEventListener("click", () => showEnable2FADialog(), { signal });
    }
    const disable2FABtn = document.getElementById("disable2FABtn");
    if (disable2FABtn) {
        disable2FABtn.addEventListener("click", () => showDisable2FADialog(), { signal });
    }
    const regenerateRecoveryCodesBtn = document.getElementById("regenerateRecoveryCodesBtn");
    if (regenerateRecoveryCodesBtn) {
        regenerateRecoveryCodesBtn.addEventListener("click", () => showRegenerateRecoveryCodesDialog(), { signal });
    }
    // Setup user management actions (users tab)
    if (getSettingsActiveTab() === "users") {
        // Promote button
        document.querySelectorAll('[data-action="promote"]').forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const userId = e.currentTarget.getAttribute("data-user-id");
                if (!userId)
                    return;
                try {
                    await apiFetch(`/api/admin/users/${userId}/role`, {
                        method: "PATCH",
                        body: JSON.stringify({ role: "admin" }),
                    });
                    showToast("User promoted to admin");
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to promote user");
                }
            }, { signal });
        });
        // Demote button
        document.querySelectorAll('[data-action="demote"]').forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const userId = e.currentTarget.getAttribute("data-user-id");
                if (!userId)
                    return;
                if (!confirm("Demote this user from admin to regular user?")) {
                    return;
                }
                try {
                    await apiFetch(`/api/admin/users/${userId}/role`, {
                        method: "PATCH",
                        body: JSON.stringify({ role: "user" }),
                    });
                    showToast("User demoted to regular user");
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to demote user");
                }
            }, { signal });
        });
        // Delete button
        document.querySelectorAll('[data-action="delete"]').forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const userId = e.currentTarget.getAttribute("data-user-id");
                if (!userId)
                    return;
                if (!confirm("Delete this user? This action cannot be undone.")) {
                    return;
                }
                try {
                    await apiFetch(`/api/admin/users/${userId}`, {
                        method: "DELETE",
                    });
                    showToast("User deleted");
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to delete user");
                }
            }, { signal });
        });
        // Password button
        document.querySelectorAll('[data-action="password"]').forEach(btn => {
            btn.addEventListener("click", (e) => {
                const userId = e.currentTarget.getAttribute("data-user-id");
                if (!userId)
                    return;
                showPasswordResetDialog(userId);
            }, { signal });
        });
    }
    // Setup sprints tab (create, activate, close)
    if (getSettingsActiveTab() === "sprints") {
        const defaultWeeksEl = document.getElementById("sprintDefaultWeeksSelect");
        const startEl = document.getElementById("sprintStartInput");
        const endEl = document.getElementById("sprintEndInput");
        let userHasEditedEndDate = false;
        if (endEl) {
            const markEdited = () => { userHasEditedEndDate = true; };
            endEl.addEventListener("input", markEdited, { signal });
            endEl.addEventListener("change", markEdited, { signal });
        }
        if (defaultWeeksEl && endEl) {
            defaultWeeksEl.addEventListener("change", () => {
                if (userHasEditedEndDate)
                    return;
                const weeks = parseInt(defaultWeeksEl.value, 10);
                const start = startEl?.value ? new Date(startEl.value) : computeDefaultSprintStart(new Date());
                if (!Number.isFinite(start.getTime()))
                    return;
                const computedEnd = computeDefaultSprintEnd(start, weeks);
                endEl.value = msToDateTimeLocalStr(computedEnd.getTime());
            }, { signal });
        }
        const createSprintBtn = document.getElementById("createSprintBtn");
        if (createSprintBtn) {
            createSprintBtn.addEventListener("click", async () => {
                const slug = getSlug();
                if (!slug)
                    return;
                const nameEl = document.getElementById("sprintNameInput");
                const name = nameEl?.value?.trim();
                const startStr = startEl?.value;
                const endStr = endEl?.value;
                if (!name) {
                    showToast("Name is required");
                    return;
                }
                if (!startStr || !endStr) {
                    showToast("Start and end dates are required");
                    return;
                }
                const plannedStartAt = new Date(startStr).getTime();
                const plannedEndAt = new Date(endStr).getTime();
                if (!Number.isFinite(plannedStartAt) || !Number.isFinite(plannedEndAt)) {
                    showToast("Invalid start or end date");
                    return;
                }
                if (plannedEndAt < plannedStartAt) {
                    showToast("End date must be after start date");
                    return;
                }
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${slug}/sprints`, {
                        method: "POST",
                        body: JSON.stringify({ name, plannedStartAt, plannedEndAt }),
                    });
                    const selectedWeeks = parseInt(defaultWeeksEl?.value ?? "", 10);
                    if (selectedWeeks === 1 || selectedWeeks === 2) {
                        recordLocalMutation();
                        apiFetch(`/api/board/${slug}/settings`, {
                            method: "PATCH",
                            body: JSON.stringify({ defaultSprintWeeks: selectedWeeks }),
                        }).then((resp) => {
                            const board = getBoard();
                            const nextWeeks = resp?.defaultSprintWeeks === 1 ? 1 : 2;
                            if (board) {
                                setBoard({
                                    ...board,
                                    project: {
                                        ...board.project,
                                        defaultSprintWeeks: nextWeeks,
                                    },
                                });
                            }
                        }).catch(() => {
                            // Best-effort settings persistence; ignore failures.
                        });
                    }
                    showToast("Sprint created");
                    invalidateSprintsForChartsCache();
                    refreshSprintsAndChips(getSlug() ?? "").catch(() => { });
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to create sprint");
                }
            }, { signal });
        }
        document.querySelectorAll("[data-sprint-activate]").forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const target = e.target;
                const sprintId = target.getAttribute("data-sprint-activate");
                const slug = getSlug();
                if (!sprintId || !slug)
                    return;
                const row = target.closest('[data-sprint-id]');
                const plannedStartRaw = row?.getAttribute("data-sprint-planned-start-at") ?? "";
                const sprintName = row?.getAttribute("data-sprint-name") ?? "Sprint";
                const plannedMs = parseInt(plannedStartRaw, 10);
                if (Number.isFinite(plannedMs) && Math.abs(plannedMs - Date.now()) > 60000) {
                    const plannedLabel = new Date(plannedMs).toLocaleString(undefined, {
                        month: "short",
                        day: "numeric",
                        year: "numeric",
                        hour: "numeric",
                        minute: "2-digit",
                    });
                    const confirmed = await showConfirmDialog(`${sprintName} will start now (activation time). Work completed after this moment will count. Planned start was ${plannedLabel}. Continue?`, "Start sprint now?", "Start Sprint");
                    if (!confirmed)
                        return;
                }
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${slug}/sprints/${sprintId}/activate`, { method: "POST" });
                    showToast("Sprint activated");
                    invalidateSprintsForChartsCache();
                    emit("sprint-updated", { sprintId: parseInt(sprintId, 10), state: "ACTIVE" });
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to activate sprint");
                }
            }, { signal });
        });
        document.querySelectorAll("[data-sprint-close]").forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const sprintId = e.target.getAttribute("data-sprint-close");
                const slug = getSlug();
                if (!sprintId || !slug)
                    return;
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${slug}/sprints/${sprintId}/close`, { method: "POST" });
                    showToast("Sprint closed");
                    invalidateSprintsForChartsCache();
                    emit("sprint-updated", { sprintId: parseInt(sprintId, 10), state: "CLOSED" });
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to close sprint");
                }
            }, { signal });
        });
        document.querySelectorAll("[data-sprint-edit]").forEach(btn => {
            btn.addEventListener("click", (e) => {
                const sprintId = e.target.getAttribute("data-sprint-edit");
                if (!sprintId)
                    return;
                editingSprintId = parseInt(sprintId, 10);
                renderSettingsModal();
            }, { signal });
        });
        document.querySelectorAll("[data-sprint-cancel]").forEach(btn => {
            btn.addEventListener("click", () => {
                editingSprintId = null;
                renderSettingsModal();
            }, { signal });
        });
        document.querySelectorAll("[data-sprint-save]").forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const sprintId = e.target.getAttribute("data-sprint-save");
                const slug = getSlug();
                if (!sprintId || !slug)
                    return;
                const row = document.querySelector(`[data-sprint-id="${sprintId}"].settings-sprint-row--editing`);
                if (!row)
                    return;
                const state = row.getAttribute("data-sprint-state") ?? "";
                const body = {};
                if (state === "PLANNED" || state === "CLOSED") {
                    const nameEl = row.querySelector("[data-sprint-edit-name]");
                    if (nameEl)
                        body.name = nameEl.value.trim();
                }
                if (state === "PLANNED") {
                    const startEl = row.querySelector("[data-sprint-edit-start]");
                    const endEl = row.querySelector("[data-sprint-edit-end]");
                    if (startEl?.value && endEl?.value) {
                        body.plannedStartAt = new Date(startEl.value).getTime();
                        body.plannedEndAt = new Date(endEl.value).getTime();
                    }
                }
                if (state === "ACTIVE") {
                    const endEl = row.querySelector("[data-sprint-edit-end]");
                    if (endEl?.value) {
                        body.plannedEndAt = new Date(endEl.value).getTime();
                    }
                }
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${slug}/sprints/${sprintId}`, { method: "PATCH", body: JSON.stringify(body) });
                    showToast("Sprint updated");
                    invalidateSprintsForChartsCache();
                    editingSprintId = null;
                    refreshSprintsAndChips(getSlug() ?? "").catch(() => { });
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to update sprint");
                }
            }, { signal });
        });
        document.querySelectorAll("[data-sprint-delete]").forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const sprintId = e.target.getAttribute("data-sprint-delete");
                const slug = getSlug();
                if (!sprintId || !slug)
                    return;
                const row = document.querySelector(`[data-sprint-id="${sprintId}"]`);
                if (!row)
                    return;
                const state = row.getAttribute("data-sprint-state") ?? "";
                const nameEl = row.querySelector("strong");
                const name = nameEl?.textContent ?? "Sprint";
                const todoCount = parseInt(row.getAttribute("data-sprint-todo-count") ?? "0", 10) || 0;
                const storyWord = todoCount === 1 ? "story" : "stories";
                let message;
                let title = "Delete sprint?";
                if (state === "ACTIVE") {
                    message = `This sprint is currently active. Deleting it will immediately end the sprint and move ${todoCount} ${storyWord} back to backlog.`;
                }
                else if (todoCount === 0) {
                    message = `Sprint '${name}' will be permanently deleted.`;
                }
                else {
                    message = `Sprint '${name}' has ${todoCount} ${storyWord}. They will be moved to backlog (unassigned from this sprint). The sprint will be permanently deleted.`;
                }
                const confirmed = await showConfirmDialog(message, title, "Delete");
                if (!confirmed)
                    return;
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${slug}/sprints/${sprintId}`, { method: "DELETE" });
                    showToast("Sprint deleted");
                    invalidateSprintsForChartsCache();
                    editingSprintId = null;
                    refreshSprintsAndChips(getSlug() ?? "").catch(() => { });
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(err.message || "Failed to delete sprint");
                }
            }, { signal });
        });
    }
    // Setup theme selector
    document.querySelectorAll('input[name="theme"]').forEach(radio => {
        radio.addEventListener('change', (e) => {
            handleThemeChange(e.target.value);
        }, { signal });
    });
    if (getSettingsActiveTab() === "customization") {
        const desktopNotifyBtn = document.getElementById("desktopNotifyEnableBtn");
        if (desktopNotifyBtn && !desktopNotifyBtn.hasAttribute("disabled")) {
            desktopNotifyBtn.addEventListener("click", async () => {
                const r = await requestDesktopNotificationPermission();
                if (r === "granted") {
                    showToast("Desktop notifications enabled");
                }
                else if (r === "denied") {
                    showToast("Notifications blocked — you can allow them in your browser settings for this site");
                }
                else {
                    showToast("Notification permission not granted");
                }
                await renderSettingsModal();
            }, { signal });
        }
        const pushToggle = document.getElementById("pushNotifyToggle");
        const pushHint = document.getElementById("pushNotifyHint");
        if (pushToggle) {
            if (!pushVapidServerReady) {
                pushToggle.checked = false;
                if (pushHint) {
                    pushHint.textContent = "";
                }
            }
            else if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
                pushToggle.disabled = true;
                if (pushHint) {
                    pushHint.textContent = "Web Push is not supported in this browser.";
                }
            }
            else {
                isPushSubscribed()
                    .then((on) => {
                    pushToggle.checked = on;
                })
                    .catch(() => { });
                pushToggle.addEventListener("change", async () => {
                    if (pushToggle.checked) {
                        const ok = await subscribeToPush();
                        if (!ok) {
                            pushToggle.checked = false;
                            showToast("Could not enable Web Push. Allow notifications or check server VAPID configuration.");
                        }
                        else {
                            showToast("Web Push enabled");
                        }
                    }
                    else {
                        await unsubscribeFromPush();
                        showToast("Web Push disabled");
                    }
                    await renderSettingsModal();
                }, { signal });
            }
        }
        resetKeybindingCaptureUI();
        document.querySelectorAll("[data-keybinding-capture]").forEach((btn) => {
            btn.addEventListener("click", () => {
                resetKeybindingCaptureUI();
                const actionId = btn.getAttribute("data-keybinding-action");
                if (!actionId)
                    return;
                btn.classList.add("keybinding-capture--listening");
                btn.textContent = "Press a key…";
                setKeybindingsCaptureListening(true);
                const onKey = (e) => {
                    if (e.key === "Escape") {
                        e.preventDefault();
                        e.stopPropagation();
                        resetKeybindingCaptureUI();
                        return;
                    }
                    e.preventDefault();
                    e.stopPropagation();
                    const chord = chordFromKeyboardEvent(e);
                    if (!chord)
                        return;
                    const saved = saveKeybindingOverride(actionId, chord);
                    // Teardown order: remove listener, clear ref, then flag (avoid global handler seeing capture off while listener still registered).
                    window.removeEventListener("keydown", onKey, true);
                    if (keybindingCaptureKeydown === onKey) {
                        keybindingCaptureKeydown = null;
                    }
                    setKeybindingsCaptureListening(false);
                    btn.classList.remove("keybinding-capture--listening");
                    const resolvedLabel = formatChordForDisplay(getResolvedChordForAction(actionId));
                    if (saved) {
                        btn.textContent = resolvedLabel;
                        btn.classList.remove("keybinding-capture--error");
                    }
                    else {
                        // Previous binding unchanged in storage; show it immediately + error outline (no timed revert).
                        btn.textContent = resolvedLabel;
                        btn.classList.add("keybinding-capture--error");
                    }
                };
                keybindingCaptureKeydown = onKey;
                window.addEventListener("keydown", onKey, true);
            }, { signal });
        });
    }
    // Setup event listeners for color pickers (only if we have project access)
    if (hasProjectAccess) {
        document.querySelectorAll(".settings-color-picker").forEach(picker => {
            picker.addEventListener("change", async (e) => {
                const el = e.target;
                const tagName = el.getAttribute("data-tag");
                const tagIdAttr = el.getAttribute("data-tag-id");
                const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
                const color = el.value;
                if (tagName) {
                    await updateTagColor(tagName, Number.isNaN(tagId) ? undefined : tagId, color);
                }
            }, { signal });
        });
        // Setup event listeners for clear buttons
        document.querySelectorAll(".settings-color-clear").forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const el = e.target;
                const tagName = el.getAttribute("data-tag");
                const tagIdAttr = el.getAttribute("data-tag-id");
                const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
                if (tagName) {
                    await updateTagColor(tagName, Number.isNaN(tagId) ? undefined : tagId, null);
                }
            }, { signal });
        });
        // Setup event listeners for delete buttons
        document.querySelectorAll(".settings-tag-delete").forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const el = e.target;
                const tagName = el.getAttribute("data-tag");
                const tagIdAttr = el.getAttribute("data-tag-id");
                const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
                if (tagName) {
                    const confirmed = await showConfirmDialog(`Delete tag "${tagName}" from all projects? This will remove it from all todos.`, "Delete Tag");
                    if (!confirmed) {
                        return;
                    }
                    await deleteTag(tagName, !Number.isNaN(tagId) ? tagId : undefined);
                }
            }, { signal });
        });
    }
}
async function renderUsersTabContent() {
    const currentUser = getUser();
    const isOwner = currentUser?.systemRole === "owner";
    const isAdmin = currentUser?.systemRole === "admin";
    try {
        const users = await apiFetch("/api/admin/users");
        if (users.length === 0) {
            return `<div class="settings-section"><div class="muted">No users found.</div></div>`;
        }
        const rows = users.map((user) => {
            const isSelf = user.id === currentUser?.id;
            const userRole = user.systemRole || "user";
            const isUserRole = userRole === "user";
            const isAdminRole = userRole === "admin";
            const isOwnerRole = userRole === "owner";
            // Determine available actions
            let actionsHTML = "—";
            if (isOwner) {
                // Owner can manage all users except themselves
                if (isSelf) {
                    // Self: no delete, no demote if last owner
                    actionsHTML = "—";
                }
                else if (isOwnerRole) {
                    // Other owner: no actions (can't demote/promote owners, can't delete owners)
                    actionsHTML = "—";
                }
                else if (isAdminRole) {
                    // Admin: can demote to user or delete
                    actionsHTML = `
            <div class="users-table__actions">
              <button class="btn btn--ghost btn--small" data-action="demote" data-user-id="${user.id}" data-user-role="${userRole}">Demote</button>
              <button class="btn btn--danger btn--small" data-action="delete" data-user-id="${user.id}">Delete</button>
              <button class="btn btn--ghost btn--small" data-action="password" data-user-id="${user.id}">Password</button>
            </div>
          `;
                }
                else if (isUserRole) {
                    // User: can promote to admin or delete
                    actionsHTML = `
            <div class="users-table__actions">
              <button class="btn btn--ghost btn--small" data-action="promote" data-user-id="${user.id}" data-user-role="${userRole}">Promote</button>
              <button class="btn btn--danger btn--small" data-action="delete" data-user-id="${user.id}">Delete</button>
              <button class="btn btn--ghost btn--small" data-action="password" data-user-id="${user.id}">Password</button>
            </div>
          `;
                }
            }
            else if (isAdmin) {
                // Admin: can view but not manage
                actionsHTML = "—";
            }
            const roleDisplay = userRole.charAt(0).toUpperCase() + userRole.slice(1);
            const userDisplay = user.name || user.email || `User ${user.id}`;
            return `
        <tr>
          <td>${escapeHTML(userDisplay)}${user.email && user.name ? ` <span class="muted">(${escapeHTML(user.email)})</span>` : ""}</td>
          <td>${escapeHTML(roleDisplay)}</td>
          <td>${actionsHTML}</td>
        </tr>
      `;
        }).join("");
        return `
      <div class="settings-section">
        <div class="settings-section__title">User Management</div>
        <div class="settings-section__description muted">Manage system users and roles.</div>
        <table class="users-table">
          <thead>
            <tr>
              <th style="width: 35%;">User</th>
              <th>System Role</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            ${rows}
          </tbody>
        </table>
        ${isOwner || isAdmin ? `<div style="margin-top: 16px;"><button class="btn btn--ghost" id="createUserBtn">Create User</button></div>` : ""}
      </div>
    `;
    }
    catch (err) {
        return `<div class="settings-section"><div class="muted">Error loading users: ${escapeHTML(err.message || "Unknown error")}</div></div>`;
    }
}
function showPasswordResetDialog(userId) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form" id="passwordResetForm">
      <div class="dialog__header">
        <div class="dialog__title">Reset Password</div>
        <button class="btn btn--ghost" type="button" id="passwordResetDialogClose" aria-label="Close">✕</button>
      </div>

      <p class="muted">Generate a one-time password reset link. The link will expire in 30 minutes.</p>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="passwordResetCancel">Cancel</button>
        <button type="submit" class="btn" id="passwordResetGenerate">Generate Link</button>
      </div>
    </form>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const closeBtn = document.getElementById("passwordResetDialogClose");
    const cancelBtn = document.getElementById("passwordResetCancel");
    const form = document.getElementById("passwordResetForm");
    const close = () => {
        document.body.removeChild(dialog);
    };
    if (closeBtn)
        closeBtn.addEventListener("click", close);
    if (cancelBtn)
        cancelBtn.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
    if (form) {
        form.addEventListener("submit", async (e) => {
            e.preventDefault();
            try {
                const res = await apiFetch(`/api/admin/users/${userId}/password-reset`, { method: "POST" });
                if (!res?.reset_url) {
                    showToast("Failed to generate reset link");
                    return;
                }
                try {
                    await navigator.clipboard.writeText(res.reset_url);
                    showToast("Reset link copied to clipboard (expires in 30 minutes)");
                    close();
                }
                catch {
                    showPasswordResetFallbackDialog(res.reset_url);
                    close();
                }
            }
            catch (err) {
                showToast(err.message || "Failed to generate reset link");
            }
        });
    }
}
function showPasswordResetFallbackDialog(resetUrl) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title">Reset link generated</div>
        <button class="btn btn--ghost" type="button" id="passwordResetFallbackClose" aria-label="Close">✕</button>
      </div>

      <p class="muted">Copy the link below and share it with the user. The link expires in 30 minutes.</p>
      <div class="field" style="margin: 12px 0;">
        <input type="text" id="passwordResetUrlDisplay" class="input" readonly value="${escapeHTML(resetUrl)}" style="font-size: 12px;" />
      </div>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn" id="passwordResetFallbackCopy">Copy</button>
      </div>
    </div>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const closeBtn = document.getElementById("passwordResetFallbackClose");
    const copyBtn = document.getElementById("passwordResetFallbackCopy");
    const urlInput = document.getElementById("passwordResetUrlDisplay");
    const close = () => {
        document.body.removeChild(dialog);
    };
    if (closeBtn)
        closeBtn.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
    if (copyBtn && urlInput) {
        copyBtn.addEventListener("click", async () => {
            try {
                await navigator.clipboard.writeText(urlInput.value);
                showToast("Link copied to clipboard");
            }
            catch {
                urlInput.select();
                showToast("Select the link and copy manually (Ctrl+C)");
            }
        });
    }
}
function showCreateUserDialog() {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form" id="createUserForm">
      <div class="dialog__header">
        <div class="dialog__title">Create User</div>
        <button class="btn btn--ghost" type="button" id="createUserDialogClose" aria-label="Close">✕</button>
      </div>

      <label class="field">
        <div class="field__label">Email</div>
        <input 
          type="email" 
          id="createUserEmail" 
          class="input" 
          placeholder="user@example.com" 
          maxlength="200" 
          autocomplete="email" 
          required 
        />
      </label>

      <label class="field">
        <div class="field__label">Name</div>
        <input 
          type="text" 
          id="createUserName" 
          class="input" 
          placeholder="User Name" 
          maxlength="200" 
          autocomplete="name" 
          required 
        />
      </label>

      <label class="field">
        <div class="field__label">Temporary Password</div>
        <div class="password-row">
          <input 
            type="password" 
            id="createUserPassword" 
            class="input" 
            placeholder="Password (min 8 characters)" 
            maxlength="200" 
            autocomplete="new-password" 
            required 
          />
          <button type="button" class="password-toggle" id="createUserPasswordToggle" aria-label="Show password" title="Show password">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z"/></svg>
          </button>
        </div>
      </label>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="createUserCancel">Cancel</button>
        <button type="submit" class="btn" id="createUserSubmit">Create</button>
      </div>
    </form>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const closeBtn = document.getElementById("createUserDialogClose");
    const cancelBtn = document.getElementById("createUserCancel");
    const form = document.getElementById("createUserForm");
    const emailInput = document.getElementById("createUserEmail");
    const nameInput = document.getElementById("createUserName");
    const passwordInput = document.getElementById("createUserPassword");
    const passwordToggle = document.getElementById("createUserPasswordToggle");
    const passwordIconPath = passwordToggle?.querySelector("path");
    const PATH_SHOW = "M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z";
    const PATH_HIDE = "M2 5.27L3.28 4 20 20.72 18.73 22 15.65 18.92C14.5 19.3 13.28 19.5 12 19.5 7 19.5 2.73 16.39 1 12c.69-1.76 1.79-3.31 3.19-4.54L2 5.27zM12 9a3 3 0 0 1 3 3c0 .35-.06.69-.17 1l-3.83-3.83c.31-.06.65-.17 1-.17zM12 4.5c5 0 9.27 3.11 11 7.5-.82 2.08-2.21 3.88-4 5.19L17.58 15.76C18.94 14.82 20.06 13.54 20.82 12 19.17 8.64 15.76 6.5 12 6.5c-1.09 0-2.16.18-3.16.5L7.3 5.47C8.74 4.85 10.33 4.5 12 4.5zM3.18 12C4.83 15.36 8.24 17.5 12 17.5c.69 0 1.37-.07 2-.21L11.72 15c-1.43-.15-2.57-1.29-2.72-2.72L5.6 8.87C4.61 9.72 3.78 10.78 3.18 12z";
    if (passwordToggle && passwordInput && passwordIconPath) {
        passwordToggle.addEventListener("click", () => {
            const isPassword = passwordInput.type === "password";
            passwordInput.type = isPassword ? "text" : "password";
            passwordIconPath.setAttribute("d", isPassword ? PATH_HIDE : PATH_SHOW);
            passwordToggle.setAttribute("aria-label", isPassword ? "Hide password" : "Show password");
            passwordToggle.setAttribute("title", isPassword ? "Hide password" : "Show password");
        });
    }
    const close = () => {
        document.body.removeChild(dialog);
    };
    if (closeBtn) {
        closeBtn.addEventListener("click", close);
    }
    if (cancelBtn) {
        cancelBtn.addEventListener("click", close);
    }
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog) {
            close();
        }
    });
    if (form) {
        form.addEventListener("submit", async (e) => {
            e.preventDefault();
            const email = emailInput.value.trim();
            const name = nameInput.value.trim();
            const password = passwordInput.value;
            try {
                await apiFetch("/api/admin/users", {
                    method: "POST",
                    body: JSON.stringify({ email, name, password }),
                });
                showToast("User created successfully");
                close();
                // Refresh the settings modal if Users tab is active
                if (getSettingsActiveTab() === "users") {
                    await renderSettingsModal();
                }
            }
            catch (err) {
                showToast(err.message || "Failed to create user");
            }
        });
    }
}
async function showEnable2FADialog() {
    try {
        const setup = await apiFetch("/api/auth/2fa/setup", { method: "POST" });
        if (!setup?.setupToken || !setup?.otpauthUri) {
            showToast("2FA setup failed");
            return;
        }
        const qrDataUrl = setup.qrCodeDataUrl ?? "";
        const dialog = document.createElement("dialog");
        dialog.className = "dialog";
        dialog.innerHTML = `
      <form method="dialog" class="dialog__form" id="enable2FAForm">
        <div class="dialog__header">
          <div class="dialog__title">Enable two-factor authentication</div>
          <button class="btn btn--ghost" type="button" id="enable2FAClose" aria-label="Close">✕</button>
        </div>
        <div class="muted" style="margin-bottom: 12px;">Scan the QR code with your authenticator app, or enter the key manually.</div>
        ${qrDataUrl ? `<div style="margin-bottom: 12px;"><img src="${escapeHTML(qrDataUrl)}" alt="QR code" width="192" height="192" style="display: block; margin: 0 auto;" /></div>` : ""}
        <div class="muted" style="margin-bottom: 8px; font-family: monospace; word-break: break-all;">${escapeHTML(setup.manualEntryKey)}</div>
        <label class="field">
          <div class="field__label">Enter the 6-digit code from your app</div>
          <input type="text" id="enable2FACode" class="input" placeholder="123456" maxlength="10" autocomplete="one-time-code" required />
          <div id="enable2FAError" class="field-error" style="display: none;" role="alert"></div>
        </label>
        <div class="dialog__footer">
          <div class="spacer"></div>
          <button type="button" class="btn btn--ghost" id="enable2FACancel">Cancel</button>
          <button type="submit" class="btn" id="enable2FASubmit">Enable</button>
        </div>
      </form>
    `;
        document.body.appendChild(dialog);
        dialog.showModal();
        const close = () => {
            document.body.removeChild(dialog);
        };
        document.getElementById("enable2FAClose")?.addEventListener("click", close);
        document.getElementById("enable2FACancel")?.addEventListener("click", close);
        dialog.addEventListener("click", (e) => {
            if (e.target === dialog)
                close();
        });
        const form = document.getElementById("enable2FAForm");
        const codeInput = document.getElementById("enable2FACode");
        const errorEl = document.getElementById("enable2FAError");
        const showError = (msg) => {
            if (errorEl) {
                errorEl.textContent = msg;
                errorEl.style.display = "";
            }
            showToast(msg);
        };
        const clearError = () => {
            if (errorEl) {
                errorEl.textContent = "";
                errorEl.style.display = "none";
            }
        };
        if (form && codeInput) {
            codeInput.addEventListener("input", clearError);
            codeInput.addEventListener("focus", clearError);
            form.addEventListener("submit", async (e) => {
                e.preventDefault();
                clearError();
                const code = codeInput.value.trim();
                try {
                    const res = await apiFetch("/api/auth/2fa/enable", {
                        method: "POST",
                        body: JSON.stringify({ setupToken: setup.setupToken, code }),
                    });
                    close();
                    const u = getUser();
                    if (u)
                        setUser({ ...u, twoFactorEnabled: true });
                    if (res?.recoveryCodes?.length) {
                        showRecoveryCodesDialog(res.recoveryCodes);
                    }
                    showToast("2FA enabled");
                    await renderSettingsModal();
                }
                catch (err) {
                    const msg = err?.message || "Failed to enable 2FA";
                    showError(msg);
                }
            });
        }
    }
    catch (err) {
        showToast(err.message || "2FA setup failed");
    }
}
function showRecoveryCodesDialog(codes) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title">Recovery codes</div>
        <button class="btn btn--ghost" type="button" id="recoveryCodesClose" aria-label="Close">✕</button>
      </div>
      <div class="muted" style="margin-bottom: 12px;">Save these codes in a secure place. Each can be used once to sign in if you lose access to your authenticator app.</div>
      <div style="font-family: monospace; word-break: break-all; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px;">
        ${codes.map((c) => escapeHTML(c)).join(" &nbsp; ")}
      </div>
      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn" id="recoveryCodesDone">Done</button>
      </div>
    </div>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const close = () => {
        document.body.removeChild(dialog);
    };
    document.getElementById("recoveryCodesClose")?.addEventListener("click", close);
    document.getElementById("recoveryCodesDone")?.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
}
function showDisable2FADialog() {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form" id="disable2FAForm">
      <div class="dialog__header">
        <div class="dialog__title">Disable two-factor authentication</div>
        <button class="btn btn--ghost" type="button" id="disable2FAClose" aria-label="Close">✕</button>
      </div>
      <div class="muted" style="margin-bottom: 12px;">Enter your password to disable 2FA.</div>
      <label class="field">
        <div class="field__label">Password</div>
        <input type="password" id="disable2FAPassword" class="input" placeholder="Password" required />
      </label>
      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="disable2FACancel">Cancel</button>
        <button type="submit" class="btn btn--danger" id="disable2FASubmit">Disable 2FA</button>
      </div>
    </form>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const close = () => {
        document.body.removeChild(dialog);
    };
    document.getElementById("disable2FAClose")?.addEventListener("click", close);
    document.getElementById("disable2FACancel")?.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
    const form = document.getElementById("disable2FAForm");
    const passwordInput = document.getElementById("disable2FAPassword");
    if (form && passwordInput) {
        form.addEventListener("submit", async (e) => {
            e.preventDefault();
            try {
                await apiFetch("/api/auth/2fa/disable", {
                    method: "POST",
                    body: JSON.stringify({ password: passwordInput.value }),
                });
                close();
                const u = getUser();
                if (u)
                    setUser({ ...u, twoFactorEnabled: false });
                showToast("2FA disabled");
                await renderSettingsModal();
            }
            catch (err) {
                showToast(err.message || "Failed to disable 2FA");
            }
        });
    }
}
async function showRegenerateRecoveryCodesDialog() {
    try {
        const res = await apiFetch("/api/auth/2fa/recovery/regenerate", {
            method: "POST",
        });
        if (res?.recoveryCodes?.length) {
            showRecoveryCodesDialog(res.recoveryCodes);
            showToast("Recovery codes regenerated");
            await renderSettingsModal();
        }
    }
    catch (err) {
        showToast(err.message || "Failed to regenerate recovery codes");
    }
}

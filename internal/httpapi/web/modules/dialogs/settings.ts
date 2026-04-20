import { settingsDialog, closeSettingsBtn } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { fetchProjectMembers } from '../members-cache.js';
import { escapeHTML, showToast, getAppVersion, showConfirmDialog, isAnonymousBoard, renderUserAvatar, processImageFile, renderAvatarContent, sanitizeHexColor } from '../utils.js';
import { getStoredTheme, handleThemeChange, THEME_SYSTEM, THEME_DARK, THEME_LIGHT } from '../theme.js';
import { getStoredWallpaperState, setWallpaperOff, setWallpaperColor, uploadWallpaperImage } from '../wallpaper.js';
import { processWallpaperFileForUpload } from '../utils.js';
import { 
  getSlug, 
  getTag, 
  getSearch,
  getSprintIdFromUrl,
  getBoard,
  getProjectId, 
  getProjects, 
  getSettingsProjectId, 
  getSettingsActiveTab, 
  getTagColors, 
  getUser, 
  getAuthStatusAvailable,
  getBackupImportBtn,
  getBackupData,
  getBackupPreview,
  getBoardMembers
} from '../state/selectors.js';
import { 
  setSettingsProjectId, 
  setSettingsActiveTab, 
  setTagColors,
  setBoard,
  setBackupImportBtn,
  setBackupData,
  setBackupPreview,
  setUser,
  setBoardMembers,
} from '../state/mutations.js';
import { BackupPreviewResponse, User } from '../types.js';
import { renderRealBurndownChart, destroyBurndownChart, mountBurndownChart } from '../charts/burndown.js';
import { emit } from '../events.js';
import { normalizeSprints } from '../sprints.js';
import {
  KEY_ACTION_LIST,
  chordFromKeyboardEvent,
  formatChordForDisplay,
  getResolvedChordForAction,
  isTypingInTextField,
  reloadKeybindingsFromStorage,
  saveKeybindingOverride,
  setKeybindingsCaptureListening,
  type KeyActionId,
} from '../core/keybindings.js';
import {
  requestDesktopNotificationPermission,
  getDesktopNotificationStatusDescription,
} from '../core/assignmentNotify.js';
import { isPushSubscribed, subscribeToPush, unsubscribeFromPush } from '../core/push.js';
import { getVoiceFlowEnabledPreference, setVoiceFlowEnabledPreference } from '../core/voiceflow-preferences.js';
import {
  bindWorkflowTabInteractions,
  clearWorkflowDraftState,
  invalidateWorkflowLaneCountsCache,
  isWorkflowDraftDirty,
  loadWorkflowTabContent,
  resetWorkflowDraftToBaseline,
} from './settings-workflow.js';
import {
  bindTagTabInteractions,
  invalidateTagsCache as invalidateTagSettingsCache,
  loadTagSettingsContent,
} from './settings-tags.js';
import { bindSprintsTabInteractions, renderSprintsTabContent } from './settings-sprints.js';

export { invalidateTagsCache } from './settings-tags.js';

// Import view functions - renderProjects is not needed here
declare function renderProjects(): Promise<void>;

/** Active keybinding capture listener (settings customization); removed when starting a new capture or on abort. */
let keybindingCaptureKeydown: ((e: KeyboardEvent) => void) | null = null;

function resetKeybindingCaptureUI(): void {
  if (keybindingCaptureKeydown) {
    window.removeEventListener("keydown", keybindingCaptureKeydown, true);
    keybindingCaptureKeydown = null;
  }
  setKeybindingsCaptureListening(false);
  document.querySelectorAll("[data-keybinding-capture]").forEach((b) => {
    const id = (b as HTMLElement).getAttribute("data-keybinding-action") as KeyActionId | null;
    if (id) (b as HTMLElement).textContent = formatChordForDisplay(getResolvedChordForAction(id));
    b.classList.remove("keybinding-capture--listening", "keybinding-capture--error");
  });
}

/** Avoid stacking `close` listeners if this module is re-evaluated (e.g. hot reload). */
let settingsKeybindingCloseListenerInstalled = false;

function installSettingsDialogCloseForKeybindingCaptureOnce(): void {
  if (settingsKeybindingCloseListenerInstalled) return;
  settingsKeybindingCloseListenerInstalled = true;
  settingsDialog.addEventListener("close", () => {
    resetKeybindingCaptureUI();
  });
}

installSettingsDialogCloseForKeybindingCaptureOnce();

// AbortController for per-render listener cleanup
let settingsAbortController: AbortController | null = null;
let settingsProfileRefetchController: AbortController | null = null;
let settingsProfileRefetchVersion = 0;

// Only one sprint row in edit mode at a time
let burndownSprintIndex = 0;

// Cache for settings modal API calls
let cachedRealBurndownData: any[] | null = null;
let cachedRealBurndownURL: string | null = null;
let cachedSprintsForCharts: { id: number; name: string; plannedStartAt: number; plannedEndAt: number; state?: string }[] | null = null;

/** Update all user-avatar elements outside the settings dialog (e.g. topbar) after avatar change. */
function refreshAvatarsOutsideSettings(): void {
  const user = getUser();
  const content = renderAvatarContent(user);
  document.querySelectorAll('.user-avatar').forEach((el) => {
    if (el.closest('#settingsDialog')) return;
    el.innerHTML = content;
  });
}

function invalidateSettingsProfileRefetch(): void {
  settingsProfileRefetchVersion++;
  settingsProfileRefetchController?.abort();
  settingsProfileRefetchController = null;
}

// Helper function to invalidate chart cache (call when todos are modified)
function invalidateChartCache(): void {
  cachedRealBurndownData = null;
  cachedRealBurndownURL = null;
}

/**
 * Single source of truth for all settings tab switches (click + keyboard).
 * Handles workflow dirty checks, cache invalidation, re-render, and dialog height fix.
 */
async function switchSettingsTab(tabName: string): Promise<void> {
  if (tabName === getSettingsActiveTab()) return;
  if (getSettingsActiveTab() === "workflow" && isWorkflowDraftDirty()) {
    const discard = await showConfirmDialog(
      "You have unsaved changes. Discard them?",
      "Unsaved changes",
      "Discard"
    );
    if (!discard) return;
    resetWorkflowDraftToBaseline();
  }
  if (tabName === "workflow") {
    invalidateWorkflowLaneCountsCache();
    clearWorkflowDraftState();
  }
  setSettingsActiveTab(tabName);
  await renderSettingsModal();
  const dialog = document.getElementById("settingsDialog");
  if (dialog && (dialog as HTMLDialogElement).open) {
    const currentHeight = (dialog as HTMLElement).style.height;
    (dialog as HTMLElement).style.height = "auto";
    void (dialog as HTMLElement).offsetHeight;
    (dialog as HTMLElement).style.height = currentHeight || "";
  }
}

// Invalidate sprints cache when sprints are created/activated/closed (so Charts tab shows fresh list)
/** Auto-select sprint for Charts: active > last closed > first planned. */
function computeDefaultBurndownSprintIndex(
  sprints: { id: number; state?: string; plannedEndAt: number }[]
): number {
  if (sprints.length === 0) return 0;
  const activeIdx = sprints.findIndex((s) => s.state === 'ACTIVE');
  if (activeIdx >= 0) return activeIdx;
  const closed = sprints
    .map((s, i) => ({ s, i }))
    .filter((x) => x.s.state === 'CLOSED');
  if (closed.length > 0) {
    const lastClosed = closed.reduce((a, b) =>
      a.s.plannedEndAt >= b.s.plannedEndAt ? a : b
    );
    return lastClosed.i;
  }
  const plannedIdx = sprints.findIndex((s) => s.state === 'PLANNED');
  if (plannedIdx >= 0) return plannedIdx;
  return 0;
}

function invalidateSprintsForChartsCache(): void {
  cachedSprintsForCharts = null;
  cachedRealBurndownData = null;
  cachedRealBurndownURL = null;
}

// Helper function for tag color
function getTagColor(tagName: string): string | null {
  return getTagColors()[tagName] || null;
}

// Render backup tab HTML
function renderBackupTabHTML(): string {
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

function renderVoiceFlowCustomizationHTML(): string {
  const enabled = getVoiceFlowEnabledPreference();
  return `
    <div class="settings-section">
      <div class="settings-section__title">VoiceFlow</div>
      <label class="row" style="align-items:center;gap:8px;margin-top:10px;cursor:pointer;">
        <input type="checkbox" id="voiceFlowEnabledToggle" ${enabled ? "checked" : ""} />
        <span>Use voice commands to move, create and delete todos.</span>
      </label>
    </div>
  `;
}

// Backup handlers
async function handleBackupExport(): Promise<void> {
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
  } catch (err: any) {
    showToast(err.message || "Export failed");
  }
}

async function handleBackupFileSelect(e: Event): Promise<void> {
  const target = e.target as HTMLInputElement;
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
    const importMode = (document.querySelector('input[name="importMode"]:checked') as HTMLInputElement)?.value || "merge";

    // Call preview endpoint
    const preview = await apiFetch("/api/backup/preview", {
      method: "POST",
      body: JSON.stringify({
        data: data,
        importMode: importMode
      })
    }) as BackupPreviewResponse;

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
        warningsEl.innerHTML = `<strong>Warnings:</strong><br>${preview.warnings.map((w: string) => escapeHTML(w)).join("<br>")}`;
        warningsEl.style.display = "block";
      }
    }

    setBackupPreview(preview);
    updateBackupUI();
  } catch (err: any) {
    showToast(err.message || "Failed to read backup file");
    setBackupData(null);
    setBackupPreview(null);
    updateBackupUI();
  }
}

function updateBackupUI(): void {
  // Use stored reference if available, otherwise find by ID
  const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn")) as HTMLButtonElement | null;
  const confirmationDiv = document.getElementById("backupConfirmation");
  const confirmationInput = document.getElementById("backupConfirmationInput") as HTMLInputElement | null;
  const importMode = (document.querySelector('input[name="importMode"]:checked') as HTMLInputElement)?.value || "merge";

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
      } else {
        importBtn.setAttribute("disabled", "disabled");
      }
    }
  } else {
    if (confirmationDiv) {
      confirmationDiv.style.display = "none";
    }
    if (importBtn) {
      importBtn.disabled = false;
      importBtn.removeAttribute("disabled");
    }
  }
}

async function handleBackupImport(): Promise<void> {
  console.log("handleBackupImport: Function called");
  
  if (!getBackupData()) {
    console.log("handleBackupImport: No backup data");
    showToast("No backup file selected");
    return;
  }

  // Use stored reference if available, otherwise find by ID
  const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn")) as HTMLButtonElement | null;
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

  const importMode = (document.querySelector('input[name="importMode"]:checked') as HTMLInputElement)?.value || "merge";
  const confirmationInput = document.getElementById("backupConfirmationInput") as HTMLInputElement | null;
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
    
    const body: any = {
      data: getBackupData(),
      importMode: importMode
    };
    if (importMode === "replace") {
      body.confirmation = confirmationInput!.value.trim();
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
    if ((result as any).imported !== undefined) message += `${(result as any).imported} projects imported, `;
    if ((result as any).updated !== undefined) message += `${(result as any).updated} updated, `;
    if ((result as any).created !== undefined) message += `${(result as any).created} created`;
    showToast(message);

    // Show warnings if any
    if ((result as any).warnings && (result as any).warnings.length > 0) {
      const warningsEl = document.getElementById("backupWarnings");
      if (warningsEl) {
        warningsEl.innerHTML = `<strong>Warnings:</strong><br>${(result as any).warnings.map((w: string) => escapeHTML(w)).join("<br>")}`;
        warningsEl.style.display = "block";
      }
    }

    // Reload the page to show updated data
    setTimeout(() => {
      window.location.reload();
    }, 1500);
  } catch (err: any) {
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
    const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn")) as HTMLButtonElement | null;
    if (importBtn) {
      importBtn.disabled = false;
      importBtn.removeAttribute("disabled");
      importBtn.textContent = "Import";
      console.log("handleBackupImport: Button restored");
    } else {
      console.error("handleBackupImport: Could not find button to restore");
    }
  }
}

async function setupBackupTab(signal?: AbortSignal): Promise<void> {
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
        (confirmationInput as HTMLInputElement).value = "";
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

export async function renderSettingsModal(options?: { skipProfileRefetch?: boolean }): Promise<void> {
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
  let tagsURL: string | null = null;
  let realBurndownURL: string | null = null;
  let hasProjectAccess = false;
  
  if (getSlug()) {
    // Board view: show tags from this specific board
    tagsURL = `/api/board/${getSlug()}/tags`;
    realBurndownURL = `/api/board/${getSlug()}/burndown`;
    setSettingsProjectId(null);
    hasProjectAccess = true;
  } else {
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
    } catch {
      boardMembers = [];
    }
  }
  const myMember = currentUser ? boardMembers.find((m: any) => m.userId === currentUser.id) : null;
  const showSprintsTab = !!slug && hasProjectAccess && myMember?.role === "maintainer";
  const showWorkflowTab = !!slug && hasProjectAccess && myMember?.role === "maintainer";

  // Charts tab only applies in durable project board view (not Dashboard/Projects/Temporary Boards, not anonymous mode, not temporary boards)
  const board = getBoard();
  const isTemporaryBoard = !!(board?.project?.expiresAt);
  const showChartsTab =
    !!slug &&
    hasProjectAccess &&
    getAuthStatusAvailable() &&
    !isTemporaryBoard;

  // Initialize active tab (default to Profile or Customization if no projects)
  if (!getSettingsActiveTab()) {
    if (showProfileTab) {
      setSettingsActiveTab("profile");
    } else if (hasProjectAccess) {
      setSettingsActiveTab("tag-colors");
    } else {
      setSettingsActiveTab("customization");
    }
  } else if (!showProfileTab && getSettingsActiveTab() === "profile") {
    setSettingsActiveTab(hasProjectAccess ? "tag-colors" : "customization");
  } else if (!showChartsTab && getSettingsActiveTab() === "charts") {
    setSettingsActiveTab(hasProjectAccess ? "tag-colors" : "customization");
  } else if (!showWorkflowTab && getSettingsActiveTab() === "workflow") {
    setSettingsActiveTab(hasProjectAccess ? "tag-colors" : "customization");
  } else if (getSettingsActiveTab() === "voiceflow") {
    setSettingsActiveTab("customization");
  }

  // Fetch full user profile (including avatar) when Profile tab is shown (skip when re-rendering after avatar change)
  if (showProfileTab && getUser() && !options?.skipProfileRefetch) {
    const profileRefetchVersion = ++settingsProfileRefetchVersion;
    settingsProfileRefetchController?.abort();
    settingsProfileRefetchController = new AbortController();
    try {
      const me = await apiFetch<User>("/api/me", { signal: settingsProfileRefetchController.signal });
      if (me && profileRefetchVersion === settingsProfileRefetchVersion) {
        setUser(me);
      }
    } catch {
      // Ignore - user may have logged out, or this refetch was invalidated by a newer render/avatar mutation.
    } finally {
      if (profileRefetchVersion === settingsProfileRefetchVersion) {
        settingsProfileRefetchController = null;
      }
    }
  }

  // Fetch tags and chart data only if we have project access
  let tagsHTML = "";
  let realBurndownData: any[] = [];
  
  const realBurndownURLChanged = cachedRealBurndownURL !== realBurndownURL;
  
  if (hasProjectAccess) {
    try {
      tagsHTML = await loadTagSettingsContent(tagsURL!);

      // Lazy-load chart data and sprints only when Charts tab is active
      const activeTab = getSettingsActiveTab();
      if (activeTab === "charts") {
        // Fetch sprints for burndown navigation
        const slug = getSlug();
        if (slug && (cachedSprintsForCharts === null || realBurndownURLChanged)) {
          try {
            const sprintsRes = await apiFetch<{ sprints?: { id: number; name: string; plannedStartAt: number; plannedEndAt: number; state?: string }[] } | null>(`/api/board/${slug}/sprints`);
            const rawSprints = normalizeSprints(sprintsRes);
            cachedSprintsForCharts = [...rawSprints].sort((a, b) => a.plannedStartAt - b.plannedStartAt);
            // Auto-select sprint: active > last closed > first planned
            burndownSprintIndex = computeDefaultBurndownSprintIndex(cachedSprintsForCharts);
          } catch {
            cachedSprintsForCharts = [];
          }
        }
        // When a sprint is selected in board view, use sprint-scoped burndown endpoint
        const sprints = cachedSprintsForCharts ?? [];
        const burndownSprintIndexClamped = sprints.length > 0 ? Math.min(burndownSprintIndex, sprints.length - 1) : 0;
        const currentSprintForFetch = sprints.length > 0 ? sprints[burndownSprintIndexClamped] : null;
        const effectiveBurndownURL =
          slug && currentSprintForFetch
            ? `/api/board/${slug}/sprints/${currentSprintForFetch.id}/burndown`
            : realBurndownURL;
        const effectiveBurndownURLChanged = cachedRealBurndownURL !== effectiveBurndownURL;
        if (effectiveBurndownURLChanged || cachedRealBurndownData === null) {
          if (effectiveBurndownURL) {
            try {
              realBurndownData = await apiFetch(effectiveBurndownURL);
              cachedRealBurndownData = realBurndownData;
              cachedRealBurndownURL = effectiveBurndownURL;
            } catch (err) {
              console.error("Failed to fetch real burndown data:", err);
              realBurndownData = [];
              cachedRealBurndownData = [];
            }
          } else {
            realBurndownData = [];
            cachedRealBurndownData = [];
          }
        } else {
          realBurndownData = cachedRealBurndownData;
        }
      } else {
        // Not viewing charts tab - use empty data or cached if available
        realBurndownData = cachedRealBurndownData || [];
      }
    } catch (err: any) {
      console.error("Failed to fetch tags:", err);
      tagsHTML = `<div class='muted'>Error loading tags: ${escapeHTML(err.message)}</div>`;
    }
  } else {
    // No project access - clear cache
    invalidateTagSettingsCache();
    cachedRealBurndownData = null;
    cachedRealBurndownURL = null;
  }

  // Get version from meta tag (embedded at build time)
  const versionText = getAppVersion();
  
  // Update the Settings title to include version number
  const titleEl = document.querySelector("#settingsDialog .dialog__title");
  if (titleEl && versionText) {
    titleEl.innerHTML = `Settings <span style="font-size: 0.75em; color: var(--muted); opacity: 0.6; font-weight: normal;">v${escapeHTML(versionText)}</span>`;
  } else if (titleEl) {
    titleEl.textContent = "Settings";
  }

  const profileHTML = (() => {
    if (!showProfileTab) return "";
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

  const desktopNotifyGranted =
    typeof Notification !== "undefined" && Notification.permission === "granted";

  let pushVapidServerReady = false;
  if (showProfileTab) {
    try {
      const r = await fetch("/api/push/vapid-public-key", { credentials: "same-origin" });
      if (r.ok) {
        const j = (await r.json()) as { publicKey?: string };
        pushVapidServerReady = !!(j.publicKey && j.publicKey.trim() !== "");
      }
    } catch {
      pushVapidServerReady = false;
    }
  }

  const showWallpaperSettings = getAuthStatusAvailable();
  const wallpaperState = showWallpaperSettings ? getStoredWallpaperState() : { v: 1 as const, mode: "off" as const };
  const wallpaperPickerHex =
    showWallpaperSettings && wallpaperState.mode === "color" && wallpaperState.hex ? wallpaperState.hex : "#8b919a";
  const wallpaperSummaryLabel =
    wallpaperState.mode === "off"
      ? "Off"
      : wallpaperState.mode === "color"
        ? "Solid color"
        : wallpaperState.mode === "builtin"
          ? "Default image"
          : "Custom image";
  const wallpaperImageModeSelected =
    wallpaperState.mode === "image" || wallpaperState.mode === "builtin";

  const wallpaperSectionHTML = showWallpaperSettings
    ? `
      <div class="settings-section">
        <div class="settings-section__title">Wallpaper</div>
        <div class="settings-section__description muted">Optional background behind the app. A scrim keeps text readable. Boards and cards stay solid; Settings can show the wallpaper when it is active.</div>
        <p class="muted" style="margin:8px 0 0 0;font-size:13px;">
          ${wallpaperSummaryLabel}: ${wallpaperState.mode === "off" ? "default appearance" : "active"}
        </p>
        <div class="theme-selector theme-selector--inline" style="margin-top:10px;">
          <label class="theme-option theme-option--inline">
            <input type="radio" name="wallpaperMode" value="off" ${wallpaperState.mode === "off" ? "checked" : ""}>
            <span>Off</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="wallpaperMode" value="color" ${wallpaperState.mode === "color" ? "checked" : ""}>
            <span>Solid color</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="wallpaperMode" value="image" ${wallpaperImageModeSelected ? "checked" : ""} ${getUser() || wallpaperState.mode === "builtin" ? "" : "disabled"}>
            <span>Custom image</span>
          </label>
        </div>
        <div id="wallpaperColorRow" class="wallpaper-settings-color-row" style="margin-top:12px;${wallpaperState.mode === "color" ? "" : "display:none;"}">
          <label class="row" style="align-items:center;gap:10px;">
            <span class="muted">Color</span>
            <input type="color" id="wallpaperColorPicker" value="${escapeHTML(wallpaperPickerHex)}" ${wallpaperState.mode === "color" ? "" : "disabled"} />
          </label>
        </div>
        <div class="wallpaper-settings-wallpaper-actions" style="margin-top:12px;display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
          <button type="button" class="btn" id="wallpaperUploadBtn" ${getUser() ? "" : "disabled"} style="${wallpaperImageModeSelected && getUser() ? "" : "display:none;"}">${wallpaperState.mode === "image" ? "Replace image…" : "Upload image…"}</button>
          <button type="button" class="btn btn--ghost" id="wallpaperRemoveBtn" ${wallpaperState.mode === "off" ? "disabled" : ""}>Remove wallpaper</button>
        </div>
        ${!getUser() ? `<p class="muted" style="margin-top:10px;font-size:13px;">Sign in to use a custom image. Solid color and Off work without signing in.</p>` : ""}
      </div>
    `
    : "";

  const pushPwaDisabledNotice = !pushVapidServerReady
    ? showProfileTab
      ? "Web Push needs VAPID keys on the server (SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY; see docs)."
      : "Web Push is not available in anonymous mode."
    : "";

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
      ${wallpaperSectionHTML}
      ${renderVoiceFlowCustomizationHTML()}
      <div class="settings-section">
        <div class="settings-section__title">Desktop notifications</div>
        <div class="settings-section__description muted">OS-level alerts when someone assigns you a todo (works when this tab is in the background).</div>
        <p class="muted" style="margin: 8px 0;">${escapeHTML(getDesktopNotificationStatusDescription())}</p>
        <button type="button" class="btn" id="desktopNotifyEnableBtn" ${desktopNotifyGranted ? "disabled" : ""}>${desktopNotifyGranted ? "Notifications enabled" : "Enable notifications"}</button>
      </div>
      ${pushPwaDisabledNotice ? `<p class="settings-push-vapid-notice" role="status">${escapeHTML(pushPwaDisabledNotice)}</p>` : ""}
      <div class="settings-section settings-section--push-pwa${!pushVapidServerReady ? " settings-section--push-pwa-disabled" : ""}">
        <div class="settings-section__title">Background notifications (PWA)</div>
        <div class="settings-section__description muted">Alerts when someone assigns you a todo while this app is in the background or closed (best on an installed PWA). Requires VAPID keys on the server. When configured, sign-in triggers an automatic subscribe attempt (the browser may ask for permission). Use the toggle to turn Web Push off or back on for this browser only.</div>
        <label class="row" style="align-items:center;gap:8px;margin-top:10px;cursor:pointer;">
          <input type="checkbox" id="pushNotifyToggle" ${!pushVapidServerReady ? "disabled" : ""} />
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
    workflowHTML = loadWorkflowTabContent({ slug, rerender: () => renderSettingsModal() });
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
      mountBurndownChart(mount as HTMLElement, realBurndownData, currentSprint ?? null, dataIsSprintScoped);
    }
    contentEl.classList.add("settings-content--charts");
    let scrollbarTimeout: ReturnType<typeof setTimeout>;
    contentEl.addEventListener(
      "scroll",
      () => {
        contentEl.classList.add("scrollbar-visible");
        clearTimeout(scrollbarTimeout);
        scrollbarTimeout = setTimeout(() => {
          contentEl.classList.remove("scrollbar-visible");
        }, 1500);
      },
      { signal }
    );
  } else {
    contentEl.classList.remove("settings-content--charts");
    contentEl.classList.remove("scrollbar-visible");
  }

  if (getSettingsActiveTab() === "profile") {
    contentEl.classList.add("settings-content--profile");
  } else {
    contentEl.classList.remove("settings-content--profile");
  }

  // Setup tab switching (click)
  document.querySelectorAll(".settings-tab").forEach(tab => {
    tab.addEventListener("click", (e) => {
      const tabName = (e.target as HTMLElement).getAttribute("data-tab");
      if (tabName) void switchSettingsTab(tabName);
    }, { signal });
  });

  // Setup tab switching (keyboard: Tab cycles visible tabs)
  const settingsDlgForKeyboard = document.getElementById("settingsDialog");
  if (settingsDlgForKeyboard) {
    settingsDlgForKeyboard.addEventListener("keydown", (e) => {
      if (e.key !== "Tab" || e.shiftKey) return;
      if (isTypingInTextField()) return;
      e.preventDefault();
      const tabs = Array.from(settingsDlgForKeyboard.querySelectorAll(".settings-tab[data-tab]")) as HTMLElement[];
      if (tabs.length === 0) return;
      const current = getSettingsActiveTab();
      const idx = tabs.findIndex((t) => t.getAttribute("data-tab") === current);
      const next = (idx + 1) % tabs.length;
      const nextTab = tabs[next].getAttribute("data-tab");
      if (nextTab) void switchSettingsTab(nextTab);
    }, { signal });
  }

  // Setup backup tab if it's active
  if (getSettingsActiveTab() === "backup") {
    // Wait a tick for DOM to be ready
    setTimeout(() => {
      setupBackupTab(signal);
    }, 0);
  }

  const settingsDlg = settingsDialog as HTMLDialogElement | null;
  if (getSettingsActiveTab() === "workflow") {
    bindWorkflowTabInteractions({
      signal,
      settingsDialog: settingsDlg,
      closeSettingsBtn,
      rerender: () => renderSettingsModal(),
    });
  }

  // Setup logout button: use form POST so browser processes Set-Cookie from document response
  // (fetch/XHR responses don't always clear cookies reliably across browsers)
  const logoutBtn = document.getElementById("logoutBtn");
  if (logoutBtn) {
    logoutBtn.addEventListener("click", () => {
      (settingsDialog as HTMLDialogElement).close();
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
        const file = (e.target as HTMLInputElement).files?.[0];
        if (!file) return;
        try {
          invalidateSettingsProfileRefetch();
          const dataUrl = await processImageFile(file);
          const updated = await apiFetch<User>("/api/me", {
            method: "PATCH",
            body: JSON.stringify({ image: dataUrl }),
          });
          if (updated) setUser(updated);
          refreshAvatarsOutsideSettings();
          await renderSettingsModal({ skipProfileRefetch: true });
          showToast("Avatar updated");
        } catch (err: any) {
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
        const updated = await apiFetch<User>("/api/me", {
          method: "PATCH",
          body: JSON.stringify({ image: null }),
        });
        if (updated) setUser(updated);
        refreshAvatarsOutsideSettings();
        await renderSettingsModal({ skipProfileRefetch: true });
        showToast("Avatar removed");
      } catch (err: any) {
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
        const userId = (e.currentTarget as HTMLElement).getAttribute("data-user-id");
        if (!userId) return;
        
        try {
          await apiFetch(`/api/admin/users/${userId}/role`, {
            method: "PATCH",
            body: JSON.stringify({ role: "admin" }),
          });
          showToast("User promoted to admin");
          await renderSettingsModal();
        } catch (err: any) {
          showToast(err.message || "Failed to promote user");
        }
      }, { signal });
    });

    // Demote button
    document.querySelectorAll('[data-action="demote"]').forEach(btn => {
      btn.addEventListener("click", async (e) => {
        const userId = (e.currentTarget as HTMLElement).getAttribute("data-user-id");
        if (!userId) return;
        
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
        } catch (err: any) {
          showToast(err.message || "Failed to demote user");
        }
      }, { signal });
    });

    // Delete button
    document.querySelectorAll('[data-action="delete"]').forEach(btn => {
      btn.addEventListener("click", async (e) => {
        const userId = (e.currentTarget as HTMLElement).getAttribute("data-user-id");
        if (!userId) return;
        
        if (!confirm("Delete this user? This action cannot be undone.")) {
          return;
        }
        
        try {
          await apiFetch(`/api/admin/users/${userId}`, {
            method: "DELETE",
          });
          showToast("User deleted");
          await renderSettingsModal();
        } catch (err: any) {
          showToast(err.message || "Failed to delete user");
        }
      }, { signal });
    });

    // Password button
    document.querySelectorAll('[data-action="password"]').forEach(btn => {
      btn.addEventListener("click", (e) => {
        const userId = (e.currentTarget as HTMLElement).getAttribute("data-user-id");
        if (!userId) return;
        showPasswordResetDialog(userId);
      }, { signal });
    });
  }

  // Setup sprints tab (create, activate, close)
  if (getSettingsActiveTab() === "sprints") {
    bindSprintsTabInteractions({
      signal,
      rerender: () => renderSettingsModal(),
      invalidateSprintChartsCache: invalidateSprintsForChartsCache,
    });
  }

  // Setup theme selector
  document.querySelectorAll('input[name="theme"]').forEach(radio => {
    radio.addEventListener('change', (e) => {
      handleThemeChange((e.target as HTMLInputElement).value);
    }, { signal });
  });

  function syncWallpaperRadiosFromState(): void {
    const st = getStoredWallpaperState();
    const rOff = document.querySelector('input[name="wallpaperMode"][value="off"]') as HTMLInputElement | null;
    const rCol = document.querySelector('input[name="wallpaperMode"][value="color"]') as HTMLInputElement | null;
    const rImg = document.querySelector('input[name="wallpaperMode"][value="image"]') as HTMLInputElement | null;
    if (st.mode === "off" && rOff) rOff.checked = true;
    else if (st.mode === "color" && rCol) rCol.checked = true;
    else if (st.mode === "image" && rImg) rImg.checked = true;
    else if (st.mode === "builtin" && rImg) rImg.checked = true;
  }

  function openWallpaperFileDialog(): void {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "image/jpeg,image/png,image/gif";
    input.onchange = async (ev) => {
      const file = (ev.target as HTMLInputElement).files?.[0];
      if (!file) return;
      try {
        const blob = await processWallpaperFileForUpload(file);
        await uploadWallpaperImage(blob);
        showToast("Wallpaper updated");
      } catch (err: any) {
        showToast(err?.message ?? String(err) ?? "Upload failed");
      }
      await renderSettingsModal();
    };
    input.click();
  }

  document.querySelectorAll('input[name="wallpaperMode"]').forEach(radio => {
    radio.addEventListener(
      "change",
      async (e) => {
        const el = e.target as HTMLInputElement;
        if (el.value === "off") {
          await setWallpaperOff();
          await renderSettingsModal();
          return;
        }
        if (el.value === "color") {
          const picker = document.getElementById("wallpaperColorPicker") as HTMLInputElement | null;
          await setWallpaperColor(picker?.value || wallpaperPickerHex);
          await renderSettingsModal();
          return;
        }
        if (el.value === "image") {
          el.checked = false;
          syncWallpaperRadiosFromState();
          if (!getUser()) {
            showToast("Sign in to use a custom image");
            return;
          }
          openWallpaperFileDialog();
        }
      },
      { signal }
    );
  });

  const wallpaperColorPicker = document.getElementById("wallpaperColorPicker") as HTMLInputElement | null;
  if (wallpaperColorPicker) {
    wallpaperColorPicker.addEventListener(
      "input",
      async () => {
        const mode = (document.querySelector('input[name="wallpaperMode"]:checked') as HTMLInputElement | null)?.value;
        if (mode !== "color") return;
        await setWallpaperColor(wallpaperColorPicker.value);
      },
      { signal }
    );
  }

  const wallpaperUploadBtn = document.getElementById("wallpaperUploadBtn");
  if (wallpaperUploadBtn) {
    wallpaperUploadBtn.addEventListener(
      "click",
      () => {
        if (!getUser()) {
          showToast("Sign in to use a custom image");
          return;
        }
        openWallpaperFileDialog();
      },
      { signal }
    );
  }

  const wallpaperRemoveBtn = document.getElementById("wallpaperRemoveBtn");
  if (wallpaperRemoveBtn) {
    wallpaperRemoveBtn.addEventListener(
      "click",
      async () => {
        await setWallpaperOff();
        showToast("Wallpaper removed");
        await renderSettingsModal();
      },
      { signal }
    );
  }

  if (getSettingsActiveTab() === "customization") {
    const voiceFlowEnabledToggle = document.getElementById("voiceFlowEnabledToggle") as HTMLInputElement | null;
    if (voiceFlowEnabledToggle) {
      voiceFlowEnabledToggle.addEventListener(
        "change",
        () => {
          setVoiceFlowEnabledPreference(voiceFlowEnabledToggle.checked);
          emit("voiceflow:enabled-changed", voiceFlowEnabledToggle.checked);
        },
        { signal }
      );
    }

    const desktopNotifyBtn = document.getElementById("desktopNotifyEnableBtn");
    if (desktopNotifyBtn && !desktopNotifyBtn.hasAttribute("disabled")) {
      desktopNotifyBtn.addEventListener(
        "click",
        async () => {
          const r = await requestDesktopNotificationPermission();
          if (r === "granted") {
            showToast("Desktop notifications enabled");
          } else if (r === "denied") {
            showToast("Notifications blocked. You can allow them in your browser settings for this site.");
          } else {
            showToast("Notification permission not granted");
          }
          await renderSettingsModal();
        },
        { signal }
      );
    }
    const pushToggle = document.getElementById("pushNotifyToggle") as HTMLInputElement | null;
    const pushHint = document.getElementById("pushNotifyHint");
    if (pushToggle) {
      if (!pushVapidServerReady) {
        pushToggle.checked = false;
        if (pushHint) {
          pushHint.textContent = "";
        }
      } else if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
        pushToggle.disabled = true;
        if (pushHint) {
          pushHint.textContent = "Web Push is not supported in this browser.";
        }
      } else {
        isPushSubscribed()
          .then((on) => {
            pushToggle.checked = on;
          })
          .catch(() => {});
        pushToggle.addEventListener(
          "change",
          async () => {
            if (pushToggle.checked) {
              const ok = await subscribeToPush();
              if (!ok) {
                pushToggle.checked = false;
                showToast("Could not enable Web Push. Allow notifications or check server VAPID configuration.");
              } else {
                showToast("Web Push enabled");
              }
            } else {
              await unsubscribeFromPush();
              showToast("Web Push disabled");
            }
            await renderSettingsModal();
          },
          { signal }
        );
      }
    }
    resetKeybindingCaptureUI();
    document.querySelectorAll("[data-keybinding-capture]").forEach((btn) => {
      btn.addEventListener(
        "click",
        () => {
          resetKeybindingCaptureUI();
          const actionId = (btn as HTMLElement).getAttribute("data-keybinding-action") as KeyActionId | null;
          if (!actionId) return;
          (btn as HTMLElement).classList.add("keybinding-capture--listening");
          (btn as HTMLElement).textContent = "Press a key…";
          setKeybindingsCaptureListening(true);
          const onKey = (e: KeyboardEvent) => {
            if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              resetKeybindingCaptureUI();
              return;
            }
            e.preventDefault();
            e.stopPropagation();
            const chord = chordFromKeyboardEvent(e);
            if (!chord) return;
            const saved = saveKeybindingOverride(actionId, chord);
            // Teardown order: remove listener, clear ref, then flag (avoid global handler seeing capture off while listener still registered).
            window.removeEventListener("keydown", onKey, true);
            if (keybindingCaptureKeydown === onKey) {
              keybindingCaptureKeydown = null;
            }
            setKeybindingsCaptureListening(false);
            (btn as HTMLElement).classList.remove("keybinding-capture--listening");
            const resolvedLabel = formatChordForDisplay(getResolvedChordForAction(actionId));
            if (saved) {
              (btn as HTMLElement).textContent = resolvedLabel;
              (btn as HTMLElement).classList.remove("keybinding-capture--error");
            } else {
              // Previous binding unchanged in storage; show it immediately + error outline (no timed revert).
              (btn as HTMLElement).textContent = resolvedLabel;
              (btn as HTMLElement).classList.add("keybinding-capture--error");
            }
          };
          keybindingCaptureKeydown = onKey;
          window.addEventListener("keydown", onKey, true);
        },
        { signal }
      );
    });
  }

  // Setup event listeners for color pickers (only if we have project access)
  bindTagTabInteractions({
    signal,
    hasProjectAccess,
    rerender: () => renderSettingsModal(),
  });
}

async function renderUsersTabContent(): Promise<string> {
  const currentUser = getUser();
  const isOwner = currentUser?.systemRole === "owner";
  const isAdmin = currentUser?.systemRole === "admin";

  try {
    const users: any[] = await apiFetch("/api/admin/users");
    
    if (users.length === 0) {
      return `<div class="settings-section"><div class="muted">No users found.</div></div>`;
    }

    const rows = users.map((user: any) => {
      const isSelf = user.id === currentUser?.id;
      const userRole = user.systemRole || "user";
      const isUserRole = userRole === "user";
      const isAdminRole = userRole === "admin";
      const isOwnerRole = userRole === "owner";

      // Determine available actions
      let actionsHTML = "-";
      
      if (isOwner) {
        // Owner can manage all users except themselves
        if (isSelf) {
          // Self: no delete, no demote if last owner
          actionsHTML = "-";
        } else if (isOwnerRole) {
          // Other owner: no actions (can't demote/promote owners, can't delete owners)
          actionsHTML = "-";
        } else if (isAdminRole) {
          // Admin: can demote to user or delete
          actionsHTML = `
            <div class="users-table__actions">
              <button class="btn btn--ghost btn--small" data-action="demote" data-user-id="${user.id}" data-user-role="${userRole}">Demote</button>
              <button class="btn btn--danger btn--small" data-action="delete" data-user-id="${user.id}">Delete</button>
              <button class="btn btn--ghost btn--small" data-action="password" data-user-id="${user.id}">Password</button>
            </div>
          `;
        } else if (isUserRole) {
          // User: can promote to admin or delete
          actionsHTML = `
            <div class="users-table__actions">
              <button class="btn btn--ghost btn--small" data-action="promote" data-user-id="${user.id}" data-user-role="${userRole}">Promote</button>
              <button class="btn btn--danger btn--small" data-action="delete" data-user-id="${user.id}">Delete</button>
              <button class="btn btn--ghost btn--small" data-action="password" data-user-id="${user.id}">Password</button>
            </div>
          `;
        }
      } else if (isAdmin) {
        // Admin: can view but not manage
        actionsHTML = "-";
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
  } catch (err: any) {
    return `<div class="settings-section"><div class="muted">Error loading users: ${escapeHTML(err.message || "Unknown error")}</div></div>`;
  }
}

function showPasswordResetDialog(userId: string): void {
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
  (dialog as HTMLDialogElement).showModal();

  const closeBtn = document.getElementById("passwordResetDialogClose");
  const cancelBtn = document.getElementById("passwordResetCancel");
  const form = document.getElementById("passwordResetForm") as HTMLFormElement;

  const close = () => {
    document.body.removeChild(dialog);
  };

  if (closeBtn) closeBtn.addEventListener("click", close);
  if (cancelBtn) cancelBtn.addEventListener("click", close);
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) close();
  });

  if (form) {
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      try {
        const res = await apiFetch<{ reset_url: string; expires_at: string }>(
          `/api/admin/users/${userId}/password-reset`,
          { method: "POST" }
        );
        if (!res?.reset_url) {
          showToast("Failed to generate reset link");
          return;
        }
        try {
          await navigator.clipboard.writeText(res.reset_url);
          showToast("Reset link copied to clipboard (expires in 30 minutes)");
          close();
        } catch {
          showPasswordResetFallbackDialog(res.reset_url);
          close();
        }
      } catch (err: any) {
        showToast(err.message || "Failed to generate reset link");
      }
    });
  }
}

function showPasswordResetFallbackDialog(resetUrl: string): void {
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
  (dialog as HTMLDialogElement).showModal();

  const closeBtn = document.getElementById("passwordResetFallbackClose");
  const copyBtn = document.getElementById("passwordResetFallbackCopy");
  const urlInput = document.getElementById("passwordResetUrlDisplay") as HTMLInputElement;

  const close = () => {
    document.body.removeChild(dialog);
  };

  if (closeBtn) closeBtn.addEventListener("click", close);
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) close();
  });

  if (copyBtn && urlInput) {
    copyBtn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(urlInput.value);
        showToast("Link copied to clipboard");
      } catch {
        urlInput.select();
        showToast("Select the link and copy manually (Ctrl+C)");
      }
    });
  }
}

function showCreateUserDialog(): void {
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
  (dialog as HTMLDialogElement).showModal();

  const closeBtn = document.getElementById("createUserDialogClose");
  const cancelBtn = document.getElementById("createUserCancel");
  const form = document.getElementById("createUserForm") as HTMLFormElement;
  const emailInput = document.getElementById("createUserEmail") as HTMLInputElement;
  const nameInput = document.getElementById("createUserName") as HTMLInputElement;
  const passwordInput = document.getElementById("createUserPassword") as HTMLInputElement;
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
      } catch (err: any) {
        showToast(err.message || "Failed to create user");
      }
    });
  }
}

async function showEnable2FADialog(): Promise<void> {
  try {
    const setup = await apiFetch<{ setupToken: string; otpauthUri: string; manualEntryKey: string; qrCodeDataUrl?: string }>(
      "/api/auth/2fa/setup",
      { method: "POST" }
    );
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
    (dialog as HTMLDialogElement).showModal();

    const close = () => {
      document.body.removeChild(dialog);
    };

    document.getElementById("enable2FAClose")?.addEventListener("click", close);
    document.getElementById("enable2FACancel")?.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
      if (e.target === dialog) close();
    });

    const form = document.getElementById("enable2FAForm");
    const codeInput = document.getElementById("enable2FACode") as HTMLInputElement;
    const errorEl = document.getElementById("enable2FAError") as HTMLElement | null;
    const showError = (msg: string) => {
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
          const res = await apiFetch<{ recoveryCodes: string[] }>("/api/auth/2fa/enable", {
            method: "POST",
            body: JSON.stringify({ setupToken: setup.setupToken, code }),
          });
          close();
          const u = getUser();
          if (u) setUser({ ...u, twoFactorEnabled: true });
          if (res?.recoveryCodes?.length) {
            showRecoveryCodesDialog(res.recoveryCodes);
          }
          showToast("2FA enabled");
          await renderSettingsModal();
        } catch (err: any) {
          const msg = err?.message || "Failed to enable 2FA";
          showError(msg);
        }
      });
    }
  } catch (err: any) {
    showToast(err.message || "2FA setup failed");
  }
}

function showRecoveryCodesDialog(codes: string[]): void {
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
  (dialog as HTMLDialogElement).showModal();

  const close = () => {
    document.body.removeChild(dialog);
  };

  document.getElementById("recoveryCodesClose")?.addEventListener("click", close);
  document.getElementById("recoveryCodesDone")?.addEventListener("click", close);
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) close();
  });
}

function showDisable2FADialog(): void {
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
  (dialog as HTMLDialogElement).showModal();

  const close = () => {
    document.body.removeChild(dialog);
  };

  document.getElementById("disable2FAClose")?.addEventListener("click", close);
  document.getElementById("disable2FACancel")?.addEventListener("click", close);
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) close();
  });

  const form = document.getElementById("disable2FAForm");
  const passwordInput = document.getElementById("disable2FAPassword") as HTMLInputElement;
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
        if (u) setUser({ ...u, twoFactorEnabled: false });
        showToast("2FA disabled");
        await renderSettingsModal();
      } catch (err: any) {
        showToast(err.message || "Failed to disable 2FA");
      }
    });
  }
}

async function showRegenerateRecoveryCodesDialog(): Promise<void> {
  try {
    const res = await apiFetch<{ recoveryCodes: string[] }>("/api/auth/2fa/recovery/regenerate", {
      method: "POST",
    });
    if (res?.recoveryCodes?.length) {
      showRecoveryCodesDialog(res.recoveryCodes);
      showToast("Recovery codes regenerated");
      await renderSettingsModal();
    }
  } catch (err: any) {
    showToast(err.message || "Failed to regenerate recovery codes");
  }
}

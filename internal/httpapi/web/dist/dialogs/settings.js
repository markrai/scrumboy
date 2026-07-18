import { settingsDialog, closeSettingsBtn } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { fetchProjectMembers } from '../members-cache.js';
import { escapeHTML, showToast, getAppVersion, showConfirmDialog, confirmDelete, isAnonymousBoard, renderUserAvatar, processImageFile, renderAvatarContent } from '../utils.js';
import { getStoredTheme, handleThemeChange, THEME_SYSTEM, THEME_DARK, THEME_LIGHT } from '../theme.js';
import { getStoredWallpaperState, setWallpaperOff, setWallpaperColor, uploadWallpaperImage } from '../wallpaper.js';
import { processWallpaperFileForUpload } from '../utils.js';
import { getSlug, getBoard, getProjectId, getProjects, getSettingsProjectId, getSettingsActiveTab, getTagColors, getUser, getAuthStatusAvailable, getOidcEnabled, getLocalAuthEnabled, getPushConfigured, getBackupImportBtn, getBackupData, getBackupPreview, getTrelloImportBtn, getTrelloImportData, getTrelloImportPreview, getTrelloImportResult, getBoardMembers } from '../state/selectors.js';
import { setSettingsProjectId, setSettingsActiveTab, setBackupImportBtn, setBackupData, setBackupPreview, setTrelloImportBtn, setTrelloImportData, setTrelloImportPreview, setTrelloImportResult, setUser, setBoardMembers, } from '../state/mutations.js';
import { renderRealBurndownChart, destroyBurndownChart, mountBurndownChart } from '../charts/burndown.js';
import { emit } from '../events.js';
import { normalizeSprints } from '../sprints.js';
import { KEY_ACTION_LIST, chordFromKeyboardEvent, formatChordForDisplay, getResolvedChordForAction, isTypingInTextField, reloadKeybindingsFromStorage, saveKeybindingOverride, setKeybindingsCaptureListening, } from '../core/keybindings.js';
import { requestDesktopNotificationPermission, getDesktopNotificationStatusDescription, getDesktopNotificationStatusKind, } from '../core/assignmentNotify.js';
import { isPushSubscribed, subscribeToPush, unsubscribeFromPush } from '../core/push.js';
import { getVoiceFlowEnabledPreference, setVoiceFlowEnabledPreference } from '../core/voiceflow-preferences.js';
import { bindWorkflowTabInteractions, clearWorkflowDraftState, invalidateWorkflowLaneCountsCache, isWorkflowDraftDirty, loadWorkflowTabContent, resetWorkflowDraftToBaseline, } from './settings-workflow.js';
import { bindTagTabInteractions, invalidateTagsCache as invalidateTagSettingsCache, loadTagSettingsContent, } from './settings-tags.js';
import { bindSprintsTabInteractions, refreshSprintDateLabels, renderSprintsTabContent } from './settings-sprints.js';
import { apiErrorMessageOrRaw, getLocale, hydrateI18n, I18N_LOCALE_CHANGED, t } from '../i18n/index.js';
import { bindPublicLocaleSelect, renderPublicLocaleSelectHTML, syncPublicLocaleSelect } from '../i18n/locale-select.js';
export { invalidateTagsCache } from './settings-tags.js';
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
let settingsLocaleListenerBound = false;
// Only one sprint row in edit mode at a time
let burndownSprintIndex = 0;
// Cache for settings modal API calls
let cachedRealBurndownData = null;
let cachedRealBurndownURL = null;
let cachedSprintsForCharts = null;
let cachedSprintsForChartsSlug = null;
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
    cachedSprintsForChartsSlug = null;
    cachedRealBurndownData = null;
    cachedRealBurndownURL = null;
}
// Helper function for tag color
function getTagColor(tagName) {
    return getTagColors()[tagName] || null;
}
function getWallpaperSummaryMessageKey(mode) {
    switch (mode) {
        case "off":
            return "settings.customization.wallpaper.summary.off";
        case "color":
            return "settings.customization.wallpaper.summary.color";
        case "builtin":
            return "settings.customization.wallpaper.summary.builtin";
        default:
            return "settings.customization.wallpaper.summary.image";
    }
}
function getDesktopNotificationStatusMessageKey(kind) {
    switch (kind) {
        case "unsupported":
            return "settings.customization.notifications.status.unsupported";
        case "granted":
            return "settings.customization.notifications.status.granted";
        case "denied":
            return "settings.customization.notifications.status.denied";
        default:
            return "settings.customization.notifications.status.default";
    }
}
function getDesktopNotificationButtonMessageKey(kind) {
    return kind === "granted"
        ? "settings.customization.notifications.actions.enabled"
        : "settings.customization.notifications.actions.enable";
}
function getKeybindingActionLabel(meta) {
    return t(meta.labelKey, meta.labelValues);
}
function getKeybindingActionMeta(actionId) {
    return KEY_ACTION_LIST.find((meta) => meta.id === actionId);
}
function getKeybindingCapturePrompt(actionId) {
    const meta = getKeybindingActionMeta(actionId);
    return t("settings.customization.keybindings.capturePrompt", {
        action: meta ? getKeybindingActionLabel(meta) : actionId,
    });
}
function syncSettingsDialogVersionText() {
    const versionEl = document.getElementById("settingsDialogVersion");
    if (!versionEl)
        return;
    const versionText = getAppVersion();
    versionEl.textContent = versionText ? ` v${versionText}` : "";
}
function syncWallpaperLocaleState() {
    const wallpaperState = getStoredWallpaperState();
    const summaryEl = document.getElementById("wallpaperSummary");
    if (summaryEl) {
        summaryEl.setAttribute("data-i18n-text", getWallpaperSummaryMessageKey(wallpaperState.mode));
    }
    const uploadBtn = document.getElementById("wallpaperUploadBtn");
    if (uploadBtn) {
        uploadBtn.setAttribute("data-i18n-text", wallpaperState.mode === "image"
            ? "settings.customization.wallpaper.actions.replace"
            : "settings.customization.wallpaper.actions.upload");
    }
}
function syncDesktopNotificationLocaleState() {
    const statusKind = getDesktopNotificationStatusKind();
    const statusEl = document.getElementById("desktopNotifyStatus");
    if (statusEl) {
        statusEl.setAttribute("data-i18n-text", getDesktopNotificationStatusMessageKey(statusKind));
    }
    const buttonEl = document.getElementById("desktopNotifyEnableBtn");
    if (buttonEl) {
        buttonEl.setAttribute("data-i18n-text", getDesktopNotificationButtonMessageKey(statusKind));
    }
}
function syncKeybindingLabelTexts() {
    document.querySelectorAll(".keybinding-row__label").forEach((labelEl) => {
        const actionId = labelEl.getAttribute("data-keybinding-action-id");
        if (!actionId)
            return;
        const meta = getKeybindingActionMeta(actionId);
        if (!meta)
            return;
        labelEl.textContent = getKeybindingActionLabel(meta);
    });
}
function syncKeybindingCapturePrompt() {
    const captureBtn = document.querySelector(".keybinding-capture--listening[data-keybinding-action]");
    if (!captureBtn)
        return;
    const actionId = captureBtn.getAttribute("data-keybinding-action");
    if (!actionId)
        return;
    captureBtn.textContent = getKeybindingCapturePrompt(actionId);
}
function bindBurndownNav(signal) {
    const opts = signal ? { signal } : undefined;
    const prevBtn = document.getElementById("burndown-prev");
    if (prevBtn) {
        prevBtn.addEventListener("click", async () => {
            if (burndownSprintIndex > 0) {
                burndownSprintIndex--;
                await renderSettingsModal();
            }
        }, opts);
    }
    const nextBtn = document.getElementById("burndown-next");
    if (nextBtn) {
        nextBtn.addEventListener("click", async () => {
            const sprints = cachedSprintsForCharts ?? [];
            if (burndownSprintIndex < sprints.length - 1) {
                burndownSprintIndex++;
                await renderSettingsModal();
            }
        }, opts);
    }
}
function relocalizeChartsFromCache() {
    const contentEl = document.querySelector("#settingsDialog .dialog__content");
    if (!contentEl)
        return;
    const chartBlock = contentEl.querySelector(".chart-block");
    if (!chartBlock)
        return;
    const slug = getSlug();
    const sprints = cachedSprintsForCharts ?? [];
    if (sprints.length > 0 && burndownSprintIndex >= sprints.length) {
        burndownSprintIndex = Math.max(0, sprints.length - 1);
    }
    const currentSprint = sprints.length > 0 ? sprints[burndownSprintIndex] : null;
    const canPrev = sprints.length > 0 && burndownSprintIndex > 0;
    const canNext = sprints.length > 0 && burndownSprintIndex < sprints.length - 1;
    const dataIsSprintScoped = !!slug && !!currentSprint;
    const realBurndownData = cachedRealBurndownData ?? [];
    chartBlock.innerHTML = currentSprint
        ? renderRealBurndownChart(realBurndownData, currentSprint, { canPrev, canNext }, dataIsSprintScoped)
        : renderRealBurndownChart(realBurndownData, undefined, undefined, dataIsSprintScoped);
    // Re-bind nav buttons (innerHTML replacement dropped the previous listeners) and
    // re-mount the chart from cached data only - never refetch on locale change.
    bindBurndownNav(settingsAbortController?.signal);
    const mount = chartBlock.querySelector("#burndown-uplot-mount");
    if (mount) {
        destroyBurndownChart();
        mountBurndownChart(mount, realBurndownData, currentSprint ?? null, dataIsSprintScoped);
    }
}
function applySettingsLocaleToOpenDialog() {
    const headerEl = settingsDialog.querySelector(".dialog__header");
    if (headerEl) {
        hydrateI18n(headerEl);
    }
    const tabsEl = settingsDialog.querySelector(".settings-tabs");
    if (tabsEl) {
        hydrateI18n(tabsEl);
    }
    syncSettingsDialogVersionText();
    const activeTab = getSettingsActiveTab();
    if (activeTab === "customization") {
        const customizationEl = document.getElementById("settingsCustomizationContent");
        if (!customizationEl)
            return;
        syncPublicLocaleSelect(document.getElementById("settingsLocaleSelect"));
        syncWallpaperLocaleState();
        syncDesktopNotificationLocaleState();
        hydrateI18n(customizationEl);
        syncKeybindingLabelTexts();
        syncKeybindingCapturePrompt();
        syncPushLocaleState();
        return;
    }
    if (activeTab === "charts") {
        relocalizeChartsFromCache();
        return;
    }
    const tabContentEl = document.getElementById("settingsTabContent");
    if (!tabContentEl)
        return;
    if (activeTab === "workflow") {
        hydrateI18n(tabContentEl);
        syncWorkflowLocaleState(tabContentEl);
        return;
    }
    if (activeTab === "tag-colors") {
        hydrateI18n(tabContentEl);
        syncTagColorsLocaleState(tabContentEl);
        return;
    }
    if (activeTab === "sprints") {
        hydrateI18n(tabContentEl);
        refreshSprintDateLabels(tabContentEl);
        return;
    }
    if (activeTab === "profile") {
        // Localize static chrome in place; raw identity values (name, email, ID,
        // system role) carry no data-i18n attribute and stay as rendered.
        hydrateI18n(tabContentEl);
        syncProfileLocaleState();
        return;
    }
    if (activeTab === "users") {
        // Hydrate existing table/action chrome in place. Rows (names, emails, role
        // values) carry no data-i18n attribute, so raw data is preserved and no
        // /api/admin/users refetch happens.
        hydrateI18n(tabContentEl);
        return;
    }
    if (activeTab === "backup") {
        // Localize static chrome, then rebuild preview/warnings/result blocks from
        // stored state only - never call export/import/preview APIs on locale
        // change. Import mode, file selection, and the typed REPLACE confirmation
        // live in inputs that hydration does not touch.
        hydrateI18n(tabContentEl);
        const backupPreview = getBackupPreview();
        renderBackupPreview(backupPreview);
        renderBackupWarnings(backupPreview?.warnings ?? null);
        const trelloPreview = getTrelloImportPreview();
        renderTrelloPreview(trelloPreview);
        renderTrelloWarnings(trelloPreview);
        renderTrelloImportResult(getTrelloImportResult());
        return;
    }
}
function ensureSettingsLocaleListener() {
    if (settingsLocaleListenerBound)
        return;
    settingsLocaleListenerBound = true;
    const settingsGlobal = globalThis;
    if (settingsGlobal.__scrumboySettingsLocaleListener) {
        document.removeEventListener(I18N_LOCALE_CHANGED, settingsGlobal.__scrumboySettingsLocaleListener);
    }
    const listener = () => {
        if (!settingsDialog?.open) {
            return;
        }
        applySettingsLocaleToOpenDialog();
    };
    settingsGlobal.__scrumboySettingsLocaleListener = listener;
    document.addEventListener(I18N_LOCALE_CHANGED, listener);
}
/**
 * Make a Settings-owned dynamic dialog locale-safe: localizes its static
 * `data-i18n-*` chrome now and re-applies it on every locale change while the
 * dialog is open, without rebuilding the dialog or resetting typed fields.
 * Returns an idempotent cleanup that manual close handlers can call; native
 * dialog `cancel` and `close` also release the listener automatically.
 */
function bindDialogLocale(dialog, sync) {
    let removed = false;
    const handleNativeCleanup = () => {
        release();
    };
    const release = () => {
        if (removed)
            return;
        removed = true;
        document.removeEventListener(I18N_LOCALE_CHANGED, listener);
        dialog.removeEventListener("cancel", handleNativeCleanup);
        dialog.removeEventListener("close", handleNativeCleanup);
    };
    const listener = () => {
        // Self-clean if the dialog was detached without calling the cleanup
        // (defensive: avoids leaked listeners hydrating stale nodes).
        if (!dialog.isConnected) {
            release();
            return;
        }
        hydrateI18n(dialog);
        sync?.();
    };
    // Localize immediately so non-English locales render correctly on open.
    hydrateI18n(dialog);
    sync?.();
    document.addEventListener(I18N_LOCALE_CHANGED, listener);
    dialog.addEventListener("cancel", handleNativeCleanup);
    dialog.addEventListener("close", handleNativeCleanup);
    return release;
}
/**
 * Re-localize the Web Push hint without probing push capability or changing the
 * toggle/subscription state. Only the unsupported-browser hint is locale-driven;
 * all other hint states stay empty, matching the binding logic.
 */
function syncPushLocaleState() {
    const hint = document.getElementById("pushNotifyHint");
    if (!hint)
        return;
    const pushReady = getAuthStatusAvailable() && getPushConfigured();
    const unsupported = !("serviceWorker" in navigator) || !("PushManager" in window);
    if (pushReady && unsupported) {
        hint.textContent = t("settings.customization.push.unsupported");
    }
}
/**
 * Re-localize Workflow-only dynamic aria-labels that are derived from raw lane
 * keys and therefore cannot be updated by `hydrateI18n()` alone.
 */
function syncWorkflowLocaleState(root) {
    root.querySelectorAll('[data-workflow-name]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-workflow-name');
        if (!key)
            return;
        inputEl.setAttribute('aria-label', t('settings.workflow.laneLabelAria', { key }));
    });
    root.querySelectorAll('[data-workflow-color]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-workflow-color');
        if (!key)
            return;
        inputEl.setAttribute('aria-label', t('settings.workflow.laneColorAria', { key }));
    });
}
/**
 * Re-localize the Tag Colors load-error wrapper while preserving the raw
 * backend error detail captured in a data attribute.
 */
function syncTagColorsLocaleState(root) {
    root.querySelectorAll('[data-tag-colors-load-error-message]').forEach((errorEl) => {
        const message = errorEl.getAttribute('data-tag-colors-load-error-message') ?? '';
        errorEl.textContent = t('settings.tagColors.error.loadFailed', { message });
    });
}
/**
 * Re-localize stateful Profile labels that `renderUserAvatar` emits as plain
 * attributes (no `data-i18n-*`). Leaves raw identity values (name, email, ID,
 * system role) untouched and never refetches `/api/me`.
 */
function syncProfileLocaleState() {
    const avatarBtn = document.getElementById("profileAvatarBtn");
    if (avatarBtn) {
        avatarBtn.setAttribute("aria-label", t("settings.profile.changeAvatar"));
    }
}
// Render backup tab HTML
export function renderBackupTabHTML() {
    const isAnonymousMode = !getAuthStatusAvailable();
    const replaceDisabled = isAnonymousMode ? 'disabled' : '';
    const replaceHidden = isAnonymousMode ? 'style="display: none;"' : '';
    return `
    <div class="settings-backup-section">
      <div class="settings-backup-export">
        <div class="settings-section__title" data-i18n-text="settings.backup.export.title">Export Data</div>
        <div class="settings-section__description muted" data-i18n-text="settings.backup.export.description">Download all your projects, todos, and tags as a JSON file.</div>
        <button class="btn" type="button" id="backupExportBtn" data-i18n-text="settings.backup.export.action">Export Backup</button>
      </div>
      <div class="settings-backup-import">
        <div class="settings-section__title" data-i18n-text="settings.backup.import.title">Import Data</div>
        <div class="settings-section__description muted" data-i18n-text="settings.backup.import.description">Restore from a backup file or merge data from another instance.</div>
        <input type="file" accept=".json" id="backupFileInput" style="margin-bottom: 16px;">
        <div style="margin-bottom: 16px;">
          <label style="display: block; margin-bottom: 8px;">
            <input type="radio" name="importMode" value="merge" checked>
            <span data-i18n-text="settings.backup.import.mode.merge">Merge & update (recommended)</span>
          </label>
          <label style="display: block; margin-bottom: 8px;" ${replaceHidden}>
            <input type="radio" name="importMode" value="replace" ${replaceDisabled}>
            <span data-i18n-text="settings.backup.import.mode.replace">Replace all data</span>
          </label>
          <label style="display: block; margin-bottom: 8px;">
            <input type="radio" name="importMode" value="copy">
            <span data-i18n-text="settings.backup.import.mode.copy">Create a copy</span>
          </label>
        </div>
        <div id="backupPreview" class="settings-backup-preview" style="display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px;"></div>
        <div id="backupConfirmation" style="display: none; margin-bottom: 16px;">
          <input type="text" id="backupConfirmationInput" placeholder="Type REPLACE to confirm" data-i18n-placeholder="settings.backup.import.confirmPlaceholder" class="settings-backup-confirmation" style="width: 100%; padding: 8px;">
        </div>
        <div id="backupWarnings" class="settings-backup-warnings" style="display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px; color: var(--muted);"></div>
        <button class="btn" type="button" id="backupImportBtn" disabled data-i18n-text="settings.backup.import.action">Import</button>
      </div>
      <div class="settings-backup-import" style="margin-top: 24px;">
        <div class="settings-section__title" data-i18n-text="settings.backup.trello.title">Import Trello Board</div>
        <div class="settings-section__description muted" data-i18n-text="settings.backup.trello.description">Upload a native Trello single-board JSON export, preview the conversion, then import it as a new Scrumboy board.</div>
        <input type="file" accept=".json,application/json" id="trelloImportFileInput" style="margin-bottom: 12px;">
        <div style="display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 16px;">
          <button class="btn btn--ghost" type="button" id="trelloImportPreviewBtn" data-i18n-text="settings.backup.trello.previewAction">Preview Trello Import</button>
          <button class="btn" type="button" id="trelloImportBtn" disabled data-i18n-text="settings.backup.trello.importAction">Import Trello Board</button>
        </div>
        <div id="trelloImportPreview" class="settings-backup-preview" style="display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px;"></div>
        <div id="trelloImportWarnings" class="settings-backup-warnings" style="display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px; color: var(--muted);"></div>
        <div id="trelloImportResult" class="settings-backup-preview" style="display: none; padding: 12px; background: var(--panel); border-radius: 4px;"></div>
      </div>
    </div>
  `;
}
function renderVoiceFlowCustomizationHTML() {
    const enabled = getVoiceFlowEnabledPreference();
    return `
    <div class="settings-section">
      <div class="settings-section__title" data-i18n-text="settings.customization.voiceFlow.title">VoiceFlow</div>
      <label class="row" style="align-items:center;gap:8px;margin-top:10px;cursor:pointer;">
        <input type="checkbox" id="voiceFlowEnabledToggle" ${enabled ? "checked" : ""} />
        <span data-i18n-text="settings.customization.voiceFlow.toggleLabel">Use voice commands to move, create and delete todos.</span>
      </label>
    </div>
  `;
}
/**
 * Render the backup preview block from a stored preview payload. Pure with
 * respect to network: rebuilds localized chrome only, leaving raw backend
 * warning strings untouched. Used on first preview and on locale change.
 */
function renderBackupPreview(preview) {
    const previewEl = document.getElementById("backupPreview");
    if (!previewEl)
        return;
    if (!preview) {
        previewEl.innerHTML = "";
        previewEl.style.display = "none";
        return;
    }
    let previewHTML = `<strong>${escapeHTML(t("settings.backup.preview.heading"))}</strong><br>`;
    previewHTML += `${escapeHTML(t("settings.backup.preview.projects", { count: preview.projects }))}<br>`;
    previewHTML += `${escapeHTML(t("settings.backup.preview.todos", { count: preview.todos }))}<br>`;
    previewHTML += `${escapeHTML(t("settings.backup.preview.tags", { count: preview.tags }))}<br>`;
    if (preview.links !== undefined && preview.links > 0) {
        previewHTML += `${escapeHTML(t("settings.backup.preview.links", { count: preview.links }))}<br>`;
    }
    if (preview.willDelete !== undefined) {
        previewHTML += `${escapeHTML(t("settings.backup.preview.willDelete", { count: preview.willDelete }))}<br>`;
    }
    if (preview.willUpdate !== undefined) {
        previewHTML += `${escapeHTML(t("settings.backup.preview.willUpdate", { count: preview.willUpdate }))}<br>`;
    }
    if (preview.willCreate !== undefined) {
        previewHTML += `${escapeHTML(t("settings.backup.preview.willCreate", { count: preview.willCreate }))}<br>`;
    }
    previewEl.innerHTML = previewHTML;
    previewEl.style.display = "block";
}
/**
 * Render the backup warnings block from raw backend warning strings. Localizes
 * only the heading; warning content stays exactly as returned by the API.
 */
function renderBackupWarnings(warnings) {
    const warningsEl = document.getElementById("backupWarnings");
    if (!warningsEl)
        return;
    if (!warnings || warnings.length === 0) {
        warningsEl.innerHTML = "";
        warningsEl.style.display = "none";
        return;
    }
    warningsEl.innerHTML = `<strong>${escapeHTML(t("settings.backup.warnings.heading"))}</strong><br>${warnings.map((w) => escapeHTML(w)).join("<br>")}`;
    warningsEl.style.display = "block";
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
            showToast(apiErrorMessageOrRaw({ data: err }, { fallbackKey: "settings.backup.toast.exportFailed" }));
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
        showToast(t("settings.backup.toast.exported"));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.backup.toast.exportFailed" }));
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
            showToast(t("settings.backup.toast.invalidFormat"));
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
        setBackupPreview(preview);
        renderBackupPreview(preview);
        renderBackupWarnings(preview.warnings);
        updateBackupUI();
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.backup.toast.readFailed" }));
        setBackupData(null);
        setBackupPreview(null);
        renderBackupPreview(null);
        renderBackupWarnings(null);
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
        showToast(t("settings.backup.toast.noFile"));
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
        showToast(t("settings.backup.toast.completeConfirmation"));
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
            showToast(t("settings.backup.toast.typeReplace"));
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
            importBtn.textContent = t("settings.backup.import.importing");
        }
        console.log("handleBackupImport: Calling API...");
        const result = await apiFetch("/api/backup/import", {
            method: "POST",
            body: JSON.stringify(body)
        });
        console.log("handleBackupImport: API call completed", result);
        // Show results
        const summaryParts = [];
        if (result.imported !== undefined)
            summaryParts.push(t("settings.backup.import.summary.imported", { count: result.imported }));
        if (result.updated !== undefined)
            summaryParts.push(t("settings.backup.import.summary.updated", { count: result.updated }));
        if (result.created !== undefined)
            summaryParts.push(t("settings.backup.import.summary.created", { count: result.created }));
        showToast(t("settings.backup.toast.importComplete", { summary: summaryParts.join(", ") }));
        // Show warnings if any
        if (result.warnings && result.warnings.length > 0) {
            renderBackupWarnings(result.warnings);
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
        const errorMsg = apiErrorMessageOrRaw(err, { fallbackKey: "settings.backup.toast.importFailed" });
        console.error("handleBackupImport: Showing toast with message:", errorMsg);
        showToast(errorMsg);
        // Re-enable button on error - use stored reference if available
        const importBtn = (getBackupImportBtn() || document.getElementById("backupImportBtn"));
        if (importBtn) {
            importBtn.disabled = false;
            importBtn.removeAttribute("disabled");
            importBtn.textContent = t("settings.backup.import.action");
            console.log("handleBackupImport: Button restored");
        }
        else {
            console.error("handleBackupImport: Could not find button to restore");
        }
    }
}
export function updateTrelloImportUI() {
    const previewBtn = document.getElementById("trelloImportPreviewBtn");
    const importBtn = (getTrelloImportBtn() || document.getElementById("trelloImportBtn"));
    const hasData = !!getTrelloImportData();
    const preview = getTrelloImportPreview();
    const canImport = !!(hasData && preview && (!preview.hardErrors || preview.hardErrors.length === 0));
    if (previewBtn) {
        previewBtn.disabled = !hasData;
    }
    if (importBtn) {
        importBtn.disabled = !canImport;
        if (canImport) {
            importBtn.removeAttribute("disabled");
        }
        else {
            importBtn.setAttribute("disabled", "disabled");
        }
    }
}
export function renderTrelloPreview(preview) {
    const previewEl = document.getElementById("trelloImportPreview");
    if (!previewEl) {
        return;
    }
    if (!preview) {
        previewEl.innerHTML = "";
        previewEl.style.display = "none";
        return;
    }
    const doneColumnLabel = preview.detectedDoneColumn || t("settings.backup.trello.preview.doneNotDetected");
    previewEl.innerHTML = `
    <strong>${escapeHTML(preview.boardName || t("settings.backup.trello.preview.unnamedBoard"))}</strong><br>
    ${escapeHTML(t("settings.backup.trello.preview.openLists", { count: preview.openLists }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.closedLists", { count: preview.closedLists }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.cards", { count: preview.cards }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.archivedCards", { count: preview.archivedCards }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.labels", { count: preview.labels }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.membersReferenced", { count: preview.membersReferenced }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.checklists", { count: preview.checklists }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.checklistItems", { count: preview.checklistItems }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.commentActions", { count: preview.commentCardActions }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.attachments", { count: preview.attachments }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.customFieldItems", { count: preview.customFieldItems }))}<br>
    ${escapeHTML(t("settings.backup.trello.preview.doneColumn", { column: doneColumnLabel }))}${preview.detectedDoneReason ? ` (${escapeHTML(preview.detectedDoneReason)})` : ""}
  `;
    previewEl.style.display = "block";
}
export function renderTrelloWarnings(preview) {
    const warningsEl = document.getElementById("trelloImportWarnings");
    if (!warningsEl) {
        return;
    }
    const hardErrors = preview?.hardErrors ?? [];
    const warnings = preview?.warnings ?? [];
    if (hardErrors.length === 0 && warnings.length === 0) {
        warningsEl.innerHTML = "";
        warningsEl.style.display = "none";
        return;
    }
    let html = "";
    if (hardErrors.length > 0) {
        html += `<strong>${escapeHTML(t("settings.backup.trello.warnings.hardErrors"))}</strong><br>${hardErrors.map((item) => escapeHTML(item)).join("<br>")}`;
    }
    if (warnings.length > 0) {
        if (html)
            html += `<br><br>`;
        html += `<strong>${escapeHTML(t("settings.backup.trello.warnings.warnings"))}</strong><br>${warnings.map((item) => escapeHTML(item)).join("<br>")}`;
    }
    warningsEl.innerHTML = html;
    warningsEl.style.display = "block";
}
export function renderTrelloImportResult(result) {
    const resultEl = document.getElementById("trelloImportResult");
    if (!resultEl) {
        return;
    }
    if (!result) {
        resultEl.innerHTML = "";
        resultEl.style.display = "none";
        return;
    }
    resultEl.innerHTML = `
    <strong>${escapeHTML(t("settings.backup.trello.result.complete"))}</strong><br>
    ${escapeHTML(t("settings.backup.trello.result.createdBoard"))} <a href="/${encodeURIComponent(result.project.slug)}">${escapeHTML(result.project.name)}</a><br>
    ${escapeHTML(t("settings.backup.trello.result.todos", { count: result.summary.todos }))}<br>
    ${escapeHTML(t("settings.backup.trello.result.labels", { count: result.summary.labels }))}
  `;
    resultEl.style.display = "block";
}
async function handleTrelloFileSelect(e) {
    const target = e.target;
    const file = target.files?.[0];
    if (!file) {
        setTrelloImportData(null);
        setTrelloImportPreview(null);
        setTrelloImportResult(null);
        renderTrelloPreview(null);
        renderTrelloWarnings(null);
        renderTrelloImportResult(null);
        updateTrelloImportUI();
        return;
    }
    try {
        const text = await file.text();
        setTrelloImportData(text);
        setTrelloImportPreview(null);
        setTrelloImportResult(null);
        renderTrelloPreview(null);
        renderTrelloWarnings(null);
        renderTrelloImportResult(null);
        updateTrelloImportUI();
    }
    catch (err) {
        showToast(err.message || t("settings.backup.trello.toast.readFailed"));
        setTrelloImportData(null);
        setTrelloImportPreview(null);
        setTrelloImportResult(null);
        renderTrelloPreview(null);
        renderTrelloWarnings(null);
        renderTrelloImportResult(null);
        updateTrelloImportUI();
    }
}
export async function handleTrelloPreview() {
    const raw = getTrelloImportData();
    if (!raw) {
        showToast(t("settings.backup.trello.toast.selectFirst"));
        return;
    }
    const previewBtn = document.getElementById("trelloImportPreviewBtn");
    try {
        if (previewBtn) {
            previewBtn.disabled = true;
            previewBtn.textContent = t("settings.backup.trello.previewing");
        }
        const preview = await apiFetch("/api/import/trello/preview", {
            method: "POST",
            body: raw,
        });
        setTrelloImportPreview(preview);
        setTrelloImportResult(null);
        renderTrelloPreview(preview);
        renderTrelloWarnings(preview);
        renderTrelloImportResult(null);
        updateTrelloImportUI();
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.backup.trello.toast.previewFailed" }));
        setTrelloImportPreview(null);
        renderTrelloPreview(null);
        renderTrelloWarnings(null);
        updateTrelloImportUI();
    }
    finally {
        if (previewBtn) {
            previewBtn.textContent = t("settings.backup.trello.previewAction");
        }
        updateTrelloImportUI();
    }
}
export async function handleTrelloImport() {
    const raw = getTrelloImportData();
    const preview = getTrelloImportPreview();
    if (!raw) {
        showToast(t("settings.backup.trello.toast.selectFirst"));
        return;
    }
    if (!preview) {
        showToast(t("settings.backup.trello.toast.previewFirst"));
        return;
    }
    if (preview.hardErrors && preview.hardErrors.length > 0) {
        showToast(t("settings.backup.trello.toast.resolveErrors"));
        return;
    }
    const importBtn = (getTrelloImportBtn() || document.getElementById("trelloImportBtn"));
    try {
        if (importBtn) {
            importBtn.disabled = true;
            importBtn.textContent = t("settings.backup.trello.importing");
        }
        const result = await apiFetch("/api/import/trello", {
            method: "POST",
            body: raw,
        });
        setTrelloImportResult(result);
        renderTrelloImportResult(result);
        renderTrelloWarnings(preview);
        showToast(t("settings.backup.trello.toast.imported", { name: result.project.name }));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.backup.trello.toast.importFailed" }));
    }
    finally {
        if (importBtn) {
            importBtn.textContent = t("settings.backup.trello.importAction");
        }
        updateTrelloImportUI();
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
    const trelloFileInput = document.getElementById("trelloImportFileInput");
    if (trelloFileInput) {
        trelloFileInput.addEventListener("change", handleTrelloFileSelect, signal ? { signal } : undefined);
    }
    const trelloPreviewBtn = document.getElementById("trelloImportPreviewBtn");
    if (trelloPreviewBtn) {
        trelloPreviewBtn.addEventListener("click", handleTrelloPreview, signal ? { signal } : undefined);
    }
    const trelloImportBtn = document.getElementById("trelloImportBtn");
    if (trelloImportBtn) {
        trelloImportBtn.addEventListener("click", handleTrelloImport, signal ? { signal } : undefined);
        setTrelloImportBtn(trelloImportBtn);
    }
    // Call updateBackupUI to set initial state after a brief delay to ensure DOM is ready
    setTimeout(() => {
        updateBackupUI();
        renderTrelloPreview(getTrelloImportPreview() ?? null);
        renderTrelloWarnings(getTrelloImportPreview() ?? null);
        renderTrelloImportResult(getTrelloImportResult() ?? null);
        updateTrelloImportUI();
    }, 0);
}
export async function renderSettingsModal(options) {
    ensureSettingsLocaleListener();
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
    else if (getSettingsActiveTab() === "voiceflow") {
        setSettingsActiveTab("customization");
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
    let tagsHTML = "";
    let realBurndownData = [];
    if (hasProjectAccess) {
        try {
            tagsHTML = await loadTagSettingsContent(tagsURL);
            // Lazy-load chart data and sprints only when Charts tab is active
            const activeTab = getSettingsActiveTab();
            if (activeTab === "charts") {
                // Fetch sprints for burndown navigation
                const slug = getSlug();
                if (!slug) {
                    cachedSprintsForCharts = null;
                    cachedSprintsForChartsSlug = null;
                }
                if (slug && (cachedSprintsForCharts === null || cachedSprintsForChartsSlug !== slug)) {
                    try {
                        const sprintsRes = await apiFetch(`/api/board/${slug}/sprints`);
                        const rawSprints = normalizeSprints(sprintsRes);
                        cachedSprintsForCharts = [...rawSprints].sort((a, b) => a.plannedStartAt - b.plannedStartAt);
                        cachedSprintsForChartsSlug = slug;
                        // Auto-select sprint: active > last closed > first planned
                        burndownSprintIndex = computeDefaultBurndownSprintIndex(cachedSprintsForCharts);
                    }
                    catch {
                        cachedSprintsForCharts = [];
                        cachedSprintsForChartsSlug = slug;
                        burndownSprintIndex = 0;
                    }
                }
                // When a sprint is selected in board view, use sprint-scoped burndown endpoint
                const sprints = slug ? (cachedSprintsForCharts ?? []) : [];
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
            const detail = err?.message ? String(err.message) : '';
            tagsHTML = `<div class='muted' data-tag-colors-load-error-message="${escapeHTML(detail)}">${escapeHTML(t('settings.tagColors.error.loadFailed', { message: detail }))}</div>`;
        }
    }
    else {
        // No project access - clear cache
        invalidateTagSettingsCache();
        cachedSprintsForCharts = null;
        cachedSprintsForChartsSlug = null;
        cachedRealBurndownData = null;
        cachedRealBurndownURL = null;
    }
    syncSettingsDialogVersionText();
    const profileHTML = (() => {
        if (!showProfileTab || getSettingsActiveTab() !== "profile")
            return "";
        const u = getUser();
        const authenticationKey = u?.hasLocalPassword && u?.oidcLinked
            ? "settings.profile.authentication.dual"
            : u?.hasLocalPassword
                ? "settings.profile.authentication.local"
                : u?.oidcLinked
                    ? "settings.profile.authentication.sso"
                    : "settings.profile.authentication.none";
        const effectiveLocal = !!u?.hasLocalPassword && getLocalAuthEnabled();
        const effectiveSSO = !!u?.oidcLinked && getOidcEnabled();
        const ownerWarning = u?.systemRole === "owner" && !effectiveLocal && !effectiveSSO
            ? `<div class="settings-section__description" role="alert" data-i18n-text="settings.profile.authentication.warning.noEffectiveOwner">This owner account has no effective sign-in method under the current authentication configuration. The current session may be temporary; host recovery may be required.</div>`
            : u?.systemRole === "owner" && !effectiveLocal && effectiveSSO && !getLocalAuthEnabled()
                ? `<div class="settings-section__description" role="alert" data-i18n-text="settings.profile.authentication.warning.localDisabledOwner">This owner relies on SSO while local authentication is disabled. If SSO becomes unavailable, recovery requires host access, recover-owner, and re-enabling local authentication.</div>`
                : u?.systemRole === "owner" && !effectiveLocal && effectiveSSO
                    ? `<div class="settings-section__description" role="alert" data-i18n-text="settings.profile.authentication.warning.providerOnly">This owner relies on the external SSO provider. Set a local recovery password to prepare for an outage.</div>`
                    : "";
        const connectSSOAction = u && getOidcEnabled() && !u.oidcLinked
            ? u.hasLocalPassword
                ? `<button class="btn" id="connectSSOBtn" data-i18n-text="settings.profile.authentication.connectSSO">Connect SSO</button>`
                : `<div class="muted"><strong data-i18n-text="settings.profile.authentication.connectSSO">Connect SSO</strong>: <span data-i18n-text="settings.profile.authentication.connectRequiresLocal">Set or recover a Scrumboy password before connecting the current SSO provider.</span></div>`
            : "";
        const methodActions = u ? `
	  <div style="margin-top: 12px; display: flex; flex-wrap: wrap; gap: 8px;">
	    ${u.oidcLinked && !u.hasLocalPassword ? `<button class="btn" id="setScrumboyPasswordBtn" data-i18n-text="settings.profile.authentication.setPassword">Set Scrumboy password</button>` : ""}
	    ${connectSSOAction}
	  </div>` : "";
        const twoFactorSection = u ? (u.twoFactorEnabled
            ? `
        <div class="settings-section" style="margin-top: 24px;">
          <div class="settings-section__title" data-i18n-text="settings.profile.twoFactor.title">Two-factor authentication</div>
          <div class="settings-section__description muted" data-i18n-text="settings.profile.twoFactor.enabledDescription">2FA is enabled. You can disable it or regenerate recovery codes.</div>
          <div style="margin-top: 12px; display: flex; flex-wrap: wrap; gap: 8px;">
            <button class="btn btn--ghost" id="disable2FABtn" data-i18n-text="settings.profile.twoFactor.disable">Disable 2FA</button>
            <button class="btn btn--ghost" id="regenerateRecoveryCodesBtn" data-i18n-text="settings.profile.twoFactor.regenerate">Regenerate recovery codes</button>
          </div>
        </div>
      `
            : `
        <div class="settings-section" style="margin-top: 24px;">
          <div class="settings-section__title" data-i18n-text="settings.profile.twoFactor.title">Two-factor authentication</div>
          <div class="settings-section__description muted" data-i18n-text="settings.profile.twoFactor.disabledDescription">Add an extra layer of security with an authenticator app.</div>
          <button class="btn" id="enable2FABtn" style="margin-top: 8px;" data-i18n-text="settings.profile.twoFactor.enable">Enable 2FA</button>
        </div>
      `) : "";
        return `
      <div class="settings-section" style="position: relative;">
        <div class="settings-section__title" data-i18n-text="settings.profile.title">Profile</div>
        <div class="settings-section__description muted" data-i18n-text="settings.profile.description">Signed-in user for this instance.</div>
        ${u ? `
          <div class="profile-avatar-wrap" style="margin-bottom: 16px;">
            <div style="display: flex; align-items: center; gap: 12px;">
              ${renderUserAvatar(u, { id: 'profileAvatarBtn', ariaLabel: 'Change avatar' })}
              ${u.image ? `<button class="btn btn--ghost" id="removeAvatarBtn" data-i18n-text="settings.profile.removeAvatar">Remove avatar</button>` : ""}
            </div>
            <div id="profileAvatarError" class="muted" style="display: none; margin-top: 8px;" role="alert"></div>
          </div>
          <div class="settings-kv">
            <div class="settings-kv__row"><div class="muted" data-i18n-text="settings.profile.fields.name">Name</div><div>${escapeHTML(u.name || "")}</div></div>
            <div class="settings-kv__row"><div class="muted" data-i18n-text="settings.profile.fields.email">Email</div><div>${escapeHTML(u.email || "")}</div></div>
            <div class="settings-kv__row"><div class="muted" data-i18n-text="settings.profile.fields.userId">User ID</div><div>${u.id != null ? escapeHTML(String(u.id)) : ""}</div></div>
            <div class="settings-kv__row"><div class="muted" data-i18n-text="settings.profile.fields.systemRole">System Role</div><div>${u.systemRole ? escapeHTML(u.systemRole.charAt(0).toUpperCase() + u.systemRole.slice(1)) : "User"}</div></div>
			<div class="settings-kv__row"><div class="muted" data-i18n-text="settings.profile.fields.authentication">Authentication</div><div data-i18n-text="${authenticationKey}">${escapeHTML(t(authenticationKey))}</div></div>
          </div>
		  ${ownerWarning}
		  ${!getLocalAuthEnabled() && u.hasLocalPassword ? `<div class="settings-section__description muted" data-i18n-text="settings.profile.authentication.localDisabled">A local password is stored, but local login is disabled by the operator.</div>` : ""}
		  ${methodActions}
          <div style="margin-top: 16px; display: flex; gap: 8px;">
            <button class="btn btn--danger" id="logoutBtn" data-i18n-text="settings.profile.logout">Log out</button>
            ${u.isBootstrap ? `<button class="btn" id="createUserBtn" data-i18n-text="settings.profile.createUser">Create User</button>` : ""}
          </div>
          ${twoFactorSection}
		  <div class="settings-section__description muted" style="margin-top: 12px;" data-i18n-text="settings.profile.twoFactor.responsibility">Scrumboy 2FA protects local-password sign-in and sensitive authentication-method changes. MFA for normal SSO sign-in is controlled by the configured identity provider.</div>
        ` : `
          <div class="muted" data-i18n-text="settings.profile.notSignedIn">Not signed in.</div>
        `}
      </div>
    `;
    })();
    reloadKeybindingsFromStorage();
    const keybindingRowsHTML = KEY_ACTION_LIST.map((meta) => {
        const chord = getResolvedChordForAction(meta.id);
        return `
      <div class="keybinding-row" data-keybinding-row="${meta.id}">
        <span class="keybinding-row__label" data-keybinding-action-id="${meta.id}">${escapeHTML(meta.label)}</span>
        <button type="button" class="btn btn--ghost keybinding-capture" data-keybinding-capture data-keybinding-action="${meta.id}">
          ${escapeHTML(formatChordForDisplay(chord))}
        </button>
      </div>`;
    }).join("");
    const desktopNotifyStatusKind = getDesktopNotificationStatusKind();
    const desktopNotifyGranted = desktopNotifyStatusKind === "granted";
    const pushVapidServerReady = showProfileTab && getPushConfigured();
    const activeSettingsTab = getSettingsActiveTab();
    const showWallpaperSettings = getAuthStatusAvailable();
    const wallpaperState = showWallpaperSettings ? getStoredWallpaperState() : { v: 1, mode: "off" };
    const wallpaperPickerHex = showWallpaperSettings && wallpaperState.mode === "color" && wallpaperState.hex ? wallpaperState.hex : "#8b919a";
    const wallpaperSummaryText = wallpaperState.mode === "off"
        ? "Off: default appearance"
        : wallpaperState.mode === "color"
            ? "Solid color: active"
            : wallpaperState.mode === "builtin"
                ? "Default image: active"
                : "Custom image: active";
    const wallpaperImageModeSelected = wallpaperState.mode === "image" || wallpaperState.mode === "builtin";
    const wallpaperSectionHTML = showWallpaperSettings
        ? `
      <div class="settings-section">
        <div class="settings-section__title" data-i18n-text="settings.customization.wallpaper.title">Wallpaper</div>
        <div class="settings-section__description muted" data-i18n-text="settings.customization.wallpaper.description">Optional background behind the app. A scrim keeps text readable. Boards and cards stay solid; Settings can show the wallpaper when it is active.</div>
        <p class="muted" id="wallpaperSummary" style="margin:8px 0 0 0;font-size:13px;" data-i18n-text="${getWallpaperSummaryMessageKey(wallpaperState.mode)}">
          ${escapeHTML(wallpaperSummaryText)}
        </p>
        <div class="theme-selector theme-selector--inline" style="margin-top:10px;">
          <label class="theme-option theme-option--inline">
            <input type="radio" name="wallpaperMode" value="off" ${wallpaperState.mode === "off" ? "checked" : ""}>
            <span data-i18n-text="settings.customization.wallpaper.mode.off">Off</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="wallpaperMode" value="color" ${wallpaperState.mode === "color" ? "checked" : ""}>
            <span data-i18n-text="settings.customization.wallpaper.mode.color">Solid color</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="wallpaperMode" value="image" ${wallpaperImageModeSelected ? "checked" : ""} ${getUser() || wallpaperState.mode === "builtin" ? "" : "disabled"}>
            <span data-i18n-text="settings.customization.wallpaper.mode.image">Custom image</span>
          </label>
        </div>
        <div id="wallpaperColorRow" class="wallpaper-settings-color-row" style="margin-top:12px;${wallpaperState.mode === "color" ? "" : "display:none;"}">
          <label class="row" style="align-items:center;gap:10px;">
            <span class="muted" data-i18n-text="settings.customization.wallpaper.colorLabel">Color</span>
            <input type="color" id="wallpaperColorPicker" value="${escapeHTML(wallpaperPickerHex)}" ${wallpaperState.mode === "color" ? "" : "disabled"} />
          </label>
        </div>
        <div class="wallpaper-settings-wallpaper-actions" style="margin-top:12px;display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
          <button type="button" class="btn" id="wallpaperUploadBtn" ${getUser() ? "" : "disabled"} style="${wallpaperImageModeSelected && getUser() ? "" : "display:none;"}" data-i18n-text="${wallpaperState.mode === "image" ? "settings.customization.wallpaper.actions.replace" : "settings.customization.wallpaper.actions.upload"}">${wallpaperState.mode === "image" ? "Replace image…" : "Upload image…"}</button>
          <button type="button" class="btn btn--ghost" id="wallpaperRemoveBtn" ${wallpaperState.mode === "off" ? "disabled" : ""} data-i18n-text="settings.customization.wallpaper.actions.remove">Remove wallpaper</button>
        </div>
        ${!getUser() ? `<p class="muted" style="margin-top:10px;font-size:13px;" data-i18n-text="settings.customization.wallpaper.signInHint">Sign in to use a custom image. Solid color and Off work without signing in.</p>` : ""}
      </div>
    `
        : "";
    const pushPwaDisabledNoticeKey = !pushVapidServerReady
        ? showProfileTab
            ? "settings.customization.push.vapidNotice"
            : "settings.customization.push.anonymousNotice"
        : "";
    const pushPwaDisabledNoticeText = !pushVapidServerReady
        ? showProfileTab
            ? "Web Push needs VAPID keys on the server (SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY; see docs)."
            : "Web Push is not available in anonymous mode."
        : "";
    const languageSectionHTML = `
      <div class="settings-section">
        <label class="settings-section__title" for="settingsLocaleSelect" data-i18n-text="settings.language.title">Language</label>
        <div class="settings-section__description muted" data-i18n-text="settings.language.description">Choose the language used for Scrumboy on this browser.</div>
        ${renderPublicLocaleSelectHTML({ id: "settingsLocaleSelect", style: "margin-top: 10px; min-width: 180px;" })}
      </div>
    `;
    const customizationHTML = activeSettingsTab === "customization" ? `
    <div id="settingsCustomizationContent">
      ${languageSectionHTML}
      <div class="settings-section">
        <div class="settings-section__title" data-i18n-text="settings.customization.theme.title">Theme</div>
        <div class="settings-section__description muted" data-i18n-text="settings.customization.theme.description">Choose your preferred color scheme.</div>
        <div class="theme-selector theme-selector--inline">
          <label class="theme-option theme-option--inline">
            <input type="radio" name="theme" value="system" ${getStoredTheme() === THEME_SYSTEM ? "checked" : ""}>
            <span data-i18n-text="settings.customization.theme.option.system">System</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="theme" value="dark" ${getStoredTheme() === THEME_DARK ? "checked" : ""}>
            <span data-i18n-text="settings.customization.theme.option.dark">Dark</span>
          </label>
          <label class="theme-option theme-option--inline">
            <input type="radio" name="theme" value="light" ${getStoredTheme() === THEME_LIGHT ? "checked" : ""}>
            <span data-i18n-text="settings.customization.theme.option.light">Light</span>
          </label>
        </div>
      </div>
      ${wallpaperSectionHTML}
      ${getAuthStatusAvailable() ? renderVoiceFlowCustomizationHTML() : ""}
      <div class="settings-section">
        <div class="settings-section__title" data-i18n-text="settings.customization.notifications.title">Desktop notifications</div>
        <div class="settings-section__description muted" data-i18n-text="settings.customization.notifications.description">OS-level alerts when someone assigns you a todo (works when this tab is in the background).</div>
        <p class="muted" id="desktopNotifyStatus" style="margin: 8px 0;" data-i18n-text="${getDesktopNotificationStatusMessageKey(desktopNotifyStatusKind)}">${escapeHTML(getDesktopNotificationStatusDescription())}</p>
        <button type="button" class="btn" id="desktopNotifyEnableBtn" ${desktopNotifyGranted ? "disabled" : ""} data-i18n-text="${getDesktopNotificationButtonMessageKey(desktopNotifyStatusKind)}">${desktopNotifyGranted ? "Notifications enabled" : "Enable notifications"}</button>
      </div>
      ${pushPwaDisabledNoticeKey ? `<p class="settings-push-vapid-notice" role="status" data-i18n-text="${pushPwaDisabledNoticeKey}">${escapeHTML(pushPwaDisabledNoticeText)}</p>` : ""}
      <div class="settings-section settings-section--push-pwa${!pushVapidServerReady ? " settings-section--push-pwa-disabled" : ""}">
        <div class="settings-section__title" data-i18n-text="settings.customization.push.title">Background notifications (PWA)</div>
        <div class="settings-section__description muted" data-i18n-text="settings.customization.push.description">Alerts when someone assigns you a todo while this app is in the background or closed (best on an installed PWA). Requires VAPID keys on the server. When configured, sign-in triggers an automatic subscribe attempt (the browser may ask for permission). Use the toggle to turn Web Push off or back on for this browser only.</div>
        <label class="row" style="align-items:center;gap:8px;margin-top:10px;cursor:pointer;">
          <input type="checkbox" id="pushNotifyToggle" ${!pushVapidServerReady ? "disabled" : ""} />
          <span data-i18n-text="settings.customization.push.toggleLabel">Web Push on this device</span>
        </label>
        <p class="muted" id="pushNotifyHint" style="margin:8px 0 0 0;font-size:13px;"></p>
      </div>
      <div class="settings-section settings-section--keybindings">
        <div class="settings-section__title" data-i18n-text="settings.customization.keybindings.title">Keybindings</div>
        <div class="settings-section__description muted" data-i18n-text="settings.customization.keybindings.description">Click a key to record a new shortcut. Press Esc to cancel while listening.</div>
        <div class="keybinding-list">
          ${keybindingRowsHTML}
        </div>
      </div>
    </div>
    ` : "";
    // Determine content for each tab
    const tagColorsContent = hasProjectAccess
        ? `
      <div class="settings-section">
        <div class="settings-section__title" data-i18n-text="settings.tagColors.title">Tag Colors</div>
        <div class="settings-section__description muted" data-i18n-text="settings.tagColors.description">Assign custom colors to tags. Colors will appear in filter chips and todo cards.</div>
        <div class="settings-tags-list">
          ${tagsHTML}
        </div>
      </div>
    `
        : `
      <div class="settings-section">
        <div class="settings-section__title" data-i18n-text="settings.tagColors.title">Tag Colors</div>
        <div class="settings-section__description muted" data-i18n-text="settings.tagColors.description">Assign custom colors to tags. Colors will appear in filter chips and todo cards.</div>
        <div class="muted" data-i18n-text="settings.tagColors.noProjects">No projects available. Create a project to manage tag colors.</div>
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
        <div class="muted" data-i18n-text="settings.charts.noProjects">No projects available. Create a project to view charts.</div>
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
      ${showProfileTab ? `<button class="settings-tab ${activeSettingsTab === "profile" ? "settings-tab--active" : ""}" data-tab="profile" data-i18n-text="settings.tabs.profile">Profile</button>` : ``}
      ${showUsersTab ? `<button class="settings-tab ${activeSettingsTab === "users" ? "settings-tab--active" : ""}" data-tab="users" data-i18n-text="settings.tabs.users">Users</button>` : ``}
      ${showSprintsTab ? `<button class="settings-tab ${activeSettingsTab === "sprints" ? "settings-tab--active" : ""}" data-tab="sprints" data-i18n-text="settings.tabs.sprints">Sprints</button>` : ``}
      ${showWorkflowTab ? `<button class="settings-tab ${activeSettingsTab === "workflow" ? "settings-tab--active" : ""}" data-tab="workflow" data-i18n-text="settings.tabs.workflow">Workflow</button>` : ``}
      <button class="settings-tab ${activeSettingsTab === "customization" ? "settings-tab--active" : ""}" data-tab="customization" data-i18n-text="settings.tabs.customization">Customization</button>
      <button class="settings-tab ${activeSettingsTab === "tag-colors" ? "settings-tab--active" : ""}" data-tab="tag-colors" data-i18n-text="settings.tabs.tagColors">Tag Colors</button>
      ${showChartsTab ? `<button class="settings-tab ${activeSettingsTab === "charts" ? "settings-tab--active" : ""}" data-tab="charts" data-i18n-text="settings.tabs.charts">Charts</button>` : ``}
      <button class="settings-tab ${activeSettingsTab === "backup" ? "settings-tab--active" : ""}" data-tab="backup" data-i18n-text="settings.tabs.backup">Backup</button>
    </div>
    <div class="settings-tab-content" id="settingsTabContent">
      ${activeSettingsTab === "profile" ? profileHTML : activeSettingsTab === "users" ? usersHTML : activeSettingsTab === "sprints" ? sprintsHTML : activeSettingsTab === "workflow" ? workflowHTML : activeSettingsTab === "customization" ? customizationHTML : activeSettingsTab === "tag-colors" ? tagColorsContent : activeSettingsTab === "charts" ? chartsContent : activeSettingsTab === "backup" ? renderBackupTabHTML() : ""}
    </div>
  `;
    if (getLocale() !== "en") {
        applySettingsLocaleToOpenDialog();
    }
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
        bindBurndownNav(signal);
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
    const settingsDlg = settingsDialog;
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
                    showToast(t("settings.profile.toast.avatarUpdated"));
                }
                catch (err) {
                    const msg = apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.toast.uploadFailed" });
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
                showToast(t("settings.profile.toast.avatarRemoved"));
            }
            catch (err) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.toast.uploadFailed" }));
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
    const setPasswordBtn = document.getElementById("setScrumboyPasswordBtn");
    if (setPasswordBtn)
        setPasswordBtn.addEventListener("click", () => void beginSetScrumboyPassword(), { signal });
    const connectSSOBtn = document.getElementById("connectSSOBtn");
    if (connectSSOBtn)
        connectSSOBtn.addEventListener("click", () => showConnectSSODialog(), { signal });
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
                    showToast(t("settings.users.toast.promoted"));
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.users.toast.promoteFailed" }));
                }
            }, { signal });
        });
        // Demote button
        document.querySelectorAll('[data-action="demote"]').forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const userId = e.currentTarget.getAttribute("data-user-id");
                if (!userId)
                    return;
                const confirmed = await showConfirmDialog(t("settings.users.demote.confirmMessage"), t("settings.users.demote.confirmTitle"), t("settings.users.demote.confirmAction"));
                if (!confirmed) {
                    return;
                }
                try {
                    await apiFetch(`/api/admin/users/${userId}/role`, {
                        method: "PATCH",
                        body: JSON.stringify({ role: "user" }),
                    });
                    showToast(t("settings.users.toast.demoted"));
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.users.toast.demoteFailed" }));
                }
            }, { signal });
        });
        // Delete button
        document.querySelectorAll('[data-action="delete"]').forEach(btn => {
            btn.addEventListener("click", async (e) => {
                const userId = e.currentTarget.getAttribute("data-user-id");
                if (!userId)
                    return;
                if (!await confirmDelete(t("settings.users.delete.confirmMessage"))) {
                    return;
                }
                try {
                    await apiFetch(`/api/admin/users/${userId}`, {
                        method: "DELETE",
                    });
                    showToast(t("settings.users.toast.deleted"));
                    await renderSettingsModal();
                }
                catch (err) {
                    showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.users.toast.deleteFailed" }));
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
        bindSprintsTabInteractions({
            signal,
            rerender: () => renderSettingsModal(),
            invalidateSprintChartsCache: invalidateSprintsForChartsCache,
        });
    }
    // Setup theme selector
    document.querySelectorAll('input[name="theme"]').forEach(radio => {
        radio.addEventListener('change', (e) => {
            handleThemeChange(e.target.value);
        }, { signal });
    });
    function syncWallpaperRadiosFromState() {
        const st = getStoredWallpaperState();
        const rOff = document.querySelector('input[name="wallpaperMode"][value="off"]');
        const rCol = document.querySelector('input[name="wallpaperMode"][value="color"]');
        const rImg = document.querySelector('input[name="wallpaperMode"][value="image"]');
        if (st.mode === "off" && rOff)
            rOff.checked = true;
        else if (st.mode === "color" && rCol)
            rCol.checked = true;
        else if (st.mode === "image" && rImg)
            rImg.checked = true;
        else if (st.mode === "builtin" && rImg)
            rImg.checked = true;
    }
    function openWallpaperFileDialog() {
        const input = document.createElement("input");
        input.type = "file";
        input.accept = "image/jpeg,image/png,image/gif";
        input.onchange = async (ev) => {
            const file = ev.target.files?.[0];
            if (!file)
                return;
            try {
                const blob = await processWallpaperFileForUpload(file);
                await uploadWallpaperImage(blob);
                showToast(t("settings.customization.wallpaper.toast.updated"));
            }
            catch (err) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.customization.wallpaper.toast.uploadFailed" }));
            }
            await renderSettingsModal();
        };
        input.click();
    }
    document.querySelectorAll('input[name="wallpaperMode"]').forEach(radio => {
        radio.addEventListener("change", async (e) => {
            const el = e.target;
            if (el.value === "off") {
                await setWallpaperOff();
                await renderSettingsModal();
                return;
            }
            if (el.value === "color") {
                const picker = document.getElementById("wallpaperColorPicker");
                await setWallpaperColor(picker?.value || wallpaperPickerHex);
                await renderSettingsModal();
                return;
            }
            if (el.value === "image") {
                el.checked = false;
                syncWallpaperRadiosFromState();
                if (!getUser()) {
                    showToast(t("settings.customization.wallpaper.toast.signInRequired"));
                    return;
                }
                openWallpaperFileDialog();
            }
        }, { signal });
    });
    const wallpaperColorPicker = document.getElementById("wallpaperColorPicker");
    if (wallpaperColorPicker) {
        wallpaperColorPicker.addEventListener("input", async () => {
            const mode = document.querySelector('input[name="wallpaperMode"]:checked')?.value;
            if (mode !== "color")
                return;
            await setWallpaperColor(wallpaperColorPicker.value);
        }, { signal });
    }
    const wallpaperUploadBtn = document.getElementById("wallpaperUploadBtn");
    if (wallpaperUploadBtn) {
        wallpaperUploadBtn.addEventListener("click", () => {
            if (!getUser()) {
                showToast(t("settings.customization.wallpaper.toast.signInRequired"));
                return;
            }
            openWallpaperFileDialog();
        }, { signal });
    }
    const wallpaperRemoveBtn = document.getElementById("wallpaperRemoveBtn");
    if (wallpaperRemoveBtn) {
        wallpaperRemoveBtn.addEventListener("click", async () => {
            await setWallpaperOff();
            showToast(t("settings.customization.wallpaper.toast.removed"));
            await renderSettingsModal();
        }, { signal });
    }
    if (getSettingsActiveTab() === "customization") {
        const localeSelect = document.getElementById("settingsLocaleSelect");
        bindPublicLocaleSelect(localeSelect, { signal });
        const voiceFlowEnabledToggle = document.getElementById("voiceFlowEnabledToggle");
        if (voiceFlowEnabledToggle) {
            voiceFlowEnabledToggle.addEventListener("change", () => {
                setVoiceFlowEnabledPreference(voiceFlowEnabledToggle.checked);
                emit("voiceflow:enabled-changed", voiceFlowEnabledToggle.checked);
            }, { signal });
        }
        const desktopNotifyBtn = document.getElementById("desktopNotifyEnableBtn");
        if (desktopNotifyBtn && !desktopNotifyBtn.hasAttribute("disabled")) {
            desktopNotifyBtn.addEventListener("click", async () => {
                const r = await requestDesktopNotificationPermission();
                if (r === "granted") {
                    showToast(t("settings.customization.notifications.toast.enabled"));
                }
                else if (r === "denied") {
                    showToast(t("settings.customization.notifications.toast.blocked"));
                }
                else {
                    showToast(t("settings.customization.notifications.toast.notGranted"));
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
                    pushHint.textContent = t("settings.customization.push.unsupported");
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
                            showToast(t("settings.customization.push.toast.enableFailed"));
                        }
                        else {
                            showToast(t("settings.customization.push.toast.enabled"));
                        }
                    }
                    else {
                        await unsubscribeFromPush();
                        showToast(t("settings.customization.push.toast.disabled"));
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
                btn.textContent = getKeybindingCapturePrompt(actionId);
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
    bindTagTabInteractions({
        signal,
        hasProjectAccess,
        rerender: () => renderSettingsModal(),
    });
}
async function renderUsersTabContent() {
    const currentUser = getUser();
    const isOwner = currentUser?.systemRole === "owner";
    const isAdmin = currentUser?.systemRole === "admin";
    try {
        const users = await apiFetch("/api/admin/users");
        if (users.length === 0) {
            return `<div class="settings-section"><div class="muted" data-i18n-text="settings.users.empty">No users found.</div></div>`;
        }
        const rows = users.map((user) => {
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
                }
                else if (isOwnerRole) {
                    // Other owner: no actions (can't demote/promote owners, can't delete owners)
                    actionsHTML = "-";
                }
                else if (isAdminRole) {
                    // Admin: can demote to user or delete
                    actionsHTML = `
            <div class="users-table__actions">
              <button class="btn btn--ghost btn--small" data-action="demote" data-user-id="${user.id}" data-user-role="${userRole}" data-i18n-text="settings.users.actions.demote">Demote</button>
              <button class="btn btn--danger btn--small" data-action="delete" data-user-id="${user.id}" data-i18n-text="settings.users.actions.delete">Delete</button>
			  ${getLocalAuthEnabled() && user.hasLocalPassword ? `<button class="btn btn--ghost btn--small" data-action="password" data-user-id="${user.id}" data-i18n-text="settings.users.actions.password">Scrumboy password</button>` : ""}
            </div>
          `;
                }
                else if (isUserRole) {
                    // User: can promote to admin or delete
                    actionsHTML = `
            <div class="users-table__actions">
              <button class="btn btn--ghost btn--small" data-action="promote" data-user-id="${user.id}" data-user-role="${userRole}" data-i18n-text="settings.users.actions.promote">Promote</button>
              <button class="btn btn--danger btn--small" data-action="delete" data-user-id="${user.id}" data-i18n-text="settings.users.actions.delete">Delete</button>
			  ${getLocalAuthEnabled() && user.hasLocalPassword ? `<button class="btn btn--ghost btn--small" data-action="password" data-user-id="${user.id}" data-i18n-text="settings.users.actions.password">Scrumboy password</button>` : ""}
            </div>
          `;
                }
            }
            else if (isAdmin) {
                // Admin: can view but not manage
                actionsHTML = "-";
            }
            const roleDisplay = userRole.charAt(0).toUpperCase() + userRole.slice(1);
            const userDisplay = user.name || user.email || `User ${user.id}`;
            const authKey = user.hasLocalPassword && user.oidcLinked
                ? "settings.profile.authentication.dual"
                : user.hasLocalPassword
                    ? "settings.profile.authentication.local"
                    : user.oidcLinked
                        ? "settings.profile.authentication.sso"
                        : "settings.profile.authentication.none";
            return `
        <tr>
          <td>${escapeHTML(userDisplay)}${user.email && user.name ? ` <span class="muted">(${escapeHTML(user.email)})</span>` : ""}</td>
          <td>${escapeHTML(roleDisplay)}</td>
		  <td data-i18n-text="${authKey}">${escapeHTML(t(authKey))}</td>
          <td>${actionsHTML}</td>
        </tr>
      `;
        }).join("");
        return `
      <div class="settings-section">
        <div class="settings-section__title" data-i18n-text="settings.users.management.title">User Management</div>
        <div class="settings-section__description muted" data-i18n-text="settings.users.management.description">Manage system users and roles.</div>
        <table class="users-table">
          <thead>
            <tr>
              <th style="width: 35%;" data-i18n-text="settings.users.table.user">User</th>
              <th data-i18n-text="settings.users.table.systemRole">System Role</th>
			  <th data-i18n-text="settings.users.table.authentication">Authentication</th>
              <th data-i18n-text="settings.users.table.actions">Actions</th>
            </tr>
          </thead>
          <tbody>
            ${rows}
          </tbody>
        </table>
        ${isOwner || isAdmin ? `<div style="margin-top: 16px;"><button class="btn btn--ghost" id="createUserBtn" data-i18n-text="settings.users.createUser">Create User</button></div>` : ""}
      </div>
    `;
    }
    catch (err) {
        return `<div class="settings-section"><div class="muted"><span data-i18n-text="settings.users.loadError">Error loading users:</span> ${escapeHTML(err.message || "Unknown error")}</div></div>`;
    }
}
function showPasswordResetDialog(userId) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form" id="passwordResetForm">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="settings.users.passwordReset.title">Reset Password</div>
        <button class="btn btn--ghost" type="button" id="passwordResetDialogClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>

      <p class="muted" data-i18n-text="settings.users.passwordReset.description">Generate a one-time password reset link. The link will expire in 30 minutes.</p>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="passwordResetCancel" data-i18n-text="settings.users.passwordReset.cancel">Cancel</button>
        <button type="submit" class="btn" id="passwordResetGenerate" data-i18n-text="settings.users.passwordReset.generate">Generate Link</button>
      </div>
    </form>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const closeBtn = document.getElementById("passwordResetDialogClose");
    const cancelBtn = document.getElementById("passwordResetCancel");
    const form = document.getElementById("passwordResetForm");
    const close = () => {
        releaseLocale();
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
                    showToast(t("settings.users.passwordReset.generateFailed"));
                    return;
                }
                try {
                    await navigator.clipboard.writeText(res.reset_url);
                    showToast(t("settings.users.passwordReset.copied"));
                    close();
                }
                catch {
                    showPasswordResetFallbackDialog(res.reset_url);
                    close();
                }
            }
            catch (err) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.users.passwordReset.generateFailed" }));
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
        <div class="dialog__title" data-i18n-text="settings.users.passwordResetFallback.title">Reset link generated</div>
        <button class="btn btn--ghost" type="button" id="passwordResetFallbackClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>

      <p class="muted" data-i18n-text="settings.users.passwordResetFallback.description">Copy the link below and share it with the user. The link expires in 30 minutes.</p>
      <div class="field" style="margin: 12px 0;">
        <input type="text" id="passwordResetUrlDisplay" class="input" readonly value="${escapeHTML(resetUrl)}" style="font-size: 12px;" />
      </div>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn" id="passwordResetFallbackCopy" data-i18n-text="settings.users.passwordResetFallback.copy">Copy</button>
      </div>
    </div>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const closeBtn = document.getElementById("passwordResetFallbackClose");
    const copyBtn = document.getElementById("passwordResetFallbackCopy");
    const urlInput = document.getElementById("passwordResetUrlDisplay");
    const close = () => {
        releaseLocale();
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
                showToast(t("settings.users.passwordResetFallback.copied"));
            }
            catch {
                urlInput.select();
                showToast(t("settings.users.passwordResetFallback.copyManual"));
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
        <div class="dialog__title" data-i18n-text="settings.users.createUser.title">Create User</div>
        <button class="btn btn--ghost" type="button" id="createUserDialogClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>

      <label class="field">
        <div class="field__label" data-i18n-text="settings.users.createUser.emailLabel">Email</div>
        <input 
          type="email" 
          id="createUserEmail" 
          class="input" 
          placeholder="user@example.com" 
          data-i18n-placeholder="settings.users.createUser.emailPlaceholder"
          maxlength="200" 
          autocomplete="email" 
          required 
        />
      </label>

      <label class="field">
        <div class="field__label" data-i18n-text="settings.users.createUser.nameLabel">Name</div>
        <input 
          type="text" 
          id="createUserName" 
          class="input" 
          placeholder="User Name" 
          data-i18n-placeholder="settings.users.createUser.namePlaceholder"
          maxlength="200" 
          autocomplete="name" 
          required 
        />
      </label>

      <label class="field">
        <div class="field__label" data-i18n-text="settings.users.createUser.passwordLabel">Temporary Password</div>
        <div class="password-row">
          <input 
            type="password" 
            id="createUserPassword" 
            class="input" 
            placeholder="Password (min 8 characters)" 
            data-i18n-placeholder="settings.users.createUser.passwordPlaceholder"
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
        <button type="button" class="btn btn--ghost" id="createUserCancel" data-i18n-text="settings.users.createUser.cancel">Cancel</button>
        <button type="submit" class="btn" id="createUserSubmit" data-i18n-text="settings.users.createUser.submit">Create</button>
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
    // Stateful: reflects current visibility, preserved across locale changes.
    const syncPasswordToggleLabel = () => {
        if (!passwordToggle)
            return;
        const label = passwordInput && passwordInput.type === "text"
            ? t("auth.password.hide")
            : t("auth.password.show");
        passwordToggle.setAttribute("aria-label", label);
        passwordToggle.setAttribute("title", label);
    };
    const releaseLocale = bindDialogLocale(dialog, syncPasswordToggleLabel);
    if (passwordToggle && passwordInput && passwordIconPath) {
        passwordToggle.addEventListener("click", () => {
            const isPassword = passwordInput.type === "password";
            passwordInput.type = isPassword ? "text" : "password";
            passwordIconPath.setAttribute("d", isPassword ? PATH_HIDE : PATH_SHOW);
            syncPasswordToggleLabel();
        });
    }
    const close = () => {
        releaseLocale();
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
                showToast(t("settings.users.createUser.created"));
                close();
                // Refresh the settings modal if Users tab is active
                if (getSettingsActiveTab() === "users") {
                    await renderSettingsModal();
                }
            }
            catch (err) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.users.createUser.failed" }));
            }
        });
    }
}
function submitOIDCAuthorizationForm(request) {
    const endpoint = new URL(request.authorizationEndpoint, window.location.origin);
    if (endpoint.protocol !== "https:" && endpoint.protocol !== "http:") {
        throw new Error(t("settings.profile.authentication.providerInvalid"));
    }
    const form = document.createElement("form");
    form.method = "POST";
    form.action = endpoint.toString();
    form.referrerPolicy = "no-referrer";
    for (const [name, value] of Object.entries(request.authorizationParameters || {})) {
        const input = document.createElement("input");
        input.type = "hidden";
        input.name = name;
        input.value = value;
        form.appendChild(input);
    }
    document.body.appendChild(form);
    form.submit();
}
async function beginSetScrumboyPassword() {
    try {
        const status = await apiFetch("/api/auth/oidc/set-password/status");
        if (status.authorized) {
            showSetScrumboyPasswordDialog(status.localAuthEnabled);
            return;
        }
        const request = await apiFetch("/api/auth/oidc/set-password/start", { method: "POST" });
        submitOIDCAuthorizationForm(request);
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.authentication.startFailed" }));
    }
}
function showSetScrumboyPasswordDialog(localAuthEnabled) {
    const u = getUser();
    if (!u)
        return;
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form" id="setScrumboyPasswordForm">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="settings.profile.authentication.setPassword">Set Scrumboy password</div>
        <button class="btn btn--ghost" type="button" id="setScrumboyPasswordClose" data-i18n-aria-label="common.close">✕</button>
      </div>
      <div class="muted" data-i18n-text="settings.profile.authentication.setPasswordDescription">Your recent SSO reauthentication authorizes one first-password change for five minutes.</div>
      ${!localAuthEnabled ? `<div role="alert" data-i18n-text="settings.profile.authentication.localDisabledSet">The password will be stored, but it cannot be used until the operator re-enables local authentication.</div>` : ""}
      <label class="field"><div class="field__label" data-i18n-text="auth.fields.newPassword.label">New password</div><input class="input" id="setScrumboyPasswordNew" type="password" autocomplete="new-password" required /></label>
      <label class="field"><div class="field__label" data-i18n-text="auth.fields.confirmPassword.label">Confirm password</div><input class="input" id="setScrumboyPasswordConfirm" type="password" autocomplete="new-password" required /></label>
      ${u.twoFactorEnabled ? `<label class="field"><div class="field__label" data-i18n-text="settings.profile.authentication.twoFactorCode">Authenticator or recovery code</div><input class="input" id="setScrumboyPassword2FA" type="text" autocomplete="one-time-code" required /></label>` : ""}
      <div class="dialog__footer"><div class="spacer"></div><button class="btn btn--ghost" type="button" id="setScrumboyPasswordCancel" data-i18n-text="common.cancel">Cancel</button><button class="btn" type="submit" data-i18n-text="settings.profile.authentication.savePassword">Save password</button></div>
    </form>`;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    let removed = false;
    const close = () => {
        if (removed)
            return;
        removed = true;
        dialog.querySelectorAll('input[type="password"], input[autocomplete="one-time-code"]').forEach((input) => { input.value = ""; });
        releaseLocale();
        dialog.remove();
    };
    dialog.querySelector("#setScrumboyPasswordClose")?.addEventListener("click", close);
    dialog.querySelector("#setScrumboyPasswordCancel")?.addEventListener("click", close);
    dialog.addEventListener("click", (event) => { if (event.target === dialog)
        close(); });
    dialog.addEventListener("cancel", (event) => { event.preventDefault(); close(); });
    dialog.addEventListener("close", close);
    dialog.querySelector("form")?.addEventListener("submit", async (event) => {
        event.preventDefault();
        const password = dialog.querySelector("#setScrumboyPasswordNew").value;
        const confirmation = dialog.querySelector("#setScrumboyPasswordConfirm").value;
        if (password !== confirmation) {
            showToast(t("auth.reset.passwordsMismatch"));
            return;
        }
        const twoFactorCode = dialog.querySelector("#setScrumboyPassword2FA")?.value || "";
        try {
            await apiFetch("/api/auth/oidc/set-password", { method: "POST", body: JSON.stringify({ newPassword: password, twoFactorCode }) });
            const updated = await apiFetch("/api/me");
            setUser(updated);
            close();
            showToast(t("settings.profile.authentication.passwordSet"));
            await renderSettingsModal({ skipProfileRefetch: true });
        }
        catch (err) {
            showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.authentication.setFailed" }));
        }
    });
}
function showConnectSSODialog() {
    const u = getUser();
    if (!u)
        return;
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form" id="connectSSOForm">
      <div class="dialog__header"><div class="dialog__title" data-i18n-text="settings.profile.authentication.connectSSO">Connect SSO</div><button class="btn btn--ghost" type="button" id="connectSSOClose" data-i18n-aria-label="common.close">✕</button></div>
      <div class="muted" data-i18n-text="settings.profile.authentication.connectDescription">Confirm your current Scrumboy password, then reauthenticate with SSO. The verified SSO email must match your Scrumboy email.</div>
      <label class="field"><div class="field__label" data-i18n-text="settings.profile.authentication.currentPassword">Current Scrumboy password</div><input class="input" id="connectSSOPassword" type="password" autocomplete="current-password" required /></label>
      ${u.twoFactorEnabled ? `<label class="field"><div class="field__label" data-i18n-text="settings.profile.authentication.twoFactorCode">Authenticator or recovery code</div><input class="input" id="connectSSO2FA" type="text" autocomplete="one-time-code" required /></label>` : ""}
      <div class="dialog__footer"><div class="spacer"></div><button class="btn btn--ghost" type="button" id="connectSSOCancel" data-i18n-text="common.cancel">Cancel</button><button class="btn" type="submit" data-i18n-text="settings.profile.authentication.continueSSO">Continue with SSO</button></div>
    </form>`;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    let removed = false;
    const close = () => {
        if (removed)
            return;
        removed = true;
        dialog.querySelectorAll('input[type="password"], input[autocomplete="one-time-code"]').forEach((input) => { input.value = ""; });
        releaseLocale();
        dialog.remove();
    };
    dialog.querySelector("#connectSSOClose")?.addEventListener("click", close);
    dialog.querySelector("#connectSSOCancel")?.addEventListener("click", close);
    dialog.addEventListener("click", (event) => { if (event.target === dialog)
        close(); });
    dialog.addEventListener("cancel", (event) => { event.preventDefault(); close(); });
    dialog.addEventListener("close", close);
    dialog.querySelector("form")?.addEventListener("submit", async (event) => {
        event.preventDefault();
        const currentPassword = dialog.querySelector("#connectSSOPassword").value;
        const twoFactorCode = dialog.querySelector("#connectSSO2FA")?.value || "";
        try {
            const request = await apiFetch("/api/auth/oidc/link/start", { method: "POST", body: JSON.stringify({ currentPassword, twoFactorCode, returnTo: "/?auth_method=linked" }) });
            submitOIDCAuthorizationForm(request);
        }
        catch (err) {
            showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.authentication.linkFailed" }));
        }
    });
}
export async function resumeAuthenticationMethodFlow(kind) {
    if (kind === "set_password") {
        await beginSetScrumboyPassword();
    }
    else if (kind === "linked") {
        showToast(t("settings.profile.authentication.linked"));
    }
}
async function showEnable2FADialog() {
    try {
        const setup = await apiFetch("/api/auth/2fa/setup", { method: "POST" });
        if (!setup?.setupToken || !setup?.otpauthUri) {
            showToast(t("settings.profile.enable2fa.setupFailed"));
            return;
        }
        const qrDataUrl = setup.qrCodeDataUrl ?? "";
        const dialog = document.createElement("dialog");
        dialog.className = "dialog";
        dialog.innerHTML = `
      <form method="dialog" class="dialog__form" id="enable2FAForm">
        <div class="dialog__header">
          <div class="dialog__title" data-i18n-text="settings.profile.enable2fa.title">Enable two-factor authentication</div>
          <button class="btn btn--ghost" type="button" id="enable2FAClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
        </div>
        <div class="muted" style="margin-bottom: 12px;" data-i18n-text="settings.profile.enable2fa.instructions">Scan the QR code with your authenticator app, or enter the key manually.</div>
        ${qrDataUrl ? `<div style="margin-bottom: 12px;"><img src="${escapeHTML(qrDataUrl)}" alt="${escapeHTML(t("settings.profile.enable2fa.qrAlt"))}" width="192" height="192" style="display: block; margin: 0 auto;" /></div>` : ""}
        <div class="muted" style="margin-bottom: 8px; font-family: monospace; word-break: break-all;">${escapeHTML(setup.manualEntryKey)}</div>
        <label class="field">
          <div class="field__label" data-i18n-text="settings.profile.enable2fa.codeLabel">Enter the 6-digit code from your app</div>
          <input type="text" id="enable2FACode" class="input" placeholder="123456" maxlength="10" autocomplete="one-time-code" required />
          <div id="enable2FAError" class="field-error" style="display: none;" role="alert"></div>
        </label>
        <div class="dialog__footer">
          <div class="spacer"></div>
          <button type="button" class="btn btn--ghost" id="enable2FACancel" data-i18n-text="settings.profile.enable2fa.cancel">Cancel</button>
          <button type="submit" class="btn" id="enable2FASubmit" data-i18n-text="settings.profile.enable2fa.submit">Enable</button>
        </div>
      </form>
    `;
        document.body.appendChild(dialog);
        dialog.showModal();
        const releaseLocale = bindDialogLocale(dialog);
        const close = () => {
            releaseLocale();
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
                    showToast(t("settings.profile.toast.enabled"));
                    await renderSettingsModal();
                }
                catch (err) {
                    const msg = apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.enable2fa.enableFailed" });
                    showError(msg);
                }
            });
        }
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.enable2fa.setupFailed" }));
    }
}
function showRecoveryCodesDialog(codes) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="settings.profile.recovery.title">Recovery codes</div>
        <button class="btn btn--ghost" type="button" id="recoveryCodesClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>
      <div class="muted" style="margin-bottom: 12px;" data-i18n-text="settings.profile.recovery.description">Save these codes in a secure place. Each can be used once to sign in if you lose access to your authenticator app.</div>
      <div style="font-family: monospace; word-break: break-all; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px;">
        ${codes.map((c) => escapeHTML(c)).join(" &nbsp; ")}
      </div>
      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn" id="recoveryCodesDone" data-i18n-text="settings.profile.recovery.done">Done</button>
      </div>
    </div>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const close = () => {
        releaseLocale();
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
        <div class="dialog__title" data-i18n-text="settings.profile.disable2fa.title">Disable two-factor authentication</div>
        <button class="btn btn--ghost" type="button" id="disable2FAClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>
      <div class="muted" style="margin-bottom: 12px;" data-i18n-text="settings.profile.disable2fa.description">Enter your password to disable 2FA.</div>
      <label class="field">
        <div class="field__label" data-i18n-text="settings.profile.disable2fa.passwordLabel">Password</div>
        <input type="password" id="disable2FAPassword" class="input" placeholder="Password" data-i18n-placeholder="settings.profile.disable2fa.passwordPlaceholder" required />
      </label>
      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="disable2FACancel" data-i18n-text="settings.profile.disable2fa.cancel">Cancel</button>
        <button type="submit" class="btn btn--danger" id="disable2FASubmit" data-i18n-text="settings.profile.disable2fa.submit">Disable 2FA</button>
      </div>
    </form>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const close = () => {
        releaseLocale();
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
                showToast(t("settings.profile.toast.disabled"));
                await renderSettingsModal();
            }
            catch (err) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.disable2fa.failed" }));
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
            showToast(t("settings.profile.toast.recoveryRegenerated"));
            await renderSettingsModal();
        }
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.recovery.regenerateFailed" }));
    }
}

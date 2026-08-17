import { apiFetch } from '../api.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { getAssigneeFromUrl, getPriorityFromUrl, getSortFromUrl, getBoard, getSearch, getSettingsActiveTab, getSlug, getSprintIdFromUrl, getTag, } from '../state/selectors.js';
import { escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import { apiErrorMessageOrRaw, t } from '../i18n/index.js';
const DEFAULT_WORKFLOW_LANE_COLOR = '#64748b';
let workflowLaneCountsCache = null;
let workflowLaneCountsFetchGeneration = 0;
let workflowTabDraft = null;
let workflowTabDraftBaseline = null;
let workflowTabDraftSlug = null;
function workflowLaneLabelAria(key) {
    return t('settings.workflow.laneLabelAria', { key });
}
function workflowLaneColorAria(key) {
    return t('settings.workflow.laneColorAria', { key });
}
function normalizeWorkflowLaneColorForInput(color) {
    const s = color?.trim();
    return s && /^#[0-9a-fA-F]{6}$/.test(s) ? s : DEFAULT_WORKFLOW_LANE_COLOR;
}
function cloneWorkflowLanesFromBoard() {
    const workflow = getBoard()?.columnOrder ?? [];
    return workflow.map((lane) => ({
        key: lane.key,
        name: lane.name,
        color: normalizeWorkflowLaneColorForInput(lane.color),
        isDone: !!lane.isDone,
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
export function resetWorkflowDraftToBaseline() {
    if (workflowTabDraftBaseline && workflowTabDraftSlug === getSlug()) {
        workflowTabDraft = JSON.parse(JSON.stringify(workflowTabDraftBaseline));
    }
    else {
        ensureWorkflowDraftInitialized();
    }
}
export function clearWorkflowDraftState() {
    workflowTabDraft = null;
    workflowTabDraftBaseline = null;
    workflowTabDraftSlug = null;
}
export function isWorkflowDraftDirty() {
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
    const btn = document.querySelector('[data-workflow-save-changes]');
    if (btn)
        btn.disabled = !isWorkflowDraftDirty();
}
export function invalidateWorkflowLaneCountsCache() {
    workflowLaneCountsCache = null;
    workflowLaneCountsFetchGeneration++;
}
async function fetchWorkflowLaneCountsState(slug) {
    try {
        const res = await apiFetch(`/api/board/${encodeURIComponent(slug)}/workflow/counts`);
        if (!res || typeof res.countsByColumnKey !== 'object' || res.countsByColumnKey === null) {
            return { status: 'error' };
        }
        return { status: 'ok', counts: res.countsByColumnKey };
    }
    catch {
        return { status: 'error' };
    }
}
function renderWorkflowTabContent(countsState) {
    const board = getBoard();
    const columns = board?.columnOrder ?? [];
    if (!getSlug()) {
        return `<div class="settings-section"><div class="muted" data-i18n-text="settings.workflow.error.noProject">${escapeHTML(t('settings.workflow.error.noProject'))}</div></div>`;
    }
    if (columns.length === 0) {
        return `<div class="settings-section"><div class="muted" data-i18n-text="settings.workflow.error.lanesUnavailable">${escapeHTML(t('settings.workflow.error.lanesUnavailable'))}</div></div>`;
    }
    ensureWorkflowDraftInitialized();
    const workflow = workflowTabDraft ?? [];
    const canDeleteAnyLane = workflow.length > 2;
    const loadingBanner = countsState.status === 'loading'
        ? `<div class="muted settings-workflow-counts-banner" style="margin-bottom:10px;" data-i18n-text="settings.workflow.counts.loading">Checking lane occupancy…</div>`
        : '';
    const errorBanner = countsState.status === 'error'
        ? `<div class="settings-workflow-counts-banner settings-workflow-counts-banner--error muted" style="margin-bottom:10px;display:flex;flex-wrap:wrap;align-items:center;gap:8px;">
          <span data-i18n-text="settings.workflow.counts.error">Could not load lane occupancy. Delete stays disabled until this succeeds.</span>
          <button type="button" class="btn btn--ghost btn--small" data-workflow-counts-retry data-i18n-text="settings.workflow.counts.retry">Retry</button>
        </div>`
        : '';
    const deleteCell = (lane) => {
        if (lane.isDone) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Done lane cannot be deleted" data-i18n-title="settings.workflow.deleteTitle.done" data-i18n-text="settings.workflow.deleteAction">Delete</button>`;
        }
        if (!canDeleteAnyLane) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Workflow must keep at least 2 lanes" data-i18n-title="settings.workflow.deleteTitle.minLanes" data-i18n-text="settings.workflow.deleteAction">Delete</button>`;
        }
        if (countsState.status === 'loading') {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Checking lane occupancy…" data-i18n-title="settings.workflow.deleteTitle.checking" data-i18n-text="settings.workflow.deleteAction">Delete</button>`;
        }
        if (countsState.status === 'error') {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Could not verify lane is empty" data-i18n-title="settings.workflow.deleteTitle.countsError" data-i18n-text="settings.workflow.deleteAction">Delete</button>`;
        }
        const n = countsState.counts[lane.key] ?? 0;
        if (n > 0) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Lane must be empty to delete" aria-label="Lane must be empty to delete" data-i18n-title="settings.workflow.deleteTitle.notEmpty" data-i18n-aria-label="settings.workflow.deleteAriaLabel.notEmpty" data-i18n-text="settings.workflow.deleteAction">Delete</button>`;
        }
        return `<button class="btn btn--danger btn--small" type="button" data-workflow-delete="${escapeHTML(lane.key)}" data-i18n-text="settings.workflow.deleteAction">Delete</button>`;
    };
    const saveDisabled = !isWorkflowDraftDirty();
    return `
    <div class="settings-section">
      <div class="settings-section__title" data-i18n-text="settings.workflow.title">Workflow</div>
      <div class="settings-section__description muted" data-i18n-text="settings.workflow.description">Edit lane labels and colors, then save. New lanes are inserted before the done lane. Keys stay immutable.</div>
      ${loadingBanner}
      ${errorBanner}
      <div class="settings-workflow-list">
        ${workflow
        .map((lane) => {
        const inputColor = normalizeWorkflowLaneColorForInput(lane.color);
        return `
          <div class="settings-workflow-row" data-workflow-key="${escapeHTML(lane.key)}" style="display:flex; gap:8px; align-items:center; margin-bottom:8px; flex-wrap:wrap; padding-left:4px;">
            <input
              class="input"
              data-workflow-name="${escapeHTML(lane.key)}"
              value="${escapeHTML(lane.name)}"
              maxlength="200"
              aria-label="${escapeHTML(workflowLaneLabelAria(lane.key))}"
              style="flex:1; min-width:120px;"
            />
            <input
              type="color"
              class="settings-color-picker"
              data-workflow-color="${escapeHTML(lane.key)}"
              value="${escapeHTML(inputColor)}"
              aria-label="${escapeHTML(workflowLaneColorAria(lane.key))}"
              title="Lane color"
              data-i18n-title="settings.workflow.laneColorTitle"
            />
            ${deleteCell(lane)}
          </div>
        `;
    })
        .join('')}
      </div>
      <div class="settings-workflow-add-row" style="display:flex; gap:8px; align-items:center; margin-top:12px;">
        <input
          class="input"
          type="text"
          data-workflow-ghost-input
          maxlength="200"
          placeholder="Add lane..."
          data-i18n-placeholder="settings.workflow.addPlaceholder"
          aria-label="${escapeHTML(t('settings.workflow.addLaneAria'))}"
          data-i18n-aria-label="settings.workflow.addLaneAria"
          style="flex:1; min-width:0;"
          ${titleAttr(FIELD_TOOLTIPS.workflowAddLane)}
        />
        <button type="button" class="btn btn--small" data-workflow-add data-i18n-text="settings.workflow.add">Add</button>
      </div>
      <div class="settings-workflow-footer">
        <button type="button" class="btn btn--ghost" data-workflow-draft-cancel data-i18n-text="settings.workflow.cancel">Cancel</button>
        <button type="button" class="btn" data-workflow-save-changes ${saveDisabled ? 'disabled' : ''} data-i18n-text="settings.workflow.save">Save Changes</button>
      </div>
    </div>
  `;
}
async function addWorkflowLane(name, rerender) {
    const slug = getSlug();
    if (!slug) {
        showToast(t('settings.workflow.toast.noProject'));
        return;
    }
    const trimmed = name.trim();
    if (!trimmed) {
        showToast(t('settings.workflow.toast.nameRequired'));
        return;
    }
    try {
        recordLocalMutation();
        await apiFetch(`/api/board/${slug}/workflow`, {
            method: 'POST',
            body: JSON.stringify({ name: trimmed }),
        });
        invalidateWorkflowLaneCountsCache();
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl());
        syncWorkflowDraftFromBoardAfterMutation();
        await rerender();
        showToast(t('settings.workflow.toast.laneAdded'));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.workflow.toast.addFailed' }));
    }
}
async function saveWorkflowDraftChanges(rerender) {
    const slug = getSlug();
    if (!slug || !workflowTabDraft || !workflowTabDraftBaseline)
        return;
    for (const lane of workflowTabDraft) {
        if (!lane.name.trim()) {
            showToast(t('settings.workflow.toast.nameRequired'));
            return;
        }
    }
    const baselineByKey = new Map(workflowTabDraftBaseline.map((lane) => [lane.key, lane]));
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
                method: 'PATCH',
                body: JSON.stringify({ name, color }),
            });
        }
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl());
        syncWorkflowDraftFromBoardAfterMutation();
        await rerender();
        showToast(t('settings.workflow.toast.updated'));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.workflow.toast.updateFailed' }));
    }
}
async function deleteWorkflowLane(key, rerender) {
    const slug = getSlug();
    if (!slug) {
        showToast(t('settings.workflow.toast.noProject'));
        return;
    }
    const lane = getBoard()?.columnOrder?.find((item) => item.key === key);
    if (!lane) {
        showToast(t('settings.workflow.toast.laneNotFound'));
        return;
    }
    if (lane.isDone) {
        showToast(t('settings.workflow.toast.doneCannotDelete'));
        return;
    }
    const confirmed = await showConfirmDialog(t('settings.workflow.deleteConfirm.message', { name: lane.name }), t('settings.workflow.deleteConfirm.title'), t('settings.workflow.deleteConfirm.confirm'));
    if (!confirmed)
        return;
    try {
        recordLocalMutation();
        await apiFetch(`/api/board/${slug}/workflow/${encodeURIComponent(key)}`, {
            method: 'DELETE',
        });
        invalidateWorkflowLaneCountsCache();
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl());
        syncWorkflowDraftFromBoardAfterMutation();
        await rerender();
        showToast(t('settings.workflow.toast.laneDeleted'));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.workflow.toast.deleteFailed' }));
    }
}
export function syncWorkflowLocaleState(root) {
    root.querySelectorAll('[data-workflow-name]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-workflow-name');
        if (!key)
            return;
        inputEl.setAttribute('aria-label', workflowLaneLabelAria(key));
    });
    root.querySelectorAll('[data-workflow-color]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-workflow-color');
        if (!key)
            return;
        inputEl.setAttribute('aria-label', workflowLaneColorAria(key));
    });
}
export function loadWorkflowTabContent(options) {
    if (workflowLaneCountsCache && workflowLaneCountsCache.slug !== options.slug) {
        invalidateWorkflowLaneCountsCache();
    }
    const cached = workflowLaneCountsCache?.slug === options.slug ? workflowLaneCountsCache.state : null;
    if (cached !== null) {
        return renderWorkflowTabContent(cached);
    }
    const generation = workflowLaneCountsFetchGeneration;
    void (async () => {
        const state = await fetchWorkflowLaneCountsState(options.slug);
        if (generation !== workflowLaneCountsFetchGeneration)
            return;
        if (getSlug() !== options.slug)
            return;
        workflowLaneCountsCache = { slug: options.slug, state };
        if (getSettingsActiveTab() !== 'workflow')
            return;
        await options.rerender();
    })();
    return renderWorkflowTabContent({ status: 'loading' });
}
export function bindWorkflowTabInteractions(options) {
    const { closeSettingsBtn, rerender, settingsDialog, signal } = options;
    const addInput = document.querySelector('[data-workflow-ghost-input]');
    const addLane = () => {
        if (!addInput)
            return;
        void addWorkflowLane(addInput.value, rerender);
    };
    const addBtn = document.querySelector('[data-workflow-add]');
    if (addBtn) {
        addBtn.addEventListener('click', addLane, { signal });
    }
    if (addInput) {
        addInput.addEventListener('keydown', (e) => {
            if (e.key !== 'Enter')
                return;
            e.preventDefault();
            addLane();
        }, { signal });
    }
    document.querySelectorAll('[data-workflow-name]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-workflow-name');
        if (!key)
            return;
        inputEl.addEventListener('input', () => {
            const lane = workflowTabDraft?.find((item) => item.key === key);
            if (lane)
                lane.name = inputEl.value;
            updateWorkflowSaveFooter();
        }, { signal });
    });
    document.querySelectorAll('[data-workflow-color]').forEach((colorEl) => {
        const key = colorEl.getAttribute('data-workflow-color');
        if (!key)
            return;
        colorEl.addEventListener('input', () => {
            const lane = workflowTabDraft?.find((item) => item.key === key);
            if (lane)
                lane.color = colorEl.value || DEFAULT_WORKFLOW_LANE_COLOR;
            updateWorkflowSaveFooter();
        }, { signal });
    });
    document.querySelectorAll('[data-workflow-delete]').forEach((btn) => {
        btn.addEventListener('click', () => {
            const key = btn.getAttribute('data-workflow-delete');
            if (!key)
                return;
            void deleteWorkflowLane(key, rerender);
        }, { signal });
    });
    const saveChangesBtn = document.querySelector('[data-workflow-save-changes]');
    if (saveChangesBtn) {
        saveChangesBtn.addEventListener('click', () => {
            void saveWorkflowDraftChanges(rerender);
        }, { signal });
    }
    const cancelDraftBtn = document.querySelector('[data-workflow-draft-cancel]');
    if (cancelDraftBtn) {
        cancelDraftBtn.addEventListener('click', () => {
            resetWorkflowDraftToBaseline();
            void rerender();
        }, { signal });
    }
    const retryCountsBtn = document.querySelector('[data-workflow-counts-retry]');
    if (retryCountsBtn) {
        retryCountsBtn.addEventListener('click', () => {
            invalidateWorkflowLaneCountsCache();
            void rerender();
        }, { signal });
    }
    if (settingsDialog) {
        const onDialogCancel = (e) => {
            if (!isWorkflowDraftDirty())
                return;
            e.preventDefault();
            void showConfirmDialog(t('settings.workflow.unsavedConfirm.message'), t('settings.workflow.unsavedConfirm.title'), t('settings.workflow.unsavedConfirm.confirm')).then((discard) => {
                if (discard) {
                    resetWorkflowDraftToBaseline();
                    clearWorkflowDraftState();
                    settingsDialog.close();
                }
            });
        };
        settingsDialog.addEventListener('cancel', onDialogCancel, { signal });
        settingsDialog.addEventListener('close', () => clearWorkflowDraftState(), { signal });
    }
    if (closeSettingsBtn) {
        const onCloseClick = (e) => {
            if (!isWorkflowDraftDirty())
                return;
            e.preventDefault();
            e.stopImmediatePropagation();
            void showConfirmDialog(t('settings.workflow.unsavedConfirm.message'), t('settings.workflow.unsavedConfirm.title'), t('settings.workflow.unsavedConfirm.confirm')).then((discard) => {
                if (discard) {
                    resetWorkflowDraftToBaseline();
                    clearWorkflowDraftState();
                    settingsDialog?.close();
                }
            });
        };
        closeSettingsBtn.addEventListener('click', onCloseClick, { capture: true, signal });
    }
}

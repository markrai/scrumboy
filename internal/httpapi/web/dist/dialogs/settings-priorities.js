import { apiFetch } from '../api.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { getAssigneeFromUrl, getSortFromUrl, getBoard, getSearch, getSettingsActiveTab, getSlug, getSprintIdFromUrl, getTag, } from '../state/selectors.js';
import { escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import { apiErrorMessageOrRaw, t } from '../i18n/index.js';
const DEFAULT_PRIORITY_TIER_COLOR = '#64748b';
let priorityTierCountsCache = null;
let priorityTierCountsFetchGeneration = 0;
let priorityTabDraft = null;
let priorityTabDraftBaseline = null;
let priorityTabDraftSlug = null;
function priorityTierLabelAria(key) {
    return t('settings.priorities.tierLabelAria', { key });
}
function priorityTierColorAria(key) {
    return t('settings.priorities.tierColorAria', { key });
}
function normalizePriorityTierColorForInput(color) {
    const s = color?.trim();
    return s && /^#[0-9a-fA-F]{6}$/.test(s) ? s : DEFAULT_PRIORITY_TIER_COLOR;
}
function clonePriorityTiersFromBoard() {
    const tiers = getBoard()?.priorityOrder ?? [];
    return tiers.map((tier) => ({
        key: tier.key,
        name: tier.name,
        color: normalizePriorityTierColorForInput(tier.color),
    }));
}
function ensurePriorityDraftInitialized() {
    const slug = getSlug();
    if (!slug)
        return;
    if (priorityTabDraftSlug !== slug || priorityTabDraft === null || priorityTabDraftBaseline === null) {
        const tiers = clonePriorityTiersFromBoard();
        priorityTabDraft = tiers;
        priorityTabDraftBaseline = JSON.parse(JSON.stringify(tiers));
        priorityTabDraftSlug = slug;
    }
}
function syncPriorityDraftFromBoardAfterMutation() {
    const tiers = clonePriorityTiersFromBoard();
    priorityTabDraft = tiers;
    priorityTabDraftBaseline = JSON.parse(JSON.stringify(tiers));
    priorityTabDraftSlug = getSlug() ?? null;
}
export function resetPriorityDraftToBaseline() {
    if (priorityTabDraftBaseline && priorityTabDraftSlug === getSlug()) {
        priorityTabDraft = JSON.parse(JSON.stringify(priorityTabDraftBaseline));
    }
    else {
        ensurePriorityDraftInitialized();
    }
}
export function clearPriorityDraftState() {
    priorityTabDraft = null;
    priorityTabDraftBaseline = null;
    priorityTabDraftSlug = null;
}
export function isPriorityDraftDirty() {
    if (!priorityTabDraft || !priorityTabDraftBaseline)
        return false;
    if (priorityTabDraft.length !== priorityTabDraftBaseline.length)
        return true;
    for (let i = 0; i < priorityTabDraft.length; i++) {
        const a = priorityTabDraft[i];
        const b = priorityTabDraftBaseline[i];
        if (a.key !== b.key)
            return true;
        if (a.name.trim() !== b.name.trim())
            return true;
        if (a.color.trim().toLowerCase() !== b.color.trim().toLowerCase())
            return true;
    }
    return false;
}
function updatePrioritySaveFooter() {
    const btn = document.querySelector('[data-priority-save-changes]');
    if (btn)
        btn.disabled = !isPriorityDraftDirty();
}
export function invalidatePriorityTierCountsCache() {
    priorityTierCountsCache = null;
    priorityTierCountsFetchGeneration++;
}
async function fetchPriorityTierCountsState(slug) {
    try {
        const res = await apiFetch(`/api/board/${encodeURIComponent(slug)}/priorities/counts`);
        if (!res || typeof res.countsByPriorityKey !== 'object' || res.countsByPriorityKey === null) {
            return { status: 'error' };
        }
        return { status: 'ok', counts: res.countsByPriorityKey };
    }
    catch {
        return { status: 'error' };
    }
}
function renderPriorityTabContent(countsState) {
    const board = getBoard();
    const tiers = board?.priorityOrder ?? [];
    if (!getSlug()) {
        return `<div class="settings-section"><div class="muted" data-i18n-text="settings.priorities.error.noProject">${escapeHTML(t('settings.priorities.error.noProject'))}</div></div>`;
    }
    if (tiers.length === 0) {
        return `<div class="settings-section"><div class="muted" data-i18n-text="settings.priorities.error.tiersUnavailable">${escapeHTML(t('settings.priorities.error.tiersUnavailable'))}</div></div>`;
    }
    ensurePriorityDraftInitialized();
    const draft = priorityTabDraft ?? [];
    const canDeleteAnyTier = draft.length > 1;
    const loadingBanner = countsState.status === 'loading'
        ? `<div class="muted settings-priorities-counts-banner" style="margin-bottom:10px;" data-i18n-text="settings.priorities.counts.loading">Checking priority usage…</div>`
        : '';
    const errorBanner = countsState.status === 'error'
        ? `<div class="settings-priorities-counts-banner settings-priorities-counts-banner--error muted" style="margin-bottom:10px;display:flex;flex-wrap:wrap;align-items:center;gap:8px;">
          <span data-i18n-text="settings.priorities.counts.error">Could not load priority usage. Delete stays disabled until this succeeds.</span>
          <button type="button" class="btn btn--ghost btn--small" data-priority-counts-retry data-i18n-text="settings.priorities.counts.retry">Retry</button>
        </div>`
        : '';
    const deleteCell = (tier) => {
        if (!canDeleteAnyTier) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Project must keep at least 1 priority tier" data-i18n-title="settings.priorities.deleteTitle.minTiers" data-i18n-text="settings.priorities.deleteAction">Delete</button>`;
        }
        if (countsState.status === 'loading') {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Checking priority usage…" data-i18n-title="settings.priorities.deleteTitle.checking" data-i18n-text="settings.priorities.deleteAction">Delete</button>`;
        }
        if (countsState.status === 'error') {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Could not verify tier is empty" data-i18n-title="settings.priorities.deleteTitle.countsError" data-i18n-text="settings.priorities.deleteAction">Delete</button>`;
        }
        const n = countsState.counts[tier.key] ?? 0;
        if (n > 0) {
            return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Tier must be empty to delete" aria-label="Tier must be empty to delete" data-i18n-title="settings.priorities.deleteTitle.notEmpty" data-i18n-aria-label="settings.priorities.deleteAriaLabel.notEmpty" data-i18n-text="settings.priorities.deleteAction">Delete</button>`;
        }
        return `<button class="btn btn--danger btn--small" type="button" data-priority-delete="${escapeHTML(tier.key)}" data-i18n-text="settings.priorities.deleteAction">Delete</button>`;
    };
    const saveDisabled = !isPriorityDraftDirty();
    return `
    <div class="settings-section">
      <div class="settings-section__title" data-i18n-text="settings.priorities.title">Priorities</div>
      <div class="settings-section__description muted" data-i18n-text="settings.priorities.description">Edit priority tier labels and colors, then save. New tiers are appended at the end. Keys stay immutable.</div>
      ${loadingBanner}
      ${errorBanner}
      <div class="settings-priorities-list">
        ${draft
        .map((tier) => {
        const inputColor = normalizePriorityTierColorForInput(tier.color);
        return `
          <div class="settings-priorities-row" data-priority-key="${escapeHTML(tier.key)}" style="display:flex; gap:8px; align-items:center; margin-bottom:8px; flex-wrap:wrap; padding-left:4px;">
            <input
              class="input"
              data-priority-name="${escapeHTML(tier.key)}"
              value="${escapeHTML(tier.name)}"
              maxlength="200"
              aria-label="${escapeHTML(priorityTierLabelAria(tier.key))}"
              style="flex:1; min-width:120px;"
            />
            <input
              type="color"
              class="settings-color-picker"
              data-priority-color="${escapeHTML(tier.key)}"
              value="${escapeHTML(inputColor)}"
              aria-label="${escapeHTML(priorityTierColorAria(tier.key))}"
              title="Priority tier color"
              data-i18n-title="settings.priorities.tierColorTitle"
            />
            ${deleteCell(tier)}
          </div>
        `;
    })
        .join('')}
      </div>
      <div class="settings-priorities-add-row" style="display:flex; gap:8px; align-items:center; margin-top:12px;">
        <input
          class="input"
          type="text"
          data-priority-ghost-input
          maxlength="200"
          placeholder="Add priority tier..."
          data-i18n-placeholder="settings.priorities.addPlaceholder"
          aria-label="${escapeHTML(t('settings.priorities.addTierAria'))}"
          data-i18n-aria-label="settings.priorities.addTierAria"
          style="flex:1; min-width:0;"
          ${titleAttr(FIELD_TOOLTIPS.priorityAddTier)}
        />
        <button type="button" class="btn btn--small" data-priority-add data-i18n-text="settings.priorities.add">Add</button>
      </div>
      <div class="settings-priorities-footer">
        <button type="button" class="btn btn--ghost" data-priority-draft-cancel data-i18n-text="settings.priorities.cancel">Cancel</button>
        <button type="button" class="btn" data-priority-save-changes ${saveDisabled ? 'disabled' : ''} data-i18n-text="settings.priorities.save">Save Changes</button>
      </div>
    </div>
  `;
}
async function addPriorityTier(name, rerender) {
    const slug = getSlug();
    if (!slug) {
        showToast(t('settings.priorities.toast.noProject'));
        return;
    }
    const trimmed = name.trim();
    if (!trimmed) {
        showToast(t('settings.priorities.toast.nameRequired'));
        return;
    }
    try {
        recordLocalMutation();
        await apiFetch(`/api/board/${slug}/priorities`, {
            method: 'POST',
            body: JSON.stringify({ name: trimmed }),
        });
        invalidatePriorityTierCountsCache();
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl());
        syncPriorityDraftFromBoardAfterMutation();
        await rerender();
        showToast(t('settings.priorities.toast.tierAdded'));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.priorities.toast.addFailed' }));
    }
}
async function savePriorityDraftChanges(rerender) {
    const slug = getSlug();
    if (!slug || !priorityTabDraft || !priorityTabDraftBaseline)
        return;
    for (const tier of priorityTabDraft) {
        if (!tier.name.trim()) {
            showToast(t('settings.priorities.toast.nameRequired'));
            return;
        }
    }
    const baselineByKey = new Map(priorityTabDraftBaseline.map((tier) => [tier.key, tier]));
    try {
        for (const tier of priorityTabDraft) {
            const base = baselineByKey.get(tier.key);
            if (!base)
                continue;
            const name = tier.name.trim();
            const color = tier.color.trim();
            if (name === base.name.trim() && color.toLowerCase() === base.color.trim().toLowerCase()) {
                continue;
            }
            recordLocalMutation();
            await apiFetch(`/api/board/${slug}/priorities/${encodeURIComponent(tier.key)}`, {
                method: 'PATCH',
                body: JSON.stringify({ name, color }),
            });
        }
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl());
        syncPriorityDraftFromBoardAfterMutation();
        await rerender();
        showToast(t('settings.priorities.toast.updated'));
    }
    catch (err) {
        const originalMessage = apiErrorMessageOrRaw(err, { fallbackKey: 'settings.priorities.toast.updateFailed' });
        invalidatePriorityTierCountsCache();
        try {
            await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), true);
            syncPriorityDraftFromBoardAfterMutation();
            await rerender();
            showToast(originalMessage);
        }
        catch {
            clearPriorityDraftState();
            showToast(originalMessage);
            showToast(t('settings.priorities.toast.reloadRequired'));
        }
    }
}
async function deletePriorityTier(key, rerender) {
    const slug = getSlug();
    if (!slug) {
        showToast(t('settings.priorities.toast.noProject'));
        return;
    }
    const tier = getBoard()?.priorityOrder?.find((item) => item.key === key);
    if (!tier) {
        showToast(t('settings.priorities.toast.tierNotFound'));
        return;
    }
    const confirmed = await showConfirmDialog(t('settings.priorities.deleteConfirm.message', { name: tier.name }), t('settings.priorities.deleteConfirm.title'), t('settings.priorities.deleteConfirm.confirm'));
    if (!confirmed)
        return;
    try {
        recordLocalMutation();
        await apiFetch(`/api/board/${slug}/priorities/${encodeURIComponent(key)}`, {
            method: 'DELETE',
        });
        invalidatePriorityTierCountsCache();
        await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl());
        syncPriorityDraftFromBoardAfterMutation();
        await rerender();
        showToast(t('settings.priorities.toast.tierDeleted'));
    }
    catch (err) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.priorities.toast.deleteFailed' }));
    }
}
export function syncPriorityLocaleState(root) {
    root.querySelectorAll('[data-priority-name]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-priority-name');
        if (!key)
            return;
        inputEl.setAttribute('aria-label', priorityTierLabelAria(key));
    });
    root.querySelectorAll('[data-priority-color]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-priority-color');
        if (!key)
            return;
        inputEl.setAttribute('aria-label', priorityTierColorAria(key));
    });
}
export function loadPriorityTabContent(options) {
    if (priorityTierCountsCache && priorityTierCountsCache.slug !== options.slug) {
        invalidatePriorityTierCountsCache();
    }
    const cached = priorityTierCountsCache?.slug === options.slug ? priorityTierCountsCache.state : null;
    if (cached !== null) {
        return renderPriorityTabContent(cached);
    }
    const generation = priorityTierCountsFetchGeneration;
    void (async () => {
        const state = await fetchPriorityTierCountsState(options.slug);
        if (generation !== priorityTierCountsFetchGeneration)
            return;
        if (getSlug() !== options.slug)
            return;
        priorityTierCountsCache = { slug: options.slug, state };
        if (getSettingsActiveTab() !== 'priorities')
            return;
        await options.rerender();
    })();
    return renderPriorityTabContent({ status: 'loading' });
}
export function bindPriorityTabInteractions(options) {
    const { closeSettingsBtn, rerender, settingsDialog, signal } = options;
    const addInput = document.querySelector('[data-priority-ghost-input]');
    const addTier = () => {
        if (!addInput)
            return;
        void addPriorityTier(addInput.value, rerender);
    };
    const addBtn = document.querySelector('[data-priority-add]');
    if (addBtn) {
        addBtn.addEventListener('click', addTier, { signal });
    }
    if (addInput) {
        addInput.addEventListener('keydown', (e) => {
            if (e.key !== 'Enter')
                return;
            e.preventDefault();
            addTier();
        }, { signal });
    }
    document.querySelectorAll('[data-priority-name]').forEach((inputEl) => {
        const key = inputEl.getAttribute('data-priority-name');
        if (!key)
            return;
        inputEl.addEventListener('input', () => {
            const tier = priorityTabDraft?.find((item) => item.key === key);
            if (tier)
                tier.name = inputEl.value;
            updatePrioritySaveFooter();
        }, { signal });
    });
    document.querySelectorAll('[data-priority-color]').forEach((colorEl) => {
        const key = colorEl.getAttribute('data-priority-color');
        if (!key)
            return;
        colorEl.addEventListener('input', () => {
            const tier = priorityTabDraft?.find((item) => item.key === key);
            if (tier)
                tier.color = colorEl.value || DEFAULT_PRIORITY_TIER_COLOR;
            updatePrioritySaveFooter();
        }, { signal });
    });
    document.querySelectorAll('[data-priority-delete]').forEach((btn) => {
        btn.addEventListener('click', () => {
            const key = btn.getAttribute('data-priority-delete');
            if (!key)
                return;
            void deletePriorityTier(key, rerender);
        }, { signal });
    });
    const saveChangesBtn = document.querySelector('[data-priority-save-changes]');
    if (saveChangesBtn) {
        saveChangesBtn.addEventListener('click', () => {
            void savePriorityDraftChanges(rerender);
        }, { signal });
    }
    const cancelDraftBtn = document.querySelector('[data-priority-draft-cancel]');
    if (cancelDraftBtn) {
        cancelDraftBtn.addEventListener('click', () => {
            resetPriorityDraftToBaseline();
            void rerender();
        }, { signal });
    }
    const retryCountsBtn = document.querySelector('[data-priority-counts-retry]');
    if (retryCountsBtn) {
        retryCountsBtn.addEventListener('click', () => {
            invalidatePriorityTierCountsCache();
            void rerender();
        }, { signal });
    }
    if (settingsDialog) {
        const onDialogCancel = (e) => {
            if (!isPriorityDraftDirty())
                return;
            e.preventDefault();
            void showConfirmDialog(t('settings.priorities.unsavedConfirm.message'), t('settings.priorities.unsavedConfirm.title'), t('settings.priorities.unsavedConfirm.confirm')).then((discard) => {
                if (discard) {
                    resetPriorityDraftToBaseline();
                    clearPriorityDraftState();
                    settingsDialog.close();
                }
            });
        };
        settingsDialog.addEventListener('cancel', onDialogCancel, { signal });
        settingsDialog.addEventListener('close', () => clearPriorityDraftState(), { signal });
    }
    if (closeSettingsBtn) {
        const onCloseClick = (e) => {
            if (!isPriorityDraftDirty())
                return;
            e.preventDefault();
            e.stopImmediatePropagation();
            void showConfirmDialog(t('settings.priorities.unsavedConfirm.message'), t('settings.priorities.unsavedConfirm.title'), t('settings.priorities.unsavedConfirm.confirm')).then((discard) => {
                if (discard) {
                    resetPriorityDraftToBaseline();
                    clearPriorityDraftState();
                    settingsDialog?.close();
                }
            });
        };
        closeSettingsBtn.addEventListener('click', onCloseClick, { capture: true, signal });
    }
}

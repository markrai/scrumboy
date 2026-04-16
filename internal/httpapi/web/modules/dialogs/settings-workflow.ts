import { apiFetch } from '../api.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { recordLocalMutation } from '../realtime/guard.js';
import {
  getBoard,
  getSearch,
  getSettingsActiveTab,
  getSlug,
  getSprintIdFromUrl,
  getTag,
} from '../state/selectors.js';
import { escapeHTML, showConfirmDialog, showToast } from '../utils.js';

type WorkflowLaneCountsState =
  | { status: 'loading' }
  | { status: 'ok'; counts: Record<string, number> }
  | { status: 'error' };

type WorkflowLaneDraft = { key: string; name: string; color: string; isDone: boolean };

type RerenderFn = () => Promise<void>;

type BindWorkflowTabInteractionsOptions = {
  signal: AbortSignal;
  settingsDialog: HTMLDialogElement | null;
  closeSettingsBtn: HTMLElement | null;
  rerender: RerenderFn;
};

const DEFAULT_WORKFLOW_LANE_COLOR = '#64748b';

let workflowLaneCountsCache: {
  slug: string;
  state: Exclude<WorkflowLaneCountsState, { status: 'loading' }>;
} | null = null;
let workflowLaneCountsFetchGeneration = 0;

let workflowTabDraft: WorkflowLaneDraft[] | null = null;
let workflowTabDraftBaseline: WorkflowLaneDraft[] | null = null;
let workflowTabDraftSlug: string | null = null;

function normalizeWorkflowLaneColorForInput(color: string | undefined | null): string {
  const s = color?.trim();
  return s && /^#[0-9a-fA-F]{6}$/.test(s) ? s : DEFAULT_WORKFLOW_LANE_COLOR;
}

function cloneWorkflowLanesFromBoard(): WorkflowLaneDraft[] {
  const workflow = getBoard()?.columnOrder ?? [];
  return workflow.map((lane) => ({
    key: lane.key,
    name: lane.name,
    color: normalizeWorkflowLaneColorForInput(lane.color),
    isDone: !!lane.isDone,
  }));
}

function ensureWorkflowDraftInitialized(): void {
  const slug = getSlug();
  if (!slug) return;
  if (workflowTabDraftSlug !== slug || workflowTabDraft === null || workflowTabDraftBaseline === null) {
    const lanes = cloneWorkflowLanesFromBoard();
    workflowTabDraft = lanes;
    workflowTabDraftBaseline = JSON.parse(JSON.stringify(lanes)) as WorkflowLaneDraft[];
    workflowTabDraftSlug = slug;
  }
}

function syncWorkflowDraftFromBoardAfterMutation(): void {
  const lanes = cloneWorkflowLanesFromBoard();
  workflowTabDraft = lanes;
  workflowTabDraftBaseline = JSON.parse(JSON.stringify(lanes)) as WorkflowLaneDraft[];
  workflowTabDraftSlug = getSlug() ?? null;
}

export function resetWorkflowDraftToBaseline(): void {
  if (workflowTabDraftBaseline && workflowTabDraftSlug === getSlug()) {
    workflowTabDraft = JSON.parse(JSON.stringify(workflowTabDraftBaseline)) as WorkflowLaneDraft[];
  } else {
    ensureWorkflowDraftInitialized();
  }
}

export function clearWorkflowDraftState(): void {
  workflowTabDraft = null;
  workflowTabDraftBaseline = null;
  workflowTabDraftSlug = null;
}

export function isWorkflowDraftDirty(): boolean {
  if (!workflowTabDraft || !workflowTabDraftBaseline) return false;
  if (workflowTabDraft.length !== workflowTabDraftBaseline.length) return true;
  for (let i = 0; i < workflowTabDraft.length; i++) {
    const a = workflowTabDraft[i];
    const b = workflowTabDraftBaseline[i];
    if (a.key !== b.key) return true;
    if (a.name.trim() !== b.name.trim()) return true;
    if (a.color.trim().toLowerCase() !== b.color.trim().toLowerCase()) return true;
  }
  return false;
}

function updateWorkflowSaveFooter(): void {
  const btn = document.querySelector('[data-workflow-save-changes]') as HTMLButtonElement | null;
  if (btn) btn.disabled = !isWorkflowDraftDirty();
}

export function invalidateWorkflowLaneCountsCache(): void {
  workflowLaneCountsCache = null;
  workflowLaneCountsFetchGeneration++;
}

async function fetchWorkflowLaneCountsState(
  slug: string
): Promise<Exclude<WorkflowLaneCountsState, { status: 'loading' }>> {
  try {
    const res = await apiFetch<{ countsByColumnKey?: Record<string, number> }>(
      `/api/board/${encodeURIComponent(slug)}/workflow/counts`
    );
    if (!res || typeof res.countsByColumnKey !== 'object' || res.countsByColumnKey === null) {
      return { status: 'error' };
    }
    return { status: 'ok', counts: res.countsByColumnKey };
  } catch {
    return { status: 'error' };
  }
}

function renderWorkflowTabContent(countsState: WorkflowLaneCountsState): string {
  const board = getBoard();
  const columns = board?.columnOrder ?? [];
  if (!getSlug()) {
    return `<div class="settings-section"><div class="muted">No project in context.</div></div>`;
  }
  if (columns.length === 0) {
    return `<div class="settings-section"><div class="muted">Workflow lanes are unavailable.</div></div>`;
  }
  ensureWorkflowDraftInitialized();
  const workflow = workflowTabDraft ?? [];
  const canDeleteAnyLane = workflow.length > 2;

  const loadingBanner =
    countsState.status === 'loading'
      ? `<div class="muted settings-workflow-counts-banner" style="margin-bottom:10px;">Checking lane occupancy…</div>`
      : '';
  const errorBanner =
    countsState.status === 'error'
      ? `<div class="settings-workflow-counts-banner settings-workflow-counts-banner--error muted" style="margin-bottom:10px;display:flex;flex-wrap:wrap;align-items:center;gap:8px;">
          Could not load lane occupancy. Delete stays disabled until this succeeds.
          <button type="button" class="btn btn--ghost btn--small" data-workflow-counts-retry>Retry</button>
        </div>`
      : '';

  const deleteCell = (lane: { key: string; name: string; isDone: boolean; color?: string }) => {
    if (lane.isDone) {
      return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Done lane cannot be deleted">Delete</button>`;
    }
    if (!canDeleteAnyLane) {
      return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Workflow must keep at least 2 lanes">Delete</button>`;
    }
    if (countsState.status === 'loading') {
      return `<button class="btn btn--ghost btn--small" type="button" disabled aria-disabled="true" title="Checking lane occupancy…">Delete</button>`;
    }
    if (countsState.status === 'error') {
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
            const inputColor = normalizeWorkflowLaneColorForInput(lane.color);
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
              value="${escapeHTML(inputColor)}"
              aria-label="Lane color for ${escapeHTML(lane.key)}"
              title="Lane color"
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
          aria-label="Add lane"
          style="flex:1; min-width:0;"
        />
        <button type="button" class="btn btn--small" data-workflow-add>Add</button>
      </div>
      <div class="settings-workflow-footer">
        <button type="button" class="btn btn--ghost" data-workflow-draft-cancel>Cancel</button>
        <button type="button" class="btn" data-workflow-save-changes ${saveDisabled ? 'disabled' : ''}>Save Changes</button>
      </div>
    </div>
  `;
}

async function addWorkflowLane(name: string, rerender: RerenderFn): Promise<void> {
  const slug = getSlug();
  if (!slug) {
    showToast('No project available');
    return;
  }
  const trimmed = name.trim();
  if (!trimmed) {
    showToast('Lane name is required');
    return;
  }
  try {
    recordLocalMutation();
    await apiFetch(`/api/board/${slug}/workflow`, {
      method: 'POST',
      body: JSON.stringify({ name: trimmed }),
    });
    invalidateWorkflowLaneCountsCache();
    await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
    syncWorkflowDraftFromBoardAfterMutation();
    await rerender();
    showToast('Lane added');
  } catch (err: any) {
    showToast(err.message || 'Failed to add lane');
  }
}

async function saveWorkflowDraftChanges(rerender: RerenderFn): Promise<void> {
  const slug = getSlug();
  if (!slug || !workflowTabDraft || !workflowTabDraftBaseline) return;
  for (const lane of workflowTabDraft) {
    if (!lane.name.trim()) {
      showToast('Lane name is required');
      return;
    }
  }
  const baselineByKey = new Map(workflowTabDraftBaseline.map((lane) => [lane.key, lane]));
  try {
    for (const lane of workflowTabDraft) {
      const base = baselineByKey.get(lane.key);
      if (!base) continue;
      const name = lane.name.trim();
      const color = lane.color.trim();
      if (
        name === base.name.trim() &&
        color.toLowerCase() === base.color.trim().toLowerCase()
      ) {
        continue;
      }
      recordLocalMutation();
      await apiFetch(`/api/board/${slug}/workflow/${encodeURIComponent(lane.key)}`, {
        method: 'PATCH',
        body: JSON.stringify({ name, color }),
      });
    }
    await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
    syncWorkflowDraftFromBoardAfterMutation();
    await rerender();
    showToast('Workflow updated');
  } catch (err: any) {
    showToast(err.message || 'Failed to update workflow');
  }
}

async function deleteWorkflowLane(key: string, rerender: RerenderFn): Promise<void> {
  const slug = getSlug();
  if (!slug) {
    showToast('No project available');
    return;
  }
  const lane = getBoard()?.columnOrder?.find((item) => item.key === key);
  if (!lane) {
    showToast('Lane not found');
    return;
  }
  if (lane.isDone) {
    showToast('Done lane cannot be deleted');
    return;
  }
  const confirmed = await showConfirmDialog(
    `Delete lane "${lane.name}"? Only empty non-done lanes can be deleted.`,
    'Delete lane?',
    'Delete'
  );
  if (!confirmed) return;
  try {
    recordLocalMutation();
    await apiFetch(`/api/board/${slug}/workflow/${encodeURIComponent(key)}`, {
      method: 'DELETE',
    });
    invalidateWorkflowLaneCountsCache();
    await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
    syncWorkflowDraftFromBoardAfterMutation();
    await rerender();
    showToast('Lane deleted');
  } catch (err: any) {
    showToast(err.message || 'Failed to delete lane');
  }
}

export function loadWorkflowTabContent(options: {
  slug: string;
  rerender: RerenderFn;
}): string {
  if (workflowLaneCountsCache && workflowLaneCountsCache.slug !== options.slug) {
    invalidateWorkflowLaneCountsCache();
  }
  const cached =
    workflowLaneCountsCache?.slug === options.slug ? workflowLaneCountsCache.state : null;
  if (cached !== null) {
    return renderWorkflowTabContent(cached);
  }
  const generation = workflowLaneCountsFetchGeneration;
  void (async () => {
    const state = await fetchWorkflowLaneCountsState(options.slug);
    if (generation !== workflowLaneCountsFetchGeneration) return;
    if (getSlug() !== options.slug) return;
    workflowLaneCountsCache = { slug: options.slug, state };
    if (getSettingsActiveTab() !== 'workflow') return;
    await options.rerender();
  })();
  return renderWorkflowTabContent({ status: 'loading' });
}

export function bindWorkflowTabInteractions(options: BindWorkflowTabInteractionsOptions): void {
  const { closeSettingsBtn, rerender, settingsDialog, signal } = options;
  const addInput = document.querySelector('[data-workflow-ghost-input]') as HTMLInputElement | null;
  const addLane = () => {
    if (!addInput) return;
    void addWorkflowLane(addInput.value, rerender);
  };
  const addBtn = document.querySelector('[data-workflow-add]');
  if (addBtn) {
    addBtn.addEventListener('click', addLane, { signal });
  }
  if (addInput) {
    addInput.addEventListener(
      'keydown',
      (e) => {
        if ((e as KeyboardEvent).key !== 'Enter') return;
        e.preventDefault();
        addLane();
      },
      { signal }
    );
  }
  document.querySelectorAll('[data-workflow-name]').forEach((inputEl) => {
    const key = (inputEl as HTMLElement).getAttribute('data-workflow-name');
    if (!key) return;
    inputEl.addEventListener(
      'input',
      () => {
        const lane = workflowTabDraft?.find((item) => item.key === key);
        if (lane) lane.name = (inputEl as HTMLInputElement).value;
        updateWorkflowSaveFooter();
      },
      { signal }
    );
  });
  document.querySelectorAll('[data-workflow-color]').forEach((colorEl) => {
    const key = (colorEl as HTMLElement).getAttribute('data-workflow-color');
    if (!key) return;
    colorEl.addEventListener(
      'input',
      () => {
        const lane = workflowTabDraft?.find((item) => item.key === key);
        if (lane) lane.color = (colorEl as HTMLInputElement).value || DEFAULT_WORKFLOW_LANE_COLOR;
        updateWorkflowSaveFooter();
      },
      { signal }
    );
  });
  document.querySelectorAll('[data-workflow-delete]').forEach((btn) => {
    btn.addEventListener(
      'click',
      () => {
        const key = (btn as HTMLElement).getAttribute('data-workflow-delete');
        if (!key) return;
        void deleteWorkflowLane(key, rerender);
      },
      { signal }
    );
  });
  const saveChangesBtn = document.querySelector('[data-workflow-save-changes]');
  if (saveChangesBtn) {
    saveChangesBtn.addEventListener(
      'click',
      () => {
        void saveWorkflowDraftChanges(rerender);
      },
      { signal }
    );
  }
  const cancelDraftBtn = document.querySelector('[data-workflow-draft-cancel]');
  if (cancelDraftBtn) {
    cancelDraftBtn.addEventListener(
      'click',
      () => {
        resetWorkflowDraftToBaseline();
        void rerender();
      },
      { signal }
    );
  }
  const retryCountsBtn = document.querySelector('[data-workflow-counts-retry]');
  if (retryCountsBtn) {
    retryCountsBtn.addEventListener(
      'click',
      () => {
        invalidateWorkflowLaneCountsCache();
        void rerender();
      },
      { signal }
    );
  }

  if (settingsDialog) {
    const onDialogCancel = (e: Event) => {
      if (!isWorkflowDraftDirty()) return;
      e.preventDefault();
      void showConfirmDialog(
        'You have unsaved changes. Discard them?',
        'Unsaved changes',
        'Discard'
      ).then((discard) => {
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
    const onCloseClick = (e: Event) => {
      if (!isWorkflowDraftDirty()) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      void showConfirmDialog(
        'You have unsaved changes. Discard them?',
        'Unsaved changes',
        'Discard'
      ).then((discard) => {
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

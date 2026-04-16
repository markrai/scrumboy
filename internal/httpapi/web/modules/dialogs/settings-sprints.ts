import { apiFetch } from '../api.js';
import { emit } from '../events.js';
import { refreshSprintsAndChips } from '../orchestration/board-refresh.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { getBoard, getSlug } from '../state/selectors.js';
import { setBoard } from '../state/mutations.js';
import { normalizeSprints } from '../sprints.js';
import { escapeHTML, showConfirmDialog, showToast } from '../utils.js';

type BindSprintsTabInteractionsOptions = {
  signal: AbortSignal;
  rerender: () => Promise<void>;
  invalidateSprintChartsCache: () => void;
};

let editingSprintId: number | null = null;

function msToDateTimeLocalStr(ms: number): string {
  const d = new Date(ms);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  return `${y}-${m}-${day}T${hh}:${mm}`;
}

function computeDefaultSprintStart(now: Date): Date {
  const daysToMonday = (now.getDay() + 6) % 7;
  const monday = new Date(now.getTime());
  monday.setDate(monday.getDate() - daysToMonday);
  monday.setHours(9, 0, 0, 0);
  return monday;
}

function computeDefaultSprintEnd(start: Date, weeks: number): Date {
  const normalizedWeeks = weeks === 1 || weeks === 2 ? weeks : 2;
  const end = new Date(start.getTime());
  end.setDate(end.getDate() + (normalizedWeeks * 7 - 1));
  end.setHours(23, 59, 0, 0);
  return end;
}

export async function renderSprintsTabContent(): Promise<string> {
  const slug = getSlug();
  if (!slug) return "<div class='muted'>No project in context.</div>";
  try {
    const res = await apiFetch<{
      sprints?: {
        id: number;
        name: string;
        state: string;
        plannedStartAt: number;
        plannedEndAt: number;
        startedAt?: number;
        closedAt?: number;
        todoCount?: number;
      }[];
    } | null>(`/api/board/${slug}/sprints`);
    const sprints = normalizeSprints(res);
    const formatDate = (ms: number) =>
      new Date(ms).toLocaleString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
      });
    const listHTML =
      sprints.length === 0
        ? "<div class='muted'>No sprints yet. Create one above.</div>"
        : sprints
            .map((sp) => {
              const isEditing = editingSprintId === sp.id;
              const dateRange = `${formatDate(sp.plannedStartAt)} - ${formatDate(sp.plannedEndAt)}`;
              const stateBadge = `<span class="status-pill status-pill--${sp.state.toLowerCase()}">${sp.state}</span>`;
              const activateBtn =
                sp.state === 'PLANNED'
                  ? `<button class="btn btn--ghost btn--sm" data-sprint-activate="${sp.id}">Activate</button>`
                  : '';
              const closeBtn =
                sp.state === 'ACTIVE'
                  ? `<button class="btn btn--ghost btn--sm" data-sprint-close="${sp.id}">Close</button>`
                  : sp.state === 'CLOSED'
                    ? `<button type="button" class="btn btn--ghost btn--sm settings-sprint-row__action-placeholder" aria-hidden="true" tabindex="-1">Close</button>`
                    : '';
              const editBtn = `<button class="btn btn--ghost btn--sm" data-sprint-edit="${sp.id}">Edit</button>`;
              const deleteBtn = `<button class="btn btn--danger btn--sm" data-sprint-delete="${sp.id}">Delete</button>`;
              if (isEditing) {
                const editingClass = ' settings-sprint-row--editing';
                const todoCount = sp.todoCount ?? 0;
                const nameInput =
                  sp.state === 'PLANNED' || sp.state === 'CLOSED'
                    ? `<input class="input" data-sprint-edit-name value="${escapeHTML(sp.name)}" style="min-width: 120px;" />`
                    : `<strong>${escapeHTML(sp.name)}</strong>`;
                const startDisplay = `<span class="muted">${formatDate(sp.plannedStartAt)}</span>`;
                const startInput =
                  sp.state === 'PLANNED'
                    ? `<input class="input" type="datetime-local" data-sprint-edit-start value="${msToDateTimeLocalStr(sp.plannedStartAt)}" style="min-width: 180px;" />`
                    : startDisplay;
                const endDisplay = `<span class="muted">${formatDate(sp.plannedEndAt)}</span>`;
                const endInput =
                  sp.state === 'PLANNED' || sp.state === 'ACTIVE'
                    ? `<input class="input" type="datetime-local" data-sprint-edit-end value="${msToDateTimeLocalStr(sp.plannedEndAt)}" style="min-width: 180px;" />`
                    : endDisplay;
                const endBlock =
                  sp.state === 'ACTIVE'
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
            })
            .join('');
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
            <option value="1" ${defaultWeeks === 1 ? 'selected' : ''}>1</option>
            <option value="2" ${defaultWeeks === 2 ? 'selected' : ''}>2</option>
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
  } catch (err: any) {
    return `<div class='muted'>Error loading sprints: ${escapeHTML(err.message)}</div>`;
  }
}

export function bindSprintsTabInteractions(options: BindSprintsTabInteractionsOptions): void {
  const { invalidateSprintChartsCache, rerender, signal } = options;
  const defaultWeeksEl = document.getElementById('sprintDefaultWeeksSelect') as HTMLSelectElement | null;
  const startEl = document.getElementById('sprintStartInput') as HTMLInputElement | null;
  const endEl = document.getElementById('sprintEndInput') as HTMLInputElement | null;
  let userHasEditedEndDate = false;

  if (endEl) {
    const markEdited = () => {
      userHasEditedEndDate = true;
    };
    endEl.addEventListener('input', markEdited, { signal });
    endEl.addEventListener('change', markEdited, { signal });
  }

  if (defaultWeeksEl && endEl) {
    defaultWeeksEl.addEventListener(
      'change',
      () => {
        if (userHasEditedEndDate) return;
        const weeks = parseInt(defaultWeeksEl.value, 10);
        const start = startEl?.value ? new Date(startEl.value) : computeDefaultSprintStart(new Date());
        if (!Number.isFinite(start.getTime())) return;
        const computedEnd = computeDefaultSprintEnd(start, weeks);
        endEl.value = msToDateTimeLocalStr(computedEnd.getTime());
      },
      { signal }
    );
  }

  const createSprintBtn = document.getElementById('createSprintBtn');
  if (createSprintBtn) {
    createSprintBtn.addEventListener(
      'click',
      async () => {
        const slug = getSlug();
        if (!slug) return;
        const nameEl = document.getElementById('sprintNameInput') as HTMLInputElement;
        const name = nameEl?.value?.trim();
        const startStr = startEl?.value;
        const endStr = endEl?.value;
        if (!name) {
          showToast('Name is required');
          return;
        }
        if (!startStr || !endStr) {
          showToast('Start and end dates are required');
          return;
        }
        const plannedStartAt = new Date(startStr).getTime();
        const plannedEndAt = new Date(endStr).getTime();
        if (!Number.isFinite(plannedStartAt) || !Number.isFinite(plannedEndAt)) {
          showToast('Invalid start or end date');
          return;
        }
        if (plannedEndAt < plannedStartAt) {
          showToast('End date must be after start date');
          return;
        }
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${slug}/sprints`, {
            method: 'POST',
            body: JSON.stringify({ name, plannedStartAt, plannedEndAt }),
          });
          const selectedWeeks = parseInt(defaultWeeksEl?.value ?? '', 10);
          if (selectedWeeks === 1 || selectedWeeks === 2) {
            recordLocalMutation();
            apiFetch<{ defaultSprintWeeks: number }>(`/api/board/${slug}/settings`, {
              method: 'PATCH',
              body: JSON.stringify({ defaultSprintWeeks: selectedWeeks }),
            })
              .then((resp) => {
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
              })
              .catch(() => {
                // Best-effort settings persistence; ignore failures.
              });
          }
          showToast('Sprint created');
          invalidateSprintChartsCache();
          refreshSprintsAndChips(getSlug() ?? '').catch(() => {});
          await rerender();
        } catch (err: any) {
          showToast(err.message || 'Failed to create sprint');
        }
      },
      { signal }
    );
  }

  document.querySelectorAll('[data-sprint-activate]').forEach((btn) => {
    btn.addEventListener(
      'click',
      async (e) => {
        const target = e.target as HTMLElement;
        const sprintId = target.getAttribute('data-sprint-activate');
        const slug = getSlug();
        if (!sprintId || !slug) return;
        const row = target.closest('[data-sprint-id]') as HTMLElement | null;
        const plannedStartRaw = row?.getAttribute('data-sprint-planned-start-at') ?? '';
        const sprintName = row?.getAttribute('data-sprint-name') ?? 'Sprint';
        const plannedMs = parseInt(plannedStartRaw, 10);
        if (Number.isFinite(plannedMs) && Math.abs(plannedMs - Date.now()) > 60000) {
          const plannedLabel = new Date(plannedMs).toLocaleString(undefined, {
            month: 'short',
            day: 'numeric',
            year: 'numeric',
            hour: 'numeric',
            minute: '2-digit',
          });
          const confirmed = await showConfirmDialog(
            `${sprintName} will start now (activation time). Work completed after this moment will count. Planned start was ${plannedLabel}. Continue?`,
            'Start sprint now?',
            'Start Sprint'
          );
          if (!confirmed) return;
        }
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${slug}/sprints/${sprintId}/activate`, { method: 'POST' });
          showToast('Sprint activated');
          invalidateSprintChartsCache();
          emit('sprint-updated', { sprintId: parseInt(sprintId, 10), state: 'ACTIVE' });
          await rerender();
        } catch (err: any) {
          showToast(err.message || 'Failed to activate sprint');
        }
      },
      { signal }
    );
  });

  document.querySelectorAll('[data-sprint-close]').forEach((btn) => {
    btn.addEventListener(
      'click',
      async (e) => {
        const sprintId = (e.target as HTMLElement).getAttribute('data-sprint-close');
        const slug = getSlug();
        if (!sprintId || !slug) return;
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${slug}/sprints/${sprintId}/close`, { method: 'POST' });
          showToast('Sprint closed');
          invalidateSprintChartsCache();
          emit('sprint-updated', { sprintId: parseInt(sprintId, 10), state: 'CLOSED' });
          await rerender();
        } catch (err: any) {
          showToast(err.message || 'Failed to close sprint');
        }
      },
      { signal }
    );
  });

  document.querySelectorAll('[data-sprint-edit]').forEach((btn) => {
    btn.addEventListener(
      'click',
      (e) => {
        const sprintId = (e.target as HTMLElement).getAttribute('data-sprint-edit');
        if (!sprintId) return;
        editingSprintId = parseInt(sprintId, 10);
        void rerender();
      },
      { signal }
    );
  });

  document.querySelectorAll('[data-sprint-cancel]').forEach((btn) => {
    btn.addEventListener(
      'click',
      () => {
        editingSprintId = null;
        void rerender();
      },
      { signal }
    );
  });

  document.querySelectorAll('[data-sprint-save]').forEach((btn) => {
    btn.addEventListener(
      'click',
      async (e) => {
        const sprintId = (e.target as HTMLElement).getAttribute('data-sprint-save');
        const slug = getSlug();
        if (!sprintId || !slug) return;
        const row = document.querySelector(`[data-sprint-id="${sprintId}"].settings-sprint-row--editing`);
        if (!row) return;
        const state = row.getAttribute('data-sprint-state') ?? '';
        const body: { name?: string; plannedStartAt?: number; plannedEndAt?: number } = {};
        if (state === 'PLANNED' || state === 'CLOSED') {
          const nameEl = row.querySelector('[data-sprint-edit-name]') as HTMLInputElement;
          if (nameEl) body.name = nameEl.value.trim();
        }
        if (state === 'PLANNED') {
          const startEl = row.querySelector('[data-sprint-edit-start]') as HTMLInputElement;
          const endEl = row.querySelector('[data-sprint-edit-end]') as HTMLInputElement;
          if (startEl?.value && endEl?.value) {
            body.plannedStartAt = new Date(startEl.value).getTime();
            body.plannedEndAt = new Date(endEl.value).getTime();
          }
        }
        if (state === 'ACTIVE') {
          const endEl = row.querySelector('[data-sprint-edit-end]') as HTMLInputElement;
          if (endEl?.value) {
            body.plannedEndAt = new Date(endEl.value).getTime();
          }
        }
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${slug}/sprints/${sprintId}`, {
            method: 'PATCH',
            body: JSON.stringify(body),
          });
          showToast('Sprint updated');
          invalidateSprintChartsCache();
          editingSprintId = null;
          refreshSprintsAndChips(getSlug() ?? '').catch(() => {});
          await rerender();
        } catch (err: any) {
          showToast(err.message || 'Failed to update sprint');
        }
      },
      { signal }
    );
  });

  document.querySelectorAll('[data-sprint-delete]').forEach((btn) => {
    btn.addEventListener(
      'click',
      async (e) => {
        const sprintId = (e.target as HTMLElement).getAttribute('data-sprint-delete');
        const slug = getSlug();
        if (!sprintId || !slug) return;
        const row = document.querySelector(`[data-sprint-id="${sprintId}"]`);
        if (!row) return;
        const state = row.getAttribute('data-sprint-state') ?? '';
        const nameEl = row.querySelector('strong');
        const name = nameEl?.textContent ?? 'Sprint';
        const todoCount =
          parseInt((row as HTMLElement).getAttribute('data-sprint-todo-count') ?? '0', 10) || 0;
        const storyWord = todoCount === 1 ? 'story' : 'stories';
        let message: string;
        const title = 'Delete sprint?';
        if (state === 'ACTIVE') {
          message = `This sprint is currently active. Deleting it will immediately end the sprint and move ${todoCount} ${storyWord} back to backlog.`;
        } else if (todoCount === 0) {
          message = `Sprint '${name}' will be permanently deleted.`;
        } else {
          message = `Sprint '${name}' has ${todoCount} ${storyWord}. They will be moved to backlog (unassigned from this sprint). The sprint will be permanently deleted.`;
        }
        const confirmed = await showConfirmDialog(message, title, 'Delete');
        if (!confirmed) return;
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${slug}/sprints/${sprintId}`, { method: 'DELETE' });
          showToast('Sprint deleted');
          invalidateSprintChartsCache();
          editingSprintId = null;
          refreshSprintsAndChips(getSlug() ?? '').catch(() => {});
          await rerender();
        } catch (err: any) {
          showToast(err.message || 'Failed to delete sprint');
        }
      },
      { signal }
    );
  });
}

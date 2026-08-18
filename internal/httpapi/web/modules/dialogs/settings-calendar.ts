import { apiFetch } from '../api.js';
import { getBoard, getSlug } from '../state/selectors.js';
import { confirmDelete, escapeHTML, sanitizeHexColor, showToast } from '../utils.js';
import { apiErrorMessageOrRaw, t } from '../i18n/index.js';
import {
  getAgendaStartOfDayPreference,
  saveAgendaStartOfDayPreference,
} from '../core/agenda-start-of-day-preferences.js';
import {
  AGENDA_NOW_LINE_PROMINENT,
  AGENDA_NOW_LINE_SUBTLE,
  isAgendaNowLineProminent,
  saveAgendaNowLinePreference,
} from '../core/agenda-now-line-preferences.js';
import { AGENDA_DEFAULT_LANE_COLOR, applyAgendaNowLineAppearance, syncOpenBoardAgendaLayout } from '../views/board-agenda.js';

export type CalendarSourceDTO = {
  id: number;
  name: string;
  type: string;
  enabled: boolean;
  urlConfigured: boolean;
  urlPreview: string;
};

export type CalendarSourcesResponse = {
  agendaEnabled: boolean;
  agendaTimezone: string;
  agendaTitle?: string;
  agendaColor?: string;
  sources: CalendarSourceDTO[];
};

type BindCalendarTabOptions = {
  signal: AbortSignal;
  rerender: () => Promise<void>;
};

export type LoadCalendarTabOptions = {
  canManageCalendar?: boolean;
};

const DEFAULT_AGENDA_TIMEZONE = 'UTC';

let cachedCalendar: CalendarSourcesResponse | null = null;

export function clearCalendarSettingsCache(): void {
  cachedCalendar = null;
}

export function resolveAgendaTimezone(raw: string | null | undefined): string {
  const timezone = typeof raw === 'string' ? raw.trim() : '';
  return timezone || DEFAULT_AGENDA_TIMEZONE;
}

export function resolveAgendaTitle(raw: string | null | undefined): string {
  const title = typeof raw === 'string' ? raw.trim() : '';
  return title || t('board.agenda.title');
}

export function resolveAgendaColor(raw: string | null | undefined): string {
  return sanitizeHexColor(raw, AGENDA_DEFAULT_LANE_COLOR) || AGENDA_DEFAULT_LANE_COLOR;
}

export function listAgendaTimezones(savedTimezone: string): string[] {
  const zones = new Set<string>([DEFAULT_AGENDA_TIMEZONE]);
  zones.add(resolveAgendaTimezone(savedTimezone));

  const intl = Intl as typeof Intl & {
    supportedValuesOf?: (key: string) => string[];
  };
  if (typeof intl.supportedValuesOf === 'function') {
    try {
      for (const zone of intl.supportedValuesOf('timeZone')) {
        if (typeof zone === 'string' && zone.trim()) zones.add(zone);
      }
    } catch {
      // Discovery is best-effort; UTC + saved + browser timezone still populate.
    }
  }

  try {
    const local = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (typeof local === 'string' && local.trim()) zones.add(local);
  } catch {
    // Ignore environments that cannot resolve a local timezone.
  }

  return [...zones].sort((a, b) => a.localeCompare(b));
}

function renderTimezoneOptions(savedTimezone: string): string {
  const selected = resolveAgendaTimezone(savedTimezone);
  return listAgendaTimezones(selected)
    .map((zone) => {
      const escaped = escapeHTML(zone);
      const selectedAttr = zone === selected ? ' selected' : '';
      return `<option value="${escaped}"${selectedAttr}>${escaped}</option>`;
    })
    .join('');
}

export async function loadCalendarTabContent(options: LoadCalendarTabOptions = {}): Promise<string> {
  const slug = getSlug();
  if (!slug) {
    return `<div class="muted" data-i18n-text="settings.calendar.error.noProject">Open a durable board to configure Agenda.</div>`;
  }
  const canManage = options.canManageCalendar !== false;
  if (!canManage) {
    return `${renderTimelinePreferenceHTML()}`;
  }
  try {
    cachedCalendar = await apiFetch<CalendarSourcesResponse>(`/api/board/${slug}/calendar-sources`);
  } catch (err: unknown) {
    return `<div class="muted">${escapeHTML(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.error.loadFailed' }))}</div>`;
  }
  return renderCalendarTabHTML(cachedCalendar);
}

function renderTimelinePreferenceHTML(): string {
  return `
    <div class="settings-section">
      <label class="field" for="agendaStartOfDay">
        <span class="field__label" data-i18n-text="settings.calendar.timeline.label">Start of day</span>
        <input class="input" type="time" id="agendaStartOfDay" value="${escapeHTML(getAgendaStartOfDayPreference())}" />
      </label>
      <p class="muted" data-i18n-text="settings.calendar.timeline.hint">Agenda initially scrolls here unless an earlier event exists.</p>
    </div>
    <div class="settings-section">
      <label class="field" style="display: flex; align-items: center; gap: 8px;">
        <input type="checkbox" id="agendaNowLineProminent" ${isAgendaNowLineProminent() ? 'checked' : ''} />
        <span data-i18n-text="settings.calendar.nowLine.label">Prominent now line</span>
      </label>
      <p class="muted" data-i18n-text="settings.calendar.nowLine.hint">Use a solid red current-time line instead of the quieter dotted line.</p>
    </div>`;
}

function renderCalendarTabHTML(data: CalendarSourcesResponse): string {
  const sources = data.sources ?? [];
  const listHTML =
    sources.length === 0
      ? `<div class="muted" data-i18n-text="settings.calendar.list.empty">No calendar feeds yet.</div>`
      : sources
          .map((source) => {
            return `
        <div class="settings-calendar-row" data-calendar-source-id="${source.id}">
          <div class="settings-calendar-row__info">
            <strong>${escapeHTML(source.name)}</strong>
            <span class="muted">${escapeHTML(source.urlPreview)}</span>
          </div>
          <label class="field" style="display: flex; align-items: center; gap: 8px;">
            <input type="checkbox" data-calendar-source-enabled ${source.enabled ? 'checked' : ''} />
            <span data-i18n-text="settings.calendar.source.enabled">Enabled</span>
          </label>
          <button class="btn btn--sm" type="button" data-calendar-source-refresh data-i18n-text="settings.calendar.source.refresh">Refresh</button>
          <button class="btn btn--danger btn--sm" type="button" data-calendar-source-delete data-i18n-text="settings.calendar.source.remove">Remove</button>
        </div>`;
          })
          .join('');

  return `
    <div class="settings-section">
      <label class="field" style="display: flex; align-items: center; gap: 8px;">
        <input type="checkbox" id="agendaEnabledToggle" ${data.agendaEnabled ? 'checked' : ''} />
        <span data-i18n-text="settings.calendar.enableToggle">Enable Agenda for this board</span>
      </label>
      <p class="muted" data-i18n-text="settings.calendar.enableHint">Today's events from ICS feeds appear in a read-only Agenda lane. All members see the same Agenda.</p>
    </div>
    <div class="settings-section">
      <div class="settings-agenda-name-row">
        <label class="field settings-agenda-name-field" for="agendaTitleInput">
          <span class="field__label" data-i18n-text="settings.calendar.title.label">Lane name</span>
          <input class="input" id="agendaTitleInput" autocomplete="off" maxlength="200" value="${escapeHTML(resolveAgendaTitle(data.agendaTitle))}" />
        </label>
        <label class="field settings-agenda-color-field" for="agendaColorInput">
          <span class="field__label" data-i18n-text="settings.calendar.color.label">Lane color</span>
          <input type="color" class="settings-color-picker" id="agendaColorInput" value="${escapeHTML(resolveAgendaColor(data.agendaColor))}" aria-label="${escapeHTML(t('settings.calendar.color.label'))}" />
        </label>
      </div>
      <p class="muted" data-i18n-text="settings.calendar.title.hint">Shown as the Agenda lane title for all members.</p>
      <p class="muted" data-i18n-text="settings.calendar.color.hint">Shown as the Agenda lane color for all members.</p>
    </div>
    <div class="settings-section">
      <label class="field" for="agendaTimezoneInput">
        <span class="field__label" data-i18n-text="settings.calendar.timezone.label">Board timezone</span>
        <select class="input" id="agendaTimezoneInput">${renderTimezoneOptions(data.agendaTimezone)}</select>
      </label>
      <p class="muted" data-i18n-text="settings.calendar.timezone.hint">Used for today's events. All members see Agenda in this timezone.</p>
    </div>
    ${renderTimelinePreferenceHTML()}
    <div class="settings-section">
      <h3 data-i18n-text="settings.calendar.add.title">Add ICS feed</h3>
      <label class="field">
        <span class="field__label" data-i18n-text="settings.calendar.add.name">Name</span>
        <input class="input" id="calendarSourceNameInput" autocomplete="off" />
      </label>
      <label class="field">
        <span class="field__label" data-i18n-text="settings.calendar.add.url">ICS URL</span>
        <input class="input" id="calendarSourceUrlInput" type="url" autocomplete="off" />
      </label>
      <p class="muted" data-i18n-text="settings.calendar.add.urlHint">Paste an HTTPS iCalendar URL. The full URL is stored encrypted and is never shown again.</p>
      <button class="btn btn--sm" type="button" id="calendarSourceAdd" data-i18n-text="settings.calendar.add.submit">Add feed</button>
    </div>
    <div class="settings-section">
      <h3 data-i18n-text="settings.calendar.list.title">Feeds</h3>
      ${listHTML}
    </div>`;
}

export function bindCalendarTabInteractions(options: BindCalendarTabOptions): void {
  const slug = getSlug();
  if (!slug) return;
  const { signal, rerender } = options;

  const startOfDayInput = document.getElementById('agendaStartOfDay') as HTMLInputElement | null;
  startOfDayInput?.addEventListener(
    'change',
    async () => {
      const previous = getAgendaStartOfDayPreference();
      const next = startOfDayInput.value;
      startOfDayInput.disabled = true;
      const saving = saveAgendaStartOfDayPreference(next);
      syncOpenBoardAgendaLayout({ forceAutoFocus: true });
      try {
        await saving;
      } catch (err: unknown) {
        startOfDayInput.value = previous;
        syncOpenBoardAgendaLayout({ forceAutoFocus: true });
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.timelineFailed' }));
      } finally {
        startOfDayInput.disabled = false;
      }
    },
    { signal },
  );

  const nowLineInput = document.getElementById('agendaNowLineProminent') as HTMLInputElement | null;
  nowLineInput?.addEventListener(
    'change',
    async () => {
      const previous = isAgendaNowLineProminent();
      const next = nowLineInput.checked ? AGENDA_NOW_LINE_PROMINENT : AGENDA_NOW_LINE_SUBTLE;
      nowLineInput.disabled = true;
      const saving = saveAgendaNowLinePreference(next);
      applyAgendaNowLineAppearance();
      try {
        await saving;
      } catch (err: unknown) {
        nowLineInput.checked = previous;
        applyAgendaNowLineAppearance();
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.nowLineFailed' }));
      } finally {
        nowLineInput.disabled = false;
      }
    },
    { signal },
  );

  const enabledToggle = document.getElementById('agendaEnabledToggle') as HTMLInputElement | null;
  enabledToggle?.addEventListener(
    'change',
    async () => {
      try {
        await apiFetch(`/api/board/${slug}/settings`, {
          method: 'PATCH',
          body: JSON.stringify({ agendaEnabled: enabledToggle.checked }),
        });
        showToast(t('settings.calendar.toast.enabledUpdated'));
        clearCalendarSettingsCache();
        await rerender();
      } catch (err: unknown) {
        enabledToggle.checked = !enabledToggle.checked;
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.enabledFailed' }));
      }
    },
    { signal },
  );

  const titleInput = document.getElementById('agendaTitleInput') as HTMLInputElement | null;
  const saveAgendaTitle = async () => {
    if (!titleInput) return;
    const title = titleInput.value.trim();
    if (!title) {
      showToast(t('settings.calendar.toast.titleRequired'));
      await rerender();
      return;
    }
    if (title === resolveAgendaTitle(cachedCalendar?.agendaTitle)) {
      return;
    }
    try {
      await apiFetch(`/api/board/${slug}/settings`, {
        method: 'PATCH',
        body: JSON.stringify({ agendaTitle: title }),
      });
      showToast(t('settings.calendar.toast.titleUpdated'));
      clearCalendarSettingsCache();
      await rerender();
    } catch (err: unknown) {
      showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.titleFailed' }));
      await rerender();
    }
  };
  titleInput?.addEventListener('blur', () => {
    void saveAgendaTitle();
  }, { signal });
  titleInput?.addEventListener(
    'keydown',
    (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      titleInput.blur();
    },
    { signal },
  );

  const colorInput = document.getElementById('agendaColorInput') as HTMLInputElement | null;
  colorInput?.addEventListener(
    'change',
    async () => {
      const previous = resolveAgendaColor(cachedCalendar?.agendaColor);
      const color = resolveAgendaColor(colorInput.value);
      if (color.toLowerCase() === previous.toLowerCase()) {
        return;
      }
      try {
        await apiFetch(`/api/board/${slug}/settings`, {
          method: 'PATCH',
          body: JSON.stringify({ agendaColor: color }),
        });
        showToast(t('settings.calendar.toast.colorUpdated'));
        const board = getBoard();
        if (board?.agenda) board.agenda.color = color;
        syncOpenBoardAgendaLayout();
        clearCalendarSettingsCache();
        await rerender();
      } catch (err: unknown) {
        colorInput.value = previous;
        const board = getBoard();
        if (board?.agenda) {
          board.agenda.color = previous;
          syncOpenBoardAgendaLayout();
        }
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.colorFailed' }));
        await rerender();
      }
    },
    { signal },
  );

  const timezoneSelect = document.getElementById('agendaTimezoneInput') as HTMLSelectElement | null;
  timezoneSelect?.addEventListener(
    'change',
    async () => {
      const timezone = resolveAgendaTimezone(timezoneSelect.value);
      try {
        await apiFetch(`/api/board/${slug}/settings`, {
          method: 'PATCH',
          body: JSON.stringify({ agendaTimezone: timezone }),
        });
        showToast(t('settings.calendar.toast.timezoneUpdated'));
        clearCalendarSettingsCache();
        await rerender();
      } catch (err: unknown) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.timezoneFailed' }));
        await rerender();
      }
    },
    { signal },
  );

  const addBtn = document.getElementById('calendarSourceAdd');
  addBtn?.addEventListener(
    'click',
    async () => {
      const nameInput = document.getElementById('calendarSourceNameInput') as HTMLInputElement | null;
      const urlInput = document.getElementById('calendarSourceUrlInput') as HTMLInputElement | null;
      const name = nameInput?.value.trim() ?? '';
      const url = urlInput?.value.trim() ?? '';
      try {
        await apiFetch(`/api/board/${slug}/calendar-sources`, {
          method: 'POST',
          body: JSON.stringify({ name, url }),
        });
        if (urlInput) urlInput.value = '';
        if (nameInput) nameInput.value = '';
        showToast(t('settings.calendar.toast.added'));
        clearCalendarSettingsCache();
        await rerender();
      } catch (err: unknown) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.addFailed' }));
      }
    },
    { signal },
  );

  document.querySelectorAll<HTMLElement>('[data-calendar-source-id]').forEach((row) => {
    const id = Number(row.getAttribute('data-calendar-source-id'));
    if (!Number.isFinite(id) || id <= 0) return;
    const enabled = row.querySelector<HTMLInputElement>('[data-calendar-source-enabled]');
    enabled?.addEventListener(
      'change',
      async () => {
        try {
          await apiFetch(`/api/board/${slug}/calendar-sources/${id}`, {
            method: 'PATCH',
            body: JSON.stringify({ enabled: enabled.checked }),
          });
          showToast(t('settings.calendar.toast.sourceUpdated'));
        } catch (err: unknown) {
          enabled.checked = !enabled.checked;
          showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.sourceUpdateFailed' }));
        }
      },
      { signal },
    );
    const refreshBtn = row.querySelector<HTMLButtonElement>('[data-calendar-source-refresh]');
    refreshBtn?.addEventListener(
      'click',
      async () => {
        try {
          await apiFetch(`/api/board/${slug}/calendar-sources/${id}/refresh`, { method: 'POST' });
          showToast(t('settings.calendar.toast.refreshed'));
        } catch (err: unknown) {
          showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.refreshFailed' }));
        }
      },
      { signal },
    );
    const removeBtn = row.querySelector<HTMLButtonElement>('[data-calendar-source-delete]');
    removeBtn?.addEventListener(
      'click',
      async () => {
        const name = row.querySelector('strong')?.textContent ?? '';
        const confirmed = await confirmDelete(t('settings.calendar.confirm.remove', { name }));
        if (!confirmed) return;
        try {
          await apiFetch(`/api/board/${slug}/calendar-sources/${id}`, { method: 'DELETE' });
          showToast(t('settings.calendar.toast.removed'));
          clearCalendarSettingsCache();
          await rerender();
        } catch (err: unknown) {
          showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.calendar.toast.removeFailed' }));
        }
      },
      { signal },
    );
  });
}

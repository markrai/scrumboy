// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import enCatalog from '../i18n/locales/en.json';

function emptyAgendaBoard() {
  return {
    project: { id: 1, name: 'Alpha', slug: 'alpha', dominantColor: '#123456' },
    tags: [],
    columnOrder: [{ key: 'backlog', name: 'Backlog', isDone: false }],
    columns: { backlog: [] },
    agenda: { enabled: true, timezone: 'UTC', stale: false, error: null, events: [], color: '#6366F1' },
  };
}

const selectorState: { slug: string | null; board: ReturnType<typeof emptyAgendaBoard> | null } = {
  slug: 'alpha',
  board: null,
};
const apiFetchMock = vi.fn();
const intlWithSupportedValues = Intl as typeof Intl & {
  supportedValuesOf?: (key: string) => string[];
};
const originalSupportedValuesOf = intlWithSupportedValues.supportedValuesOf;

vi.mock('../api.js', () => ({ apiFetch: apiFetchMock }));
vi.mock('../state/selectors.js', () => ({
  getSlug: () => selectorState.slug,
  getBoard: () => selectorState.board,
  getMobileTab: () => null,
  getUser: () => ({ id: 1, name: 'Ada' }),
}));
vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;'),
  confirmDelete: vi.fn(),
  showToast: vi.fn(),
  sanitizeHexColor: (color?: string | null, fallback?: string | null) => {
    if (typeof color === 'string' && /^#[0-9a-fA-F]{6}$/.test(color.trim())) return color.trim();
    return fallback ?? null;
  },
}));

const calendarPayload = {
  agendaEnabled: true,
  agendaTimezone: 'UTC',
  agendaTitle: 'Agenda',
  agendaColor: '#6366F1',
  sources: [
    {
      id: 3,
      name: 'Family',
      type: 'ics_feed',
      enabled: true,
      urlConfigured: true,
      urlPreview: 'https://calendar.example.com/…',
    },
  ],
};

async function flushMicrotasks(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

describe('settings calendar tab', () => {
  beforeEach(async () => {
    selectorState.slug = 'alpha';
    selectorState.board = null;
    calendarPayload.agendaTitle = 'Agenda';
    calendarPayload.agendaColor = '#6366F1';
    apiFetchMock.mockReset();
    localStorage.clear();
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const { clearCalendarSettingsCache } = await import('./settings-calendar.js');
    clearCalendarSettingsCache();
    const { showToast } = await import('../utils.js');
    vi.mocked(showToast).mockClear();
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    if (originalSupportedValuesOf) {
      intlWithSupportedValues.supportedValuesOf = originalSupportedValuesOf;
    } else {
      delete intlWithSupportedValues.supportedValuesOf;
    }
  });

  it('renders redacted previews and never interpolates the raw ICS URL', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent } = await import('./settings-calendar.js');
    const html = await loadCalendarTabContent();
    expect(html).toContain('https://calendar.example.com/…');
    expect(html).toContain('Family');
    expect(html).not.toContain('super-secret-token');
    expect(html).toContain('id="agendaEnabledToggle"');
    expect(html).toContain('id="agendaTitleInput"');
    expect(html).toContain('for="agendaTitleInput"');
    expect(html).toContain('class="field settings-agenda-name-field"');
    expect(html).toContain('id="agendaColorInput"');
    expect(html).toContain('class="field settings-agenda-color-field"');
    expect(html).toContain('settings-agenda-name-row');
    expect(html).toContain('type="color"');
    expect(html).toContain('settings-color-picker');
    expect(html).toContain('value="#6366F1"');
    expect(html).toContain('id="agendaTimezoneInput"');
    expect(html).toContain('<select class="input" id="agendaTimezoneInput">');
    expect(html).toContain('for="agendaTimezoneInput"');
    expect(html).not.toContain('id="agendaTimezoneSave"');
    expect(html).toContain('id="agendaStartOfDay"');
    expect(html).toContain('id="agendaNowLineProminent"');
    expect(html).toContain('data-calendar-source-refresh');
  });

  it('shows the failure toast when manual refresh fails', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    const html = await loadCalendarTabContent();
    document.body.innerHTML = html;
    const { showToast } = await import('../utils.js');
    apiFetchMock.mockReset();
    const err = Object.assign(new Error('calendar feed request failed'), {
      status: 502,
      data: { error: { code: 'BAD_GATEWAY', message: 'calendar feed request failed' } },
    });
    apiFetchMock.mockRejectedValueOnce(err);
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });
    document.querySelector<HTMLButtonElement>('[data-calendar-source-refresh]')?.click();
    await flushMicrotasks();
    expect(showToast).toHaveBeenCalled();
    const messages = vi.mocked(showToast).mock.calls.map((call) => call[0]);
    expect(messages).not.toContain(enCatalog['settings.calendar.toast.refreshed']);
    expect(messages.some((msg) => String(msg).includes('calendar feed request failed') || msg === enCatalog['settings.calendar.toast.refreshFailed'])).toBe(true);
  });

  it('populates timezone options from Intl.supportedValuesOf and selects the saved timezone', async () => {
    intlWithSupportedValues.supportedValuesOf = (key: string) => {
      expect(key).toBe('timeZone');
      return ['Pacific/Auckland', 'America/New_York', 'UTC'];
    };
    apiFetchMock.mockResolvedValue({ ...calendarPayload, agendaTimezone: 'America/New_York' });
    const { loadCalendarTabContent } = await import('./settings-calendar.js');
    const html = await loadCalendarTabContent();
    document.body.innerHTML = html;
    const select = document.getElementById('agendaTimezoneInput') as HTMLSelectElement;
    const values = Array.from(select.options).map((option) => option.value);
    expect(values).toEqual([...new Set(values)].sort((a, b) => a.localeCompare(b)));
    expect(values).toEqual(expect.arrayContaining(['America/New_York', 'Pacific/Auckland', 'UTC']));
    expect(select.value).toBe('America/New_York');
  });

  it('still includes the browser timezone when it is absent from supportedValuesOf', async () => {
    intlWithSupportedValues.supportedValuesOf = () => ['Pacific/Auckland', 'UTC'];
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockReturnValue({
      locale: 'en-US',
      calendar: 'gregory',
      numberingSystem: 'latn',
      timeZone: 'Europe/Berlin',
    } as Intl.ResolvedDateTimeFormatOptions);

    const { listAgendaTimezones } = await import('./settings-calendar.js');
    const zones = listAgendaTimezones('UTC');
    expect(zones).toEqual(['Europe/Berlin', 'Pacific/Auckland', 'UTC']);
  });

  it('falls back to UTC, saved timezone, and browser timezone when supportedValuesOf is unavailable', async () => {
    delete intlWithSupportedValues.supportedValuesOf;
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockReturnValue({
      locale: 'en-US',
      calendar: 'gregory',
      numberingSystem: 'latn',
      timeZone: 'Europe/Berlin',
    } as Intl.ResolvedDateTimeFormatOptions);

    const { listAgendaTimezones } = await import('./settings-calendar.js');
    const zones = listAgendaTimezones('America/Chicago');
    expect(zones).toEqual(['America/Chicago', 'Europe/Berlin', 'UTC']);
    expect(new Set(zones).size).toBe(zones.length);

    apiFetchMock.mockResolvedValue({ ...calendarPayload, agendaTimezone: 'America/Chicago' });
    const { loadCalendarTabContent } = await import('./settings-calendar.js');
    const html = await loadCalendarTabContent();
    document.body.innerHTML = html;
    const select = document.getElementById('agendaTimezoneInput') as HTMLSelectElement;
    const values = Array.from(select.options).map((option) => option.value);
    expect(values).toEqual(expect.arrayContaining(['UTC', 'America/Chicago', 'Europe/Berlin']));
    expect(select.value).toBe('America/Chicago');
  });

  it('treats an empty saved timezone as UTC', async () => {
    const { resolveAgendaTimezone, listAgendaTimezones } = await import('./settings-calendar.js');
    expect(resolveAgendaTimezone('')).toBe('UTC');
    expect(resolveAgendaTimezone('   ')).toBe('UTC');
    expect(listAgendaTimezones('')).toContain('UTC');
  });

  it('saves the selected timezone on change', async () => {
    intlWithSupportedValues.supportedValuesOf = () => ['America/New_York', 'UTC'];
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    const { showToast } = await import('../utils.js');
    const rerender = vi.fn(async () => {
      document.body.innerHTML = await loadCalendarTabContent();
    });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender });

    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValueOnce({});
    apiFetchMock.mockResolvedValueOnce({
      ...calendarPayload,
      agendaTimezone: 'America/New_York',
    });

    const select = document.getElementById('agendaTimezoneInput') as HTMLSelectElement;
    select.value = 'America/New_York';
    select.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/settings', {
      method: 'PATCH',
      body: JSON.stringify({ agendaTimezone: 'America/New_York' }),
    });
    expect(showToast).toHaveBeenCalledWith(enCatalog['settings.calendar.toast.timezoneUpdated']);
    expect(rerender).toHaveBeenCalledTimes(1);
    const saved = document.getElementById('agendaTimezoneInput') as HTMLSelectElement;
    expect(saved.value).toBe('America/New_York');
  });

  it('restores the persisted timezone when the timezone PATCH fails', async () => {
    intlWithSupportedValues.supportedValuesOf = () => ['America/New_York', 'UTC'];
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    const { showToast } = await import('../utils.js');
    const rerender = vi.fn(async () => {
      document.body.innerHTML = await loadCalendarTabContent();
    });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender });

    const err = Object.assign(new Error('invalid agenda timezone'), {
      status: 400,
      data: { error: { code: 'BAD_REQUEST', message: 'invalid agenda timezone' } },
    });
    apiFetchMock.mockReset();
    apiFetchMock.mockRejectedValueOnce(err);
    apiFetchMock.mockResolvedValue(calendarPayload);

    const select = document.getElementById('agendaTimezoneInput') as HTMLSelectElement;
    select.value = 'America/New_York';
    select.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(showToast).toHaveBeenCalled();
    const messages = vi.mocked(showToast).mock.calls.map((call) => call[0]);
    expect(messages).not.toContain(enCatalog['settings.calendar.toast.timezoneUpdated']);
    expect(
      messages.some(
        (msg) =>
          String(msg).includes('invalid agenda timezone') ||
          msg === enCatalog['settings.calendar.toast.timezoneFailed'],
      ),
    ).toBe(true);
    expect(rerender).toHaveBeenCalledTimes(1);
    const restored = document.getElementById('agendaTimezoneInput') as HTMLSelectElement;
    expect(restored.value).toBe('UTC');
  });

  it('saves the lane name on blur', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    const { showToast } = await import('../utils.js');
    const rerender = vi.fn(async () => {
      document.body.innerHTML = await loadCalendarTabContent();
    });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender });

    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValueOnce({});
    apiFetchMock.mockResolvedValueOnce({
      ...calendarPayload,
      agendaTitle: 'Team calendar',
    });

    const input = document.getElementById('agendaTitleInput') as HTMLInputElement;
    input.value = 'Team calendar';
    input.dispatchEvent(new Event('blur'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/settings', {
      method: 'PATCH',
      body: JSON.stringify({ agendaTitle: 'Team calendar' }),
    });
    expect(showToast).toHaveBeenCalledWith(enCatalog['settings.calendar.toast.titleUpdated']);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('does not PATCH an empty lane name', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    const { showToast } = await import('../utils.js');
    const rerender = vi.fn(async () => {
      document.body.innerHTML = await loadCalendarTabContent();
    });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender });
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue(calendarPayload);

    const input = document.getElementById('agendaTitleInput') as HTMLInputElement;
    input.value = '   ';
    input.dispatchEvent(new Event('blur'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).not.toHaveBeenCalledWith(
      '/api/board/alpha/settings',
      expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ agendaTitle: '' }) }),
    );
    expect(showToast).toHaveBeenCalledWith(enCatalog['settings.calendar.toast.titleRequired']);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('saves the lane color on change', async () => {
    selectorState.board = emptyAgendaBoard();
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    const { showToast } = await import('../utils.js');
    const rerender = vi.fn(async () => {
      document.body.innerHTML = await loadCalendarTabContent();
    });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender });

    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValueOnce({});
    apiFetchMock.mockResolvedValueOnce({
      ...calendarPayload,
      agendaColor: '#aabbcc',
    });

    const input = document.getElementById('agendaColorInput') as HTMLInputElement;
    input.value = '#aabbcc';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/settings', {
      method: 'PATCH',
      body: JSON.stringify({ agendaColor: '#aabbcc' }),
    });
    expect(showToast).toHaveBeenCalledWith(enCatalog['settings.calendar.toast.colorUpdated']);
    expect(selectorState.board?.agenda?.color).toBe('#aabbcc');
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('restores the picker and open lane color when the color PATCH fails', async () => {
    selectorState.board = emptyAgendaBoard();
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    const { buildAgendaColumnHtml } = await import('../views/board-agenda.js');
    document.body.innerHTML = `${await loadCalendarTabContent()}${buildAgendaColumnHtml(selectorState.board, null)}`;
    const { showToast } = await import('../utils.js');
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });

    apiFetchMock.mockReset();
    const err = Object.assign(new Error('invalid agenda color'), {
      status: 400,
      data: { error: { code: 'VALIDATION', message: 'invalid agenda color' } },
    });
    apiFetchMock.mockRejectedValueOnce(err);

    const input = document.getElementById('agendaColorInput') as HTMLInputElement;
    input.value = '#aabbcc';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(input.value.toLowerCase()).toBe('#6366f1');
    expect(selectorState.board?.agenda?.color).toBe('#6366F1');
    expect(document.querySelector('.col--agenda')?.getAttribute('style')).toContain('--agenda-lane-color:#6366F1');
    expect(showToast).toHaveBeenCalled();
    const messages = vi.mocked(showToast).mock.calls.map((call) => call[0]);
    expect(
      messages.some(
        (msg) =>
          String(msg).includes('invalid agenda color') ||
          msg === enCatalog['settings.calendar.toast.colorFailed'],
      ),
    ).toBe(true);
  });

  it('does not reset a manually scrolled Agenda lane when color saves', async () => {
    selectorState.board = emptyAgendaBoard();
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    const { buildAgendaColumnHtml, applyAgendaScrollAfterRender } = await import('../views/board-agenda.js');
    document.body.innerHTML = `${await loadCalendarTabContent()}${buildAgendaColumnHtml(selectorState.board, null)}`;
    const list = document.getElementById('list_agenda') as HTMLElement;
    Object.defineProperty(list, 'clientHeight', { configurable: true, get: () => 400 });
    list.scrollTop = 240;
    applyAgendaScrollAfterRender({ restoreScrollTop: 240, board: selectorState.board });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });

    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValueOnce({});

    const input = document.getElementById('agendaColorInput') as HTMLInputElement;
    input.value = '#aabbcc';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    const nextList = document.getElementById('list_agenda') as HTMLElement;
    expect(nextList.scrollTop).toBe(240);
    expect(selectorState.board?.agenda?.color).toBe('#aabbcc');
    expect(document.querySelector('.col--agenda')?.getAttribute('style')).toContain('--agenda-lane-color:#aabbcc');
  });

  it('does not PATCH the lane color when it is unchanged', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });
    apiFetchMock.mockReset();

    const input = document.getElementById('agendaColorInput') as HTMLInputElement;
    input.value = '#6366F1';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('saves Start of day through user preferences and does not PATCH board settings', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    const prefs = await import('../core/agenda-start-of-day-preferences.js');
    document.body.innerHTML = await loadCalendarTabContent();
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({});

    const input = document.getElementById('agendaStartOfDay') as HTMLInputElement;
    expect(input.value).toBe('08:00');
    input.value = '06:00';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/user/preferences', {
      method: 'PUT',
      body: JSON.stringify({ key: 'agendaStartOfDay', value: '06:00' }),
    });
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      '/api/board/alpha/settings',
      expect.anything(),
    );
    expect(prefs.getAgendaStartOfDayPreference()).toBe('06:00');
    expect(input.value).toBe('06:00');
  });

  it('saves prominent now line through user preferences and does not PATCH board settings', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    const prefs = await import('../core/agenda-now-line-preferences.js');
    const { buildAgendaColumnHtml, applyAgendaScrollAfterRender } = await import('../views/board-agenda.js');
    selectorState.board = emptyAgendaBoard();
    document.body.innerHTML = `${await loadCalendarTabContent()}${buildAgendaColumnHtml(selectorState.board, null)}`;
    applyAgendaScrollAfterRender({ restoreScrollTop: 0, board: selectorState.board });
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({});

    const input = document.getElementById('agendaNowLineProminent') as HTMLInputElement;
    expect(input.checked).toBe(false);
    expect(document.querySelector('.agenda-now-line')?.classList.contains('agenda-now-line--prominent')).toBe(false);
    input.checked = true;
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/user/preferences', {
      method: 'PUT',
      body: JSON.stringify({ key: 'agendaNowLine', value: 'prominent' }),
    });
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      '/api/board/alpha/settings',
      expect.anything(),
    );
    expect(prefs.isAgendaNowLineProminent()).toBe(true);
    expect(document.querySelector('.agenda-now-line')?.classList.contains('agenda-now-line--prominent')).toBe(true);
  });

  it('rolls back Start of day when remote save fails', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    const prefs = await import('../core/agenda-start-of-day-preferences.js');
    const { buildAgendaColumnHtml } = await import('../views/board-agenda.js');
    selectorState.board = emptyAgendaBoard();
    const settingsHtml = await loadCalendarTabContent();
    document.body.innerHTML = `${settingsHtml}${buildAgendaColumnHtml(selectorState.board, null)}`;
    expect(document.querySelector('.agenda-day')).not.toBeNull();
    expect(document.querySelectorAll('.agenda-hour')).toHaveLength(24);
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });
    apiFetchMock.mockReset();
    const err = Object.assign(new Error('could not save preference'), {
      status: 500,
      data: { error: { code: 'INTERNAL', message: 'could not save preference' } },
    });
    apiFetchMock.mockRejectedValueOnce(err);
    const { showToast } = await import('../utils.js');

    const input = document.getElementById('agendaStartOfDay') as HTMLInputElement;
    input.value = '06:00';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();
    await flushMicrotasks();

    expect(prefs.getAgendaStartOfDayPreference()).toBe('08:00');
    expect(input.value).toBe('08:00');
    expect(document.querySelector('.agenda-day')).not.toBeNull();
    expect(showToast).toHaveBeenCalled();
    const messages = vi.mocked(showToast).mock.calls.map((call) => call[0]);
    expect(
      messages.some(
        (msg) =>
          String(msg).includes('could not save preference') ||
          msg === enCatalog['settings.calendar.toast.timelineFailed'],
      ),
    ).toBe(true);
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      '/api/board/alpha/settings',
      expect.anything(),
    );
  });

  it('disables Start of day while its save is pending', async () => {
    apiFetchMock.mockResolvedValue(calendarPayload);
    const { loadCalendarTabContent, bindCalendarTabInteractions } = await import('./settings-calendar.js');
    document.body.innerHTML = await loadCalendarTabContent();
    bindCalendarTabInteractions({ signal: new AbortController().signal, rerender: async () => {} });
    let resolveSave: (value: unknown) => void = () => {};
    const pending = new Promise((resolve) => {
      resolveSave = resolve;
    });
    apiFetchMock.mockReset();
    apiFetchMock.mockImplementation(() => pending);

    const input = document.getElementById('agendaStartOfDay') as HTMLInputElement;
    input.value = '06:00';
    input.dispatchEvent(new Event('change'));
    await flushMicrotasks();

    expect(input.disabled).toBe(true);
    resolveSave({});
    await flushMicrotasks();
    await flushMicrotasks();
    expect(input.disabled).toBe(false);
    expect(input.value).toBe('06:00');
  });

  it('renders only the Start of day input for non-maintainers without fetching calendar sources', async () => {
    const { loadCalendarTabContent } = await import('./settings-calendar.js');
    const html = await loadCalendarTabContent({ canManageCalendar: false });
    expect(html).toContain('id="agendaStartOfDay"');
    expect(html).toContain('id="agendaNowLineProminent"');
    expect(html).toContain(enCatalog['settings.calendar.timeline.label']);
    expect(html).toContain(enCatalog['settings.calendar.nowLine.label']);
    expect(html).not.toContain('id="agendaEnabledToggle"');
    expect(html).not.toContain('id="agendaTimezoneInput"');
    expect(html).not.toContain('id="agendaTitleInput"');
    expect(html).not.toContain('id="agendaColorInput"');
    expect(html).not.toContain('id="calendarSourceAdd"');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });
});

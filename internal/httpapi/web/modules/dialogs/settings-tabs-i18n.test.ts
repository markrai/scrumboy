// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enCatalog from '../i18n/locales/en.json';
import deCatalog from '../i18n/locales/de.json';
import pseudoCatalog from '../i18n/locales/pseudo.json';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  mountBurndownChartMock,
  destroyBurndownChartMock,
  invalidateBoardMock,
  refreshSprintsAndChipsMock,
  recordLocalMutationMock,
  showConfirmDialogMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  fetchProjectMembersMock: vi.fn(),
  mountBurndownChartMock: vi.fn(),
  destroyBurndownChartMock: vi.fn(),
  invalidateBoardMock: vi.fn(),
  refreshSprintsAndChipsMock: vi.fn(),
  recordLocalMutationMock: vi.fn(),
  showConfirmDialogMock: vi.fn(),
}));

vi.mock('../api.js', () => ({ apiFetch: apiFetchMock }));
vi.mock('../members-cache.js', () => ({ fetchProjectMembers: fetchProjectMembersMock }));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  showToast: vi.fn(),
  getAppVersion: () => 'test-version',
  showConfirmDialog: showConfirmDialogMock,
  confirmDelete: vi.fn(),
  isAnonymousBoard: () => false,
  renderUserAvatar: () => '',
  processImageFile: vi.fn(),
  processWallpaperFileForUpload: vi.fn(),
  renderAvatarContent: () => '',
  sanitizeHexColor: (color?: string | null, fallback?: string | null) => color ?? fallback ?? null,
}));

vi.mock('../theme.js', () => ({
  getStoredTheme: () => 'system',
  handleThemeChange: vi.fn(),
  THEME_SYSTEM: 'system',
  THEME_DARK: 'dark',
  THEME_LIGHT: 'light',
}));

vi.mock('../wallpaper.js', () => ({
  getStoredWallpaperState: () => ({ v: 1, mode: 'off', hex: '#8b919a' }),
  setWallpaperOff: vi.fn(),
  setWallpaperColor: vi.fn(),
  uploadWallpaperImage: vi.fn(),
}));

vi.mock('../events.js', () => ({ emit: vi.fn() }));

vi.mock('../sprints.js', () => ({
  normalizeSprints: (value: { sprints?: any[] } | null | undefined) => value?.sprints ?? [],
  boardSprintsEnabled: (board: { project?: { sprintsEnabled?: boolean } } | null | undefined) =>
    board?.project?.sprintsEnabled !== false,
}));

// Keep the real tab modules (settings-sprints/workflow/tags), but mock the chart
// renderer. The rendered title carries the active locale (via documentElement's
// data-locale, which i18n updates) so we can prove the charts branch re-renders the
// block from cached state on locale change. Real chart-copy localization is covered
// in charts/burndown.test.ts.
vi.mock('../charts/burndown.js', () => ({
  renderRealBurndownChart: (
    _data: any[],
    currentSprint?: { id?: number; name?: string } | null,
    _sprintNav?: { canPrev: boolean; canNext: boolean },
  ) => `
    <div class="burndown-chart">
      <div class="burndown-chart__title" data-locale="${document.documentElement.getAttribute('data-locale') ?? 'en'}">Real Burndown ${currentSprint?.name ?? 'none'}</div>
      <button id="burndown-prev" type="button" aria-label="Previous sprint"></button>
      <button id="burndown-next" type="button" aria-label="Next sprint"></button>
      <div id="burndown-uplot-mount"></div>
    </div>
  `,
  destroyBurndownChart: destroyBurndownChartMock,
  mountBurndownChart: mountBurndownChartMock,
}));

vi.mock('../orchestration/board-refresh.js', () => ({
  invalidateBoard: invalidateBoardMock,
  refreshSprintsAndChips: refreshSprintsAndChipsMock,
  CARDS_PER_LANE_ALLOWED: [20, 50, 75, 100],
  CARDS_PER_LANE_PREFERENCE_KEY: 'cardsPerLane',
  getDefaultCardsPerLane: () => 20,
  setDefaultCardsPerLane: vi.fn(),
}));

vi.mock('../realtime/guard.js', () => ({ recordLocalMutation: recordLocalMutationMock }));

vi.mock('../core/keybindings.js', () => ({
  KEY_ACTION_LIST: [],
  chordFromKeyboardEvent: () => null,
  formatChordForDisplay: (chord: string) => chord,
  getResolvedChordForAction: () => 'shift+s',
  isTypingInTextField: () => false,
  reloadKeybindingsFromStorage: vi.fn(),
  saveKeybindingOverride: vi.fn(),
  setKeybindingsCaptureListening: vi.fn(),
}));

vi.mock('../core/assignmentNotify.js', () => ({
  requestDesktopNotificationPermission: vi.fn(),
  getDesktopNotificationStatusKind: () => 'default',
  getDesktopNotificationStatusDescription: () => 'Not enabled yet',
}));

vi.mock('../core/push.js', () => ({
  isPushSubscribed: vi.fn(),
  subscribeToPush: vi.fn(),
  unsubscribeFromPush: vi.fn(),
}));

vi.mock('../core/voiceflow-preferences.js', () => ({
  getVoiceFlowEnabledPreference: () => false,
  setVoiceFlowEnabledPreference: vi.fn(),
}));

const SPRINT_DATE_OPTS: Intl.DateTimeFormatOptions = {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
};

function installBaseDOM(): void {
  document.body.innerHTML = `
    <dialog id="settingsDialog">
      <div class="dialog__header">
        <div class="dialog__title">
          <span id="settingsDialogTitleLabel" data-i18n-text="settings.shell.title">Settings</span>
          <span id="settingsDialogVersion"></span>
        </div>
        <button id="closeSettingsBtn" type="button" data-i18n-aria-label="common.close"></button>
      </div>
      <div class="dialog__content"></div>
    </dialog>
  `;
}

function loader() {
  const catalogs: Record<string, Record<string, string>> = {
    en: enCatalog as Record<string, string>,
    de: deCatalog as Record<string, string>,
    pseudo: pseudoCatalog as Record<string, string>,
  };
  return vi.fn(async (locale: string) => catalogs[locale]);
}

async function flushPromises(count = 12): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function initI18nFor(locale: 'en' | 'de' | 'pseudo' = 'en') {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({ locale, loadLocale: loader() });
  return i18n;
}

async function setupSettingsView(options: {
  activeTab: string;
  slug?: string | null;
  board?: Record<string, unknown> | null;
  user?: Record<string, unknown> | null;
  boardMembers?: any[];
  open?: boolean;
} = { activeTab: 'tag-colors' }) {
  const i18n = await initI18nFor('en');
  const settings = await import('./settings.js');
  const mutations = await import('../state/mutations.js');
  mutations.setAuthStatusAvailable(true);
  mutations.setPushConfigured(false);
  mutations.setUser((options.user as any) ?? null);
  mutations.setSlug(options.slug ?? null);
  mutations.setBoard((options.board as any) ?? null);
  mutations.setProjects(null);
  mutations.setProjectId(null);
  mutations.setSettingsProjectId(null);
  mutations.setSettingsActiveTab(options.activeTab);
  mutations.setBoardMembers(options.boardMembers ?? []);
  await settings.renderSettingsModal();
  await flushPromises();
  const dialog = document.getElementById('settingsDialog') as HTMLDialogElement | null;
  if (dialog) {
    if (options.open === false) {
      dialog.removeAttribute('open');
      dialog.open = false;
    } else {
      dialog.setAttribute('open', '');
      dialog.open = true;
    }
  }
  return { i18n, settings };
}

const MAINTAINER = [{ userId: 1, role: 'maintainer' }];
const USER = { id: 1, name: 'Alex' };

describe('settings tabs i18n (charts, sprints, workflow, tag colors)', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    window.history.replaceState({}, '', '/');
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    mountBurndownChartMock.mockClear();
    destroyBurndownChartMock.mockClear();
    invalidateBoardMock.mockReset();
    refreshSprintsAndChipsMock.mockReset();
    recordLocalMutationMock.mockReset();
    showConfirmDialogMock.mockReset();
    showConfirmDialogMock.mockResolvedValue(false);
  });

  afterEach(async () => {
    const dialog = document.getElementById('settingsDialog') as HTMLDialogElement | null;
    dialog?.dispatchEvent(new Event('close'));
    const i18n = await import('../i18n/index.js');
    const settingsGlobal = globalThis as { __scrumboySettingsLocaleListener?: EventListener };
    if (settingsGlobal.__scrumboySettingsLocaleListener) {
      document.removeEventListener('scrumboy:i18n-locale-changed', settingsGlobal.__scrumboySettingsLocaleListener);
      delete settingsGlobal.__scrumboySettingsLocaleListener;
    }
    i18n.resetI18nForTests();
    document.body.innerHTML = '';
    document.documentElement.lang = 'en';
    document.documentElement.removeAttribute('data-locale');
    localStorage.clear();
    vi.restoreAllMocks();
    window.history.replaceState({}, '', '/');
  });

  it('Charts: re-localizes chart copy from cache and re-mounts without fetching /sprints or /burndown', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      if (url === '/api/board/alpha/sprints') {
        return {
          sprints: [
            { id: 1, name: 'Sprint 1', state: 'ACTIVE', plannedStartAt: 1704067200000, plannedEndAt: 1705276800000 },
          ],
        };
      }
      if (url === '/api/board/alpha/sprints/1/burndown') {
        return [
          { date: '2024-01-01T00:00:00Z', remainingWork: 5, initialScope: 5 },
          { date: '2024-01-02T00:00:00Z', remainingWork: 3, initialScope: 5 },
        ];
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'charts',
      slug: 'alpha',
      board: { project: {} },
    });

    const titleEl = document.querySelector('.burndown-chart__title');
    expect(titleEl?.getAttribute('data-locale')).toBe('en');
    expect(document.querySelector('.settings-tab--active[data-tab="charts"]')).toBeTruthy();

    apiFetchMock.mockClear();
    mountBurndownChartMock.mockClear();
    destroyBurndownChartMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    // Constraint 1: no /sprints and no /burndown fetch on locale change.
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(document.querySelector('.settings-tab--active[data-tab="charts"]')).toBeTruthy();
    // The chart block was rebuilt from cached state under the new locale.
    expect(document.querySelector('.burndown-chart__title')?.getAttribute('data-locale')).toBe('de');
    // Re-mounted from cached data (rebuild from cache, no refetch).
    expect(mountBurndownChartMock).toHaveBeenCalled();
  });

  it('Sprints: updates formatted timestamps on locale change without losing inputs or refetching', async () => {
    const s1 = { id: 1, name: 'Sprint 1', state: 'PLANNED', plannedStartAt: 1704067200000, plannedEndAt: 1705276800000, todoCount: 0 };
    const s2 = { id: 2, name: 'Sprint 2', state: 'PLANNED', plannedStartAt: 1706745600000, plannedEndAt: 1707955200000, todoCount: 0 };
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      if (url === '/api/me') return USER;
      if (url === '/api/board/alpha/sprints') return { sprints: [s1, s2] };
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'sprints',
      slug: 'alpha',
      board: { project: { id: 7 } },
      user: USER,
      boardMembers: MAINTAINER,
    });

    expect(document.querySelector('.settings-tab--active[data-tab="sprints"]')).toBeTruthy();

    // Enter edit mode on Sprint 1 (triggers a rerender that refetches the list).
    (document.querySelector('[data-sprint-edit="1"]') as HTMLElement).click();
    await flushPromises();

    // Fill create-form + the edit-row name AFTER the rerender so they reflect user input.
    const nameInput = document.getElementById('sprintNameInput') as HTMLInputElement;
    const startInput = document.getElementById('sprintStartInput') as HTMLInputElement;
    const endInput = document.getElementById('sprintEndInput') as HTMLInputElement;
    nameInput.value = 'My Draft Sprint';
    startInput.value = '2026-03-01T09:00';
    endInput.value = '2026-03-14T17:00';
    const editNameInput = document.querySelector('[data-sprint-edit-name]') as HTMLInputElement;
    editNameInput.value = 'Renamed Sprint 1';

    const rangeBefore = document.querySelector('[data-sprint-range-start="1706745600000"]')?.textContent;
    expect(rangeBefore).toBeTruthy();

    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    // No refetch on locale change.
    expect(apiFetchMock).not.toHaveBeenCalled();

    // Inputs preserved (create-form + editing row), edit mode preserved.
    expect((document.getElementById('sprintNameInput') as HTMLInputElement).value).toBe('My Draft Sprint');
    expect((document.getElementById('sprintStartInput') as HTMLInputElement).value).toBe('2026-03-01T09:00');
    expect((document.getElementById('sprintEndInput') as HTMLInputElement).value).toBe('2026-03-14T17:00');
    expect((document.querySelector('[data-sprint-edit-name]') as HTMLInputElement).value).toBe('Renamed Sprint 1');

    // Chrome localized; sprint name + state stay raw.
    expect(document.querySelector('[data-i18n-text="settings.sprints.create.title"]')?.textContent).toBe(deCatalog['settings.sprints.create.title']);
    expect(document.querySelector('.settings-sprint-row[data-sprint-id="2"] strong')?.textContent).toBe('Sprint 2');
    expect(document.querySelector('.settings-sprint-row[data-sprint-id="2"] .status-pill')?.textContent).toBe('PLANNED');

    // Non-editing row date range reflows to the active locale's format.
    const expectedRange = `${i18n.formatDate(s2.plannedStartAt, SPRINT_DATE_OPTS)} - ${i18n.formatDate(s2.plannedEndAt, SPRINT_DATE_OPTS)}`;
    expect(document.querySelector('[data-sprint-range-start="1706745600000"]')?.textContent).toBe(expectedRange);
  });

  it('Workflow: re-localizes delete labels/titles while preserving enabled/disabled state and not refetching counts', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      if (url === '/api/me') return USER;
      if (url === '/api/board/alpha/workflow/counts') {
        return { countsByColumnKey: { todo: 0, doing: 3, done: 0 } };
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'workflow',
      slug: 'alpha',
      board: {
        project: { id: 7 },
        columnOrder: [
          { key: 'todo', name: 'To Do', isDone: false },
          { key: 'doing', name: 'Doing', isDone: false },
          { key: 'done', name: 'Done', isDone: true },
        ],
      },
      user: USER,
      boardMembers: MAINTAINER,
    });

    // Wait for the async lane-counts fetch + rerender to settle.
    await flushPromises();

    const todoDelete = document.querySelector('[data-workflow-delete="todo"]') as HTMLButtonElement | null;
    const doingRow = document.querySelector('.settings-workflow-row[data-workflow-key="doing"]');
    const doneRow = document.querySelector('.settings-workflow-row[data-workflow-key="done"]');
    const doingDelete = doingRow?.querySelector('button') as HTMLButtonElement;
    const doneDelete = doneRow?.querySelector('button') as HTMLButtonElement;

    expect(todoDelete).toBeTruthy();
    expect(todoDelete!.disabled).toBe(false);
    expect(doingDelete.disabled).toBe(true);
    expect(doneDelete.disabled).toBe(true);

    const todoNameInput = document.querySelector('[data-workflow-name="todo"]') as HTMLInputElement;
    const todoColorInput = document.querySelector('[data-workflow-color="todo"]') as HTMLInputElement;
    const addLaneInput = document.querySelector('[data-workflow-ghost-input]') as HTMLInputElement;
    todoNameInput.value = 'Queued';
    todoColorInput.value = '#123456';

    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    // No counts refetch on locale change.
    expect(apiFetchMock).not.toHaveBeenCalled();

    // Disabled/enabled gating preserved exactly.
    const todoDeleteAfter = document.querySelector('[data-workflow-delete="todo"]') as HTMLButtonElement;
    expect(todoDeleteAfter.disabled).toBe(false);
    expect(todoDeleteAfter.textContent).toBe(deCatalog['settings.workflow.deleteAction']);
    const doingDeleteAfter = document.querySelector('.settings-workflow-row[data-workflow-key="doing"] button') as HTMLButtonElement;
    expect(doingDeleteAfter.disabled).toBe(true);
    expect(doingDeleteAfter.getAttribute('title')).toBe(deCatalog['settings.workflow.deleteTitle.notEmpty']);
    const doneDeleteAfter = document.querySelector('.settings-workflow-row[data-workflow-key="done"] button') as HTMLButtonElement;
    expect(doneDeleteAfter.disabled).toBe(true);
    expect(doneDeleteAfter.getAttribute('title')).toBe(deCatalog['settings.workflow.deleteTitle.done']);

    // Dynamic aria labels and unsaved values (raw user data) stay intact.
    expect((document.querySelector('[data-workflow-name="todo"]') as HTMLInputElement).value).toBe('Queued');
    expect((document.querySelector('[data-workflow-color="todo"]') as HTMLInputElement).value).toBe('#123456');
    expect((document.querySelector('[data-workflow-name="todo"]') as HTMLInputElement).getAttribute('aria-label')).toBe(
      deCatalog['settings.workflow.laneLabelAria'].replace('{key}', 'todo'),
    );
    expect((document.querySelector('[data-workflow-color="todo"]') as HTMLInputElement).getAttribute('aria-label')).toBe(
      deCatalog['settings.workflow.laneColorAria'].replace('{key}', 'todo'),
    );
    expect((document.querySelector('[data-workflow-ghost-input]') as HTMLInputElement).getAttribute('aria-label')).toBe(
      deCatalog['settings.workflow.addLaneAria'],
    );

    // Lane name input value (user data) preserved + chrome localized.
    expect(document.querySelector('[data-i18n-text="settings.workflow.title"]')?.textContent).toBe(deCatalog['settings.workflow.title']);
  });

  it('Priorities: uses priority-specific unsaved confirmation when switching tabs with a dirty draft', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      if (url === '/api/me') return USER;
      if (url === '/api/board/alpha/priorities/counts') {
        return { countsByPriorityKey: { low: 0, medium: 0, urgent: 0 } };
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'priorities',
      slug: 'alpha',
      board: {
        project: { id: 7 },
        priorityOrder: [
          { key: 'low', name: 'Low', color: '#9CA3AF' },
          { key: 'medium', name: 'Medium', color: '#F59E0B' },
          { key: 'urgent', name: 'Urgent', color: '#EF4444' },
        ],
      },
      user: USER,
      boardMembers: MAINTAINER,
    });

    await flushPromises();
    await i18n.setLocale('de');
    await flushPromises();

    const nameInput = document.querySelector('[data-priority-name="medium"]') as HTMLInputElement | null;
    if (!nameInput) throw new Error('missing priority name input');
    nameInput.value = 'Dirty priority';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    const profileTab = document.querySelector('.settings-tab[data-tab="profile"]') as HTMLElement | null;
    if (!profileTab) throw new Error('missing profile settings tab');
    profileTab.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledOnce();
    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      deCatalog['settings.priorities.unsavedConfirm.message'],
      deCatalog['settings.priorities.unsavedConfirm.title'],
      deCatalog['settings.priorities.unsavedConfirm.confirm'],
    );
    expect(document.querySelector('.settings-tab--active[data-tab="priorities"]')).toBeTruthy();
  });

  it('Workflow: localizes lanes-unavailable error in place without refetching counts', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      if (url === '/api/me') return USER;
      if (url === '/api/board/alpha/workflow/counts') {
        return { countsByColumnKey: {} };
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'workflow',
      slug: 'alpha',
      board: {
        project: { id: 7 },
        columnOrder: [],
      },
      user: USER,
      boardMembers: MAINTAINER,
    });

    await flushPromises();

    const errorEl = document.querySelector('[data-i18n-text="settings.workflow.error.lanesUnavailable"]');
    expect(errorEl?.textContent).toBe(enCatalog['settings.workflow.error.lanesUnavailable']);

    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(document.querySelector('[data-i18n-text="settings.workflow.error.lanesUnavailable"]')?.textContent).toBe(
      deCatalog['settings.workflow.error.lanesUnavailable'],
    );
  });

  it('Tag Colors: localizes chrome and preserves unsaved color picker value with raw tag names and no refetch', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') {
        return [{ name: 'frontend', color: '#ff0000', canDelete: true, tagId: 5 }];
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'tag-colors',
      slug: 'alpha',
      board: { project: {} },
    });

    const picker = document.querySelector('.settings-color-picker') as HTMLInputElement;
    expect(picker).toBeTruthy();
    picker.value = '#00ff00';

    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();

    // Unsaved picker value preserved; tag name raw.
    expect((document.querySelector('.settings-color-picker') as HTMLInputElement).value).toBe('#00ff00');
    expect(document.querySelector('.settings-tag-name')?.textContent).toBe('frontend');

    // Chrome + row controls localized.
    expect(document.querySelector('[data-i18n-text="settings.tagColors.title"]')?.textContent).toBe(deCatalog['settings.tagColors.title']);
    expect(document.querySelector('.settings-color-clear')?.textContent).toBe(deCatalog['settings.tagColors.clear']);
    expect(document.querySelector('.settings-color-clear')?.getAttribute('title')).toBe(deCatalog['settings.tagColors.clearTitle']);
    expect(document.querySelector('.settings-tag-delete')?.getAttribute('title')).toBe(deCatalog['settings.tagColors.deleteTitle']);
    expect(document.querySelector('.settings-color-picker')?.getAttribute('title')).toBe(deCatalog['settings.tagColors.colorTitle']);
  });

  it('Tag Colors: localizes the load-error prefix in place and preserves raw backend detail', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') {
        throw new Error('boom');
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'tag-colors',
      slug: 'alpha',
      board: { project: {} },
    });

    const errorEl = document.querySelector('[data-tag-colors-load-error-message="boom"]');
    expect(errorEl?.textContent).toBe(enCatalog['settings.tagColors.error.loadFailed'].replace('{message}', 'boom'));

    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(document.querySelector('[data-tag-colors-load-error-message="boom"]')?.textContent).toBe(
      deCatalog['settings.tagColors.error.loadFailed'].replace('{message}', 'boom'),
    );
  });

  it('audit: repeated renderSettingsModal calls do not stack settings locale listeners', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      throw new Error(`unexpected apiFetch url: ${url}`);
    });
    const addEventListenerSpy = vi.spyOn(document, 'addEventListener');
    const { settings } = await setupSettingsView({ activeTab: 'tag-colors', slug: 'alpha', board: { project: {} } });

    await settings.renderSettingsModal();
    await settings.renderSettingsModal();
    await flushPromises();

    const localeListenerAdds = addEventListenerSpy.mock.calls.filter(
      ([type]) => type === 'scrumboy:i18n-locale-changed',
    );
    expect(localeListenerAdds).toHaveLength(1);
  });

  it('audit: locale change while the dialog is closed does not mutate hidden settings DOM', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') {
        return [{ name: 'frontend', color: '#ff0000', canDelete: true, tagId: 5 }];
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({
      activeTab: 'tag-colors',
      slug: 'alpha',
      board: { project: {} },
      open: false,
    });

    const bodyBefore = document.getElementById('settingsTabContent')?.innerHTML;
    const titleBefore = document.getElementById('settingsDialogTitleLabel')?.textContent;

    await i18n.setLocale('de');
    await flushPromises();

    expect(document.getElementById('settingsTabContent')?.innerHTML).toBe(bodyBefore);
    expect(document.getElementById('settingsDialogTitleLabel')?.textContent).toBe(titleBefore);
  });

  it('relocalizes backup tab static chrome in place on locale change without API calls', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/tags') return [];
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n } = await setupSettingsView({ activeTab: 'backup', slug: 'alpha', board: { project: {} } });

    const tabContent = document.getElementById('settingsTabContent');
    const trelloTitle = tabContent?.querySelector('[data-i18n-text="settings.backup.trello.title"]');
    expect(trelloTitle?.textContent).toBe(enCatalog['settings.backup.trello.title']);
    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(document.querySelector('.settings-tab--active[data-tab="backup"]')).toBeTruthy();
    expect(document.querySelector('.settings-tab[data-tab="backup"]')?.textContent).toBe(deCatalog['settings.tabs.backup']);
    // Backup is now a localized surface: its static chrome relocalizes in place.
    expect(
      tabContent?.querySelector('[data-i18n-text="settings.backup.trello.title"]')?.textContent,
    ).toBe(deCatalog['settings.backup.trello.title']);
    expect(apiFetchMock).not.toHaveBeenCalled();
  });
});

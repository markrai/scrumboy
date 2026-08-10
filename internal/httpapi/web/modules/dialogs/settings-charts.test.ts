// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  loadTagSettingsContentMock,
  mountBurndownChartMock,
  destroyBurndownChartMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  fetchProjectMembersMock: vi.fn(),
  loadTagSettingsContentMock: vi.fn().mockResolvedValue(''),
  mountBurndownChartMock: vi.fn(),
  destroyBurndownChartMock: vi.fn(),
}));

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../members-cache.js', () => ({
  fetchProjectMembers: fetchProjectMembersMock,
}));

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
  showConfirmDialog: vi.fn(),
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
  getStoredWallpaperState: () => ({ mode: 'off' }),
  setWallpaperOff: vi.fn(),
  setWallpaperColor: vi.fn(),
  uploadWallpaperImage: vi.fn(),
}));

vi.mock('../charts/burndown.js', () => ({
  renderRealBurndownChart: (
    _data: any[],
    currentSprint?: { id?: number; name?: string } | null,
    sprintNav?: { canPrev: boolean; canNext: boolean }
  ) => `
    <div class="burndown-chart" data-current-sprint-id="${currentSprint?.id ?? ''}">
      <button id="burndown-prev" ${!sprintNav?.canPrev ? 'disabled' : ''} type="button">Prev</button>
      <div id="burndown-current-sprint">${currentSprint?.name ?? 'No sprint'}</div>
      <button id="burndown-next" ${!sprintNav?.canNext ? 'disabled' : ''} type="button">Next</button>
      <div id="burndown-uplot-mount"></div>
    </div>
  `,
  destroyBurndownChart: destroyBurndownChartMock,
  mountBurndownChart: mountBurndownChartMock,
}));

vi.mock('../events.js', () => ({
  emit: vi.fn(),
}));

vi.mock('../sprints.js', () => ({
  normalizeSprints: (value: { sprints?: any[] } | null | undefined) => value?.sprints ?? [],
  boardSprintsEnabled: (board: { project?: { sprintsEnabled?: boolean } } | null | undefined) =>
    board?.project?.sprintsEnabled !== false,
}));

vi.mock('../core/keybindings.js', () => ({
  KEY_ACTION_LIST: [],
  chordFromKeyboardEvent: vi.fn(),
  formatChordForDisplay: () => '',
  getResolvedChordForAction: () => '',
  isTypingInTextField: () => false,
  reloadKeybindingsFromStorage: vi.fn(),
  saveKeybindingOverride: vi.fn(),
  setKeybindingsCaptureListening: vi.fn(),
}));

vi.mock('../core/assignmentNotify.js', () => ({
  requestDesktopNotificationPermission: vi.fn(),
  getDesktopNotificationStatusKind: () => 'default',
  getDesktopNotificationStatusDescription: () => '',
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

vi.mock('./settings-workflow.js', () => ({
  bindWorkflowTabInteractions: vi.fn(),
  clearWorkflowDraftState: vi.fn(),
  invalidateWorkflowLaneCountsCache: vi.fn(),
  isWorkflowDraftDirty: () => false,
  loadWorkflowTabContent: () => '',
  resetWorkflowDraftToBaseline: vi.fn(),
}));

vi.mock('./settings-tags.js', () => ({
  bindTagTabInteractions: vi.fn(),
  invalidateTagsCache: vi.fn(),
  loadTagSettingsContent: loadTagSettingsContentMock,
}));

vi.mock('./settings-sprints.js', () => ({
  bindSprintsTabInteractions: vi.fn(),
  renderSprintsTabContent: vi.fn().mockResolvedValue(''),
}));

function installBaseDOM(): void {
  document.body.innerHTML = `
    <dialog id="settingsDialog">
      <div class="dialog__header">
        <div class="dialog__title">
          <span id="settingsDialogTitleLabel">Settings</span>
          <span id="settingsDialogVersion"></span>
        </div>
        <button id="closeSettingsBtn" type="button"></button>
      </div>
      <div class="dialog__content"></div>
    </dialog>
  `;
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadSettingsModule() {
  const mod = await import('./settings.js');
  return mod;
}

async function loadStateMutations() {
  const mod = await import('../state/mutations.js');
  return mod;
}

describe('settings-charts', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    window.history.replaceState({}, '', '/alpha');
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    loadTagSettingsContentMock.mockClear();
    mountBurndownChartMock.mockClear();
    destroyBurndownChartMock.mockClear();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      json: async () => ({}),
    }));
  });

  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.history.replaceState({}, '', '/');
  });

  it('preserves the selected sprint and fetches that sprint burndown URL after next navigation', async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/board/alpha/sprints') {
        return {
          sprints: [
            { id: 1, name: 'Sprint 1', state: 'CLOSED', plannedStartAt: 1000, plannedEndAt: 2000 },
            { id: 2, name: 'Sprint 2', state: 'ACTIVE', plannedStartAt: 3000, plannedEndAt: 4000 },
            { id: 3, name: 'Sprint 3', state: 'PLANNED', plannedStartAt: 5000, plannedEndAt: 6000 },
          ],
        };
      }
      if (url === '/api/board/alpha/sprints/2/burndown') {
        return [{ date: '2026-04-13T00:00:00Z', remainingWork: 5, initialScope: 5 }];
      }
      if (url === '/api/board/alpha/sprints/3/burndown') {
        return [{ date: '2026-04-20T00:00:00Z', remainingWork: 4, initialScope: 4 }];
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const settings = await loadSettingsModule();
    const state = await loadStateMutations();
    state.setAuthStatusAvailable(true);
    state.setSlug('alpha');
    state.setUser(null);
    state.setBoard(null);
    state.setProjects(null);
    state.setProjectId(null);
    state.setSettingsProjectId(null);
    state.setSettingsActiveTab('charts');
    state.setBoardMembers([]);

    await settings.renderSettingsModal();
    expect((document.getElementById('burndown-current-sprint')?.textContent ?? '').trim()).toBe('Sprint 2');

    const nextBtn = document.getElementById('burndown-next');
    if (!(nextBtn instanceof HTMLButtonElement)) {
      throw new Error('missing next button');
    }
    nextBtn.click();
    await flushPromises();

    expect((document.getElementById('burndown-current-sprint')?.textContent ?? '').trim()).toBe('Sprint 3');

    const burndownUrls = apiFetchMock.mock.calls
      .map((args) => args[0])
      .filter((url): url is string => typeof url === 'string' && url.endsWith('/burndown'));
    expect(burndownUrls[burndownUrls.length - 1]).toBe('/api/board/alpha/sprints/3/burndown');
  });

  it('uses only project-level chart data while sprints are disabled', async () => {
    apiFetchMock.mockResolvedValue([]);
    const settings = await loadSettingsModule();
    const state = await loadStateMutations();
    state.setAuthStatusAvailable(true);
    state.setSlug('alpha');
    state.setBoard({
      project: { id: 1, slug: 'alpha', name: 'Alpha', sprintsEnabled: false },
      tags: [],
      columns: {},
    } as any);
    state.setProjectId(1);
    state.setSettingsActiveTab('charts');
    state.setBoardMembers([]);

    await settings.renderSettingsModal();

    const urls = apiFetchMock.mock.calls.map((args) => String(args[0]));
    expect(urls.some((url) => url.includes('/sprints'))).toBe(false);
  });
});

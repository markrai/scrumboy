// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  loadTagSettingsContentMock,
  setDefaultCardsPerLaneMock,
  clearBoardPrefetchCacheMock,
  invalidateBoardMock,
  defaultCardsPerLane,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  fetchProjectMembersMock: vi.fn(),
  loadTagSettingsContentMock: vi.fn().mockResolvedValue(''),
  setDefaultCardsPerLaneMock: vi.fn(),
  clearBoardPrefetchCacheMock: vi.fn(),
  invalidateBoardMock: vi.fn(),
  defaultCardsPerLane: { value: 20 },
}));

vi.mock('../api.js', () => ({ apiFetch: apiFetchMock }));

vi.mock('../members-cache.js', () => ({
  fetchProjectMembers: fetchProjectMembersMock,
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) => s,
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
  getStoredWallpaperState: () => ({ v: 1, mode: 'off', hex: '#8b919a' }),
  setWallpaperOff: vi.fn(),
  setWallpaperColor: vi.fn(),
  uploadWallpaperImage: vi.fn(),
}));

vi.mock('../charts/burndown.js', () => ({
  renderRealBurndownChart: () => '<div></div>',
  destroyBurndownChart: vi.fn(),
  mountBurndownChart: vi.fn(),
}));

vi.mock('../events.js', () => ({ emit: vi.fn() }));
vi.mock('../sprints.js', () => ({ normalizeSprints: () => [] }));
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
  isPushSubscribed: vi.fn().mockResolvedValue(false),
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
vi.mock('../orchestration/board-refresh.js', () => ({
  invalidateBoard: invalidateBoardMock,
  refreshSprintsAndChips: vi.fn(),
  CARDS_PER_LANE_ALLOWED: [20, 50, 75, 100],
  CARDS_PER_LANE_PREFERENCE_KEY: 'cardsPerLane',
  getDefaultCardsPerLane: () => defaultCardsPerLane.value,
  setDefaultCardsPerLane: setDefaultCardsPerLaneMock,
  usePreferenceLimitOnNextBoardRequest: vi.fn(),
}));
vi.mock('../views/board-prefetch-cache.js', () => ({
  clearBoardPrefetchCache: clearBoardPrefetchCacheMock,
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

function cardsPerLanePutCalls(): unknown[][] {
  return apiFetchMock.mock.calls.filter(
    (call) => call[0] === '/api/user/preferences' && typeof call[1]?.body === 'string' && call[1].body.includes('cardsPerLane'),
  );
}

async function renderCustomizationSettings(opts?: { slug?: string | null }) {
  const settings = await import('./settings.js');
  const state = await import('../state/mutations.js');
  state.setAuthStatusAvailable(true);
  state.setPushConfigured(false);
  state.setPushStatus({ state: 'not_configured', reason: null });
  state.setUser({
    id: 1,
    email: 'user@example.com',
    name: 'User',
    systemRole: 'user',
  } as any);
  state.setSlug(opts?.slug ?? null);
  state.setBoard(opts?.slug ? { project: { slug: opts.slug } } as any : null);
  state.setProjects(null);
  state.setProjectId(null);
  state.setSettingsProjectId(null);
  state.setSettingsActiveTab('customization');
  state.setBoardMembers([]);
  await settings.renderSettingsModal();
}

describe('settings cards per lane', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    defaultCardsPerLane.value = 20;
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    setDefaultCardsPerLaneMock.mockReset();
    setDefaultCardsPerLaneMock.mockImplementation((n: number) => {
      defaultCardsPerLane.value = n;
    });
    clearBoardPrefetchCacheMock.mockReset();
    invalidateBoardMock.mockReset();
  });

  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('renders a select with allowlisted options', async () => {
    await renderCustomizationSettings();
    const select = document.getElementById('cardsPerLaneSelect');
    expect(select).toBeInstanceOf(HTMLSelectElement);
    const options = Array.from((select as HTMLSelectElement).options).map((o) => o.value);
    expect(options).toEqual(['20', '50', '75', '100']);
  });

  it('persists a successful change and clears board prefetch cache', async () => {
    window.history.replaceState({ boardData: { project: { id: 1 }, columns: {} } }, '', '/alpha');
    apiFetchMock.mockResolvedValue({ ok: true });
    await renderCustomizationSettings();
    const select = document.getElementById('cardsPerLaneSelect') as HTMLSelectElement;
    select.value = '50';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    await vi.waitFor(() => expect(setDefaultCardsPerLaneMock).toHaveBeenCalledWith(50));

    expect(cardsPerLanePutCalls()[0]).toEqual([
      '/api/user/preferences',
      {
        method: 'PUT',
        body: JSON.stringify({ key: 'cardsPerLane', value: '50' }),
      },
    ]);
    expect(setDefaultCardsPerLaneMock).toHaveBeenCalledWith(50);
    expect(clearBoardPrefetchCacheMock).toHaveBeenCalledTimes(1);
    expect((window.history.state as { boardData?: unknown } | null)?.boardData).toBeUndefined();
    expect(invalidateBoardMock).not.toHaveBeenCalled();
    expect(select.value).toBe('50');
    expect(select.disabled).toBe(false);
  });

  it('reloads the open board after a successful preference change', async () => {
    apiFetchMock.mockResolvedValue({ ok: true });
    await renderCustomizationSettings({ slug: 'alpha' });
    const select = document.getElementById('cardsPerLaneSelect') as HTMLSelectElement;
    select.value = '50';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    await vi.waitFor(() => expect(invalidateBoardMock).toHaveBeenCalled());

    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', '', '', null, null, null, null);
  });

  it('restores the previous value when save fails', async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: { body?: string }) => {
      if (url === '/api/user/preferences' && init?.body?.includes('cardsPerLane')) {
        throw new Error('network');
      }
      return { ok: true };
    });
    await renderCustomizationSettings();
    const select = document.getElementById('cardsPerLaneSelect') as HTMLSelectElement;
    select.value = '100';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    await vi.waitFor(() => expect(cardsPerLanePutCalls()).toHaveLength(1));

    expect(setDefaultCardsPerLaneMock).not.toHaveBeenCalled();
    expect(clearBoardPrefetchCacheMock).not.toHaveBeenCalled();
    expect(select.value).toBe('20');
    expect(select.disabled).toBe(false);
  });
});

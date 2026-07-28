// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  loadTagSettingsContentMock,
  syncOpenBoardWrapLanesClassMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  fetchProjectMembersMock: vi.fn(),
  loadTagSettingsContentMock: vi.fn().mockResolvedValue(''),
  syncOpenBoardWrapLanesClassMock: vi.fn(),
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
vi.mock('../core/wrap-lanes-preferences.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../core/wrap-lanes-preferences.js')>();
  return {
    ...actual,
    syncOpenBoardWrapLanesClass: syncOpenBoardWrapLanesClassMock,
  };
});
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
  invalidateBoard: vi.fn(),
  refreshSprintsAndChips: vi.fn(),
  CARDS_PER_LANE_ALLOWED: [20, 50, 75, 100],
  CARDS_PER_LANE_PREFERENCE_KEY: 'cardsPerLane',
  getDefaultCardsPerLane: () => 20,
  setDefaultCardsPerLane: vi.fn(),
  usePreferenceLimitOnNextBoardRequest: vi.fn(),
}));
vi.mock('../views/board-prefetch-cache.js', () => ({
  clearBoardPrefetchCache: vi.fn(),
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

function wrapLanesPutCalls(): unknown[][] {
  return apiFetchMock.mock.calls.filter(
    (call) => call[0] === '/api/user/preferences' && typeof call[1]?.body === 'string' && call[1].body.includes('wrapLanes'),
  );
}

async function renderCustomizationSettings() {
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
  state.setSlug('alpha');
  state.setBoard({
    project: { slug: 'alpha' },
    columnOrder: ['a', 'b', 'c', 'd', 'e', 'f'],
    columns: {},
  } as any);
  state.setProjects(null);
  state.setProjectId(null);
  state.setSettingsProjectId(null);
  state.setSettingsActiveTab('customization');
  state.setBoardMembers([]);
  await settings.renderSettingsModal();
}

describe('settings wrap lanes', () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
    installBaseDOM();
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    syncOpenBoardWrapLanesClassMock.mockReset();
    apiFetchMock.mockResolvedValue({ ok: true });
  });

  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('renders the wrap lanes toggle unchecked by default', async () => {
    await renderCustomizationSettings();
    const toggle = document.getElementById('wrapLanesToggle') as HTMLInputElement;
    expect(toggle).toBeInstanceOf(HTMLInputElement);
    expect(toggle.checked).toBe(false);
  });

  it('persists enabled state and live-updates the open board', async () => {
    await renderCustomizationSettings();
    const toggle = document.getElementById('wrapLanesToggle') as HTMLInputElement;
    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));

    expect(wrapLanesPutCalls()[0]).toEqual([
      '/api/user/preferences',
      {
        method: 'PUT',
        body: JSON.stringify({ key: 'wrapLanes', value: 'true' }),
      },
    ]);
    expect(localStorage.getItem('scrumboy.wrapLanes')).toBe('true');
    expect(syncOpenBoardWrapLanesClassMock).toHaveBeenCalledTimes(1);
  });
});

// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WebPushStatus } from '../types.js';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  loadTagSettingsContentMock,
  windowFetchMock,
  isPushSubscribedMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  fetchProjectMembersMock: vi.fn(),
  loadTagSettingsContentMock: vi.fn().mockResolvedValue(''),
  windowFetchMock: vi.fn().mockResolvedValue({
    ok: false,
    json: async () => ({}),
  }),
  isPushSubscribedMock: vi.fn().mockResolvedValue(false),
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
  renderRealBurndownChart: () => '<div></div>',
  destroyBurndownChart: vi.fn(),
  mountBurndownChart: vi.fn(),
}));

vi.mock('../events.js', () => ({
  emit: vi.fn(),
}));

vi.mock('../sprints.js', () => ({
  normalizeSprints: (value: unknown) => value,
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
  isPushSubscribed: isPushSubscribedMock,
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

async function loadSettingsModule() {
  return import('./settings.js');
}

async function loadStateMutations() {
  return import('../state/mutations.js');
}

async function renderPushSettings(options: {
  pushConfigured?: boolean;
  pushStatus?: WebPushStatus | null;
  role?: 'user' | 'admin' | 'owner';
} = {}) {
  const settings = await loadSettingsModule();
  const state = await loadStateMutations();
  state.setAuthStatusAvailable(true);
  state.setPushConfigured(options.pushConfigured ?? false);
  state.setPushStatus(options.pushStatus ?? null);
  state.setUser({
    id: 1,
    email: 'user@example.com',
    name: 'User',
    systemRole: options.role ?? 'user',
  } as any);
  state.setSlug(null);
  state.setBoard(null);
  state.setProjects(null);
  state.setProjectId(null);
  state.setSettingsProjectId(null);
  state.setSettingsActiveTab('customization');
  state.setBoardMembers([]);
  await settings.renderSettingsModal();
}

describe('settings-push', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    window.history.replaceState({}, '', '/');
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    loadTagSettingsContentMock.mockClear();
    windowFetchMock.mockClear();
    isPushSubscribedMock.mockReset();
    isPushSubscribedMock.mockResolvedValue(false);
    vi.stubGlobal('fetch', windowFetchMock);
  });

  afterEach(() => {
    delete (navigator as Navigator & { serviceWorker?: unknown }).serviceWorker;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.history.replaceState({}, '', '/');
  });

  it('renders the disabled push notice from auth status without probing the vapid route', async () => {
    await renderPushSettings({ pushStatus: { state: 'not_configured', reason: null } });

    expect(windowFetchMock).not.toHaveBeenCalled();
    const notice = document.querySelector('.settings-push-vapid-notice');
    if (!(notice instanceof HTMLElement)) {
      throw new Error('missing push notice');
    }
    expect(notice.textContent ?? '').toContain('SCRUMBOY_VAPID_PUBLIC_KEY');
    const pushToggle = document.getElementById('pushNotifyToggle');
    if (!(pushToggle instanceof HTMLInputElement)) {
      throw new Error('missing push toggle');
    }
    expect(pushToggle.disabled).toBe(true);
  });

  it('renders no warning and keeps the Web Push section enabled when configured', async () => {
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: {} });
    vi.stubGlobal('PushManager', class PushManager {});
    await renderPushSettings({
      pushConfigured: true,
      pushStatus: { state: 'enabled', reason: null },
      role: 'admin',
    });

    expect(document.querySelector('.settings-push-vapid-notice')).toBeNull();
    expect(document.querySelector('.settings-section--push-pwa')?.classList.contains('settings-section--push-pwa-disabled')).toBe(false);
  });

  it.each([
    ['invalid_subscriber', 'Web Push is disabled because SCRUMBOY_VAPID_SUBSCRIBER is invalid.'],
    ['invalid_vapid_public_key', 'Web Push is disabled because SCRUMBOY_VAPID_PUBLIC_KEY is invalid.'],
    ['invalid_vapid_private_key', 'Web Push is disabled because SCRUMBOY_VAPID_PRIVATE_KEY is invalid.'],
    ['initialization_failed', 'Web Push is disabled because initialization failed. Check the server logs.'],
  ] as const)('shows the sanitized %s warning to administrators', async (reason, expected) => {
    await renderPushSettings({
      pushStatus: { state: reason === 'initialization_failed' ? 'unavailable' : 'invalid', reason },
      role: 'admin',
    });

    expect(document.querySelector('.settings-push-vapid-notice')?.textContent).toBe(expected);
  });

  it('shows configuration detail to owners as well as administrators', async () => {
    await renderPushSettings({
      pushStatus: { state: 'invalid', reason: 'invalid_subscriber' },
      role: 'owner',
    });

    expect(document.querySelector('.settings-push-vapid-notice')?.textContent).toBe(
      'Web Push is disabled because SCRUMBOY_VAPID_SUBSCRIBER is invalid.',
    );
  });

  it('hides configuration details from regular users', async () => {
    await renderPushSettings({
      pushStatus: { state: 'invalid', reason: 'invalid_subscriber' },
      role: 'user',
    });

    const warning = document.querySelector('.settings-push-vapid-notice')?.textContent ?? '';
    expect(warning).toBe('Web Push is currently unavailable.');
    expect(warning).not.toContain('SCRUMBOY_VAPID_SUBSCRIBER');
  });

  it('uses a safe generic administrator warning for an unknown future reason', async () => {
    const rawReason = '<configured-value-must-not-render>';
    await renderPushSettings({
      pushStatus: { state: 'invalid', reason: rawReason } as unknown as WebPushStatus,
      role: 'admin',
    });

    const warning = document.querySelector('.settings-push-vapid-notice')?.textContent ?? '';
    expect(warning).toBe('Web Push is disabled because of a server configuration error. Check the server logs.');
    expect(warning).not.toContain(rawReason);
  });
});

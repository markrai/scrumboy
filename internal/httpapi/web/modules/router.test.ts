// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WebPushStatus } from './types.js';

const {
  apiFetchMock,
  renderAuthMock,
  renderResetPasswordMock,
  renderProjectsMock,
  renderDashboardMock,
  renderBoardMock,
  renderNotFoundMock,
  stopBoardEventsMock,
  startGlobalRealtimeMock,
  stopGlobalRealtimeMock,
  initForegroundLifecycleMock,
  hydrateNotificationsForUserMock,
  initNotificationBadgeMock,
  unsubscribeFromPushMock,
  maybeAutoSubscribePushAfterLoginMock,
  loadUserThemeMock,
  applyWallpaperForAuthContextMock,
  loadUserWallpaperMock,
  hydrateVoiceFlowEnabledFromServerMock,
  hydrateVoiceFlowHandsFreeConfirmationFromServerMock,
  hydrateVoiceFlowModeFromServerMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  renderAuthMock: vi.fn(),
  renderResetPasswordMock: vi.fn(),
  renderProjectsMock: vi.fn(),
  renderDashboardMock: vi.fn(),
  renderBoardMock: vi.fn(),
  renderNotFoundMock: vi.fn(),
  stopBoardEventsMock: vi.fn(),
  startGlobalRealtimeMock: vi.fn(),
  stopGlobalRealtimeMock: vi.fn(),
  initForegroundLifecycleMock: vi.fn(),
  hydrateNotificationsForUserMock: vi.fn(),
  initNotificationBadgeMock: vi.fn(),
  unsubscribeFromPushMock: vi.fn().mockResolvedValue(undefined),
  maybeAutoSubscribePushAfterLoginMock: vi.fn(),
  loadUserThemeMock: vi.fn().mockResolvedValue(undefined),
  applyWallpaperForAuthContextMock: vi.fn(),
  loadUserWallpaperMock: vi.fn().mockResolvedValue(undefined),
  hydrateVoiceFlowEnabledFromServerMock: vi.fn(),
  hydrateVoiceFlowHandsFreeConfirmationFromServerMock: vi.fn(),
  hydrateVoiceFlowModeFromServerMock: vi.fn(),
}));

vi.mock('./api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('./views/index.js', () => ({
  renderAuth: renderAuthMock,
  renderResetPassword: renderResetPasswordMock,
  renderProjects: renderProjectsMock,
  renderDashboard: renderDashboardMock,
  renderBoard: renderBoardMock,
  renderNotFound: renderNotFoundMock,
  stopBoardEvents: stopBoardEventsMock,
}));

vi.mock('./core/realtime.js', () => ({
  startGlobalRealtime: startGlobalRealtimeMock,
  stopGlobalRealtime: stopGlobalRealtimeMock,
  initForegroundLifecycle: initForegroundLifecycleMock,
}));

vi.mock('./core/notifications.js', () => ({
  hydrateNotificationsForUser: hydrateNotificationsForUserMock,
  initNotificationBadge: initNotificationBadgeMock,
}));

vi.mock('./core/push.js', () => ({
  unsubscribeFromPush: unsubscribeFromPushMock,
  maybeAutoSubscribePushAfterLogin: maybeAutoSubscribePushAfterLoginMock,
}));

vi.mock('./theme.js', () => ({
  loadUserTheme: loadUserThemeMock,
}));

vi.mock('./wallpaper.js', () => ({
  applyWallpaperForAuthContext: applyWallpaperForAuthContextMock,
  loadUserWallpaper: loadUserWallpaperMock,
}));

vi.mock('./core/voiceflow-preferences.js', () => ({
  hydrateVoiceFlowEnabledFromServer: hydrateVoiceFlowEnabledFromServerMock,
  hydrateVoiceFlowHandsFreeConfirmationFromServer: hydrateVoiceFlowHandsFreeConfirmationFromServerMock,
  hydrateVoiceFlowModeFromServer: hydrateVoiceFlowModeFromServerMock,
  VOICE_FLOW_ENABLED_PREFERENCE_KEY: 'voiceflowEnabled',
  VOICE_FLOW_HANDS_FREE_CONFIRMATION_PREFERENCE_KEY: 'voiceflowHandsFreeConfirmation',
  VOICE_FLOW_MODE_PREFERENCE_KEY: 'voiceflowMode',
}));

function userStatus() {
  return {
    id: 7,
    email: 'ada@example.com',
    name: 'Ada',
    isBootstrap: false,
    systemRole: 'user',
    twoFactorEnabled: false,
  };
}

async function loadRouterModule() {
  return import('./router.js');
}

describe('router push autosubscribe gate', () => {
  beforeEach(() => {
    vi.resetModules();
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    apiFetchMock.mockReset();
    renderAuthMock.mockReset();
    renderResetPasswordMock.mockReset();
    renderProjectsMock.mockReset();
    renderDashboardMock.mockReset();
    renderBoardMock.mockReset();
    renderNotFoundMock.mockReset();
    stopBoardEventsMock.mockReset();
    startGlobalRealtimeMock.mockReset();
    stopGlobalRealtimeMock.mockReset();
    initForegroundLifecycleMock.mockReset();
    hydrateNotificationsForUserMock.mockReset();
    initNotificationBadgeMock.mockReset();
    unsubscribeFromPushMock.mockReset();
    unsubscribeFromPushMock.mockResolvedValue(undefined);
    maybeAutoSubscribePushAfterLoginMock.mockClear();
    loadUserThemeMock.mockClear();
    applyWallpaperForAuthContextMock.mockClear();
    loadUserWallpaperMock.mockClear();
    hydrateVoiceFlowEnabledFromServerMock.mockClear();
    hydrateVoiceFlowHandsFreeConfirmationFromServerMock.mockClear();
    hydrateVoiceFlowModeFromServerMock.mockClear();
  });

  afterEach(() => {
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    vi.restoreAllMocks();
  });

  function installAuthStatus(pushConfigured: boolean, push?: WebPushStatus): void {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/auth/status') {
        return {
          user: userStatus(),
          bootstrapAvailable: false,
          mode: 'full',
          pushConfigured,
          push,
          selfServicePasswordResetEnabled: false,
          oidcEnabled: false,
          localAuthEnabled: true,
          wallEnabled: false,
          markdownNotesEnabled: false,
          mermaidNotesEnabled: false,
        };
      }
      if (url === '/api/me') {
        return userStatus();
      }
      if (url.startsWith('/api/user/preferences?key=')) {
        return {};
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });
  }

  function installSignedOutAuthStatus(selfServicePasswordResetEnabled?: boolean): void {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/auth/status') {
        const status: Record<string, unknown> = {
          user: null,
          bootstrapAvailable: false,
          mode: 'full',
          pushConfigured: false,
          oidcEnabled: false,
          localAuthEnabled: true,
          wallEnabled: false,
          markdownNotesEnabled: false,
          mermaidNotesEnabled: false,
        };
        if (selfServicePasswordResetEnabled !== undefined) {
          status.selfServicePasswordResetEnabled = selfServicePasswordResetEnabled;
        }
        return status;
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });
  }

  it('skips auto-subscribe when auth status says push is not configured', async () => {
    installAuthStatus(false);
    const mod = await loadRouterModule();

    await mod.router();

    expect(maybeAutoSubscribePushAfterLoginMock).not.toHaveBeenCalled();
    expect(renderProjectsMock).toHaveBeenCalledTimes(1);
  });

  it('auto-subscribes after login when auth status says push is configured', async () => {
    installAuthStatus(true);
    const mod = await loadRouterModule();

    await mod.router();

    expect(maybeAutoSubscribePushAfterLoginMock).toHaveBeenCalledTimes(1);
    expect(maybeAutoSubscribePushAfterLoginMock).toHaveBeenCalledWith(7);
    expect(renderProjectsMock).toHaveBeenCalledTimes(1);
  });

  it('hydrates structured Web Push status and clears it on logout', async () => {
    installAuthStatus(false, { state: 'invalid', reason: 'invalid_subscriber' });
    const mod = await loadRouterModule();

    await mod.router();

    const selectors = await import('./state/selectors.js');
    const mutations = await import('./state/mutations.js');
    expect(selectors.getPushStatus()).toEqual({ state: 'invalid', reason: 'invalid_subscriber' });

    mutations.setAuthStatusChecked(false);
    installSignedOutAuthStatus();
    await mod.router();

    expect(selectors.getPushStatus()).toBeNull();
  });

  it('passes the self-service password-reset capability to the signed-out auth view', async () => {
    installSignedOutAuthStatus(true);
    const mod = await loadRouterModule();

    await mod.router();

    expect(renderAuthMock).toHaveBeenCalledWith({
      next: '/',
      bootstrap: false,
      oidcEnabled: false,
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
  });

  it('fails closed when auth status omits the self-service password-reset capability', async () => {
    installSignedOutAuthStatus();
    const mod = await loadRouterModule();

    await mod.router();

    expect(renderAuthMock).toHaveBeenCalledWith(expect.objectContaining({
      selfServicePasswordResetEnabled: false,
    }));
  });

  it('passes the capability to the board-401 auth fallback', async () => {
    window.history.replaceState({}, '', '/sample-board');
    installSignedOutAuthStatus(true);
    renderBoardMock.mockRejectedValueOnce(Object.assign(new Error('unauthorized'), { status: 401 }));
    const mod = await loadRouterModule();

    await mod.router();

    expect(renderAuthMock).toHaveBeenCalledWith({
      next: '/sample-board',
      bootstrap: false,
      oidcEnabled: false,
      localAuthEnabled: true,
      selfServicePasswordResetEnabled: true,
    });
  });

	it('does not render the direct local reset page when local authentication is disabled', async () => {
	  window.history.replaceState({}, '', '/auth/reset-password?token=secret');
	  apiFetchMock.mockImplementation(async (url: string) => {
	    if (url === '/api/auth/status') return { user: null, bootstrapAvailable: false, mode: 'full', oidcEnabled: true, localAuthEnabled: false, selfServicePasswordResetEnabled: false };
	    throw new Error(`unexpected apiFetch url: ${url}`);
	  });
	  const mod = await loadRouterModule();
	  await mod.router();
	  expect(renderResetPasswordMock).not.toHaveBeenCalled();
	  expect(renderAuthMock).toHaveBeenCalledWith(expect.objectContaining({ oidcEnabled: true, localAuthEnabled: false, selfServicePasswordResetEnabled: false }));
	});
});

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

describe('router cold-start boardData handoff', () => {
  const staleBoard = {
    project: { id: 1, slug: 'alpha', name: 'Alpha' },
    columns: { backlog: [] },
    columnsMeta: {},
    tags: [],
    workflow: [],
  };

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
    renderBoardMock.mockResolvedValue(undefined);
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
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/auth/status') {
        return {
          user: userStatus(),
          bootstrapAvailable: false,
          mode: 'full',
          pushConfigured: false,
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
  });

  afterEach(() => {
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('ignores stale history.state.boardData on cold document load (F5)', async () => {
    window.history.replaceState({ boardData: staleBoard }, '', '/alpha');
    const mod = await loadRouterModule();

    await mod.router();

    expect(renderBoardMock).toHaveBeenCalledTimes(1);
    const opts = renderBoardMock.mock.calls[0][9];
    expect(opts?.prefetchedBoard).toBeUndefined();
    expect((window.history.state as { boardData?: unknown } | null)?.boardData).toBeUndefined();
  });

  it('still uses history.state.boardData for same-session navigations after cold start', async () => {
    window.history.replaceState({}, '', '/');
    const mod = await loadRouterModule();
    await mod.router();
    expect(renderProjectsMock).toHaveBeenCalledTimes(1);

    window.history.pushState({ boardData: staleBoard }, '', '/alpha');
    await mod.router();

    expect(renderBoardMock).toHaveBeenCalledTimes(1);
    const opts = renderBoardMock.mock.calls[0][9];
    expect(opts?.prefetchedBoard).toEqual(staleBoard);
  });
});

describe('router wrap lanes hydration', () => {
  function userBob() {
    return {
      id: 8,
      email: 'bob@example.com',
      name: 'Bob',
      isBootstrap: false,
      systemRole: 'user',
      twoFactorEnabled: false,
    };
  }

  function installSignedInAuth(user: ReturnType<typeof userStatus>, wrapLanesValue: string): void {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/auth/status') {
        return {
          user,
          bootstrapAvailable: false,
          mode: 'full',
          pushConfigured: false,
          selfServicePasswordResetEnabled: false,
          oidcEnabled: false,
          localAuthEnabled: true,
          wallEnabled: false,
          markdownNotesEnabled: false,
          mermaidNotesEnabled: false,
        };
      }
      if (url === '/api/me') {
        return user;
      }
      if (url.includes('key=wrapLanes')) {
        return { value: wrapLanesValue };
      }
      if (url.startsWith('/api/user/preferences?key=')) {
        return { value: '' };
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });
  }

  beforeEach(() => {
    vi.resetModules();
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    apiFetchMock.mockReset();
    renderProjectsMock.mockReset();
    renderProjectsMock.mockResolvedValue(undefined);
    loadUserThemeMock.mockClear();
    applyWallpaperForAuthContextMock.mockClear();
    loadUserWallpaperMock.mockClear();
  });

  afterEach(() => {
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('resets stale local true when signed-in server preference is missing', async () => {
    const prefs = await import('./core/wrap-lanes-preferences.js');
    localStorage.setItem(prefs.WRAP_LANES_STORAGE_KEY, 'true');
    installSignedInAuth(userBob(), '');
    const mod = await loadRouterModule();

    await mod.router();

    expect(prefs.getWrapLanesPreference()).toBe(false);
  });

  it('does not carry wrap lanes from user A to user B on login', async () => {
    const prefs = await import('./core/wrap-lanes-preferences.js');
    localStorage.setItem(prefs.WRAP_LANES_STORAGE_KEY, 'true');
    installSignedInAuth(userStatus(), 'true');
    const mod = await loadRouterModule();
    await mod.router();
    expect(prefs.getWrapLanesPreference()).toBe(true);

    const mutations = await import('./state/mutations.js');
    mutations.setAuthStatusChecked(false);
    installSignedInAuth(userBob(), '');
    await mod.router();

    expect(prefs.getWrapLanesPreference()).toBe(false);
  });
});

describe('router board todo sort hydration', () => {
  function userBob() {
    return {
      id: 8,
      email: 'bob@example.com',
      name: 'Bob',
      isBootstrap: false,
      systemRole: 'user',
      twoFactorEnabled: false,
    };
  }

  function installSignedInAuth(user: ReturnType<typeof userStatus>, boardTodoSortValue: string): void {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/auth/status') {
        return {
          user,
          bootstrapAvailable: false,
          mode: 'full',
          pushConfigured: false,
          selfServicePasswordResetEnabled: false,
          oidcEnabled: false,
          localAuthEnabled: true,
          wallEnabled: false,
          markdownNotesEnabled: false,
          mermaidNotesEnabled: false,
        };
      }
      if (url === '/api/me') {
        return user;
      }
      if (url.includes('key=boardTodoSort')) {
        return { value: boardTodoSortValue };
      }
      if (url.startsWith('/api/user/preferences?key=')) {
        return { value: '' };
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });
  }

  function installAnonymousAuth(): void {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/auth/status') {
        return {
          user: null,
          bootstrapAvailable: false,
          mode: 'anonymous',
          pushConfigured: false,
          selfServicePasswordResetEnabled: false,
          oidcEnabled: false,
          localAuthEnabled: true,
          wallEnabled: false,
          markdownNotesEnabled: false,
          mermaidNotesEnabled: false,
        };
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });
  }

  beforeEach(() => {
    vi.resetModules();
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    apiFetchMock.mockReset();
    renderProjectsMock.mockReset();
    renderProjectsMock.mockResolvedValue(undefined);
    renderBoardMock.mockReset();
    renderBoardMock.mockResolvedValue(undefined);
    loadUserThemeMock.mockClear();
    applyWallpaperForAuthContextMock.mockClear();
    loadUserWallpaperMock.mockClear();
  });

  afterEach(() => {
    window.history.replaceState({}, '', '/');
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('applies saved newest to a board URL with no sort before render', async () => {
    window.history.replaceState({}, '', '/alpha');
    installSignedInAuth(userStatus(), 'newest');
    const mod = await loadRouterModule();

    await mod.router();

    expect(new URL(window.location.href).searchParams.get('sort')).toBe('newest');
    expect(renderBoardMock).toHaveBeenCalledTimes(1);
    expect(renderBoardMock.mock.calls[0][5]).toBe('newest');
  });

  it('applies saved oldest to a board URL with no sort before render', async () => {
    window.history.replaceState({}, '', '/alpha');
    installSignedInAuth(userStatus(), 'oldest');
    const mod = await loadRouterModule();

    await mod.router();

    expect(new URL(window.location.href).searchParams.get('sort')).toBe('oldest');
    expect(renderBoardMock.mock.calls[0][5]).toBe('oldest');
  });

  it('leaves a board URL without sort when the saved preference is default', async () => {
    window.history.replaceState({}, '', '/alpha');
    installSignedInAuth(userStatus(), 'default');
    const mod = await loadRouterModule();

    await mod.router();

    expect(new URL(window.location.href).searchParams.get('sort')).toBeNull();
    expect(renderBoardMock.mock.calls[0][5]).toBeNull();
  });

  it('lets an explicit sort=oldest override a saved newest without overwriting the preference', async () => {
    window.history.replaceState({}, '', '/alpha?sort=oldest');
    installSignedInAuth(userStatus(), 'newest');
    const mod = await loadRouterModule();

    await mod.router();

    const prefs = await import('./core/board-sort-preferences.js');
    expect(new URL(window.location.href).searchParams.get('sort')).toBe('oldest');
    expect(renderBoardMock.mock.calls[0][5]).toBe('oldest');
    expect(prefs.getBoardTodoSortPreference()).toBe('newest');
    expect(apiFetchMock.mock.calls.some((call) => call[0] === '/api/user/preferences' && call[1]?.method === 'PUT')).toBe(false);
  });

  it('does not let an invalid sort query override a saved newest preference', async () => {
    window.history.replaceState({}, '', '/alpha?sort=bogus');
    installSignedInAuth(userStatus(), 'newest');
    const mod = await loadRouterModule();

    await mod.router();

    const prefs = await import('./core/board-sort-preferences.js');
    expect(new URL(window.location.href).searchParams.get('sort')).toBe('newest');
    expect(renderBoardMock.mock.calls[0][5]).toBe('newest');
    expect(prefs.getBoardTodoSortPreference()).toBe('newest');
    expect(apiFetchMock.mock.calls.some((call) => call[0] === '/api/user/preferences' && call[1]?.method === 'PUT')).toBe(false);
  });

  it('leaves an invalid sort query alone when the saved preference is default', async () => {
    window.history.replaceState({}, '', '/alpha?sort=bogus');
    installSignedInAuth(userStatus(), 'default');
    const mod = await loadRouterModule();

    await mod.router();

    const prefs = await import('./core/board-sort-preferences.js');
    expect(new URL(window.location.href).searchParams.get('sort')).toBe('bogus');
    expect(renderBoardMock.mock.calls[0][5]).toBe('bogus');
    expect(prefs.getBoardTodoSortPreference()).toBe('default');
    expect(apiFetchMock.mock.calls.some((call) => call[0] === '/api/user/preferences' && call[1]?.method === 'PUT')).toBe(false);
  });

  it('keeps URL-only sort behavior when there is no authenticated user', async () => {
    const prefs = await import('./core/board-sort-preferences.js');
    localStorage.setItem(prefs.BOARD_TODO_SORT_STORAGE_KEY, 'newest');
    window.history.replaceState({}, '', '/alpha');
    installAnonymousAuth();
    const mod = await loadRouterModule();

    await mod.router();

    expect(new URL(window.location.href).searchParams.get('sort')).toBeNull();
    expect(renderBoardMock.mock.calls[0][5]).toBeNull();
    expect(apiFetchMock.mock.calls.some((call) => typeof call[0] === 'string' && call[0].includes('key=boardTodoSort'))).toBe(false);
  });

  it('does not carry board todo sort from user A to user B on login', async () => {
    const prefs = await import('./core/board-sort-preferences.js');
    localStorage.setItem(prefs.BOARD_TODO_SORT_STORAGE_KEY, 'newest');
    installSignedInAuth(userStatus(), 'newest');
    const mod = await loadRouterModule();
    await mod.router();
    expect(prefs.getBoardTodoSortPreference()).toBe('newest');

    const mutations = await import('./state/mutations.js');
    mutations.setAuthStatusChecked(false);
    installSignedInAuth(userBob(), '');
    window.history.replaceState({}, '', '/alpha');
    await mod.router();

    expect(prefs.getBoardTodoSortPreference()).toBe('default');
    expect(new URL(window.location.href).searchParams.get('sort')).toBeNull();
    expect(renderBoardMock.mock.calls.at(-1)?.[5]).toBeNull();
  });
});

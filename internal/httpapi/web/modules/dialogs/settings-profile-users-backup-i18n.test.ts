// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enCatalog from '../i18n/locales/en.json';
import deCatalog from '../i18n/locales/de.json';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  showToastMock,
  showConfirmDialogMock,
  confirmDeleteMock,
  isPushSubscribedMock,
  subscribeToPushMock,
  unsubscribeFromPushMock,
  requestDesktopNotificationPermissionMock,
  setVoiceFlowEnabledPreferenceMock,
  state,
} = vi.hoisted(() => {
  const state = {
    voiceFlowEnabled: false,
    pushConfigured: false,
  };
  return {
    apiFetchMock: vi.fn(),
    fetchProjectMembersMock: vi.fn(),
    showToastMock: vi.fn(),
    showConfirmDialogMock: vi.fn().mockResolvedValue(false),
    confirmDeleteMock: vi.fn().mockResolvedValue(false),
    isPushSubscribedMock: vi.fn().mockResolvedValue(false),
    subscribeToPushMock: vi.fn().mockResolvedValue(true),
    unsubscribeFromPushMock: vi.fn().mockResolvedValue(undefined),
    requestDesktopNotificationPermissionMock: vi.fn(),
    setVoiceFlowEnabledPreferenceMock: vi.fn((value: boolean) => {
      state.voiceFlowEnabled = value;
    }),
    state,
  };
});

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
  showToast: showToastMock,
  getAppVersion: () => 'test-version',
  showConfirmDialog: showConfirmDialogMock,
  confirmDelete: confirmDeleteMock,
  isAnonymousBoard: () => false,
  renderUserAvatar: (_user: unknown, opts?: { id?: string; ariaLabel?: string }) =>
    `<button class="user-avatar" id="${opts?.id ?? 'userAvatarBtn'}" aria-label="${opts?.ariaLabel ?? ''}"></button>`,
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
vi.mock('../sprints.js', () => ({ normalizeSprints: (value: unknown) => value }));

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
  requestDesktopNotificationPermission: requestDesktopNotificationPermissionMock,
  getDesktopNotificationStatusKind: () => 'default',
  getDesktopNotificationStatusDescription: () => '',
}));

vi.mock('../core/push.js', () => ({
  isPushSubscribed: isPushSubscribedMock,
  subscribeToPush: subscribeToPushMock,
  unsubscribeFromPush: unsubscribeFromPushMock,
}));

vi.mock('../core/voiceflow-preferences.js', () => ({
  getVoiceFlowEnabledPreference: () => state.voiceFlowEnabled,
  setVoiceFlowEnabledPreference: setVoiceFlowEnabledPreferenceMock,
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
  loadTagSettingsContent: vi.fn().mockResolvedValue(''),
}));

vi.mock('./settings-sprints.js', () => ({
  bindSprintsTabInteractions: vi.fn(),
  renderSprintsTabContent: vi.fn().mockResolvedValue(''),
  refreshSprintDateLabels: vi.fn(),
}));

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
  };
  return vi.fn(async (locale: string) => catalogs[locale]);
}

async function flushPromises(count = 12): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function initI18nFor(locale: 'en' | 'de' = 'en') {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({ locale, loadLocale: loader() });
  return i18n;
}

async function setupSettingsView(options: {
  activeTab: string;
  user?: Record<string, unknown> | null;
  authStatusAvailable?: boolean;
  pushConfigured?: boolean;
	oidcEnabled?: boolean;
	localAuthEnabled?: boolean;
}) {
  const i18n = await initI18nFor('en');
  const settings = await import('./settings.js');
  const mutations = await import('../state/mutations.js');
  state.pushConfigured = options.pushConfigured ?? false;
  mutations.setAuthStatusAvailable(options.authStatusAvailable ?? true);
  mutations.setPushConfigured(options.pushConfigured ?? false);
	mutations.setOidcEnabled(options.oidcEnabled ?? false);
	mutations.setLocalAuthEnabled(options.localAuthEnabled ?? true);
  mutations.setUser((options.user as any) ?? null);
  mutations.setSlug(null);
  mutations.setBoard(null);
  mutations.setProjects(null);
  mutations.setProjectId(null);
  mutations.setSettingsProjectId(null);
  mutations.setSettingsActiveTab(options.activeTab);
  mutations.setBoardMembers([]);
  mutations.setBackupData(null);
  mutations.setBackupPreview(null);
  mutations.setTrelloImportData(null);
  mutations.setTrelloImportPreview(null);
  mutations.setTrelloImportResult(null);
  await settings.renderSettingsModal({ skipProfileRefetch: true });
  const dialog = document.getElementById('settingsDialog') as HTMLDialogElement | null;
  if (dialog) {
    dialog.setAttribute('open', '');
    dialog.open = true;
  }
  return { i18n, settings, mutations };
}

describe('settings i18n (profile / users / backup / customization)', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    window.history.replaceState({}, '', '/');
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    showToastMock.mockReset();
    showConfirmDialogMock.mockReset();
    showConfirmDialogMock.mockResolvedValue(false);
    confirmDeleteMock.mockReset();
    confirmDeleteMock.mockResolvedValue(false);
    isPushSubscribedMock.mockReset();
    isPushSubscribedMock.mockResolvedValue(false);
    subscribeToPushMock.mockReset();
    subscribeToPushMock.mockResolvedValue(true);
    unsubscribeFromPushMock.mockReset();
    requestDesktopNotificationPermissionMock.mockReset();
    setVoiceFlowEnabledPreferenceMock.mockClear();
    state.voiceFlowEnabled = false;
    state.pushConfigured = false;
  });

  afterEach(async () => {
    document.querySelectorAll('body > dialog').forEach((d) => {
      if (d.id !== 'settingsDialog') d.remove();
    });
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
    window.history.replaceState({}, '', '/');
  });

  // ---- Profile ----------------------------------------------------------

	it('shows configured-provider authentication state, recovery warning, and eligible method actions', async () => {
	  const ssoOwner = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: false, hasLocalPassword: false, oidcLinked: true };
	  await setupSettingsView({ activeTab: 'profile', user: ssoOwner, oidcEnabled: true, localAuthEnabled: true });
	  const content = document.getElementById('settingsTabContent');
	  expect(content?.textContent).toContain('SSO');
	  expect(content?.textContent).toContain('relies on the external SSO provider');
	  expect(document.getElementById('setScrumboyPasswordBtn')).not.toBeNull();
	  expect(document.getElementById('connectSSOBtn')).toBeNull();
	  expect(content?.textContent).toContain('MFA for normal SSO sign-in is controlled by the configured identity provider');

	  const historical = { ...ssoOwner, hasLocalPassword: true, oidcLinked: false };
	  const mutations = await import('../state/mutations.js');
	  mutations.setUser(historical as any);
	  await (await import('./settings.js')).renderSettingsModal({ skipProfileRefetch: true });
	  expect(document.getElementById('connectSSOBtn')).not.toBeNull();

	  mutations.setUser({ ...historical, hasLocalPassword: false } as any);
	  await (await import('./settings.js')).renderSettingsModal({ skipProfileRefetch: true });
	  expect(document.getElementById('connectSSOBtn')).toBeNull();
	  expect(document.getElementById('settingsTabContent')?.textContent).toContain('Connect SSO');
	  expect(document.getElementById('settingsTabContent')?.textContent).toContain('Set or recover a Scrumboy password');
	});

	it('scopes the no-effective-method warning to this owner account', async () => {
	  const strandedOwner = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: false, hasLocalPassword: false, oidcLinked: false };
	  await setupSettingsView({ activeTab: 'profile', user: strandedOwner, oidcEnabled: true, localAuthEnabled: true });
	  const text = document.getElementById('settingsTabContent')?.textContent ?? '';
	  expect(text).toContain('This owner account has no effective sign-in method');
	  expect(text).not.toContain('No effective owner login method is available');
	});

	it('makes the local-auth-disabled owner warning conditional on SSO becoming unavailable', async () => {
	  const ssoOnlyOwner = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: false, hasLocalPassword: false, oidcLinked: true };
	  await setupSettingsView({ activeTab: 'profile', user: ssoOnlyOwner, oidcEnabled: true, localAuthEnabled: false });
	  const text = document.getElementById('settingsTabContent')?.textContent ?? '';
	  expect(text).toContain('If SSO becomes unavailable');
	  expect(text).not.toMatch(/disabled\. Recovery requires host access/);
	});

	it('relocalizes first-password dialog without clearing typed secrets and releases it on close', async () => {
	  const user = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: true, hasLocalPassword: false, oidcLinked: true };
	  apiFetchMock.mockImplementation(async (url: string) => {
	    if (url === '/api/auth/oidc/set-password/status') return { authorized: true, localAuthEnabled: true };
	    return undefined;
	  });
	  const { i18n } = await setupSettingsView({ activeTab: 'profile', user, oidcEnabled: true });
	  document.getElementById('setScrumboyPasswordBtn')?.dispatchEvent(new Event('click'));
	  await flushPromises();
	  const dialog = document.querySelector('body > dialog.dialog') as HTMLDialogElement;
	  const password = dialog.querySelector('#setScrumboyPasswordNew') as HTMLInputElement;
	  const factor = dialog.querySelector('#setScrumboyPassword2FA') as HTMLInputElement;
	  password.value = 'TypedSecret123!'; factor.value = '123456';
	  await i18n.setLocale('de'); await flushPromises();
	  expect(password.value).toBe('TypedSecret123!');
	  expect(factor.value).toBe('123456');
	  dialog.querySelector('#setScrumboyPasswordCancel')?.dispatchEvent(new Event('click'));
	  expect(document.body.contains(dialog)).toBe(false);
	});

	it('clears and removes authentication-method secrets when Escape cancels a dialog', async () => {
	  const ssoUser = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: true, hasLocalPassword: false, oidcLinked: true };
	  apiFetchMock.mockImplementation(async (url: string) => {
	    if (url === '/api/auth/oidc/set-password/status') return { authorized: true, localAuthEnabled: true };
	    return undefined;
	  });
	  await setupSettingsView({ activeTab: 'profile', user: ssoUser, oidcEnabled: true });
	  document.getElementById('setScrumboyPasswordBtn')?.dispatchEvent(new Event('click'));
	  await flushPromises();
	  const passwordDialog = document.querySelector('body > dialog.dialog') as HTMLDialogElement;
	  const newPassword = passwordDialog.querySelector('#setScrumboyPasswordNew') as HTMLInputElement;
	  const passwordFactor = passwordDialog.querySelector('#setScrumboyPassword2FA') as HTMLInputElement;
	  newPassword.value = 'TypedSecret123!';
	  passwordFactor.value = 'ABCD-EFGH';
	  passwordDialog.dispatchEvent(new Event('cancel', { cancelable: true }));
	  expect(newPassword.value).toBe('');
	  expect(passwordFactor.value).toBe('');
	  expect(document.body.contains(passwordDialog)).toBe(false);

	  const localUser = { ...ssoUser, hasLocalPassword: true, oidcLinked: false };
	  const mutations = await import('../state/mutations.js');
	  mutations.setUser(localUser as any);
	  await (await import('./settings.js')).renderSettingsModal({ skipProfileRefetch: true });
	  document.getElementById('connectSSOBtn')?.dispatchEvent(new Event('click'));
	  const linkDialog = document.querySelector('body > dialog.dialog') as HTMLDialogElement;
	  const currentPassword = linkDialog.querySelector('#connectSSOPassword') as HTMLInputElement;
	  const linkFactor = linkDialog.querySelector('#connectSSO2FA') as HTMLInputElement;
	  currentPassword.value = 'CurrentSecret123!';
	  linkFactor.value = '123456';
	  linkDialog.dispatchEvent(new Event('cancel', { cancelable: true }));
	  expect(currentPassword.value).toBe('');
	  expect(linkFactor.value).toBe('');
	  expect(document.body.contains(linkDialog)).toBe(false);
	});

  it('relocalizes profile tab chrome on locale change without /api/me refetch and preserves raw identity values', async () => {
    const user = { id: 42, name: 'Ada Lovelace', email: 'ada@example.com', systemRole: 'owner', twoFactorEnabled: false };
    const { i18n } = await setupSettingsView({ activeTab: 'profile', user });

    const tabContent = document.getElementById('settingsTabContent');
    expect(tabContent?.querySelector('.settings-section__title')?.textContent).toBe('Profile');
    expect(document.getElementById('enable2FABtn')?.textContent).toBe('Enable 2FA');
    expect(tabContent?.textContent).toContain('Ada Lovelace');
    expect(tabContent?.textContent).toContain('ada@example.com');

    apiFetchMock.mockClear();
    await i18n.setLocale('de');
    await flushPromises();

    expect(tabContent?.querySelector('.settings-section__title')?.textContent).toBe('Profil');
    expect(document.getElementById('enable2FABtn')?.textContent).toBe('2FA aktivieren');
    expect(document.getElementById('profileAvatarBtn')?.getAttribute('aria-label')).toBe('Avatar ändern');
    // Raw identity values stay exactly as backend provided.
    expect(tabContent?.textContent).toContain('Ada Lovelace');
    expect(tabContent?.textContent).toContain('ada@example.com');
    expect(tabContent?.textContent).toContain('42');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('relocalizes an open Enable-2FA dialog in place without resetting typed code or refetching /api/me', async () => {
    const user = { id: 1, name: 'Grace', email: 'grace@example.com', systemRole: 'owner', twoFactorEnabled: false };
    const { i18n } = await setupSettingsView({ activeTab: 'profile', user });

    apiFetchMock.mockResolvedValueOnce({
      setupToken: 'tok',
      otpauthUri: 'otpauth://x',
      manualEntryKey: 'ABCD-EFGH',
    });
    document.getElementById('enable2FABtn')?.dispatchEvent(new Event('click'));
    await flushPromises();

    const dialog = document.querySelector('body > dialog.dialog') as HTMLDialogElement | null;
    expect(dialog).not.toBeNull();
    expect(dialog?.querySelector('.dialog__title')?.textContent).toBe('Enable two-factor authentication');
    const codeInput = document.getElementById('enable2FACode') as HTMLInputElement;
    codeInput.value = '123456';

    apiFetchMock.mockClear();
    await i18n.setLocale('de');
    await flushPromises();

    expect(dialog?.querySelector('.dialog__title')?.textContent).toBe('Zwei-Faktor-Authentifizierung aktivieren');
    expect((document.getElementById('enable2FACode') as HTMLInputElement).value).toBe('123456');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('releases the Enable-2FA locale listener on native cancel/close so closed dialogs do not relocalize', async () => {
    const user = { id: 1, name: 'Grace', email: 'grace@example.com', systemRole: 'owner', twoFactorEnabled: false };
    const { i18n } = await setupSettingsView({ activeTab: 'profile', user });

    apiFetchMock.mockResolvedValueOnce({ setupToken: 'tok', otpauthUri: 'otpauth://x', manualEntryKey: 'KEY' });
    document.getElementById('enable2FABtn')?.dispatchEvent(new Event('click'));
    await flushPromises();

    const dialog = document.querySelector('body > dialog.dialog') as HTMLDialogElement | null;
    expect(dialog?.querySelector('.dialog__title')?.textContent).toBe('Enable two-factor authentication');

    dialog?.dispatchEvent(new Event('cancel'));
    dialog?.close();
    expect(dialog?.open).toBe(false);

    await i18n.setLocale('de');
    await flushPromises();

    expect(dialog?.querySelector('.dialog__title')?.textContent).toBe('Enable two-factor authentication');
  });

  // ---- Users ------------------------------------------------------------

  it('relocalizes users table chrome/actions on locale change without /api/admin/users refetch and preserves raw rows', async () => {
    const owner = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: false };
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/admin/users') {
        return [
		  { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', hasLocalPassword: true, oidcLinked: true },
		  { id: 2, name: 'Bob Member', email: 'bob@example.com', systemRole: 'user', hasLocalPassword: true, oidcLinked: false },
		  { id: 3, name: 'SSO Member', email: 'sso@example.com', systemRole: 'user', hasLocalPassword: false, oidcLinked: true },
        ];
      }
      return undefined;
    });
    const { i18n } = await setupSettingsView({ activeTab: 'users', user: owner });

    const tabContent = document.getElementById('settingsTabContent');
    expect(tabContent?.querySelector('.settings-section__title')?.textContent).toBe('User Management');
    expect(tabContent?.querySelector('[data-action="promote"]')?.textContent).toBe('Promote');
    expect(tabContent?.textContent).toContain('Bob Member');
    expect(tabContent?.textContent).toContain('bob@example.com');
	expect(tabContent?.textContent).toContain('Local password + SSO');
	expect(tabContent?.textContent).toContain('Local password');
	expect(tabContent?.querySelectorAll('[data-action="password"]')).toHaveLength(1);

    apiFetchMock.mockClear();
    await i18n.setLocale('de');
    await flushPromises();

    expect(tabContent?.querySelector('.settings-section__title')?.textContent).toBe('Benutzerverwaltung');
    expect(tabContent?.querySelector('[data-action="promote"]')?.textContent).toBe('Hochstufen');
    expect(tabContent?.querySelector('[data-action="delete"]')?.textContent).toBe('Löschen');
    // Raw names/emails/role values unchanged.
    expect(tabContent?.textContent).toContain('Bob Member');
    expect(tabContent?.textContent).toContain('bob@example.com');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('hides admin Scrumboy-password actions when local authentication is disabled', async () => {
    const owner = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: false };
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/admin/users') {
        return [
          { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', hasLocalPassword: true, oidcLinked: true },
          { id: 2, name: 'Local User', email: 'local@example.com', systemRole: 'user', hasLocalPassword: true, oidcLinked: false },
        ];
      }
      return undefined;
    });
    await setupSettingsView({ activeTab: 'users', user: owner, localAuthEnabled: false, oidcEnabled: true });
    expect(document.querySelectorAll('[data-action="password"]')).toHaveLength(0);
    expect(document.getElementById('settingsTabContent')?.textContent).toContain('Local password');
  });

  it('relocalizes an open Create-User dialog in place, preserving typed fields and password visibility', async () => {
    const owner = { id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner', twoFactorEnabled: false };
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === '/api/admin/users') return [{ id: 1, name: 'Owner', email: 'owner@example.com', systemRole: 'owner' }];
      return undefined;
    });
    const { i18n } = await setupSettingsView({ activeTab: 'users', user: owner });

    document.getElementById('createUserBtn')?.dispatchEvent(new Event('click'));
    await flushPromises();

    const dialog = document.querySelector('body > dialog.dialog') as HTMLDialogElement | null;
    expect(dialog?.querySelector('.dialog__title')?.textContent).toBe('Create User');
    const emailInput = document.getElementById('createUserEmail') as HTMLInputElement;
    const passwordInput = document.getElementById('createUserPassword') as HTMLInputElement;
    const toggle = document.getElementById('createUserPasswordToggle') as HTMLElement;
    emailInput.value = 'new@example.com';
    toggle.dispatchEvent(new Event('click'));
    expect(passwordInput.type).toBe('text');
    expect(toggle.getAttribute('aria-label')).toBe('Hide password');

    apiFetchMock.mockClear();
    await i18n.setLocale('de');
    await flushPromises();

    expect(dialog?.querySelector('.dialog__title')?.textContent).toBe('Benutzer erstellen');
    expect((document.getElementById('createUserEmail') as HTMLInputElement).value).toBe('new@example.com');
    // Visibility state preserved; label localized to the hide variant.
    expect((document.getElementById('createUserPassword') as HTMLInputElement).type).toBe('text');
    expect(toggle.getAttribute('aria-label')).toBe('Passwort verbergen');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  // ---- Backup / Trello --------------------------------------------------

  it('rebuilds backup preview/warnings from stored state on locale change while preserving import mode, REPLACE input, and making no API calls', async () => {
    const { i18n, mutations } = await setupSettingsView({ activeTab: 'backup', user: null, authStatusAvailable: true });
    await flushPromises();

    // Seed a stored preview and pick replace mode + a typed REPLACE confirmation.
    mutations.setBackupPreview({
      projects: 3,
      todos: 12,
      tags: 4,
      links: 2,
      willDelete: 1,
      warnings: ['Project "Legacy" will be overwritten'],
    });
    const replaceRadio = document.querySelector('input[name="importMode"][value="replace"]') as HTMLInputElement;
    replaceRadio.checked = true;
    const confirmInput = document.getElementById('backupConfirmationInput') as HTMLInputElement;
    confirmInput.value = 'REPLACE';

    apiFetchMock.mockClear();
    await i18n.setLocale('de');
    await flushPromises();

    const previewText = document.getElementById('backupPreview')?.textContent ?? '';
    const warningsText = document.getElementById('backupWarnings')?.textContent ?? '';
    expect(previewText).toContain('Vorschau:');
    expect(previewText).toContain('Projekte: 3');
    expect(warningsText).toContain('Warnungen:');
    // Raw backend warning text unchanged.
    expect(warningsText).toContain('Project "Legacy" will be overwritten');
    // Selected import mode + typed REPLACE confirmation survive.
    expect((document.querySelector('input[name="importMode"][value="replace"]') as HTMLInputElement).checked).toBe(true);
    expect((document.getElementById('backupConfirmationInput') as HTMLInputElement).value).toBe('REPLACE');
    // Localized REPLACE sentinel is NOT translated.
    expect(enCatalog['settings.backup.import.confirmPlaceholder']).toContain('REPLACE');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('rebuilds the Trello import result from stored state on locale change while keeping raw payload values', async () => {
    const { i18n, mutations } = await setupSettingsView({ activeTab: 'backup', user: null, authStatusAvailable: true });
    await flushPromises();

    mutations.setTrelloImportResult({
      project: { id: 9, name: 'Marketing Board', slug: 'marketing-board' },
      summary: { todos: 5, labels: 3 },
      warnings: [],
    });

    apiFetchMock.mockClear();
    await i18n.setLocale('de');
    await flushPromises();

    const resultEl = document.getElementById('trelloImportResult');
    expect(resultEl?.textContent).toContain('Import abgeschlossen');
    expect(resultEl?.querySelector('a')?.getAttribute('href')).toBe('/marketing-board');
    expect(resultEl?.querySelector('a')?.textContent).toBe('Marketing Board');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  // ---- Customization residuals (VoiceFlow + Push) -----------------------

  it('relocalizes VoiceFlow + Push/PWA copy on locale change while preserving toggle state and triggering no push/preference side effects', async () => {
    const user = { id: 1, name: 'Eve', email: 'eve@example.com', systemRole: 'owner', twoFactorEnabled: false };
    const { i18n } = await setupSettingsView({ activeTab: 'customization', user, pushConfigured: true });
    await flushPromises();

    const voiceFlowTitle = document.querySelector('#settingsCustomizationContent .settings-section__title[data-i18n-text="settings.customization.voiceFlow.title"]');
    const pushTitle = document.querySelector('[data-i18n-text="settings.customization.push.title"]');
    expect(voiceFlowTitle?.textContent).toBe('VoiceFlow');
    expect(pushTitle?.textContent).toBe('Background notifications (PWA)');

    const voiceToggle = document.getElementById('voiceFlowEnabledToggle') as HTMLInputElement;
    voiceToggle.checked = true;

    isPushSubscribedMock.mockClear();
    subscribeToPushMock.mockClear();
    unsubscribeFromPushMock.mockClear();
    requestDesktopNotificationPermissionMock.mockClear();
    setVoiceFlowEnabledPreferenceMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(document.querySelector('[data-i18n-text="settings.customization.voiceFlow.title"]')?.textContent).toBe('VoiceFlow');
    expect(document.querySelector('[data-i18n-text="settings.customization.voiceFlow.toggleLabel"]')?.textContent)
      .toBe('Verwende Sprachbefehle, um Todos zu verschieben, zu erstellen und zu löschen.');
    expect(document.querySelector('[data-i18n-text="settings.customization.push.title"]')?.textContent)
      .toBe('Hintergrund-Benachrichtigungen (PWA)');
    expect(document.querySelector('[data-i18n-text="settings.customization.push.toggleLabel"]')?.textContent)
      .toBe('Web Push auf diesem Gerät');
    // Toggle state preserved; no push/preference side effects from locale change.
    expect((document.getElementById('voiceFlowEnabledToggle') as HTMLInputElement).checked).toBe(true);
    expect(isPushSubscribedMock).not.toHaveBeenCalled();
    expect(subscribeToPushMock).not.toHaveBeenCalled();
    expect(unsubscribeFromPushMock).not.toHaveBeenCalled();
    expect(requestDesktopNotificationPermissionMock).not.toHaveBeenCalled();
    expect(setVoiceFlowEnabledPreferenceMock).not.toHaveBeenCalled();
  });
});

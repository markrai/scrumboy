// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { EmailNotifyPreferenceState, WebPushStatus } from '../types.js';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  loadTagSettingsContentMock,
  mountBurndownChartMock,
  destroyBurndownChartMock,
  requestDesktopNotificationPermissionMock,
  handleThemeChangeMock,
  saveKeybindingOverrideMock,
  setKeybindingsCaptureListeningMock,
  showToastMock,
  state,
} = vi.hoisted(() => {
  const state = {
    theme: 'system',
    wallpaperState: { v: 1, mode: 'off', hex: '#8b919a' },
    desktopNotificationKind: 'default',
    captureListening: false,
    savedChords: { openSettings: 'shift+s' } as Record<string, string>,
  };
  return {
    apiFetchMock: vi.fn(),
    fetchProjectMembersMock: vi.fn(),
    loadTagSettingsContentMock: vi.fn().mockResolvedValue(''),
    mountBurndownChartMock: vi.fn(),
    destroyBurndownChartMock: vi.fn(),
    requestDesktopNotificationPermissionMock: vi.fn(),
    handleThemeChangeMock: vi.fn((value: string) => {
      state.theme = value;
    }),
    saveKeybindingOverrideMock: vi.fn((actionId: string, chord: string) => {
      state.savedChords[actionId] = chord;
      return true;
    }),
    setKeybindingsCaptureListeningMock: vi.fn((active: boolean) => {
      state.captureListening = active;
    }),
    showToastMock: vi.fn(),
    state,
  };
});

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
  showToast: showToastMock,
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
  getStoredTheme: () => state.theme,
  handleThemeChange: handleThemeChangeMock,
  THEME_SYSTEM: 'system',
  THEME_DARK: 'dark',
  THEME_LIGHT: 'light',
}));

vi.mock('../wallpaper.js', () => ({
  getStoredWallpaperState: () => state.wallpaperState,
  setWallpaperOff: vi.fn(),
  setWallpaperColor: vi.fn(),
  uploadWallpaperImage: vi.fn(),
}));

vi.mock('../charts/burndown.js', () => ({
  renderRealBurndownChart: (
    _data: any[],
    currentSprint?: { id?: number; name?: string } | null,
    sprintNav?: { canPrev: boolean; canNext: boolean },
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
}));

vi.mock('../core/keybindings.js', () => ({
  KEY_ACTION_LIST: [
    {
      id: 'openSettings',
      label: 'Open Settings',
      labelKey: 'settings.customization.keybindings.actions.openSettings',
      contexts: ['unknown'],
    },
  ],
  chordFromKeyboardEvent: (event: KeyboardEvent) => {
    if (event.key === 'Escape') return 'escape';
    if (event.ctrlKey && event.key.toLowerCase() === 'k') return 'ctrl+k';
    return null;
  },
  formatChordForDisplay: (chord: string) => (chord === 'ctrl+k' ? 'Ctrl+K' : chord === 'shift+s' ? 'Shift+S' : chord),
  getResolvedChordForAction: (actionId: string) => state.savedChords[actionId] ?? 'shift+s',
  isTypingInTextField: () => false,
  reloadKeybindingsFromStorage: vi.fn(),
  saveKeybindingOverride: saveKeybindingOverrideMock,
  setKeybindingsCaptureListening: setKeybindingsCaptureListeningMock,
}));

vi.mock('../core/assignmentNotify.js', () => ({
  requestDesktopNotificationPermission: requestDesktopNotificationPermissionMock,
  getDesktopNotificationStatusKind: () => state.desktopNotificationKind,
  getDesktopNotificationStatusDescription: () => {
    switch (state.desktopNotificationKind) {
      case 'unsupported':
        return 'Not supported in this browser.';
      case 'granted':
        return 'Enabled - you will receive OS notifications for new assignments.';
      case 'denied':
        return 'Blocked — allow notifications for this site in your browser settings.';
      default:
        return 'Not enabled yet — click the button below (your browser will ask for permission).';
    }
  },
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

const enCatalog = {
  'common.close': 'Close',
  'settings.shell.title': 'Settings',
  'settings.language.title': 'Language',
  'settings.language.description': 'Choose the language used for Scrumboy on this browser.',
  'settings.language.selectLabel': 'Language',
  'settings.tabs.profile': 'Profile',
  'settings.tabs.users': 'Users',
  'settings.tabs.sprints': 'Sprints',
  'settings.tabs.workflow': 'Workflow',
  'settings.tabs.customization': 'Customization',
  'settings.tabs.tagColors': 'Tag Colors',
  'settings.tabs.charts': 'Charts',
  'settings.tabs.backup': 'Backup',
  'settings.customization.theme.title': 'Theme',
  'settings.customization.theme.description': 'Choose your preferred color scheme.',
  'settings.customization.theme.option.system': 'System',
  'settings.customization.theme.option.dark': 'Dark',
  'settings.customization.theme.option.light': 'Light',
  'settings.customization.wallpaper.title': 'Wallpaper',
  'settings.customization.wallpaper.description': 'Optional background behind the app. A scrim keeps text readable. Boards and cards stay solid; Settings can show the wallpaper when it is active.',
  'settings.customization.wallpaper.summary.off': 'Off: default appearance',
  'settings.customization.wallpaper.summary.color': 'Solid color: active',
  'settings.customization.wallpaper.summary.builtin': 'Default image: active',
  'settings.customization.wallpaper.summary.image': 'Custom image: active',
  'settings.customization.wallpaper.mode.off': 'Off',
  'settings.customization.wallpaper.mode.color': 'Solid color',
  'settings.customization.wallpaper.mode.image': 'Custom image',
  'settings.customization.wallpaper.colorLabel': 'Color',
  'settings.customization.wallpaper.actions.upload': 'Upload image…',
  'settings.customization.wallpaper.actions.replace': 'Replace image…',
  'settings.customization.wallpaper.actions.remove': 'Remove wallpaper',
  'settings.customization.wallpaper.signInHint': 'Sign in to use a custom image. Solid color and Off work without signing in.',
  'settings.customization.wallpaper.toast.updated': 'Wallpaper updated',
  'settings.customization.wallpaper.toast.uploadFailed': 'Upload failed',
  'settings.customization.wallpaper.toast.signInRequired': 'Sign in to use a custom image',
  'settings.customization.wallpaper.toast.removed': 'Wallpaper removed',
  'settings.customization.notifications.title': 'Desktop notifications',
  'settings.customization.notifications.description': 'OS-level alerts when someone assigns you a todo (works when this tab is in the background).',
  'settings.customization.notifications.status.unsupported': 'Not supported in this browser.',
  'settings.customization.notifications.status.granted': 'Enabled - you will receive OS notifications for new assignments.',
  'settings.customization.notifications.status.denied': 'Blocked — allow notifications for this site in your browser settings.',
  'settings.customization.notifications.status.default': 'Not enabled yet — click the button below (your browser will ask for permission).',
  'settings.customization.notifications.actions.enable': 'Enable notifications',
  'settings.customization.notifications.actions.enabled': 'Notifications enabled',
  'settings.customization.notifications.toast.enabled': 'Desktop notifications enabled',
  'settings.customization.notifications.toast.blocked': 'Notifications blocked. You can allow them in your browser settings for this site.',
  'settings.customization.notifications.toast.notGranted': 'Notification permission not granted',
  'settings.customization.keybindings.title': 'Keybindings',
  'settings.customization.keybindings.description': 'Click a key to record a new shortcut. Press Esc to cancel while listening.',
  'settings.customization.keybindings.capturePrompt': 'Press a shortcut for {action}',
  'settings.customization.keybindings.actions.openSettings': 'Open Settings',
  'settings.customization.voiceFlow.title': 'VoiceFlow',
  'settings.customization.voiceFlow.toggleLabel': 'Use voice commands to move, create and delete todos.',
  'settings.customization.push.title': 'Background notifications (PWA)',
  'settings.customization.push.description': 'Alerts when someone assigns you a todo while this app is in the background or closed.',
  'settings.customization.push.toggleLabel': 'Web Push on this device',
  'settings.customization.push.vapidNotice': 'Web Push needs VAPID keys on the server (SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY; see docs).',
  'settings.customization.push.unavailableNotice': 'Web Push is currently unavailable.',
  'settings.customization.push.adminWarning.invalidSubscriber': 'Web Push is disabled because SCRUMBOY_VAPID_SUBSCRIBER is invalid.',
  'settings.customization.push.adminWarning.invalidPublicKey': 'Web Push is disabled because SCRUMBOY_VAPID_PUBLIC_KEY is invalid.',
  'settings.customization.push.adminWarning.invalidPrivateKey': 'Web Push is disabled because SCRUMBOY_VAPID_PRIVATE_KEY is invalid.',
  'settings.customization.push.adminWarning.initializationFailed': 'Web Push is disabled because initialization failed. Check the server logs.',
  'settings.customization.push.adminWarning.unknown': 'Web Push is disabled because of a server configuration error. Check the server logs.',
  'settings.customization.push.anonymousNotice': 'Web Push is not available in anonymous mode.',
  'settings.customization.push.unsupported': 'Web Push is not supported in this browser.',
  'settings.customization.emailNotify.title': 'Email notifications',
  'settings.customization.emailNotify.description': 'Get emailed about activity on your boards. Off by default.',
  'settings.customization.emailNotify.unavailableNotice': 'Email notifications require SMTP to be configured on the server (see docs).',
  'settings.customization.emailNotify.loadFailed': 'Could not load email notification settings. Reload the page to try again.',
  'settings.customization.emailNotify.saveFailed': 'Could not save email notification settings. Your previous settings are still active.',
  'settings.customization.emailNotify.toggleLabel': 'Email notifications on',
  'settings.customization.emailNotify.category.assigned': 'When a card is assigned to me',
  'settings.customization.emailNotify.category.cardActivity': 'Card created, moved, or deleted',
  'settings.customization.emailNotify.category.sprintActivity': 'Sprint activity',
  'settings.customization.emailNotify.category.projectActivity': 'Project, workflow, or tag changes',
  'settings.customization.emailNotify.category.addedToProject': "When I'm added to a project",
  'settings.backup.export.title': 'Export Data',
  'settings.backup.export.description': 'Download all your projects, todos, and tags as a JSON file.',
  'settings.backup.export.action': 'Export Backup',
  'settings.backup.import.title': 'Import Data',
  'settings.backup.import.description': 'Restore from a backup file or merge data from another instance.',
  'settings.backup.import.mode.merge': 'Merge & update (recommended)',
  'settings.backup.import.mode.replace': 'Replace all data',
  'settings.backup.import.mode.copy': 'Create a copy',
  'settings.backup.import.confirmPlaceholder': 'Type REPLACE to confirm',
  'settings.backup.import.action': 'Import',
  'settings.backup.trello.title': 'Import Trello Board',
  'settings.backup.trello.description': 'Upload a native Trello single-board JSON export, preview the conversion, then import it as a new Scrumboy board.',
  'settings.backup.trello.previewAction': 'Preview Trello Import',
  'settings.backup.trello.importAction': 'Import Trello Board',
};

function prefixCatalog(prefix: string): Record<string, string> {
  return Object.fromEntries(
    Object.entries(enCatalog).map(([key, value]) => [
      key,
      prefix === '[!!' ? `[!! ${value} !!]` : `${prefix}${value}`,
    ]),
  );
}

const deCatalog = prefixCatalog('DE ');
const pseudoCatalog = prefixCatalog('[!!');

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

function loader(catalogs: Record<string, Record<string, string>>) {
  return vi.fn(async (locale: 'en' | 'de' | 'pseudo') => catalogs[locale]);
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadSettingsModule() {
  return import('./settings.js');
}

async function loadStateMutations() {
  return import('../state/mutations.js');
}

async function initSettingsI18n(locale: 'en' | 'de' | 'pseudo' = 'en') {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale,
    loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }),
  });
  return i18n;
}

async function setupSettingsView(options: {
  locale?: 'en' | 'de' | 'pseudo';
  activeTab?: string;
  authStatusAvailable?: boolean;
  slug?: string | null;
  board?: Record<string, unknown> | null;
  pushConfigured?: boolean;
  pushStatus?: WebPushStatus | null;
  emailNotifyAvailable?: boolean;
  emailNotifyPreference?: EmailNotifyPreferenceState;
  user?: Record<string, unknown> | null;
  open?: boolean;
} = {}) {
  const i18n = await initSettingsI18n(options.locale ?? 'en');
  const settings = await loadSettingsModule();
  const mutations = await loadStateMutations();
  mutations.setAuthStatusAvailable(options.authStatusAvailable ?? true);
  mutations.setUser((options.user as any) ?? null);
  mutations.setPushConfigured(options.pushConfigured ?? false);
  mutations.setPushStatus(options.pushStatus ?? null);
  mutations.setEmailNotifyAvailable(options.emailNotifyAvailable ?? false);
  if (options.emailNotifyPreference) {
    mutations.setEmailNotifyPreferenceState(options.emailNotifyPreference);
  }
  mutations.setSlug(options.slug ?? null);
  mutations.setBoard((options.board as any) ?? null);
  mutations.setProjects(null);
  mutations.setProjectId(null);
  mutations.setSettingsProjectId(null);
  mutations.setSettingsActiveTab(options.activeTab ?? 'customization');
  mutations.setBoardMembers([]);
  await settings.renderSettingsModal();
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

describe('settings customization i18n', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    window.history.replaceState({}, '', '/');
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    loadTagSettingsContentMock.mockClear();
    mountBurndownChartMock.mockClear();
    destroyBurndownChartMock.mockClear();
    requestDesktopNotificationPermissionMock.mockReset();
    handleThemeChangeMock.mockClear();
    saveKeybindingOverrideMock.mockClear();
    setKeybindingsCaptureListeningMock.mockClear();
    showToastMock.mockClear();
    state.theme = 'system';
    state.wallpaperState = { v: 1, mode: 'off', hex: '#8b919a' };
    state.desktopNotificationKind = 'default';
    state.captureListening = false;
    state.savedChords = { openSettings: 'shift+s' };
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

  it('renders English shell and customization copy by default', async () => {
    await setupSettingsView();

    expect(document.getElementById('settingsDialogTitleLabel')?.textContent).toBe('Settings');
    expect(document.getElementById('settingsDialogVersion')?.textContent).toBe(' vtest-version');
    expect(document.querySelector('.settings-tab[data-tab="customization"]')?.textContent).toBe('Customization');
    expect(document.querySelector('.settings-tab[data-tab="profile"]')?.textContent).toBe('Profile');
    expect(document.querySelector('.settings-section__title')?.textContent).toBe('Language');
    expect(document.getElementById('desktopNotifyStatus')?.textContent).toBe(enCatalog['settings.customization.notifications.status.default']);
    expect(document.querySelector('.settings-section--keybindings .settings-section__title')?.textContent).toBe('Keybindings');
  });

  it('renders catalog-backed pseudo strings on first render', async () => {
    await setupSettingsView({ locale: 'pseudo' });

    expect(document.getElementById('settingsDialogTitleLabel')?.textContent).toBe('[!! Settings !!]');
    expect(document.querySelector('.settings-tab[data-tab="customization"]')?.textContent).toBe('[!! Customization !!]');
    expect(document.querySelector('.settings-section--keybindings .settings-section__title')?.textContent).toBe('[!! Keybindings !!]');
  });

  it('renders scoped dynamic customization labels in a non-English locale on first render', async () => {
    state.wallpaperState = { v: 1, mode: 'image', hex: '#8b919a' };
    state.desktopNotificationKind = 'granted';

    await setupSettingsView({
      locale: 'de',
      user: { id: 1, name: 'Alex' },
    });

    const captureBtn = document.querySelector<HTMLElement>('[data-keybinding-capture][data-keybinding-action="openSettings"]');
    if (!captureBtn) {
      throw new Error('missing keybinding capture button');
    }

    expect(document.getElementById('settingsDialogTitleLabel')?.textContent).toBe('DE Settings');
    expect(document.getElementById('settingsDialogVersion')?.textContent).toBe(' vtest-version');
    expect(document.querySelector('.settings-tab[data-tab="customization"]')?.textContent).toBe('DE Customization');
    expect(document.querySelector('.settings-tab[data-tab="profile"]')?.textContent).toBe('DE Profile');
    expect(document.getElementById('wallpaperSummary')?.textContent).toBe('DE Custom image: active');
    expect(document.getElementById('wallpaperUploadBtn')?.textContent).toBe('DE Replace image…');
    expect(document.getElementById('wallpaperRemoveBtn')?.textContent).toBe('DE Remove wallpaper');
    expect(document.getElementById('desktopNotifyStatus')?.textContent).toBe('DE Enabled - you will receive OS notifications for new assignments.');
    expect(document.getElementById('desktopNotifyEnableBtn')?.textContent).toBe('DE Notifications enabled');
    expect(document.querySelector('.keybinding-row__label')?.textContent).toBe('DE Open Settings');

    captureBtn.click();
    expect(captureBtn.textContent).toBe('DE Press a shortcut for DE Open Settings');
  });

  it('updates visible customization copy in place on locale change while preserving version, options, and unsaved state', async () => {
    const { i18n } = await setupSettingsView();

    const titleLabel = document.getElementById('settingsDialogTitleLabel');
    const version = document.getElementById('settingsDialogVersion');
    const themeLight = document.querySelector<HTMLInputElement>('input[name="theme"][value="light"]');
    const languageSelect = document.getElementById('settingsLocaleSelect') as HTMLButtonElement | null;
    const wallpaperRemoveBtn = document.getElementById('wallpaperRemoveBtn');

    if (!titleLabel || !version || !themeLight || !languageSelect || !wallpaperRemoveBtn) {
      throw new Error('missing customization controls');
    }

    themeLight.checked = true;
    const versionBefore = version.textContent;

    languageSelect.click();
    const deOption = languageSelect.closest('.locale-picker')?.querySelector('[role="option"][data-locale="de"]') as HTMLElement | null;
    if (!deOption) throw new Error('missing German locale option');
    deOption.click();
    await flushPromises();

    expect(i18n.getLocale()).toBe('de');
    expect(titleLabel.textContent).toBe('DE Settings');
    expect(version.textContent).toBe(versionBefore);
    expect(themeLight.checked).toBe(true);
    expect(
      languageSelect.closest('.locale-picker')?.querySelector('[role="option"][aria-selected="true"]')?.getAttribute('data-locale'),
    ).toBe('de');
    expect(
      Array.from(languageSelect.closest('.locale-picker')?.querySelectorAll('[role="option"]') ?? []).map((option) => [
        option.getAttribute('data-locale'),
        option.querySelector('.locale-picker__label')?.textContent,
        (option.querySelector('.locale-picker__flag') as HTMLImageElement | null)?.getAttribute('src'),
      ]),
    ).toEqual([
      ['en', 'English', '/assets/flags/us.svg'],
      ['zh', '简体中文', '/assets/flags/cn.svg'],
      ['hi', 'हिन्दी', '/assets/flags/in.svg'],
      ['es', 'Español (Latinoamérica)', '/assets/flags/mx.svg'],
      ['ar', 'العربية', '/assets/flags/sa.svg'],
      ['fr', 'Français', '/assets/flags/fr.svg'],
      ['bn', 'বাংলা', '/assets/flags/bd.svg'],
      ['pt', 'Português (Brasil)', '/assets/flags/br.svg'],
      ['id', 'Bahasa Indonesia', '/assets/flags/id.svg'],
      ['ur', 'اردو', '/assets/flags/pk.svg'],
      ['ru', 'Русский', '/assets/flags/ru.svg'],
      ['de', 'Deutsch', '/assets/flags/de.svg'],
      ['ja', '日本語', '/assets/flags/jp.svg'],
      ['sw', 'Kiswahili', '/assets/flags/tz.svg'],
      ['vi', 'Tiếng Việt', '/assets/flags/vn.svg'],
      ['tr', 'Türkçe', '/assets/flags/tr.svg'],
      ['ko', '한국어', '/assets/flags/kr.svg'],
      ['fa', 'فارسی', '/assets/flags/ir.svg'],
      ['th', 'ไทย', '/assets/flags/th.svg'],
      ['it', 'Italiano', '/assets/flags/it.svg'],
      ['ms', 'Bahasa Melayu', '/assets/flags/my.svg'],
      ['pl', 'Polski', '/assets/flags/pl.svg'],
      ['uk', 'Українська', '/assets/flags/ua.svg'],
    ]);
    expect(wallpaperRemoveBtn.textContent).toBe('DE Remove wallpaper');
  });

  it('preserves active keybinding capture across locale change and does not duplicate listeners', async () => {
    await setupSettingsView();

    const captureBtn = document.querySelector<HTMLElement>('[data-keybinding-capture][data-keybinding-action="openSettings"]');
    if (!captureBtn) {
      throw new Error('missing keybinding capture button');
    }

    captureBtn.click();
    expect(captureBtn.classList.contains('keybinding-capture--listening')).toBe(true);
    expect(captureBtn.textContent).toBe('Press a shortcut for Open Settings');

    const i18n = await import('../i18n/index.js');
    await i18n.setLocale('de');
    await flushPromises();

    expect(captureBtn.classList.contains('keybinding-capture--listening')).toBe(true);
    expect(captureBtn.textContent).toBe('DE Press a shortcut for DE Open Settings');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }));
    await flushPromises();

    expect(saveKeybindingOverrideMock).toHaveBeenCalledTimes(1);
    expect(saveKeybindingOverrideMock).toHaveBeenCalledWith('openSettings', 'ctrl+k');
    expect(captureBtn.classList.contains('keybinding-capture--listening')).toBe(false);
    expect(captureBtn.textContent).toBe('Ctrl+K');
  });

  it('updates desktop notification labels on locale change without requesting permission', async () => {
    state.desktopNotificationKind = 'granted';
    const { i18n } = await setupSettingsView();

    const status = document.getElementById('desktopNotifyStatus');
    const button = document.getElementById('desktopNotifyEnableBtn') as HTMLButtonElement | null;
    if (!status || !button) {
      throw new Error('missing desktop notification controls');
    }

    expect(status.textContent).toBe(enCatalog['settings.customization.notifications.status.granted']);
    expect(button.textContent).toBe(enCatalog['settings.customization.notifications.actions.enabled']);
    expect(button.disabled).toBe(true);

    await i18n.setLocale('de');
    await flushPromises();

    expect(status.textContent).toBe(`DE ${enCatalog['settings.customization.notifications.status.granted']}`);
    expect(button.textContent).toBe(`DE ${enCatalog['settings.customization.notifications.actions.enabled']}`);
    expect(button.disabled).toBe(true);
    expect(requestDesktopNotificationPermissionMock).not.toHaveBeenCalled();
  });

  it('relocalizes an open administrator Web Push warning', async () => {
    const { i18n } = await setupSettingsView({
      user: { id: 1, name: 'Admin', systemRole: 'owner' },
      pushStatus: { state: 'invalid', reason: 'invalid_subscriber' },
    });

    const warning = document.querySelector<HTMLElement>('.settings-push-vapid-notice');
    expect(warning?.textContent).toBe(enCatalog['settings.customization.push.adminWarning.invalidSubscriber']);

    await i18n.setLocale('de');
    await flushPromises();

    expect(document.querySelector('.settings-push-vapid-notice')?.textContent).toBe(
      `DE ${enCatalog['settings.customization.push.adminWarning.invalidSubscriber']}`,
    );
  });

  it('renders a localized load failure with disabled email controls', async () => {
    await setupSettingsView({
      locale: 'pseudo',
      user: { id: 1, name: 'Alex' },
      emailNotifyAvailable: true,
      emailNotifyPreference: { userId: 1, status: 'error', value: null },
    });

    expect(document.querySelector('.settings-section--email-notify p')?.textContent).toBe(
      '[!! Could not load email notification settings. Reload the page to try again. !!]',
    );
    expect((document.getElementById('emailNotifyEnabledToggle') as HTMLInputElement).disabled).toBe(true);
    expect(Array.from(document.querySelectorAll<HTMLInputElement>('.email-notify-category-toggle')).every((input) => input.disabled)).toBe(true);
  });

  it('restores the previous email preference and shows localized generic copy after a rejected save', async () => {
    await setupSettingsView({
      locale: 'pseudo',
      user: { id: 1, name: 'Alex' },
      emailNotifyAvailable: true,
      emailNotifyPreference: {
        userId: 1,
        status: 'ready',
        value: {
          v: 1,
          enabled: false,
          assigned: true,
          cardActivity: false,
          sprintActivity: false,
          projectActivity: false,
          addedToProject: true,
        },
      },
    });
    apiFetchMock.mockRejectedValueOnce(new Error('sensitive backend detail'));

    const toggle = document.getElementById('emailNotifyEnabledToggle') as HTMLInputElement;
    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises(16);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await flushPromises(8);

    expect((document.getElementById('emailNotifyEnabledToggle') as HTMLInputElement).checked).toBe(false);
    expect(showToastMock).toHaveBeenCalledWith(
      '[!! Could not save email notification settings. Your previous settings are still active. !!]',
    );
    expect(showToastMock).not.toHaveBeenCalledWith('sensitive backend detail');
  });

  it('relocalizes backup tab static chrome in place on locale change without API calls', async () => {
    // Phase 5: the backup tab is now localized, so its static copy must update
    // on locale change (previously this asserted the body stayed untouched).
    const { i18n } = await setupSettingsView({ activeTab: 'backup' });

    const activeTab = document.querySelector('.settings-tab--active[data-tab="backup"]');
    const exportTitle = document.querySelector('.settings-backup-export .settings-section__title');
    if (!(activeTab instanceof HTMLElement) || !(exportTitle instanceof HTMLElement)) {
      throw new Error('missing backup tab content');
    }
    expect(exportTitle.textContent).toBe('Export Data');

    apiFetchMock.mockClear();
    mountBurndownChartMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(document.querySelector('.settings-tab--active[data-tab="backup"]')).toBe(activeTab);
    expect(document.getElementById('settingsDialogTitleLabel')?.textContent).toBe('DE Settings');
    expect(document.querySelector('.settings-tab[data-tab="backup"]')?.textContent).toBe('DE Backup');
    expect(document.querySelector('.settings-backup-export .settings-section__title')?.textContent).toBe('DE Export Data');
    expect(document.getElementById('backupImportBtn')?.textContent).toBe('DE Import');
    const confirmInput = document.getElementById('backupConfirmationInput') as HTMLInputElement | null;
    expect(confirmInput?.getAttribute('placeholder')).toBe('DE Type REPLACE to confirm');
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(mountBurndownChartMock).not.toHaveBeenCalled();
  });

  it('does not mutate hidden settings content on locale change while the dialog is closed', async () => {
    const { i18n } = await setupSettingsView({ open: false });

    const titleBefore = document.getElementById('settingsDialogTitleLabel')?.textContent;
    const tabBefore = document.querySelector('.settings-tab[data-tab="customization"]')?.textContent;
    const statusBefore = document.getElementById('desktopNotifyStatus')?.textContent;

    await i18n.setLocale('de');
    await flushPromises();

    expect(document.getElementById('settingsDialogTitleLabel')?.textContent).toBe(titleBefore);
    expect(document.querySelector('.settings-tab[data-tab="customization"]')?.textContent).toBe(tabBefore);
    expect(document.getElementById('desktopNotifyStatus')?.textContent).toBe(statusBefore);
  });

  it('registers only one settings locale listener across repeated renders', async () => {
    const addEventListenerSpy = vi.spyOn(document, 'addEventListener');
    const { settings } = await setupSettingsView();

    await settings.renderSettingsModal();
    await settings.renderSettingsModal();

    const localeListenerAdds = addEventListenerSpy.mock.calls.filter(
      ([type]) => type === 'scrumboy:i18n-locale-changed',
    );
    expect(localeListenerAdds).toHaveLength(1);
  });
});

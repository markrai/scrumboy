// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  apiFetchMock,
  fetchProjectMembersMock,
  loadTagSettingsContentMock,
  windowFetchMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  fetchProjectMembersMock: vi.fn(),
  loadTagSettingsContentMock: vi.fn().mockResolvedValue(''),
  windowFetchMock: vi.fn().mockResolvedValue({
    ok: false,
    json: async () => ({}),
  }),
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
  "common.close": "Close",
  "settings.shell.title": "Settings",
  "settings.language.description": "Choose the language used for Scrumboy on this browser.",
  "settings.language.selectLabel": "Language",
  "settings.language.title": "Language",
  "settings.tabs.customization": "Customization",
  "settings.tabs.tagColors": "Tag Colors",
  "settings.tabs.backup": "Backup",
  "settings.customization.theme.title": "Theme",
  "settings.customization.theme.description": "Choose your preferred color scheme.",
  "settings.customization.theme.option.system": "System",
  "settings.customization.theme.option.dark": "Dark",
  "settings.customization.theme.option.light": "Light",
  "settings.customization.cardsPerLane.title": "Cards per lane",
  "settings.customization.cardsPerLane.description": "Number of cards shown by default in each lane before \"Load more\" is needed.",
  "settings.customization.cardsPerLane.signInHint": "Sign in to save this preference.",
  "settings.customization.cardsPerLane.toast.updated": "Cards per lane updated",
  "settings.customization.cardsPerLane.toast.updateFailed": "Failed to update cards per lane",
  "settings.customization.notifications.title": "Desktop notifications",
  "settings.customization.notifications.description": "OS-level alerts when someone assigns you a todo (works when this tab is in the background).",
  "settings.customization.notifications.status.default": "Not enabled yet — click the button below (your browser will ask for permission).",
  "settings.customization.notifications.actions.enable": "Enable notifications",
  "settings.customization.keybindings.title": "Keybindings",
  "settings.customization.keybindings.description": "Click a key to record a new shortcut. Press Esc to cancel while listening.",
  "settings.customization.push.title": "Background notifications (PWA)",
  "settings.customization.push.description": "Alerts when someone assigns you a todo while this app is in the background or closed.",
  "settings.customization.push.toggleLabel": "Web Push on this device",
  "settings.customization.push.anonymousNotice": "Web Push is not available in anonymous mode.",
  "settings.customization.push.unsupported": "Web Push is not supported in this browser.",
  "test.shell": "Shell text",
};

const deCatalog = {
  "common.close": "Schließen",
  "settings.shell.title": "Einstellungen",
  "settings.language.description": "Wähle die Sprache, die Scrumboy in diesem Browser verwendet.",
  "settings.language.selectLabel": "Sprache",
  "settings.language.title": "Sprache",
  "settings.tabs.customization": "Anpassung",
  "settings.tabs.tagColors": "Tag-Farben",
  "settings.tabs.backup": "Backup",
  "settings.customization.theme.title": "Thema",
  "settings.customization.theme.description": "Wähle dein bevorzugtes Farbschema.",
  "settings.customization.theme.option.system": "System",
  "settings.customization.theme.option.dark": "Dunkel",
  "settings.customization.theme.option.light": "Hell",
  "settings.customization.cardsPerLane.title": "Karten pro Spalte",
  "settings.customization.cardsPerLane.description": "Standardmäßig angezeigte Anzahl an Karten pro Spalte, bevor „Mehr laden“ erforderlich ist.",
  "settings.customization.cardsPerLane.signInHint": "Melde dich an, um diese Einstellung zu speichern.",
  "settings.customization.cardsPerLane.toast.updated": "Karten pro Spalte aktualisiert",
  "settings.customization.cardsPerLane.toast.updateFailed": "Karten pro Spalte konnten nicht aktualisiert werden",
  "settings.customization.notifications.title": "Desktop-Benachrichtigungen",
  "settings.customization.notifications.description": "Systemweite Hinweise, wenn dir jemand ein Todo zuweist (funktioniert, wenn dieser Tab im Hintergrund ist).",
  "settings.customization.notifications.status.default": "Noch nicht aktiviert — klicke auf die Schaltfläche unten (dein Browser fragt nach Berechtigung).",
  "settings.customization.notifications.actions.enable": "Benachrichtigungen aktivieren",
  "settings.customization.keybindings.title": "Tastenkürzel",
  "settings.customization.keybindings.description": "Klicke auf eine Taste, um ein neues Kürzel aufzuzeichnen. Drücke Esc, um das Lauschen abzubrechen.",
  "settings.customization.push.title": "Hintergrund-Benachrichtigungen (PWA)",
  "settings.customization.push.description": "Hinweise, wenn dir jemand ein Todo zuweist, während diese App im Hintergrund oder geschlossen ist.",
  "settings.customization.push.toggleLabel": "Web Push auf diesem Gerät",
  "settings.customization.push.anonymousNotice": "Web Push ist im anonymen Modus nicht verfügbar.",
  "settings.customization.push.unsupported": "Web Push wird in diesem Browser nicht unterstützt.",
  "test.shell": "Shell-Text",
};

const pseudoCatalog = {
  "common.close": "[!! Close !!]",
  "settings.shell.title": "[!! Settings !!]",
  "settings.language.description": "[!! Choose the language used for Scrumboy on this browser. !!]",
  "settings.language.selectLabel": "[!! Language !!]",
  "settings.language.title": "[!! Language !!]",
  "settings.tabs.customization": "[!! Customization !!]",
  "settings.tabs.tagColors": "[!! Tag Colors !!]",
  "settings.tabs.backup": "[!! Backup !!]",
  "settings.customization.theme.title": "[!! Theme !!]",
  "settings.customization.theme.description": "[!! Choose your preferred color scheme. !!]",
  "settings.customization.theme.option.system": "[!! System !!]",
  "settings.customization.theme.option.dark": "[!! Dark !!]",
  "settings.customization.theme.option.light": "[!! Light !!]",
  "settings.customization.cardsPerLane.title": "[!! Cards per lane !!]",
  "settings.customization.cardsPerLane.description": "[!! Number of cards shown by default in each lane before \"Load more\" is needed. !!]",
  "settings.customization.cardsPerLane.signInHint": "[!! Sign in to save this preference. !!]",
  "settings.customization.cardsPerLane.toast.updated": "[!! Cards per lane updated !!]",
  "settings.customization.cardsPerLane.toast.updateFailed": "[!! Failed to update cards per lane !!]",
  "settings.customization.notifications.title": "[!! Desktop notifications !!]",
  "settings.customization.notifications.description": "[!! OS-level alerts when someone assigns you a todo (works when this tab is in the background). !!]",
  "settings.customization.notifications.status.default": "[!! Not enabled yet — click the button below (your browser will ask for permission). !!]",
  "settings.customization.notifications.actions.enable": "[!! Enable notifications !!]",
  "settings.customization.keybindings.title": "[!! Keybindings !!]",
  "settings.customization.keybindings.description": "[!! Click a key to record a new shortcut. Press Esc to cancel while listening. !!]",
  "settings.customization.push.title": "[!! Background notifications (PWA) !!]",
  "settings.customization.push.description": "[!! Alerts when someone assigns you a todo while this app is in the background or closed. !!]",
  "settings.customization.push.toggleLabel": "[!! Web Push on this device !!]",
  "settings.customization.push.anonymousNotice": "[!! Web Push is not available in anonymous mode. !!]",
  "settings.customization.push.unsupported": "[!! Web Push is not supported in this browser. !!]",
  "test.shell": "[!! Shell text !!]",
};

function installBaseDOM(): void {
  document.body.innerHTML = `
    <button id="shellProbe" data-i18n-text="test.shell"></button>
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
  return vi.fn(async (locale: "en" | "de" | "pseudo") => catalogs[locale]);
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

async function setupCustomizationSettings() {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({ locale: 'en', loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });
  const hydrateOnLocaleChange = () => i18n.hydrateI18n(document.body);
  document.addEventListener(i18n.I18N_LOCALE_CHANGED, hydrateOnLocaleChange);
  const settings = await loadSettingsModule();
  const state = await loadStateMutations();
  state.setAuthStatusAvailable(false);
  state.setUser(null);
  state.setSlug(null);
  state.setBoard(null);
  state.setProjects(null);
  state.setProjectId(null);
  state.setSettingsProjectId(null);
  state.setSettingsActiveTab('customization');
  state.setBoardMembers([]);
  return { i18n, settings, cleanup: () => document.removeEventListener(i18n.I18N_LOCALE_CHANGED, hydrateOnLocaleChange) };
}

const EXPECTED_LOCALE_FLAG_PATHS = [
  '/assets/flags/us.svg',
  '/assets/flags/cn.svg',
  '/assets/flags/in.svg',
  '/assets/flags/mx.svg',
  '/assets/flags/sa.svg',
  '/assets/flags/fr.svg',
  '/assets/flags/bd.svg',
  '/assets/flags/br.svg',
  '/assets/flags/id.svg',
  '/assets/flags/pk.svg',
  '/assets/flags/ru.svg',
  '/assets/flags/de.svg',
  '/assets/flags/jp.svg',
  '/assets/flags/tz.svg',
  '/assets/flags/vn.svg',
  '/assets/flags/tr.svg',
  '/assets/flags/kr.svg',
  '/assets/flags/ir.svg',
  '/assets/flags/th.svg',
  '/assets/flags/it.svg',
  '/assets/flags/my.svg',
  '/assets/flags/pl.svg',
  '/assets/flags/ua.svg',
];

function getSettingsLocalePicker(): HTMLButtonElement {
  const button = document.getElementById('settingsLocaleSelect') as HTMLButtonElement | null;
  if (!button) throw new Error('missing settings locale selector');
  return button;
}

function settingsLocaleOptionDetails(): Array<{ locale: string; label: string; flagSrc: string }> {
  const list = getSettingsLocalePicker().closest('.locale-picker')?.querySelector('.locale-picker__list');
  return Array.from(list?.querySelectorAll('[role="option"]') ?? []).map((option) => ({
    locale: option.getAttribute('data-locale') ?? '',
    label: option.querySelector('.locale-picker__label')?.textContent ?? '',
    flagSrc: (option.querySelector('.locale-picker__flag') as HTMLImageElement | null)?.getAttribute('src') ?? '',
  }));
}

async function selectSettingsLocale(locale: string): Promise<void> {
  const button = getSettingsLocalePicker();
  button.click();
  const option = button.closest('.locale-picker')?.querySelector(`[role="option"][data-locale="${locale}"]`) as HTMLElement | null;
  if (!option) throw new Error(`missing settings locale option: ${locale}`);
  option.click();
  await flushPromises();
}

describe('settings language selector', () => {
  beforeEach(() => {
    vi.resetModules();
    installBaseDOM();
    window.history.replaceState({}, '', '/');
    apiFetchMock.mockReset();
    fetchProjectMembersMock.mockReset();
    fetchProjectMembersMock.mockResolvedValue([]);
    loadTagSettingsContentMock.mockClear();
    windowFetchMock.mockClear();
    vi.stubGlobal('fetch', windowFetchMock);
  });

  afterEach(async () => {
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
    vi.unstubAllGlobals();
    window.history.replaceState({}, '', '/');
  });

  it('renders public locale options only', async () => {
    const { settings, cleanup } = await setupCustomizationSettings();
    try {
      await settings.renderSettingsModal();

      const button = getSettingsLocalePicker();
      expect(button).toBeTruthy();
      expect(settingsLocaleOptionDetails().map((option) => [option.locale, option.label])).toEqual([
        ['en', 'English'],
        ['zh', '简体中文'],
        ['hi', 'हिन्दी'],
        ['es', 'Español (Latinoamérica)'],
        ['ar', 'العربية'],
        ['fr', 'Français'],
        ['bn', 'বাংলা'],
        ['pt', 'Português (Brasil)'],
        ['id', 'Bahasa Indonesia'],
        ['ur', 'اردو'],
        ['ru', 'Русский'],
        ['de', 'Deutsch'],
        ['ja', '日本語'],
        ['sw', 'Kiswahili'],
        ['vi', 'Tiếng Việt'],
        ['tr', 'Türkçe'],
        ['ko', '한국어'],
        ['fa', 'فارسی'],
        ['th', 'ไทย'],
        ['it', 'Italiano'],
        ['ms', 'Bahasa Melayu'],
        ['pl', 'Polski'],
        ['uk', 'Українська'],
      ]);
      expect(settingsLocaleOptionDetails().map((option) => option.flagSrc)).toEqual(EXPECTED_LOCALE_FLAG_PATHS);
      expect(settingsLocaleOptionDetails().some((option) => option.locale === 'pseudo')).toBe(false);
      expect(document.querySelector('label[for="settingsLocaleSelect"]')?.textContent).toBe('Language');
    } finally {
      cleanup();
    }
  });

  it('does not pin the open settings locale list with fixed positioning', async () => {
    const { settings, cleanup } = await setupCustomizationSettings();
    try {
      await settings.renderSettingsModal();

      const button = getSettingsLocalePicker();
      button.click();

      const list = button.closest('.locale-picker')?.querySelector('.locale-picker__list') as HTMLUListElement;
      expect(list.hidden).toBe(false);
      expect(list.style.position).toBe('');
      expect(list.style.top).toBe('');
      expect(list.style.left).toBe('');
      expect(list.style.right).toBe('');
      expect(list.style.minWidth).toBe('');
      expect(list.style.zIndex).toBe('');
    } finally {
      cleanup();
    }
  });

  it('selecting German persists locale, updates lang, and hydrates migrated shell text', async () => {
    const { i18n, settings, cleanup } = await setupCustomizationSettings();
    try {
      await settings.renderSettingsModal();
      i18n.hydrateI18n(document.body);
      expect(document.getElementById('shellProbe')?.textContent).toBe('Shell text');

      await selectSettingsLocale('de');

      expect(i18n.getLocale()).toBe('de');
      expect(localStorage.getItem(i18n.LOCALE_STORAGE_KEY)).toBe('de');
      expect(document.cookie).toContain(`${i18n.LOCALE_STORAGE_KEY}=de`);
      expect(document.documentElement.lang).toBe('de');
      expect(document.documentElement.getAttribute('data-locale')).toBe('de');
      expect(document.getElementById('shellProbe')?.textContent).toBe('Shell-Text');
      expect(document.querySelector('label[for="settingsLocaleSelect"]')?.textContent).toBe('Sprache');
    } finally {
      cleanup();
    }
  });

  it('keeps pseudo hidden from Settings while the developer QA helper still works', async () => {
    const { i18n, settings, cleanup } = await setupCustomizationSettings();
    try {
      const qa = await import('../i18n/qa.js');
      const helper = qa.installI18nQa({
        target: window,
        storage: localStorage,
        location: { hostname: 'localhost' },
        nodeEnv: 'production',
      });
      await helper?.enablePseudo();
      await settings.renderSettingsModal();

      expect(i18n.getLocale()).toBe('pseudo');
      expect(settingsLocaleOptionDetails().map((option) => option.locale)).toEqual(['en', 'zh', 'hi', 'es', 'ar', 'fr', 'bn', 'pt', 'id', 'ur', 'ru', 'de', 'ja', 'sw', 'vi', 'tr', 'ko', 'fa', 'th', 'it', 'ms', 'pl', 'uk']);
      expect(settingsLocaleOptionDetails().some((option) => option.locale === 'pseudo')).toBe(false);
    } finally {
      cleanup();
    }
  });
});

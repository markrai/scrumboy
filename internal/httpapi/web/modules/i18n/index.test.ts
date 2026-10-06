// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const enCatalog = {
  "board.backToProjects": "\u2190 Projects",
  "board.filters.label": "Tags:",
  "board.selection.multiple": "Edit {count} selected",
  "board.selection.single": "Edit 1 selected",
  "common.add": "Add",
  "common.apply": "Apply",
  "common.cancel": "Cancel",
  "common.close": "Close",
  "common.confirm": "Confirm",
  "common.delete": "Delete",
  "common.prompt": "Prompt",
  "common.save": "Save",
  "common.value": "Value",
  "errors.CONFLICT": "Conflict",
  "errors.NOT_FOUND": "Not found",
  "errors.VALIDATION_ERROR": "Please check the request and try again.",
  "errors.VALIDATION_ERROR.invalid_workflow_column_color": "Enter a valid workflow column color.",
  "errors.VALIDATION_ERROR.name_required": "Please enter a name.",
  "errors.generic": "Something went wrong.",
  "errors.httpStatus": "HTTP {status}",
  "test.aria": "Close panel",
  "test.greeting": "Hello, {name}",
  "test.malicious": "<img src=x onerror=alert(1)>",
  "test.placeholder": "Type <safe>",
  "test.shell": "Shell text",
  "test.title": "Title & <safe>",
  "todo.dialog.title.new": "New Todo",
  "todo.fields.title": "Title",
  "todo.links.remove": "Remove link",
  "todo.notes.markdown": "markdown",
  "todo.notes.modeLabel": "Notes editor mode",
  "todo.tags.placeholder": "Type tag and press Enter or Tab",
  "todo.saveFailed": "Save fallback",
};

const pseudoCatalog = {
  "board.backToProjects": "[!! \u2190 Projects !!]",
  "board.filters.label": "[!! Tags: !!]",
  "board.selection.multiple": "[!! Edit {count} selected !!]",
  "board.selection.single": "[!! Edit 1 selected !!]",
  "common.add": "[!! Add !!]",
  "common.apply": "[!! Apply !!]",
  "common.cancel": "[!! Cancel !!]",
  "common.close": "[!! Close !!]",
  "common.confirm": "[!! Confirm !!]",
  "common.delete": "[!! Delete !!]",
  "common.prompt": "[!! Prompt !!]",
  "common.save": "[!! Save !!]",
  "common.value": "[!! Value !!]",
  "errors.CONFLICT": "[!! Conflict !!]",
  "errors.NOT_FOUND": "[!! Not found !!]",
  "errors.VALIDATION_ERROR": "[!! Please check the request and try again. !!]",
  "errors.VALIDATION_ERROR.invalid_workflow_column_color": "[!! Enter a valid workflow column color. !!]",
  "errors.VALIDATION_ERROR.name_required": "[!! Please enter a name. !!]",
  "errors.generic": "[!! Something went wrong. !!]",
  "errors.httpStatus": "[!! HTTP {status} !!]",
  "test.aria": "[!! Close panel !!]",
  "test.greeting": "[!! Hello, {name} !!]",
  "test.malicious": "[!! <img src=x onerror=alert(1)> !!]",
  "test.placeholder": "[!! Type <safe> !!]",
  "test.shell": "[!! Shell text !!]",
  "test.title": "[!! Title & <safe> !!]",
  "todo.dialog.title.new": "[!! New Todo !!]",
  "todo.fields.title": "[!! Title !!]",
  "todo.links.remove": "[!! Remove link !!]",
  "todo.notes.markdown": "[!! markdown !!]",
  "todo.notes.modeLabel": "[!! Notes editor mode !!]",
  "todo.tags.placeholder": "[!! Type tag and press Enter or Tab !!]",
  "todo.saveFailed": "[!! Save fallback !!]",
};

const deCatalog = {
  "board.backToProjects": "\u2190 Projekte",
  "board.filters.label": "Tags:",
  "board.selection.multiple": "{count} ausgew\u00e4hlte Eintr\u00e4ge bearbeiten",
  "board.selection.single": "1 ausgew\u00e4hlten Eintrag bearbeiten",
  "common.add": "Hinzufügen",
  "common.apply": "Anwenden",
  "common.cancel": "Abbrechen",
  "common.close": "Schließen",
  "common.confirm": "Bestätigen",
  "common.delete": "Löschen",
  "common.prompt": "Eingabe",
  "common.save": "Speichern",
  "common.value": "Wert",
  "errors.CONFLICT": "Konflikt",
  "errors.NOT_FOUND": "Nicht gefunden",
  "errors.VALIDATION_ERROR": "Bitte prüfe die Eingabe und versuche es erneut.",
  "errors.VALIDATION_ERROR.invalid_workflow_column_color": "Bitte gib eine gültige Workflow-Spaltenfarbe ein.",
  "errors.VALIDATION_ERROR.name_required": "Bitte gib einen Namen ein.",
  "errors.generic": "Etwas ist schiefgelaufen.",
  "errors.httpStatus": "HTTP {status}",
  "test.aria": "Bereich schließen",
  "test.greeting": "Hallo, {name}",
  "test.malicious": "<img src=x onerror=alert(1)>",
  "test.placeholder": "Tippe <sicher>",
  "test.shell": "Shell-Text",
  "test.title": "Titel & <sicher>",
  "todo.dialog.title.new": "Neues Todo",
  "todo.fields.title": "Titel",
  "todo.links.remove": "Link entfernen",
  "todo.notes.markdown": "Markdown",
  "todo.notes.modeLabel": "Modus für Notizeditor",
  "todo.tags.placeholder": "Tag eingeben und Enter oder Tab drücken",
  "todo.saveFailed": "Speicher-Fallback",
};

async function loadModule() {
  vi.resetModules();
  return await import("./index.js");
}

function loader(catalogs: Record<string, Record<string, string>>) {
  return vi.fn(async (locale: string) => catalogs[locale]);
}

function cookieValue(name: string): string | null {
  const prefix = `${name}=`;
  return document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix))
    ?.slice(prefix.length) ?? null;
}

function clearLocaleCookieForTests(): void {
  document.cookie = "scrumboy.locale=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT";
}

describe("i18n locale detection", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("uses localStorage before browser languages", async () => {
    const i18n = await loadModule();
    localStorage.setItem(i18n.LOCALE_STORAGE_KEY, "pseudo");

    expect(i18n.detectLocale({ languages: ["en-US"] })).toBe("pseudo");
  });

  it("falls back to navigator language aliases and then English", async () => {
    const i18n = await loadModule();

    expect(i18n.detectLocale({ storage: null, languages: ["fr-FR", "de-DE", "en-US"] })).toBe("fr");
    expect(i18n.detectLocale({ storage: null, languages: ["de-DE", "fr-FR", "en-US"] })).toBe("de");
    expect(i18n.detectLocale({ storage: null, languages: ["fr-FR", "en-US"] })).toBe("fr");
    expect(i18n.detectLocale({ storage: null, languages: ["fr-FR"] })).toBe("fr");
    expect(i18n.normalizeLocale("en_GB")).toBe("en");
    expect(i18n.normalizeLocale("de")).toBe("de");
    expect(i18n.normalizeLocale("de-DE")).toBe("de");
    expect(i18n.normalizeLocale("it")).toBe("it");
    expect(i18n.normalizeLocale("it-IT")).toBe("it");
    expect(i18n.normalizeLocale("fr")).toBe("fr");
    expect(i18n.normalizeLocale("fr-FR")).toBe("fr");
    expect(i18n.normalizeLocale("pt")).toBe("pt");
    expect(i18n.normalizeLocale("pt-BR")).toBe("pt");
    expect(i18n.normalizeLocale("es")).toBe("es");
    expect(i18n.normalizeLocale("es-MX")).toBe("es");
    expect(i18n.normalizeLocale("ar")).toBe("ar");
    expect(i18n.normalizeLocale("ar-SA")).toBe("ar");
    expect(i18n.normalizeLocale("ru")).toBe("ru");
    expect(i18n.normalizeLocale("ru-RU")).toBe("ru");
    expect(i18n.normalizeLocale("ja")).toBe("ja");
    expect(i18n.normalizeLocale("ja-JP")).toBe("ja");
    expect(i18n.normalizeLocale("tr")).toBe("tr");
    expect(i18n.normalizeLocale("tr-TR")).toBe("tr");
    expect(i18n.normalizeLocale("ko")).toBe("ko");
    expect(i18n.normalizeLocale("ko-KR")).toBe("ko");
    expect(i18n.normalizeLocale("zh")).toBe("zh");
    expect(i18n.normalizeLocale("zh-CN")).toBe("zh");
    expect(i18n.normalizeLocale("zh-Hans")).toBe("zh");
    expect(i18n.normalizeLocale("zh-TW")).toBe(null);
    expect(i18n.normalizeLocale("id")).toBe("id");
    expect(i18n.normalizeLocale("id-ID")).toBe("id");
    expect(i18n.normalizeLocale("vi")).toBe("vi");
    expect(i18n.normalizeLocale("vi-VN")).toBe("vi");
    expect(i18n.normalizeLocale("th")).toBe("th");
    expect(i18n.normalizeLocale("th-TH")).toBe("th");
    expect(i18n.normalizeLocale("ur")).toBe("ur");
    expect(i18n.normalizeLocale("ur-PK")).toBe("ur");
    expect(i18n.normalizeLocale("hi")).toBe("hi");
    expect(i18n.normalizeLocale("hi-IN")).toBe("hi");
    expect(i18n.isRtlLocale("ar")).toBe(true);
    expect(i18n.isRtlLocale("ur")).toBe(true);
    expect(i18n.documentDirection("ar")).toBe("rtl");
    expect(i18n.documentDirection("ur")).toBe("rtl");
    expect(i18n.documentDirection("en")).toBe("ltr");
  });
});

describe("browserLanguageMatchesPublicLocale", () => {
  it("matches exact and regional browser tags for landing locales", async () => {
    const i18n = await loadModule();

    expect(i18n.browserLanguageMatchesPublicLocale("fr", ["de-DE", "fr-FR"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("fr", ["fr"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("fr", ["fr_CA"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("pt", ["pt-BR"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("pt", ["pt-PT"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("zh", ["zh-TW"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("zh", ["zh-Hans"])).toBe(true);
    expect(i18n.browserLanguageMatchesPublicLocale("de", ["de-AT"])).toBe(true);
  });

  it("does not match unrelated browser tags", async () => {
    const i18n = await loadModule();

    expect(i18n.browserLanguageMatchesPublicLocale("fr", ["de-DE", "en-US"])).toBe(false);
    expect(i18n.browserLanguageMatchesPublicLocale("fr", [])).toBe(false);
    expect(i18n.browserLanguageMatchesPublicLocale("fr", [""])).toBe(false);
    expect(i18n.browserLanguageMatchesPublicLocale("id", ["in-ID"])).toBe(false);
  });
});

describe("i18n catalog loading", () => {
  beforeEach(() => {
    localStorage.clear();
    clearLocaleCookieForTests();
    document.documentElement.lang = "en";
    document.documentElement.removeAttribute("data-locale");
    document.documentElement.removeAttribute("dir");
  });

  afterEach(async () => {
    const i18n = await import("./index.js");
    i18n.resetI18nForTests();
    localStorage.clear();
    clearLocaleCookieForTests();
    vi.restoreAllMocks();
  });

  it("loads English first, then the active locale", async () => {
    const i18n = await loadModule();
    const loadLocale = loader({ en: enCatalog, pseudo: pseudoCatalog });

    await i18n.initI18n({ locale: "pseudo", loadLocale });

    expect(loadLocale.mock.calls.map(([locale]) => locale)).toEqual(["en", "pseudo"]);
    expect(i18n.getLocale()).toBe("pseudo");
    expect(document.documentElement.lang).toBe("en");
    expect(document.documentElement.getAttribute("data-locale")).toBe("pseudo");
    expect(i18n.t("common.cancel")).toBe("[!! Cancel !!]");
  });

  it("loads German from navigator language fallback", async () => {
    const i18n = await loadModule();
    const loadLocale = loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog });

    await i18n.initI18n({ storage: null, languages: ["de-DE"], loadLocale });

    expect(loadLocale.mock.calls.map(([locale]) => locale)).toEqual(["en", "de"]);
    expect(i18n.getLocale()).toBe("de");
    expect(document.documentElement.lang).toBe("de");
    expect(document.documentElement.getAttribute("data-locale")).toBe("de");
    expect(document.documentElement.hasAttribute("dir")).toBe(false);
    expect(i18n.t("common.cancel")).toBe("Abbrechen");
  });

  it("sets dir=rtl for Arabic and clears it when switching back to English", async () => {
    const i18n = await loadModule();
    const arCatalog = { "common.cancel": "إلغاء" };
    const loadLocale = loader({ en: enCatalog, ar: arCatalog });

    await i18n.initI18n({ locale: "en", loadLocale });
    expect(document.documentElement.hasAttribute("dir")).toBe(false);

    await i18n.setLocale("ar");
    expect(document.documentElement.getAttribute("dir")).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
    expect(document.documentElement.getAttribute("data-locale")).toBe("ar");

    await i18n.setLocale("en");
    expect(document.documentElement.hasAttribute("dir")).toBe(false);
    expect(document.documentElement.lang).toBe("en");
  });

  it("keeps the bootstrap English fallback when a custom loader fails loading English", async () => {
    const i18n = await loadModule();
    const loadLocale = vi.fn(async () => {
      throw new Error("catalog unavailable");
    });

    await expect(i18n.initI18n({ locale: "pseudo", loadLocale })).rejects.toThrow("catalog unavailable");

    expect(i18n.getLocale()).toBe("en");
    expect(i18n.t("common.cancel")).toBe("Cancel");
  });

  it("persists public locale changes to localStorage and the landing locale cookie", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    await i18n.setLocale("de");

    expect(i18n.getLocale()).toBe("de");
    expect(localStorage.getItem(i18n.LOCALE_STORAGE_KEY)).toBe("de");
    expect(cookieValue(i18n.LOCALE_STORAGE_KEY)).toBe("de");

    await i18n.setLocale("en");

    expect(i18n.getLocale()).toBe("en");
    expect(localStorage.getItem(i18n.LOCALE_STORAGE_KEY)).toBe("en");
    expect(cookieValue(i18n.LOCALE_STORAGE_KEY)).toBe("en");
  });

  it("clears the landing locale cookie for pseudo while preserving localStorage", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    await i18n.setLocale("de");
    await i18n.setLocale("pseudo");

    expect(i18n.getLocale()).toBe("pseudo");
    expect(localStorage.getItem(i18n.LOCALE_STORAGE_KEY)).toBe("pseudo");
    expect(cookieValue(i18n.LOCALE_STORAGE_KEY)).toBeNull();
  });

  it("mirrors an existing public localStorage locale to the landing locale cookie during init", async () => {
    const i18n = await loadModule();
    localStorage.setItem(i18n.LOCALE_STORAGE_KEY, "de");

    await i18n.initI18n({ loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.getLocale()).toBe("de");
    expect(cookieValue(i18n.LOCALE_STORAGE_KEY)).toBe("de");
  });

  it("does not write the landing locale cookie for navigator-only locale detection", async () => {
    const i18n = await loadModule();

    await i18n.initI18n({ storage: null, languages: ["de-DE"], loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.getLocale()).toBe("de");
    expect(cookieValue(i18n.LOCALE_STORAGE_KEY)).toBeNull();
  });

  it("dispatches locale-change events only after the active locale changes", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    const events: string[] = [];
    document.addEventListener(i18n.I18N_LOCALE_CHANGED, ((event: CustomEvent<{ locale: string }>) => {
      events.push(event.detail.locale);
    }) as EventListener);

    await i18n.setLocale("en");
    expect(events).toEqual([]);

    await i18n.setLocale("pseudo");

    expect(events).toEqual(["pseudo"]);
    expect(i18n.getLocale()).toBe("pseudo");
  });

  it("does not dispatch locale-change events when a locale switch fails back to the current locale", async () => {
    const i18n = await loadModule();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => {
        if (locale === "pseudo") throw new Error("pseudo unavailable");
        return enCatalog;
      }),
    });
    const events: string[] = [];
    document.addEventListener(i18n.I18N_LOCALE_CHANGED, ((event: CustomEvent<{ locale: string }>) => {
      events.push(event.detail.locale);
    }) as EventListener);

    await i18n.setLocale("pseudo");

    expect(i18n.getLocale()).toBe("en");
    expect(events).toEqual([]);
    warn.mockRestore();
  });

  it("supports simple placeholder interpolation", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "pseudo", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.t("test.greeting", { name: "Ada" })).toBe("[!! Hello, Ada !!]");
  });

  it("fails loudly for missing keys in tests", async () => {
    const i18n = await loadModule();
    const incompletePseudo = { ...pseudoCatalog };
    delete incompletePseudo["common.save"];
    await i18n.initI18n({ locale: "pseudo", loadLocale: loader({ en: enCatalog, pseudo: incompletePseudo }) });

    expect(() => i18n.t("common.save")).toThrow('Missing i18n key "common.save" for locale "pseudo"');
  });

  it("fails loudly for missing German keys in tests", async () => {
    const i18n = await loadModule();
    const incompleteDe = { ...deCatalog };
    delete incompleteDe["common.save"];
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: incompleteDe, pseudo: pseudoCatalog }) });

    expect(() => i18n.t("common.save")).toThrow('Missing i18n key "common.save" for locale "de"');
  });

  it("falls back to English for missing keys in production mode", async () => {
    const i18n = await loadModule();
    const incompletePseudo = { ...pseudoCatalog };
    delete incompletePseudo["common.save"];
    await i18n.initI18n({ locale: "pseudo", loadLocale: loader({ en: enCatalog, pseudo: incompletePseudo }) });
    const previousNodeEnv = process.env.NODE_ENV;
    process.env.NODE_ENV = "production";

    try {
      expect(i18n.t("common.save")).toBe("Save");
    } finally {
      process.env.NODE_ENV = previousNodeEnv;
    }
  });

  it("formats dates and numbers with the active locale", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.formatDate(Date.parse("2026-04-13T12:00:00Z"), {
      month: "short",
      day: "numeric",
      timeZone: "UTC",
    })).toBe("Apr 13");
    expect(i18n.formatNumber(1234.5)).toBe("1,234.5");
  });

  it("preserves the English ordinal long-date style", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });
    const date = new Date(2026, 0, 2, 12, 0, 0);

    expect(i18n.formatLongDateWithWeekday(date)).toBe("Friday, January 2nd 2026");
  });

  it("uses locale-aware German long-date formatting without English ordinal suffixes", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });
    const date = new Date(2026, 0, 2, 12, 0, 0);
    const formatted = i18n.formatLongDateWithWeekday(date);

    expect(formatted).toBe(
      new Intl.DateTimeFormat("de", {
        weekday: "long",
        month: "long",
        day: "numeric",
        year: "numeric",
      }).format(date),
    );
    expect(formatted).not.toMatch(/\b\d+(st|nd|rd|th)\b/);
  });

  it("prefers reason-specific and code-level localized API errors before a fallback key", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessage({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw english",
          details: { reason: "name_required" },
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("Bitte gib einen Namen ein.");

    expect(i18n.apiErrorMessage({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw english",
          details: { reason: "invalid_workflow_column_color", field: "color" },
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("Bitte gib eine gültige Workflow-Spaltenfarbe ein.");

    expect(i18n.apiErrorMessage({
      data: {
        error: {
          code: "CONFLICT",
          message: "raw english",
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("Konflikt");

    expect(i18n.apiErrorMessage({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw english",
          details: { reason: "unknown_reason" },
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("Bitte prüfe die Eingabe und versuche es erneut.");

    expect(i18n.apiErrorMessage({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw english",
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("Bitte prüfe die Eingabe und versuche es erneut.");
  });

  it("uses the provided localized fallback key before raw API messages", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessage({
      status: 409,
      data: {
        error: {
          code: "UNMAPPED",
          message: "server fallback",
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("Speicher-Fallback");
  });

  it("uses the raw API body message before HTTP status when no localized mapping exists", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessage({
      status: 418,
      data: {
        error: {
          code: "TEAPOT",
          message: "server fallback",
        },
      },
    }, { fallbackKey: "missing.fallback.key" })).toBe("server fallback");
  });

  it("uses the raw top-level error message before HTTP status when no API body message exists", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    const err = new Error("top-level fallback") as Error & { status?: number; data?: unknown };
    err.status = 503;
    err.data = { error: { code: "UNMAPPED" } };

    expect(i18n.apiErrorMessage(err)).toBe("top-level fallback");
  });

  it("falls back to HTTP status before the generic localized error only when no message exists", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessage({ status: 503 })).toBe("HTTP 503");
    expect(i18n.apiErrorMessage({ data: { error: { code: "UNMAPPED", message: "   " } } })).toBe("Something went wrong.");
  });

  it("apiErrorMessageOrRaw returns a localized reason-specific message when the reason key exists", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessageOrRaw({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw english",
          details: { reason: "name_required" },
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe(deCatalog["errors.VALIDATION_ERROR.name_required"]);
  });

  it("apiErrorMessageOrRaw preserves raw API messages for unknown or missing reasons", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessageOrRaw({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw unknown reason",
          details: { reason: "unknown_reason" },
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("raw unknown reason");

    expect(i18n.apiErrorMessageOrRaw({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          message: "raw missing reason",
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe("raw missing reason");
  });

  it("apiErrorMessageOrRaw uses raw thrown messages before localized fallbacks", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    const err = new Error("top-level raw fallback") as Error & { data?: unknown };
    err.data = { error: { code: "VALIDATION_ERROR", details: { reason: "unknown_reason" } } };

    expect(i18n.apiErrorMessageOrRaw(err, { fallbackKey: "todo.saveFailed" })).toBe("top-level raw fallback");
  });

  it("apiErrorMessageOrRaw uses explicit and default localized fallbacks only when no raw message exists", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "de", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });

    expect(i18n.apiErrorMessageOrRaw({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          details: { reason: "unknown_reason" },
        },
      },
    }, { fallbackKey: "todo.saveFailed" })).toBe(deCatalog["todo.saveFailed"]);

    expect(i18n.apiErrorMessageOrRaw({
      data: {
        error: {
          code: "VALIDATION_ERROR",
          details: { reason: "unknown_reason" },
        },
      },
    })).toBe(deCatalog["errors.generic"]);
  });

  it("hydrates text and safe attributes within only the provided root", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    document.body.innerHTML = `
      <section id="target" data-i18n-title="test.title">
        <button id="text" data-i18n-text="test.shell">Old</button>
        <button id="aria" data-i18n-aria-label="test.aria"></button>
        <input id="placeholder" data-i18n-placeholder="test.placeholder" value="Keep value" />
      </section>
      <button id="outside" data-i18n-text="test.shell">Outside</button>
    `;

    i18n.hydrateI18n(document.getElementById("target")!);

    expect(document.getElementById("target")?.getAttribute("title")).toBe("Title & <safe>");
    expect(document.getElementById("text")?.textContent).toBe("Shell text");
    expect(document.getElementById("aria")?.getAttribute("aria-label")).toBe("Close panel");
    expect(document.getElementById("placeholder")?.getAttribute("placeholder")).toBe("Type <safe>");
    expect((document.getElementById("placeholder") as HTMLInputElement).value).toBe("Keep value");
    expect(document.getElementById("outside")?.textContent).toBe("Outside");
  });

  it("updates already-hydrated shell text after pseudo locale and a hydration rerun", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    document.body.innerHTML = `<button id="shell" data-i18n-text="test.shell"></button>`;

    i18n.hydrateI18n(document.body);
    expect(document.getElementById("shell")?.textContent).toBe("Shell text");

    await i18n.setLocale("pseudo");
    i18n.hydrateI18n(document.body);

    expect(document.getElementById("shell")?.textContent).toBe("[!! Shell text !!]");
  });

  it("hydrates todo dialog shell copy after switching to German", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }) });
    document.body.innerHTML = `
      <div id="todoDialogTitle" data-i18n-text="todo.dialog.title.new">Todo</div>
      <div id="todoTitleLabel" data-i18n-text="todo.fields.title">Title</div>
      <div id="todoBodyToggle" data-i18n-aria-label="todo.notes.modeLabel">
        <button id="todoBodyWriteTab" data-i18n-text="todo.notes.markdown">markdown</button>
      </div>
      <input id="todoTags" data-i18n-placeholder="todo.tags.placeholder" />
    `;

    i18n.hydrateI18n(document.body);
    await i18n.setLocale("de");
    i18n.hydrateI18n(document.body);

    expect(document.getElementById("todoDialogTitle")?.textContent).toBe("Neues Todo");
    expect(document.getElementById("todoTitleLabel")?.textContent).toBe("Titel");
    expect(document.getElementById("todoBodyToggle")?.getAttribute("aria-label")).toBe("Modus für Notizeditor");
    expect(document.getElementById("todoBodyWriteTab")?.textContent).toBe("Markdown");
    expect(document.getElementById("todoTags")?.getAttribute("placeholder")).toBe("Tag eingeben und Enter oder Tab drücken");
  });

  it("pseudo-locale updates board and todo shell labels in place after hydration", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    document.body.innerHTML = `
      <button id="back" data-i18n-text="board.backToProjects">\u2190 Projects</button>
      <div id="filters" data-i18n-text="board.filters.label">Tags:</div>
      <div id="todoTitleLabel" data-i18n-text="todo.fields.title">Title</div>
    `;

    i18n.hydrateI18n(document.body);
    await i18n.setLocale("pseudo");
    i18n.hydrateI18n(document.body);

    expect(document.getElementById("back")?.textContent).toBe("[!! \u2190 Projects !!]");
    expect(document.getElementById("filters")?.textContent).toBe("[!! Tags: !!]");
    expect(document.getElementById("todoTitleLabel")?.textContent).toBe("[!! Title !!]");
  });

  it("applies malicious-looking catalog strings as text or inert attributes", async () => {
    const i18n = await loadModule();
    await i18n.initI18n({ locale: "en", loadLocale: loader({ en: enCatalog, pseudo: pseudoCatalog }) });
    document.body.innerHTML = `
      <div id="text" data-i18n-text="test.malicious"><span>Old</span></div>
      <input id="placeholder" data-i18n-placeholder="test.malicious" />
      <div id="title" data-i18n-title="test.malicious"></div>
    `;

    i18n.hydrateI18n(document.body);

    const malicious = "<img src=x onerror=alert(1)>";
    const text = document.getElementById("text")!;
    expect(text.textContent).toBe(malicious);
    expect(text.querySelector("img")).toBeNull();
    expect(text.innerHTML).not.toContain("<img");
    expect(document.getElementById("placeholder")?.getAttribute("placeholder")).toBe(malicious);
    expect(document.getElementById("title")?.getAttribute("title")).toBe(malicious);
  });
});

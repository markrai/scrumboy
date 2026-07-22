import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { PUBLIC_LOCALES } from "./index.js";

const localesDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "locales");

const AFFECTED_KEYS = [
  "auth.actions.login",
  "auth.forgot.helper",
  "auth.forgot.link",
  "settings.users.actions.password",
  "settings.profile.authentication.warning.noEffectiveOwner",
  "settings.profile.authentication.warning.localDisabledOwner",
] as const;

function loadCatalog(locale: string): Record<string, string> {
  return JSON.parse(readFileSync(path.join(localesDir, `${locale}.json`), "utf8")) as Record<string, string>;
}

function pseudoWrap(english: string): string {
  return `[!! ${english} !!]`;
}

describe("authentication wording", () => {
  const en = loadCatalog("en");
  const publicNonEnglish = PUBLIC_LOCALES.filter((locale) => locale !== "en");

  it("includes the affected authentication keys in every supported public catalog", () => {
    for (const locale of PUBLIC_LOCALES) {
      const catalog = loadCatalog(locale);
      for (const key of AFFECTED_KEYS) {
        expect(catalog[key], `${locale}:${key}`).toBeTruthy();
      }
    }
  });

  it("does not leave non-English affected values identical to English", () => {
    for (const locale of publicNonEnglish) {
      const catalog = loadCatalog(locale);
      for (const key of AFFECTED_KEYS) {
        expect(catalog[key], `${locale}:${key}`).not.toBe(en[key]);
      }
    }
  });

  it("keeps pseudo-localized authentication wording synchronized with English", () => {
    const pseudo = loadCatalog("pseudo");
    for (const key of AFFECTED_KEYS) {
      expect(pseudo[key]).toBe(pseudoWrap(en[key]));
    }
  });

  it("keeps the English owner warning scoped to the current account", () => {
    const warning = en["settings.profile.authentication.warning.noEffectiveOwner"];
    expect(warning).toMatch(/this owner account/i);
    expect(warning).not.toMatch(/no effective owner login method is available/i);
  });

  it("keeps recovery conditional on SSO becoming unavailable", () => {
    const warning = en["settings.profile.authentication.warning.localDisabledOwner"];
    expect(warning).toMatch(/if SSO becomes unavailable/i);
    expect(warning).not.toMatch(/disabled\. Recovery requires host access/);
  });

  it("distinguishes Scrumboy password recovery from SSO credential recovery", () => {
    const helper = en["auth.forgot.helper"];
    expect(helper).toMatch(/Scrumboy password/i);
    expect(helper).toMatch(/SSO/);
    expect(helper).toMatch(/identity provider/i);
    expect(helper).not.toMatch(/identity-provider credentials/i);
  });
});

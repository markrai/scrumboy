import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const scriptPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "verify-i18n-locales.mjs",
);

describe("verify-i18n-locales script", () => {
  async function writeBootstrapSource(webDir: string, keys: string[]): Promise<void> {
    const i18nDir = path.join(webDir, "modules", "i18n");
    await fs.mkdir(i18nDir, { recursive: true });
    const entries = keys.map((key) => `  "${key}": "${key}",`).join("\n");
    await fs.writeFile(
      path.join(i18nDir, "index.ts"),
      `type MessageCatalog = Record<string, string>;\nconst BOOTSTRAP_EN_CATALOG: MessageCatalog = {\n${entries}\n};\n`,
    );
  }

  it("fails clearly when locale keys drift from English", async () => {
    const webDir = await fs.mkdtemp(path.join(os.tmpdir(), "scrumboy-i18n-check-"));
    const localesDir = path.join(webDir, "modules", "i18n", "locales");
    await fs.mkdir(localesDir, { recursive: true });
    await writeBootstrapSource(webDir, []);
    await fs.writeFile(path.join(localesDir, "en.json"), JSON.stringify({
      "common.cancel": "Cancel",
      "common.save": "Save",
    }));
    await fs.writeFile(path.join(localesDir, "pseudo.json"), JSON.stringify({
      "common.cancel": "[!! Cancel !!]",
      "common.extra": "[!! Extra !!]",
    }));

    const result = spawnSync(process.execPath, [scriptPath], {
      cwd: webDir,
      encoding: "utf8",
      env: {
        ...process.env,
        SCRUMBOY_WEB_DIR: webDir,
      },
    });

    expect(result.status).toBe(1);
    expect(result.stderr || result.stdout).toContain("i18n locale key parity failed");
    expect(result.stderr || result.stdout).toContain("missing: common.save");
    expect(result.stderr || result.stdout).toContain("extra: common.extra");
  });

  it("fails clearly when required bootstrap error keys drift from English", async () => {
    const webDir = await fs.mkdtemp(path.join(os.tmpdir(), "scrumboy-i18n-bootstrap-check-"));
    const localesDir = path.join(webDir, "modules", "i18n", "locales");
    await fs.mkdir(localesDir, { recursive: true });
    await writeBootstrapSource(webDir, ["errors.generic"]);
    await fs.writeFile(path.join(localesDir, "en.json"), JSON.stringify({
      "errors.BAD_REQUEST": "Bad request",
      "errors.generic": "Something went wrong.",
    }));
    await fs.writeFile(path.join(localesDir, "de.json"), JSON.stringify({
      "errors.BAD_REQUEST": "Ungültige Anfrage",
      "errors.generic": "Etwas ist schiefgelaufen.",
    }));
    await fs.writeFile(path.join(localesDir, "pseudo.json"), JSON.stringify({
      "errors.BAD_REQUEST": "[!! Bad request !!]",
      "errors.generic": "[!! Something went wrong. !!]",
    }));

    const result = spawnSync(process.execPath, [scriptPath], {
      cwd: webDir,
      encoding: "utf8",
      env: {
        ...process.env,
        SCRUMBOY_WEB_DIR: webDir,
      },
    });

    expect(result.status).toBe(1);
    expect(result.stderr || result.stdout).toContain("i18n bootstrap catalog coverage failed");
    expect(result.stderr || result.stdout).toContain("errors.BAD_REQUEST");
  });

  it("fails clearly when required bootstrap auth keys drift from English", async () => {
    const webDir = await fs.mkdtemp(path.join(os.tmpdir(), "scrumboy-i18n-auth-bootstrap-check-"));
    const localesDir = path.join(webDir, "modules", "i18n", "locales");
    await fs.mkdir(localesDir, { recursive: true });
    await writeBootstrapSource(webDir, ["errors.generic"]);
    await fs.writeFile(path.join(localesDir, "en.json"), JSON.stringify({
      "auth.signIn.title": "Sign in",
      "errors.generic": "Something went wrong.",
    }));
    await fs.writeFile(path.join(localesDir, "de.json"), JSON.stringify({
      "auth.signIn.title": "Anmelden",
      "errors.generic": "Etwas ist schiefgelaufen.",
    }));
    await fs.writeFile(path.join(localesDir, "pseudo.json"), JSON.stringify({
      "auth.signIn.title": "[!! Sign in !!]",
      "errors.generic": "[!! Something went wrong. !!]",
    }));

    const result = spawnSync(process.execPath, [scriptPath], {
      cwd: webDir,
      encoding: "utf8",
      env: {
        ...process.env,
        SCRUMBOY_WEB_DIR: webDir,
      },
    });

    expect(result.status).toBe(1);
    expect(result.stderr || result.stdout).toContain("i18n bootstrap catalog coverage failed");
    expect(result.stderr || result.stdout).toContain("auth.signIn.title");
  });

  it("fails clearly when translated placeholders drift from English", async () => {
    const webDir = await fs.mkdtemp(path.join(os.tmpdir(), "scrumboy-i18n-placeholder-check-"));
    const localesDir = path.join(webDir, "modules", "i18n", "locales");
    await fs.mkdir(localesDir, { recursive: true });
    await writeBootstrapSource(webDir, []);
    await fs.writeFile(path.join(localesDir, "en.json"), JSON.stringify({
      "todo.creator.userFallback": "User {id}",
    }));
    await fs.writeFile(path.join(localesDir, "de.json"), JSON.stringify({
      "todo.creator.userFallback": "Benutzer {name}",
    }));
    await fs.writeFile(path.join(localesDir, "pseudo.json"), JSON.stringify({
      "todo.creator.userFallback": "[!! User {id} !!]",
    }));

    const result = spawnSync(process.execPath, [scriptPath], {
      cwd: webDir,
      encoding: "utf8",
      env: {
        ...process.env,
        SCRUMBOY_WEB_DIR: webDir,
      },
    });

    expect(result.status).toBe(1);
    expect(result.stderr || result.stdout).toContain("i18n placeholder consistency failed");
    expect(result.stderr || result.stdout).toContain("de.json todo.creator.userFallback");
    expect(result.stderr || result.stdout).toContain("expected {id}; found {name}");
  });
});

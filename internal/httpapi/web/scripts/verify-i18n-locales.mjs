import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const webDir = process.env.SCRUMBOY_WEB_DIR
  ? path.resolve(process.env.SCRUMBOY_WEB_DIR)
  : path.resolve(__dirname, "..");
const localesDir = path.join(webDir, "modules", "i18n", "locales");
const i18nIndexPath = path.join(webDir, "modules", "i18n", "index.ts");
const REQUIRED_BOOTSTRAP_PREFIXES = ["errors.", "auth."];
const BOOTSTRAP_KEY_EXEMPTIONS = new Map();

function flattenMessages(value, prefix = "", out = new Map()) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${prefix || "catalog"} must be an object`);
  }
  for (const [key, child] of Object.entries(value)) {
    const nextKey = prefix ? `${prefix}.${key}` : key;
    if (typeof child === "string") {
      out.set(nextKey, child);
      continue;
    }
    if (child && typeof child === "object" && !Array.isArray(child)) {
      flattenMessages(child, nextKey, out);
      continue;
    }
    throw new Error(`${nextKey} must be a string`);
  }
  return out;
}

async function readCatalog(fileName) {
  const raw = await fs.readFile(path.join(localesDir, fileName), "utf8");
  return flattenMessages(JSON.parse(raw));
}

async function readBootstrapCatalogKeys() {
  let source;
  try {
    source = await fs.readFile(i18nIndexPath, "utf8");
  } catch {
    throw new Error(`Cannot read bootstrap i18n catalog source: ${i18nIndexPath}`);
  }

  const match = source.match(/const BOOTSTRAP_EN_CATALOG: MessageCatalog = \{([\s\S]*?)\n\};/);
  if (!match) {
    throw new Error(`Cannot locate BOOTSTRAP_EN_CATALOG in: ${i18nIndexPath}`);
  }

  const keys = new Set();
  const keyPattern = /"([^"]+)":/g;
  let keyMatch;
  while ((keyMatch = keyPattern.exec(match[1])) !== null) {
    keys.add(keyMatch[1]);
  }
  return keys;
}

function sortedDifference(left, right) {
  return [...left].filter((key) => !right.has(key)).sort();
}

function placeholders(message) {
  return [...new Set(message.match(/\{[a-zA-Z0-9_.-]+\}/g) ?? [])].sort();
}

async function main() {
  let entries;
  try {
    entries = await fs.readdir(localesDir, { withFileTypes: true });
  } catch (err) {
    throw new Error(`Cannot read i18n locale directory: ${localesDir}`);
  }

  const localeFiles = entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".json"))
    .map((entry) => entry.name)
    .sort();

  if (!localeFiles.includes("en.json")) {
    throw new Error("Missing canonical i18n source catalog: modules/i18n/locales/en.json");
  }

  const en = await readCatalog("en.json");
  const failures = [];

  for (const fileName of localeFiles) {
    const catalog = await readCatalog(fileName);
    const missing = sortedDifference(en.keys(), catalog);
    const extra = sortedDifference(catalog.keys(), en);
    if (missing.length > 0 || extra.length > 0) {
      failures.push({ fileName, missing, extra });
    }
  }

  if (failures.length > 0) {
    const lines = ["i18n locale key parity failed:"];
    for (const failure of failures) {
      lines.push(`- ${failure.fileName}`);
      if (failure.missing.length > 0) {
        lines.push(`  missing: ${failure.missing.join(", ")}`);
      }
      if (failure.extra.length > 0) {
        lines.push(`  extra: ${failure.extra.join(", ")}`);
      }
    }
    throw new Error(lines.join("\n"));
  }

  const placeholderFailures = [];
  for (const fileName of localeFiles) {
    if (fileName === "en.json") continue;
    const catalog = await readCatalog(fileName);
    for (const [key, englishMessage] of en) {
      const expected = placeholders(englishMessage);
      const actual = placeholders(catalog.get(key));
      if (expected.join("\0") !== actual.join("\0")) {
        placeholderFailures.push({ fileName, key, expected, actual });
      }
    }
  }

  if (placeholderFailures.length > 0) {
    const lines = ["i18n placeholder consistency failed:"];
    for (const failure of placeholderFailures) {
      lines.push(
        `- ${failure.fileName} ${failure.key}: expected ${failure.expected.join(", ") || "none"}; found ${failure.actual.join(", ") || "none"}`,
      );
    }
    throw new Error(lines.join("\n"));
  }

  const bootstrapKeys = await readBootstrapCatalogKeys();
  const missingBootstrapKeys = [...en.keys()]
    .filter((key) => REQUIRED_BOOTSTRAP_PREFIXES.some((prefix) => key.startsWith(prefix)))
    .filter((key) => !bootstrapKeys.has(key) && !BOOTSTRAP_KEY_EXEMPTIONS.has(key))
    .sort();

  if (missingBootstrapKeys.length > 0) {
    const lines = [
      "i18n bootstrap catalog coverage failed:",
      `- Missing bootstrap keys in modules/i18n/index.ts: ${missingBootstrapKeys.join(", ")}`,
    ];
    const exemptions = [...BOOTSTRAP_KEY_EXEMPTIONS.entries()]
      .map(([key, reason]) => `${key} (${reason})`);
    if (exemptions.length > 0) {
      lines.push(`- Documented exemptions: ${exemptions.join(", ")}`);
    }
    throw new Error(lines.join("\n"));
  }

  console.log(`i18n locale key and placeholder parity passed (${localeFiles.length} catalogs, bootstrap coverage ok).`);
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});

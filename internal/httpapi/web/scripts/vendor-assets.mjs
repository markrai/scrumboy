import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const defaultWebDir = path.resolve(scriptDir, "..");

const copiedAssets = [
  ["node_modules/uplot/dist/uPlot.iife.min.js", "vendor/uplot.min.js"],
  ["node_modules/uplot/dist/uPlot.min.css", "vendor/uplot.min.css"],
  ["node_modules/markdown-it/dist/markdown-it.min.js", "vendor/markdown-it.min.js"],
  ["node_modules/dompurify/dist/purify.min.js", "vendor/purify.min.js"],
];

export const mermaidBundlePath = "vendor/mermaid.min.js";
export const mermaidMetadataPath = "vendor/mermaid.meta.json";
const mermaidLegalComments = "eof";
export const requiredVendorFiles = Object.freeze([
  ...copiedAssets.map(([, destination]) => destination),
  mermaidBundlePath,
  mermaidMetadataPath,
]);

function normalizePath(value) {
  return value.replaceAll("\\", "/");
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

async function readJson(filePath) {
  return JSON.parse(await fs.readFile(filePath, "utf8"));
}

async function readInstalledVersion(webDir, packageName) {
  const manifestPath = path.join(webDir, "node_modules", ...packageName.split("/"), "package.json");
  const manifest = await readJson(manifestPath);
  assert(typeof manifest.version === "string", `${packageName} package metadata does not contain a version`);
  return manifest.version;
}

function assertExactVersion(actual, intended, packageName) {
  assert(
    typeof intended === "string" && /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(intended),
    `${packageName} must be pinned to an exact version for deterministic vendor generation`,
  );
  assert(
    actual === intended,
    `${packageName} ${actual} is installed, but package.json intends ${intended}`,
  );
}

function sha256(contents) {
  return createHash("sha256").update(contents).digest("hex");
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

async function findPackageRoot(inputPath, webDir) {
  let directory = path.dirname(path.resolve(webDir, inputPath));
  while (normalizePath(directory).includes("/node_modules/")) {
    try {
      const manifest = await readJson(path.join(directory, "package.json"));
      if (typeof manifest.name === "string" && typeof manifest.version === "string") {
        return { directory, manifest };
      }
    } catch {}
    const parent = path.dirname(directory);
    if (parent === directory) break;
    directory = parent;
  }
  return null;
}

async function readLicenseDocuments(packageRoot, packageName) {
  const entries = (await fs.readdir(packageRoot, { withFileTypes: true }))
    .filter((entry) => entry.isFile())
    .map((entry) => entry.name)
    .sort(compareText);
  const licenseFiles = entries.filter((name) =>
    /^(?:licen[cs]e|copying|notice)(?:[-_][a-z0-9]+)*(?:\.(?:txt|md|markdown))?$/i.test(name),
  );
  const documents = [];
  for (const file of licenseFiles) {
    const text = (await fs.readFile(path.join(packageRoot, file), "utf8"))
      .replaceAll("\r\n", "\n")
      .trim();
    if (text) documents.push({ file, text });
  }
  if (documents.length > 0) return documents;

  for (const file of entries.filter((name) => /^readme(?:\.(?:md|markdown|txt))?$/i.test(name))) {
    const readme = (await fs.readFile(path.join(packageRoot, file), "utf8")).replaceAll("\r\n", "\n");
    const marker = readme.search(/(?:^|\n)(?:(?:#{1,6}\s+license)|(?:\(?the [^\n]* license\)?))\s*\n/i);
    if (marker >= 0) {
      documents.push({ file: `${file} (license section)`, text: readme.slice(marker).trim() });
      break;
    }
  }
  assert(documents.length > 0, `Bundled package ${packageName} does not expose license text`);
  return documents;
}

async function collectBundledPackageNotices(inputPaths, webDir) {
  const packages = new Map();
  for (const inputPath of inputPaths) {
    const packageRoot = await findPackageRoot(inputPath, webDir);
    if (!packageRoot) continue;
    const { directory, manifest } = packageRoot;
    const key = `${manifest.name}@${manifest.version}`;
    if (packages.has(key)) continue;
    const documents = await readLicenseDocuments(directory, key);
    const licenseText = documents.map(({ file, text }) => `[${file}]\n${text}`).join("\n\n");
    packages.set(key, {
      name: manifest.name,
      version: manifest.version,
      license: typeof manifest.license === "string" ? manifest.license : "UNSPECIFIED",
      files: documents.map(({ file }) => file),
      text: licenseText,
      textSha256: sha256(Buffer.from(licenseText, "utf8")),
    });
  }
  return [...packages.values()].sort((left, right) =>
    compareText(`${left.name}@${left.version}`, `${right.name}@${right.version}`),
  );
}

function renderBundledPackageNotices(packages) {
  const lines = [
    "Third-party software notices for vendor/mermaid.min.js",
    "Generated from the package versions in esbuild's actual input graph.",
  ];
  for (const bundledPackage of packages) {
    lines.push(
      "",
      `Package: ${bundledPackage.name}@${bundledPackage.version}`,
      `Declared-License: ${bundledPackage.license}`,
      `License-Files: ${bundledPackage.files.join(", ")}`,
      `License-Text-SHA256: ${bundledPackage.textSha256}`,
      "",
      bundledPackage.text,
    );
  }
  return `${lines.map((line) => ` * ${line.replaceAll("*/", "* /")}`).join("\n")}\n`;
}

function findSinglePackageEntry(inputPaths, packageName, packageEntry) {
  const suffix = `/node_modules/${packageName}/${packageEntry}`;
  const matches = inputPaths.filter((input) => normalizePath(input).endsWith(suffix));
  assert(
    matches.length === 1,
    `Generated Mermaid bundle must incorporate exactly one ${packageName}/${packageEntry} input; found ${matches.length}`,
  );
  return matches[0];
}

export function resolveWebDir() {
  return process.env.SCRUMBOY_WEB_DIR
    ? path.resolve(process.env.SCRUMBOY_WEB_DIR)
    : defaultWebDir;
}

export async function buildMermaidVendorAsset(webDir = resolveWebDir()) {
  const rootManifest = await readJson(path.join(webDir, "package.json"));
  const result = await build({
    absWorkingDir: webDir,
    entryPoints: [path.join(scriptDir, "mermaid-vendor-entry.mjs")],
    bundle: true,
    charset: "utf8",
    format: "iife",
    legalComments: mermaidLegalComments,
    logLevel: "silent",
    metafile: true,
    minify: true,
    platform: "browser",
    sourcemap: false,
    target: ["es2020"],
    treeShaking: true,
    write: false,
  });

  assert(result.outputFiles.length === 1, "Mermaid vendor generation must produce exactly one browser asset");
  const inputPaths = Object.keys(result.metafile.inputs).map((input) => path.resolve(webDir, input));
  const mermaidEntry = findSinglePackageEntry(inputPaths, "mermaid", "dist/mermaid.core.mjs");
  const katexEntry = findSinglePackageEntry(inputPaths, "katex", "dist/katex.mjs");
  const mermaidRoot = path.dirname(path.dirname(mermaidEntry));
  const katexRoot = path.dirname(path.dirname(katexEntry));
  const versions = {
    mermaid: (await readJson(path.join(mermaidRoot, "package.json"))).version,
    katex: (await readJson(path.join(katexRoot, "package.json"))).version,
    esbuild: await readInstalledVersion(webDir, "esbuild"),
  };

  assertExactVersion(versions.mermaid, rootManifest.dependencies?.mermaid, "mermaid");
  assertExactVersion(versions.katex, rootManifest.overrides?.katex, "katex");
  assertExactVersion(versions.esbuild, rootManifest.devDependencies?.esbuild, "esbuild");

  const bundledPackages = await collectBundledPackageNotices(inputPaths, webDir);
  const generatedNotices = renderBundledPackageNotices(bundledPackages);
  const contents = Buffer.concat([
    Buffer.from(result.outputFiles[0].contents),
    Buffer.from(`/*!\n${generatedNotices} */\n`, "utf8"),
  ]);
  const metadata = `${JSON.stringify({
    schemaVersion: 2,
    artifact: mermaidBundlePath,
    sha256: sha256(contents),
    dependencies: {
      mermaid: {
        version: versions.mermaid,
        entry: normalizePath(path.relative(webDir, mermaidEntry)),
      },
      katex: {
        version: versions.katex,
        entry: normalizePath(path.relative(webDir, katexEntry)),
      },
    },
    builder: {
      esbuild: versions.esbuild,
      legalComments: mermaidLegalComments,
    },
    notices: bundledPackages.map(({ name, version, license, files, textSha256 }) => ({
      name,
      version,
      license,
      files,
      textSha256,
    })),
  }, null, 2)}\n`;

  return { contents, metadata };
}

export async function buildExpectedVendorAssets(webDir = resolveWebDir()) {
  const assets = [];
  for (const [source, relativePath] of copiedAssets) {
    assets.push({
      relativePath,
      contents: await fs.readFile(path.join(webDir, ...source.split("/"))),
    });
  }

  const mermaid = await buildMermaidVendorAsset(webDir);
  assets.push({ relativePath: mermaidBundlePath, contents: mermaid.contents });
  assets.push({ relativePath: mermaidMetadataPath, contents: Buffer.from(mermaid.metadata, "utf8") });
  return assets;
}

export async function findMismatchedVendorAssets(expectedAssets, webDir = resolveWebDir()) {
  const mismatched = [];
  for (const asset of expectedAssets) {
    try {
      const actual = await fs.readFile(path.join(webDir, ...asset.relativePath.split("/")));
      if (!actual.equals(asset.contents)) {
        mismatched.push(asset.relativePath);
      }
    } catch {
      mismatched.push(asset.relativePath);
    }
  }
  return mismatched;
}

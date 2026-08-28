import { createHash } from 'node:crypto';
import {
  copyFile,
  mkdir,
  readFile,
  readdir,
  writeFile,
} from 'node:fs/promises';
import { dirname, extname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

export const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const repositoryRoot = resolve(webRoot, '..', '..', '..');
export const artifactRoot = resolve(repositoryRoot, 'mobile', 'capacitor', 'www');
export const artifactManifestName = '.artifact-manifest.json';

const explicitRuntimeFiles = [
  'favicon.ico',
  'icon-512.png',
  'styles.css',
  'scrumbabylogo.png',
  'scrumbabytrash.png',
  'scrumboytext.png',
  'mic.svg',
  'new.svg',
  'postit.svg',
  'trash.svg',
  'mermaid-semantic-edges.json',
  'fonts/kalam.ttf',
  'static/sounds/notify.mp3',
  'static/sounds/notify.ogg',
  'wallpapers/default.jpg',
  'assets/calendar/apple.webp',
  'assets/calendar/google.webp',
  'assets/flags/COUNTRY_FLAG_ICONS_LICENSE.txt',
  'assets/flags/bd.svg',
  'assets/flags/br.svg',
  'assets/flags/cn.svg',
  'assets/flags/de.svg',
  'assets/flags/fr.svg',
  'assets/flags/id.svg',
  'assets/flags/in.svg',
  'assets/flags/ir.svg',
  'assets/flags/it.svg',
  'assets/flags/jp.svg',
  'assets/flags/kr.svg',
  'assets/flags/mx.svg',
  'assets/flags/my.svg',
  'assets/flags/pk.svg',
  'assets/flags/pl.svg',
  'assets/flags/ru.svg',
  'assets/flags/sa.svg',
  'assets/flags/th.svg',
  'assets/flags/tr.svg',
  'assets/flags/tz.svg',
  'assets/flags/ua.svg',
  'assets/flags/us.svg',
  'assets/flags/vn.svg',
  'vendor/markdown-it.min.js',
  'vendor/mermaid.min.js',
  'vendor/purify.min.js',
  'vendor/sortable.min.js',
  'vendor/uplot.min.css',
  'vendor/uplot.min.js',
];

function assertInside(parent, child, label) {
  const prefix = `${resolve(parent)}${sep}`.toLowerCase();
  const candidate = resolve(child).toLowerCase();
  if (!candidate.startsWith(prefix)) {
    throw new Error(`${label} escaped its expected root: ${child}`);
  }
}

async function copyRelativeFile(relativePath) {
  const source = resolve(webRoot, relativePath);
  const destination = resolve(artifactRoot, relativePath);
  assertInside(webRoot, source, 'source path');
  assertInside(artifactRoot, destination, 'artifact path');
  await mkdir(dirname(destination), { recursive: true });
  await copyFile(source, destination);
}

function moduleSpecifiers(source) {
  const specifiers = new Set();
  const patterns = [
    /\b(?:import|export)\s+(?:[^'";]*?\s+from\s+)?['"]([^'"]+)['"]/g,
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      specifiers.add(match[1]);
    }
  }
  return [...specifiers];
}

export async function copyReachableModuleGraph() {
  const queue = ['app.js'];
  const copied = new Set();
  while (queue.length > 0) {
    const relativePath = queue.shift();
    if (copied.has(relativePath)) continue;
    const sourcePath = resolve(webRoot, relativePath);
    assertInside(webRoot, sourcePath, 'module source');
    if (extname(sourcePath) !== '.js') {
      throw new Error(`Runtime module is not JavaScript: ${relativePath}`);
    }
    const source = await readFile(sourcePath, 'utf8');
    await copyRelativeFile(relativePath);
    copied.add(relativePath);

    for (const specifier of moduleSpecifiers(source)) {
      if (!specifier.startsWith('.')) continue;
      const dependency = resolve(dirname(sourcePath), specifier);
      assertInside(webRoot, dependency, `module import ${specifier}`);
      const dependencyRelative = relative(webRoot, dependency).replaceAll('\\', '/');
      if (!dependencyRelative.startsWith('dist/')) {
        throw new Error(`Runtime module import is outside generated dist/: ${relativePath} -> ${specifier}`);
      }
      queue.push(dependencyRelative);
    }
  }
  return [...copied].sort();
}

export async function copyExplicitRuntimeAssets() {
  for (const relativePath of explicitRuntimeFiles) {
    await copyRelativeFile(relativePath);
  }
  const localeDirectory = resolve(webRoot, 'dist', 'i18n', 'locales');
  for (const entry of (await readdir(localeDirectory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    if (entry.isFile() && entry.name.endsWith('.json')) {
      await copyRelativeFile(`dist/i18n/locales/${entry.name}`);
    }
  }
}

export function transformIndex(source, version) {
  if (!source.includes('{{VERSION}}')) {
    throw new Error('Authoritative index.html no longer contains the expected {{VERSION}} token');
  }
  const manifestLinks = source.match(/^[ \t]*<link\s+rel="manifest"[^>]*>[ \t]*\r?\n/gm) || [];
  if (manifestLinks.length !== 1) {
    throw new Error(`Expected one PWA manifest link, found ${manifestLinks.length}`);
  }
  let output = source.replace(manifestLinks[0], '');
  const appVersionMeta = `<meta name="app-version" content="{{VERSION}}" />`;
  if (!output.includes(appVersionMeta)) {
    throw new Error('Could not locate app-version meta element for the runtime marker');
  }
  output = output.replace(
    appVersionMeta,
    `${appVersionMeta}\n    <meta name="scrumboy-runtime" content="capacitor" />`,
  );
  output = output.replaceAll('{{VERSION}}', version);
  if (/\{\{[^}]+\}\}/.test(output)) {
    throw new Error('Generated index.html contains an unresolved template token');
  }
  return output;
}

async function listFiles(directory, prefix = '') {
  const files = [];
  const entries = await readdir(directory, { withFileTypes: true });
  entries.sort((a, b) => a.name.localeCompare(b.name));
  for (const entry of entries) {
    const relativePath = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (relativePath === artifactManifestName) continue;
    if (entry.isDirectory()) {
      files.push(...await listFiles(join(directory, entry.name), relativePath));
    } else if (entry.isFile()) {
      files.push(relativePath);
    }
  }
  return files;
}

async function fileDigest(path) {
  return createHash('sha256').update(await readFile(path)).digest('hex');
}

export async function buildArtifactManifest(version) {
  const files = [];
  for (const relativePath of await listFiles(artifactRoot)) {
    files.push({
      path: relativePath,
      sha256: await fileDigest(resolve(artifactRoot, relativePath)),
    });
  }
  return `${JSON.stringify({ version, files }, null, 2)}\n`;
}

export async function writeArtifactManifest(version) {
  const content = await buildArtifactManifest(version);
  await writeFile(resolve(artifactRoot, artifactManifestName), content, 'utf8');
  return content;
}

export function extractModuleSpecifiers(source) {
  return moduleSpecifiers(source);
}

export async function artifactFiles() {
  return listFiles(artifactRoot);
}

export function explicitAssetAllowlist() {
  return [...explicitRuntimeFiles];
}

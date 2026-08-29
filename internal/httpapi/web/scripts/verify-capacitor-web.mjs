import { readFile, readdir, stat } from 'node:fs/promises';
import { dirname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  artifactFiles,
  artifactManifestName,
  artifactRoot,
  buildArtifactManifest,
  extractModuleSpecifiers,
  webRoot,
} from './capacitor-web-artifact-lib.mjs';

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function exists(path) {
  try {
    return (await stat(path)).isFile();
  } catch {
    return false;
  }
}

function localReferencePath(reference) {
  if (!reference || reference.startsWith('#') || reference.startsWith('data:')) return null;
  if (/^[a-z][a-z0-9+.-]*:/i.test(reference) || reference.startsWith('//')) return null;
  const path = reference.split(/[?#]/, 1)[0];
  return path.startsWith('/') ? path.slice(1) : path;
}

async function assertLocalReferencesExist(relativeFile, source, pattern) {
  for (const match of source.matchAll(pattern)) {
    const reference = localReferencePath(match[1]);
    if (!reference) continue;
    const target = match[1].startsWith('/')
      ? resolve(artifactRoot, reference)
      : resolve(artifactRoot, dirname(relativeFile), reference);
    assert(await exists(target), `${relativeFile} references missing local asset ${match[1]}`);
  }
}

async function verifyModuleGraph(files) {
  const javascriptFiles = files.filter((file) => file === 'app.js' || (file.startsWith('dist/') && file.endsWith('.js')));
  const reachable = new Set();
  const queue = ['app.js'];
  while (queue.length > 0) {
    const relativeFile = queue.shift();
    if (reachable.has(relativeFile)) continue;
    assert(javascriptFiles.includes(relativeFile), `Missing reachable JavaScript module ${relativeFile}`);
    reachable.add(relativeFile);
    const source = await readFile(resolve(artifactRoot, relativeFile), 'utf8');
    for (const specifier of extractModuleSpecifiers(source)) {
      if (!specifier.startsWith('.')) continue;
      const target = relative(artifactRoot, resolve(artifactRoot, dirname(relativeFile), specifier)).replaceAll('\\', '/');
      assert(target.startsWith('dist/'), `${relativeFile} imports outside generated dist/: ${specifier}`);
      assert(await exists(resolve(artifactRoot, target)), `${relativeFile} imports missing module ${specifier}`);
      queue.push(target);
    }
    await assertLocalReferencesExist(relativeFile, source, /['"](\/[^'"\s]+\.(?:png|jpe?g|svg|webp|ico|mp3|ogg|json)(?:\?[^'"]*)?)['"]/g);
  }
  const unreferenced = javascriptFiles.filter((file) => !reachable.has(file));
  assert(unreferenced.length === 0, `Artifact contains unreachable JavaScript: ${unreferenced.join(', ')}`);
}

export async function verifyCapacitorWebArtifact() {
  assert(await exists(resolve(artifactRoot, 'index.html')), 'Missing generated index.html');
  const index = await readFile(resolve(artifactRoot, 'index.html'), 'utf8');
  assert(!/\{\{[^}]+\}\}/.test(index), 'Generated index.html contains an unresolved template token');
  assert(index.includes('<meta name="scrumboy-runtime" content="capacitor" />'), 'Generated index.html lacks the Capacitor runtime marker');
  assert(!/<link\s+rel="manifest"/i.test(index), 'Generated index.html must not expose the PWA manifest');
  assert(index.includes('<script type="module" src="/bootstrap.js"></script>'), 'Generated index.html does not load the mobile bootstrap');
  assert(!/<script\b[^>]*\bsrc="\/app\.js(?:[?#][^"]*)?"/i.test(index), 'Generated index.html must not start the product app before C2 installs the runtime');
  assert(!/\b(?:src|href|content)="https?:\/\//i.test(index), 'Generated index.html contains a remote URL');
  await assertLocalReferencesExist('index.html', index, /(?:src|href)="([^"]+)"/g);

  assert(await exists(resolve(artifactRoot, 'bootstrap.js')), 'Missing generated bootstrap.js');
  const bootstrap = await readFile(resolve(artifactRoot, 'bootstrap.js'), 'utf8');
  assert(bootstrap.includes('meta[name="scrumboy-runtime"]'), 'Bootstrap does not verify the Capacitor runtime marker');
  assert(bootstrap.includes('scrumboy.server.origin.v1'), 'Bootstrap does not own the selected-server preference');
  assert(bootstrap.includes('ScrumboyTransport'), 'Bootstrap does not contain the native transport binding');
  assert(bootstrap.includes('/dist/platform/runtime.js'), 'Bootstrap does not import the packaged runtime seam');
  assert(bootstrap.includes('/app.js'), 'Bootstrap does not retain the packaged product entry handoff');
  assert(bootstrap.indexOf('installAppRuntime') < bootstrap.lastIndexOf('/app.js'), 'Bootstrap product import is not ordered after runtime installation');
  // The pinned @capacitor/core bundle carries a dormant CapacitorHttp web
  // fallback with fetch(). Audit Scrumboy-authored shell sources so that
  // upstream dead code is not mistaken for the selected-server transport.
  const shellDirectory = resolve(artifactRoot, '..', 'shell');
  const shellSources = await Promise.all(
    (await readdir(shellDirectory))
      .filter((name) => name.endsWith('.ts'))
      .map((name) => readFile(resolve(shellDirectory, name), 'utf8')),
  );
  assert(
    !/\b(?:fetch|EventSource|XMLHttpRequest)\s*\(/.test(shellSources.join('\n')),
    'Scrumboy mobile shell source contains direct networking',
  );
  // @capacitor/browser intentionally carries a web fallback implemented with
  // window.open(). Audit Scrumboy-authored shell sources so that the pinned
  // plugin can own external OIDC navigation without allowing direct WebView
  // or window navigation in the application shell.
  assert(
    !/(?:\b(?:window|globalThis|self)\.open|(?<![.$\w])open)\s*\(|\b(?:window\.)?location\.(?:assign|replace)\s*\(/.test(shellSources.join('\n')),
    'Scrumboy mobile shell source opens remote navigation directly',
  );

  const files = await artifactFiles();
  const forbidden = files.filter((file) =>
    file === 'sw.js' ||
    file.startsWith('modules/') ||
    file.startsWith('scripts/') ||
    file.startsWith('node_modules/') ||
    /(?:^|\/)[^/]*_test\.[^/]+$/.test(file) ||
    /(?:^|\/)[^/]*\.test\.[^/]+$/.test(file) ||
    /(?:^|\/)package(?:-lock)?\.json$/.test(file) ||
    /(?:^|\/)tsconfig\.json$/.test(file) ||
    /(?:^|\/)landing(?:\.|\/)/.test(file),
  );
  assert(forbidden.length === 0, `Artifact contains excluded content: ${forbidden.join(', ')}`);

  const styles = await readFile(resolve(artifactRoot, 'styles.css'), 'utf8');
  await assertLocalReferencesExist('styles.css', styles, /url\(["']?([^"')]+)["']?\)/g);
  await verifyModuleGraph(files);

  const sourceLocales = (await readdir(resolve(webRoot, 'modules', 'i18n', 'locales')))
    .filter((name) => name.endsWith('.json'))
    .sort();
  const artifactLocales = (await readdir(resolve(artifactRoot, 'dist', 'i18n', 'locales')))
    .filter((name) => name.endsWith('.json'))
    .sort();
  assert(JSON.stringify(artifactLocales) === JSON.stringify(sourceLocales), 'Generated locale catalog set is incomplete');
  assert(await exists(resolve(artifactRoot, 'mermaid-semantic-edges.json')), 'Missing semantic Mermaid config');

  const manifestPath = resolve(artifactRoot, artifactManifestName);
  assert(await exists(manifestPath), `Missing ${artifactManifestName}`);
  const recordedManifest = await readFile(manifestPath, 'utf8');
  const parsed = JSON.parse(recordedManifest);
  assert(typeof parsed.version === 'string' && parsed.version.length > 0, 'Artifact manifest lacks a version');
  const expectedManifest = await buildArtifactManifest(parsed.version);
  assert(recordedManifest === expectedManifest, 'Artifact manifest does not match file contents or deterministic ordering');

  return { fileCount: files.length + 1, version: parsed.version };
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  const result = await verifyCapacitorWebArtifact();
  console.log(`Verified ${result.fileCount} deterministic mobile web files for version ${result.version}`);
}

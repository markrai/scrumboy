import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import {
  artifactRoot,
  copyExplicitRuntimeAssets,
  copyMobileBootstrap,
  copyReachableModuleGraph,
  transformIndex,
  webRoot,
  writeArtifactManifest,
} from './capacitor-web-artifact-lib.mjs';
import { verifyCapacitorWebArtifact } from './verify-capacitor-web.mjs';
import { buildMobileShell } from '../../../../mobile/capacitor/scripts/build-shell.mjs';

function versionArgument(argv) {
  const index = argv.indexOf('--version');
  if (index < 0 || !argv[index + 1] || argv[index + 1].startsWith('--')) {
    throw new Error('Usage: node scripts/build-capacitor-web.mjs --version <existing-scrumboy-version>');
  }
  const version = argv[index + 1].trim();
  if (!version || /[\r\n]/.test(version)) {
    throw new Error('The supplied version must be a non-empty single-line value');
  }
  return version;
}

export async function buildCapacitorWebArtifact(version) {
  await buildMobileShell();
  await rm(artifactRoot, { recursive: true, force: true });
  await mkdir(artifactRoot, { recursive: true });

  const sourceIndex = await readFile(resolve(webRoot, 'index.html'), 'utf8');
  await writeFile(resolve(artifactRoot, 'index.html'), transformIndex(sourceIndex, version), 'utf8');
  const modules = await copyReachableModuleGraph();
  await copyExplicitRuntimeAssets();
  await copyMobileBootstrap();
  const manifest = await writeArtifactManifest(version);
  const verification = await verifyCapacitorWebArtifact();
  return { modules, manifest, verification };
}

const version = versionArgument(process.argv.slice(2));
const result = await buildCapacitorWebArtifact(version);
console.log(`Built ${result.verification.fileCount} mobile web files (${result.modules.length} reachable JS modules) at ${artifactRoot}`);

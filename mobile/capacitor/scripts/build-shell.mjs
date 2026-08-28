import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const workspaceRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const entryPoint = resolve(workspaceRoot, 'shell', 'bootstrap.ts');
const outfile = resolve(workspaceRoot, '.generated', 'bootstrap.js');

export async function buildMobileShell() {
  await mkdir(dirname(outfile), { recursive: true });
  await build({
    entryPoints: [entryPoint],
    outfile,
    bundle: true,
    format: 'esm',
    platform: 'browser',
    target: ['es2020'],
    sourcemap: false,
    minify: false,
    legalComments: 'none',
    charset: 'utf8',
    logLevel: 'silent',
  });
  return outfile;
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  const result = await buildMobileShell();
  console.log(`Built deterministic mobile shell at ${result}`);
}

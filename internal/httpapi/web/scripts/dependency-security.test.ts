import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repositoryDir = path.resolve(webDir, "..", "..", "..");
const mobileDir = path.join(repositoryDir, "mobile", "capacitor");

async function readJson(filePath: string): Promise<any> {
  return JSON.parse(await fs.readFile(filePath, "utf8"));
}

function lockedVersions(lock: any, packageName: string): string[] {
  const suffix = `/node_modules/${packageName}`;
  return Object.entries(lock.packages || {})
    .filter(([packagePath]) => packagePath === `node_modules/${packageName}` || packagePath.endsWith(suffix))
    .map(([, value]: [string, any]) => value.version)
    .sort();
}

function compareVersions(left: string, right: string): number {
  const parse = (value: string): number[] => {
    const match = value.match(/^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/);
    if (!match) throw new Error(`Expected a semantic version, received ${value}`);
    return match.slice(1).map(Number);
  };
  const leftParts = parse(left);
  const rightParts = parse(right);
  for (let index = 0; index < 3; index += 1) {
    if (leftParts[index] !== rightParts[index]) return leftParts[index] - rightParts[index];
  }
  return 0;
}

function inRange(version: string, minimum: string, maximumExclusive: string): boolean {
  return compareVersions(version, minimum) >= 0 && compareVersions(version, maximumExclusive) < 0;
}

function expectLockedVersionsMatch(lock: any, packageName: string, declaredVersion: string): void {
  const versions = lockedVersions(lock, packageName);
  expect(versions.length).toBeGreaterThan(0);
  expect([...new Set(versions)]).toEqual([declaredVersion]);
}

async function installedVersion(workspace: string, packageName: string): Promise<string> {
  const manifest = await readJson(path.join(workspace, "node_modules", ...packageName.split("/"), "package.json"));
  return manifest.version;
}

function isVulnerableCapacitor8(version: string): boolean {
  return inRange(version, "8.0.0", "8.4.3") || inRange(version, "8.5.0", "8.5.1");
}

describe("security dependency resolutions", () => {
  it("keeps web dependency declarations, installed packages, and lock entries aligned and patched", async () => {
    const manifest = await readJson(path.join(webDir, "package.json"));
    const lock = await readJson(path.join(webDir, "package-lock.json"));

    expectLockedVersionsMatch(lock, "mermaid", manifest.dependencies.mermaid);
    expectLockedVersionsMatch(lock, "katex", manifest.overrides.katex);
    expectLockedVersionsMatch(lock, "source-map-js", manifest.overrides["source-map-js"]);
    expect(await installedVersion(webDir, "mermaid")).toBe(manifest.dependencies.mermaid);
    expect(await installedVersion(webDir, "source-map-js")).toBe(manifest.overrides["source-map-js"]);

    for (const version of lockedVersions(lock, "katex")) {
      expect(inRange(version, "0.11.0", "0.18.2")).toBe(false);
    }
    for (const version of lockedVersions(lock, "source-map-js")) {
      expect(inRange(version, "1.0.0", "1.2.2")).toBe(false);
    }
  });

  it("records the declared and installed dependencies incorporated into the generated Mermaid bundle", async () => {
    const manifest = await readJson(path.join(webDir, "package.json"));
    const metadata = await readJson(path.join(webDir, "vendor", "mermaid.meta.json"));
    const bundle = await fs.readFile(path.join(webDir, "vendor", "mermaid.min.js"));
    const katexPackageRoot = path.dirname(path.dirname(path.join(webDir, metadata.dependencies.katex.entry)));
    const installedKatex = await readJson(path.join(katexPackageRoot, "package.json"));

    expect(metadata.dependencies.mermaid.version).toBe(manifest.dependencies.mermaid);
    expect(metadata.dependencies.mermaid.entry).toMatch(/node_modules\/mermaid\/dist\/mermaid\.core\.mjs$/);
    expect(metadata.dependencies.katex.version).toBe(manifest.overrides.katex);
    expect(metadata.dependencies.katex.entry).toMatch(/node_modules\/katex\/dist\/katex\.mjs$/);
    expect(installedKatex.version).toBe(manifest.overrides.katex);
    expect(metadata.builder.esbuild).toBe(manifest.devDependencies.esbuild);
    expect(metadata.builder.legalComments).toBe("eof");
    expect(metadata.sha256).toBe(createHash("sha256").update(bundle).digest("hex"));
  });

  it("keeps Capacitor declarations, installed packages, and lock entries aligned and outside affected ranges", async () => {
    const manifest = await readJson(path.join(mobileDir, "package.json"));
    const lock = await readJson(path.join(mobileDir, "package-lock.json"));
    const declarations: Record<string, string> = {
      "@capacitor/android": manifest.dependencies["@capacitor/android"],
      "@capacitor/core": manifest.dependencies["@capacitor/core"],
      "@capacitor/cli": manifest.devDependencies["@capacitor/cli"],
    };

    expect(declarations["@capacitor/core"]).toBe(declarations["@capacitor/android"]);
    expect(declarations["@capacitor/cli"]).toBe(declarations["@capacitor/android"]);
    for (const [packageName, declaredVersion] of Object.entries(declarations)) {
      expectLockedVersionsMatch(lock, packageName, declaredVersion);
      expect(await installedVersion(mobileDir, packageName)).toBe(declaredVersion);
      expect(isVulnerableCapacitor8(declaredVersion)).toBe(false);
    }
  });
});

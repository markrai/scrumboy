import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  buildMermaidVendorAsset,
  buildExpectedVendorAssets,
  findMismatchedVendorAssets,
  mermaidBundlePath,
} from "./vendor-assets.mjs";

const scriptPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "verify-vendor.mjs",
);

describe("verify-vendor script", () => {
  it("fails clearly when required vendor assets are missing", async () => {
    const webDir = await fs.mkdtemp(path.join(os.tmpdir(), "scrumboy-vendor-check-"));
    await fs.mkdir(path.join(webDir, "vendor"), { recursive: true });
    await fs.writeFile(path.join(webDir, "vendor", "uplot.min.js"), "present");

    const result = spawnSync(process.execPath, [scriptPath], {
      cwd: webDir,
      encoding: "utf8",
      env: {
        ...process.env,
        SCRUMBOY_WEB_DIR: webDir,
      },
    });

    expect(result.status).toBe(1);
    expect(result.stderr || result.stdout).toContain("Missing required browser vendor assets");
    expect(result.stderr || result.stdout).toContain("vendor/markdown-it.min.js");
    expect(result.stderr || result.stdout).toContain("vendor/purify.min.js");
  });

  it("detects a stale generated Mermaid bundle", async () => {
    const expectedAssets = await buildExpectedVendorAssets();
    const webDir = await fs.mkdtemp(path.join(os.tmpdir(), "scrumboy-vendor-stale-"));

    for (const asset of expectedAssets) {
      const destination = path.join(webDir, ...asset.relativePath.split("/"));
      await fs.mkdir(path.dirname(destination), { recursive: true });
      await fs.writeFile(destination, asset.contents);
    }
    await fs.appendFile(path.join(webDir, ...mermaidBundlePath.split("/")), "// stale\n");

    expect(await findMismatchedVendorAssets(expectedAssets, webDir)).toEqual([mermaidBundlePath]);
  }, 15_000);

  it("embeds versioned notices for third-party packages in the generated Mermaid bundle", async () => {
    const generated = await buildMermaidVendorAsset();
    const bundle = generated.contents.toString("utf8");
    const metadata = JSON.parse(generated.metadata);
    const noticeNames = metadata.notices.map((notice: any) => notice.name);

    expect(noticeNames).toEqual(expect.arrayContaining([
      "cytoscape",
      "dompurify",
      "katex",
      "lodash-es",
      "mermaid",
    ]));
    for (const notice of metadata.notices) {
      expect(bundle).toContain(`Package: ${notice.name}@${notice.version}`);
      expect(bundle).toContain(`License-Text-SHA256: ${notice.textSha256}`);
      expect(notice.files.length).toBeGreaterThan(0);
    }
    const domPurify = metadata.notices.find((notice: any) => notice.name === "dompurify");
    expect(domPurify.files.map((file: string) => file.toLowerCase())).toEqual(
      expect.arrayContaining(["license", "license-mpl"]),
    );
  }, 30_000);
});

import fs from "node:fs/promises";
import path from "node:path";
import {
  buildExpectedVendorAssets,
  findMismatchedVendorAssets,
  mermaidBundlePath,
  mermaidMetadataPath,
  requiredVendorFiles,
  resolveWebDir,
} from "./vendor-assets.mjs";

const webDir = resolveWebDir();

async function main() {
  const missing = [];

  for (const relativePath of requiredVendorFiles) {
    const absolutePath = path.join(webDir, relativePath);
    try {
      const stat = await fs.stat(absolutePath);
      if (!stat.isFile() || stat.size === 0) {
        missing.push(relativePath);
      }
    } catch {
      missing.push(relativePath);
    }
  }

  if (missing.length > 0) {
    console.error(
      [
        "Missing required browser vendor assets:",
        ...missing.map((entry) => `- ${entry}`),
        "",
        "Run `npm install` or `npm run sync:vendor` in internal/httpapi/web before building or testing.",
      ].join("\n"),
    );
    process.exit(1);
  }

  const expectedAssets = (await buildExpectedVendorAssets(webDir)).filter(
    (asset) => asset.relativePath === mermaidBundlePath || asset.relativePath === mermaidMetadataPath,
  );
  const mismatched = await findMismatchedVendorAssets(expectedAssets, webDir);
  if (mismatched.length > 0) {
    console.error(
      [
        "Stale or mismatched browser vendor assets:",
        ...mismatched.map((entry) => `- ${entry}`),
        "",
        "Run `npm run sync:vendor` in internal/httpapi/web and commit the regenerated assets.",
      ].join("\n"),
    );
    process.exit(1);
  }

  console.log("Verified deterministic browser vendor assets");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

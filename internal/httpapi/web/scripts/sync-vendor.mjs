import fs from "node:fs/promises";
import path from "node:path";
import { buildExpectedVendorAssets, resolveWebDir } from "./vendor-assets.mjs";

const webDir = resolveWebDir();

async function main() {
  const assets = await buildExpectedVendorAssets(webDir);
  for (const asset of assets) {
    const destination = path.join(webDir, ...asset.relativePath.split("/"));
    await fs.mkdir(path.dirname(destination), { recursive: true });
    await fs.writeFile(destination, asset.contents);
    console.log(`Synced ${asset.relativePath}`);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

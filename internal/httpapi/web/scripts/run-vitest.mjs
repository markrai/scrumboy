import { spawn } from "node:child_process";
import { realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";

const cwd = realpathSync.native(process.cwd());
const vitestCli = realpathSync.native(
  fileURLToPath(new URL("../node_modules/vitest/vitest.mjs", import.meta.url)),
);

const child = spawn(process.execPath, [vitestCli, ...process.argv.slice(2)], {
  cwd,
  stdio: "inherit",
});

child.once("error", (error) => {
  console.error("Failed to start Vitest:", error);
  process.exitCode = 1;
});

child.once("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exitCode = code ?? 1;
});

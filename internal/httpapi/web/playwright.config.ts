import os from "node:os";
import path from "node:path";
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  testMatch: ["scripts/landing-mobile-mascot.playwright.ts", "scripts/cards-per-lane.playwright.ts"],
  outputDir: path.join(os.tmpdir(), "scrumboy-web-playwright-results"),
  reporter: "line",
  timeout: 120_000,
  use: {
    browserName: "chromium",
  },
});

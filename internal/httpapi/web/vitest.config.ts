import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Preserve the Vitest 2 fake-timer set; Vitest 3 also fakes performance.now().
    fakeTimers: {
      toFake: [
        "setTimeout",
        "clearTimeout",
        "setInterval",
        "clearInterval",
        "setImmediate",
        "clearImmediate",
        "Date",
      ],
    },
  },
});

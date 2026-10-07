import { describe, expect, it } from "vitest";
import { chooseWallStoryPosition } from "./wall-story-placement.js";

describe("chooseWallStoryPosition", () => {
  it("keeps the placement visible and avoids a substantially occupied preferred slot", () => {
    const visible = { x: -100, y: 50, width: 1000, height: 700 };
    const first = chooseWallStoryPosition(visible, [], { width: 280, height: 148 });
    expect(first.x).toBeGreaterThanOrEqual(visible.x + 24);
    expect(first.y).toBeGreaterThanOrEqual(visible.y + 24);
    expect(first.x + 280).toBeLessThanOrEqual(visible.x + visible.width - 24);
    expect(first.y + 148).toBeLessThanOrEqual(visible.y + visible.height - 24);

    const next = chooseWallStoryPosition(
      visible,
      [{ x: first.x, y: first.y, width: 280, height: 148 }],
      { width: 280, height: 148 },
    );
    expect(next).not.toEqual(first);
  });

  it("is deterministic and bounded for a viewport smaller than a card", () => {
    const visible = { x: 10, y: 20, width: 100, height: 80 };
    expect(chooseWallStoryPosition(visible, [], { width: 280, height: 148 })).toEqual({ x: 34, y: 44 });
  });
});

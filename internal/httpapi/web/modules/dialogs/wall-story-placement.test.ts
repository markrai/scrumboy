import { describe, expect, it } from "vitest";
import { measureStoryCanvasRect, WALL_STORY_ESTIMATED_HEIGHT } from "./wall-story-geometry.js";
import { chooseWallStoryPosition } from "./wall-story-placement.js";

describe("chooseWallStoryPosition", () => {
  it("keeps the placement visible and avoids a substantially occupied preferred slot", () => {
    const visible = { x: -100, y: 50, width: 1000, height: 700 };
    const first = chooseWallStoryPosition(visible, [], { width: 280, height: WALL_STORY_ESTIMATED_HEIGHT });
    expect(first.x).toBeGreaterThanOrEqual(visible.x + 24);
    expect(first.y).toBeGreaterThanOrEqual(visible.y + 24);
    expect(first.x + 280).toBeLessThanOrEqual(visible.x + visible.width - 24);
    expect(first.y + WALL_STORY_ESTIMATED_HEIGHT).toBeLessThanOrEqual(visible.y + visible.height - 24);

    const next = chooseWallStoryPosition(
      visible,
      [{ x: first.x, y: first.y, width: 280, height: WALL_STORY_ESTIMATED_HEIGHT }],
      { width: 280, height: WALL_STORY_ESTIMATED_HEIGHT },
    );
    expect(next).not.toEqual(first);
  });

  it("is deterministic and bounded for a viewport smaller than a card", () => {
    const visible = { x: 10, y: 20, width: 100, height: 80 };
    expect(chooseWallStoryPosition(visible, [], { width: 280, height: WALL_STORY_ESTIMATED_HEIGHT })).toEqual({ x: 34, y: 44 });
  });

  it("uses a rendered tall story's canvas height for placement at non-1 zoom", () => {
    const visible = { x: 0, y: 0, width: 1000, height: 800 };
    const defaultPosition = chooseWallStoryPosition(visible, [], { width: 280, height: WALL_STORY_ESTIMATED_HEIGHT });
    const rendered = {
      offsetWidth: 0,
      offsetHeight: 0,
      getBoundingClientRect: () => ({ width: 140, height: 240 }),
    } as unknown as HTMLElement;
    const occupied = measureStoryCanvasRect(
      { x: defaultPosition.x, y: defaultPosition.y },
      rendered,
      0.5,
    );

    expect(occupied).toEqual({
      x: defaultPosition.x,
      y: defaultPosition.y,
      width: 280,
      height: 480,
    });
    expect(chooseWallStoryPosition(visible, [occupied], { width: 280, height: WALL_STORY_ESTIMATED_HEIGHT }))
      .not.toEqual(defaultPosition);
  });
});

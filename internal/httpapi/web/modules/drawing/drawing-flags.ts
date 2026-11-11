let storyDrawingEnabled = false;
let wallDrawingEnabled = false;

export function setDrawingFeatureFlags(flags: { story: boolean; wall: boolean }): void {
  storyDrawingEnabled = flags.story;
  wallDrawingEnabled = flags.wall;
}

export function isStoryDrawingEnabled(): boolean {
  return storyDrawingEnabled;
}

export function isWallDrawingEnabled(): boolean {
  return wallDrawingEnabled;
}

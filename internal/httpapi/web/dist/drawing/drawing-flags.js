let storyDrawingEnabled = false;
let wallDrawingEnabled = false;
export function setDrawingFeatureFlags(flags) {
    storyDrawingEnabled = flags.story;
    wallDrawingEnabled = flags.wall;
}
export function isStoryDrawingEnabled() {
    return storyDrawingEnabled;
}
export function isWallDrawingEnabled() {
    return wallDrawingEnabled;
}

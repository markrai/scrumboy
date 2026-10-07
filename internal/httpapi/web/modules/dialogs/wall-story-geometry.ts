export const WALL_STORY_WIDTH = 280;

// A new story has no DOM box until the pin completes and the Wall refetches.
// This estimate is used only for that candidate; rendered stories are measured.
export const WALL_STORY_ESTIMATED_HEIGHT = 148;

export type WallStoryCanvasRect = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export function measureStoryCanvasSize(
  element: HTMLElement | null,
  zoom: number,
  fallback: { width: number; height: number } = {
    width: WALL_STORY_WIDTH,
    height: WALL_STORY_ESTIMATED_HEIGHT,
  },
): { width: number; height: number } {
  if (!element) return fallback;

  // offsetWidth/offsetHeight are layout dimensions before the Wall content's
  // scale transform, so they are already canvas units. The bounding-rect path
  // covers DOM implementations where layout offsets are unavailable and must
  // be converted back from screen pixels at the current zoom.
  const rect = element.getBoundingClientRect();
  const safeZoom = Number.isFinite(zoom) && zoom > 0 ? zoom : 1;
  const width = element.offsetWidth > 0
    ? element.offsetWidth
    : rect.width > 0 ? rect.width / safeZoom : fallback.width;
  const height = element.offsetHeight > 0
    ? element.offsetHeight
    : rect.height > 0 ? rect.height / safeZoom : fallback.height;
  return {
    width: Math.max(1, width),
    height: Math.max(1, height),
  };
}

export function measureStoryCanvasRect(
  story: { x: number; y: number },
  element: HTMLElement | null,
  zoom: number,
): WallStoryCanvasRect {
  const size = measureStoryCanvasSize(element, zoom);
  return { x: story.x, y: story.y, width: size.width, height: size.height };
}

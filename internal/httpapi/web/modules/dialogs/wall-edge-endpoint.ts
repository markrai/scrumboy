import { measureStoryCanvasSize } from "./wall-story-geometry.js";

export type WallEdgeEndpointKind = "note" | "story";

export interface WallEdgeEndpoint {
  readonly kind: WallEdgeEndpointKind;
  readonly noteId: string | null;
  readonly storyLocalId: number | null;
  readonly canonical: string;
}

const STORY_ENDPOINT_PREFIX = "story:";

export function formatWallStoryEndpoint(storyLocalId: number): string {
  return `${STORY_ENDPOINT_PREFIX}${storyLocalId}`;
}

export function parseWallEdgeEndpoint(raw: string): WallEdgeEndpoint | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  if (trimmed.startsWith(STORY_ENDPOINT_PREFIX)) {
    const rest = trimmed.slice(STORY_ENDPOINT_PREFIX.length);
    if (!/^\d+$/.test(rest)) return null;
    const storyLocalId = Number(rest);
    if (!Number.isSafeInteger(storyLocalId) || storyLocalId <= 0) return null;
    return {
      kind: "story",
      noteId: null,
      storyLocalId,
      canonical: formatWallStoryEndpoint(storyLocalId),
    };
  }
  return { kind: "note", noteId: trimmed, storyLocalId: null, canonical: trimmed };
}

export function canonicalEndpointForElement(target: HTMLElement | null): string | null {
  const storyEl = target?.closest<HTMLElement>(".wall-story");
  if (storyEl) {
    const localId = Number(storyEl.dataset.storyLocalId);
    if (!Number.isSafeInteger(localId) || localId <= 0) return null;
    return formatWallStoryEndpoint(localId);
  }
  const noteEl = target?.closest<HTMLElement>(".wall-note");
  const noteId = noteEl?.dataset.noteId || "";
  if (noteEl && noteId) return noteId;
  return null;
}

export function resolveWallEdgeElement(
  surface: HTMLElement,
  endpoint: WallEdgeEndpoint,
): HTMLElement | null {
  if (endpoint.kind === "story") {
    return surface.querySelector<HTMLElement>(
      `.wall-story[data-story-local-id="${endpoint.storyLocalId}"]`,
    );
  }
  return surface.querySelector<HTMLElement>(
    `.wall-note[data-note-id="${CSS.escape(endpoint.noteId ?? "")}"]`,
  );
}

export interface StoryEdgeCenter {
  readonly endpoint: string;
  readonly cx: number;
  readonly cy: number;
}

export function storyEdgeCenter(
  surface: HTMLElement,
  storyLocalId: number,
  zoom: number,
): StoryEdgeCenter | null {
  const endpoint = parseWallEdgeEndpoint(formatWallStoryEndpoint(storyLocalId));
  if (!endpoint) return null;
  const center = wallEdgeEndpointCenter(surface, endpoint, zoom);
  if (!center) return null;
  return { endpoint: endpoint.canonical, cx: center.cx, cy: center.cy };
}

export function wallEdgeEndpointCenter(
  surface: HTMLElement,
  endpoint: WallEdgeEndpoint,
  zoom: number,
): { cx: number; cy: number } | null {
  const el = resolveWallEdgeElement(surface, endpoint);
  if (!el) return null;
  if (endpoint.kind === "story") {
    const size = measureStoryCanvasSize(el, zoom);
    return { cx: el.offsetLeft + size.width / 2, cy: el.offsetTop + size.height / 2 };
  }
  return { cx: el.offsetLeft + el.offsetWidth / 2, cy: el.offsetTop + el.offsetHeight / 2 };
}

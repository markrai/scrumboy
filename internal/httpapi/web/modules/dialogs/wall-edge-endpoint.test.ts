// @vitest-environment happy-dom
import { describe, expect, it } from "vitest";

import {
  formatWallStoryEndpoint,
  parseWallEdgeEndpoint,
  resolveWallEdgeElement,
  wallEdgeEndpointCenter,
} from "./wall-edge-endpoint.js";

describe("parseWallEdgeEndpoint", () => {
  it("passes legacy note endpoints through verbatim", () => {
    const ep = parseWallEdgeEndpoint("n_abc123");
    expect(ep).toEqual({ kind: "note", noteId: "n_abc123", storyLocalId: null, canonical: "n_abc123" });
  });

  it("trims surrounding whitespace", () => {
    expect(parseWallEdgeEndpoint("  story:12  ")?.canonical).toBe("story:12");
    expect(parseWallEdgeEndpoint("  n_abc  ")?.canonical).toBe("n_abc");
  });

  it("parses canonical story endpoints", () => {
    const ep = parseWallEdgeEndpoint("story:123");
    expect(ep).toEqual({ kind: "story", noteId: null, storyLocalId: 123, canonical: "story:123" });
  });

  it("normalizes non-canonical story input to canonical form", () => {
    expect(parseWallEdgeEndpoint("story:007")?.canonical).toBe("story:7");
    expect(parseWallEdgeEndpoint("story:007")?.storyLocalId).toBe(7);
  });

  it("treats the story prefix case-sensitively", () => {
    expect(parseWallEdgeEndpoint("Story:12")?.kind).toBe("note");
  });

  it.each(["", "   ", "story:", "story:abc", "story:-1", "story:0", "story:1.5", "story:12:34", "story: 12", "story:99999999999999999999999"])(
    "rejects malformed endpoint %q",
    (raw) => {
      expect(parseWallEdgeEndpoint(raw)).toBeNull();
    },
  );
});

describe("formatWallStoryEndpoint", () => {
  it("renders canonical story endpoints", () => {
    expect(formatWallStoryEndpoint(123)).toBe("story:123");
  });
});

function mountSurface(): HTMLElement {
  const surface = document.createElement("div");
  document.body.appendChild(surface);
  const note = document.createElement("div");
  note.className = "wall-note";
  note.dataset.noteId = "na";
  surface.appendChild(note);
  const story = document.createElement("div");
  story.className = "wall-story";
  story.dataset.storyLocalId = "7";
  surface.appendChild(story);
  return surface;
}

function stubBox(el: HTMLElement, l: number, t: number, w: number, h: number): void {
  Object.defineProperty(el, "offsetLeft", { configurable: true, value: l });
  Object.defineProperty(el, "offsetTop", { configurable: true, value: t });
  Object.defineProperty(el, "offsetWidth", { configurable: true, value: w });
  Object.defineProperty(el, "offsetHeight", { configurable: true, value: h });
}

describe("resolveWallEdgeElement", () => {
  it("resolves note endpoints to note elements", () => {
    const surface = mountSurface();
    const ep = parseWallEdgeEndpoint("na");
    expect(resolveWallEdgeElement(surface, ep!)?.dataset.noteId).toBe("na");
  });

  it("resolves story endpoints to story elements", () => {
    const surface = mountSurface();
    const ep = parseWallEdgeEndpoint("story:7");
    expect(resolveWallEdgeElement(surface, ep!)?.dataset.storyLocalId).toBe("7");
  });

  it("returns null when the element is absent", () => {
    const surface = mountSurface();
    expect(resolveWallEdgeElement(surface, parseWallEdgeEndpoint("missing")!)).toBeNull();
    expect(resolveWallEdgeElement(surface, parseWallEdgeEndpoint("story:99")!)).toBeNull();
  });
});

describe("wallEdgeEndpointCenter", () => {
  it("centers notes on their rendered box", () => {
    const surface = mountSurface();
    stubBox(surface.querySelector('.wall-note[data-note-id="na"]') as HTMLElement, 100, 100, 200, 100);
    expect(wallEdgeEndpointCenter(surface, parseWallEdgeEndpoint("na")!, 1)).toEqual({ cx: 200, cy: 150 });
  });

  it("centers stories on their rendered dimensions", () => {
    const surface = mountSurface();
    stubBox(surface.querySelector('.wall-story[data-story-local-id="7"]') as HTMLElement, 400, 300, 280, 200);
    expect(wallEdgeEndpointCenter(surface, parseWallEdgeEndpoint("story:7")!, 1)).toEqual({ cx: 540, cy: 400 });
  });

  it("returns null when the element is absent", () => {
    const surface = mountSurface();
    expect(wallEdgeEndpointCenter(surface, parseWallEdgeEndpoint("story:99")!, 1)).toBeNull();
  });
});

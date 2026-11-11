import { createDrawingSvg, renderStrokesInto, SVG_NS } from "./drawing-svg.js";
import type { DrawingDocument } from "./drawing-types.js";

export function sizeOverlayToImage(img: HTMLImageElement, svg: SVGSVGElement): boolean {
  if (!img.complete || !(img.naturalWidth > 0) || !(img.naturalHeight > 0)) {
    svg.style.visibility = "hidden";
    return false;
  }
  const width = img.clientWidth;
  const height = img.clientHeight;
  if (!(width > 0) || !(height > 0)) {
    svg.style.visibility = "hidden";
    return false;
  }
  svg.style.left = `${img.offsetLeft + img.clientLeft}px`;
  svg.style.top = `${img.offsetTop + img.clientTop}px`;
  svg.style.width = `${width}px`;
  svg.style.height = `${height}px`;
  svg.style.visibility = "visible";
  return true;
}

export function renderDocumentIntoSvg(svg: SVGSVGElement, doc: DrawingDocument | undefined, fallback?: { width: number; height: number }): void {
  const width = doc?.width ?? fallback?.width;
  const height = doc?.height ?? fallback?.height;
  if (width && height) svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  let group = svg.querySelector<SVGGElement>("g.drawing-strokes");
  if (!group) {
    group = document.createElementNS(SVG_NS, "g") as SVGGElement;
    group.setAttribute("class", "drawing-strokes");
    svg.appendChild(group);
  }
  renderStrokesInto(group, doc?.strokes ?? []);
}

export function attachImageOverlay(wrapper: HTMLElement, img: HTMLImageElement, doc: DrawingDocument | undefined): SVGSVGElement {
  let svg = wrapper.querySelector<SVGSVGElement>("svg.todo-notes-image__annotation-layer");
  if (!svg) {
    svg = createDrawingSvg("todo-notes-image__annotation-layer", doc?.width ?? 1, doc?.height ?? 1);
    svg.style.position = "absolute";
    svg.style.pointerEvents = "none";
    svg.style.visibility = "hidden";
    wrapper.appendChild(svg);
  }
  const target = svg;
  renderDocumentIntoSvg(target, doc, { width: img.naturalWidth || 1, height: img.naturalHeight || 1 });
  const resize = () => sizeOverlayToImage(img, target);
  if (!resize()) img.addEventListener("load", resize, { once: true });
  if (typeof ResizeObserver !== "undefined" && !(target as unknown as { __observed?: boolean }).__observed) {
    const ro = new ResizeObserver(resize);
    ro.observe(img);
    (target as unknown as { __observed?: boolean; __ro?: ResizeObserver }).__observed = true;
    (target as unknown as { __ro?: ResizeObserver }).__ro = ro;
  }
  return target;
}

export function detachImageOverlay(wrapper: HTMLElement): void {
  const svg = wrapper.querySelector<SVGSVGElement>("svg.todo-notes-image__annotation-layer");
  if (!svg) return;
  (svg as unknown as { __ro?: ResizeObserver }).__ro?.disconnect();
  svg.remove();
}

export function createStandaloneSvg(doc: DrawingDocument | undefined, fallbackWidth: number, fallbackHeight: number): SVGSVGElement {
  const width = doc?.width ?? fallbackWidth;
  const height = doc?.height ?? fallbackHeight;
  const svg = createDrawingSvg("todo-notes-drawing__svg", width, height);
  svg.setAttribute("preserveAspectRatio", "xMidYMid meet");
  svg.style.aspectRatio = `${width} / ${height}`;
  renderDocumentIntoSvg(svg, doc, { width, height });
  return svg;
}

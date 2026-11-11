import { createDrawingSvg, renderStrokesInto, SVG_NS } from "./drawing-svg.js";
export function sizeOverlayToImage(img, svg) {
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
export function renderDocumentIntoSvg(svg, doc, fallback) {
    const width = doc?.width ?? fallback?.width;
    const height = doc?.height ?? fallback?.height;
    if (width && height)
        svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
    let group = svg.querySelector("g.drawing-strokes");
    if (!group) {
        group = document.createElementNS(SVG_NS, "g");
        group.setAttribute("class", "drawing-strokes");
        svg.appendChild(group);
    }
    renderStrokesInto(group, doc?.strokes ?? []);
}
export function attachImageOverlay(wrapper, img, doc) {
    let svg = wrapper.querySelector("svg.todo-notes-image__annotation-layer");
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
    if (!resize())
        img.addEventListener("load", resize, { once: true });
    if (typeof ResizeObserver !== "undefined" && !target.__observed) {
        const ro = new ResizeObserver(resize);
        ro.observe(img);
        target.__observed = true;
        target.__ro = ro;
    }
    return target;
}
export function detachImageOverlay(wrapper) {
    const svg = wrapper.querySelector("svg.todo-notes-image__annotation-layer");
    if (!svg)
        return;
    svg.__ro?.disconnect();
    svg.remove();
}
export function createStandaloneSvg(doc, fallbackWidth, fallbackHeight) {
    const width = doc?.width ?? fallbackWidth;
    const height = doc?.height ?? fallbackHeight;
    const svg = createDrawingSvg("todo-notes-drawing__svg", width, height);
    svg.setAttribute("preserveAspectRatio", "xMidYMid meet");
    svg.style.aspectRatio = `${width} / ${height}`;
    renderDocumentIntoSvg(svg, doc, { width, height });
    return svg;
}

import { hasPressureVariation, pointsToSvgPath, strokeOutlinePath } from "./drawing-spline.js";
import { STROKE_COLOR_RE } from "./drawing-types.js";
export const SVG_NS = "http://www.w3.org/2000/svg";
export function createStrokeElement(stroke, opts = {}) {
    if (!STROKE_COLOR_RE.test(stroke.color) || stroke.points.length === 0)
        return null;
    const path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("class", `drawing-stroke drawing-stroke--${stroke.tool}`);
    if (stroke.id)
        path.setAttribute("data-stroke-id", stroke.id);
    const variable = opts.variableWidth !== false && hasPressureVariation(stroke);
    if (variable) {
        path.setAttribute("d", strokeOutlinePath(stroke.points, stroke.size));
        path.setAttribute("fill", stroke.color);
        path.setAttribute("stroke", "none");
    }
    else {
        path.setAttribute("d", pointsToSvgPath(stroke.points));
        path.setAttribute("fill", "none");
        path.setAttribute("stroke", stroke.color);
        path.setAttribute("stroke-width", String(stroke.size));
        path.setAttribute("stroke-linecap", "round");
        path.setAttribute("stroke-linejoin", "round");
    }
    path.setAttribute("opacity", String(Math.min(1, Math.max(0, stroke.opacity))));
    return path;
}
export function renderStrokesInto(group, strokes) {
    const frag = document.createDocumentFragment();
    for (const s of strokes) {
        const el = createStrokeElement(s);
        if (el)
            frag.appendChild(el);
    }
    group.replaceChildren(frag);
}
export function updatePreviewPath(group, style, points) {
    let path = group.querySelector("path.drawing-stroke--preview");
    if (points.length === 0) {
        path?.remove();
        return;
    }
    if (!path) {
        path = document.createElementNS(SVG_NS, "path");
        path.setAttribute("class", "drawing-stroke drawing-stroke--preview");
        path.setAttribute("fill", "none");
        path.setAttribute("stroke-linecap", "round");
        path.setAttribute("stroke-linejoin", "round");
        group.appendChild(path);
    }
    if (!STROKE_COLOR_RE.test(style.color))
        return;
    path.setAttribute("d", pointsToSvgPath(points));
    path.setAttribute("stroke", style.color);
    path.setAttribute("stroke-width", String(style.size));
    path.setAttribute("opacity", String(style.opacity));
}
export function createDrawingSvg(className, width, height) {
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", className);
    svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    return svg;
}
export function createPencilIcon(size = 20) {
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("width", String(size));
    svg.setAttribute("height", String(size));
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("fill", "none");
    svg.setAttribute("stroke", "currentColor");
    svg.setAttribute("stroke-width", "2");
    svg.setAttribute("stroke-linecap", "round");
    svg.setAttribute("stroke-linejoin", "round");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    svg.setAttribute("class", "lucide lucide-pencil");
    for (const d of ["M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z", "m15 5 4 4"]) {
        const path = document.createElementNS(SVG_NS, "path");
        path.setAttribute("d", d);
        svg.appendChild(path);
    }
    return svg;
}

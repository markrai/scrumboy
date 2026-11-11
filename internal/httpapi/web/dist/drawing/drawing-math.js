function requireFinite(value, name) {
    if (!Number.isFinite(value)) {
        throw new RangeError(`${name} must be a finite number`);
    }
}
function requirePositiveFinite(value, name) {
    if (!Number.isFinite(value) || value <= 0) {
        throw new RangeError(`${name} must be a finite number greater than zero`);
    }
}
function requireValidImageTransform(transform) {
    requireFinite(transform.rectLeft, "rectLeft");
    requireFinite(transform.rectTop, "rectTop");
    requirePositiveFinite(transform.rectWidth, "rectWidth");
    requirePositiveFinite(transform.rectHeight, "rectHeight");
    requirePositiveFinite(transform.naturalWidth, "naturalWidth");
    requirePositiveFinite(transform.naturalHeight, "naturalHeight");
}
function requireValidWallTransform(transform) {
    requireFinite(transform.viewportLeft, "viewportLeft");
    requireFinite(transform.viewportTop, "viewportTop");
    requireFinite(transform.panX, "panX");
    requireFinite(transform.panY, "panY");
    requirePositiveFinite(transform.zoom, "zoom");
}
function clampToRange(value, max) {
    if (value < 0) {
        return 0;
    }
    if (value > max) {
        return max;
    }
    return value;
}
export function imageScaleFactors(transform) {
    requireValidImageTransform(transform);
    return {
        sx: transform.rectWidth / transform.naturalWidth,
        sy: transform.rectHeight / transform.naturalHeight,
    };
}
export function imageClientToIntrinsic(transform, clientX, clientY) {
    requireValidImageTransform(transform);
    requireFinite(clientX, "clientX");
    requireFinite(clientY, "clientY");
    const rawX = ((clientX - transform.rectLeft) * transform.naturalWidth) / transform.rectWidth;
    const rawY = ((clientY - transform.rectTop) * transform.naturalHeight) / transform.rectHeight;
    return {
        x: clampToRange(rawX, transform.naturalWidth),
        y: clampToRange(rawY, transform.naturalHeight),
    };
}
export function imageIntrinsicToClient(transform, x, y) {
    requireValidImageTransform(transform);
    requireFinite(x, "x");
    requireFinite(y, "y");
    return {
        x: transform.rectLeft + (x * transform.rectWidth) / transform.naturalWidth,
        y: transform.rectTop + (y * transform.rectHeight) / transform.naturalHeight,
    };
}
export function wallClientToWorld(transform, clientX, clientY) {
    requireValidWallTransform(transform);
    requireFinite(clientX, "clientX");
    requireFinite(clientY, "clientY");
    return {
        x: (clientX - transform.viewportLeft - transform.panX) / transform.zoom,
        y: (clientY - transform.viewportTop - transform.panY) / transform.zoom,
    };
}
export function wallWorldToClient(transform, x, y) {
    requireValidWallTransform(transform);
    requireFinite(x, "x");
    requireFinite(y, "y");
    return {
        x: transform.viewportLeft + transform.panX + x * transform.zoom,
        y: transform.viewportTop + transform.panY + y * transform.zoom,
    };
}
export function imageBrushLogicalWidth(cssDiameter, sx, sy) {
    requirePositiveFinite(cssDiameter, "cssDiameter");
    requirePositiveFinite(sx, "sx");
    requirePositiveFinite(sy, "sy");
    const scale = sx < sy ? sx : sy;
    return cssDiameter / scale;
}
export function wallBrushLogicalWidth(cssDiameter, zoom) {
    requirePositiveFinite(cssDiameter, "cssDiameter");
    requirePositiveFinite(zoom, "zoom");
    return cssDiameter / zoom;
}
export const MAX_WORLD_ABS = 100000;
function clampWorld(value) {
    if (value < -MAX_WORLD_ABS)
        return -MAX_WORLD_ABS;
    if (value > MAX_WORLD_ABS)
        return MAX_WORLD_ABS;
    return value;
}
export function freezeImageCapture(transform) {
    requireValidImageTransform(transform);
    const t = { ...transform };
    const { sx, sy } = imageScaleFactors(t);
    return {
        originX: t.rectLeft,
        originY: t.rectTop,
        toLogical(px, py) {
            return imageClientToIntrinsic(t, px + t.rectLeft, py + t.rectTop);
        },
        brushToLogical(cssDiameter) {
            return imageBrushLogicalWidth(cssDiameter, sx, sy);
        },
        eraserRadii(cssRadius) {
            requirePositiveFinite(cssRadius, "cssRadius");
            return { rx: cssRadius / sx, ry: cssRadius / sy };
        },
    };
}
export function freezeWallCapture(transform) {
    requireValidWallTransform(transform);
    const t = { ...transform };
    return {
        originX: t.viewportLeft,
        originY: t.viewportTop,
        toLogical(px, py) {
            const world = wallClientToWorld(t, px + t.viewportLeft, py + t.viewportTop);
            return { x: clampWorld(world.x), y: clampWorld(world.y) };
        },
        brushToLogical(cssDiameter) {
            return wallBrushLogicalWidth(cssDiameter, t.zoom);
        },
        eraserRadii(cssRadius) {
            requirePositiveFinite(cssRadius, "cssRadius");
            return { rx: cssRadius / t.zoom, ry: cssRadius / t.zoom };
        },
    };
}
export function roundCoord(value) {
    return Math.round(value * 10) / 10;
}
export function roundPressure(value) {
    return Math.round(value * 100) / 100;
}
export function roundSize(value) {
    return Math.max(0.01, Math.round(value * 100) / 100);
}

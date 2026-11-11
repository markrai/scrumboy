export type ImagePoint = {
  x: number;
  y: number;
};

export type ImageScaleFactors = {
  sx: number;
  sy: number;
};

export type ImageCaptureTransform = {
  rectLeft: number;
  rectTop: number;
  rectWidth: number;
  rectHeight: number;
  naturalWidth: number;
  naturalHeight: number;
};

export type WallCaptureTransform = {
  viewportLeft: number;
  viewportTop: number;
  panX: number;
  panY: number;
  zoom: number;
};

function requireFinite(value: number, name: string): void {
  if (!Number.isFinite(value)) {
    throw new RangeError(`${name} must be a finite number`);
  }
}

function requirePositiveFinite(value: number, name: string): void {
  if (!Number.isFinite(value) || value <= 0) {
    throw new RangeError(`${name} must be a finite number greater than zero`);
  }
}

function requireValidImageTransform(transform: ImageCaptureTransform): void {
  requireFinite(transform.rectLeft, "rectLeft");
  requireFinite(transform.rectTop, "rectTop");
  requirePositiveFinite(transform.rectWidth, "rectWidth");
  requirePositiveFinite(transform.rectHeight, "rectHeight");
  requirePositiveFinite(transform.naturalWidth, "naturalWidth");
  requirePositiveFinite(transform.naturalHeight, "naturalHeight");
}

function requireValidWallTransform(transform: WallCaptureTransform): void {
  requireFinite(transform.viewportLeft, "viewportLeft");
  requireFinite(transform.viewportTop, "viewportTop");
  requireFinite(transform.panX, "panX");
  requireFinite(transform.panY, "panY");
  requirePositiveFinite(transform.zoom, "zoom");
}

function clampToRange(value: number, max: number): number {
  if (value < 0) {
    return 0;
  }
  if (value > max) {
    return max;
  }
  return value;
}

export function imageScaleFactors(transform: ImageCaptureTransform): ImageScaleFactors {
  requireValidImageTransform(transform);
  return {
    sx: transform.rectWidth / transform.naturalWidth,
    sy: transform.rectHeight / transform.naturalHeight,
  };
}

export function imageClientToIntrinsic(
  transform: ImageCaptureTransform,
  clientX: number,
  clientY: number,
): ImagePoint {
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

export function imageIntrinsicToClient(
  transform: ImageCaptureTransform,
  x: number,
  y: number,
): ImagePoint {
  requireValidImageTransform(transform);
  requireFinite(x, "x");
  requireFinite(y, "y");
  return {
    x: transform.rectLeft + (x * transform.rectWidth) / transform.naturalWidth,
    y: transform.rectTop + (y * transform.rectHeight) / transform.naturalHeight,
  };
}

export function wallClientToWorld(
  transform: WallCaptureTransform,
  clientX: number,
  clientY: number,
): ImagePoint {
  requireValidWallTransform(transform);
  requireFinite(clientX, "clientX");
  requireFinite(clientY, "clientY");
  return {
    x: (clientX - transform.viewportLeft - transform.panX) / transform.zoom,
    y: (clientY - transform.viewportTop - transform.panY) / transform.zoom,
  };
}

export function wallWorldToClient(
  transform: WallCaptureTransform,
  x: number,
  y: number,
): ImagePoint {
  requireValidWallTransform(transform);
  requireFinite(x, "x");
  requireFinite(y, "y");
  return {
    x: transform.viewportLeft + transform.panX + x * transform.zoom,
    y: transform.viewportTop + transform.panY + y * transform.zoom,
  };
}

export function imageBrushLogicalWidth(
  cssDiameter: number,
  sx: number,
  sy: number,
): number {
  requirePositiveFinite(cssDiameter, "cssDiameter");
  requirePositiveFinite(sx, "sx");
  requirePositiveFinite(sy, "sy");
  const scale = sx < sy ? sx : sy;
  return cssDiameter / scale;
}

export function wallBrushLogicalWidth(cssDiameter: number, zoom: number): number {
  requirePositiveFinite(cssDiameter, "cssDiameter");
  requirePositiveFinite(zoom, "zoom");
  return cssDiameter / zoom;
}

export type FrozenCapture = {
  readonly originX: number;
  readonly originY: number;
  toLogical(sx: number, sy: number): ImagePoint;
  brushToLogical(cssDiameter: number): number;
  eraserRadii(cssRadius: number): { rx: number; ry: number };
};

export const MAX_WORLD_ABS = 100000;

function clampWorld(value: number): number {
  if (value < -MAX_WORLD_ABS) return -MAX_WORLD_ABS;
  if (value > MAX_WORLD_ABS) return MAX_WORLD_ABS;
  return value;
}

export function freezeImageCapture(transform: ImageCaptureTransform): FrozenCapture {
  requireValidImageTransform(transform);
  const t = { ...transform };
  const { sx, sy } = imageScaleFactors(t);
  return {
    originX: t.rectLeft,
    originY: t.rectTop,
    toLogical(px: number, py: number): ImagePoint {
      return imageClientToIntrinsic(t, px + t.rectLeft, py + t.rectTop);
    },
    brushToLogical(cssDiameter: number): number {
      return imageBrushLogicalWidth(cssDiameter, sx, sy);
    },
    eraserRadii(cssRadius: number): { rx: number; ry: number } {
      requirePositiveFinite(cssRadius, "cssRadius");
      return { rx: cssRadius / sx, ry: cssRadius / sy };
    },
  };
}

export function freezeWallCapture(transform: WallCaptureTransform): FrozenCapture {
  requireValidWallTransform(transform);
  const t = { ...transform };
  return {
    originX: t.viewportLeft,
    originY: t.viewportTop,
    toLogical(px: number, py: number): ImagePoint {
      const world = wallClientToWorld(t, px + t.viewportLeft, py + t.viewportTop);
      return { x: clampWorld(world.x), y: clampWorld(world.y) };
    },
    brushToLogical(cssDiameter: number): number {
      return wallBrushLogicalWidth(cssDiameter, t.zoom);
    },
    eraserRadii(cssRadius: number): { rx: number; ry: number } {
      requirePositiveFinite(cssRadius, "cssRadius");
      return { rx: cssRadius / t.zoom, ry: cssRadius / t.zoom };
    },
  };
}

export function roundCoord(value: number): number {
  return Math.round(value * 10) / 10;
}

export function roundPressure(value: number): number {
  return Math.round(value * 100) / 100;
}

export function roundSize(value: number): number {
  return Math.max(0.01, Math.round(value * 100) / 100);
}

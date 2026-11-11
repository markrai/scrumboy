import { describe, expect, it } from "vitest";
import {
  imageBrushLogicalWidth,
  imageClientToIntrinsic,
  imageIntrinsicToClient,
  imageScaleFactors,
  wallBrushLogicalWidth,
  wallClientToWorld,
  wallWorldToClient,
  type ImageCaptureTransform,
  type WallCaptureTransform,
} from "./drawing-math.js";

const IMAGE_4000x3000_AT_400x300: ImageCaptureTransform = {
  rectLeft: 100,
  rectTop: 50,
  rectWidth: 400,
  rectHeight: 300,
  naturalWidth: 4000,
  naturalHeight: 3000,
};

const IMAGE_4000x3000_AT_800x600: ImageCaptureTransform = {
  rectLeft: 100,
  rectTop: 50,
  rectWidth: 800,
  rectHeight: 600,
  naturalWidth: 4000,
  naturalHeight: 3000,
};

const IMAGE_4000x3000_NONUNIFORM: ImageCaptureTransform = {
  rectLeft: 0,
  rectTop: 0,
  rectWidth: 400,
  rectHeight: 600,
  naturalWidth: 4000,
  naturalHeight: 3000,
};

function wallAtZoom(zoom: number): WallCaptureTransform {
  return { viewportLeft: 100, viewportTop: 50, panX: -200, panY: 300, zoom };
}

describe("image scale factors", () => {
  it("computes independent X/Y factors for 4000x3000 at 400x300", () => {
    expect(imageScaleFactors(IMAGE_4000x3000_AT_400x300)).toEqual({ sx: 0.1, sy: 0.1 });
  });

  it("computes independent X/Y factors for 4000x3000 at 800x600", () => {
    expect(imageScaleFactors(IMAGE_4000x3000_AT_800x600)).toEqual({ sx: 0.2, sy: 0.2 });
  });

  it("keeps nonuniform X/Y factors independent", () => {
    expect(imageScaleFactors(IMAGE_4000x3000_NONUNIFORM)).toEqual({ sx: 0.1, sy: 0.2 });
  });
});

describe("image client to intrinsic mapping", () => {
  it("maps the rect center to the intrinsic center at 400x300", () => {
    expect(imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 300, 200)).toEqual({
      x: 2000,
      y: 1500,
    });
  });

  it("maps the rect center to the intrinsic center at 800x600", () => {
    expect(imageClientToIntrinsic(IMAGE_4000x3000_AT_800x600, 500, 350)).toEqual({
      x: 2000,
      y: 1500,
    });
  });

  it("maps per-axis under nonuniform scaling", () => {
    expect(imageClientToIntrinsic(IMAGE_4000x3000_NONUNIFORM, 200, 300)).toEqual({
      x: 2000,
      y: 1500,
    });
  });

  it("clamps coordinates outside the image bounds", () => {
    expect(imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 0, 0)).toEqual({
      x: 0,
      y: 0,
    });
    expect(imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 9000, 9000)).toEqual({
      x: 4000,
      y: 3000,
    });
    expect(imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 300, -40)).toEqual({
      x: 2000,
      y: 0,
    });
    expect(imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, -40, 200)).toEqual({
      x: 0,
      y: 1500,
    });
  });
});

describe("image coordinate round trips", () => {
  const cases = [
    [IMAGE_4000x3000_AT_400x300, 101, 51],
    [IMAGE_4000x3000_AT_400x300, 499, 349],
    [IMAGE_4000x3000_AT_800x600, 101, 51],
    [IMAGE_4000x3000_AT_800x600, 899, 649],
    [IMAGE_4000x3000_NONUNIFORM, 1, 599],
    [IMAGE_4000x3000_NONUNIFORM, 399, 1],
  ] as const;

  it.each(cases)("round-trips client point through intrinsic space (%i, %i)", (t, cx, cy) => {
    const intrinsic = imageClientToIntrinsic(t, cx, cy);
    const back = imageIntrinsicToClient(t, intrinsic.x, intrinsic.y);
    expect(back.x).toBeCloseTo(cx, 9);
    expect(back.y).toBeCloseTo(cy, 9);
  });

  it("round-trips an intrinsic point through client space", () => {
    const back = imageClientToIntrinsic(
      IMAGE_4000x3000_AT_400x300,
      ...(() => {
        const c = imageIntrinsicToClient(IMAGE_4000x3000_AT_400x300, 1234.5, 678.9);
        return [c.x, c.y] as const;
      })(),
    );
    expect(back.x).toBeCloseTo(1234.5, 9);
    expect(back.y).toBeCloseTo(678.9, 9);
  });
});

describe("wall coordinate transforms", () => {
  it.each([0.02, 0.2, 1, 3])("maps client to world at zoom %s", (zoom) => {
    const t = wallAtZoom(zoom);
    expect(wallClientToWorld(t, 100, 50)).toEqual({ x: 200 / zoom, y: -300 / zoom });
    const world = wallClientToWorld(t, 100 - 200 + 10 * zoom, 50 + 300 + 20 * zoom);
    expect(world.x).toBeCloseTo(10, 9);
    expect(world.y).toBeCloseTo(20, 9);
  });

  it.each([0.02, 0.2, 1, 3])("round-trips world points at zoom %s", (zoom) => {
    const t = wallAtZoom(zoom);
    for (const [wx, wy] of [
      [0, 0],
      [-50000, -25000],
      [100000, 100000],
      [-100000, 100000],
      [12.5, -7.25],
    ] as const) {
      const client = wallWorldToClient(t, wx, wy);
      const back = wallClientToWorld(t, client.x, client.y);
      expect(back.x).toBeCloseTo(wx, 9);
      expect(back.y).toBeCloseTo(wy, 9);
    }
  });

  it("round-trips client points including negative world coordinates", () => {
    const t = wallAtZoom(1);
    const world = wallClientToWorld(t, -400, -600);
    expect(world.x).toBeLessThan(0);
    expect(world.y).toBeLessThan(0);
    const back = wallWorldToClient(t, world.x, world.y);
    expect(back.x).toBeCloseTo(-400, 9);
    expect(back.y).toBeCloseTo(-600, 9);
  });
});

describe("brush-size conversion", () => {
  it("converts a 4 CSS-pixel brush on a 4000px image at 400px width", () => {
    const { sx, sy } = imageScaleFactors(IMAGE_4000x3000_AT_400x300);
    expect(imageBrushLogicalWidth(4, sx, sy)).toBe(40);
  });

  it("converts a 4 CSS-pixel brush on a 4000px image at 800px width", () => {
    const { sx, sy } = imageScaleFactors(IMAGE_4000x3000_AT_800x600);
    expect(imageBrushLogicalWidth(4, sx, sy)).toBe(20);
  });

  it("uses the smaller scale under nonuniform image scaling", () => {
    const { sx, sy } = imageScaleFactors(IMAGE_4000x3000_NONUNIFORM);
    expect(imageBrushLogicalWidth(4, sx, sy)).toBe(40);
  });

  it("converts a 4 CSS-pixel brush on the wall across zoom levels", () => {
    expect(wallBrushLogicalWidth(4, 0.02)).toBe(200);
    expect(wallBrushLogicalWidth(4, 0.2)).toBe(20);
    expect(wallBrushLogicalWidth(4, 1)).toBe(4);
    expect(wallBrushLogicalWidth(4, 3)).toBeCloseTo(4 / 3, 12);
  });
});

describe("invalid inputs", () => {
  it("rejects zero, negative, NaN, and infinite rect dimensions", () => {
    for (const rectWidth of [0, -400, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() =>
        imageClientToIntrinsic({ ...IMAGE_4000x3000_AT_400x300, rectWidth }, 300, 200),
      ).toThrow(RangeError);
    }
    for (const rectHeight of [0, -300, Number.NaN, Number.NEGATIVE_INFINITY]) {
      expect(() =>
        imageClientToIntrinsic({ ...IMAGE_4000x3000_AT_400x300, rectHeight }, 300, 200),
      ).toThrow(RangeError);
    }
  });

  it("rejects zero, negative, NaN, and infinite natural dimensions", () => {
    for (const naturalWidth of [0, -4000, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() =>
        imageScaleFactors({ ...IMAGE_4000x3000_AT_400x300, naturalWidth }),
      ).toThrow(RangeError);
    }
    for (const naturalHeight of [0, -3000, Number.NaN, Number.NEGATIVE_INFINITY]) {
      expect(() =>
        imageScaleFactors({ ...IMAGE_4000x3000_AT_400x300, naturalHeight }),
      ).toThrow(RangeError);
    }
  });

  it("rejects non-finite client, intrinsic, and offset values", () => {
    for (const bad of [Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY]) {
      expect(() => imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, bad, 200)).toThrow(
        RangeError,
      );
      expect(() => imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 300, bad)).toThrow(
        RangeError,
      );
      expect(() => imageIntrinsicToClient(IMAGE_4000x3000_AT_400x300, bad, 1500)).toThrow(
        RangeError,
      );
      expect(() => wallClientToWorld(wallAtZoom(1), bad, 50)).toThrow(RangeError);
      expect(() => wallWorldToClient(wallAtZoom(1), 10, bad)).toThrow(RangeError);
      expect(() =>
        imageClientToIntrinsic({ ...IMAGE_4000x3000_AT_400x300, rectLeft: bad }, 300, 200),
      ).toThrow(RangeError);
      expect(() => wallClientToWorld({ ...wallAtZoom(1), panX: bad }, 300, 200)).toThrow(
        RangeError,
      );
    }
  });

  it("rejects zero, negative, NaN, and infinite wall zoom", () => {
    for (const zoom of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => wallClientToWorld(wallAtZoom(zoom), 300, 200)).toThrow(RangeError);
      expect(() => wallWorldToClient(wallAtZoom(zoom), 10, 20)).toThrow(RangeError);
      expect(() => wallBrushLogicalWidth(4, zoom)).toThrow(RangeError);
    }
  });

  it("rejects zero, negative, NaN, and infinite brush diameters and scales", () => {
    for (const cssDiameter of [0, -4, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => imageBrushLogicalWidth(cssDiameter, 0.1, 0.1)).toThrow(RangeError);
      expect(() => wallBrushLogicalWidth(cssDiameter, 1)).toThrow(RangeError);
    }
    for (const scale of [0, -0.1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => imageBrushLogicalWidth(4, scale, 0.1)).toThrow(RangeError);
      expect(() => imageBrushLogicalWidth(4, 0.1, scale)).toThrow(RangeError);
    }
  });
});

describe("device pixel ratio independence", () => {
  it("produces identical results for identical CSS-pixel inputs regardless of DPR", () => {
    const g = globalThis as { devicePixelRatio?: number };
    const original = g.devicePixelRatio;
    try {
      g.devicePixelRatio = 1;
      const atDpr1 = imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 300, 200);
      const wallDpr1 = wallClientToWorld(wallAtZoom(0.2), 300, 200);
      const brush1 = [imageBrushLogicalWidth(4, 0.1, 0.1), wallBrushLogicalWidth(4, 1)];
      g.devicePixelRatio = 2;
      const atDpr2 = imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 300, 200);
      const wallDpr2 = wallClientToWorld(wallAtZoom(0.2), 300, 200);
      const brush2 = [imageBrushLogicalWidth(4, 0.1, 0.1), wallBrushLogicalWidth(4, 1)];
      expect(atDpr2).toEqual(atDpr1);
      expect(wallDpr2).toEqual(wallDpr1);
      expect(brush2).toEqual(brush1);
      expect(brush1).toEqual([40, 4]);
    } finally {
      g.devicePixelRatio = original;
    }
  });

  it("keeps identical screen-space samples identical at different image render widths", () => {
    const small = imageClientToIntrinsic(IMAGE_4000x3000_AT_400x300, 300, 200);
    const large = imageClientToIntrinsic(IMAGE_4000x3000_AT_800x600, 500, 350);
    expect(small).toEqual({ x: 2000, y: 1500 });
    expect(large).toEqual({ x: 2000, y: 1500 });
  });
});

// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest';
import { bindViewTabsFit, VIEW_TABS_MIN_SCALE, viewTabsScaleForWidths } from './view-tabs.js';

afterEach(() => {
  bindViewTabsFit(null);
});

describe('view tabs fit', () => {
  it('keeps full size when the labels already fit', () => {
    expect(viewTabsScaleForWidths(360, 300)).toBe(1);
  });

  it('scales the row down so full labels still fit in one row', () => {
    expect(viewTabsScaleForWidths(240, 300)).toBeCloseTo(240 / 300);
  });

  it('does not scale below the minimum', () => {
    expect(viewTabsScaleForWidths(50, 400)).toBe(VIEW_TABS_MIN_SCALE);
  });
});

import { describe, expect, it } from 'vitest';
import {
  RAINBOW_COLORS,
  DOUBLE_TAP_MS,
  DRAG_THRESHOLD_PX,
  TRANSIENT_COALESCE_MS,
  colorIndexFromHex,
  nextColor,
  normalizeHex,
} from './wall-postbaby-constants.js';

describe('wall-postbaby-constants', () => {
  it('starts the palette with powder blue and preserves the Postbaby cycle order', () => {
    expect(RAINBOW_COLORS).toEqual([
      '#B0E0E6',
      '#DC143C',
      '#FF7F00',
      '#FFBF00',
      '#AAFF00',
      '#89CFF0',
      '#CBC3E3',
      '#CF9FFF',
    ]);
  });

  it('lifts thresholds from Postbaby script.js', () => {
    expect(DOUBLE_TAP_MS).toBe(300);
    expect(DRAG_THRESHOLD_PX).toBe(10);
    expect(TRANSIENT_COALESCE_MS).toBe(100);
  });

  it('normalizes hex values for case-insensitive palette lookup', () => {
    expect(normalizeHex('#b0e0e6')).toBe('#B0E0E6');
    expect(normalizeHex('b0e0e6')).toBe('#B0E0E6');
    expect(normalizeHex('')).toBe('');
    expect(normalizeHex(null)).toBe('');
  });

  it('finds palette indices and returns 0 on miss', () => {
    expect(colorIndexFromHex('#B0E0E6')).toBe(0);
    expect(colorIndexFromHex('#b0e0e6')).toBe(0);
    expect(colorIndexFromHex('#DC143C')).toBe(1);
    expect(colorIndexFromHex('#CF9FFF')).toBe(7);
    expect(colorIndexFromHex('#123456')).toBe(0);
    expect(colorIndexFromHex(null)).toBe(0);
  });

  it('cycles colors with wraparound', () => {
    expect(nextColor('#B0E0E6')).toEqual({ color: '#DC143C', index: 1 });
    expect(nextColor('#DC143C')).toEqual({ color: '#FF7F00', index: 2 });
    expect(nextColor('#CF9FFF')).toEqual({ color: '#B0E0E6', index: 0 });
    // Unknown colors start at palette[1] (one after the fallback index 0).
    expect(nextColor('#123456')).toEqual({ color: '#DC143C', index: 1 });
  });
});

// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest';
import {
  appendTagParams,
  getTagsFromUrl,
  normalizeBoardTagFilters,
  setTagParams,
} from './board-filter-url.js';

describe('board tag URL state', () => {
  afterEach(() => window.history.replaceState({}, '', '/'));

  it('reads repeated tag parameters in order and normalizes only transport details', () => {
    window.history.replaceState({}, '', '/alpha?tag=%20Bug%20&tag=feature&tag=bug&tag=&tag=Needs+QA');

    expect(getTagsFromUrl()).toEqual(['Bug', 'feature', 'Needs QA']);
  });

  it('rewrites only tag parameters while preserving unrelated query state and hash', () => {
    window.history.replaceState({}, '', '/alpha?search=needle&tag=old&sprintId=7#card');

    expect(setTagParams([' bug ', 'FEATURE', 'Bug'])).toEqual(['bug', 'FEATURE']);

    const url = new URL(window.location.href);
    expect(url.searchParams.getAll('tag')).toEqual(['bug', 'FEATURE']);
    expect(url.searchParams.get('search')).toBe('needle');
    expect(url.searchParams.get('sprintId')).toBe('7');
    expect(url.hash).toBe('#card');
  });

  it('caps normalized URL filters at twenty in first-seen order', () => {
    const tags = Array.from({ length: 25 }, (_, index) => `tag-${index}`);
    expect(normalizeBoardTagFilters(tags)).toEqual(tags.slice(0, 20));
  });

  it('appends every normalized tag to request query parameters', () => {
    const params = new URLSearchParams('limitPerLane=20');
    appendTagParams(params, ['bug', 'feature', 'BUG']);

    expect(params.getAll('tag')).toEqual(['bug', 'feature']);
    expect(params.get('limitPerLane')).toBe('20');
  });
});

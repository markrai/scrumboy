import { describe, expect, it } from 'vitest';
import { resolveNotificationProjectSlugCore } from './notification-slug-resolve.js';

describe('resolveNotificationProjectSlugCore', () => {
  it('prefers map slug over event slug', () => {
    expect(resolveNotificationProjectSlugCore('event-slug', 'map-slug', null)).toBe('map-slug');
  });

  it('uses catalog when map missing', () => {
    expect(resolveNotificationProjectSlugCore('event-slug', null, 'cat-slug')).toBe('cat-slug');
  });

  it('uses event when map and catalog missing', () => {
    expect(resolveNotificationProjectSlugCore('event-slug', null, null)).toBe('event-slug');
  });

  it('returns null when all missing', () => {
    expect(resolveNotificationProjectSlugCore(null, undefined, null)).toBeNull();
  });

  it('treats empty event string as missing', () => {
    expect(resolveNotificationProjectSlugCore('', null, null)).toBeNull();
    expect(resolveNotificationProjectSlugCore('  ', null, null)).toBeNull();
  });

  it('rename case: map wins over stale event', () => {
    expect(resolveNotificationProjectSlugCore('old-slug', 'new-slug', null)).toBe('new-slug');
  });
});

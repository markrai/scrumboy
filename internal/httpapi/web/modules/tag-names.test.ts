import { describe, expect, it } from 'vitest';
import { compareTagNames, tagIdentityKey, tagLookupKey } from './tag-names.js';

describe('Unicode tag display matching', () => {
  it('matches case and NFC-equivalent display strings without compatibility folding', () => {
    expect(tagLookupKey('Café')).toBe('café');
    expect(tagLookupKey('Cafe\u0301')).toBe('café');
    expect(tagLookupKey('ERP系统流程')).toBe('erp系统流程');
    expect(tagLookupKey('Ａ')).toBe('ａ');
    expect(tagLookupKey('Ａ')).not.toBe(tagLookupKey('A'));
  });

  it('uses deterministic non-locale ordering', () => {
    const names = ['つながるひろが', 'Café', 'erp系统流程', '개발-작업'];
    expect([...names].sort(compareTagNames)).toEqual(['Café', 'erp系统流程', 'つながるひろが', '개발-작업']);
  });

  it('documents the JavaScript and Go lowercase boundary', () => {
    expect(tagLookupKey('İ')).toBe('i\u0307');
    expect(tagLookupKey('İ')).not.toBe('i');
    expect(tagIdentityKey('İ')).toBe('İ');
    expect(tagIdentityKey('I')).toBe('i');
    expect(tagIdentityKey('Cafe\u0301')).toBe('café');
    expect(tagIdentityKey('CAFÉ')).toBe('café');
    expect(tagIdentityKey('ΟΣ')).toBe('οσ');
    expect(tagLookupKey('ΟΣ')).toBe('ος');
    expect(new Set(['i', 'i\u0307'].map(tagIdentityKey)).size).toBe(2);
  });
});

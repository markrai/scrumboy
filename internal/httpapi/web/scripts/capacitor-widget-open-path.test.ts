// @vitest-environment happy-dom

import { describe, expect, it } from 'vitest';
import { sanitizeWidgetOpenPath } from '../../../../mobile/capacitor/shell/bootstrap-core.js';

describe('widget open path sanitizer', () => {
  it('accepts dashboard and todo routes', () => {
    expect(sanitizeWidgetOpenPath('/dashboard')).toBe('/dashboard');
    expect(sanitizeWidgetOpenPath('/alpha/t/12')).toBe('/alpha/t/12');
  });

  it('rejects schemes, hosts, and OIDC callback paths', () => {
    expect(sanitizeWidgetOpenPath('com.markrai.scrumboy://oidc/callback')).toBeNull();
    expect(sanitizeWidgetOpenPath('//evil.example/dashboard')).toBeNull();
    expect(sanitizeWidgetOpenPath('/oidc/callback')).toBeNull();
    expect(sanitizeWidgetOpenPath('/auth/reset-password')).toBeNull();
    expect(sanitizeWidgetOpenPath('/dashboard?x=1')).toBeNull();
    expect(sanitizeWidgetOpenPath('/alpha/t/0')).toBeNull();
  });
});

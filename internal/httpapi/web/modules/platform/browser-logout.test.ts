// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BrowserServerTransport } from './browser-server-transport.js';

describe('browser logout return destination', () => {
  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('posts the current path and query as return_to so a still-published board resolves again', async () => {
    history.replaceState({}, '', '/ignite/t/3?tag=api');
    let submitted: HTMLFormElement | null = null;
    vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(function (this: HTMLFormElement) {
      submitted = this;
    });
    await new BrowserServerTransport().logout();
    expect(submitted).not.toBeNull();
    const form = submitted as unknown as HTMLFormElement;
    expect(form.method.toLowerCase()).toBe('post');
    expect(new URL(form.action, 'https://app.test').pathname).toBe('/api/auth/logout');
    const input = form.querySelector<HTMLInputElement>('input[name="return_to"]');
    expect(input?.type).toBe('hidden');
    expect(input?.value).toBe('/ignite/t/3?tag=api');
  });
});

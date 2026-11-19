// @vitest-environment happy-dom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

const api = vi.fn();
const toast = vi.fn();
vi.mock('../api.js', () => ({ apiFetch: api }));
vi.mock('../realtime/guard.js', () => ({ recordLocalMutation: vi.fn() }));
vi.mock('../utils.js', () => ({ showToast: toast }));
vi.mock('../i18n/index.js', () => ({
  apiErrorMessage: () => 'GitHub request failed',
}));

const repository = { id: 17, connectionId: 1, kind: 'repository', externalId: '123', name: 'org/repo', url: 'https://github.com/org/repo', availability: 'available' };
const issue = { resourceId: 17, kind: 'issue', number: 357, title: 'Fix controls', state: 'open', draft: false, merged: false, url: 'https://github.com/org/repo/issues/357', repository: 'org/repo', availability: 'available' };

describe('card GitHub issue and pull request references', () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="todoExternalResourcesField"></div>';
    api.mockReset();
    toast.mockReset();
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method === 'POST' || opts?.method === 'DELETE') return null;
      if (path.endsWith('/github-items')) return [];
      if (path.endsWith('/integrations/resources')) return [repository];
      if (path.includes('/integrations/github/search?')) return { items: [issue] };
      return [];
    });
  });

  afterEach(async () => {
    const mod = await import('./todo-github-items.js');
    mod.resetTodoGitHubItems();
    vi.resetModules();
    document.body.replaceChildren();
  });

  it('allows URL pasting and GitHub search only inside project-associated repositories', async () => {
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', 5, true);
    const field = document.getElementById('todoGitHubItems')!;
    const urlInput = field.querySelector<HTMLInputElement>('input[type="url"]')!;
    urlInput.value = 'https://github.com/org/repo/issues/357';
    const buttons = Array.from(field.querySelectorAll<HTMLButtonElement>('button'));
    buttons.find(btn => btn.textContent === 'Link URL')!.click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/github-items', {
      method: 'POST', body: JSON.stringify({ url: 'https://github.com/org/repo/issues/357' }),
    }));
    await vi.waitFor(() => expect(field.querySelector<HTMLInputElement>('input[type="url"]')).not.toBe(urlInput));
    const input = field.querySelector<HTMLInputElement>('input[type="search"]')!;
    input.value = 'Fix controls';
    Array.from(field.querySelectorAll<HTMLButtonElement>('button')).find(btn => btn.textContent === 'Search')!.click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith(expect.stringContaining('/api/board/alpha/integrations/github/search?')));
    const query = api.mock.calls.find(([path]) => path.includes('/integrations/github/search?'))[0];
    const params = new URL(query, 'https://example.test').searchParams;
    expect(params.get('resourceId')).toBe('17');
    expect(params.get('kind')).toBe('issue');
    expect(params.get('q')).toBe('Fix controls');
    await vi.waitFor(() => expect(field.querySelector<HTMLAnchorElement>('a[href$="/issues/357"]')).not.toBeNull());
    const resultButtons = Array.from(field.querySelectorAll<HTMLButtonElement>('button'));
    resultButtons.find(btn => btn.textContent === 'Link')!.click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/github-items', {
      method: 'POST', body: JSON.stringify({ url: 'https://github.com/org/repo/issues/357' }),
    }));
  });

  it('renders informative read-only issue status for project Viewers', async () => {
    api.mockImplementation(async (path: string) => {
      if (path.endsWith('/github-items')) return [{ ...issue, title: '<script>alert(1)</script>' }];
      if (path.endsWith('/integrations/resources')) return [repository];
      return [];
    });
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', 5, false);
    const field = document.getElementById('todoGitHubItems')!;
    expect(field.textContent).toContain('Issue #357');
    expect(field.textContent).toContain('Open');
    expect(field.querySelector('script')).toBeNull();
    expect(field.querySelector('input')).toBeNull();
    expect(field.querySelector('button')).toBeNull();
    expect(field.querySelector<HTMLAnchorElement>('a')?.target).toBe('_blank');
  });

  it('refreshes metadata and unlinks only the selected item', async () => {
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method) return null;
      if (path.endsWith('/github-items')) return [issue];
      if (path.endsWith('/integrations/resources')) return [repository];
      return [];
    });
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', 5, true);
    const field = document.getElementById('todoGitHubItems')!;
    const refresh = Array.from(field.querySelectorAll<HTMLButtonElement>('button')).find(btn => btn.textContent === 'Refresh')!;
    refresh.click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/github-items/17/issue/357/refresh', { method: 'POST' }));
    const remove = Array.from(field.querySelectorAll<HTMLButtonElement>('button')).find(btn => btn.textContent === 'Remove')!;
    remove.click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/github-items/17/issue/357', { method: 'DELETE' }));
  });
});

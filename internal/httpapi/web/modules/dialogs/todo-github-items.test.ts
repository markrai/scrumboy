// @vitest-environment happy-dom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

const api = vi.fn();
const toast = vi.fn();
vi.mock('../api.js', () => ({ apiFetch: api }));
vi.mock('../realtime/guard.js', () => ({ recordLocalMutation: vi.fn() }));
vi.mock('../utils.js', () => ({ showToast: toast }));
vi.mock('../i18n/index.js', () => ({ apiErrorMessage: () => 'GitHub request failed' }));

const repository = { id: 17, connectionId: 1, kind: 'repository', externalId: '123', name: 'org/repo', url: 'https://github.com/org/repo', availability: 'available' };
const issue = { resourceId: 17, kind: 'issue', number: 357, title: 'Fix controls', state: 'open', draft: false, merged: false, url: 'https://github.com/org/repo/issues/357', repository: 'org/repo', availability: 'available' };
const pr = { ...issue, kind: 'pr', number: 41, title: 'Review GitHub UX', url: 'https://github.com/org/repo/pull/41' };

function options(): HTMLSelectElement[] {
  return Array.from(document.querySelectorAll<HTMLSelectElement>('#todoGitHubItems select'));
}

function findButton(text: string): HTMLButtonElement {
  const result = Array.from(document.querySelectorAll<HTMLButtonElement>('#todoGitHubItems button'))
    .find(btn => btn.textContent === text);
  if (!result) throw new Error('Missing button ' + text);
  return result;
}

describe('Todo GitHub reference browser', () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="todoExternalResourcesField"></div>';
    api.mockReset();
    toast.mockReset();
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method === 'POST' || opts?.method === 'DELETE') return null;
      if (path.endsWith('/github-items')) return [];
      if (path.endsWith('/integrations/resources')) return [repository];
      if (path.includes('/integrations/github/browse?')) {
        return { items: path.includes('kind=issue') ? [issue] : [pr] };
      }
      return [];
    });
  });

  afterEach(async () => {
    const mod = await import('./todo-github-items.js');
    mod.resetTodoGitHubItems();
    vi.resetModules();
    document.body.replaceChildren();
  });

  it('loads open pull requests automatically and filters by kind, state and title without Search', async () => {
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', 5, true);
    const initial = api.mock.calls.find(([path]) => path.includes('/integrations/github/browse?'))?.[0];
    expect(initial).toContain('kind=pr');
    expect(initial).toContain('state=open');
    expect(initial).not.toContain('&q=');
    expect(document.getElementById('todoGitHubItems')?.textContent).toContain('Review GitHub UX');
    expect(Array.from(document.querySelectorAll('button')).some(b => b.textContent === 'Search')).toBe(false);
    const kind = options()[2];
    kind.value = 'issue';
    kind.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(document.getElementById('todoGitHubItems')?.textContent).toContain('Fix controls'));
    const state = options()[1];
    state.value = 'closed';
    state.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(api.mock.calls.some(([p]) => p.includes('/browse?') && p.includes('state=closed'))).toBe(true));
    const query = document.querySelector<HTMLInputElement>('#todoGitHubItems input[type="search"]')!;
    query.value = 'controls';
    query.dispatchEvent(new Event('input'));
    await vi.waitFor(() => expect(api.mock.calls.some(([p]) => p.includes('q=controls'))).toBe(true), { timeout: 2500 });
  });

  it('stages linked GitHub issues and PRs before creation and persists after server creates the Todo', async () => {
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', null, true);
    findButton('Link').click();
    await vi.waitFor(() => expect(mod.getPendingTodoGitHubURLs()).toEqual([pr.url]));
    expect(api.mock.calls.some(([path,opts]) => path.includes('/todos/') && opts?.method === 'POST')).toBe(false);
    const urlInput = document.querySelector<HTMLInputElement>('#todoGitHubItems input[aria-label="GitHub issue or PR URL"]')!;
    urlInput.value = issue.url;
    findButton('Link URL').click();
    await vi.waitFor(() => expect(mod.getPendingTodoGitHubURLs()).toHaveLength(2));
    expect(await mod.commitPendingTodoGitHubItems('alpha', 11)).toBe(0);
    expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/11/github-items', {
      method: 'POST', body: JSON.stringify({ url: issue.url }),
    });
    expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/11/github-items', {
      method: 'POST', body: JSON.stringify({ url: pr.url }),
    });
  });

  it('rejects unrelated GitHub hosts and unlinked repositories while staging', async () => {
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', null, true);
    const input = document.querySelector<HTMLInputElement>('#todoGitHubItems input[aria-label="GitHub issue or PR URL"]')!;
    for (const [index, url] of ['https://github.com.evil.test/org/repo/issues/1', 'https://github.com/secret/repo/issues/1'].entries()) {
      input.value = url;
      findButton('Link URL').click();
      await vi.waitFor(() => expect(toast).toHaveBeenCalledTimes(index + 1));
      await vi.waitFor(() => expect(findButton('Link URL').disabled).toBe(false));
    }
    expect(mod.getPendingTodoGitHubURLs()).toEqual([]);
  });

  it('shows cached links read-only for Viewers without allowing mutations or GitHub browsing', async () => {
    api.mockImplementation(async (path: string) => {
      if (path.endsWith('/github-items')) return [{ ...issue, title: '<script>alert(1)</script>' }];
      if (path.endsWith('/integrations/resources')) return [repository];
      return [];
    });
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', 5, false);
    const field = document.getElementById('todoGitHubItems')!;
    expect(field.textContent).toContain('Issue #357');
    expect(field.querySelector('script')).toBeNull();
    expect(field.querySelector('input')).toBeNull();
    expect(field.querySelector('button')).toBeNull();
    expect(field.querySelector<HTMLAnchorElement>('a')?.target).toBe('_blank');
    expect(api.mock.calls.filter(([p]) => p.includes('/browse?'))).toHaveLength(0);
  });

  it('refreshes and removes only saved Todo references', async () => {
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method) return null;
      if (path.endsWith('/github-items')) return [issue];
      if (path.endsWith('/integrations/resources')) return [repository];
      if (path.includes('/browse?')) return { items: [pr] };
      return [];
    });
    const mod = await import('./todo-github-items.js');
    await mod.initializeTodoGitHubItems('alpha', 5, true);
    findButton('Refresh').click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/github-items/17/issue/357/refresh', { method: 'POST' }));
    findButton('Remove').click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/github-items/17/issue/357', { method: 'DELETE' }));
  });

  it('ignores stale browse responses after selecting a different reference type', async () => {
    let finishFirst: ((value: unknown) => void) | undefined;
    api.mockImplementation(async (path: string) => {
      if (path.endsWith('/github-items')) return [];
      if (path.endsWith('/integrations/resources')) return [repository];
      if (path.includes('/browse?') && path.includes('kind=pr')) return await new Promise(resolve => { finishFirst = resolve; });
      if (path.includes('/browse?') && path.includes('kind=issue')) return { items: [issue] };
      return [];
    });
    const mod = await import('./todo-github-items.js');
    const pending = mod.initializeTodoGitHubItems('alpha', 5, true);
    await vi.waitFor(() => expect(finishFirst).toBeDefined());
    const kind = options()[2];
    kind.value = 'issue';
    kind.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(document.getElementById('todoGitHubItems')?.textContent).toContain('Fix controls'));
    finishFirst!({ items: [pr] });
    await pending;
    expect(document.getElementById('todoGitHubItems')?.textContent).not.toContain('Review GitHub UX');
  });
});

// @vitest-environment happy-dom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

const api = vi.fn();
const toast = vi.fn();
const permission = { canEditLinks: true };

vi.mock('../api.js', () => ({ apiFetch: api }));
vi.mock('../realtime/guard.js', () => ({ recordLocalMutation: vi.fn() }));
vi.mock('../utils.js', () => ({ showToast: toast }));
vi.mock('../i18n/index.js', () => ({
  t: (key: string) => key,
  apiErrorMessage: () => 'Failed',
}));
vi.mock('./todo-permissions.js', () => ({ getTodoFormPermissions: () => permission }));

const resource = { id: 17, connectionId: 1, kind: 'repository', externalId: '99', name: '<unsafe>', url: 'https://github.com/test/repo' };

describe('todo external references', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <dialog id="todoDialog"></dialog>
      <div id="todoExternalResourcesField" style="display:none">
        <div id="externalResourceControls"><select id="externalResourcePicker"></select><button id="addExternalResourceBtn"></button></div>
        <div id="externalResourcesChips"></div>
      </div>`;
    api.mockReset();
    toast.mockReset();
    permission.canEditLinks = true;
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method === 'POST' || opts?.method === 'DELETE') return null;
      if (path.endsWith('/integrations/resources')) return [resource];
      if (path.endsWith('/integrations')) return [resource];
      return [];
    });
  });

  afterEach(async () => {
    const mod = await import('./todo-external-links.js');
    mod.resetTodoExternalLinks();
    vi.resetModules();
    document.body.replaceChildren();
  });

  it('renders an escaped external link and hides editing controls for viewers', async () => {
    permission.canEditLinks = false;
    const mod = await import('./todo-external-links.js');
    await mod.initializeTodoExternalLinks('alpha', 5, false);
    const anchor = document.querySelector<HTMLAnchorElement>('#externalResourcesChips a');
    expect(anchor?.textContent).toBe('<unsafe>');
    expect(anchor?.rel).toContain('noopener');
    expect(anchor?.target).toBe('_blank');
    expect(document.querySelector('#externalResourcesChips button')).toBeNull();
    expect((document.getElementById('externalResourceControls') as HTMLElement).hidden).toBe(true);
  });

  it('retains unavailable links and excludes them from new-link choices', async () => {
    const mod = await import('./todo-external-links.js');
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method === 'POST' || opts?.method === 'DELETE') return null;
      if (path.endsWith('/integrations/resources')) return [{ ...resource, id: 18, name: 'other', availability: 'unavailable' }];
      if (path.endsWith('/integrations')) return [{ ...resource, availability: 'unavailable' }];
      return [];
    });
    await mod.initializeTodoExternalLinks('alpha', 5, false);
    expect(document.querySelector('#externalResourcesChips')?.textContent).toContain('Unavailable');
    expect(document.querySelector<HTMLAnchorElement>('#externalResourcesChips a')?.href).toContain('github.com/test/repo');
    expect((document.getElementById('externalResourceControls') as HTMLElement).hidden).toBe(true);
    expect(document.querySelector('#externalResourcesChips button')).not.toBeNull();
  });

  it('keeps repository references visible when GitHub item metadata fails', async () => {
    api.mockImplementation(async (path: string) => {
      if (path.endsWith('/github-items')) throw new Error('offline');
      if (path.endsWith('/integrations/resources')) return [resource];
      if (path.endsWith('/integrations')) return [resource];
      return [];
    });
    const mod = await import('./todo-external-links.js');
    await mod.initializeTodoExternalLinks('alpha', 5, false);
    await vi.waitFor(() => expect(toast).toHaveBeenCalled());
    expect(document.querySelector<HTMLAnchorElement>('#externalResourcesChips a')?.href).toContain('github.com/test/repo');
    expect(document.getElementById('todoGitHubItems')).toBeNull();
  });

  it('links available resources and unlinks existing references', async () => {
    const mod = await import('./todo-external-links.js');
    api.mockImplementation(async (path: string, opts?: RequestInit) => {
      if (opts?.method === 'POST' || opts?.method === 'DELETE') return null;
      if (path.endsWith('/integrations/resources')) return [resource, {...resource, id: 18, name: 'second'}];
      if (path.endsWith('/integrations')) return [resource];
      return [];
    });
    await mod.initializeTodoExternalLinks('alpha', 5, false);
    document.querySelector<HTMLButtonElement>('#externalResourcesChips button')?.click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/integrations/17', { method:'DELETE' }));
    const picker = document.getElementById('externalResourcePicker') as HTMLSelectElement;
    expect(picker.value).toBe('18');
    (document.getElementById('addExternalResourceBtn') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/5/integrations', { method:'POST', body:JSON.stringify({resourceId:18}) }));
  });
  it('stages repositories in New Todo and saves them only after a Todo ID exists', async () => {
    const mod = await import('./todo-external-links.js');
    await mod.initializeTodoExternalLinksForCreate('alpha');
    expect(document.querySelector<HTMLButtonElement>('#addExternalResourceBtn')).not.toBeNull();
    const picker = document.getElementById('externalResourcePicker') as HTMLSelectElement;
    expect(picker.value).toBe('17');
    (document.getElementById('addExternalResourceBtn') as HTMLButtonElement).click();
    expect(mod.pendingTodoExternalLinksSignature()).toContain('"resources":[17]');
    expect(api.mock.calls.some(([path, opts]) => path.includes('/todos/') && opts?.method === 'POST')).toBe(false);
    const failures = await mod.commitPendingTodoExternalLinks('alpha', 35);
    expect(failures).toBe(0);
    expect(api).toHaveBeenCalledWith('/api/board/alpha/todos/35/integrations', {
      method: 'POST', body: JSON.stringify({ resourceId:17 }),
    });
  });

});

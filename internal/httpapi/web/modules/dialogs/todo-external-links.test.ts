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
});

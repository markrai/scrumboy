// @vitest-environment happy-dom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

const requests = vi.fn();
const toast = vi.fn();
const confirm = vi.fn();

vi.mock('../api.js', () => ({ apiFetch: requests }));
vi.mock('../utils.js', () => ({ showToast: toast, showConfirmDialog: confirm }));

function root(): HTMLElement {
    const el = document.createElement('div');
    document.body.append(el);
    return el;
}

describe('GitHub admin settings tab', () => {
    beforeEach(() => {
        document.body.replaceChildren();
        requests.mockReset();
        toast.mockReset();
        confirm.mockReset().mockResolvedValue(true);
    });

    afterEach(() => {
        document.body.replaceChildren();
    });

    it('explains operator configuration without triggering live discovery', async () => {
        requests.mockResolvedValue({ configured: false, connections: [], installUrl: '' });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, 'alpha', controller.signal);
        await vi.waitFor(() => expect(el.textContent).toContain('SCRUMBOY_GITHUB_APP_ID'));
        expect(requests).toHaveBeenCalledTimes(1);
        controller.abort();
    });

    it('discovers an installation and connects without inserting untrusted account names into HTML', async () => {
        let connected = false;
        requests.mockImplementation(async (path: string, init?: RequestInit) => {
            if (path.endsWith('/installations/25/sync') && init?.method === 'POST') {
                connected = true;
                return { connectionId: 5, repositoryCount: 1 };
            }
            if (path === '/api/admin/integrations/github') {
                return {
                    configured: true,
                    installUrl: 'https://github.com/apps/example/installations/new',
                    connections: connected ? [{ id: 5, installationId: '25', account: '<img src=x onerror=alert(1)>', status: 'active' }] : [],
                };
            }
            if (path.endsWith('/installations')) return [{ id: 25, account: '<img src=x onerror=alert(1)>', kind: 'Organization' }];
            if (path.endsWith('/connections/5/resources')) return [{ id: 10, name: 'example/repo' }];
            if (path.endsWith('/integrations/resources')) return [];
            return [];
        });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, 'alpha', controller.signal);
        await vi.waitFor(() => expect(Array.from(el.querySelectorAll('button')).some(button => button.textContent === 'Connect and sync')).toBe(true));
        expect(el.querySelector('img')).toBeNull();
        const connect = Array.from(el.querySelectorAll('button')).find(button => button.textContent === 'Connect and sync');
        connect?.click();
        await vi.waitFor(() => expect(Array.from(el.querySelectorAll('button')).some(button => button.textContent === 'Sync repositories')).toBe(true));
        expect(requests).toHaveBeenCalledWith('/api/admin/integrations/github/installations/25/sync', expect.objectContaining({ method: 'POST' }));
        expect(el.querySelector('img')).toBeNull();
        controller.abort();
    });
});

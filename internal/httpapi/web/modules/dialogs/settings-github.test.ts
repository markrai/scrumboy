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

describe('GitHub compact onboarding', () => {
    beforeEach(() => {
        document.body.replaceChildren();
        requests.mockReset();
        toast.mockReset();
        confirm.mockReset().mockResolvedValue(true);
    });

    afterEach(() => {
        document.body.replaceChildren();
    });

    it('shows concise setup guidance and points to the main-branch Markdown guide', async () => {
        requests.mockResolvedValue({ configured: false, connections: [], installUrl: '' });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, 'alpha', controller.signal);
        await vi.waitFor(() => expect(el.querySelector('.github-onboarding__columns')).not.toBeNull());
        const guide = el.querySelector<HTMLAnchorElement>('.github-onboarding__guide');
        expect(guide?.href).toBe('https://github.com/markrai/scrumboy/blob/main/docs/github-app.md');
        expect(guide?.target).toBe('_blank');
        expect(guide?.rel).toContain('noopener');
        expect(el.textContent).toContain('Create a GitHub App');
        expect(el.textContent).toContain('Restart Scrumboy');
        expect(el.textContent).not.toContain('Your credentials stay on your server');
        const variables = el.querySelectorAll('.github-onboarding__variable-name');
        expect(Array.from(variables).map(variable => variable.textContent)).toEqual([
            'SCRUMBOY_GITHUB_APP_ID',
            'SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE',
            'SCRUMBOY_GITHUB_APP_SLUG',
        ]);
        expect(requests).toHaveBeenCalledTimes(1);
        controller.abort();
    });

    it('copies the GitHub App variable name on click', async () => {
        const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
        const writeText = vi.fn().mockResolvedValue(undefined);
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
        try {
            requests.mockResolvedValue({ configured: false, connections: [], installUrl: '' });
            const el = root();
            const { bindGitHubSettingsTab } = await import('./settings-github.js');
            const controller = new AbortController();
            bindGitHubSettingsTab(el, 'alpha', controller.signal);
            await vi.waitFor(() => expect(el.querySelector('.github-onboarding__copy')).not.toBeNull());
            const copy = el.querySelector<HTMLButtonElement>('.github-onboarding__copy');
            copy?.click();
            await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('SCRUMBOY_GITHUB_APP_ID'));
            await vi.waitFor(() => expect(copy?.getAttribute('aria-label')).toBe('Copied SCRUMBOY_GITHUB_APP_ID'));
            controller.abort();
        } finally {
            if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard);
            else Reflect.deleteProperty(navigator, 'clipboard');
        }
    });
});

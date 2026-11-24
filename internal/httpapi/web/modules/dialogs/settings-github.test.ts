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
        expect(el.querySelector('img[src="x"]')).toBeNull();
        const connect = Array.from(el.querySelectorAll('button')).find(button => button.textContent === 'Connect and sync');
        connect?.click();
        await vi.waitFor(() => expect(Array.from(el.querySelectorAll('button')).some(button => button.textContent === 'Refresh repositories')).toBe(true));
        expect(requests).toHaveBeenCalledWith('/api/admin/integrations/github/installations/25/sync', expect.objectContaining({ method: 'POST' }));
        expect(el.querySelector('img[src="x"]')).toBeNull();
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

    it('prepares the registration form only when a secure credential sink is available', async () => {
        const submit = vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(function (this: HTMLFormElement) {
            expect(this.method).toBe('post');
            expect(this.action).toMatch(/^https:\/\/github\.com\/settings\/apps\/new\?state=[a-f0-9]{64}$/);
            expect(new FormData(this).get('manifest')).toBe('{"public":false}');
        });
        try {
            requests.mockImplementation(async (path: string) => {
                if (path === '/api/admin/integrations/github') {
                    return { configured: false, manifestEnrollmentAvailable: true, connections: [], installUrl: '' };
                }
                if (path.endsWith('/manifest/start')) {
                    return { action: 'https://github.com/settings/apps/new?state=' + 'a'.repeat(64), manifest: '{"public":false}' };
                }
                return {};
            });
            const el = root();
            const { bindGitHubSettingsTab } = await import('./settings-github.js');
            const controller = new AbortController();
            bindGitHubSettingsTab(el, null, controller.signal);
            await vi.waitFor(() => expect(Array.from(el.querySelectorAll('button')).some(button => button.textContent === 'Set up GitHub')).toBe(true));
            Array.from(el.querySelectorAll('button')).find(button => button.textContent === 'Set up GitHub')?.click();
            await vi.waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
            controller.abort();
        } finally {
            submit.mockRestore();
        }
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

describe('GitHub integration management layout', () => {
    beforeEach(() => {
        document.body.replaceChildren();
        requests.mockReset();
        toast.mockReset();
        confirm.mockReset().mockResolvedValue(true);
    });

    afterEach(() => {
        document.body.replaceChildren();
    });

    it('shows the supplied GitHub marks, a compact installation row, and no duplicate connected section', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            return { configured: true, installUrl: 'https://github.com/apps/scrumboy-connect/installations/new', connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
        });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, null, controller.signal);
        await vi.waitFor(() => expect(el.querySelectorAll('.github-integration__account')).toHaveLength(1));
        expect(el.querySelector('.github-integration__header')?.textContent).toContain('App configured');
        expect(el.querySelector('.github-integration__logo--white')?.getAttribute('src')).toBe('/assets/github/GitHub_Invertocat_White.svg');
        expect(el.querySelector('.github-integration__logo--black')?.getAttribute('src')).toBe('/assets/github/GitHub_Invertocat_Black.svg');
        expect(el.querySelector('.github-integration__account')?.textContent).toContain('Connected');
        expect(el.textContent).not.toContain('Connected accounts');
        expect(el.querySelector('.github-integration__disconnect')).not.toBeNull();
        expect(el.querySelector('.github-integration__more')).toBeNull();
        expect(el.querySelector('input[aria-label="Project slug"]')).toBeNull();
        controller.abort();
    });

    it('preserves confirmation when disconnecting an installation', async () => {
        requests.mockImplementation(async (path: string, init?: RequestInit) => {
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            if (path.endsWith('/impact')) return { repositories: 2, projectAssociations: 1, cardReferences: 3 };
            if (init?.method === 'DELETE') return {};
            return { configured: true, installUrl: '', connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
        });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, null, controller.signal);
        await vi.waitFor(() => expect(el.querySelector('.github-integration__disconnect')).not.toBeNull());
        (el.querySelector('.github-integration__disconnect') as HTMLButtonElement).click();
        await vi.waitFor(() => expect(requests).toHaveBeenCalledWith('/api/admin/integrations/github/connections/7', expect.objectContaining({ method: 'DELETE' })));
        expect(confirm).toHaveBeenCalledTimes(1);
        expect(confirm.mock.calls[0][0]).toContain('3 card references');
        expect(requests).toHaveBeenCalledWith('/api/admin/integrations/github/connections/7', expect.objectContaining({
            method: 'DELETE',
            body: JSON.stringify({ confirmConnectionId: 7, impact: { repositories: 2, projectAssociations: 1, cardReferences: 3 } }),
        }));
        controller.abort();
    });

    it('retains disconnected accounts even when GitHub no longer reports an installation', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path.endsWith('/installations')) return [];
            return { configured: true, installUrl: '', connections: [{ id: 7, installationId: '25', account: 'old-account', status: 'active' }] };
        });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, null, controller.signal);
        await vi.waitFor(() => expect(el.querySelectorAll('.github-integration__account')).toHaveLength(1));
        expect(el.querySelector('.github-integration__account')?.textContent).toContain('old-account');
        expect(el.querySelector('.github-integration__primary')).toBeNull();
        expect(el.querySelector('.github-integration__disconnect')).not.toBeNull();
        controller.abort();
    });
});

describe('GitHub project picker flow', () => {
    beforeEach(() => {
        document.body.replaceChildren();
        requests.mockReset();
        toast.mockReset();
        confirm.mockReset().mockResolvedValue(true);
    });
    afterEach(() => { document.body.replaceChildren(); });

    it('lists maintainable projects by name, selects the current project and loads repositories', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            if (path === '/api/projects') return [
                { name: 'Engineering', slug: 'engineering', role: 'Maintainer' },
                { name: '<img src=x onerror=alert(1)>', slug: 'viewer', role: 'Viewer' },
                { name: 'Temporary', slug: 'temporary', role: 'Maintainer', expiresAt: '2027-01-01' },
                { name: 'Product', slug: 'product', role: 'maintainer' },
            ];
            if (path.endsWith('/connections/7/resources')) return [{ id: 40, name: 'markrai/scrumboy' }];
            if (path.endsWith('/integrations/resources')) return [];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, 'engineering', controller.signal);
        await vi.waitFor(() => expect(el.querySelector<HTMLSelectElement>('#githubProjectPicker')?.value).toBe('engineering'));
        const picker = el.querySelector<HTMLSelectElement>('#githubProjectPicker')!;
        expect(Array.from(picker.options).map(option => option.textContent)).toEqual(['Select a project…', 'Engineering', 'Product']);
        expect(el.querySelector('#githubAccountPicker')).toBeNull();
        expect(el.querySelector('.github-integration__account-context')).toBeNull();
        expect(el.querySelector('label[for="githubAccountPicker"]')).toBeNull();
        expect(el.querySelector('.github-integration__project h4')?.textContent).toBe('Project: Engineering');
        await vi.waitFor(() => expect(requests).toHaveBeenCalledWith('/api/board/engineering/integrations/resources', expect.anything()));
        await vi.waitFor(() => expect(el.querySelector<HTMLSelectElement>('#githubRepositoryPicker')?.value).toBe('40'));
        expect(Array.from(el.querySelectorAll('.github-integration__project-body h5')).map(el => el.textContent)).toEqual(['Linked Repositories', 'Add Repositories']);
        expect(el.querySelector('img[src="x"]')).toBeNull();
        picker.value = 'product';
        picker.dispatchEvent(new Event('change'));
        await vi.waitFor(() => expect(requests).toHaveBeenCalledWith('/api/board/product/integrations/resources', expect.anything()));
        expect(el.querySelector('.github-integration__project h4')?.textContent).toBe('Project: Product');
        controller.abort();
    });

    it('associates a repository with the selected project through existing authorized API', async () => {
        let added = false;
        requests.mockImplementation(async (path: string, init?: RequestInit) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            if (path === '/api/projects') return [{ name: 'Engineering', slug: 'engineering', role: 'Maintainer' }];
            if (path.endsWith('/connections/7/resources')) return [{ id: 40, name: 'markrai/scrumboy' }];
            if (path === '/api/board/engineering/integrations/resources' && init?.method === 'POST') { added = true; return {}; }
            if (path === '/api/board/engineering/integrations/resources') return added ? [{ id: 40, name: 'markrai/scrumboy' }] : [];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, 'engineering', controller.signal);
        await vi.waitFor(() => expect(el.querySelector<HTMLButtonElement>('.github-integration__add-repo')?.disabled).toBe(false));
        el.querySelector<HTMLButtonElement>('.github-integration__add-repo')!.click();
        await vi.waitFor(() => expect(requests).toHaveBeenCalledWith('/api/board/engineering/integrations/resources', expect.objectContaining({ method: 'POST', body: JSON.stringify({ resourceId: 40 }) })));
        await vi.waitFor(() => expect(el.querySelector('.github-integration__linked-row')?.textContent).toContain('markrai/scrumboy'));
        controller.abort();
    });

    it('shows Linked Repositories before Add Repositories without empty add controls', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            if (path === '/api/projects') return [{ name: 'Engineering', slug: 'engineering', role: 'Maintainer' }];
            if (path.endsWith('/connections/7/resources')) return [{ id: 40, name: 'markrai/scrumboy', availability: 'available' }];
            if (path === '/api/board/engineering/integrations/resources') return [{ id: 40, name: 'markrai/scrumboy', availability: 'available' }];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, 'engineering', controller.signal);
        await vi.waitFor(() => expect(el.querySelectorAll('.github-integration__linked-row')).toHaveLength(1));
        expect(Array.from(el.querySelectorAll('.github-integration__project-body h5')).map(item => item.textContent)).toEqual(['Linked Repositories', 'Add Repositories']);
        expect(el.querySelector('.github-integration__linked-row')?.textContent).toContain('markrai/scrumboy');
        expect(el.querySelector('#githubRepositoryPicker')).toBeNull();
        expect(el.querySelector('.github-integration__add-repo')).toBeNull();
        expect(el.querySelector('.github-integration__add-section')?.textContent).toContain('All available repositories are linked');
        expect(el.querySelector('#githubAccountPicker')).toBeNull();
        expect(el.querySelector('.github-integration__account-context')).toBeNull();
        controller.abort();
    });

    it('warns before unlinking a repository from a project and does not mutate when cancelled', async () => {
        let linked = true;
        confirm.mockResolvedValueOnce(false);
        requests.mockImplementation(async (path: string, init?: RequestInit) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            if (path === '/api/projects') return [{ name: 'Engineering', slug: 'engineering', role: 'Maintainer' }];
            if (path.endsWith('/connections/7/resources')) return [{ id: 40, name: 'markrai/scrumboy', availability: 'available' }];
            if (path === '/api/board/engineering/integrations/resources' && init?.method === 'DELETE') { linked = false; return {}; }
            if (path === '/api/board/engineering/integrations/resources') return linked ? [{ id: 40, name: 'markrai/scrumboy', availability: 'available' }] : [];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, 'engineering', controller.signal);
        await vi.waitFor(() => expect(el.querySelector<HTMLButtonElement>('.github-integration__remove-repo')?.textContent).toBe('Unlink from project'));
        el.querySelector<HTMLButtonElement>('.github-integration__remove-repo')!.click();
        await vi.waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
        expect(confirm.mock.calls[0][0]).toContain('Existing card references');
        expect(confirm.mock.calls[0][0]).toContain('will not restore');
        expect(confirm.mock.calls[0].slice(1)).toEqual(['Unlink repository', 'Unlink', 'danger']);
        expect(requests).not.toHaveBeenCalledWith('/api/board/engineering/integrations/resources', expect.objectContaining({ method: 'DELETE' }));

        confirm.mockResolvedValueOnce(true);
        el.querySelector<HTMLButtonElement>('.github-integration__remove-repo')!.click();
        await vi.waitFor(() => expect(requests).toHaveBeenCalledWith('/api/board/engineering/integrations/resources', expect.objectContaining({ method: 'DELETE' })));
        controller.abort();
    });

    it('keeps linked repositories project-specific through project switches and installation refresh', async () => {
        const resources = [{ id: 40, name: 'org/alpha', availability: 'available' }, { id: 41, name: 'org/beta', availability: 'available' }];
        requests.mockImplementation(async (path: string) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
            if (path.endsWith('/installations')) return [{ id: 25, account: 'markrai', kind: 'User' }];
            if (path === '/api/projects') return [{ name: 'Alpha', slug: 'alpha', role: 'Maintainer' }, { name: 'Beta', slug: 'beta', role: 'Maintainer' }];
            if (path.endsWith('/connections/7/resources')) return resources;
            if (path === '/api/board/alpha/integrations/resources') return [resources[0]];
            if (path === '/api/board/beta/integrations/resources') return [resources[1]];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, 'alpha', controller.signal);
        await vi.waitFor(() => expect(el.querySelector('.github-integration__linked-row')?.textContent).toContain('org/alpha'));
        expect(el.querySelector('.github-integration__linked-row')?.textContent).not.toContain('org/beta');
        const picker = el.querySelector<HTMLSelectElement>('#githubProjectPicker')!;
        picker.value = 'beta';
        picker.dispatchEvent(new Event('change'));
        await vi.waitFor(() => expect(el.querySelector('.github-integration__linked-row')?.textContent).toContain('org/beta'));
        expect(el.querySelector('.github-integration__linked-row')?.textContent).not.toContain('org/alpha');
        expect(el.querySelector('.github-integration__project h4')?.textContent).toBe('Project: Beta');
        el.querySelector<HTMLButtonElement>('.github-integration__refresh')!.click();
        await vi.waitFor(() => expect(el.querySelector<HTMLSelectElement>('#githubProjectPicker')?.value).toBe('beta'));
        await vi.waitFor(() => expect(el.querySelector('.github-integration__linked-row')?.textContent).toContain('org/beta'));
        controller.abort();
    });

    it('shows GitHub account selector only when multiple accounts are connected', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }, { id: 8, installationId: '26', account: 'team', status: 'active' }] };
            if (path.endsWith('/installations')) return [];
            if (path === '/api/projects') return [{ name: 'Engineering', slug: 'engineering', role: 'Maintainer' }];
            if (path.endsWith('/connections/8/resources')) return [{ id: 41, name: 'team/repo' }];
            if (path.endsWith('/connections/7/resources') || path.endsWith('/integrations/resources')) return [];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, 'engineering', controller.signal);
        await vi.waitFor(() => expect(el.querySelector<HTMLSelectElement>('#githubAccountPicker')).not.toBeNull());
        const account = el.querySelector<HTMLSelectElement>('#githubAccountPicker')!;
        expect(account.options.length).toBe(2);
        account.value = '8';
        account.dispatchEvent(new Event('change'));
        await vi.waitFor(() => expect(requests).toHaveBeenCalledWith('/api/admin/integrations/github/connections/8/resources', expect.anything()));
        controller.abort();
    });

    it('explains when no maintainable projects exist', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path === '/api/admin/integrations/github') return { configured: true, connections: [{ id: 7, installationId: '25', account: 'markrai', status: 'active' }] };
            if (path.endsWith('/installations')) return [];
            if (path === '/api/projects') return [{ name: 'Viewer', slug: 'viewer', role: 'Viewer' }];
            return [];
        });
        const el = root(), controller = new AbortController();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        bindGitHubSettingsTab(el, null, controller.signal);
        await vi.waitFor(() => expect(el.textContent).toContain('No projects you maintain are available'));
        expect(el.querySelector<HTMLSelectElement>('#githubProjectPicker')?.disabled).toBe(true);
        controller.abort();
    });
});


describe('GitHub event readiness', () => {
    beforeEach(() => {
        document.body.replaceChildren();
        requests.mockReset();
        toast.mockReset();
        confirm.mockReset().mockResolvedValue(true);
    });

    afterEach(() => {
        document.body.replaceChildren();
    });

    it('shows a disabled receiver even when the GitHub App is configured', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path.endsWith('/installations')) return [];
            if (path === '/api/admin/integrations/github') {
                return {
                    configured: true,
                    connections: [],
                    installUrl: '',
                    readiness: {
                        state: 'events_unavailable',
                        message: 'GitHub App configured, but incoming events are disabled.',
                        appConfigured: true,
                        webhookConfigured: false,
                        webhookPath: '/api/integrations/github/webhook',
                        externalReachability: 'unverified',
                        subscriptions: 'unverified',
                    },
                };
            }
            return [];
        });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, null, controller.signal);
        await vi.waitFor(() => expect(el.querySelector('[data-github-readiness="events_unavailable"]')).not.toBeNull());
        const details = el.querySelector<HTMLDetailsElement>('.github-integration__diagnostics');
        expect(details).not.toBeNull();
        expect(details?.open).toBe(false);
        expect(details?.querySelector('summary')?.textContent).toBe('Connection details and troubleshooting');
        expect(el.querySelectorAll('.github-integration__diagnostic-grid dt').length).toBeGreaterThanOrEqual(4);
        expect(el.textContent).toContain('SCRUMBOY_GITHUB_WEBHOOK_SECRET');
        expect(el.textContent).toContain('/api/integrations/github/webhook');
        expect(el.textContent).toContain('401');
        expect(el.textContent).toContain('Public reachability');
        expect(el.textContent).toContain('Not verified');
        controller.abort();
    });

    it('shows a verified-since-restart event without claiming the endpoint stays reachable', async () => {
        requests.mockImplementation(async (path: string) => {
            if (path.endsWith('/installations')) return [];
            if (path === '/api/admin/integrations/github') {
                return {
                    configured: true,
                    connections: [],
                    installUrl: '',
                    readiness: {
                        state: 'verified_delivery',
                        message: 'A valid signed GitHub delivery reached this process.',
                        appConfigured: true,
                        webhookConfigured: true,
                        webhookUrl: 'https://example.test/api/integrations/github/webhook',
                        lastVerifiedAt: '2026-11-22T12:00:00Z',
                    },
                };
            }
            return [];
        });
        const el = root();
        const { bindGitHubSettingsTab } = await import('./settings-github.js');
        const controller = new AbortController();
        bindGitHubSettingsTab(el, null, controller.signal);
        await vi.waitFor(() => expect(el.querySelector('[data-github-readiness="verified_delivery"]')).not.toBeNull());
        expect(el.textContent).toContain('https://example.test/api/integrations/github/webhook');
        expect(el.textContent).toContain('Last signed delivery since restart');
        expect(el.textContent).toContain('Public reachability');
        expect(el.textContent).toContain('Not verified');
        controller.abort();
    });
});

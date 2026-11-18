import { apiFetch } from '../api.js';
import { showConfirmDialog, showToast } from '../utils.js';

function textNode(parent, tag, text) {
    const item = document.createElement(tag);
    item.textContent = text;
    parent.append(item);
    return item;
}

function action(parent, label, run) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn';
    button.textContent = label;
    button.addEventListener('click', run);
    parent.append(button);
    return button;
}

export function bindGitHubSettingsTab(root, initialSlug, signal): void {
    const base = '/api/admin/integrations/github';

    async function render() {
        if (signal.aborted || !root.isConnected) return;
        root.replaceChildren();
        textNode(root, 'h3', 'GitHub App');
        const status = await apiFetch(base, { signal });
        if (signal.aborted || !root.isConnected) return;
        if (!status.configured) {
            textNode(root, 'p', 'GitHub App is not configured. The operator must set SCRUMBOY_GITHUB_APP_ID and SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE and restart Scrumboy.');
            return;
        }
        if (status.installUrl) {
            const link = textNode(root, 'a', 'Install GitHub App');
            link.setAttribute('href', status.installUrl);
            link.setAttribute('rel', 'noopener noreferrer');
            link.setAttribute('target', '_blank');
        }
        textNode(root, 'p', 'Install the App on GitHub, then connect an installation below. This sync imports repository references, not issues or card changes.');
        action(root, 'Refresh installations', () => { void reload(); });
        const installations = await apiFetch(base + '/installations', { signal });
        if (signal.aborted || !root.isConnected) return;
        const connections = Array.isArray(status.connections) ? status.connections : [];
        const installationSection = document.createElement('section');
        root.append(installationSection);
        textNode(installationSection, 'h4', 'Available installations');
        if (!installations.length) textNode(installationSection, 'p', 'No GitHub App installations found. Install the App, then refresh.');
        for (const installation of installations) {
            const row = document.createElement('div');
            row.className = 'settings-section';
            installationSection.append(row);
            textNode(row, 'span', installation.account + ' (' + installation.kind + ') ');
            const existing = connections.find(c => c.installationId === String(installation.id));
            action(row, existing ? 'Sync repositories' : 'Connect and sync', () => {
                void mutate(() => apiFetch(base + '/installations/' + installation.id + '/sync', { method: 'POST', signal }));
            });
        }
        const connectedSection = document.createElement('section');
        root.append(connectedSection);
        textNode(connectedSection, 'h4', 'Connected accounts');
        if (!connections.length) textNode(connectedSection, 'p', 'No accounts connected yet.');
        for (const connection of connections) {
            const row = document.createElement('div');
            row.className = 'settings-section';
            connectedSection.append(row);
            textNode(row, 'span', connection.account + ' (' + connection.status + ') ');
            action(row, 'Disconnect', () => {
                void (async () => {
                    const confirmed = await showConfirmDialog(
                        'Disconnecting removes all linked GitHub resource references from Scrumboy. It does not uninstall the GitHub App.',
                        'Disconnect GitHub App',
                        'Disconnect'
                    );
                    if (confirmed) await mutate(() => apiFetch(base + '/connections/' + connection.id, { method: 'DELETE', signal }));
                })();
            });
        }
        if (!connections.length) return;
        const section = document.createElement('section');
        section.className = 'settings-section';
        root.append(section);
        textNode(section, 'h4', 'Associate repositories with a project');
        textNode(section, 'p', 'Choose a project you maintain. Repository associations are separate from card references.');
        const slugInput = document.createElement('input');
        slugInput.className = 'input';
        slugInput.placeholder = 'Project slug';
        slugInput.setAttribute('aria-label', 'Project slug');
        slugInput.value = initialSlug ?? '';
        section.append(slugInput);
        const select = document.createElement('select');
        select.className = 'select';
        select.setAttribute('aria-label', 'GitHub account');
        for (const connection of connections) {
            const opt = document.createElement('option');
            opt.value = String(connection.id);
            opt.textContent = connection.account;
            select.append(opt);
        }
        section.append(select);
        const projectBody = document.createElement('div');
        section.append(projectBody);
        let sequence = 0;
        async function loadProject() {
            const stamp = ++sequence;
            const slug = slugInput.value.trim();
            const id = Number(select.value);
            projectBody.replaceChildren();
            if (!slug || !id) return;
            try {
                const [resources, associated] = await Promise.all([
                    apiFetch(base + '/connections/' + id + '/resources', { signal }),
                    apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources', { signal }),
                ]);
                if (signal.aborted || stamp !== sequence || !root.isConnected) return;
                const connectedIDs = new Set(associated.map((item) => item.id));
                const picker = document.createElement('select');
                picker.className = 'select';
                picker.setAttribute('aria-label', 'Repository');
                for (const resource of resources) {
                    if (connectedIDs.has(resource.id)) continue;
                    const opt = document.createElement('option');
                    opt.value = String(resource.id);
                    opt.textContent = resource.name;
                    picker.append(opt);
                }
                projectBody.append(picker);
                const attach = action(projectBody, 'Add to project', () => {
                    const resourceID = Number(picker.value);
                    if (!resourceID) return;
                    void mutate(async () => {
                        await apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources', {
                            method: 'POST', signal, body: JSON.stringify({ resourceId: resourceID }),
                        });
                        await loadProject();
                    }, false);
                });
                attach.disabled = !picker.options.length;
                textNode(projectBody, 'h4', 'Project repositories');
                if (!associated.length) textNode(projectBody, 'p', 'No repositories associated with this project.');
                for (const resource of associated) {
                    const row = document.createElement('div');
                    row.className = 'settings-section';
                    projectBody.append(row);
                    textNode(row, 'span', resource.name + ' ');
                    action(row, 'Remove from project', () => {
                        void mutate(async () => {
                            await apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources/' + resource.id, {
                                method: 'DELETE', signal,
                            });
                            await loadProject();
                        }, false);
                    });
                }
            } catch (error) {
                if (!signal.aborted && stamp === sequence) {
                    textNode(projectBody, 'p', 'This project may be unavailable or you may lack Maintainer access.');
                }
            }
        }
        select.addEventListener('change', () => { void loadProject(); });
        action(section, 'Load project', () => { void loadProject(); });
        if (initialSlug) void loadProject();
    }

    async function mutate(task: () => Promise<unknown>, reloadAll = true) {
        try {
            await task();
            if (reloadAll && !signal.aborted) await render();
        } catch (error) {
            if (!signal.aborted) showToast(error instanceof Error ? error.message : 'GitHub integration operation failed.');
        }
    }

    async function reload() {
        try {
            await render();
        } catch (error) {
            if (!signal.aborted) showToast(error instanceof Error ? error.message : 'Could not load GitHub integrations.');
        }
    }

    void reload();
}

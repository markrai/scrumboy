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

function appendGitHubMark(parent) {
    const mark = document.createElement('span');
    mark.className = 'github-integration__mark';
    for (const variant of ['White', 'Black']) {
        const img = document.createElement('img');
        img.className = 'github-integration__logo github-integration__logo--' + variant.toLowerCase();
        img.src = '/assets/github/GitHub_Invertocat_' + variant + '.svg';
        img.alt = '';
        img.setAttribute('aria-hidden', 'true');
        mark.append(img);
    }
    parent.append(mark);
}

export function bindGitHubSettingsTab(root, initialSlug, signal) {
    const base = '/api/admin/integrations/github';
    async function render() {
        if (signal.aborted || !root.isConnected)
            return;
        root.replaceChildren();
        const status = await apiFetch(base, { signal });
        if (signal.aborted || !root.isConnected)
            return;
        if (!status.configured) {
            const onboarding = document.createElement('section');
            onboarding.className = 'github-onboarding';
            const header = document.createElement('div');
            header.className = 'github-onboarding__header';
            const onboardingBrand = document.createElement('div');
            onboardingBrand.className = 'github-integration__brand';
            appendGitHubMark(onboardingBrand);
            textNode(onboardingBrand, 'h3', 'GitHub Integration').className = 'github-onboarding__title';
            header.append(onboardingBrand);
            const badge = textNode(header, 'span', 'Setup required');
            badge.className = 'github-onboarding__badge';
            badge.setAttribute('role', 'status');
            onboarding.append(header);
            const columns = document.createElement('div');
            columns.className = 'github-onboarding__columns';
            const overview = document.createElement('div');
            overview.className = 'github-onboarding__overview';
            textNode(overview, 'p', 'Connect GitHub repositories to Scrumboy projects and reference them from individual cards.').className = 'github-onboarding__summary';
            const steps = document.createElement('div');
            steps.className = 'github-onboarding__steps';
            for (const [number, heading, description] of [
                ['1', 'Create a GitHub App', 'Install it on selected repositories with read-only metadata access.'],
                ['2', 'Restart Scrumboy', 'After configuring the variables, return here to connect your installation.'],
            ]) {
                const step = document.createElement('div');
                step.className = 'github-onboarding__step';
                textNode(step, 'span', number).className = 'github-onboarding__step-number';
                const details = document.createElement('div');
                textNode(details, 'strong', heading).className = 'github-onboarding__step-heading';
                textNode(details, 'p', description).className = 'github-onboarding__step-description';
                step.append(details);
                steps.append(step);
            }
            overview.append(steps);
            const guide = textNode(overview, 'a', 'Setup instructions');
            guide.className = 'github-onboarding__guide';
            guide.setAttribute('href', 'https://github.com/markrai/scrumboy/blob/main/docs/github-app.md');
            guide.setAttribute('target', '_blank');
            guide.setAttribute('rel', 'noopener noreferrer');
            columns.append(overview);
            const configuration = document.createElement('div');
            configuration.className = 'github-onboarding__config';
            textNode(configuration, 'h4', 'Server configuration').className = 'github-onboarding__config-title';
            for (const item of [
                { name: 'SCRUMBOY_GITHUB_APP_ID', description: 'GitHub App ID' },
                { name: 'SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE', description: 'Server-side path to the private key' },
                { name: 'SCRUMBOY_GITHUB_APP_SLUG', description: 'Optional app slug' },
            ]) {
                const card = document.createElement('div');
                card.className = 'github-onboarding__variable';
                const row = document.createElement('div');
                row.className = 'github-onboarding__variable-row';
                textNode(row, 'code', item.name).className = 'github-onboarding__variable-name';
                const copy = document.createElement('button');
                copy.type = 'button';
                copy.className = 'github-onboarding__copy';
                copy.textContent = '⧉';
                copy.title = 'Copy ' + item.name;
                copy.setAttribute('aria-label', copy.title);
                copy.addEventListener('click', async () => {
                    try {
                        if (!navigator.clipboard?.writeText)
                            throw new Error('Clipboard unavailable');
                        await navigator.clipboard.writeText(item.name);
                        copy.textContent = '✓';
                        copy.title = 'Copied ' + item.name;
                        copy.setAttribute('aria-label', copy.title);
                    }
                    catch {
                        showToast('Clipboard unavailable. Select and copy the variable name manually.');
                    }
                });
                row.append(copy);
                card.append(row);
                textNode(card, 'p', item.description).className = 'github-onboarding__variable-description';
                configuration.append(card);
            }
            columns.append(configuration);
            onboarding.append(columns);
            root.append(onboarding);
            return;
        }

        const panel = document.createElement('section');
        panel.className = 'github-integration';
        root.append(panel);
        const header = document.createElement('header');
        header.className = 'github-integration__header';
        const brand = document.createElement('div');
        brand.className = 'github-integration__brand';
        appendGitHubMark(brand);
        const identity = document.createElement('div');
        identity.className = 'github-integration__identity';
        textNode(identity, 'h3', 'GitHub Integration').className = 'github-integration__title';
        textNode(identity, 'p', 'Connect repositories to your Scrumboy projects').className = 'github-integration__subtitle';
        brand.append(identity);
        header.append(brand);
        const badge = textNode(header, 'span', 'App configured');
        badge.className = 'github-integration__status github-integration__status--ready';
        badge.setAttribute('role', 'status');
        panel.append(header);

        const toolbar = document.createElement('div');
        toolbar.className = 'github-integration__toolbar';
        textNode(toolbar, 'h4', 'Installations').className = 'github-integration__section-title';
        const tools = document.createElement('div');
        tools.className = 'github-integration__toolbar-actions';
        if (status.installUrl) {
            const link = textNode(tools, 'a', 'Install App');
            link.className = 'github-integration__link';
            link.href = status.installUrl;
            link.rel = 'noopener noreferrer';
            link.target = '_blank';
        }
        const refresh = action(tools, 'Refresh', () => { void reload(); });
        refresh.classList.add('github-integration__refresh');
        refresh.setAttribute('aria-label', 'Refresh GitHub App installations');
        toolbar.append(tools);
        panel.append(toolbar);

        const fetchedInstallations = await apiFetch(base + '/installations', { signal });
        if (signal.aborted || !root.isConnected) return;
        const installations = Array.isArray(fetchedInstallations) ? fetchedInstallations : [];
        const connections = Array.isArray(status.connections) ? status.connections : [];
        const list = document.createElement('div');
        list.className = 'github-integration__accounts';
        panel.append(list);

        function renderConnection(account, kind, installationId, connection, discoverable) {
            const row = document.createElement('div');
            row.className = 'github-integration__account';
            const avatar = textNode(row, 'span', String(account || '?').slice(0, 1).toUpperCase());
            avatar.className = 'github-integration__avatar';
            avatar.setAttribute('aria-hidden', 'true');
            const info = document.createElement('div');
            info.className = 'github-integration__account-info';
            textNode(info, 'strong', String(account || 'Unknown account')).className = 'github-integration__account-name';
            const meta = document.createElement('div');
            meta.className = 'github-integration__account-meta';
            textNode(meta, 'span', kind === 'Organization' ? 'Organization' : kind === 'User' ? 'Personal account' : 'GitHub account');
            const connected = !!connection && connection.status === 'active';
            const state = textNode(meta, 'span', connection ? connected ? 'Connected' : 'Attention needed' : 'Not connected');
            state.className = connected ? 'github-integration__account-status github-integration__account-status--connected' : 'github-integration__account-status';
            info.append(meta);
            row.append(info);
            const controls = document.createElement('div');
            controls.className = 'github-integration__account-actions';
            if (discoverable) {
                const button = action(controls, connection ? 'Sync repositories' : 'Connect and sync', () => {
                    button.disabled = true;
                    button.setAttribute('aria-busy', 'true');
                    void mutate(() => apiFetch(base + '/installations/' + installationId + '/sync', { method: 'POST', signal })).finally(() => {
                        if (button.isConnected) {
                            button.disabled = false;
                            button.removeAttribute('aria-busy');
                        }
                    });
                });
                button.classList.add('github-integration__primary');
            }
            if (connection) {
                const more = document.createElement('details');
                more.className = 'github-integration__more';
                const summary = textNode(more, 'summary', 'More');
                summary.setAttribute('aria-label', 'More actions for ' + String(account || 'GitHub account'));
                const menu = document.createElement('div');
                menu.className = 'github-integration__menu';
                const disconnect = action(menu, 'Disconnect', () => {
                    more.open = false;
                    void (async () => {
                        const confirmed = await showConfirmDialog(
                            'Disconnecting removes all linked GitHub resource references from Scrumboy. It does not uninstall the GitHub App.',
                            'Disconnect GitHub App',
                            'Disconnect'
                        );
                        if (confirmed) await mutate(() => apiFetch(base + '/connections/' + connection.id, { method: 'DELETE', signal }));
                    })();
                });
                disconnect.classList.add('github-integration__disconnect');
                more.append(menu);
                controls.append(more);
            }
            row.append(controls);
            list.append(row);
        }

        if (!installations.length && !connections.length) {
            const empty = document.createElement('div');
            empty.className = 'github-integration__empty';
            textNode(empty, 'strong', 'No installations found');
            textNode(empty, 'p', 'Install the GitHub App on an account, then refresh to connect it here.');
            list.append(empty);
        }
        const visibleInstallations = new Set();
        for (const installation of installations) {
            const installationId = String(installation.id);
            visibleInstallations.add(installationId);
            const connection = connections.find(c => String(c.installationId) === installationId);
            renderConnection(installation.account, installation.kind, installationId, connection, true);
        }
        for (const connection of connections) {
            if (visibleInstallations.has(String(connection.installationId))) continue;
            renderConnection(connection.account, '', String(connection.installationId), connection, false);
        }

        const note = document.createElement('div');
        note.className = 'github-integration__note';
        textNode(note, 'span', 'Sync imports repository references only. GitHub issues and card content are not changed.');
        const guide = textNode(note, 'a', 'Setup guide');
        guide.href = 'https://github.com/markrai/scrumboy/blob/main/docs/github-app.md';
        guide.target = '_blank';
        guide.rel = 'noopener noreferrer';
        panel.append(note);
        if (!connections.length) return;
        const section = document.createElement('section');
        section.className = 'github-integration__project';
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
            if (!slug || !id)
                return;
            try {
                const [resources, associated] = await Promise.all([
                    apiFetch(base + '/connections/' + id + '/resources', { signal }),
                    apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources', { signal }),
                ]);
                if (signal.aborted || stamp !== sequence || !root.isConnected)
                    return;
                const connectedIDs = new Set(associated.map((item) => item.id));
                const picker = document.createElement('select');
                picker.className = 'select';
                picker.setAttribute('aria-label', 'Repository');
                for (const resource of resources) {
                    if (connectedIDs.has(resource.id))
                        continue;
                    const opt = document.createElement('option');
                    opt.value = String(resource.id);
                    opt.textContent = resource.name;
                    picker.append(opt);
                }
                projectBody.append(picker);
                const attach = action(projectBody, 'Add to project', () => {
                    const resourceID = Number(picker.value);
                    if (!resourceID)
                        return;
                    void mutate(async () => {
                        await apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources', {
                            method: 'POST', signal, body: JSON.stringify({ resourceId: resourceID }),
                        });
                        await loadProject();
                    }, false);
                });
                attach.disabled = !picker.options.length;
                textNode(projectBody, 'h4', 'Project repositories');
                if (!associated.length)
                    textNode(projectBody, 'p', 'No repositories associated with this project.');
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
            }
            catch (error) {
                if (!signal.aborted && stamp === sequence) {
                    textNode(projectBody, 'p', 'This project may be unavailable or you may lack Maintainer access.');
                }
            }
        }
        select.addEventListener('change', () => { void loadProject(); });
        action(section, 'Load project', () => { void loadProject(); });
        if (initialSlug)
            void loadProject();
    }
    async function mutate(task, reloadAll = true) {
        try {
            await task();
            if (reloadAll && !signal.aborted)
                await render();
        }
        catch (error) {
            if (!signal.aborted)
                showToast(error instanceof Error ? error.message : 'GitHub integration operation failed.');
        }
    }
    async function reload() {
        try {
            await render();
        }
        catch (error) {
            if (!signal.aborted)
                showToast(error instanceof Error ? error.message : 'Could not load GitHub integrations.');
        }
    }
    void reload();
}

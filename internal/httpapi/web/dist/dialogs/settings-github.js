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
    let selectedProjectSlug = initialSlug;
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
            link.setAttribute('href', status.installUrl);
            link.setAttribute('rel', 'noopener noreferrer');
            link.setAttribute('target', '_blank');
        }
        const refresh = action(tools, 'Refresh', () => { void reload(); });
        refresh.classList.add('github-integration__refresh');
        refresh.setAttribute('aria-label', 'Refresh GitHub App installations');
        toolbar.append(tools);
        panel.append(toolbar);
        const fetchedInstallations = await apiFetch(base + '/installations', { signal });
        if (signal.aborted || !root.isConnected)
            return;
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
                const button = action(controls, connection ? 'Refresh repositories' : 'Connect and sync', () => {
                    button.disabled = true;
                    button.setAttribute('aria-busy', 'true');
                    void mutate(() => apiFetch(base + '/installations/' + installationId + '/sync', { method: 'POST', signal })).finally(() => {
                        if (button.isConnected) {
                            button.disabled = false;
                            button.removeAttribute('aria-busy');
                        }
                    });
                });
                button.classList.add(connection ? 'github-integration__refresh-repos' : 'github-integration__primary');
                button.title = connection ? 'Refresh access after changing GitHub App repositories' : 'Connect and import repository references';
            }
            if (connection) {
                const disconnect = action(controls, 'Disconnect', () => {
                    void (async () => {
                        try {
                            const impact = await apiFetch(base + '/connections/' + connection.id + '/impact', { signal });
                            if (signal.aborted || !disconnect.isConnected)
                                return;
                            const confirmed = await showConfirmDialog('Disconnecting ' + String(account || 'this GitHub account') +
                                ' permanently removes ' + impact.repositories + ' repository records, ' +
                                impact.projectAssociations + ' project associations, and ' +
                                impact.cardReferences + ' card references from Scrumboy. It does not uninstall the GitHub App.', 'Disconnect GitHub App', 'Disconnect');
                            if (confirmed)
                                await mutate(() => apiFetch(base + '/connections/' + connection.id, {
                                    method: 'DELETE',
                                    signal,
                                    body: JSON.stringify({ confirmConnectionId: connection.id, impact }),
                                }));
                        }
                        catch (error) {
                            if (!signal.aborted)
                                showToast(error instanceof Error ? error.message : 'Could not preview disconnect impact.');
                        }
                    })();
                });
                disconnect.classList.add('github-integration__disconnect');
                disconnect.setAttribute('aria-label', 'Disconnect ' + String(account || 'GitHub account'));
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
            if (visibleInstallations.has(String(connection.installationId)))
                continue;
            renderConnection(connection.account, '', String(connection.installationId), connection, false);
        }
        const note = document.createElement('div');
        note.className = 'github-integration__note';
        textNode(note, 'span', 'Repository access changes preserve existing Scrumboy references. Unavailable repositories cannot be linked anew.');
        const guide = textNode(note, 'a', 'Setup guide');
        guide.setAttribute('href', 'https://github.com/markrai/scrumboy/blob/main/docs/github-app.md');
        guide.setAttribute('target', '_blank');
        guide.setAttribute('rel', 'noopener noreferrer');
        panel.append(note);
        if (!connections.length)
            return;
        const section = document.createElement('section');
        section.className = 'github-integration__project';
        root.append(section);
        const projectHeading = textNode(section, 'h4', 'Select a project');
        textNode(section, 'p', 'Manage repositories for this Scrumboy project. GitHub App installations are shared across projects.');
        const fields = document.createElement('div');
        fields.className = 'github-integration__project-fields';
        section.append(fields);
        const projectField = document.createElement('div');
        projectField.className = 'github-integration__field';
        textNode(projectField, 'label', 'Scrumboy project').setAttribute('for', 'githubProjectPicker');
        const projectSelect = document.createElement('select');
        projectSelect.id = 'githubProjectPicker';
        projectSelect.className = 'select';
        projectSelect.setAttribute('aria-label', 'Scrumboy project');
        const placeholder = document.createElement('option');
        placeholder.value = '';
        placeholder.textContent = 'Select a project…';
        projectSelect.append(placeholder);
        projectField.append(projectSelect);
        fields.append(projectField);
        const accountSelect = document.createElement('select');
        accountSelect.className = 'select';
        accountSelect.setAttribute('aria-label', 'GitHub account');
        for (const connection of connections) {
            const option = document.createElement('option');
            option.value = String(connection.id);
            option.textContent = String(connection.account);
            accountSelect.append(option);
        }
        if (connections.length > 1) {
            const accountField = document.createElement('div');
            accountField.className = 'github-integration__field';
            fields.append(accountField);
            textNode(accountField, 'label', 'GitHub account').setAttribute('for', 'githubAccountPicker');
            accountSelect.id = 'githubAccountPicker';
            accountField.append(accountSelect);
        }
        const projectBody = document.createElement('div');
        projectBody.className = 'github-integration__project-body';
        projectBody.setAttribute('aria-live', 'polite');
        section.append(projectBody);
        let sequence = 0;
        async function loadProject() {
            const stamp = ++sequence;
            const slug = projectSelect.value;
            const accountId = Number(accountSelect.value);
            selectedProjectSlug = slug || null;
            projectHeading.textContent = slug ? 'Project: ' + (Array.from(projectSelect.options).find(option => option.value === slug)?.textContent || slug) : 'Select a project';
            projectBody.replaceChildren();
            if (!slug || !accountId) {
                textNode(projectBody, 'p', 'Select a project to manage its GitHub repositories.').className = 'github-integration__hint';
                return;
            }
            textNode(projectBody, 'p', 'Loading repositories…').className = 'github-integration__hint';
            try {
                const [available, linked] = await Promise.all([
                    apiFetch(base + '/connections/' + accountId + '/resources', { signal }),
                    apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources', { signal }),
                ]);
                if (signal.aborted || !root.isConnected || stamp !== sequence)
                    return;
                projectBody.replaceChildren();
                const resources = Array.isArray(available) ? available : [];
                const associated = Array.isArray(linked) ? linked : [];
                const linkedIds = new Set(associated.map(resource => Number(resource.id)));
                const activeResources = resources.filter(resource => resource.availability !== 'unavailable');
                const choices = activeResources.filter(resource => !linkedIds.has(Number(resource.id)));
                const list = document.createElement('div');
                list.className = 'github-integration__linked-list';
                textNode(list, 'h5', 'Linked Repositories');
                if (!associated.length)
                    textNode(list, 'p', 'No repositories linked to this project yet.').className = 'github-integration__hint';
                for (const resource of associated) {
                    const row = document.createElement('div');
                    row.className = 'github-integration__linked-row';
                    textNode(row, 'span', String(resource.name) + (resource.availability === 'unavailable' ? ' (Unavailable)' : ''));
                    const remove = action(row, 'Remove', () => {
                        remove.disabled = true;
                        void mutate(async () => {
                            await apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources/' + resource.id, { method: 'DELETE', signal });
                            await loadProject();
                        }, false).finally(() => { if (remove.isConnected)
                            remove.disabled = false; });
                    });
                    remove.classList.add('github-integration__remove-repo');
                    list.append(row);
                }
                projectBody.append(list);
                textNode(projectBody, 'p', 'Linked repositories can be referenced from cards in this project.').className = 'github-integration__hint';
                const addSection = document.createElement('section');
                addSection.className = 'github-integration__add-section';
                textNode(addSection, 'h5', 'Add Repositories');
                if (choices.length) {
                    const addFields = document.createElement('div');
                    addFields.className = 'github-integration__repository-fields';
                    const repositoryField = document.createElement('div');
                    repositoryField.className = 'github-integration__field';
                    textNode(repositoryField, 'label', 'GitHub repository').setAttribute('for', 'githubRepositoryPicker');
                    const repositorySelect = document.createElement('select');
                    repositorySelect.className = 'select';
                    repositorySelect.id = 'githubRepositoryPicker';
                    repositorySelect.setAttribute('aria-label', 'GitHub repository');
                    for (const resource of choices) {
                        const option = document.createElement('option');
                        option.value = String(resource.id);
                        option.textContent = String(resource.name);
                        repositorySelect.append(option);
                    }
                    repositorySelect.disabled = choices.length === 0;
                    repositoryField.append(repositorySelect);
                    addFields.append(repositoryField);
                    const add = action(addFields, 'Add to project', () => {
                        const resourceId = Number(repositorySelect.value);
                        if (!resourceId)
                            return;
                        add.disabled = true;
                        void mutate(async () => {
                            await apiFetch('/api/board/' + encodeURIComponent(slug) + '/integrations/resources', {
                                method: 'POST', signal, body: JSON.stringify({ resourceId }),
                            });
                            await loadProject();
                        }, false).finally(() => { if (add.isConnected)
                            add.disabled = false; });
                    });
                    add.classList.add('github-integration__add-repo');
                    add.disabled = choices.length === 0;
                    addSection.append(addFields);
                }
                else {
                    textNode(addSection, 'p', activeResources.length ? 'All available repositories are linked to this project.' : 'No repositories available. Try Refresh repositories if GitHub access has changed.').className = 'github-integration__hint';
                }
                projectBody.append(addSection);
            }
            catch (error) {
                if (!signal.aborted && root.isConnected && stamp === sequence) {
                    projectBody.replaceChildren();
                    textNode(projectBody, 'p', 'Unable to load repositories. Check Maintainer access and try again.').className = 'github-integration__project-error';
                }
            }
        }
        projectSelect.addEventListener('change', () => { void loadProject(); });
        accountSelect.addEventListener('change', () => { void loadProject(); });
        try {
            const projectsResponse = await apiFetch('/api/projects', { signal });
            if (signal.aborted || !root.isConnected)
                return;
            const projects = Array.isArray(projectsResponse) ? projectsResponse : [];
            const eligible = projects.filter(project => project && String(project.role || '').toLowerCase() === 'maintainer' && !project.expiresAt && typeof project.slug === 'string' && project.slug);
            eligible.sort((a, b) => String(a.name).localeCompare(String(b.name)));
            for (const project of eligible) {
                const option = document.createElement('option');
                option.value = project.slug;
                option.textContent = String(project.name);
                projectSelect.append(option);
            }
            if (!eligible.length) {
                projectSelect.disabled = true;
                textNode(projectBody, 'p', 'No projects you maintain are available. Create a Scrumboy project or ask for Maintainer access.').className = 'github-integration__hint';
                return;
            }
            if (selectedProjectSlug && eligible.some(project => project.slug === selectedProjectSlug))
                projectSelect.value = selectedProjectSlug;
            else if (eligible.length === 1)
                projectSelect.value = eligible[0].slug;
            await loadProject();
        }
        catch (error) {
            if (!signal.aborted && root.isConnected) {
                projectBody.replaceChildren();
                textNode(projectBody, 'p', 'Could not load projects. Reopen Settings to try again.').className = 'github-integration__project-error';
            }
        }
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

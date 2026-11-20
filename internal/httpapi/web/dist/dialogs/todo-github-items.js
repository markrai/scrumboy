import { apiFetch } from '../api.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { showToast } from '../utils.js';
import { apiErrorMessage } from '../i18n/index.js';
let generation = 0;
let activeBrowse = null;
let browseTimer = null;
const pendingURLs = new Map();
function add(parent, tag, label, className) {
    const node = document.createElement(tag);
    if (label !== undefined)
        node.textContent = label;
    if (className)
        node.className = className;
    parent.append(node);
    return node;
}
function button(parent, label, perform) {
    const control = add(parent, 'button', label, 'btn');
    control.type = 'button';
    control.addEventListener('click', async () => {
        control.disabled = true;
        try {
            await perform();
        }
        catch (err) {
            showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
        }
        finally {
            control.disabled = false;
        }
    });
    return control;
}
function select(parent, aria, options) {
    const element = add(parent, 'select', undefined, 'select');
    element.setAttribute('aria-label', aria);
    for (const [value, label] of options) {
        const option = document.createElement('option');
        option.value = value;
        option.textContent = label;
        element.append(option);
    }
    return element;
}
function canonicalURL(value, available) {
    try {
        const parsed = new URL(value.trim());
        if (parsed.protocol !== 'https:' || parsed.hostname.toLowerCase() !== 'github.com' ||
            parsed.port || parsed.username || parsed.password || parsed.search || parsed.hash)
            return null;
        const parts = parsed.pathname.split('/').filter(Boolean);
        if (parts.length !== 4 || !/^[a-zA-Z0-9_.-]+$/.test(parts[0]) ||
            !/^[a-zA-Z0-9_.-]+$/.test(parts[1]) || !['issues', 'pull'].includes(parts[2]) ||
            !/^[1-9][0-9]*$/.test(parts[3]) || Number(parts[3]) > 2147483647)
            return null;
        const repository = parts[0] + '/' + parts[1];
        if (!available.some(item => item.name.toLowerCase() === repository.toLowerCase()))
            return null;
        return 'https://github.com/' + repository + '/' + parts[2] + '/' + parts[3];
    }
    catch {
        return null;
    }
}
export function resetTodoGitHubItems() {
    generation++;
    if (browseTimer !== null)
        clearTimeout(browseTimer);
    browseTimer = null;
    activeBrowse?.abort();
    activeBrowse = null;
    pendingURLs.clear();
    document.getElementById('todoGitHubItems')?.remove();
}
export function getPendingTodoGitHubURLs() {
    return [...pendingURLs.keys()];
}
export async function commitPendingTodoGitHubItems(slug, localId) {
    const path = '/api/board/' + encodeURIComponent(slug) + '/todos/' + localId + '/github-items';
    let failures = 0;
    for (const url of pendingURLs.keys()) {
        try {
            recordLocalMutation();
            await apiFetch(path, { method: 'POST', body: JSON.stringify({ url }) });
        }
        catch {
            failures++;
        }
    }
    return failures;
}
export async function initializeTodoGitHubItems(slug, localId, editable) {
    resetTodoGitHubItems();
    const request = generation;
    const field = document.getElementById('todoExternalResourcesField');
    if (!field)
        return;
    const section = add(field, 'section', undefined, 'github-items');
    section.id = 'todoGitHubItems';
    const base = '/api/board/' + encodeURIComponent(slug);
    const path = localId === null ? null : base + '/todos/' + localId + '/github-items';
    const [resources, existing] = await Promise.all([
        apiFetch(base + '/integrations/resources'),
        path ? apiFetch(path) : Promise.resolve([]),
    ]);
    if (request !== generation || !section.isConnected)
        return;
    const repositories = (Array.isArray(resources) ? resources : [])
        .filter(resource => resource.kind === 'repository' && resource.availability !== 'unavailable');
    let linked = Array.isArray(existing) ? existing : [];
    add(section, 'h5', 'GitHub Issues & Pull Requests');
    const linkedHost = add(section, 'div', undefined, 'github-items__results');
    async function reloadLinked() {
        if (!path)
            return;
        const items = await apiFetch(path);
        if (request !== generation || !section.isConnected)
            return;
        linked = Array.isArray(items) ? items : [];
        renderLinked();
        renderResults();
    }
    async function linkURL(value) {
        if (!editable)
            return;
        const canonical = canonicalURL(value, repositories);
        if (!canonical) {
            showToast('Enter a canonical GitHub issue or pull request URL from a linked repository.');
            return;
        }
        if (localId === null) {
            pendingURLs.set(canonical, canonical);
            renderLinked();
            renderResults();
            return;
        }
        recordLocalMutation();
        await apiFetch(path, { method: 'POST', body: JSON.stringify({ url: canonical }) });
        await reloadLinked();
    }
    function renderLinked() {
        if (request !== generation || !section.isConnected)
            return;
        linkedHost.replaceChildren();
        add(linkedHost, 'div', localId === null ? 'Selected for this Todo' : 'Linked references', 'github-items__heading');
        if (localId === null) {
            if (!pendingURLs.size)
                add(linkedHost, 'p', 'No references selected yet. Selections are attached when you save the Todo.', 'muted');
            for (const url of pendingURLs.keys()) {
                const row = add(linkedHost, 'div', undefined, 'github-items__item');
                const anchor = add(row, 'a', url);
                anchor.href = url;
                anchor.target = '_blank';
                anchor.rel = 'noopener noreferrer';
                button(row, 'Remove', () => {
                    pendingURLs.delete(url);
                    renderLinked();
                    renderResults();
                });
            }
            return;
        }
        if (!linked.length)
            add(linkedHost, 'p', 'No GitHub issues or pull requests linked.', 'muted');
        for (const item of linked) {
            const row = add(linkedHost, 'div', undefined, 'github-items__item');
            const anchor = add(row, 'a', (item.kind === 'pr' ? 'PR #' : 'Issue #') + item.number + ' · ' + item.title);
            if (canonicalURL(item.url, [{ id: item.resourceId, kind: 'repository', name: item.repository }])) {
                anchor.href = item.url;
                anchor.target = '_blank';
                anchor.rel = 'noopener noreferrer';
            }
            add(row, 'span', item.repository + ' · ' +
                (item.merged ? 'Merged' : item.draft ? 'Draft' : item.state === 'open' ? 'Open' : 'Closed') +
                (item.availability === 'unavailable' ? ' · Unavailable' : ''), 'github-items__state');
            if (!editable)
                continue;
            button(row, 'Refresh', async () => {
                recordLocalMutation();
                await apiFetch(path + '/' + item.resourceId + '/' + item.kind + '/' + item.number + '/refresh', { method: 'POST' });
                await reloadLinked();
            });
            button(row, 'Remove', async () => {
                recordLocalMutation();
                await apiFetch(path + '/' + item.resourceId + '/' + item.kind + '/' + item.number, { method: 'DELETE' });
                await reloadLinked();
            });
        }
    }
    renderLinked();
    if (!editable)
        return;
    if (!repositories.length) {
        add(section, 'p', 'No available GitHub repositories are linked to this project. Link one in Settings → GitHub.', 'muted');
        return;
    }
    const paste = add(section, 'div', undefined, 'github-items__row');
    const urlInput = add(paste, 'input', undefined, 'input');
    urlInput.type = 'text';
    urlInput.inputMode = 'url';
    urlInput.maxLength = 2048;
    urlInput.placeholder = 'Paste a GitHub issue or PR URL';
    urlInput.setAttribute('aria-label', 'GitHub issue or PR URL');
    urlInput.addEventListener('keydown', event => {
        if (event.key === 'Enter')
            event.preventDefault();
    });
    button(paste, 'Link URL', async () => {
        if (!urlInput.value.trim())
            return;
        await linkURL(urlInput.value);
        urlInput.value = '';
    });
    add(section, 'div', 'Browse repository issues and pull requests', 'github-items__heading');
    const selectors = add(section, 'div', undefined, 'github-items__browse');
    const repoPicker = select(selectors, 'GitHub repository to browse', repositories.map(repo => [String(repo.id), repo.name]));
    const statePicker = select(selectors, 'GitHub item state', [['open', 'Open'], ['closed', 'Closed'], ['all', 'All']]);
    const filters = add(section, 'div', undefined, 'github-items__filters');
    const kindPicker = select(filters, 'GitHub reference type', [['pr', 'Pull requests'], ['issue', 'Issues']]);
    const input = add(filters, 'input', undefined, 'input');
    input.type = 'search';
    input.maxLength = 100;
    input.placeholder = 'Filter by title…';
    input.setAttribute('aria-label', 'Filter GitHub issues or pull requests');
    input.addEventListener('keydown', event => {
        if (event.key === 'Enter')
            event.preventDefault();
    });
    const results = add(section, 'div', undefined, 'github-items__results');
    const cached = new Map();
    let hits = [];
    let sequence = 0;
    function renderResults() {
        if (request !== generation || !results.isConnected)
            return;
        results.replaceChildren();
        const query = input.value.trim().toLowerCase();
        const visible = query.length === 1 ? hits.filter(item => item.title.toLowerCase().includes(query)) : hits;
        if (!visible.length) {
            add(results, 'p', query.length === 1 ? 'Enter another character to search all titles.' :
                'No matching GitHub items in the first 20 results.', 'muted github-items__empty');
        }
        for (const hit of visible) {
            const row = add(results, 'div', undefined, 'github-items__item');
            const anchor = add(row, 'a', '#' + hit.number + ' · ' + hit.title);
            const safe = canonicalURL(hit.url, repositories);
            if (safe) {
                anchor.href = safe;
                anchor.target = '_blank';
                anchor.rel = 'noopener noreferrer';
            }
            add(row, 'span', hit.state === 'open' ? 'Open' : 'Closed', 'github-items__state');
            const alreadyLinked = linked.some(item => item.url.toLowerCase() === hit.url.toLowerCase()) || pendingURLs.has(hit.url);
            if (alreadyLinked) {
                add(row, 'span', 'Linked', 'github-items__state');
            }
            else if (safe) {
                button(row, 'Link', () => linkURL(safe));
            }
        }
        if (query.length < 2)
            add(results, 'p', 'Showing at most 20 recently updated results. Type two or more characters to search titles.', 'muted');
    }
    async function browse() {
        const current = ++sequence;
        activeBrowse?.abort();
        activeBrowse = new AbortController();
        if (browseTimer !== null)
            clearTimeout(browseTimer);
        browseTimer = null;
        if (request !== generation || !section.isConnected)
            return;
        const query = input.value.trim();
        if (query.length === 1) {
            renderResults();
            return;
        }
        const params = new URLSearchParams({
            resourceId: repoPicker.value,
            kind: kindPicker.value,
            state: statePicker.value,
        });
        if (query)
            params.set('q', query);
        const key = params.toString();
        const entry = cached.get(key);
        if (entry && Date.now() - entry.at < 30000) {
            hits = entry.items;
            renderResults();
            return;
        }
        results.replaceChildren();
        add(results, 'p', 'Loading GitHub items…', 'muted').setAttribute('role', 'status');
        try {
            const data = await apiFetch(base + '/integrations/github/browse?' + key, { signal: activeBrowse.signal });
            if (request !== generation || current !== sequence || !section.isConnected)
                return;
            hits = Array.isArray(data.items) ? data.items.slice(0, 20) : [];
            cached.set(key, { items: hits, at: Date.now() });
            renderResults();
        }
        catch (err) {
            if (request !== generation || current !== sequence || !section.isConnected)
                return;
            results.replaceChildren();
            add(results, 'p', 'Could not load GitHub items. Change filters or reopen References to retry.', 'muted').setAttribute('role', 'alert');
        }
    }
    repoPicker.addEventListener('change', () => { void browse(); });
    kindPicker.addEventListener('change', () => { void browse(); });
    statePicker.addEventListener('change', () => { void browse(); });
    input.addEventListener('input', () => {
        ++sequence;
        activeBrowse?.abort();
        if (browseTimer !== null)
            clearTimeout(browseTimer);
        browseTimer = setTimeout(() => { void browse(); }, 350);
        if (input.value.trim().length === 1)
            renderResults();
    });
    await browse();
}

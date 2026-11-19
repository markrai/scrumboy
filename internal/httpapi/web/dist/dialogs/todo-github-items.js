import { apiFetch } from '../api.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { showToast } from '../utils.js';
import { apiErrorMessage } from '../i18n/index.js';
let generation = 0;
function add(parent, tag, text) {
    const node = document.createElement(tag);
    if (text !== undefined)
        node.textContent = text;
    parent.append(node);
    return node;
}
function action(parent, name, perform) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn';
    button.textContent = name;
    button.onclick = async () => {
        button.disabled = true;
        try {
            await perform();
        }
        catch (error) {
            showToast(apiErrorMessage(error, { fallbackKey: 'todo.externalResources.failed' }));
        }
        finally {
            button.disabled = false;
        }
    };
    parent.append(button);
    return button;
}
export function resetTodoGitHubItems() {
    ++generation;
    document.getElementById('todoGitHubItems')?.remove();
}
export async function initializeTodoGitHubItems(slug, localId, editable) {
    const field = document.getElementById('todoExternalResourcesField');
    if (!field)
        return;
    resetTodoGitHubItems();
    const section = add(field, 'section');
    section.id = 'todoGitHubItems';
    section.className = 'github-items';
    await reload(slug, localId, editable, section);
}
async function reload(slug, localId, editable, section) {
    const request = ++generation;
    const base = '/api/board/' + encodeURIComponent(slug);
    const path = base + '/todos/' + localId + '/github-items';
    const [linked, projects] = await Promise.all([
        apiFetch(path),
        apiFetch(base + '/integrations/resources'),
    ]);
    if (generation !== request || !section.isConnected)
        return;
    section.replaceChildren();
    add(section, 'h5', 'GitHub Issues & Pull Requests');
    const items = Array.isArray(linked) ? linked : [];
    const available = Array.isArray(projects) ? projects.filter(r => r.kind === 'repository' && r.availability !== 'unavailable') : [];
    const list = add(section, 'div');
    for (const item of items) {
        const entry = add(list, 'div');
        entry.className = 'github-items__item';
        const anchor = add(entry, 'a', (item.kind === 'pr' ? 'PR #' : 'Issue #') + item.number + ' · ' + item.title);
        anchor.href = item.url;
        anchor.rel = 'noopener noreferrer';
        anchor.target = '_blank';
        add(entry, 'span', item.repository + ' · ' + (item.merged ? 'Merged' : item.draft ? 'Draft' : item.state === 'open' ? 'Open' : 'Closed') + (item.availability === 'unavailable' ? ' · Unavailable' : '')).className = 'github-items__state';
        if (!editable)
            continue;
        action(entry, 'Refresh', async () => {
            recordLocalMutation();
            await apiFetch(path + '/' + item.resourceId + '/' + item.kind + '/' + item.number + '/refresh', { method: 'POST' });
            await reload(slug, localId, editable, section);
        });
        action(entry, 'Remove', async () => {
            recordLocalMutation();
            await apiFetch(path + '/' + item.resourceId + '/' + item.kind + '/' + item.number, { method: 'DELETE' });
            await reload(slug, localId, editable, section);
        });
    }
    if (!items.length)
        add(list, 'p', 'No GitHub issues or pull requests linked.').className = 'muted';
    if (!editable)
        return;
    if (!available.length) {
        add(section, 'p', 'Add repositories to this project in Settings → GitHub before linking issues and PRs.').className = 'muted';
        return;
    }
    const paste = add(section, 'div');
    paste.className = 'github-items__row';
    const urlInput = document.createElement('input');
    urlInput.className = 'input';
    urlInput.type = 'url';
    urlInput.maxLength = 2048;
    urlInput.placeholder = 'Paste a GitHub issue or PR URL';
    urlInput.setAttribute('aria-label', 'GitHub issue or PR URL');
    paste.append(urlInput);
    action(paste, 'Link URL', async () => {
        if (!urlInput.value.trim())
            return;
        recordLocalMutation();
        await apiFetch(path, { method: 'POST', body: JSON.stringify({ url: urlInput.value.trim() }) });
        await reload(slug, localId, editable, section);
    });
    add(section, 'p', 'Or search linked repositories:').className = 'muted';
    const search = add(section, 'div');
    search.className = 'github-items__row';
    const repoPicker = document.createElement('select');
    repoPicker.className = 'select';
    repoPicker.setAttribute('aria-label', 'GitHub repository to search');
    for (const repository of available) {
        const option = document.createElement('option');
        option.value = String(repository.id);
        option.textContent = repository.name;
        repoPicker.append(option);
    }
    search.append(repoPicker);
    const kindPicker = document.createElement('select');
    kindPicker.className = 'select';
    kindPicker.setAttribute('aria-label', 'GitHub reference type');
    for (const [value, label] of [['issue', 'Issues'], ['pr', 'Pull requests']]) {
        const option = document.createElement('option');
        option.value = value;
        option.textContent = label;
        kindPicker.append(option);
    }
    search.append(kindPicker);
    const input = document.createElement('input');
    input.className = 'input';
    input.type = 'search';
    input.placeholder = 'Search title…';
    input.maxLength = 100;
    input.setAttribute('aria-label', 'Search issue or PR title');
    search.append(input);
    const results = add(section, 'div');
    let lastSearch = 0;
    const searchButton = action(search, 'Search', async () => {
        const q = input.value.trim();
        if (q.length < 2) {
            showToast('Enter at least two characters to search.');
            return;
        }
        const current = ++lastSearch;
        results.replaceChildren();
        add(results, 'p', 'Searching…').className = 'muted';
        const params = new URLSearchParams({ resourceId: repoPicker.value, kind: kindPicker.value, q });
        const result = await apiFetch(base + '/integrations/github/search?' + params.toString());
        if (request !== generation || current !== lastSearch || !results.isConnected)
            return;
        results.replaceChildren();
        const hits = Array.isArray(result.items) ? result.items : [];
        if (!hits.length)
            add(results, 'p', 'No matching issues or PRs.').className = 'muted';
        for (const hit of hits) {
            const row = add(results, 'div');
            row.className = 'github-items__item';
            const a = add(row, 'a', '#' + hit.number + ' · ' + hit.title);
            a.href = hit.url;
            a.target = '_blank';
            a.rel = 'noopener noreferrer';
            action(row, 'Link', async () => {
                recordLocalMutation();
                await apiFetch(path, { method: 'POST', body: JSON.stringify({ url: hit.url }) });
                await reload(slug, localId, editable, section);
            });
        }
    });
    input.addEventListener('keydown', event => {
        if (event.key === 'Enter') {
            event.preventDefault();
            searchButton.click();
        }
    });
}

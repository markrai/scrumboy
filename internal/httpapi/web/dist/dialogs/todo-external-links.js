import { apiFetch } from '../api.js';
import { initializeTodoGitHubItems, resetTodoGitHubItems } from './todo-github-items.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import { showToast } from '../utils.js';
import { getTodoFormPermissions } from './todo-permissions.js';
let generation = 0;
let lifecycleBound = false;
function renderExternalResources(slug, localId, linked, available, editable) {
    const container = document.getElementById('externalResourcesChips');
    const controls = document.getElementById('externalResourceControls');
    const picker = document.getElementById('externalResourcePicker');
    const add = document.getElementById('addExternalResourceBtn');
    if (!container || !controls || !picker || !add)
        return;
    container.replaceChildren();
    const linkedIds = new Set(linked.map(item => item.id));
    for (const item of linked) {
        const chip = document.createElement('span');
        chip.className = 'tag-chip';
        const anchor = document.createElement('a');
        anchor.className = 'tag-chip-link';
        anchor.href = item.url;
        anchor.target = '_blank';
        anchor.rel = 'noopener noreferrer';
        anchor.textContent = item.name;
        chip.append(anchor);
        if (item.availability === 'unavailable') {
            chip.classList.add('tag-chip--unavailable');
            const marker = document.createElement('span');
            marker.className = 'tag-chip-unavailable';
            marker.textContent = ' (Unavailable)';
            marker.title = 'Repository is no longer accessible through the GitHub App';
            chip.append(marker);
        }
        if (editable) {
            const remove = document.createElement('button');
            remove.type = 'button';
            remove.className = 'tag-chip-remove';
            remove.textContent = '×';
            remove.setAttribute('aria-label', t('todo.externalResources.remove'));
            remove.addEventListener('click', async () => {
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${encodeURIComponent(slug)}/todos/${localId}/integrations/${item.id}`, { method: 'DELETE' });
                    await loadExternalResources(slug, localId, editable);
                }
                catch (err) {
                    showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
                }
            });
            chip.append(remove);
        }
        container.append(chip);
    }
    picker.replaceChildren();
    for (const item of available.filter(resource => !linkedIds.has(resource.id) && resource.availability !== 'unavailable')) {
        const option = document.createElement('option');
        option.value = String(item.id);
        option.textContent = item.name;
        picker.append(option);
    }
    const canAdd = editable && picker.options.length > 0;
    controls.hidden = !canAdd;
    picker.disabled = !canAdd;
    add.disabled = !canAdd;
    add.onclick = async () => {
        const id = Number(picker.value);
        if (!id)
            return;
        try {
            recordLocalMutation();
            await apiFetch(`/api/board/${encodeURIComponent(slug)}/todos/${localId}/integrations`, {
                method: 'POST',
                body: JSON.stringify({ resourceId: id }),
            });
            await loadExternalResources(slug, localId, editable);
        }
        catch (err) {
            showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
        }
    };
    if (linked.length === 0) {
        const empty = document.createElement('span');
        empty.className = 'muted';
        empty.textContent = t('todo.externalResources.empty');
        container.append(empty);
    }
}
async function loadExternalResources(slug, localId, editable) {
    const request = ++generation;
    const path = `/api/board/${encodeURIComponent(slug)}`;
    const [linked, available] = await Promise.all([
        apiFetch(`${path}/todos/${localId}/integrations`),
        apiFetch(`${path}/integrations/resources`),
    ]);
    if (request !== generation)
        return;
    renderExternalResources(slug, localId, Array.isArray(linked) ? linked : [], Array.isArray(available) ? available : [], editable);
}
export async function initializeTodoExternalLinks(slug, localId, archived) {
    const field = document.getElementById('todoExternalResourcesField');
    if (!field)
        return;
    field.style.display = '';
    const editable = !archived && getTodoFormPermissions().canEditLinks;
    await loadExternalResources(slug, localId, editable);
    void initializeTodoGitHubItems(slug, localId, editable).catch(err => {
        resetTodoGitHubItems();
        showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
    });
}
export function resetTodoExternalLinks() {
    resetTodoGitHubItems();
    ++generation;
    const field = document.getElementById('todoExternalResourcesField');
    if (field)
        field.style.display = 'none';
    document.getElementById('externalResourcesChips')?.replaceChildren();
    const add = document.getElementById('addExternalResourceBtn');
    if (add)
        add.onclick = null;
}
export function bindTodoExternalLinksLifecycle() {
    if (lifecycleBound)
        return;
    const dialog = document.getElementById('todoDialog');
    if (!dialog)
        return;
    lifecycleBound = true;
    dialog.addEventListener('close', resetTodoExternalLinks);
}
